package connector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
)

type runtimeRun struct {
	cancel context.CancelFunc
}

type connectorSettingCacheEntry struct {
	setting app.ConnectorSetting
	exists  bool
}

func (r *Registry) Enabled(ownerID, channel string) bool {
	channel = normalizeChannel(channel)
	if channel == "" {
		return false
	}
	if setting, ok := r.connectorSetting(normalizeOwner(ownerID), channel); ok {
		return setting.Enabled
	}
	return r.configuredEnabled(channel)
}

func (r *Registry) SetEnabled(ctx context.Context, ownerID, actorID, channel string, enabled bool, expectedVersion int64) (app.ConnectorStatus, error) {
	channel = normalizeChannel(channel)
	if _, ok := r.registrations[channel]; !ok {
		return app.ConnectorStatus{}, fmt.Errorf("connector channel %q is not registered", channel)
	}
	if r.store == nil {
		return app.ConnectorStatus{}, errors.New("connector setting store is unavailable")
	}
	normalizedOwner := normalizeOwner(ownerID)
	r.settingsMu.Lock()
	setting, _ := r.connectorSettingLocked(normalizedOwner, channel)
	setting.OwnerID = normalizedOwner
	setting.Channel = channel
	setting.Enabled = enabled
	setting.UpdatedBy = strings.TrimSpace(actorID)
	updated, err := r.store.UpdateConnectorSetting(ctx, setting, expectedVersion)
	if err != nil {
		r.settingsMu.Unlock()
		return app.ConnectorStatus{}, err
	}
	r.settings[connectorSettingCacheKey(normalizedOwner, channel)] = connectorSettingCacheEntry{setting: updated, exists: true}
	r.settingsMu.Unlock()
	r.reconcileChannel(channel)
	return r.Status(ctx, ownerID, channel)
}

func (r *Registry) SetMCPTransports(ctx context.Context, ownerID, actorID string, iscpEnabled, lanAccessEnabled bool, expectedVersion int64) (app.ConnectorStatus, error) {
	const channel = "mcp"
	if _, ok := r.registrations[channel]; !ok {
		return app.ConnectorStatus{}, errors.New("MCP connector is not registered")
	}
	if r.store == nil {
		return app.ConnectorStatus{}, errors.New("connector setting store is unavailable")
	}
	normalizedOwner := normalizeOwner(ownerID)
	r.settingsMu.Lock()
	setting, exists := r.connectorSettingLocked(normalizedOwner, channel)
	if !exists {
		setting.Enabled = r.configuredEnabled(channel)
	}
	setting.OwnerID = normalizedOwner
	setting.Channel = channel
	setting.ISCPEnabled = iscpEnabled
	setting.LANAccessEnabled = lanAccessEnabled
	setting.UpdatedBy = strings.TrimSpace(actorID)
	updated, err := r.store.UpdateConnectorSetting(ctx, setting, expectedVersion)
	if err != nil {
		r.settingsMu.Unlock()
		return app.ConnectorStatus{}, err
	}
	r.settings[connectorSettingCacheKey(normalizedOwner, channel)] = connectorSettingCacheEntry{setting: updated, exists: true}
	r.settingsMu.Unlock()
	return r.Status(ctx, ownerID, channel)
}

