package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
)

// Run with production intake paused: this owns temporary watchers in the same
// dedicated profile and never sends or modifies account/login settings.
func TestAppCLISharedPageOrdersLive(t *testing.T) {
	if os.Getenv("SPARKCLAW_TEST_APP_CLI_SHARED_ORDERS") != "1" {
		t.Skip("explicit read-only App-CLI startup-order qualification required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	cfg, err := config.Load(os.Getenv("SPARKCLAW_TEST_CONFIG"))
	if err != nil {
		t.Fatal("live configuration unavailable")
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
		t.Fatal("browser proof unavailable")
	}
	runner := NewPlaywrightRunner(liveWaitingController{Service: controller, t: t})
	sum := sha256.Sum256([]byte(app.NewID("startup_order_qualification")))
	scope := hex.EncodeToString(sum[:])
	for _, id := range []string{app.EmailProviderQQMail, app.EmailProviderGmail, app.EmailProviderOutlook} {
		provider, _ := DefaultRegistry().Get(id)
		probe, err := runner.Probe(ctx, provider, app.NewID("order_probe"), uint64(status.CredentialGeneration))
		if err != nil {
			t.Fatalf("%s authenticated probe: %s", id, ErrorCode(err))
		}
		identity, err := runner.Discover(ctx, provider, ReadRequest{Provider: id, Account: app.EmailAccountDefault, OwnerScope: scope,
			InvocationID: app.NewID("order_identity"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Discover.Revision})
		if err != nil || !mailAddressPattern.MatchString(identity.AccountAddress) {
			t.Fatalf("%s identity unavailable", id)
		}
		watch := func(callContext context.Context, action string) string {
			t.Helper()
			result, err := controller.RunScript(callContext, browsercontrol.RunScriptRequest{TaskID: app.NewID("order_watch"),
				CredentialGeneration: status.CredentialGeneration, WaitTimeoutMS: 30000, Provider: id, Operation: "observe",
				ScriptID: provider.Observe.ID, Revision: provider.Observe.Revision,
				Input: map[string]any{"schema_version": 1, "action": action, "account_address": identity.AccountAddress, "owner_scope": scope}})
			var value struct {
				Status string `json:"status"`
			}
			if err != nil || result.State != "completed" || json.Unmarshal(result.Result, &value) != nil {
				t.Fatalf("%s watch %s unavailable", id, action)
			}
			return value.Status
		}
		defer func() {
			cleanup, done := context.WithTimeout(context.Background(), 60*time.Second)
			defer done()
			watch(cleanup, "stop")
		}()
		read := func() {
			t.Helper()
			start := map[string]time.Time{"qq_mail": time.UnixMilli(1789477863000), "gmail": time.UnixMilli(1789533548432), "outlook": time.UnixMilli(1788851630069)}[id].UTC().Truncate(time.Second).Add(time.Second)
			result, err := runner.CollectPage(ctx, provider, ReadRequest{Provider: id, Account: app.EmailAccountDefault, OwnerScope: scope,
				InvocationID: app.NewID("order_read"), BrowserCredentialGeneration: probe.Generation, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.CollectPage.Revision,
				Discovery: &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: identity.AccountAddress,
					IntervalStart: start, IntervalEnd: start.Add(time.Second), Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}})
			if err != nil || len(result.Failures) != 0 || len(result.Captures) != 0 || !result.Discovery.Coverage.ScanComplete || !result.Discovery.Coverage.BoundaryQualified {
				t.Fatalf("%s bounded Reader round: %s", id, ErrorCode(err))
			}
		}
		for _, order := range []string{"read_first", "watch_first"} {
			began := time.Now()
			if order == "read_first" {
				read()
			}
			watch(ctx, "start")
			deadline := time.Now().Add(60 * time.Second)
			for watch(ctx, "status") != "watching" {
				if time.Now().After(deadline) {
					t.Fatalf("%s %s watch did not become ready", id, order)
				}
				select {
				case <-time.After(time.Second):
				case <-ctx.Done():
					t.Fatal("startup-order wait timed out")
				}
			}
			read()
			read()
			if watch(ctx, "status") != "watching" {
				t.Fatalf("%s %s warm reads retired the watcher", id, order)
			}
			if watch(ctx, "stop") != "stopped" {
				t.Fatalf("%s watch cancel did not complete", id)
			}
			t.Logf("%s %s authenticated cold/warm reads and cancellation passed elapsed=%s", id, order, time.Since(began).Round(time.Millisecond))
		}
	}
}
