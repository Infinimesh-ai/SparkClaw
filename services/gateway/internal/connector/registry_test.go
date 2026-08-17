package connector

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type registryBindingAdapter struct{}

func (registryBindingAdapter) Availability() error { return nil }
func (registryBindingAdapter) Policy() binding.AdapterPolicy {
	return binding.AdapterPolicy{}
}
func (registryBindingAdapter) Start(_ context.Context, record app.NotificationBinding, _ binding.StartOptions) (app.NotificationBinding, error) {
	record.Status = "waiting_confirm"
	return record, nil
}
func (registryBindingAdapter) Poll(context.Context, app.NotificationBinding) (binding.PollResult, error) {
	return binding.PollResult{Status: "active"}, nil
}
func (registryBindingAdapter) Cancel(context.Context, app.NotificationBinding) error { return nil }

type registryRuntime struct {
	starts atomic.Int32
	ready  chan struct{}
}

func (r *registryRuntime) Run(ctx context.Context, _ connectorruntime.RuntimeScope) error {
	r.starts.Add(1)
	close(r.ready)
	<-ctx.Done()
	return nil
}

func TestRegistryBuildsCapabilityRoutersAndRunsEnabledConnectors(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: true, Provider: "alpha-v1"},
		"beta":  {Enabled: false, Provider: "beta-v1"},
	}
	registry := NewRegistry(cfg)
	alphaRuntime := &registryRuntime{ready: make(chan struct{})}
	betaRuntime := &registryRuntime{ready: make(chan struct{})}
	var canceled string
	if err := registry.Register(Registration{
		Channel: " Alpha ",
		Binding: registryBindingAdapter{},
		Runtime: alphaRuntime,
		CancelBinding: func(record app.NotificationBinding) {
			canceled = record.ID
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Registration{
		Channel: "beta", Binding: registryBindingAdapter{}, Runtime: betaRuntime,
	}); err != nil {
		t.Fatal(err)
	}

	bindingRouter := registry.BindingRouter()
	alpha := bindingRouter.Capability("alpha", nil)
	if !alpha.Available || !alpha.OperatorEnabled || !alpha.Startable || alpha.Provider != "alpha-v1" {
		t.Fatalf("unexpected enabled capability: %#v", alpha)
	}
	beta := bindingRouter.Capability("beta", nil)
	if !beta.Available || beta.OperatorEnabled || beta.Startable || beta.DisabledReason != binding.CodeUserDisabled {
		t.Fatalf("unexpected disabled capability: %#v", beta)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-alphaRuntime.ready:
	case <-time.After(time.Second):
		t.Fatal("enabled runtime did not start")
	}
	if betaRuntime.starts.Load() != 0 {
		t.Fatalf("disabled runtime started %d times", betaRuntime.starts.Load())
	}
	registry.CancelBinding(app.NotificationBinding{ID: "binding-alpha", Channel: "alpha"})
	if canceled != "binding-alpha" {
		t.Fatalf("binding cancellation was not routed: %q", canceled)
	}
	cancel()
}

func TestRegistryRejectsInvalidAndDuplicateChannels(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: true},
	}
	registry := NewRegistry(cfg)
	if err := registry.Register(Registration{}); err == nil {
		t.Fatal("empty channel was accepted")
	}
	if err := registry.Register(Registration{Channel: "missing"}); err == nil {
		t.Fatal("unconfigured channel was accepted")
	}
	if err := registry.Register(Registration{Channel: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Registration{Channel: "ALPHA"}); err == nil {
		t.Fatal("duplicate channel was accepted")
	}
}

func TestRegistryPreservesMCPTransportsAcrossMasterToggle(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	registry := NewRegistry(cfg, st)
	if err := registry.Register(Registration{Channel: "mcp", ExternalManaged: true}); err != nil {
		t.Fatal(err)
	}

	transport, err := registry.SetMCPTransports(t.Context(), app.DefaultOwnerID, app.DefaultOwnerID, true, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if transport.Enabled || !transport.ISCPEnabled || !transport.LANAccessEnabled || transport.Version != 1 {
		t.Fatalf("unexpected initial MCP transport setting: %#v", transport)
	}

	enabled, err := registry.SetEnabled(t.Context(), app.DefaultOwnerID, app.DefaultOwnerID, "mcp", true, transport.Version)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled || !enabled.ISCPEnabled || !enabled.LANAccessEnabled || enabled.Version != 2 {
		t.Fatalf("master enable lost MCP transport settings: %#v", enabled)
	}

	disabled, err := registry.SetEnabled(t.Context(), app.DefaultOwnerID, app.DefaultOwnerID, "mcp", false, enabled.Version)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || !disabled.ISCPEnabled || !disabled.LANAccessEnabled || disabled.Version != 3 {
		t.Fatalf("master disable lost MCP transport settings: %#v", disabled)
	}
}
