package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
)

// A read-only, explicit candidate smoke. It does not establish positive native
// arrival coexistence, the long soak or the full shared-page release gate.
func TestMailSharedPageLive(t *testing.T) {
	if os.Getenv("SPARKCLAW_TEST_SHARED_PAGE") != "1" {
		t.Skip("explicit shared-page qualification required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	cfg, err := config.Load(os.Getenv("SPARKCLAW_TEST_CONFIG"))
	if err != nil {
		t.Fatal("live config unavailable")
	}
	runtime, err := newPlaywrightEmailLiveStoreRuntime(ctx, cfg)
	if err != nil {
		t.Fatal("credential store unavailable")
	}
	defer runtime.Close(context.Background())
	vault := credential.New(runtime.CredentialRepository(), credential.Options{Key: cfg.State.CredentialKey, KeyFile: cfg.State.CredentialKeyFile})
	if err := vault.Ready(); err != nil {
		t.Fatal("credential vault unavailable")
	}
	client, err := browsercontrol.NewHTTPControllerClient(cfg.Adapters.BrowserAutomation.PlaywrightExtension.ControllerSocket, 15*time.Second)
	if err != nil {
		t.Fatal("controller unavailable")
	}
	defer client.Close()
	controller := browsercontrol.New(vault, client, "default")
	controller.Initialize(ctx)
	defer controller.Close()
	status, err := controller.Check(ctx)
	if err != nil || !status.Configured {
		t.Fatal("browser credential unavailable")
	}
	runner := NewPlaywrightRunner(liveWaitingController{Service: controller, t: t})
	digest := sha256.Sum256([]byte(app.NewID("shared_page_qualification")))
	scope := hex.EncodeToString(digest[:])
	ids := strings.Split(os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_PROVIDERS"), ",")
	if len(ids) != 3 {
		t.Fatal("all three providers are required")
	}
	type mailbox struct {
		provider Provider
		address  string
		probe    ProbeResult
	}
	accounts := map[string]mailbox{}
	for _, id := range ids {
		provider, ok := DefaultRegistry().Get(id)
		if !ok {
			t.Fatal("unknown provider")
		}
		probe, err := runner.Probe(ctx, provider, app.NewID("shared_probe"), uint64(status.CredentialGeneration))
		if err != nil {
			t.Fatalf("%s probe: %s", id, ErrorCode(err))
		}
		identity, err := runner.Discover(ctx, provider, ReadRequest{Provider: id, Account: app.EmailAccountDefault,
			OwnerScope: scope, InvocationID: app.NewID("shared_identity"), BrowserCredentialGeneration: probe.Generation,
			ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Discover.Revision})
		if err != nil || !mailAddressPattern.MatchString(identity.AccountAddress) {
			t.Fatalf("%s identity: %s", id, ErrorCode(err))
		}
		accounts[id] = mailbox{provider, identity.AccountAddress, probe}
	}
	observe := func(id, action string) (string, int) {
		t.Helper()
		account := accounts[id]
		result, err := controller.RunScript(ctx, browsercontrol.RunScriptRequest{TaskID: app.NewID("shared_watch"),
			CredentialGeneration: status.CredentialGeneration, WaitTimeoutMS: 30000, Provider: id, Operation: "observe",
			ScriptID: id + ".observe", Revision: 1, Input: map[string]any{"schema_version": 1, "action": action,
				"account_address": account.address, "owner_scope": scope}})
		if err != nil || result.State != "completed" {
			t.Fatalf("%s observer %s: %v", id, action, err)
		}
		var value struct {
			State string `json:"state"`
			Hints int    `json:"hints"`
		}
		if json.Unmarshal(result.Result, &value) != nil {
			t.Fatal("invalid observer status")
		}
		return value.State, value.Hints
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 2*time.Minute)
		defer done()
		for _, id := range ids {
			account, ok := accounts[id]
			if !ok {
				continue
			}
			_, err := controller.RunScript(cleanup, browsercontrol.RunScriptRequest{TaskID: app.NewID("shared_cleanup"),
				CredentialGeneration: status.CredentialGeneration, WaitTimeoutMS: 30000, Provider: id,
				Operation: "observe", ScriptID: id + ".observe", Revision: 1,
				Input: map[string]any{"schema_version": 1, "action": "stop", "account_address": account.address, "owner_scope": scope}})
			if err != nil {
				t.Errorf("%s observer cleanup: %s", id, ErrorCode(err))
			}
		}
	}()
	for _, id := range ids {
		if state, _ := observe(id, "start"); state != "watching" {
			t.Fatalf("%s did not start watching: %s", id, state)
		}
	}
	time.Sleep(30 * time.Second)
	for _, id := range ids {
		account := accounts[id]
		start := map[string]time.Time{
			"qq_mail": time.UnixMilli(1789477863000).UTC(),
			"gmail":   time.UnixMilli(1789533548432).UTC(),
			"outlook": time.UnixMilli(1788851630069).UTC(),
		}[id].Truncate(time.Second).Add(time.Second)
		for round := 0; round < 2; round++ {
			request := ReadRequest{Provider: id, Account: app.EmailAccountDefault, OwnerScope: scope,
				InvocationID: app.NewID("shared_round"), BrowserCredentialGeneration: account.probe.Generation,
				ProbeRevision: account.provider.Probe.Revision, ScriptRevision: account.provider.CollectPage.Revision,
				Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: account.address,
					IntervalStart: start, IntervalEnd: start.Add(time.Second), Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}}
			began := time.Now()
			page, err := runner.CollectPage(ctx, account.provider, request)
			if err != nil || len(page.Captures) != 0 || len(page.Failures) != 0 || len(page.Discovery.Candidates) != 0 ||
				!page.Discovery.Coverage.ScanComplete || !page.Discovery.Coverage.BoundaryQualified {
				t.Fatalf("%s round %d failed: %s", id, round, ErrorCode(err))
			}
			state, hints := observe(id, "status")
			if state != "watching" || hints != 0 {
				t.Fatalf("%s round %d observer state=%s hints=%d", id, round, state, hints)
			}
			t.Logf("%s shared-page smoke empty round %d elapsed_ms=%d", id, round, time.Since(began).Milliseconds())
		}
	}
	if os.Getenv("SPARKCLAW_TEST_SHARED_PAGE_NONEMPTY") == "1" {
		label := os.Getenv("SPARKCLAW_TEST_SHARED_PAGE_LABEL")
		fixtureDir := os.Getenv("SPARKCLAW_TEST_SHARED_PAGE_FIXTURE_DIR")
		workspace := os.Getenv("SPARKCLAW_TEST_EMAIL_WORKSPACE_ROOT")
		if (label != "baseline" && label != "candidate") || !filepath.IsAbs(fixtureDir) || !filepath.IsAbs(workspace) {
			t.Fatal("nonempty comparison needs a label and absolute fixture/workspace directories")
		}
		for _, id := range ids {
			account := accounts[id]
			inventoryStart := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
			if id == "outlook" {
				inventoryStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			inventory, err := runner.Discover(ctx, account.provider, ReadRequest{Provider: id, Account: app.EmailAccountDefault,
				OwnerScope: scope, InvocationID: app.NewID("shared_inventory"), BrowserCredentialGeneration: account.probe.Generation,
				ProbeRevision: account.provider.Probe.Revision, ScriptRevision: account.provider.Discover.Revision,
				Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: account.address,
					IntervalStart: inventoryStart, IntervalEnd: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC),
					Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}})
			if err != nil || len(inventory.Candidates) < 2 {
				t.Fatalf("%s nonempty inventory unavailable: %s", id, ErrorCode(err))
			}
			targetPath := filepath.Join(fixtureDir, id+"-targets.json")
			hashPath := filepath.Join(fixtureDir, id+"-original-hashes.json")
			targets := inventory.Candidates[:2]
			if label == "baseline" {
				raw, _ := json.Marshal(targets)
				if err := os.WriteFile(targetPath, raw, 0600); err != nil {
					t.Fatal("cannot retain private target inventory")
				}
			} else {
				raw, readErr := os.ReadFile(targetPath)
				if readErr != nil || json.Unmarshal(raw, &targets) != nil || len(targets) != 2 {
					t.Fatal("matching baseline target inventory required")
				}
			}
			baselineHashes := map[string]string{}
			if label == "candidate" {
				raw, readErr := os.ReadFile(hashPath)
				if readErr != nil || json.Unmarshal(raw, &baselineHashes) != nil || len(baselineHashes) != 2 {
					t.Fatal("matching baseline originals required")
				}
			}
			currentHashes := map[string]string{}
			start := map[string]time.Time{
				"qq_mail": time.UnixMilli(1789477863000).UTC(),
				"gmail":   time.UnixMilli(1789533548432).UTC(),
				"outlook": time.UnixMilli(1788851630069).UTC(),
			}[id].Truncate(time.Second).Add(time.Second)
			for _, count := range []int{1, 2} {
				request := ReadRequest{Provider: id, Account: app.EmailAccountDefault, OwnerScope: scope,
					InvocationID: app.NewID("shared_nonempty"), BrowserCredentialGeneration: account.probe.Generation,
					ProbeRevision: account.provider.Probe.Revision, ScriptRevision: account.provider.CollectPage.Revision,
					Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: account.address,
						IntervalStart: start, IntervalEnd: start.Add(time.Second), Limit: 50,
						ProviderMode: app.EmailProviderModeTimeRange, RetryTargets: targets[:count]}}
				for replay := 0; replay < 2; replay++ {
					began := time.Now()
					page, callErr := runner.CollectPage(ctx, account.provider, request)
					if callErr != nil || len(page.Captures) != count || len(page.Failures) != 0 ||
						len(page.Discovery.Candidates) != 0 || !page.Discovery.Coverage.ScanComplete ||
						!page.Discovery.Coverage.BoundaryQualified {
						t.Fatalf("%s nonempty count=%d replay=%d: %s", id, count, replay, ErrorCode(callErr))
					}
					for _, capture := range page.Captures {
						bind := request
						bind.Target = &capture.Target
						bind.InvocationID = PageCaptureInvocationID(request.InvocationID, id, capture.Target)
						if verifyCapture(ctx, workspace, bind, capture.Result) != nil || capture.Result.Capture == nil {
							t.Fatal("nonempty original verification failed")
						}
						key := capture.Target.ProviderMessageID
						raw, readErr := os.ReadFile(filepath.Join(workspace, capture.Result.Capture.ManifestPath))
						var manifest struct {
							Files []struct {
								SHA256 string `json:"sha256"`
							} `json:"files"`
						}
						if readErr != nil || json.Unmarshal(raw, &manifest) != nil || len(manifest.Files) != 1 || manifest.Files[0].SHA256 == "" {
							t.Fatal("nonempty original manifest invalid")
						}
						hash := manifest.Files[0].SHA256
						if previous := currentHashes[key]; previous != "" && previous != hash {
							t.Fatal("replay changed original hash")
						}
						if label == "candidate" && baselineHashes[key] != hash {
							t.Fatal("candidate original differs from baseline")
						}
						currentHashes[key] = hash
					}
					state, _ := observe(id, "status")
					if state != "watching" {
						t.Fatal("watch did not survive original collection")
					}
					t.Logf("%s nonempty count=%d replay=%d elapsed_ms=%d verified=%d", id, count, replay,
						time.Since(began).Milliseconds(), len(page.Captures))
				}
			}
			if len(currentHashes) != 2 {
				t.Fatal("nonempty original inventory incomplete")
			}
			if id == "qq_mail" {
				stressRounds, _ := strconv.Atoi(os.Getenv("SPARKCLAW_TEST_SHARED_PAGE_STRESS_QQ_ROUNDS"))
				if stressRounds < 0 || stressRounds > 150 {
					t.Fatal("QQ stress rounds out of range")
				}
				for round := 0; round < stressRounds; round++ {
					request := ReadRequest{Provider: id, Account: app.EmailAccountDefault, OwnerScope: scope,
						InvocationID: app.NewID("shared_stress"), BrowserCredentialGeneration: account.probe.Generation,
						ProbeRevision: account.provider.Probe.Revision, ScriptRevision: account.provider.CollectPage.Revision,
						Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: account.address,
							IntervalStart: start, IntervalEnd: start.Add(time.Second), Limit: 50,
							ProviderMode: app.EmailProviderModeTimeRange, RetryTargets: targets[:1]}}
					page, callErr := runner.CollectPage(ctx, account.provider, request)
					if callErr != nil || len(page.Captures) != 1 || len(page.Failures) != 0 {
						code := string(ErrorCode(callErr))
						if len(page.Failures) != 0 {
							code = page.Failures[0].ErrorCode
						}
						t.Fatalf("QQ stress round=%d failed with %s", round+1, code)
					}
					bind := request
					bind.Target = &page.Captures[0].Target
					bind.InvocationID = PageCaptureInvocationID(request.InvocationID, id, page.Captures[0].Target)
					if verifyCapture(ctx, workspace, bind, page.Captures[0].Result) != nil {
						t.Fatalf("QQ stress round=%d original invalid", round+1)
					}
					if (round+1)%10 == 0 {
						state, _ := observe(id, "status")
						if state != "watching" {
							t.Fatalf("QQ stress round=%d watch state=%s", round+1, state)
						}
						t.Logf("QQ stress completed rounds=%d", round+1)
					}
				}
			}
			if label == "baseline" {
				raw, _ := json.Marshal(currentHashes)
				if err := os.WriteFile(hashPath, raw, 0600); err != nil {
					t.Fatal("cannot retain private baseline original hashes")
				}
			}
		}
	}
	if root := os.Getenv("SPARKCLAW_TEST_SHARED_PAGE_CLI_RUNTIME_DIR"); root != "" {
		if !filepath.IsAbs(root) {
			t.Fatal("absolute runtime directory required")
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal("runtime inventory unavailable")
		}
		count := 0
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "session-") {
				count++
			}
		}
		t.Logf("retained mail runtime sessions=%d", count)
		if expected, err := strconv.Atoi(os.Getenv("SPARKCLAW_TEST_SHARED_PAGE_EXPECT_SESSIONS")); err == nil && count != expected {
			t.Fatalf("retained session count=%d, expected=%d", count, expected)
		}
	}
	if seconds, err := strconv.Atoi(os.Getenv("SPARKCLAW_TEST_SHARED_PAGE_HOLD_SECONDS")); err == nil && seconds > 0 && seconds <= 120 {
		t.Log("shared-page observation hold started")
		time.Sleep(time.Duration(seconds) * time.Second)
	}
}

