package store

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func normalizeISCPOnboarding(onboarding app.ISCPOnboarding, now time.Time) (app.ISCPOnboarding, error) {
	if onboarding.SchemaVersion == 0 {
		onboarding.SchemaVersion = app.ISCPOnboardingSchemaVersion
	}
	if onboarding.SchemaVersion != app.ISCPOnboardingSchemaVersion || strings.TrimSpace(onboarding.ID) == "" ||
		strings.TrimSpace(onboarding.OwnerID) == "" || strings.TrimSpace(onboarding.DomainID) == "" ||
		strings.TrimSpace(onboarding.AuthorityRef) == "" || strings.TrimSpace(onboarding.TicketID) == "" ||
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

func (s *MemoryStore) SaveISCPOnboarding(onboarding app.ISCPOnboarding) (app.ISCPOnboarding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	onboarding, err := normalizeISCPOnboarding(onboarding, time.Now().UTC())
	if err != nil {
		return app.ISCPOnboarding{}, err
	}
	if _, exists := s.iscpOnboardings[onboarding.ID]; exists {
		return app.ISCPOnboarding{}, ErrISCPOnboardingConflict
	}
	s.iscpOnboardings[onboarding.ID] = onboarding
	return onboarding, nil
}

func (s *MemoryStore) GetISCPOnboarding(id string) (app.ISCPOnboarding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	onboarding, ok := s.iscpOnboardings[id]
	return onboarding, ok
}

func (s *MemoryStore) ListISCPOnboardings(ownerID string) []app.ISCPOnboarding {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]app.ISCPOnboarding, 0)
	for _, onboarding := range s.iscpOnboardings {
		if ownerID == "" || onboarding.OwnerID == ownerID {
			out = append(out, onboarding)
		}
	}
	slices.SortFunc(out, func(a, b app.ISCPOnboarding) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out
}
