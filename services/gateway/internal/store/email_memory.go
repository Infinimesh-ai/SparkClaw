package store

import (
	"context"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) GetEmailProviderSetting(ctx context.Context, ownerID, provider string) (app.EmailProviderSetting, bool, error) {
	ctx, cancel := operationContext(ctx, OperationEmailProviderSettingGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEmailProviderSettingGet, ctx); err != nil {
		return app.EmailProviderSetting{}, false, err
	}
	provider = stringsLowerTrim(provider)
	if !supportedEmailProvider(provider) {
		return app.EmailProviderSetting{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	setting, ok := s.emailProviderSettings[emailProviderKey(ownerID, provider)]
	if !ok {
		return app.EmailProviderSetting{}, false, nil
	}
	if err := validatePersistedEmailProviderSetting(setting); err != nil {
		return app.EmailProviderSetting{}, false, storeError(ctx, OperationEmailProviderSettingGet, StoreErrorCorrupt, err)
	}
	return cloneEmailProviderSetting(setting), true, nil
}

func (s *MemoryStore) ListEmailProviderSettings(ctx context.Context, ownerID string) ([]app.EmailProviderSetting, error) {
	ctx, cancel := operationContext(ctx, OperationEmailProviderSettingList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEmailProviderSettingList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := normalizeAndValidatePersistedEmailProviderState(cloneEmailProviderSettingMap(s.emailProviderSettings)); err != nil {
		return nil, storeError(ctx, OperationEmailProviderSettingList, StoreErrorCorrupt, err)
	}
	return sortedEmailProviderSettings(s.emailProviderSettings, ownerID), nil
}

func (s *MemoryStore) UpdateEmailProviderSetting(ctx context.Context, setting app.EmailProviderSetting, expectedVersion int64) (app.EmailProviderSetting, error) {
	ctx, cancel := operationContext(ctx, OperationEmailProviderSettingUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEmailProviderSettingUpdate, ctx); err != nil {
		return app.EmailProviderSetting{}, err
	}
	candidate, err := normalizeEmailProviderCandidate(setting, expectedVersion)
	if err != nil {
		return app.EmailProviderSetting{}, storeError(ctx, OperationEmailProviderSettingUpdate, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := emailProviderKey(candidate.OwnerID, candidate.Provider)
	current, exists := s.emailProviderSettings[key]
	if exists {
		if err := validatePersistedEmailProviderSetting(current); err != nil {
			return app.EmailProviderSetting{}, storeError(ctx, OperationEmailProviderSettingUpdate, StoreErrorCorrupt, err)
		}
	}
	if (!exists && expectedVersion != 0) || (exists && current.Version != expectedVersion) {
		return app.EmailProviderSetting{}, storeError(ctx, OperationEmailProviderSettingUpdate, StoreErrorConflict, ErrEmailProviderSettingConflict)
	}
	at := nextRepositoryTime(s.connectorNow(), s.emailProviderWriteHighWater[key], current.UpdatedAt)
	if candidate.Default {
		for otherKey, other := range s.emailProviderSettings {
			if otherKey == key || other.OwnerID != candidate.OwnerID || !other.Default {
				continue
			}
			other.Default = false
			other.Version++
			other.UpdatedAt = at
			other.UpdatedBy = candidate.UpdatedBy
			s.emailProviderSettings[otherKey] = cloneEmailProviderSetting(other)
			s.emailProviderWriteHighWater[otherKey] = at
			s.appendAuditLockedAt(at, "email.provider.default_demoted", "", "", candidate.UpdatedBy, other.Provider, emailProviderAuditFields(other))
			s.appendEventLockedAt(at, "email.provider.updated", "", "", other)
		}
	}
	candidate.Version = expectedVersion + 1
	candidate.UpdatedAt = at
	s.emailProviderSettings[key] = cloneEmailProviderSetting(candidate)
	s.emailProviderWriteHighWater[key] = at
	typ := "email.provider.updated"
	if !exists {
		typ = "email.provider.configured"
	}
	s.appendAuditLockedAt(at, typ, "", "", candidate.UpdatedBy, candidate.Provider, emailProviderAuditFields(candidate))
	s.appendEventLockedAt(at, typ, "", "", candidate)
	return cloneEmailProviderSetting(candidate), nil
}

func stringsLowerTrim(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
