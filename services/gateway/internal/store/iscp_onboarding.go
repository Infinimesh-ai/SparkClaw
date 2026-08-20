package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func normalizePersistedISCPOnboardings(onboardings map[string]app.ISCPOnboarding) error {
	for key, onboarding := range onboardings {
		normalized, err := normalizeISCPOnboarding(onboarding, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("invalid persisted ISCP onboarding %q: %w", key, err)
		}
		if normalized.ID != key {
			return fmt.Errorf("persisted ISCP onboarding key %q does not match payload ID", key)
		}
		onboardings[key] = normalized
	}
	return nil
}

func normalizeISCPOnboarding(onboarding app.ISCPOnboarding, now time.Time) (app.ISCPOnboarding, error) {
	if onboarding.SchemaVersion == 0 {
		onboarding.SchemaVersion = app.ISCPOnboardingSchemaVersion
	}
	if onboarding.SchemaVersion != app.ISCPOnboardingSchemaVersion || strings.TrimSpace(onboarding.ID) == "" ||
		strings.TrimSpace(onboarding.OwnerID) == "" || strings.TrimSpace(onboarding.DomainID) == "" ||
		strings.TrimSpace(onboarding.AuthorityRef) == "" || strings.TrimSpace(onboarding.TicketID) == "" ||
		strings.TrimSpace(onboarding.TicketType) == "" || strings.TrimSpace(onboarding.RelayID) == "" ||
		strings.TrimSpace(onboarding.TrustRootID) == "" || onboarding.TicketIssuedAt.IsZero() ||
		onboarding.Status != app.ISCPOnboardingTicketIssued || onboarding.MaxUses != 1 ||
		!onboarding.TicketExpiresAt.After(onboarding.TicketIssuedAt) {
		return app.ISCPOnboarding{}, errors.New("invalid ISCP onboarding receipt")
	}
	if onboarding.ActorID == "" {
		onboarding.ActorID = onboarding.OwnerID
	}
	if onboarding.CreatedAt.IsZero() {
		onboarding.CreatedAt = now
	}
	if onboarding.UpdatedAt.IsZero() {
		onboarding.UpdatedAt = onboarding.CreatedAt
	}
	return onboarding, nil
}

func (s *MemoryStore) SaveISCPOnboarding(ctx context.Context, onboarding app.ISCPOnboarding) (app.ISCPOnboarding, error) {
	ctx, cancel := operationContext(ctx, OperationISCPOnboardingSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationISCPOnboardingSave, ctx); err != nil {
		return app.ISCPOnboarding{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationISCPOnboardingSave, ctx); err != nil {
		return app.ISCPOnboarding{}, err
	}
	onboarding, err := normalizeISCPOnboarding(onboarding, time.Now().UTC())
	if err != nil {
		return app.ISCPOnboarding{}, storeError(OperationISCPOnboardingSave, StoreErrorInvalid, err)
	}
	if _, exists := s.iscpOnboardings[onboarding.ID]; exists {
		return app.ISCPOnboarding{}, storeError(OperationISCPOnboardingSave, StoreErrorConflict, ErrISCPOnboardingConflict)
	}
	s.iscpOnboardings[onboarding.ID] = onboarding
	return onboarding, nil
}

func (s *MemoryStore) GetISCPOnboarding(ctx context.Context, id string) (app.ISCPOnboarding, bool, error) {
	ctx, cancel := operationContext(ctx, OperationISCPOnboardingGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationISCPOnboardingGet, ctx); err != nil {
		return app.ISCPOnboarding{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationISCPOnboardingGet, ctx); err != nil {
		return app.ISCPOnboarding{}, false, err
	}
	onboarding, ok := s.iscpOnboardings[id]
	return onboarding, ok, nil
}

func (s *MemoryStore) ListISCPOnboardings(ctx context.Context, ownerID string) ([]app.ISCPOnboarding, error) {
	ctx, cancel := operationContext(ctx, OperationISCPOnboardingList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationISCPOnboardingList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationISCPOnboardingList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.ISCPOnboarding, 0)
	for _, onboarding := range s.iscpOnboardings {
		if ownerID == "" || onboarding.OwnerID == ownerID {
			out = append(out, onboarding)
		}
	}
	slices.SortFunc(out, func(a, b app.ISCPOnboarding) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out, nil
}
