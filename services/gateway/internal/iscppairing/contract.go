package iscppairing

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/provisioning"
)

const (
	AuthorityRequestType = "sparkclaw.iscp_pairing.request.v1"
	DefaultTicketType    = provisioning.TypePairingTicket
)

var (
	ErrUnavailable     = errors.New("ISCP pairing is temporarily unavailable")
	ErrAuthority       = errors.New("ISCP pairing authority request failed")
	ErrPersistence     = errors.New("ISCP onboarding receipt could not be persisted")
	ErrPendingConflict = errors.New("another ISCP onboarding request is awaiting reconciliation")
	ErrTicketExpired   = errors.New("the reconciled ISCP Pairing Ticket has expired; start a new request")
)

type FailureCode string

const (
	FailureTimeout     FailureCode = "timeout"
	FailureUnavailable FailureCode = "unavailable"
	FailureConflict    FailureCode = "conflict"
	FailureInvalid     FailureCode = "invalid"
	FailureExpired     FailureCode = "expired"
)

type Failure struct {
	Code   FailureCode
	Public error
	Cause  error
}

func (e *Failure) Error() string {
	if e == nil || e.Public == nil {
		return ""
	}
	return e.Public.Error()
}

func (e *Failure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Public
}

func FailureCodeOf(err error) FailureCode {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

type StartRequest struct {
	DisplayName string `json:"display_name"`
	TTLSeconds  int    `json:"ttl_seconds,omitempty"`
}

type AuthorityRequest struct {
	Type       string `json:"type"`
	RequestRef string `json:"request_ref"`
	DomainID   string `json:"domain_id"`
	MaxUses    int    `json:"max_uses"`
	TTLSeconds int    `json:"ttl_seconds"`
}

type AuthorityResult struct {
	AuthorityRef string
	Ticket       provisioning.PairingTicket
}

type Authority interface {
	Ready(context.Context) error
	IssuePairingTicket(context.Context, AuthorityRequest) (AuthorityResult, error)
}

type Status struct {
	Enabled            bool   `json:"enabled"`
	Ready              bool   `json:"ready"`
	State              string `json:"state"`
	DomainID           string `json:"domain_id,omitempty"`
	AuthorityHost      string `json:"authority_host,omitempty"`
	ExpectedTicketType string `json:"expected_ticket_type"`
	DisabledReason     string `json:"disabled_reason,omitempty"`
}

type IssuedPairing struct {
	Onboarding app.ISCPOnboarding         `json:"onboarding"`
	Ticket     provisioning.PairingTicket `json:"ticket"`
}

type Options struct {
	Enabled            bool
	DomainID           string
	AuthorityHost      string
	ExpectedTicketType string
	DefaultTTL         time.Duration
	Authority          Authority
}
