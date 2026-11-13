// Package mcdintegration owns leased selection credentials. It has no purchase
// authority and no public model/HTTP registration. Product admission remains
// the responsibility of the authenticated workbench adapter.
package mcdintegration

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

var (
	ErrInvalid     = errors.New("mcd_session_invalid")
	ErrUnavailable = errors.New("mcd_session_unavailable")
	ErrConflict    = errors.New("mcd_session_conflict")
	ErrClosed      = errors.New("mcd_session_closed")
	ErrExpired     = errors.New("mcd_session_expired_or_clock_untrusted")
	ErrScope       = errors.New("mcd_session_scope_mismatch")
	ErrBudget      = errors.New("mcd_execution_budget_exhausted")
	ErrCredential  = errors.New("mcd_credential_invalid")
	hashPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

const MaxLeaseMS int64 = 24 * 60 * 60 * 1000

type Identity struct {
	DeploymentID   string `json:"deployment_id"`
	OwnerID        string `json:"owner_id"`
	ClientID       string `json:"client_id"`
	InstallationID string `json:"installation_id"`
	AccountRef     string `json:"account_ref"`
	MemberDigest   string `json:"member_digest"`
}

type Envelope struct {
	SchemaVersion      int             `json:"schema_version"`
	Canonicalization   string          `json:"canonicalization"`
	AdapterRevision    string          `json:"adapter_revision"`
	Identity           Identity        `json:"identity"`
	ConnectionID       string          `json:"connection_id"`
	ProvisionRequestID string          `json:"provision_request_id"`
	CredentialRevision int64           `json:"credential_revision"`
	GrantRevision      int64           `json:"grant_revision"`
	IssuedAtMS         int64           `json:"issued_at_ms"`
	SessionNotAfterMS  int64           `json:"session_not_after_ms"`
	Operations         []string        `json:"operations"`
	MaxRequests        int             `json:"max_requests"`
	Material           json.RawMessage `json:"material"`
	MaterialDigest     string          `json:"material_digest"`
	EnvelopeDigest     string          `json:"envelope_digest"`
}

// Receipt deliberately cannot contain credentials, quote bodies or provider
// results. An acceptance receipt is historical, never proof of current access.
type Receipt struct {
	RequestID          string `json:"request_id"`
	Digest             string `json:"digest"`
	ConnectionID       string `json:"connection_id"`
	CredentialRevision int64  `json:"credential_revision"`
	GrantRevision      int64  `json:"grant_revision"`
	NotAfterMS         int64  `json:"session_not_after_ms"`
	Disposition        string `json:"disposition"`
}

// Vault stores material separately from permanent payload-free control.
// A claimed digest anchor fences interrupted multi-record changes. Admission
// checks control before opening material; cleanup never deletes the fences.
type Vault interface {
	OpenBindingVersion(context.Context, string, string) ([]byte, int64, bool, error)
	CompareAndReplaceBinding(context.Context, string, string, []byte, int64) error
	ReplaceBinding(context.Context, string, string, []byte) error
	DeleteBinding(context.Context, string, string) error
}

type Runner interface {
	Validate(context.Context, Envelope) error
	Read(context.Context, Envelope, Execution, string, json.RawMessage) (json.RawMessage, error)
}

// Execution is supplied only by trusted admission, never inferred from a
// session. The eventual Workflow adapter must bind these fields to its grant.
type Execution struct {
	Identity           Identity `json:"identity"`
	ConnectionID       string   `json:"connection_id"`
	ExecutionID        string   `json:"execution_id"`
	CredentialRevision int64    `json:"credential_revision"`
	GrantRevision      int64    `json:"grant_revision"`
	DeadlineMS         int64    `json:"deadline_ms"`
	MaxRequests        int      `json:"max_requests"`
}

type Connection struct {
	vault             Vault
	runner            Runner
	identity          Identity
	id, binding, kind string
	now               func() time.Time
	mu                sync.Mutex
	active            map[*int]context.CancelFunc
}

func NewConnection(v Vault, runner Runner, identity Identity, id string, now func() time.Time) (*Connection, error) {
	if v == nil || runner == nil || !identity.valid() || !idPattern.MatchString(id) {
		return nil, ErrInvalid
	}
	if now == nil {
		now = time.Now
	}
	// Stable control identity excludes account/member so changing either cannot
	// bypass the original connection tombstone. Kind contributes full identity
	// to Vault AEAD associated data; a rebound account fails decryption.
	key := identity
	key.AccountRef, key.MemberDigest = "", ""
	return &Connection{vault: v, runner: runner, identity: identity, id: id, now: now,
		binding: "mcd-selection:" + digest([]any{key, id}), kind: "mcd-selection-v1:" + digest([]any{identity, id}), active: map[*int]context.CancelFunc{}}, nil
}

func (i Identity) valid() bool {
	return idPattern.MatchString(i.DeploymentID) && idPattern.MatchString(i.OwnerID) && idPattern.MatchString(i.ClientID) &&
		idPattern.MatchString(i.InstallationID) && idPattern.MatchString(i.AccountRef) && hashPattern.MatchString(i.MemberDigest)
}

func DecodeEnvelope(raw []byte) (Envelope, error) {
	var e Envelope
	if len(raw) > 48<<10 || appstream.DecodeStrict(raw, &e) != nil {
		return e, ErrInvalid
	}
	canonical, err := Canonical(raw)
	if err != nil {
		return e, ErrInvalid
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(canonical, &obj) != nil {
		return e, ErrInvalid
	}
	delete(obj, "envelope_digest")
	if e.SchemaVersion != 1 || e.Canonicalization != "mcd-json-v1" || !ascii(e.AdapterRevision, 128) ||
		!e.Identity.valid() || !idPattern.MatchString(e.ConnectionID) || !idPattern.MatchString(e.ProvisionRequestID) ||
		e.CredentialRevision < 1 || e.GrantRevision < 1 || e.CredentialRevision > 1<<53-1 || e.GrantRevision > 1<<53-1 ||
		e.IssuedAtMS < 1 || e.SessionNotAfterMS <= e.IssuedAtMS || e.SessionNotAfterMS-e.IssuedAtMS > MaxLeaseMS ||
		e.MaxRequests < 1 || e.MaxRequests > 128 || len(e.Operations) == 0 || len(e.Operations) > 20 ||
		!slices.IsSorted(e.Operations) || e.MaterialDigest != digest(e.Material) || e.EnvelopeDigest != digest(obj) {
		return e, ErrInvalid
	}
	for i, op := range e.Operations {
		if !ascii(op, 64) || i > 0 && op == e.Operations[i-1] {
			return e, ErrInvalid
		}
	}
	return e, nil
}
