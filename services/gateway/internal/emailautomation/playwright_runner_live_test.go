package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestPlaywrightExtensionLiveEmailProbes(t *testing.T) {
	providerList := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_PROVIDERS"))
	if providerList == "" {
		t.Skip("set SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_PROVIDERS to a comma-separated provider list to run live login probes")
	}
	configPath := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_CONFIG"))
	if configPath == "" {
		configPath = filepath.Join("..", "..", "..", "..", "configs", "sparkclaw.default.json")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load live config: %v", err)
	}
	runtime, err := newPlaywrightEmailLiveStoreRuntime(ctx, cfg)
	if err != nil {
		t.Fatalf("open live state backend: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := runtime.Close(closeCtx); err != nil {
			t.Errorf("close live state backend: %v", err)
		}
	})

	vault := credential.New(runtime.CredentialRepository(), credential.Options{
		Key: cfg.State.CredentialKey, KeyFile: cfg.State.CredentialKeyFile,
	})
	if err := vault.Ready(); err != nil {
		t.Fatalf("open live credential vault: %v", err)
	}
	extensionCfg := cfg.Adapters.BrowserAutomation.PlaywrightExtension
	client, err := browsercontrol.NewHTTPControllerClient(
		extensionCfg.ControllerSocket,
		time.Duration(extensionCfg.ConnectTimeoutMS)*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("create live controller client: %v", err)
	}
	t.Cleanup(client.Close)
	controller := browsercontrol.New(vault, client, extensionCfg.ProfileID)
	controller.Initialize(ctx)
	status, err := controller.Check(ctx)
	if err != nil {
		t.Fatalf(
			"validate saved extension credential: %v (code=%s retryable=%t cause=%v)",
			err, browsercontrol.ErrorCode(err), browsercontrol.ErrorRetryable(err), errors.Unwrap(err),
		)
	}
	if !status.Configured || status.CredentialGeneration <= 0 {
		t.Fatalf("live browser credential status is invalid: %#v", status)
	}
	t.Cleanup(func() {
		if err := controller.Close(); err != nil {
			t.Errorf("close live browser controller: %v", err)
		}
	})

	registry := DefaultRegistry()
	liveController := &liveEmailController{Service: controller, t: t}
	runner := NewPlaywrightRunner(liveController)
	seen := map[string]bool{}
	for _, rawProvider := range strings.Split(providerList, ",") {
		providerID := strings.ToLower(strings.TrimSpace(rawProvider))
		provider, ok := registry.Get(providerID)
		if !ok || seen[providerID] {
			t.Fatalf("invalid or duplicate live email provider %q", rawProvider)
		}
		seen[providerID] = true
		t.Run(providerID, func(t *testing.T) {
			probe, err := runner.Probe(ctx, provider, "playwright-live-probe-"+providerID, uint64(status.CredentialGeneration))
			if err != nil {
				t.Fatalf("live login probe: %v (code=%s)", err, ErrorCode(err))
			}
			if probe.Provider != provider.ID || probe.Generation != uint64(status.CredentialGeneration) ||
				probe.Revision != provider.Probe.Revision || probe.CheckedAt.IsZero() || !validAccountHint(probe.AccountHint) {
				t.Fatalf("live login probe result is invalid: %#v", probe)
			}
			if operation := os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_INTAKE"); operation != "" {
				qualifyPinnedIntake(t, ctx, runner, provider, probe, operation)
			}
			if os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_DISCOVER") == "1" {
				digest := sha256.Sum256([]byte("email-management-qualification"))
				request := ReadRequest{Provider: provider.ID, Account: app.EmailAccountDefault, OwnerScope: hex.EncodeToString(digest[:]), InvocationID: app.NewID("email_live_discovery"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Discover.Revision}
				result, err := runner.Discover(ctx, provider, request)
				if err != nil {
					t.Fatalf("live discovery: %v (code=%s)", err, ErrorCode(err))
				}
				t.Logf("live discovery: provider=%s candidates=%d complete=%t qualified=%t reason=%s", provider.ID, len(result.Candidates), result.Coverage.ScanComplete, result.Coverage.BoundaryQualified, result.Coverage.Reason)
				if os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_TIME_RANGE") == "1" {
					end := time.Now().UTC()
					request.InvocationID = app.NewID("email_live_interval")
					request.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: result.AccountAddress, IntervalStart: end.Add(-time.Hour), IntervalEnd: end, Limit: 50, ProviderMode: "time_range"}
					if raw := os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_INTERVAL_START"); raw != "" {
						start, parseErr := time.Parse(time.RFC3339Nano, raw)
						if parseErr != nil || !start.Before(end) {
							t.Fatal("invalid isolated live interval")
						}
						request.Discovery.IntervalStart = start
					}
					started := time.Now()
					interval, err := runner.Discover(ctx, provider, request)
					if err != nil {
						t.Fatalf("live time range: code=%s elapsed=%s", ErrorCode(err), time.Since(started))
					}
					t.Logf("live time range: provider=%s candidates=%d complete=%t qualified=%t reason=%s elapsed=%s", provider.ID, len(interval.Candidates), interval.Coverage.ScanComplete, interval.Coverage.BoundaryQualified, interval.Coverage.Reason, time.Since(started))
				}
			}
			if os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_READ") == "1" {
				ownerID := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_EMAIL_OWNER_ID"))
				if ownerID == "" {
					t.Fatal("SPARKCLAW_TEST_EMAIL_OWNER_ID is required for live capture")
				}
				ownerDigest := sha256.Sum256([]byte(ownerID))
				invocationID := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_EMAIL_INVOCATION_ID"))
				if invocationID == "" {
					invocationID = app.NewID("email_live_read")
				}
				request := ReadRequest{
					Provider: provider.ID, Account: app.EmailAccountDefault, OwnerScope: hex.EncodeToString(ownerDigest[:]),
					InvocationID: invocationID, BrowserCredentialGeneration: probe.Generation,
					ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Read.Revision,
				}
				result, err := runner.Read(ctx, provider, request)
				if err != nil {
					t.Fatalf("live read failed: %v (code=%s)", err, ErrorCode(err))
				}
				if result.Status != "empty" && result.Capture == nil {
					t.Fatal("live read returned an invalid envelope")
				}
				if result.Capture != nil {
					workspaceRoot := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_EMAIL_WORKSPACE_ROOT"))
					if err := verifyCapture(ctx, workspaceRoot, request, result); err != nil {
						t.Fatal("live capture files could not be verified in the host workspace")
					}
					t.Logf("live capture succeeded: provider=%s status=%s attachments=%d read_state=%s files_verified=true", provider.ID, result.Status, result.Capture.AttachmentsCount, result.Capture.ReadState)
				} else {
					t.Logf("live capture succeeded: provider=%s status=%s captured=false", provider.ID, result.Status)
				}
			}
			if os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_MARK_READ") == "1" {
				qualifyLatestCapturedMarkRead(t, ctx, runtime.EmailRepository(), runner, provider, probe)
			}
		})
	}
	if len(seen) == 0 {
		t.Fatal("live email provider list is empty")
	}
}

