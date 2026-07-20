package app

import "time"

const (
	MessageEnvelopeSchemaVersion = 1
	RouteDecisionSchemaVersion   = 1
	WorkflowResultSchemaVersion  = 1
	DeliveryRequestSchemaVersion = 2
)

const (
	BindingScopeReminderSendSelf = "reminder_send_self"
	BindingScopeMessageSendSelf  = "message_send_self"
)

type MessageSourceKind string

const (
	MessageSourceWeb              MessageSourceKind = "web"
	MessageSourceThirdPartyDevice MessageSourceKind = "third_party_device"
	MessageSourceTimer            MessageSourceKind = "timer"
)

type MessagePartKind string

const (
	MessagePartText  MessagePartKind = "text"
	MessagePartImage MessagePartKind = "image"
	MessagePartAudio MessagePartKind = "audio"
	MessagePartFile  MessagePartKind = "file"
)

type MessagePartDisposition string

const (
	MessageDispositionInline     MessagePartDisposition = "inline"
	MessageDispositionAttachment MessagePartDisposition = "attachment"
	MessageDispositionVoiceNote  MessagePartDisposition = "voice_note"
)

type EndpointID string
type ScheduleID string
type CapabilityID string
type DeliveryID string

const (
	CapabilityBrowserSearch       CapabilityID = "browser.search"
	CapabilityBrowserAutomation   CapabilityID = "browser.automation"
	CapabilityDocumentInformation CapabilityID = "document.information"
	CapabilityDocumentProcessing  CapabilityID = "document.processing"
)

const (
	CapabilityQualifierOperation = "operation"
	CapabilityOperationDiscover  = "discover"
	CapabilityOperationRead      = "read"
)

type EndpointKind string

const (
	EndpointKindWeb              EndpointKind = "web"
	EndpointKindThirdPartyDevice EndpointKind = "third_party_device"
)

type MessageDirection string

const (
	MessageDirectionReceive MessageDirection = "receive"
	MessageDirectionSend    MessageDirection = "send"
)

type ReturnMode string

const (
	ReturnToSource   ReturnMode = "source"
	ReturnToEndpoint ReturnMode = "endpoint"
	ReturnNowhere    ReturnMode = "none"
)

type MessageSourceContext struct {
	Kind            MessageSourceKind `json:"kind"`
	Adapter         string            `json:"adapter,omitempty"`
	EndpointID      EndpointID        `json:"endpoint_id,omitempty"`
	NativeMessageID string            `json:"native_message_id,omitempty"`
	NativeThreadRef string            `json:"native_thread_ref,omitempty"`
	ScheduleID      ScheduleID        `json:"schedule_id,omitempty"`
}

type MessageAuthorization struct {
	PrincipalID string   `json:"principal_id"`
	Scope       []string `json:"scope,omitempty"`
}

// MessageIngressContext carries provider-neutral source and return metadata
// alongside the legacy text/attachment Agent API during migration.
type MessageIngressContext struct {
	Source        MessageSourceContext `json:"source"`
	OwnerID       string               `json:"owner_id"`
	Authorization MessageAuthorization `json:"authorization"`
	ReturnRoute   ReturnRoute          `json:"return_route"`
}

type MessagePart struct {
	ID                string                 `json:"id"`
	Kind              MessagePartKind        `json:"kind"`
	Disposition       MessagePartDisposition `json:"disposition"`
	Text              string                 `json:"text,omitempty"`
	ArtifactID        string                 `json:"artifact_id,omitempty"`
	Resource          *ResourceRef           `json:"resource,omitempty"`
	Name              string                 `json:"name,omitempty"`
	ContentType       string                 `json:"content_type,omitempty"`
	Bytes             int                    `json:"bytes,omitempty"`
	Width             int                    `json:"width,omitempty"`
	Height            int                    `json:"height,omitempty"`
	SHA256            string                 `json:"sha256,omitempty"`
	Caption           string                 `json:"caption,omitempty"`
	DerivedFromPartID string                 `json:"derived_from_part_id,omitempty"`
}

type MessageContent struct {
	Parts []MessagePart `json:"parts"`
}

