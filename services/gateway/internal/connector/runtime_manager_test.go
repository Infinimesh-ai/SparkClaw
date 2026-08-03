package connector

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type managedRuntime struct {
	starts  atomic.Int32
	started chan struct{}
	stopped chan struct{}
}

func (r *managedRuntime) Run(ctx context.Context) error {
	r.starts.Add(1)
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case r.stopped <- struct{}{}:
	default:
	}
	return nil
}

type managedTestProvider struct {
	deliveries atomic.Int32
}

func (p *managedTestProvider) Key() string { return "alpha" }
func (p *managedTestProvider) Capabilities() app.DeliveryCapabilities {
	return app.DeliveryCapabilities{Kinds: []app.MessagePartKind{app.MessagePartText}}
}
func (p *managedTestProvider) Deliver(_ context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	p.deliveries.Add(1)
	return app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliverySucceeded}, nil
}

func TestRegistryDynamicallyReconcilesPersistedOptInAndGatesDelivery(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: false, Provider: "alpha-v1"},
	}
	st := store.NewMemoryStore()
	runtime := &managedRuntime{started: make(chan struct{}, 1), stopped: make(chan struct{}, 1)}
	provider := &managedTestProvider{}
	registry := NewRegistry(cfg, st)
	if err := registry.Register(Registration{
		Channel: "alpha", SetupKind: app.ConnectorSetupSecret, Binding: registryBindingAdapter{}, Provider: provider, Runtime: runtime,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry.Start(ctx)
	if runtime.starts.Load() != 0 {
		t.Fatal("disabled connector runtime started")
	}
	initial, err := registry.Status(app.DefaultOwnerID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if initial.Enabled || initial.State != app.ConnectorStateDisabled || initial.Version != 0 || initial.BindingStartable {
		t.Fatalf("unexpected initial connector status: %#v", initial)
	}

	enabled, err := registry.SetEnabled(context.Background(), app.DefaultOwnerID, app.DefaultOwnerID, "alpha", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("enabled connector runtime did not start")
	}
	if !enabled.Enabled || !enabled.Running || enabled.State != app.ConnectorStateSetupRequired || enabled.Version != 1 || !enabled.BindingStartable {
		t.Fatalf("unexpected enabled connector status: %#v", enabled)
	}
	providers, err := registry.ProviderRegistry()
	if err != nil {
		t.Fatal(err)
	}
	request := app.DeliveryRequest{
		SchemaVersion: app.DeliveryRequestSchemaVersion, ID: "delivery-alpha", OwnerID: app.DefaultOwnerID,
		Content: app.MessageContent{Parts: []app.MessagePart{{ID: "part-1", Kind: app.MessagePartText, Text: "hello"}}},
	}
	endpoint := app.MessageEndpoint{ID: "endpoint-alpha", ProviderKey: "alpha"}
	if _, err := providers.Deliver(context.Background(), endpoint, request); err != nil {
		t.Fatalf("enabled connector delivery failed: %v", err)
	}

	disabled, err := registry.SetEnabled(context.Background(), app.DefaultOwnerID, app.DefaultOwnerID, "alpha", false, enabled.Version)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.stopped:
	case <-time.After(time.Second):
		t.Fatal("disabled connector runtime did not stop")
	}
	if disabled.Enabled || disabled.Running || disabled.State != app.ConnectorStateDisabled || disabled.Version != 2 {
		t.Fatalf("unexpected disabled connector status: %#v", disabled)
	}
	if receipt, err := providers.Deliver(context.Background(), endpoint, request); err == nil || delivery.ErrorCode(err) != delivery.CodeConnectorDisabled || receipt.ErrorCode != delivery.CodeConnectorDisabled {
		t.Fatalf("disabled connector delivery was not blocked: receipt=%#v err=%v", receipt, err)
	}
}

func TestManagedProviderGatesSourceReplyByAuthorizedEndpointOwner(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: false, Provider: "alpha-v1"},
	}
	st := store.NewMemoryStore()
	if _, err := st.UpdateConnectorSetting(app.ConnectorSetting{
		OwnerID: "owner-a", Channel: "alpha", Enabled: true, UpdatedBy: "owner-a",
	}, 0); err != nil {
		t.Fatal(err)
	}
	bindingRecord := st.SaveNotificationBinding(app.NotificationBinding{
		ID: "bind-alpha", OwnerID: "owner-a", ActorID: "owner-a", Channel: "alpha", Status: "active",
		Scopes: []string{app.BindingScopeMessageSendSelf},
	})
	st.SaveExternalChatSession(app.ExternalChatSession{
		ID: "chat-alpha", OwnerID: "external-actor-a", AuthorizedOwnerID: "owner-a", AuthorizedActorID: "owner-a",
		BindingID: bindingRecord.ID, Channel: "alpha", ExternalUserID: "user-a", ExternalChatID: "chat-a", Status: "active",
	})

	provider := &managedTestProvider{}
	registry := NewRegistry(cfg, st)
	if err := registry.Register(Registration{Channel: "alpha", Provider: provider}); err != nil {
		t.Fatal(err)
	}
	providers, err := registry.ProviderRegistry()
	if err != nil {
		t.Fatal(err)
	}
	endpoints := messagecontrol.NewEndpointRegistry(st).WithChannelEnabled(registry.Enabled)
	routes := messagecontrol.NewReturnRouteResolver(endpoints)
	deliverer := delivery.NewWorkflowResultDeliverer(routes, delivery.NewGateway(endpoints, providers, nil))
	_, err = deliverer.DeliverWorkflowResult(t.Context(), app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion,
		ID:            "result-alpha",
		OwnerID:       "external-actor-a",
		Authorization: app.MessageAuthorization{PrincipalID: "external-actor-a"},
		Content: app.MessageContent{Parts: []app.MessagePart{{
			ID: "part-1", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "reply",
		}}},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "chat-alpha"},
	})
	if err != nil {
		t.Fatalf("enabled authorized owner source reply failed: %v", err)
	}
	if provider.deliveries.Load() != 1 {
		t.Fatalf("source reply deliveries = %d, want 1", provider.deliveries.Load())
	}
}