// qualifyLatestCapturedMarkRead performs one explicit provider mutation. It is
// opt-in because a successful run changes the selected server-side message to
// read. The committed source remains the immutable identity anchor.
func qualifyLatestCapturedMarkRead(t *testing.T, ctx context.Context, repository store.EmailRepository, runner *PlaywrightRunner, provider Provider, probe ProbeResult) {
	t.Helper()
	if provider.ID != app.EmailProviderQQMail {
		t.Fatal("live mark-read qualification currently supports QQ Mail only")
	}
	ownerID := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_EMAIL_OWNER_ID"))
	if ownerID == "" {
		t.Fatal("SPARKCLAW_TEST_EMAIL_OWNER_ID is required for live mark-read qualification")
	}
	mailboxes, err := repository.ListEmailMailboxes(ctx, ownerID)
	if err != nil {
		t.Fatalf("list live mailboxes: %v", err)
	}
	var mailbox app.EmailMailbox
	for _, candidate := range mailboxes {
		if candidate.Provider == provider.ID {
			mailbox = candidate
			break
		}
	}
	if mailbox.ID == "" {
		t.Fatal("QQ mailbox is not configured")
	}
	mails, err := repository.ListEmailMails(ctx, store.EmailQuery{OwnerID: ownerID, MailboxID: mailbox.ID, CapturedOnly: true, Limit: 50})
	if err != nil {
		t.Fatalf("list captured QQ mail: %v", err)
	}
	candidateIndex := 0
	if raw := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_EMAIL_MARK_READ_INDEX")); raw != "" {
		candidateIndex, err = strconv.Atoi(raw)
		if err != nil || candidateIndex < 0 {
			t.Fatal("SPARKCLAW_TEST_EMAIL_MARK_READ_INDEX must be a non-negative integer")
		}
	}
	var mail app.EmailMail
	matched := 0
	for _, candidate := range mails.Items {
		if candidate.Direction == "inbound" && candidate.CaptureState == app.EmailCaptureComplete && candidate.CaptureID != "" && candidate.RemoteReadState != "read" {
			if matched != candidateIndex {
				matched++
				continue
			}
			mail = candidate
			break
		}
	}
	if mail.ID == "" {
		t.Fatal("no captured non-read QQ mail is available for qualification")
	}
	capture, found, err := repository.GetEmailCapture(ctx, ownerID, mail.CaptureID)
	if err != nil || !found || capture.State != app.EmailCaptureComplete || capture.PurgedAt != nil {
		t.Fatalf("load committed QQ capture: found=%t err=%v", found, err)
	}
	var manifest struct {
		MailID      string `json:"mail_id"`
		MailboxID   string `json:"mailbox_id"`
		Attachments []struct {
			Status string `json:"status"`
		} `json:"attachments"`
	}
	if json.Unmarshal([]byte(capture.ManifestJSON), &manifest) != nil {
		t.Fatal("committed QQ capture manifest is invalid")
	}
	attachments := 0
	for _, attachment := range manifest.Attachments {
		if attachment.Status == "available" {
			attachments++
		}
	}
	ownerScope := sha256.Sum256([]byte(ownerID))
	target := app.EmailCaptureTarget{AccountAddress: mailbox.Address, ProviderMessageID: mail.ProviderMessageID, ProviderNativeID: mail.ProviderNativeID, ProviderSelectionID: mail.ProviderSelectionID, ProviderThreadID: mail.ProviderThreadID, Folder: mail.Folder}
	binding := ReadRequest{Provider: provider.ID, Account: app.EmailAccountDefault, OwnerScope: hex.EncodeToString(ownerScope[:]), InvocationID: app.NewID("qq_mark_read_live"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.MarkRead.Revision, Target: &target}
	receipt := app.EmailCaptureReceipt{ManifestPath: capture.ManifestPath, ManifestSHA256: capture.ManifestSHA256, MailID: manifest.MailID, MailboxID: manifest.MailboxID, CaptureID: capture.ID, AttachmentsCount: attachments, ReadState: mail.RemoteReadState}
	result, err := runner.MarkRead(ctx, provider, app.EmailMarkReadRequest{Binding: binding, CommittedCapture: receipt})
	if err != nil {
		t.Fatalf("live QQ mark-read: %v (code=%s)", err, ErrorCode(err))
	}
	if result.ReadState != "read" {
		t.Fatalf("live QQ mark-read was not confirmed: state=%s", result.ReadState)
	}
	t.Log("live QQ mark-read confirmed exact committed target without exposing mailbox content")
}

