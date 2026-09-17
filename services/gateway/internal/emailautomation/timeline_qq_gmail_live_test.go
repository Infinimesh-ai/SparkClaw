package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
)

// Opt-in, isolated Controller only. It reads lists/originals but never invokes
// mark_read, send, mailbox settings, deployment or production Store commands.
func TestQQGmailTimelineLiveQualification(t *testing.T) {
	if os.Getenv("SPARKCLAW_TEST_QQ_GMAIL_TIMELINE") != "1" {
		t.Skip("explicit isolated QQ/Gmail qualification required")
	}
	directory := os.Getenv("SPARKCLAW_TEST_TIMELINE_DIRECTORY")
	if directory == "" {
		t.Fatal("isolated evidence directory required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	cfg, err := config.Load(os.Getenv("SPARKCLAW_TEST_CONFIG"))
	if err != nil {
		t.Fatal("config unavailable")
	}
	runtime, err := newPlaywrightEmailLiveStoreRuntime(ctx, cfg)
	if err != nil {
		t.Fatal("credential backend unavailable")
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		_ = runtime.Close(closeCtx)
	})
	vault := credential.New(runtime.CredentialRepository(), credential.Options{Key: cfg.State.CredentialKey, KeyFile: cfg.State.CredentialKeyFile})
	if vault.Ready() != nil {
		t.Fatal("credential vault unavailable")
	}
	ec := cfg.Adapters.BrowserAutomation.PlaywrightExtension
	client, err := browsercontrol.NewHTTPControllerClient(ec.ControllerSocket, time.Duration(ec.ConnectTimeoutMS)*time.Millisecond)
	if err != nil {
		t.Fatal("controller unavailable")
	}
	t.Cleanup(client.Close)
	controller := browsercontrol.New(vault, client, ec.ProfileID)
	controller.Initialize(ctx)
	t.Cleanup(func() { _ = controller.Close() })
	status, err := controller.Check(ctx)
	if err != nil || !status.Configured {
		t.Fatal("browser credential unavailable")
	}
	runner := NewPlaywrightRunner(&liveEmailController{Service: controller, t: t})
	digest := sha256.Sum256([]byte("isolated-timeline-qualification:" + app.NewID("run")))
	for _, id := range strings.Split(os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_PROVIDERS"), ",") {
		if id != "gmail" && id != "qq_mail" && id != "outlook" {
			continue
		}
		t.Run(id, func(t *testing.T) {
			provider, _ := DefaultRegistry().Get(id)
			probe, err := runner.Probe(ctx, provider, app.NewID("timeline_probe"), uint64(status.CredentialGeneration))
			if err != nil {
				t.Fatalf("probe code=%s", ErrorCode(err))
			}
			request := ReadRequest{Provider: id, Account: app.EmailAccountDefault, OwnerScope: hex.EncodeToString(digest[:]), InvocationID: app.NewID("timeline_bootstrap"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Discover.Revision}
			bootstrap, err := runner.Discover(ctx, provider, request)
			if err != nil {
				t.Fatalf("bootstrap code=%s", ErrorCode(err))
			}
			readEvidence := func() timelineLiveEvidence {
				t.Helper()
				raw, err := os.ReadFile(filepath.Join(directory, id+"-evidence.json"))
				if err != nil {
					t.Fatal("evidence unavailable")
				}
				var e timelineLiveEvidence
				if json.Unmarshal(raw, &e) != nil {
					t.Fatal("evidence invalid")
				}
				return e
			}
			start := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
			end := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
			if id == "outlook" {
				start = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			discover := func(a, b time.Time, limit int) app.EmailDiscoveryResult {
				t.Helper()
				request.InvocationID = app.NewID("timeline_discovery")
				request.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: bootstrap.AccountAddress, IntervalStart: a, IntervalEnd: b, Limit: limit, ProviderMode: app.EmailProviderModeTimeRange}
				began := time.Now()
				out, err := runner.Discover(ctx, provider, request)
				if err != nil {
					t.Fatalf("range code=%s elapsed_ms=%d", ErrorCode(err), time.Since(began).Milliseconds())
				}
				t.Logf("range candidates=%d complete=%t qualified=%t overflow=%t unsupported=%d elapsed_ms=%d", len(out.Candidates), out.Coverage.ScanComplete, out.Coverage.BoundaryQualified, out.Coverage.Continuation != "", out.Coverage.UnsupportedRows, time.Since(began).Milliseconds())
				return out
			}
			broad := discover(start, end, 50)
			e := readEvidence()
			if len(broad.Candidates) == 0 || e.SampleMS <= 0 {
				t.Fatal("nonempty sample unavailable")
			}
			if !e.InboundOnly {
				t.Fatal("non-inbound scope observed")
			}
			target := broad.Candidates[0]
			contains := func(rows []app.EmailCaptureTarget) bool {
				for _, row := range rows {
					if row.ProviderMessageID == target.ProviderMessageID {
						return true
					}
				}
				return false
			}
			second := time.UnixMilli(e.SampleMS).UTC().Truncate(time.Second)
			if os.Getenv("SPARKCLAW_TEST_TIMELINE_CAPTURE_ONLY") != "1" {
				within := discover(second, second.Add(time.Second), 50)
				if !contains(within.Candidates) {
					t.Fatal("same-second sample missing")
				}
				after := discover(second.Add(time.Second), second.Add(2*time.Second), 50)
				if contains(after.Candidates) {
					t.Fatal("upper boundary sample leaked")
				}
				before := discover(second.Add(-time.Second), second, 50)
				if contains(before.Candidates) {
					t.Fatal("exclusive end sample leaked")
				}
				t.Logf("second_boundary_include=true lower_exclusion=true upper_exclusion=true same_second_candidates=%d scope_inbound=true", len(within.Candidates))
				if len(broad.Candidates) > 1 {
					bounded := discover(start, end, 1)
					if bounded.Coverage.Continuation == "" || bounded.Coverage.ScanComplete {
						t.Fatal("bounded overflow was incorrectly complete")
					}
					t.Log("bounded_overflow_detected=true; this is limit=1 qualification, not proof of >50 real rows")
				}
			}
			request.ScriptRevision = provider.CollectPage.Revision
			request.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: bootstrap.AccountAddress, IntervalStart: second.Add(time.Second), IntervalEnd: second.Add(2 * time.Second), Limit: 50, ProviderMode: app.EmailProviderModeTimeRange, RetryTargets: []app.EmailCaptureTarget{target}}
			intervalHash := sha256.Sum256([]byte(id + "-isolated-replay"))
			request.InvocationID = "email_changes_" + hex.EncodeToString(intervalHash[:]) + "_r1"
			began := time.Now()
			first, err := runner.CollectPage(ctx, provider, request)
			if err != nil {
				t.Fatalf("original page code=%s", ErrorCode(err))
			}
			firstEvidence := readEvidence()
			if firstEvidence.Downloads != 1 {
				t.Fatal("fresh qualification must download exactly one original")
			}
			if len(first.Captures) != 1 || len(first.Failures) != 0 {
				for _, failure := range first.Failures {
					t.Logf("original failure_code=%s scope=%s qualified=%t", failure.ErrorCode, failure.Scope, failure.Qualified)
				}
				t.Fatalf("original captures=%d failures=%d", len(first.Captures), len(first.Failures))
			}
			bind := request
			bind.Target = &target
			bind.InvocationID = PageCaptureInvocationID(request.InvocationID, id, target)
			if verifyCapture(ctx, filepath.Join(directory, "workspace"), bind, first.Captures[0].Result) != nil {
				t.Fatal("original verification failed")
			}
			t.Logf("original_verified=true downloads=%d elapsed_ms=%d", firstEvidence.Downloads, time.Since(began).Milliseconds())
			repeated, err := runner.CollectPage(ctx, provider, request)
			if err != nil {
				t.Fatalf("replay code=%s", ErrorCode(err))
			}
			replayEvidence := readEvidence()
			if len(repeated.Captures) != 1 || repeated.Captures[0].Result.Capture.ManifestSHA256 != first.Captures[0].Result.Capture.ManifestSHA256 || replayEvidence.Downloads != 0 {
				t.Fatal("replay repeated download or changed hash")
			}
			t.Log("same_batch_replay_hash_equal=true downloads=0")
			intervalHash = sha256.Sum256([]byte(id + "-isolated-next-replay"))
			request.InvocationID = "email_changes_" + hex.EncodeToString(intervalHash[:]) + "_r2"
			repeated, err = runner.CollectPage(ctx, provider, request)
			if err != nil {
				t.Fatalf("next round code=%s", ErrorCode(err))
			}
			replayEvidence = readEvidence()
			if len(repeated.Captures) != 1 || repeated.Captures[0].Result.Capture.ManifestSHA256 != first.Captures[0].Result.Capture.ManifestSHA256 || replayEvidence.Downloads != 0 {
				t.Fatal("next round repeated download or changed hash")
			}
			t.Log("next_round_replay_hash_equal=true downloads=0")
		})
	}
}

type timelineLiveEvidence struct {
	SampleMS    int64 `json:"sample_ms"`
	Downloads   int   `json:"downloads"`
	InboundOnly bool  `json:"inbound_only"`
}
