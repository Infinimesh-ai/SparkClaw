package app

import "time"

const ScheduleSpecSchemaVersion = 2

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
	RequesterDeviceID    string         `json:"requester_device_id,omitempty"`
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

type SchedulePayload struct {
	Content MessageContent `json:"content"`
}

// ScheduleSpec freezes the owner request and delivery context that the Timer
// republishes through the ordinary Message Runtime when the schedule is due.
type ScheduleSpec struct {
	SchemaVersion int                  `json:"schema_version"`
	OwnerID       string               `json:"owner_id"`
	ActorID       string               `json:"actor_id"`
	Payload       SchedulePayload      `json:"payload"`
	ReturnRoute   ReturnRoute          `json:"return_route"`
	Authorization MessageAuthorization `json:"authorization"`
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
