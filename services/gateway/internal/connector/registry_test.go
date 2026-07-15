package connector

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
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

type registryNotificationAdapter struct {
	channel string
}

func (a registryNotificationAdapter) Send(_ context.Context, request notification.Notification) (notification.Result, error) {
	return notification.Result{Channel: a.channel, Status: "sent", Recipient: request.Recipient}, nil
}

type registryRuntime struct {
	starts atomic.Int32
	ready  chan struct{}
}

func (r *registryRuntime) Run(ctx context.Context) error {
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
	st := store.NewMemoryStore()
	registry := NewRegistry(cfg, st)
	alphaRuntime := &registryRuntime{ready: make(chan struct{})}
	betaRuntime := &registryRuntime{ready: make(chan struct{})}
	var canceled string
	if err := registry.Register(Registration{
		Channel:      " Alpha ",
		Binding:      registryBindingAdapter{},
		Notification: registryNotificationAdapter{channel: "alpha"},
		Runtime:      alphaRuntime,
		CancelBinding: func(record app.NotificationBinding) {
			canceled = record.ID
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Registration{
		Channel:      "beta",
		Binding:      registryBindingAdapter{},
		Notification: registryNotificationAdapter{channel: "beta"},
		Runtime:      betaRuntime,
	}); err != nil {
		t.Fatal(err)
	}

	bindingRouter := registry.BindingRouter()
	alpha := bindingRouter.Capability("alpha", nil)
	if !alpha.Available || !alpha.OperatorEnabled || !alpha.Startable || alpha.Provider != "alpha-v1" {
		t.Fatalf("unexpected enabled capability: %#v", alpha)
	}
	beta := bindingRouter.Capability("beta", nil)
	if !beta.Available || beta.OperatorEnabled || beta.Startable || beta.DisabledReason != binding.CodeOperatorDisabled {
		t.Fatalf("unexpected disabled capability: %#v", beta)
	}

	notificationRouter := registry.NotificationRouter()
	if result, err := notificationRouter.Send(context.Background(), notification.Notification{Channel: "alpha", Recipient: "owner"}); err != nil || result.Status != "sent" {
		t.Fatalf("enabled notification failed: result=%#v err=%v", result, err)
	}
	if _, err := notificationRouter.Send(context.Background(), notification.Notification{Channel: "beta"}); err == nil {
		t.Fatal("disabled notification adapter was registered")
	}

	ctx, cancel := context.WithCancel(context.Background())
	registry.Start(ctx)
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
	registry := NewRegistry(cfg, store.NewMemoryStore())
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
