package connector

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type managedRuntime struct {
	starts  atomic.Int32
	started chan struct{}
	stopped chan struct{}
	scopes  chan connectorruntime.RuntimeScope
}

func (r *managedRuntime) Run(ctx context.Context, scope connectorruntime.RuntimeScope) error {
	r.starts.Add(1)
	select {
	case r.scopes <- scope:
	default:
	}
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

type drainingRuntime struct {
	starts        atomic.Int32
	firstStarted  chan struct{}
	firstCanceled chan struct{}
	releaseFirst  chan struct{}
	secondStarted chan struct{}
}

func (r *drainingRuntime) Run(ctx context.Context, _ connectorruntime.RuntimeScope) error {
	switch r.starts.Add(1) {
	case 1:
		close(r.firstStarted)
		<-ctx.Done()
		close(r.firstCanceled)
		<-r.releaseFirst
	case 2:
		close(r.secondStarted)
		<-ctx.Done()
	default:
		<-ctx.Done()
	}
	return nil
}

type countingConnectorStore struct {
	*store.MemoryStore
	gets    atomic.Int32
	lists   atomic.Int32
	listErr error
}

func (s *countingConnectorStore) GetConnectorSetting(ownerID, channel string) (app.ConnectorSetting, bool) {
	s.gets.Add(1)
	return s.MemoryStore.GetConnectorSetting(ownerID, channel)
}

func (s *countingConnectorStore) ListAllConnectorSettings() ([]app.ConnectorSetting, error) {
	s.lists.Add(1)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.MemoryStore.ListAllConnectorSettings()
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
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
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

func TestRegistrySharesOneRuntimeWithoutCrossOwnerDisable(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: false, Provider: "alpha-v1"},
	}
	st := store.NewMemoryStore()
	for _, ownerID := range []string{"owner-a", "owner-b"} {
		if _, err := st.UpdateConnectorSetting(app.ConnectorSetting{
			OwnerID: ownerID, Channel: "alpha", Enabled: true, UpdatedBy: ownerID,
		}, 0); err != nil {
			t.Fatal(err)
		}
	}
	runtime := &managedRuntime{
		started: make(chan struct{}, 1), stopped: make(chan struct{}, 1), scopes: make(chan connectorruntime.RuntimeScope, 1),
	}
	registry := NewRegistry(cfg, st)
	if err := registry.Register(Registration{Channel: "alpha", Binding: registryBindingAdapter{}, Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("shared runtime did not start")
	}
	scope := <-runtime.scopes
	if runtime.starts.Load() != 1 || !scope.AllowsOwner("owner-a") || !scope.AllowsOwner("owner-b") {
		t.Fatalf("unexpected initial shared scope: starts=%d owner-a=%v owner-b=%v", runtime.starts.Load(), scope.AllowsOwner("owner-a"), scope.AllowsOwner("owner-b"))
	}
	disabledA, err := registry.SetEnabled(t.Context(), "owner-a", "owner-a", "alpha", false, 1)
	if err != nil {
		t.Fatal(err)
	}
	if disabledA.Enabled || disabledA.Running || scope.AllowsOwner("owner-a") || !scope.AllowsOwner("owner-b") {
		t.Fatalf("owner disable crossed runtime scope: status=%#v owner-a=%v owner-b=%v", disabledA, scope.AllowsOwner("owner-a"), scope.AllowsOwner("owner-b"))
	}
	select {
	case <-runtime.stopped:
		t.Fatal("disabling owner-a stopped owner-b's shared runtime")
	case <-time.After(50 * time.Millisecond):
	}
	statusB, err := registry.Status("owner-b", "alpha")
	if err != nil || !statusB.Enabled || !statusB.Running {
		t.Fatalf("owner-b lost runtime after owner-a disable: %#v err=%v", statusB, err)
	}
	if _, err := registry.SetEnabled(t.Context(), "owner-b", "owner-b", "alpha", false, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.stopped:
	case <-time.After(time.Second):
		t.Fatal("last enabled owner did not stop default-off runtime")
	}
	if runtime.starts.Load() != 1 {
		t.Fatalf("shared runtime starts = %d, want 1", runtime.starts.Load())
	}
}

func TestRegistryConfiguredDefaultRemainsFallbackPerOwner(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: true, Provider: "alpha-v1"},
	}
	st := store.NewMemoryStore()
	if _, err := st.UpdateConnectorSetting(app.ConnectorSetting{OwnerID: "owner-a", Channel: "alpha", Enabled: false}, 0); err != nil {
		t.Fatal(err)
	}
	runtime := &managedRuntime{
		started: make(chan struct{}, 1), stopped: make(chan struct{}, 1), scopes: make(chan connectorruntime.RuntimeScope, 1),
	}
	registry := NewRegistry(cfg, st)
	if err := registry.Register(Registration{Channel: "alpha", Binding: registryBindingAdapter{}, Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	<-runtime.started
	scope := <-runtime.scopes
	if scope.AllowsOwner("owner-a") || !scope.AllowsOwner("owner-without-setting") {
		t.Fatalf("configured default fallback mismatch: owner-a=%v owner-without-setting=%v", scope.AllowsOwner("owner-a"), scope.AllowsOwner("owner-without-setting"))
	}
	statusA, err := registry.Status("owner-a", "alpha")
	if err != nil || statusA.Enabled || statusA.Running {
		t.Fatalf("explicit owner opt-out leaked shared runtime state: %#v err=%v", statusA, err)
	}
}

func TestRegistryPreloadsSettingsAndKeepsHotPathInMemory(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: false, Provider: "alpha-v1"},
	}
	memory := store.NewMemoryStore()
	if _, err := memory.UpdateConnectorSetting(app.ConnectorSetting{OwnerID: "owner-a", Channel: "alpha", Enabled: true}, 0); err != nil {
		t.Fatal(err)
	}
	st := &countingConnectorStore{MemoryStore: memory}
	registry := NewRegistry(cfg, st)
	if err := registry.Register(Registration{Channel: "alpha", Binding: registryBindingAdapter{}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if !registry.Enabled("owner-a", "alpha") {
			t.Fatal("preloaded owner setting was not enabled")
		}
		if _, err := registry.Status("owner-a", "alpha"); err != nil {
			t.Fatal(err)
		}
	}
	if st.lists.Load() != 1 || st.gets.Load() != 0 {
		t.Fatalf("connector hot path store reads: lists=%d gets=%d", st.lists.Load(), st.gets.Load())
	}
	updated, err := registry.SetEnabled(t.Context(), "owner-a", "owner-a", "alpha", false, 1)
	if err != nil || updated.Enabled || st.gets.Load() != 0 {
		t.Fatalf("write-through cache update = %#v err=%v gets=%d", updated, err, st.gets.Load())
	}
	if _, err := registry.SetEnabled(t.Context(), "owner-a", "owner-a", "alpha", true, 1); !errors.Is(err, store.ErrConnectorSettingConflict) {
		t.Fatalf("stale CAS error = %v", err)
	}
	if registry.Enabled("owner-a", "alpha") {
		t.Fatal("stale CAS changed cached connector setting")
	}
}

