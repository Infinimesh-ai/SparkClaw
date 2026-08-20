package iscppairing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/provisioning"
	"golang.org/x/sync/semaphore"
)

type Repository interface {
	store.ISCPOnboardingRepository
	AddAudit(app.AuditEvent)
}

type Service struct {
	repository Repository
	options    Options
	startGate  *semaphore.Weighted
	pending    *pendingOnboarding
	now        func() time.Time
}

type pendingOnboarding struct {
	ownerID     string
	actorID     string
	id          string
	fingerprint string
	receipt     app.ISCPOnboarding
	ticket      provisioning.PairingTicket
}

func New(repository Repository, options Options) *Service {
	if options.ExpectedTicketType == "" {
		options.ExpectedTicketType = DefaultTicketType
	}
	if options.DefaultTTL <= 0 {
		options.DefaultTTL = 10 * time.Minute
	}
	return &Service{
		repository: repository,
		options:    options,
		startGate:  semaphore.NewWeighted(1),
		now:        func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Status(ctx context.Context) Status {
	status := Status{
		Enabled: s != nil && s.options.Enabled, DomainID: strings.TrimSpace(s.options.DomainID),
		AuthorityHost: strings.TrimSpace(s.options.AuthorityHost), ExpectedTicketType: DefaultTicketType,
	}
	if s != nil && s.options.ExpectedTicketType != "" {
		status.ExpectedTicketType = s.options.ExpectedTicketType
	}
	if !status.Enabled {
		status.State, status.DisabledReason = "disabled", "not_enabled"
		return status
	}
	if s.repository == nil || s.options.Authority == nil || status.DomainID == "" {
		status.State, status.DisabledReason = "unavailable", "not_configured"
		return status
	}
	if err := s.options.Authority.Ready(ctx); err != nil {
		status.State, status.DisabledReason = "unavailable", "credential_unavailable"
		return status
	}
	status.Ready, status.State = true, "ready"
	return status
}

func (s *Service) Start(ctx context.Context, ownerID, actorID string, input StartRequest, now time.Time) (IssuedPairing, error) {
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" || len(displayName) > 120 {
		return IssuedPairing{}, errors.New("external MCP client name must be between 1 and 120 characters")
	}
	ttl := time.Duration(input.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = s.options.DefaultTTL
	}
	if ttl < time.Minute || ttl > 30*time.Minute {
		return IssuedPairing{}, errors.New("ISCP Pairing Ticket TTL must be between 60 and 1800 seconds")
	}
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	if actorID == "" {
		actorID = ownerID
	}
	fingerprint := onboardingRequestFingerprint(ownerID, displayName, ttl, s.options.DomainID, s.options.ExpectedTicketType)
	if err := s.startGate.Acquire(ctx, 1); err != nil {
		return IssuedPairing{}, failureFromContext(err)
	}
	defer s.startGate.Release(1)
	if s.pending != nil {
		if s.pending.ownerID != ownerID || s.pending.fingerprint != fingerprint {
			return IssuedPairing{}, &Failure{Code: FailureConflict, Public: ErrPendingConflict}
		}
		return s.reconcilePending(ctx)
	}
	if !s.Status(ctx).Ready {
		return IssuedPairing{}, &Failure{Code: FailureUnavailable, Public: ErrUnavailable}
	}

	id := app.NewID("iscp_onboarding")
	result, err := s.options.Authority.IssuePairingTicket(ctx, AuthorityRequest{
		Type: AuthorityRequestType, RequestRef: id, DomainID: s.options.DomainID, MaxUses: 1, TTLSeconds: int(ttl.Seconds()),
	})
	if err != nil {
		return IssuedPairing{}, err
	}
	if err := s.validateResult(result, now); err != nil {
		return IssuedPairing{}, fmt.Errorf("%w: %v", ErrAuthority, err)
	}
	ticket := result.Ticket
	receipt := app.ISCPOnboarding{
		SchemaVersion: app.ISCPOnboardingSchemaVersion, ID: id, OwnerID: ownerID, ActorID: actorID,
		DisplayName: displayName, DomainID: ticket.DomainID, AuthorityRef: result.AuthorityRef,
		TicketID: ticket.TicketID, TicketType: ticket.Type, RelayID: ticket.RelayID, TrustRootID: ticket.TrustRootID,
		MaxUses: ticket.MaxUses, Status: app.ISCPOnboardingTicketIssued, TicketIssuedAt: ticket.IssuedAt,
		TicketExpiresAt: ticket.ExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	s.pending = &pendingOnboarding{
		ownerID: ownerID, actorID: actorID, id: id, fingerprint: fingerprint,
		receipt: receipt, ticket: ticket,
	}
	saved, err := s.repository.SaveISCPOnboarding(ctx, receipt)
	if err != nil {
		if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome {
			return s.reconcilePending(ctx)
		}
		s.pending = nil
		return IssuedPairing{}, failureFromStore(err)
	}
	s.pending.receipt = saved
	return s.completePending(saved)
}

func (s *Service) List(ctx context.Context, ownerID string) ([]app.ISCPOnboarding, error) {
	if s == nil || s.repository == nil {
		return nil, &Failure{Code: FailureUnavailable, Public: ErrUnavailable}
	}
	onboardings, err := s.repository.ListISCPOnboardings(ctx, ownerID)
	if err != nil {
		return nil, failureFromStore(err)
	}
	return onboardings, nil
}

func (s *Service) reconcilePending(ctx context.Context) (IssuedPairing, error) {
	pending := s.pending
	if !pending.ticket.ExpiresAt.After(s.currentTime()) {
		pending.ticket.Signature = identity.Signature{}
	}
	receipt, found, err := s.repository.GetISCPOnboarding(ctx, pending.id)
	if err != nil {
		return IssuedPairing{}, failureFromStore(err)
	}
	if !found {
		s.pending = nil
		return IssuedPairing{}, &Failure{Code: FailureUnavailable, Public: ErrPersistence}
	}
	if receipt.ID != pending.receipt.ID || receipt.OwnerID != pending.receipt.OwnerID || receipt.TicketID != pending.receipt.TicketID {
		return IssuedPairing{}, &Failure{Code: FailureUnavailable, Public: ErrUnavailable}
	}
	return s.completePending(receipt)
}

func (s *Service) completePending(receipt app.ISCPOnboarding) (IssuedPairing, error) {
	pending := s.pending
	s.pending = nil
	s.repository.AddAudit(app.AuditEvent{Actor: pending.actorID, Type: "iscp.onboarding.ticket_issued", Summary: "Requested a single-use ISCP Pairing Ticket", Fields: map[string]any{
		"onboarding_id": receipt.ID, "authority_ref": receipt.AuthorityRef, "ticket_id": receipt.TicketID,
		"domain_id": receipt.DomainID, "relay_id": receipt.RelayID, "expires_at": receipt.TicketExpiresAt,
	}})
	if !pending.ticket.ExpiresAt.After(s.currentTime()) {
		pending.ticket.Signature = identity.Signature{}
		return IssuedPairing{}, &Failure{Code: FailureExpired, Public: ErrTicketExpired}
	}
	return IssuedPairing{Onboarding: receipt, Ticket: pending.ticket}, nil
}

func (s *Service) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func onboardingRequestFingerprint(ownerID, displayName string, ttl time.Duration, domainID, ticketType string) string {
	payload, _ := json.Marshal(struct {
		OwnerID     string `json:"owner_id"`
		DisplayName string `json:"display_name"`
		TTLSeconds  int64  `json:"ttl_seconds"`
		DomainID    string `json:"domain_id"`
		TicketType  string `json:"ticket_type"`
	}{
		OwnerID: strings.TrimSpace(ownerID), DisplayName: strings.TrimSpace(displayName), TTLSeconds: int64(ttl / time.Second),
		DomainID: strings.TrimSpace(domainID), TicketType: strings.TrimSpace(ticketType),
	})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func failureFromContext(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Failure{Code: FailureTimeout, Public: errors.New("ISCP pairing timed out"), Cause: err}
	}
	return &Failure{Code: FailureUnavailable, Public: ErrUnavailable, Cause: err}
}

func failureFromStore(err error) error {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorTimeout:
		return &Failure{Code: FailureTimeout, Public: errors.New("ISCP pairing timed out"), Cause: err}
	case store.StoreErrorConflict:
		return &Failure{Code: FailureConflict, Public: errors.New("ISCP onboarding conflicts with existing state"), Cause: err}
	case store.StoreErrorInvalid:
		return &Failure{Code: FailureInvalid, Public: errors.New("ISCP onboarding receipt is invalid"), Cause: err}
	default:
		return &Failure{Code: FailureUnavailable, Public: ErrUnavailable, Cause: err}
	}
}

func (s *Service) validateResult(result AuthorityResult, now time.Time) error {
	ticket := result.Ticket
	if result.AuthorityRef == "" || ticket.Type != s.options.ExpectedTicketType || ticket.DomainID != s.options.DomainID {
		return errors.New("authority returned a ticket outside the configured contract")
	}
	if ticket.TicketID == "" || ticket.RelayID == "" || ticket.TrustRootID == "" || ticket.MaxUses != 1 {
		return errors.New("authority returned incomplete single-use ticket metadata")
	}
	if ticket.Signature.Alg != "Ed25519" || ticket.Signature.KID == "" || ticket.Signature.Value == "" {
		return errors.New("authority returned an unsigned Pairing Ticket")
	}
	if ticket.IssuedAt.After(now.Add(time.Minute)) || !ticket.ExpiresAt.After(now) || ticket.ExpiresAt.Sub(ticket.IssuedAt) > 30*time.Minute {
		return errors.New("authority returned an invalid Pairing Ticket lifetime")
	}
	return nil
}