// Recovers a private Outlook delivery address only from a previously verified
// outgoing original received by QQ. The sign-in alias is not a send target.
func TestMailSharedPageHistoricalSenderProof(t *testing.T) {
	if os.Getenv("SPARKCLAW_TEST_SHARED_PAGE_SENDER_PROOF") != "1" {
		t.Skip("explicit historical sender proof required")
	}
	marker := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_RECONCILE_MARKER")
	proofPath := os.Getenv("SPARKCLAW_TEST_NOTIFICATION_RECEIPT_IDENTITY")
	if !strings.HasPrefix(marker, "SCW-mail-") || !filepath.IsAbs(proofPath) {
		t.Fatal("private marker and absolute proof output are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cfg, err := config.Load(os.Getenv("SPARKCLAW_TEST_CONFIG"))
	if err != nil {
		t.Fatal("live config unavailable")
	}
	runtime, err := newPlaywrightEmailLiveStoreRuntime(ctx, cfg)
	if err != nil {
		t.Fatal("credential store unavailable")
	}
	defer runtime.Close(context.Background())
	vault := credential.New(runtime.CredentialRepository(), credential.Options{Key: cfg.State.CredentialKey, KeyFile: cfg.State.CredentialKeyFile})
	if err := vault.Ready(); err != nil {
		t.Fatal("credential vault unavailable")
	}
	client, err := browsercontrol.NewHTTPControllerClient(cfg.Adapters.BrowserAutomation.PlaywrightExtension.ControllerSocket, 15*time.Second)
	if err != nil {
		t.Fatal("controller unavailable")
	}
	defer client.Close()
	controller := browsercontrol.New(vault, client, "default")
	controller.Initialize(ctx)
	defer controller.Close()
	status, err := controller.Check(ctx)
	if err != nil || !status.Configured {
		t.Fatal("browser credential unavailable")
	}
	runner := NewPlaywrightRunner(liveWaitingController{Service: controller, t: t})
	provider, _ := DefaultRegistry().Get("qq_mail")
	probe, err := runner.Probe(ctx, provider, app.NewID("shared_sender_proof_probe"), uint64(status.CredentialGeneration))
	if err != nil {
		t.Fatalf("QQ probe: %s", ErrorCode(err))
	}
	digest := sha256.Sum256([]byte(app.NewID("shared_sender_proof")))
	scope := hex.EncodeToString(digest[:])
	identity, err := runner.Discover(ctx, provider, ReadRequest{Provider: provider.ID, Account: app.EmailAccountDefault,
		OwnerScope: scope, InvocationID: app.NewID("shared_sender_proof_identity"), BrowserCredentialGeneration: probe.Generation,
		ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Discover.Revision})
	if err != nil || !mailAddressPattern.MatchString(identity.AccountAddress) {
		t.Fatal("QQ identity unavailable")
	}
	verifyLiveNotificationReceipt(t, ctx, runner, provider, scope, identity.AccountAddress, probe, marker,
		time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC))
}