func TestRegistryStartFailsWhenAllOwnerPreloadFails(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: true, Provider: "alpha-v1"},
	}
	wantErr := errors.New("connector settings unavailable")
	st := &countingConnectorStore{MemoryStore: store.NewMemoryStore(), listErr: wantErr}
	runtime := &managedRuntime{started: make(chan struct{}, 1), stopped: make(chan struct{}, 1)}
	registry := NewRegistry(cfg, st)
	if err := registry.Register(Registration{Channel: "alpha", Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Start(t.Context()); !errors.Is(err, wantErr) {
		t.Fatalf("startup preload error = %v", err)
	}
	if runtime.starts.Load() != 0 {
		t.Fatal("runtime started after connector setting preload failure")
	}
}

func TestRegistryReenableWaitsForDrainingRuntimeBeforeRestart(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: false, Provider: "alpha-v1"},
	}
	st := store.NewMemoryStore()
	runtime := &drainingRuntime{
		firstStarted:  make(chan struct{}),
		firstCanceled: make(chan struct{}),
		releaseFirst:  make(chan struct{}, 1),
		secondStarted: make(chan struct{}),
	}
	registry := NewRegistry(cfg, st)
	if err := registry.Register(Registration{Channel: "alpha", Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		select {
		case runtime.releaseFirst <- struct{}{}:
		default:
		}
	}()
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SetEnabled(t.Context(), "owner-a", "owner-a", "alpha", true, 0); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first connector runtime did not start")
	}
	if _, err := registry.SetEnabled(t.Context(), "owner-a", "owner-a", "alpha", false, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.firstCanceled:
	case <-time.After(time.Second):
		t.Fatal("connector runtime did not enter drain")
	}
	if _, err := registry.SetEnabled(t.Context(), "owner-a", "owner-a", "alpha", true, 2); err != nil {
		t.Fatal(err)
	}
	if got := runtime.starts.Load(); got != 1 {
		t.Fatalf("connector runtime starts during drain = %d, want 1", got)
	}
	select {
	case <-runtime.secondStarted:
		t.Fatal("second connector runtime started before the draining run exited")
	default:
	}
	runtime.releaseFirst <- struct{}{}
	select {
	case <-runtime.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("connector runtime did not restart after drain")
	}
	if got := runtime.starts.Load(); got != 2 {
		t.Fatalf("connector runtime starts after drain = %d, want 2", got)
	}
}

