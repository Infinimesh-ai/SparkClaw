package iscppairing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type Service struct {
	store   store.Store
	options Options
}

func New(st store.Store, options Options) *Service {
	if options.ExpectedTicketType == "" {
		options.ExpectedTicketType = DefaultTicketType
	}
	if options.DefaultTTL <= 0 {
		options.DefaultTTL = 10 * time.Minute
	}
	return &Service{store: st, options: options}
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
	if s.store == nil || s.options.Authority == nil || status.DomainID == "" {
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
	if !s.Status(ctx).Ready {
		return IssuedPairing{}, ErrUnavailable
	}
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
	onboarding, err := s.store.SaveISCPOnboarding(app.ISCPOnboarding{
		SchemaVersion: app.ISCPOnboardingSchemaVersion, ID: id, OwnerID: ownerID, ActorID: actorID,
		DisplayName: displayName, DomainID: ticket.DomainID, AuthorityRef: result.AuthorityRef,
		TicketID: ticket.TicketID, TicketType: ticket.Type, RelayID: ticket.RelayID, TrustRootID: ticket.TrustRootID,
		MaxUses: ticket.MaxUses, Status: app.ISCPOnboardingTicketIssued, TicketIssuedAt: ticket.IssuedAt,
		TicketExpiresAt: ticket.ExpiresAt, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return IssuedPairing{}, errors.New("ISCP onboarding receipt could not be persisted")
	}
	s.store.AddAudit(app.AuditEvent{Actor: actorID, Type: "iscp.onboarding.ticket_issued", Summary: "Requested a single-use ISCP Pairing Ticket", Fields: map[string]any{
		"onboarding_id": onboarding.ID, "authority_ref": onboarding.AuthorityRef, "ticket_id": onboarding.TicketID,
		"domain_id": onboarding.DomainID, "relay_id": onboarding.RelayID, "expires_at": onboarding.TicketExpiresAt,
	}})
	return IssuedPairing{Onboarding: onboarding, Ticket: ticket}, nil
}

func (s *Service) List(ownerID string) []app.ISCPOnboarding {
	if s == nil || s.store == nil {
		return []app.ISCPOnboarding{}
	}
	return s.store.ListISCPOnboardings(ownerID)
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
