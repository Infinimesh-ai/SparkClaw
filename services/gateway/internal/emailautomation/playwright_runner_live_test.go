package emailautomation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	registry := DefaultRegistry("")
	runner := NewPlaywrightRunner(&liveEmailController{Service: controller, t: t})
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
		})
	}
	if len(seen) == 0 {
		t.Fatal("live email provider list is empty")
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
