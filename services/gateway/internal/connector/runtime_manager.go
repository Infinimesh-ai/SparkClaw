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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
)

type runtimeRun struct {
	cancel context.CancelFunc
}

func (r *Registry) Enabled(ownerID, channel string) bool {
	channel = normalizeChannel(channel)
	if channel == "" {
		return false
	}
	if r.store != nil {
		if setting, ok := r.store.GetConnectorSetting(normalizeOwner(ownerID), channel); ok {
			return setting.Enabled
		}
	}
	channelCfg, ok := r.cfg.Tools.Notifications.Channels[channel]
	return ok && channelCfg.Enabled
}

func (r *Registry) SetEnabled(_ context.Context, ownerID, actorID, channel string, enabled bool, expectedVersion int64) (app.ConnectorStatus, error) {
	channel = normalizeChannel(channel)
	if _, ok := r.registrations[channel]; !ok {
		return app.ConnectorStatus{}, fmt.Errorf("connector channel %q is not registered", channel)
	}
	if r.store == nil {
		return app.ConnectorStatus{}, errors.New("connector setting store is unavailable")
	}
	if _, err := r.store.UpdateConnectorSetting(app.ConnectorSetting{
		OwnerID: normalizeOwner(ownerID), Channel: channel, Enabled: enabled, UpdatedBy: strings.TrimSpace(actorID),
	}, expectedVersion); err != nil {
		return app.ConnectorStatus{}, err
	}
	if enabled {
		r.startChannel(channel)
	} else {
		r.stopChannel(channel)
	}
	return r.Status(ownerID, channel)
}

func (r *Registry) ListStatus(ownerID string) []app.ConnectorStatus {
	statuses := make([]app.ConnectorStatus, 0, len(r.registrations))
	for _, channel := range r.channels() {
		status, err := r.Status(ownerID, channel)
		if err == nil {
			statuses = append(statuses, status)
		}
	}
	return statuses
}

func (r *Registry) Status(ownerID, channel string) (app.ConnectorStatus, error) {
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
		for _, record := range r.store.ListNotificationBindings(channel, "") {
			if normalizeOwner(record.OwnerID) == ownerID {
				bindings = append(bindings, record)
			}
		}
		setting, hasSetting = r.store.GetConnectorSetting(ownerID, channel)
	}
	capability := r.BindingRouter().CapabilityForOwner(ownerID, channel, bindings)
	enabled := r.Enabled(ownerID, channel)
	running, runtimeError := r.runtimeStatus(channel)
	status := app.ConnectorStatus{
		Channel: channel, Provider: capability.Provider, SetupKind: registration.SetupKind,
		Available: capability.Available, Enabled: enabled, Running: running,
		BindingStatus: capability.BindingStatus, BindingStartable: enabled && capability.Startable,
		SupportsMultipleBindings: registration.Binding != nil && !registration.Binding.Policy().ExclusiveBinding,
		Version:                  setting.Version, UpdatedAt: setting.UpdatedAt, LastError: runtimeError,
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

func (r *Registry) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.runtimeMu.Lock()
	if r.started {
		r.runtimeMu.Unlock()
		return
	}
	r.started = true
	r.runtimeCtx = ctx
	r.runtimeMu.Unlock()
	for _, channel := range r.channels() {
		if r.Enabled(app.DefaultOwnerID, channel) {
			r.startChannel(channel)
		}
	}
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
	go func() {
		err := registration.Runtime.Run(runCtx)
		r.runtimeMu.Lock()
		if r.runtimeRuns[channel] == run {
			delete(r.runtimeRuns, channel)
			if err != nil && runCtx.Err() == nil {
				r.runtimeErrors[channel] = "connector runtime stopped unexpectedly"
			}
		}
		r.runtimeMu.Unlock()
		if err != nil && runCtx.Err() == nil {
			slog.Warn("connector runtime stopped", "channel", channel, "code", "connector_runtime_failed")
		}
	}()
}

func (r *Registry) stopChannel(channel string) {
	r.runtimeMu.Lock()
	run := r.runtimeRuns[channel]
	delete(r.runtimeRuns, channel)
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
	if p.registry == nil || !p.registry.Enabled(request.OwnerID, p.Key()) {
		err := delivery.NewError(delivery.CodeConnectorDisabled, "delivery connector is disabled", "blocked")
		return app.DeliveryReceipt{
			DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliveryFailed,
			Error: err.Error(), ErrorCode: delivery.CodeConnectorDisabled, RetryState: "blocked", AttemptedAt: time.Now().UTC(),
		}, err
	}
	return p.provider.Deliver(ctx, endpoint, request)
}