// A fixture qualification cannot fall back to first unread or another account.
// Its input file is an explicit previously observed target, never credentials.
func qualifyPinnedIntake(t *testing.T, ctx context.Context, runner *PlaywrightRunner, provider Provider, probe ProbeResult, operation string) {
	t.Helper()
	if operation != "enumerate_thread" && operation != "capture" && operation != "recent_inbound" && operation != "discover" && operation != "metadata" {
		t.Fatal("invalid pinned qualification operation")
	}
	raw, err := os.ReadFile(os.Getenv("SPARKCLAW_TEST_EMAIL_TARGET_FILE"))
	if err != nil {
		t.Fatal("pinned target file required")
	}
	var target app.EmailCaptureTarget
	if decodeStrictJSON(raw, &target) != nil || !validMailTarget(target) {
		t.Fatal("invalid pinned qualification target")
	}
	if target.Folder == "" {
		target.Folder = "inbox"
	}
	if target.ProviderThreadID == "" {
		target.ProviderThreadID = target.ProviderSelectionID
	}
	digest := sha256.Sum256([]byte("email-management-qualification"))
	request := ReadRequest{Provider: provider.ID, Account: app.EmailAccountDefault, OwnerScope: hex.EncodeToString(digest[:]), InvocationID: app.NewID("email_pinned_qualification"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Capture.Revision}
	switch operation {
	case "discover", "metadata":
		request.ScriptRevision = provider.Discover.Revision
		result, err := runner.Discover(ctx, provider, request)
		if err != nil {
			t.Fatalf("bootstrap discovery failed: %v", err)
		}
		t.Logf("bootstrap discovery: provider=%s candidates=%d fixture_account_matches=%t", provider.ID, len(result.Candidates), strings.EqualFold(result.AccountAddress, target.AccountAddress))
		if operation == "metadata" {
			end := time.Now().UTC()
			request.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: result.AccountAddress, IntervalStart: end.Add(-24 * time.Hour), IntervalEnd: end, Limit: 50}
			for batch := 0; batch < 2; batch++ {
				request.InvocationID = app.NewID("email_metadata_qualification")
				observed, err := runner.Discover(ctx, provider, request)
				if err != nil {
					t.Fatalf("current-account metadata discovery failed: %v", err)
				}
				t.Logf("metadata batch: provider=%s batch=%d candidates=%d threads=%d complete=%t ordering=%s reason=%s continuation=%t", provider.ID, batch, len(observed.Candidates), len(observed.Threads), observed.Coverage.ScanComplete, observed.Coverage.Ordering, observed.Coverage.Reason, observed.Coverage.Continuation != "")
				if observed.Coverage.Continuation == "" {
					break
				}
				request.Discovery.Continuation = observed.Coverage.Continuation
			}
		}
	case "enumerate_thread":
		request.ScriptRevision = provider.EnumerateThread.Revision
		result, err := runner.EnumerateThread(ctx, provider, app.EmailThreadRequest{Binding: request, Thread: app.EmailThreadTarget{AccountAddress: target.AccountAddress, ProviderThreadID: target.ProviderThreadID, ProviderSelectionID: target.ProviderSelectionID, Folder: target.Folder}, Limit: 50})
		if err != nil {
			t.Fatalf("pinned inventory failed: %v", err)
		}
		t.Logf("pinned inventory: provider=%s members=%d complete=%t reason=%s", provider.ID, len(result.Members), result.Coverage.ScanComplete, result.Coverage.Reason)
	case "recent_inbound":
		request.ScriptRevision = provider.Discover.Revision
		end := time.Now().UTC()
		request.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: target.AccountAddress, IntervalStart: end.Add(-time.Hour), IntervalEnd: end, Limit: 50}
		result, err := runner.Discover(ctx, provider, request)
		if err != nil {
			t.Fatalf("recent discovery failed: %v", err)
		}
		t.Logf("recent discovery: provider=%s candidates=%d complete=%t qualified=%t reason=%s", provider.ID, len(result.Candidates), result.Coverage.ScanComplete, result.Coverage.BoundaryQualified, result.Coverage.Reason)
	case "capture":
		request.Target = &target
		result, err := runner.Read(ctx, provider, request)
		if err != nil {
			t.Fatalf("pinned capture failed: %v", err)
		}
		if err := verifyCapture(ctx, os.Getenv("SPARKCLAW_TEST_EMAIL_WORKSPACE_ROOT"), request, result); err != nil {
			t.Fatal("pinned capture files failed verification")
		}
		t.Logf("pinned capture: provider=%s status=%s attachments=%d read_state=%s", provider.ID, result.Status, result.Capture.AttachmentsCount, result.Capture.ReadState)
	}
}