func TestActiveBindingDoesNotAutoEnableConnector(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: false, Provider: "alpha-v1"},
	}
	st := store.NewMemoryStore()
	st.SaveNotificationBinding(app.NotificationBinding{
		OwnerID: app.DefaultOwnerID, Channel: "alpha", Provider: "alpha-v1", Status: "active",
	})
	registry := NewRegistry(cfg, st)
	if err := registry.Register(Registration{Channel: "alpha", Binding: registryBindingAdapter{}}); err != nil {
		t.Fatal(err)
	}
	status, err := registry.Status(app.DefaultOwnerID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || status.State != app.ConnectorStateDisabled || status.BindingStatus != "active" {
		t.Fatalf("binding incorrectly enabled connector: %#v", status)
	}
}

type unavailableBindingAdapter struct {
	registryBindingAdapter
}

func (unavailableBindingAdapter) Availability() error {
	return &binding.BindingError{Code: binding.CodeConnectorUnavailable}
}

func TestConnectorStatusReportsAdapterAvailabilityFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: true, Provider: "alpha-v1"},
	}
	registry := NewRegistry(cfg)
	if err := registry.Register(Registration{Channel: "alpha", Binding: unavailableBindingAdapter{}}); err != nil {
		t.Fatal(err)
	}
	status, err := registry.Status(app.DefaultOwnerID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != app.ConnectorStateUnavailable || status.BindingStartable || status.DisabledReason != binding.CodeConnectorUnavailable {
		t.Fatalf("unexpected unavailable connector status: %#v", status)
	}
}

func TestRegistryRestoresOnlyPersistedEnabledConnector(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: false, Provider: "alpha-v1"},
	}
	st := store.NewMemoryStore()
	if _, err := st.UpdateConnectorSetting(app.ConnectorSetting{
		OwnerID: app.DefaultOwnerID, Channel: "alpha", Enabled: true, UpdatedBy: app.DefaultOwnerID,
	}, 0); err != nil {
		t.Fatal(err)
	}
	runtime := &managedRuntime{started: make(chan struct{}, 1), stopped: make(chan struct{}, 1)}
	registry := NewRegistry(cfg, st)
	if err := registry.Register(Registration{Channel: "alpha", Binding: registryBindingAdapter{}, Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry.Start(ctx)
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("persisted enabled connector was not restored")
	}
}