type ReturnRoute struct {
	Mode             ReturnMode `json:"mode"`
	SourceEndpointID EndpointID `json:"source_endpoint_id,omitempty"`
	EndpointID       EndpointID `json:"endpoint_id,omitempty"`
}

type MessageEnvelope struct {
	SchemaVersion  int                  `json:"schema_version"`
	ID             string               `json:"id"`
	IdempotencyKey string               `json:"idempotency_key"`
	CorrelationID  string               `json:"correlation_id,omitempty"`
	CausationID    string               `json:"causation_id,omitempty"`
	Source         MessageSourceContext `json:"source"`
	OwnerID        string               `json:"owner_id"`
	ActorID        string               `json:"actor_id"`
	Content        MessageContent       `json:"content"`
	ReturnRoute    ReturnRoute          `json:"return_route"`
	Authorization  MessageAuthorization `json:"authorization"`
	CreatedAt      time.Time            `json:"created_at"`
}

type RouteStatus string

const (
	RouteMatched   RouteStatus = "matched"
	RouteClarify   RouteStatus = "clarify"
	RouteUnmatched RouteStatus = "unmatched"
	RouteBlocked   RouteStatus = "blocked"
)

type RouteDecision struct {
	SchemaVersion   int               `json:"schema_version"`
	Status          RouteStatus       `json:"status"`
	CatalogRevision string            `json:"catalog_revision"`
	CapabilityPath  []CapabilityID    `json:"capability_path,omitempty"`
	Slots           RouteSlots        `json:"slots,omitempty"`
	Confidence      float64           `json:"confidence,omitempty"`
	Facts           map[string]string `json:"facts,omitempty"`
	Reason          string            `json:"reason,omitempty"`
}

// MessageRunContext persists the identity and return boundary needed by
// idempotent replay and approval/login resume, including unmatched ReAct runs.
type MessageRunContext struct {
	OwnerID       string               `json:"owner_id"`
	Authorization MessageAuthorization `json:"authorization"`
	ReturnRoute   ReturnRoute          `json:"return_route"`
	Route         RouteDecision        `json:"route"`
}

type RouteOperation string

const (
	RouteOperationSearch    RouteOperation = "search"
	RouteOperationRender    RouteOperation = "render"
	RouteOperationRead      RouteOperation = "read"
	RouteOperationOpen      RouteOperation = "open"
	RouteOperationNavigate  RouteOperation = "navigate"
	RouteOperationInspect   RouteOperation = "inspect"
	RouteOperationInteract  RouteOperation = "interact"
	RouteOperationCreate    RouteOperation = "create"
	RouteOperationEdit      RouteOperation = "edit"
	RouteOperationTransform RouteOperation = "transform"
	RouteOperationDelete    RouteOperation = "delete"
)

type RouteFactScope string

const (
	RouteFactScopeCurrentInternet RouteFactScope = "current_internet_state"
	RouteFactScopeWeatherSnapshot RouteFactScope = "weather_snapshot"
)

// RouteSlots are semantic inputs only. They deliberately cannot identify a
// tool, workflow step, risk level, policy decision, or model lane.
type RouteSlots struct {
	Operation  RouteOperation `json:"operation,omitempty"`
	FactScope  RouteFactScope `json:"fact_scope,omitempty"`
	Query      string         `json:"query,omitempty"`
	Location   string         `json:"location,omitempty"`
	TargetKind string         `json:"target_kind,omitempty"`
	TargetRef  string         `json:"target_ref,omitempty"`
	TargetRefs []string       `json:"target_refs,omitempty"`
	OutputRef  string         `json:"output_ref,omitempty"`
	Format     string         `json:"format,omitempty"`
}

func (slots RouteSlots) Empty() bool {
	return slots.Operation == "" && slots.FactScope == "" && slots.Query == "" && slots.Location == "" &&
		slots.TargetKind == "" && slots.TargetRef == "" && len(slots.TargetRefs) == 0 && slots.OutputRef == "" && slots.Format == ""
}

type WorkflowContractRef struct {
	ID       WorkflowID `json:"id"`
	Revision int        `json:"revision"`
}

type WorkflowResultStatus string

