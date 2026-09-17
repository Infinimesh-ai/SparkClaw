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

// Explicit opt-in only: isolated Controller/workspace, Vault reads, no production
// mail Store writes, sends, mark-read or intake changes. Measured cases issue one
// empty range plus N exact missing-original targets (not N natural new arrivals).
func TestTimelineLivePerformance(t *testing.T) {
	if os.Getenv("SPARKCLAW_TEST_EMAIL_PERFORMANCE") != "1" {
		t.Skip("explicit isolated live performance opt-in required")
	}
	directory := os.Getenv("SPARKCLAW_TEST_TIMELINE_DIRECTORY")
	if !filepath.IsAbs(directory) || directory == "/" {
		t.Fatal("absolute isolated evidence directory required")
	}
	label := os.Getenv("SPARKCLAW_TEST_TIMELINE_PERFORMANCE_LABEL")
	if label != "baseline" && label != "optimized" && label != "pooled-cold" && label != "pooled-warm" {
		t.Fatal("explicit performance label required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
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
	if ec.ControllerSocket != filepath.Join(directory, "performance-controller.sock") {
		t.Fatal("isolated performance Controller socket required")
	}
	client, err := browsercontrol.NewHTTPControllerClient(ec.ControllerSocket, time.Duration(ec.ConnectTimeoutMS)*time.Millisecond)
	if err != nil {
		t.Fatal("controller unavailable")
	}
	t.Cleanup(client.Close)
	controller := browsercontrol.New(vault, client, ec.ProfileID)
	controller.Initialize(ctx)
	t.Cleanup(func() { _ = controller.Close() })
	checked, err := controller.Check(ctx)
	if err != nil || !checked.Configured {
		t.Fatal("browser credential unavailable")
	}
	runner := NewPlaywrightRunner(&liveEmailController{Service: controller, t: t})
	providers := strings.Split(os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_PROVIDERS"), ",")
	if len(providers) == 1 && providers[0] == "" {
		t.Fatal("explicit providers required")
	}
	for _, id := range providers {
		if id != "qq_mail" && id != "gmail" && id != "outlook" {
			t.Fatal("unsupported benchmark provider")
		}
		t.Run(id, func(t *testing.T) {
			provider, _ := DefaultRegistry().Get(id)
			probe, err := runner.Probe(ctx, provider, app.NewID("performance_probe"), uint64(checked.CredentialGeneration))
			if err != nil {
				t.Fatalf("probe code=%s", ErrorCode(err))
			}
			request := ReadRequest{Provider: id, Account: app.EmailAccountDefault, OwnerScope: performanceScope(), InvocationID: app.NewID("performance_bootstrap"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Discover.Revision}
			bootstrap, err := runner.Discover(ctx, provider, request)
			if err != nil {
				t.Fatalf("bootstrap code=%s", ErrorCode(err))
			}
			start := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
			if id == "outlook" {
				start = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			request.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: bootstrap.AccountAddress, IntervalStart: start, IntervalEnd: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}
			request.InvocationID = app.NewID("performance_inventory")
			inventory, err := runner.Discover(ctx, provider, request)
			if err != nil {
				t.Fatalf("inventory code=%s", ErrorCode(err))
			}
			maximum := 11
			if id == "outlook" {
				maximum = 2
			}
			if len(inventory.Candidates) < maximum {
				t.Fatalf("need %d distinct available samples, have %d", maximum, len(inventory.Candidates))
			}
			// Freeze the exact candidate inventory between baseline/optimized runs.
			inventoryPath := filepath.Join(directory, id+"-performance-targets.json")
			if label == "baseline" {
				raw, _ := json.Marshal(inventory.Candidates[:maximum])
				if os.WriteFile(inventoryPath, raw, 0600) != nil {
					t.Fatal("cannot retain private targets")
				}
			} else {
				raw, readErr := os.ReadFile(inventoryPath)
				if readErr != nil || json.Unmarshal(raw, &inventory.Candidates) != nil || len(inventory.Candidates) != maximum {
					t.Fatal("baseline target inventory required")
				}
			}
			// Use the adjacent interval already exercised by live qualification;
			// future-date predicates are not supported uniformly by providers.
			// Still revalidate emptiness instead of trusting stale evidence.
			qualifiedSampleMS := map[string]int64{"qq_mail": 1789477863000, "gmail": 1789533548432, "outlook": 1788851630069}
			emptyStart := time.UnixMilli(qualifiedSampleMS[id]).UTC().Truncate(time.Second).Add(time.Second)
			intervalPath := filepath.Join(directory, id+"-performance-interval.json")
			if label == "baseline" {
				raw, _ := json.Marshal(emptyStart)
				if os.WriteFile(intervalPath, raw, 0600) != nil {
					t.Fatal("cannot retain benchmark interval")
				}
			} else {
				raw, readErr := os.ReadFile(intervalPath)
				if readErr != nil || json.Unmarshal(raw, &emptyStart) != nil {
					t.Fatal("baseline interval required")
				}
			}
			request.Discovery.IntervalStart, request.Discovery.IntervalEnd = emptyStart, emptyStart.Add(time.Second)
			request.InvocationID = app.NewID("performance_empty_check")
			empty, err := runner.Discover(ctx, provider, request)
			if err != nil || len(empty.Candidates) != 0 || !empty.Coverage.ScanComplete || !empty.Coverage.BoundaryQualified {
				t.Fatal("empty control interval not qualified")
			}
			request.ScriptRevision = provider.CollectPage.Revision
			if os.Getenv("SPARKCLAW_TEST_TIMELINE_STABILITY") == "1" {
				request.OwnerScope = performanceScope()
				for attempt := 1; attempt <= 5; attempt++ {
					if label != "pooled-warm" {
						request.OwnerScope = performanceScope()
					}
					request.InvocationID = "email_changes_" + performanceScope() + "_r1"
					request.Discovery.RetryTargets = nil
					began := time.Now()
					out, callErr := runner.CollectPage(ctx, provider, request)
					passed := callErr == nil && len(out.Captures) == 0 && len(out.Failures) == 0 && len(out.Discovery.Candidates) == 0 && out.Discovery.Coverage.ScanComplete
					t.Logf("stability provider=%s attempt=%d passed=%t error_code=%s elapsed_ms=%d", id, attempt, passed, ErrorCode(callErr), time.Since(began).Milliseconds())
					if !passed {
						t.Errorf("empty interval stability attempt %d failed", attempt)
					}
				}
				return
			}
			for _, count := range []int{0, 1, maximum} {
				request.OwnerScope = performanceScope()
				request.InvocationID = "email_changes_" + performanceScope() + "_r1"
				request.Discovery.RetryTargets = inventory.Candidates[:count]
				if label == "pooled-warm" {
					// Prime only an empty native range in this same isolated owner.
					// No selected original is acquired before the measured fresh case.
					prime := request
					prime.InvocationID = "email_changes_" + performanceScope() + "_r1"
					discovery := *request.Discovery
					discovery.RetryTargets = nil
					prime.Discovery = &discovery
					out, primeErr := runner.CollectPage(ctx, provider, prime)
					if primeErr != nil || len(out.Captures) != 0 || len(out.Failures) != 0 || len(out.Discovery.Candidates) != 0 || !out.Discovery.Coverage.ScanComplete || !out.Discovery.Coverage.BoundaryQualified {
						t.Fatal("empty same-owner warm-up did not qualify")
					}
				}
				var originalHashes []string
				for replay := 0; replay < 2; replay++ {
					began := time.Now()
					out, callErr := runner.CollectPage(ctx, provider, request)
					elapsed := time.Since(began).Milliseconds() // excludes local independent verification
					if callErr != nil {
						t.Fatalf("collect count=%d replay=%d code=%s", count, replay, ErrorCode(callErr))
					}
					if len(out.Captures) != count || len(out.Failures) != 0 || len(out.Discovery.Candidates) != 0 {
						for _, failure := range out.Failures {
							t.Logf("original failure_code=%s scope=%s qualified=%t", failure.ErrorCode, failure.Scope, failure.Qualified)
						}
						t.Fatalf("case count=%d captures=%d failures=%d discovered=%d", count, len(out.Captures), len(out.Failures), len(out.Discovery.Candidates))
					}
					var sizes []int64
					var total int64
					for index, capture := range out.Captures {
						bind := request
						bind.Target = &capture.Target
						bind.InvocationID = PageCaptureInvocationID(request.InvocationID, id, *bind.Target)
						if verifyCapture(ctx, filepath.Join(directory, "workspace"), bind, capture.Result) != nil {
							t.Fatal("capture verification failed")
						}
						if capture.Result.Capture == nil {
							t.Fatal("missing receipt")
						}
						if replay == 0 {
							originalHashes = append(originalHashes, capture.Result.Capture.ManifestSHA256)
						} else if originalHashes[index] != capture.Result.Capture.ManifestSHA256 {
							t.Fatal("same-batch replay changed receipt")
						}
						raw, readErr := os.ReadFile(filepath.Join(directory, "workspace", capture.Result.Capture.ManifestPath))
						var manifest struct {
							Files []struct {
								Bytes int64 `json:"bytes"`
							} `json:"files"`
						}
						if readErr != nil || json.Unmarshal(raw, &manifest) != nil || len(manifest.Files) != 1 {
							t.Fatal("invalid original manifest")
						}
						sizes = append(sizes, manifest.Files[0].Bytes)
						total += manifest.Files[0].Bytes
					}
					record := map[string]any{"label": label, "provider": id, "targets": count, "replay": replay == 1, "elapsed_ms": elapsed, "original_bytes": total, "original_sizes": sizes, "verified": true, "shape": "empty_range_plus_exact_targets"}
					raw, _ := json.Marshal(record)
					t.Log(string(raw))
					f, openErr := os.OpenFile(filepath.Join(directory, label+"-performance.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
					if openErr != nil {
						t.Fatal("cannot record benchmark")
					}
					_, writeErr := f.Write(append(raw, '\n'))
					closeErr := f.Close()
					if writeErr != nil || closeErr != nil {
						t.Fatal("cannot flush benchmark")
					}
				}
			}
		})
	}
}

func performanceScope() string {
	digest := sha256.Sum256([]byte("isolated-timeline-performance:" + app.NewID("run")))
	return hex.EncodeToString(digest[:])
}