func (r *Registry) ListStatus(ctx context.Context, ownerID string) ([]app.ConnectorStatus, error) {
	statuses := make([]app.ConnectorStatus, 0, len(r.registrations))
	for _, channel := range r.channels() {
		status, err := r.Status(ctx, ownerID, channel)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (r *Registry) Status(ctx context.Context, ownerID, channel string) (app.ConnectorStatus, error) {
	ownerID = normalizeOwner(ownerID)
	channel = normalizeChannel(channel)
	registration, ok := r.registrations[channel]
	if !ok {
		return app.ConnectorStatus{}, fmt.Errorf("connector channel %q is not registered", channel)
	}
	bindings := []app.NotificationBinding{}
	var setting app.ConnectorSetting
	var hasSetting bool
	if r.store != nil {
		records, err := r.store.ListNotificationBindings(ctx, channel, "")
		if err != nil {
			return app.ConnectorStatus{}, err
		}
		for _, record := range records {
			if normalizeOwner(record.OwnerID) == ownerID {
				bindings = append(bindings, record)
			}
		}
	}
	setting, hasSetting = r.connectorSetting(ownerID, channel)
	capability := r.BindingRouter().CapabilityForOwner(ownerID, channel, bindings)
	enabled := setting.Enabled
	if !hasSetting {
		enabled = r.configuredEnabled(channel)
	}
	var running bool
	var runtimeError string
	if enabled {
		running, runtimeError = r.runtimeStatus(channel)
	}
	status := app.ConnectorStatus{
		Channel: channel, Provider: capability.Provider, SetupKind: registration.SetupKind,
		Available: capability.Available, Enabled: enabled, Running: running,
		BindingStatus: capability.BindingStatus, BindingStartable: enabled && capability.Startable,
		SupportsMultipleBindings: registration.Binding != nil && !registration.Binding.Policy().ExclusiveBinding,
		ISCPEnabled:              setting.ISCPEnabled, LANAccessEnabled: setting.LANAccessEnabled,
		Version: setting.Version, UpdatedAt: setting.UpdatedAt, LastError: runtimeError,
	}
	if registration.ExternalManaged {
		status.BindingStatus = "managed_externally"
		status.BindingStartable = false
	}
	switch {
	case !enabled:
		status.State = app.ConnectorStateDisabled
		status.DisabledReason = binding.CodeUserDisabled
	case !status.Available:
		status.State = app.ConnectorStateUnavailable
		status.DisabledReason = capability.DisabledReason
	case capabilityUnavailable(capability):
		status.State = app.ConnectorStateUnavailable
		status.DisabledReason = capability.DisabledReason
	case runtimeError != "" && !running:
		status.State = app.ConnectorStateError
		status.DisabledReason = "connector_runtime_failed"
	case registration.ExternalManaged:
		status.State = app.ConnectorStateActive
	case capability.BindingStatus == "active":
		status.State = app.ConnectorStateActive
	case capability.BindingStatus == "waiting_scan" || capability.BindingStatus == "waiting_confirm":
		status.State = app.ConnectorStateSetupPending
	case registration.Runtime != nil && !running:
		status.State = app.ConnectorStateStarting
	default:
		status.State = app.ConnectorStateSetupRequired
	}
	if enabled && capability.DisabledReason != "" && capability.DisabledReason != binding.CodeUserDisabled && status.DisabledReason == "" {
		status.DisabledReason = capability.DisabledReason
	}
	if !hasSetting {
		status.Version = 0
		status.UpdatedAt = time.Time{}
	}
	return status, nil
}

func capabilityUnavailable(capability binding.ConnectorCapability) bool {
	switch capability.DisabledReason {
	case "", binding.CodeUserDisabled, binding.CodeOperatorDisabled, binding.CodeBindingInProgress, binding.CodeBindingActive:
		return false
	default:
		return true
	}
}

func (r *Registry) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.runtimeMu.Lock()
	if r.started {
		r.runtimeMu.Unlock()
		return nil
	}
	r.runtimeMu.Unlock()
	if err := r.loadConnectorSettings(ctx); err != nil {
		return fmt.Errorf("load connector settings: %w", err)
	}
	if err := r.recoverNotificationBindings(ctx); err != nil {
		return fmt.Errorf("recover connector bindings: %w", err)
	}
	r.runtimeMu.Lock()
	if r.started {
		r.runtimeMu.Unlock()
		return nil
	}
	r.started = true
	r.runtimeCtx = ctx
	r.runtimeMu.Unlock()
	for _, channel := range r.channels() {
		r.reconcileChannel(channel)
	}
	return nil
}

func (r *Registry) loadConnectorSettings(ctx context.Context) error {
	r.settingsMu.Lock()
	defer r.settingsMu.Unlock()
	settings := []app.ConnectorSetting{}
	if r.store != nil {
		var err error
		settings, err = r.store.ListAllConnectorSettings(ctx)
		if err != nil {
			return err
		}
	}
	loaded := make(map[string]connectorSettingCacheEntry, len(settings))
	for _, setting := range settings {
		ownerID := normalizeOwner(setting.OwnerID)
		channel := normalizeChannel(setting.Channel)
		if channel == "" {
			continue
		}
		setting.OwnerID = ownerID
		setting.Channel = channel
		loaded[connectorSettingCacheKey(ownerID, channel)] = connectorSettingCacheEntry{setting: setting, exists: true}
	}
	r.settings = loaded
	r.settingsLoaded = true
	return nil
}

func (r *Registry) reconcileChannel(channel string) {
	if r.runtimeWanted(channel) {
		r.startChannel(channel)
		return
	}
	r.stopChannel(channel)
}

func (r *Registry) runtimeWanted(channel string) bool {
	channel = normalizeChannel(channel)
	if r.configuredEnabled(channel) {
		return true
	}
	r.settingsMu.RLock()
	defer r.settingsMu.RUnlock()
	for _, entry := range r.settings {
		if entry.exists && entry.setting.Channel == channel && entry.setting.Enabled {
			return true
		}
	}
	return false
}

func (r *Registry) startChannel(channel string) {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	if !r.started || r.runtimeCtx == nil || r.runtimeCtx.Err() != nil {
		return
	}
	registration, ok := r.registrations[channel]
	if !ok || registration.Runtime == nil {
		return
	}
	if _, running := r.runtimeRuns[channel]; running {
		return
	}
	runCtx, cancel := context.WithCancel(r.runtimeCtx)
	run := &runtimeRun{cancel: cancel}
	r.runtimeRuns[channel] = run
	delete(r.runtimeErrors, channel)
	scope := connectorruntime.RuntimeScope{
		Channel:          channel,
		LifecycleContext: r.runtimeCtx,
		OwnerEnabled: func(ownerID string) bool {
			return r.Enabled(ownerID, channel)
		},
	}
	go func() {
		err := registration.Runtime.Run(runCtx, scope)
		r.runtimeMu.Lock()
		restart := false
		if r.runtimeRuns[channel] == run {
			delete(r.runtimeRuns, channel)
			if err != nil && runCtx.Err() == nil {
				r.runtimeErrors[channel] = "connector runtime stopped unexpectedly"
			}
			restart = runCtx.Err() != nil && r.runtimeCtx != nil && r.runtimeCtx.Err() == nil
		}
		r.runtimeMu.Unlock()
		if err != nil && runCtx.Err() == nil {
			slog.Warn("connector runtime stopped", "channel", channel, "code", "connector_runtime_failed")
		}
		if restart && r.runtimeWanted(channel) {
			r.startChannel(channel)
		}
	}()
}

func (r *Registry) stopChannel(channel string) {
	r.runtimeMu.Lock()
	run := r.runtimeRuns[channel]
	delete(r.runtimeErrors, channel)
	r.runtimeMu.Unlock()
	if run != nil {
		run.cancel()
	}
}

func (r *Registry) runtimeStatus(channel string) (bool, string) {
	r.runtimeMu.Lock()
	defer r.runtimeMu.Unlock()
	_, running := r.runtimeRuns[channel]
	return running, r.runtimeErrors[channel]
}

func (r *Registry) connectorSetting(ownerID, channel string) (app.ConnectorSetting, bool) {
	key := connectorSettingCacheKey(ownerID, channel)
	r.settingsMu.RLock()
	entry, cached := r.settings[key]
	r.settingsMu.RUnlock()
	if cached {
		return entry.setting, entry.exists
	}
	return app.ConnectorSetting{}, false
}

func (r *Registry) connectorSettingLocked(ownerID, channel string) (app.ConnectorSetting, bool) {
	if entry, cached := r.settings[connectorSettingCacheKey(ownerID, channel)]; cached {
		return entry.setting, entry.exists
	}
	return app.ConnectorSetting{}, false
}

func (r *Registry) configuredEnabled(channel string) bool {
	channelCfg, ok := r.cfg.Tools.Notifications.Channels[normalizeChannel(channel)]
	return ok && channelCfg.Enabled
}

func connectorSettingCacheKey(ownerID, channel string) string {
	return normalizeOwner(ownerID) + "\x00" + normalizeChannel(channel)
}

func normalizeOwner(ownerID string) string {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return app.DefaultOwnerID
	}
	return ownerID
}

type managedProvider struct {
	registry *Registry
	provider delivery.Provider
}

func (p managedProvider) Key() string { return p.provider.Key() }

func (p managedProvider) Capabilities() app.DeliveryCapabilities {
	return p.provider.Capabilities()
}

func (p managedProvider) Deliver(ctx context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	admittedSourceReply := request.Origin == app.DeliveryOriginSourceReply && request.SourceAdmitted
	if p.registry == nil || (!admittedSourceReply && !p.registry.Enabled(endpoint.OwnerID, p.Key())) {
		err := delivery.NewError(delivery.CodeConnectorDisabled, "delivery connector is disabled", "blocked")
		return app.DeliveryReceipt{
			DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliveryFailed,
			Error: err.Error(), ErrorCode: delivery.CodeConnectorDisabled, RetryState: "blocked", AttemptedAt: time.Now().UTC(),
		}, err
	}
	return p.provider.Deliver(ctx, endpoint, request)
}
