package store

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) GetOwnerProfile(ctx context.Context) (app.OwnerProfile, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationOwnerProfileGet, ctx); err != nil {
		return app.OwnerProfile{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationOwnerProfileGet, ctx); err != nil {
		return app.OwnerProfile{}, err
	}
	profile, ok := s.ownerProfiles[app.DefaultOwnerID]
	if !ok {
		return app.OwnerProfile{}, storeError(ctx, OperationOwnerProfileGet, StoreErrorCorrupt, errors.New("default owner profile is missing"))
	}
	return cloneOwnerProfile(profile), nil
}

func (s *MemoryStore) UpdateOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	profile.ID = app.DefaultOwnerID
	return s.saveOwnerProfile(ctx, OperationOwnerProfileUpdate, profile)
}

func (s *MemoryStore) GetOwnerProfileByID(ctx context.Context, id string) (app.OwnerProfile, bool, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileGetByID, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationOwnerProfileGetByID, ctx); err != nil {
		return app.OwnerProfile{}, false, err
	}
	id = normalizeOwnerProfileID(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationOwnerProfileGetByID, ctx); err != nil {
		return app.OwnerProfile{}, false, err
	}
	profile, ok := s.ownerProfiles[id]
	if !ok {
		return app.OwnerProfile{}, false, nil
	}
	return cloneOwnerProfile(profile), true, nil
}

func (s *MemoryStore) SaveOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	return s.saveOwnerProfile(ctx, OperationOwnerProfileSave, profile)
}

func (s *MemoryStore) saveOwnerProfile(ctx context.Context, operation StoreOperation, profile app.OwnerProfile) (app.OwnerProfile, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.OwnerProfile{}, err
	}
	profile.ID = normalizeOwnerProfileID(profile.ID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(operation, ctx); err != nil {
		return app.OwnerProfile{}, err
	}
	current, exists := s.ownerProfiles[profile.ID]
	candidate := prepareOwnerProfile(profile, current, exists, s.ownerNow(), s.ownerWriteHighWater[profile.ID])
	s.ownerWriteHighWater[candidate.ID] = candidate.UpdatedAt
	s.ownerProfiles[candidate.ID] = cloneOwnerProfile(candidate)
	if candidate.ID == app.DefaultOwnerID {
		s.ownerProfile = cloneOwnerProfile(candidate)
	}
	s.appendAuditLocked("owner_profile.updated", "", "", "owner", candidate.DisplayName, ownerProfileAuditFields(candidate))
	s.appendEventLocked("owner_profile.updated", "", "", candidate)
	return cloneOwnerProfile(candidate), nil
}

func (s *MemoryStore) ListOwnerProfiles(ctx context.Context) ([]app.OwnerProfile, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationOwnerProfileList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationOwnerProfileList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.OwnerProfile, 0, len(s.ownerProfiles))
	for _, profile := range s.ownerProfiles {
		out = append(out, cloneOwnerProfile(profile))
	}
	slices.SortFunc(out, compareOwnerProfiles)
	return out, nil
}

func (s *MemoryStore) FindOwnerProfileByExternalRef(ctx context.Context, source, externalRef string) (app.OwnerProfile, bool, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileFindExternalRef, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationOwnerProfileFindExternalRef, ctx); err != nil {
		return app.OwnerProfile{}, false, err
	}
	source = strings.TrimSpace(source)
	externalRef = strings.TrimSpace(externalRef)
	if source == "" || externalRef == "" {
		return app.OwnerProfile{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationOwnerProfileFindExternalRef, ctx); err != nil {
		return app.OwnerProfile{}, false, err
	}
	var matches []app.OwnerProfile
	for _, profile := range s.ownerProfiles {
		if profile.Source == source && profile.ExternalRef == externalRef {
			matches = append(matches, cloneOwnerProfile(profile))
		}
	}
	if len(matches) == 0 {
		return app.OwnerProfile{}, false, nil
	}
	slices.SortFunc(matches, compareOwnerProfiles)
	return matches[0], true, nil
}

func normalizeOwnerProfileID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return app.DefaultOwnerID
	}
	return id
}

func prepareOwnerProfile(profile, current app.OwnerProfile, exists bool, now, lastIssued time.Time) app.OwnerProfile {
	profile.ID = normalizeOwnerProfileID(profile.ID)
	profile.Source = strings.TrimSpace(profile.Source)
	profile.ExternalRef = strings.TrimSpace(profile.ExternalRef)
	profile.WorkspaceRoot = strings.TrimSpace(profile.WorkspaceRoot)
	profile.DefaultChannel = strings.TrimSpace(profile.DefaultChannel)
	profile.DefaultBindingID = strings.TrimSpace(profile.DefaultBindingID)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	profile.Email = strings.TrimSpace(profile.Email)
	if profile.Source == "" && profile.ID == app.DefaultOwnerID {
		profile.Source = "web"
	}
	if profile.DisplayName == "" {
		profile.DisplayName = "Owner"
	}
	profile.Preferences = cloneStringMap(profile.Preferences)
	now = now.UTC().Truncate(time.Microsecond)
	if exists {
		profile.CreatedAt = current.CreatedAt
	} else if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	} else {
		profile.CreatedAt = profile.CreatedAt.UTC().Truncate(time.Microsecond)
	}
	floor := current.UpdatedAt
	if lastIssued.After(floor) {
		floor = lastIssued
	}
	profile.UpdatedAt = now
	if profile.CreatedAt.After(profile.UpdatedAt) {
		profile.UpdatedAt = profile.CreatedAt.UTC().Truncate(time.Microsecond)
	}
	if !profile.UpdatedAt.After(floor) {
		profile.UpdatedAt = floor.UTC().Truncate(time.Microsecond).Add(time.Microsecond)
	}
	return profile
}

func compareOwnerProfiles(a, b app.OwnerProfile) int {
	if compared := b.UpdatedAt.Compare(a.UpdatedAt); compared != 0 {
		return compared
	}
	return strings.Compare(a.ID, b.ID)
}

func ownerProfileAuditFields(profile app.OwnerProfile) map[string]any {
	return map[string]any{
		"owner_id": profile.ID, "source": profile.Source,
		"external_ref": profile.ExternalRef != "", "email_set": profile.Email != "",
		"preferences": len(profile.Preferences), "display_name": profile.DisplayName,
	}
}

func OwnerProfilesEqual(a, b app.OwnerProfile) bool {
	return a.ID == b.ID && a.Source == b.Source && a.ExternalRef == b.ExternalRef &&
		a.WorkspaceRoot == b.WorkspaceRoot && a.DefaultChannel == b.DefaultChannel &&
		a.DefaultBindingID == b.DefaultBindingID && a.DisplayName == b.DisplayName &&
		a.Email == b.Email && a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt) &&
		maps.Equal(a.Preferences, b.Preferences)
}

func cloneOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	profile.Preferences = cloneStringMap(profile.Preferences)
	return profile
}

func cloneOwnerProfileMap(in map[string]app.OwnerProfile) map[string]app.OwnerProfile {
	if in == nil {
		return map[string]app.OwnerProfile{}
	}
	out := make(map[string]app.OwnerProfile, len(in))
	for id, profile := range in {
		out[id] = cloneOwnerProfile(profile)
	}
	return out
}