type liveEmailController struct {
	*browsercontrol.Service
	t *testing.T
}

func (c *liveEmailController) RunScript(
	ctx context.Context,
	request browsercontrol.RunScriptRequest,
) (browsercontrol.ScriptExecutionResult, error) {
	result, err := c.Service.RunScript(ctx, request)
	if err != nil {
		c.t.Logf(
			"live browser control failure: code=%s retryable=%t",
			browsercontrol.ErrorCode(err),
			browsercontrol.ErrorRetryable(err),
		)
	} else if result.State == "failed" {
		var failure struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(result.Result, &failure) == nil {
			c.t.Logf("live provider script failure: code=%s", failure.Code)
		}
	}
	return result, err
}

func newPlaywrightEmailLiveStoreRuntime(ctx context.Context, cfg config.Config) (*store.Runtime, error) {
	timeouts := store.OperationTimeouts{
		Read:        time.Duration(cfg.State.ReadTimeoutSeconds) * time.Second,
		Write:       time.Duration(cfg.State.WriteTimeoutSeconds) * time.Second,
		Transaction: time.Duration(cfg.State.TransactionTimeoutSeconds) * time.Second,
	}
	return store.NewRuntime(ctx, store.RuntimeOptions{
		Backend: store.BackendKind(cfg.State.Backend), Timeouts: timeouts,
		File: store.FileStoreOptions{
			Path: cfg.State.Path, EncryptAtRest: cfg.State.EncryptAtRest,
			EncryptionKey: cfg.State.EncryptionKey, EncryptionKeyFile: cfg.State.EncryptionKeyFile,
		},
		PostgresDSN: cfg.State.DSN,
	})
}
