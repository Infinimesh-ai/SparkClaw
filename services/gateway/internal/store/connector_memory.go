package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) GetConnectorSetting(ctx context.Context, ownerID, channel string) (app.ConnectorSetting, bool, error) {
	ctx, cancel := operationContext(ctx, OperationConnectorSettingGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConnectorSettingGet, ctx); err != nil {
		return app.ConnectorSetting{}, false, err
	}
	channel = normalizeConnectorChannel(channel)
	if channel == "" {
		return app.ConnectorSetting{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationConnectorSettingGet, ctx); err != nil {
		return app.ConnectorSetting{}, false, err
	}
	setting, ok := s.connectorSettings[connectorSettingKey(ownerID, channel)]
	if !ok {
		return app.ConnectorSetting{}, false, nil
	}
	if err := validatePersistedConnectorSetting(setting); err != nil {
		return app.ConnectorSetting{}, false, storeError(OperationConnectorSettingGet, StoreErrorCorrupt, err)
	}
	return setting, true, nil
}

func (s *MemoryStore) ListConnectorSettings(ctx context.Context, ownerID string) ([]app.ConnectorSetting, error) {
	ctx, cancel := operationContext(ctx, OperationConnectorSettingList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConnectorSettingList, ctx); err != nil {
		return nil, err
	}
	ownerID = normalizeConnectorOwner(ownerID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationConnectorSettingList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.ConnectorSetting, 0)
	for _, setting := range s.connectorSettings {
		if err := validatePersistedConnectorSetting(setting); err != nil {
			return nil, storeError(OperationConnectorSettingList, StoreErrorCorrupt, err)
		}
		if setting.OwnerID == ownerID {
			out = append(out, setting)
		}
	}
	slices.SortFunc(out, func(a, b app.ConnectorSetting) int { return strings.Compare(a.Channel, b.Channel) })
	return out, nil
}

func (s *MemoryStore) ListAllConnectorSettings(ctx context.Context) ([]app.ConnectorSetting, error) {
	ctx, cancel := operationContext(ctx, OperationConnectorSettingListAll, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConnectorSettingListAll, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationConnectorSettingListAll, ctx); err != nil {
		return nil, err
	}
	out := make([]app.ConnectorSetting, 0, len(s.connectorSettings))
	for _, setting := range s.connectorSettings {
		if err := validatePersistedConnectorSetting(setting); err != nil {
			return nil, storeError(OperationConnectorSettingListAll, StoreErrorCorrupt, err)
		}
		out = append(out, setting)
	}
	slices.SortFunc(out, func(a, b app.ConnectorSetting) int {
		if byOwner := strings.Compare(a.OwnerID, b.OwnerID); byOwner != 0 {
			return byOwner
		}
		return strings.Compare(a.Channel, b.Channel)
	})
	return out, nil
}

func (s *MemoryStore) UpdateConnectorSetting(ctx context.Context, setting app.ConnectorSetting, expectedVersion int64) (app.ConnectorSetting, error) {
	ctx, cancel := operationContext(ctx, OperationConnectorSettingUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConnectorSettingUpdate, ctx); err != nil {
		return app.ConnectorSetting{}, err
	}
	setting, err := normalizeConnectorSettingCandidate(setting, expectedVersion)
	if err != nil {
		return app.ConnectorSetting{}, storeError(OperationConnectorSettingUpdate, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationConnectorSettingUpdate, ctx); err != nil {
		return app.ConnectorSetting{}, err
	}
	key := connectorSettingKey(setting.OwnerID, setting.Channel)
	current, exists := s.connectorSettings[key]
	if exists {
		if err := validatePersistedConnectorSetting(current); err != nil {
			return app.ConnectorSetting{}, storeError(OperationConnectorSettingUpdate, StoreErrorCorrupt, err)
		}
	}
	if (!exists && expectedVersion != 0) || (exists && current.Version != expectedVersion) {
		return app.ConnectorSetting{}, storeError(OperationConnectorSettingUpdate, StoreErrorConflict, ErrConnectorSettingConflict)
	}
	at := nextRepositoryTime(s.connectorNow(), s.connectorSettingWriteHighWater[key], current.UpdatedAt)
	setting.Version = expectedVersion + 1
	setting.UpdatedAt = at
	s.connectorSettingWriteHighWater[key] = at
	s.connectorSettings[key] = setting
	typ := connectorSettingAuditType(exists, current.Enabled, current.ISCPEnabled, current.LANAccessEnabled, setting)
	s.appendAuditLockedAt(at, typ, "", "", setting.UpdatedBy, setting.Channel, connectorSettingAuditFields(setting))
	s.appendEventLockedAt(at, typ, "", "", setting)
	return setting, nil
}

func (s *MemoryStore) CreateNotificationBinding(ctx context.Context, binding app.NotificationBinding) (app.NotificationBinding, error) {
	ctx, cancel := operationContext(ctx, OperationNotificationBindingCreate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationNotificationBindingCreate, ctx); err != nil {
		return app.NotificationBinding{}, err
	}
	binding, err := normalizeNotificationBindingCreate(binding)
	if err != nil {
		return app.NotificationBinding{}, storeError(OperationNotificationBindingCreate, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationNotificationBindingCreate, ctx); err != nil {
		return app.NotificationBinding{}, err
	}
	if _, exists := s.notificationBindings[binding.ID]; exists {
		return app.NotificationBinding{}, storeError(OperationNotificationBindingCreate, StoreErrorConflict, errors.New("notification binding already exists"))
	}
	at := nextRepositoryTime(s.connectorNow(), s.notificationBindingWriteHighWater[binding.ID])
	binding.Version = 1
	binding.CreatedAt = at
	binding.UpdatedAt = at
	s.notificationBindingWriteHighWater[binding.ID] = at
	s.notificationBindings[binding.ID] = cloneNotificationBinding(binding)
	s.appendNotificationBindingLifecycleLocked(at, binding)
	return cloneNotificationBinding(binding), nil
}

func (s *MemoryStore) GetNotificationBinding(ctx context.Context, id string) (app.NotificationBinding, bool, error) {
	ctx, cancel := operationContext(ctx, OperationNotificationBindingGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationNotificationBindingGet, ctx); err != nil {
		return app.NotificationBinding{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.NotificationBinding{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationNotificationBindingGet, ctx); err != nil {
		return app.NotificationBinding{}, false, err
	}
	binding, ok := s.notificationBindings[id]
	if !ok {
		return app.NotificationBinding{}, false, nil
	}
	if err := validatePersistedNotificationBinding(binding); err != nil {
		return app.NotificationBinding{}, false, storeError(OperationNotificationBindingGet, StoreErrorCorrupt, err)
	}
	return cloneNotificationBinding(binding), true, nil
}

func (s *MemoryStore) ListNotificationBindings(ctx context.Context, channel, status string) ([]app.NotificationBinding, error) {
	ctx, cancel := operationContext(ctx, OperationNotificationBindingList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationNotificationBindingList, ctx); err != nil {
		return nil, err
	}
	channel = normalizeConnectorChannel(channel)
	status = strings.TrimSpace(status)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationNotificationBindingList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.NotificationBinding, 0)
	vaultOwners := map[string]string{}
	activeDefaults := map[string]string{}
	for _, binding := range s.notificationBindings {
		if err := validatePersistedNotificationBinding(binding); err != nil {
			return nil, storeError(OperationNotificationBindingList, StoreErrorCorrupt, err)
		}
		if err := claimBindingCredentialRef(vaultOwners, binding); err != nil {
			return nil, storeError(OperationNotificationBindingList, StoreErrorCorrupt, err)
		}
		if binding.Status == app.NotificationBindingActive && binding.DefaultForChannel {
			key := connectorSettingKey(binding.OwnerID, binding.Channel)
			if activeDefaults[key] != "" {
				return nil, storeError(OperationNotificationBindingList, StoreErrorCorrupt, errors.New("multiple active default bindings"))
			}
			activeDefaults[key] = binding.ID
		}
		if (channel == "" || binding.Channel == channel) && (status == "" || binding.Status == status) {
			out = append(out, cloneNotificationBinding(binding))
		}
	}
	slices.SortFunc(out, compareNotificationBindings)
	return out, nil
}

func (s *MemoryStore) UpdateNotificationBinding(ctx context.Context, command NotificationBindingUpdateCommand) (app.NotificationBinding, error) {
	ctx, cancel := operationContext(ctx, OperationNotificationBindingUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationNotificationBindingUpdate, ctx); err != nil {
		return app.NotificationBinding{}, err
	}
	command, err := normalizeNotificationBindingUpdateCommand(command)
	if err != nil {
		return app.NotificationBinding{}, storeError(OperationNotificationBindingUpdate, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationNotificationBindingUpdate, ctx); err != nil {
		return app.NotificationBinding{}, err
	}
	previous, ok := s.notificationBindings[command.id]
	if !ok {
		return app.NotificationBinding{}, storeError(OperationNotificationBindingUpdate, StoreErrorNotFound, errors.New("notification binding not found"))
	}
	if err := validatePersistedNotificationBinding(previous); err != nil {
		return app.NotificationBinding{}, storeError(OperationNotificationBindingUpdate, StoreErrorCorrupt, err)
	}
	if notificationBindingDigest(previous) != command.expected {
		return app.NotificationBinding{}, storeError(OperationNotificationBindingUpdate, StoreErrorConflict, errors.New("notification binding changed"))
	}
	at := s.nextNotificationBindingCommandTimeLocked(previous, command.next)
	candidate, err := prepareNotificationBindingUpdate(previous, command.next, at)
	if err != nil {
		return app.NotificationBinding{}, storeError(OperationNotificationBindingUpdate, StoreErrorInvalid, err)
	}
	if err := s.validateNotificationBindingOwnershipLocked(candidate); err != nil {
		return app.NotificationBinding{}, err
	}
	if candidate.Status == app.NotificationBindingActive && candidate.DefaultForChannel {
		for id, existing := range s.notificationBindings {
			if id == candidate.ID || existing.OwnerID != candidate.OwnerID || existing.Channel != candidate.Channel || existing.Status != app.NotificationBindingActive || !existing.DefaultForChannel {
				continue
			}
			existing = cloneNotificationBinding(existing)
			existing.DefaultForChannel = false
			existing.Version++
			existing.UpdatedAt = at
			s.notificationBindingWriteHighWater[id] = at
			s.notificationBindings[id] = existing
			s.appendAuditLockedAt(at, "notification_binding.default_demoted", "", "", "system", existing.Channel, notificationBindingAuditFields(existing))
			s.appendEventLockedAt(at, "notification_binding.default_demoted", "", "", cloneNotificationBinding(existing))
		}
	}
	s.notificationBindingWriteHighWater[candidate.ID] = at
	s.notificationBindings[candidate.ID] = cloneNotificationBinding(candidate)
	s.appendNotificationBindingLifecycleLocked(at, candidate)
	return cloneNotificationBinding(candidate), nil
}

func (s *MemoryStore) nextNotificationBindingCommandTimeLocked(previous, replacement app.NotificationBinding) time.Time {
	highWater := []time.Time{s.notificationBindingWriteHighWater[previous.ID], latestNotificationBindingTime(previous)}
	if replacement.Status == app.NotificationBindingActive && replacement.DefaultForChannel {
		for id, existing := range s.notificationBindings {
			if id != previous.ID && existing.OwnerID == previous.OwnerID && existing.Channel == previous.Channel && existing.Status == app.NotificationBindingActive && existing.DefaultForChannel {
				highWater = append(highWater, s.notificationBindingWriteHighWater[id], latestNotificationBindingTime(existing))
			}
		}
	}
	return nextRepositoryTime(s.connectorNow(), highWater...)
}

func (s *MemoryStore) validateNotificationBindingOwnershipLocked(candidate app.NotificationBinding) error {
	owners := map[string]string{}
	for id, existing := range s.notificationBindings {
		if id == candidate.ID {
			continue
		}
		if err := validatePersistedNotificationBinding(existing); err != nil {
			return storeError(OperationNotificationBindingUpdate, StoreErrorCorrupt, err)
		}
		if err := claimBindingCredentialRef(owners, existing); err != nil {
			return storeError(OperationNotificationBindingUpdate, StoreErrorCorrupt, err)
		}
	}
	if err := claimBindingCredentialRef(owners, candidate); err != nil {
		return storeError(OperationNotificationBindingUpdate, StoreErrorConflict, err)
	}
	return nil
}

func compareNotificationBindings(left, right app.NotificationBinding) int {
	if ordered := right.UpdatedAt.Compare(left.UpdatedAt); ordered != 0 {
		return ordered
	}
	return strings.Compare(left.ID, right.ID)
}

func connectorSettingAuditFields(setting app.ConnectorSetting) map[string]any {
	return map[string]any{
		"owner_id": setting.OwnerID, "channel": setting.Channel, "enabled": setting.Enabled,
		"iscp_enabled": setting.ISCPEnabled, "lan_access_enabled": setting.LANAccessEnabled,
		"version": setting.Version,
	}
}

func notificationBindingAuditFields(binding app.NotificationBinding) map[string]any {
	return map[string]any{
		"binding_id": binding.ID, "channel": binding.Channel, "provider": binding.Provider,
		"default": binding.DefaultForChannel, "version": binding.Version,
	}
}

func (s *MemoryStore) appendNotificationBindingLifecycleLocked(at time.Time, binding app.NotificationBinding) {
	typ := "notification_binding." + binding.Status
	s.appendAuditLockedAt(at, typ, "", "", "owner", binding.Channel, notificationBindingAuditFields(binding))
	s.appendEventLockedAt(at, typ, "", "", cloneNotificationBinding(binding))
}
