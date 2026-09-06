package browserautomation

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestPlaywrightExtensionLiveAdapter(t *testing.T) {
	targetURL := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EXTENSION_URL"))
	if targetURL == "" {
		t.Skip("set SPARKCLAW_TEST_PLAYWRIGHT_EXTENSION_URL to run the live extension adapter acceptance test")
	}
	configPath := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_CONFIG"))
	if configPath == "" {
		configPath = "configs/sparkclaw.default.json"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load live config: %v", err)
	}
	runtime, err := newPlaywrightLiveStoreRuntime(ctx, cfg)
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
	if _, err := controller.Check(ctx); err != nil {
		t.Fatalf("validate saved extension credential: %v", err)
	}
	t.Cleanup(func() {
		if err := controller.Close(); err != nil {
			t.Errorf("close live browser controller: %v", err)
		}
	})

	adapter := NewPlaywrightExtensionAdapter(cfg, controller).(*PlaywrightExtensionAdapter)
	t.Cleanup(func() {
		if err := adapter.Close(); err != nil {
			t.Errorf("close live Playwright adapter: %v", err)
		}
	})
	baseArgs := map[string]any{
		"owner_id": "playwright-live-qualification", "browser_profile_id": extensionCfg.ProfileID,
	}
	opened, err := adapter.Call(ctx, "browser.open", mergeArgs(baseArgs, map[string]any{"url": targetURL}))
	if err != nil {
		t.Fatalf(
			"open live fixture: %v (code=%s retryable=%t cause=%v)",
			err, browsercontrol.ErrorCode(err), browsercontrol.ErrorRetryable(err), errors.Unwrap(err),
		)
	}
	pageID := selectedPageID(mapValue(opened.Output))
	if pageID == "" {
		t.Fatalf("live open returned no selected task page: %#v", opened.Output)
	}
	pageArgs := mergeArgs(baseArgs, map[string]any{"page_id": pageID})

	read, err := adapter.ReadPage(ctx, targetURL, mergeArgs(pageArgs, map[string]any{"reuse_active_page": true}))
	if err != nil {
		t.Fatalf("read live fixture: %v", err)
	}
	if !strings.Contains(read.Text, "playwright-extension-adapter-live-fixture") {
		t.Fatalf("live fixture marker missing from rendered text: %q", read.Text)
	}

	snapshot := takePlaywrightTestSnapshot(t, adapter, pageArgs)
	nameRef := playwrightTestControlRef(t, snapshot, "Name")
	if _, err := adapter.Call(ctx, "browser.type", mergeArgs(pageArgs, map[string]any{
		"ref": nameRef, "text": "SparkClaw",
	})); err != nil {
		t.Fatalf("fill live fixture: %v", err)
	}

	snapshot = takePlaywrightTestSnapshot(t, adapter, pageArgs)
	countryRef := playwrightTestControlRef(t, snapshot, "Country")
	if _, err := adapter.Call(ctx, "browser.select", mergeArgs(pageArgs, map[string]any{
		"ref": countryRef, "value": "US",
	})); err != nil {
		t.Fatalf("select live fixture: %v", err)
	}

	snapshot = takePlaywrightTestSnapshot(t, adapter, pageArgs)
	buttonRef := playwrightTestControlRef(t, snapshot, "Advance")
	beforeDigest := firstStringValue(snapshot, "content_digest")
	if _, err := adapter.Call(ctx, "browser.click", mergeArgs(pageArgs, map[string]any{"ref": buttonRef})); err != nil {
		t.Fatalf("click live fixture: %v", err)
	}
	if _, err := adapter.Call(ctx, "browser.wait", mergeArgs(pageArgs, map[string]any{
		"mode": "stable_state", "expected_url": targetURL, "before_digest": beforeDigest,
		"timeout_ms": 10000, "quiet_period_ms": 300, "poll_interval_ms": 100,
	})); err != nil {
		t.Fatalf("settle live fixture: %v", err)
	}

	read, err = adapter.ReadPage(ctx, targetURL, mergeArgs(pageArgs, map[string]any{"reuse_active_page": true}))
	if err != nil {
		t.Fatalf("read changed live fixture: %v", err)
	}
	if !strings.Contains(read.Text, "advanced:SparkClaw:US") {
		t.Fatalf("live interaction effect missing from rendered text: %q", read.Text)
	}
	if os.Getenv("SPARKCLAW_TEST_PLAYWRIGHT_EXTENSION_HANDOFF") == "1" {
		focused, err := adapter.Call(ctx, "browser.focus", pageArgs)
		if err != nil {
			t.Fatalf("handoff live task page: %v", err)
		}
		if !boolValue(mapValue(focused.Output)["selected"]) {
			t.Fatalf("live handoff did not select the task page: %#v", focused.Output)
		}
		time.Sleep(5 * time.Second)
	}
	screenshot, err := adapter.Call(ctx, "browser.screenshot", pageArgs)
	if err != nil {
		t.Fatalf("capture live screenshot: %v", err)
	}
	content, ok := mapValue(screenshot.Output)["content"].([]any)
	if !ok || len(content) != 1 || firstStringValue(mapValue(content[0]), "type") != "image" {
		t.Fatalf("live screenshot response is invalid: %#v", screenshot.Output)
	}
	if _, err := adapter.Call(ctx, "browser.close", pageArgs); err != nil {
		t.Fatalf("close live task page: %v", err)
	}
}

func newPlaywrightLiveStoreRuntime(ctx context.Context, cfg config.Config) (*store.Runtime, error) {
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
