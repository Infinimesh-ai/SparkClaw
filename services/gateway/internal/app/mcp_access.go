package app

import (
	"encoding/json"
	"time"
)

const (
	ISCPOnboardingSchemaVersion  = 1
	MCPAccessTicketSchemaVersion = 2
	MCPBindingSchemaVersion      = 2
	MCPInvocationSchemaVersion   = 2
	MCPOperationSchemaVersion    = 1
	// MCP embeds binary result parts as base64 in a 4 MiB result envelope.
	// Reserve 128 KiB for text and typed operation metadata.
	MCPMaxResultRawBinaryBytes = (3 << 20) - (128 << 10)
)

type ISCPOnboardingStatus string

const (
	ISCPOnboardingTicketIssued ISCPOnboardingStatus = "ticket_issued"
)

// ISCPOnboarding retains only the non-secret receipt for an authority-issued
// Pairing Ticket. The signed ticket itself is deliberately never persisted.
type ISCPOnboarding struct {
	SchemaVersion   int                  `json:"schema_version"`
	ID              string               `json:"id"`
	OwnerID         string               `json:"owner_id"`
	ActorID         string               `json:"actor_id"`
	DisplayName     string               `json:"display_name"`
	DomainID        string               `json:"domain_id"`
	AuthorityRef    string               `json:"authority_ref"`
	TicketID        string               `json:"ticket_id"`
	TicketType      string               `json:"ticket_type"`
	RelayID         string               `json:"relay_id"`
	TrustRootID     string               `json:"trust_root_id"`
	MaxUses         int                  `json:"max_uses"`
	Status          ISCPOnboardingStatus `json:"status"`
	TicketIssuedAt  time.Time            `json:"ticket_issued_at"`
	TicketExpiresAt time.Time            `json:"ticket_expires_at"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

type MCPAccessStatus string

const (
	MCPAccessPending  MCPAccessStatus = "pending"
	MCPAccessConsumed MCPAccessStatus = "consumed"
	MCPAccessRevoked  MCPAccessStatus = "revoked"
	MCPAccessExpired  MCPAccessStatus = "expired"
)

type MCPBindingStatus string

const (
	MCPBindingActive    MCPBindingStatus = "active"
	MCPBindingSuspended MCPBindingStatus = "suspended"
	MCPBindingRevoked   MCPBindingStatus = "revoked"
)

type MCPAccessScope string

const MCPAccessConversation MCPAccessScope = "conversation"

type MCPAccessTicket struct {
	SchemaVersion         int             `json:"schema_version"`
	ID                    string          `json:"id"`
	SecretHash            string          `json:"secret_hash,omitempty"`
	OwnerID               string          `json:"owner_id"`
	ActorID               string          `json:"actor_id"`
	DomainID              string          `json:"domain_id"`
	AuthorizationRevision int64           `json:"authorization_revision"`
	Scope                 MCPAccessScope  `json:"scope"`
	Status                MCPAccessStatus `json:"status"`
	MaxUses               int             `json:"max_uses"`
	UseCount              int             `json:"use_count"`
	IssuedAt              time.Time       `json:"issued_at"`
	ExpiresAt             time.Time       `json:"expires_at"`
	ConsumedAt            *time.Time      `json:"consumed_at,omitempty"`
	RevokedAt             *time.Time      `json:"revoked_at,omitempty"`
}

type MCPBinding struct {
	SchemaVersion          int              `json:"schema_version"`
	ID                     string           `json:"id"`
	OwnerID                string           `json:"owner_id"`
	ActorID                string           `json:"actor_id"`
	DomainID               string           `json:"domain_id"`
	RequesterDeviceID      string           `json:"requester_device_id"`
	RequesterKeyThumbprint string           `json:"requester_key_thumbprint"`
	AuthorizationRevision  int64            `json:"authorization_revision"`
	Scope                  MCPAccessScope   `json:"scope"`
	Status                 MCPBindingStatus `json:"status"`
	LinkedSessionID        string           `json:"linked_session_id"`
	LatestISCPSessionID    string           `json:"latest_iscp_session_id,omitempty"`
	CreatedAt              time.Time        `json:"created_at"`
	UpdatedAt              time.Time        `json:"updated_at"`
	LastUsedAt             *time.Time       `json:"last_used_at,omitempty"`
	RevokedAt              *time.Time       `json:"revoked_at,omitempty"`
}

type MCPPeerIdentity struct {
	DomainID      string `json:"domain_id"`
	DeviceID      string `json:"device_id"`
	KeyThumbprint string `json:"key_thumbprint"`
	ISCPSessionID string `json:"iscp_session_id"`
}

type MCPInvocationRef struct {
	InvocationID      string `json:"invocation_id"`
	OperationID       string `json:"operation_id"`
	BindingRef        string `json:"binding_ref"`
	BindingRevision   int64  `json:"binding_revision"`
	RequesterDeviceID string `json:"requester_device_id"`
}

type MCPInvocationContext struct {
	SchemaVersion          int            `json:"schema_version"`
	ID                     string         `json:"id"`
	MCPRequestID           string         `json:"mcp_request_id"`
	MCPSessionID           string         `json:"mcp_session_id"`
	ISCPSessionID          string         `json:"iscp_session_id"`
	OperationID            string         `json:"operation_id"`
	RequesterDeviceID      string         `json:"requester_device_id"`
	RequesterKeyThumbprint string         `json:"requester_key_thumbprint"`
	BindingRef             string         `json:"binding_ref"`
	BindingRevision        int64          `json:"binding_revision"`
	OwnerID                string         `json:"owner_id"`
	ActorID                string         `json:"actor_id"`
	ToolName               string         `json:"tool_name"`
	Arguments              map[string]any `json:"arguments"`
	ArgumentDigest         string         `json:"argument_digest"`
	IdempotencyKey         string         `json:"idempotency_key"`
	Deadline               time.Time      `json:"deadline"`
	MessageID              string         `json:"message_id,omitempty"`
	RunID                  string         `json:"run_id,omitempty"`
	DeliveryID             DeliveryID     `json:"delivery_id,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
}

type MCPOperationState string

const (
	MCPOperationRunning          MCPOperationState = "running"
	MCPOperationApprovalRequired MCPOperationState = "approval_required"
	MCPOperationSucceeded        MCPOperationState = "succeeded"
	MCPOperationFailed           MCPOperationState = "failed"
	MCPOperationCancelled        MCPOperationState = "cancelled"
	MCPOperationRevoked          MCPOperationState = "revoked"
)

type MCPOperation struct {
	SchemaVersion  int                  `json:"schema_version"`
	ID             string               `json:"id"`
	BindingID      string               `json:"binding_id"`
	IdempotencyKey string               `json:"idempotency_key"`
	Fingerprint    string               `json:"fingerprint"`
	Invocation     MCPInvocationContext `json:"invocation"`
	State          MCPOperationState    `json:"state"`
	Result         json.RawMessage      `json:"result,omitempty"`
	ErrorCode      string               `json:"error_code,omitempty"`
	ErrorMessage   string               `json:"error_message,omitempty"`
	Version        int64                `json:"version"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
	CompletedAt    *time.Time           `json:"completed_at,omitempty"`
}

type MCPConversationRequest struct {
	Text       string                `json:"text,omitempty"`
	Media      []MessageMediaLocator `json:"media,omitempty"`
	Invocation MCPInvocationRef      `json:"invocation"`
}