const (
	WorkflowResultSucceeded WorkflowResultStatus = "succeeded"
	WorkflowResultWaiting   WorkflowResultStatus = "waiting"
	WorkflowResultBlocked   WorkflowResultStatus = "blocked"
	WorkflowResultFailed    WorkflowResultStatus = "failed"
)

type WorkflowResumeState struct {
	Kind  string         `json:"kind"`
	Token string         `json:"token,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

type WorkflowResultError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type WorkflowResult struct {
	SchemaVersion  int                  `json:"schema_version"`
	ID             string               `json:"id"`
	RunID          string               `json:"run_id"`
	OwnerID        string               `json:"owner_id"`
	Authorization  MessageAuthorization `json:"authorization"`
	Status         WorkflowResultStatus `json:"status"`
	CapabilityPath []CapabilityID       `json:"capability_path"`
	Workflow       WorkflowContractRef  `json:"workflow"`
	Data           map[string]any       `json:"data,omitempty"`
	Content        MessageContent       `json:"content"`
	References     []ResourceRef        `json:"references,omitempty"`
	ReturnRoute    ReturnRoute          `json:"return_route"`
	Resume         *WorkflowResumeState `json:"resume,omitempty"`
	Error          *WorkflowResultError `json:"error,omitempty"`
}

type DeliveryRequest struct {
	SchemaVersion  int                  `json:"schema_version"`
	ID             DeliveryID           `json:"id"`
	IdempotencyKey string               `json:"idempotency_key"`
	ResultID       string               `json:"result_id,omitempty"`
	RunID          string               `json:"run_id,omitempty"`
	OwnerID        string               `json:"owner_id"`
	ActorID        string               `json:"actor_id"`
	Authorization  MessageAuthorization `json:"authorization"`
	Target         EndpointID           `json:"target"`
	Content        MessageContent       `json:"content"`
	Origin         DeliveryOrigin       `json:"origin"`
	ApprovalSource string               `json:"approval_source,omitempty"`
	ContentDigest  string               `json:"content_digest,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
}

type DeliveryOrigin string

const (
	DeliveryOriginWebDirect     DeliveryOrigin = "web_direct"
	DeliveryOriginAgentWorkflow DeliveryOrigin = "agent_workflow"
	DeliveryOriginSourceReply   DeliveryOrigin = "source_reply"
	DeliveryOriginSchedule      DeliveryOrigin = "schedule"
)

type DeliveryCapabilities struct {
	Kinds                []MessagePartKind         `json:"kinds"`
	Dispositions         []MessagePartDisposition  `json:"dispositions"`
	FileFallbackKinds    []MessagePartKind         `json:"file_fallback_kinds,omitempty"`
	NativeVoiceTypes     []string                  `json:"native_voice_types,omitempty"`
	MaxParts             int                       `json:"max_parts"`
	MaxTotalBytes        int64                     `json:"max_total_bytes"`
	MaxBytesByKind       map[MessagePartKind]int64 `json:"max_bytes_by_kind,omitempty"`
	SupportsCaption      bool                      `json:"supports_caption"`
	SupportsNativeVoice  bool                      `json:"supports_native_voice"`
	SupportsFileFallback bool                      `json:"supports_file_fallback"`
}

type DeliveryStatus string

const (
	DeliveryDraft            DeliveryStatus = "draft"
	DeliveryTargetResolved   DeliveryStatus = "target_resolved"
	DeliveryAwaitingApproval DeliveryStatus = "awaiting_send_approval"
	DeliveryApproved         DeliveryStatus = "approved"
	DeliverySending          DeliveryStatus = "sending"
	DeliveryPending          DeliveryStatus = "pending"
	DeliverySucceeded        DeliveryStatus = "sent"
	DeliveryPartiallySent    DeliveryStatus = "partially_sent"
	DeliveryFailed           DeliveryStatus = "failed"
	DeliveryOutcomeUnknown   DeliveryStatus = "outcome_unknown"
)

