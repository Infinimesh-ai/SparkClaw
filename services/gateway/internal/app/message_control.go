package app

import "time"

const ScheduleSpecSchemaVersion = 1

type EndpointStatus string

const (
	EndpointActive  EndpointStatus = "active"
	EndpointStale   EndpointStatus = "stale"
	EndpointRevoked EndpointStatus = "revoked"
)

// MessageEndpoint is the provider-neutral address used by return routing.
// Provider-native credentials and protocol clients stay behind BindingRef.
type MessageEndpoint struct {
	ID                   EndpointID     `json:"id"`
	OwnerID              string         `json:"owner_id"`
	ActorID              string         `json:"actor_id"`
	SourceActorID        string         `json:"source_actor_id,omitempty"`
	Kind                 EndpointKind   `json:"kind"`
	ProviderKey          string         `json:"provider_key,omitempty"`
	BindingRef           string         `json:"binding_ref,omitempty"`
	ExternalUserRef      string         `json:"external_user_ref,omitempty"`
	Address              string         `json:"address,omitempty"`
	ThreadRef            string         `json:"thread_ref,omitempty"`
	ContextRef           string         `json:"context_ref,omitempty"`
	SessionID            string         `json:"session_id,omitempty"`
	SoftwareDisplayName  string         `json:"software_display_name,omitempty"`
	AccountDisplayName   string         `json:"account_display_name,omitempty"`
	RecipientDisplayName string         `json:"recipient_display_name,omitempty"`
	ConversationLabel    string         `json:"conversation_label,omitempty"`
	Status               EndpointStatus `json:"status"`
	CreatedAt            time.Time      `json:"created_at,omitempty"`
	UpdatedAt            time.Time      `json:"updated_at,omitempty"`
}

type SchedulePayloadMode string

const (
	SchedulePayloadLiteral SchedulePayloadMode = "literal"
	SchedulePayloadRequest SchedulePayloadMode = "request"
)

type SchedulePayload struct {
	Mode    SchedulePayloadMode `json:"mode"`
	Content MessageContent      `json:"content"`
}

// ScheduleSpec is embedded in legacy Reminder records during migration. A
// missing spec is interpreted as the original literal-text reminder shape.
type ScheduleSpec struct {
	SchemaVersion          int                  `json:"schema_version"`
	OwnerID                string               `json:"owner_id"`
	ActorID                string               `json:"actor_id"`
	Payload                SchedulePayload      `json:"payload"`
	ReturnRoute            ReturnRoute          `json:"return_route"`
	Authorization          MessageAuthorization `json:"authorization"`
	ExpectedCapabilityPath []CapabilityID       `json:"expected_capability_path,omitempty"`
}

type MessageSchedule struct {
	ID              ScheduleID   `json:"id"`
	SessionID       string       `json:"session_id,omitempty"`
	RunID           string       `json:"run_id,omitempty"`
	Spec            ScheduleSpec `json:"spec"`
	DueTime         time.Time    `json:"due_time"`
	Timezone        string       `json:"timezone"`
	Recurrence      string       `json:"recurrence,omitempty"`
	DedupeKey       string       `json:"dedupe_key"`
	Status          string       `json:"status"`
	DeliveryAttempt int          `json:"delivery_attempt"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	SentAt          *time.Time   `json:"sent_at,omitempty"`
	CanceledAt      *time.Time   `json:"canceled_at,omitempty"`
}