func TestAdmittedSourceReplyDrainsAfterOwnerDisablesConnector(t *testing.T) {
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
	admittedEndpoint, err := endpoints.Get(t.Context(), "chat-alpha")
	if err != nil {
		t.Fatalf("enabled source endpoint admission failed: %v", err)
	}
	if _, err := registry.SetEnabled(t.Context(), "owner-a", "owner-a", "alpha", false, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := endpoints.Get(t.Context(), "chat-alpha"); delivery.ErrorCode(err) != delivery.CodeConnectorDisabled {
		t.Fatalf("disabled connector still resolved a new endpoint: %v", err)
	}
	routes := messagecontrol.NewReturnRouteResolver(endpoints)
	result := app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion,
		ID:            "result-alpha",
		OwnerID:       "external-actor-a",
		Authorization: app.MessageAuthorization{PrincipalID: "external-actor-a"},
		Content: app.MessageContent{Parts: []app.MessagePart{{
			ID: "part-1", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "reply",
		}}},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "chat-alpha", SourceAdmitted: true},
	}
	request, deliverable, err := delivery.RequestFromWorkflowResult(t.Context(), result, routes)
	if err != nil || !deliverable || request.Origin != app.DeliveryOriginSourceReply {
		t.Fatalf("admitted source request projection failed: request=%#v deliverable=%v err=%v", request, deliverable, err)
	}
	_, err = delivery.NewGateway(endpoints, providers, nil).Deliver(t.Context(), request)
	if err != nil {
		t.Fatalf("admitted source reply did not drain after disable: %v", err)
	}
	if provider.deliveries.Load() != 1 {
		t.Fatalf("source reply deliveries = %d, want 1", provider.deliveries.Load())
	}
	activeRequest := app.DeliveryRequest{
		SchemaVersion: app.DeliveryRequestSchemaVersion,
		ID:            "active-after-disable",
		OwnerID:       "owner-a",
		Origin:        app.DeliveryOriginWebDirect,
		Content: app.MessageContent{Parts: []app.MessagePart{{
			ID: "part-active", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "new send",
		}}},
	}
	if _, err := providers.Deliver(t.Context(), admittedEndpoint, activeRequest); delivery.ErrorCode(err) != delivery.CodeConnectorDisabled {
		t.Fatalf("new active delivery bypassed owner disable: %v", err)
	}
	if provider.deliveries.Load() != 1 {
		t.Fatalf("blocked active delivery reached provider: %d", provider.deliveries.Load())
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

func TestRegistryRestoresNonDefaultOwnerPersistedConnector(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels = map[string]config.NotificationChannelConfig{
		"alpha": {Enabled: false, Provider: "alpha-v1"},
	}
	st := store.NewMemoryStore()
	if _, err := st.UpdateConnectorSetting(app.ConnectorSetting{
		OwnerID: "owner-b", Channel: "alpha", Enabled: true, UpdatedBy: "owner-b",
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
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("persisted enabled connector was not restored")
	}
}