type PartDeliveryReceipt struct {
	PartID         string `json:"part_id"`
	Status         string `json:"status"`
	Representation string `json:"representation"`
	ProviderRef    string `json:"provider_ref,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
}

type DeliveryReceipt struct {
	DeliveryID   DeliveryID            `json:"delivery_id"`
	EndpointID   EndpointID            `json:"endpoint_id"`
	Status       DeliveryStatus        `json:"status"`
	ProviderRef  string                `json:"provider_ref,omitempty"`
	Error        string                `json:"error,omitempty"`
	ErrorCode    string                `json:"error_code,omitempty"`
	RetryState   string                `json:"retry_state,omitempty"`
	Attempt      int                   `json:"attempt"`
	PartReceipts []PartDeliveryReceipt `json:"part_receipts,omitempty"`
	AttemptedAt  time.Time             `json:"attempted_at"`
	DeliveredAt  *time.Time            `json:"delivered_at,omitempty"`
}

type TargetResolutionStatus string

const (
	TargetDefaultWeb     TargetResolutionStatus = "default_web"
	TargetSourceReply    TargetResolutionStatus = "source_reply"
	TargetNeedsChannel   TargetResolutionStatus = "needs_channel"
	TargetNeedsRecipient TargetResolutionStatus = "needs_recipient"
	TargetAmbiguous      TargetResolutionStatus = "ambiguous"
	TargetResolved       TargetResolutionStatus = "resolved"
	TargetUnavailable    TargetResolutionStatus = "unavailable"
)

type DeliveryTargetSelection struct {
	Status                 TargetResolutionStatus `json:"status"`
	RequestedProviderKey   string                 `json:"requested_provider_key,omitempty"`
	RequestedRecipientText string                 `json:"requested_recipient_text,omitempty"`
	CandidateEndpointIDs   []EndpointID           `json:"candidate_endpoint_ids,omitempty"`
	ResolvedEndpointID     EndpointID             `json:"resolved_endpoint_id,omitempty"`
	ResolutionRule         string                 `json:"resolution_rule"`
}

type MessageLifecycleTransition struct {
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

type MessageReceiveRecord struct {
	ID                   string                       `json:"id"`
	Direction            MessageDirection             `json:"direction"`
	OwnerID              string                       `json:"owner_id"`
	ActorID              string                       `json:"actor_id"`
	ProviderKey          string                       `json:"provider_key"`
	SourceEndpointID     EndpointID                   `json:"source_endpoint_id,omitempty"`
	NativeMessageID      string                       `json:"native_message_id"`
	Status               string                       `json:"status"`
	Transitions          []MessageLifecycleTransition `json:"transitions"`
	LinkedMessageID      string                       `json:"linked_message_id,omitempty"`
	LinkedRunID          string                       `json:"linked_run_id,omitempty"`
	SoftwareDisplayName  string                       `json:"software_display_name,omitempty"`
	RecipientDisplayName string                       `json:"recipient_display_name,omitempty"`
	AccountDisplayName   string                       `json:"account_display_name,omitempty"`
	CreatedAt            time.Time                    `json:"created_at"`
	UpdatedAt            time.Time                    `json:"updated_at"`
}

type MessageDeliveryRecord struct {
	ID                   DeliveryID              `json:"id"`
	Direction            MessageDirection        `json:"direction"`
	OwnerID              string                  `json:"owner_id"`
	ActorID              string                  `json:"actor_id"`
	Origin               DeliveryOrigin          `json:"origin"`
	Request              DeliveryRequest         `json:"request"`
	TargetSelection      DeliveryTargetSelection `json:"target_selection"`
	Status               DeliveryStatus          `json:"status"`
	ApprovalSource       string                  `json:"approval_source,omitempty"`
	ContentDigest        string                  `json:"content_digest"`
	Receipt              *DeliveryReceipt        `json:"receipt,omitempty"`
	Attempts             int                     `json:"attempts"`
	SoftwareDisplayName  string                  `json:"software_display_name,omitempty"`
	RecipientDisplayName string                  `json:"recipient_display_name,omitempty"`
	AccountDisplayName   string                  `json:"account_display_name,omitempty"`
	CreatedAt            time.Time               `json:"created_at"`
	UpdatedAt            time.Time               `json:"updated_at"`
}
