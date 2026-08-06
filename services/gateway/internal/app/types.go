package app

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
)

type RiskLevel string

const (
	RiskRead       RiskLevel = "read"
	RiskDraft      RiskLevel = "draft"
	RiskReversible RiskLevel = "reversible"
	RiskDangerous  RiskLevel = "dangerous"
)

type ToolDefinition struct {
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	InputSchema      map[string]any         `json:"input_schema"`
	OutputSchema     map[string]any         `json:"output_schema,omitempty"`
	Annotations      map[string]any         `json:"annotations,omitempty"`
	Risk             RiskLevel              `json:"risk"`
	RequiresApproval bool                   `json:"requires_approval"`
	Idempotent       bool                   `json:"idempotent"`
	TimeoutMS        int                    `json:"timeout_ms"`
	Sandbox          string                 `json:"sandbox"`
	Audit            string                 `json:"audit"`
	Capabilities     []CapabilityDescriptor `json:"capabilities,omitempty"`
	OutcomeAdapter   ToolOutcomeAdapter     `json:"outcome_adapter,omitempty"`
	Directory        ToolDirectoryMetadata  `json:"directory,omitempty"`
}

type ToolCall struct {
	ID                 string         `json:"id"`
	SessionID          string         `json:"session_id"`
	RunID              string         `json:"run_id"`
	Tool               string         `json:"tool"`
	Risk               RiskLevel      `json:"risk"`
	Status             string         `json:"status"`
	Arguments          map[string]any `json:"arguments"`
	Result             any            `json:"result,omitempty"`
	Error              string         `json:"error,omitempty"`
	ErrorCode          string         `json:"error_code,omitempty"`
	ApprovalID         string         `json:"approval_id,omitempty"`
	StartedAt          time.Time      `json:"started_at"`
	CompletedAt        *time.Time     `json:"completed_at,omitempty"`
	ObservationRef     string         `json:"observation_ref,omitempty"`
	ObservationSummary string         `json:"observation_summary,omitempty"`
	WorkflowID         WorkflowID     `json:"workflow_id,omitempty"`
	WorkflowNodeID     WorkflowNodeID `json:"workflow_node_id,omitempty"`
	ScopeRevision      int            `json:"scope_revision,omitempty"`
	Capability         string         `json:"capability,omitempty"`
}

type Approval struct {
	ID              string                   `json:"id"`
	Source          ApprovalSource           `json:"source"`
	ExternalID      string                   `json:"external_id,omitempty"`
	ExternalContext *ExternalApprovalContext `json:"external_context,omitempty"`
	SessionID       string                   `json:"session_id"`
	RunID           string                   `json:"run_id"`
	ToolCallID      string                   `json:"tool_call_id"`
	Tool            string                   `json:"tool"`
	Risk            RiskLevel                `json:"risk"`
	Status          string                   `json:"status"`
	Summary         string                   `json:"summary"`
	Reason          string                   `json:"reason"`
	Resources       []string                 `json:"resources"`
	Arguments       map[string]any           `json:"arguments"`
	CreatedAt       time.Time                `json:"created_at"`
	ResolvedAt      *time.Time               `json:"resolved_at,omitempty"`
	ResolutionNote  string                   `json:"resolution_note,omitempty"`
}

type ApprovalSource string

const (
	ApprovalSourceTool          ApprovalSource = "tool"
	ApprovalSourceHappyTeamPlan ApprovalSource = "happy_team_plan"
)

type ExternalApprovalContext struct {
	Provider         string `json:"provider"`
	Title            string `json:"title"`
	GoalPrompt       string `json:"goal_prompt"`
	Plan             string `json:"plan,omitempty"`
	PlanAvailability string `json:"plan_availability"`
	PlanEdited       bool   `json:"plan_edited,omitempty"`
}

const (
	ExternalPlanAvailable              = "available"
	ExternalPlanTemporarilyUnavailable = "temporarily_unavailable"
	MaxExternalApprovalPlanBytes       = 1 << 20
)

type Reminder struct {
	ID               string        `json:"id"`
	SessionID        string        `json:"session_id,omitempty"`
	RunID            string        `json:"run_id,omitempty"`
	Text             string        `json:"text"`
	TextSummary      string        `json:"text_summary"`
	DueTime          time.Time     `json:"due_time"`
	Timezone         string        `json:"timezone"`
	Channel          string        `json:"channel"`
	Recipient        string        `json:"recipient"`
	RecipientBinding string        `json:"recipient_binding,omitempty"`
	BindingID        string        `json:"binding_id,omitempty"`
	CredentialRef    string        `json:"credential_ref,omitempty"`
	BaseURL          string        `json:"base_url,omitempty"`
	Recurrence       string        `json:"recurrence,omitempty"`
	DedupeKey        string        `json:"dedupe_key,omitempty"`
	Status           string        `json:"status"`
	LastDeliveryID   string        `json:"last_delivery_id,omitempty"`
	LastError        string        `json:"last_error,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	SentAt           *time.Time    `json:"sent_at,omitempty"`
	CanceledAt       *time.Time    `json:"canceled_at,omitempty"`
	DeliveryAttempt  int           `json:"delivery_attempt"`
	ScheduleSpec     *ScheduleSpec `json:"schedule_spec,omitempty"`
}

type ReminderFilter struct {
	Status string
	From   *time.Time
	To     *time.Time
	Limit  int
}

type ReminderDelivery struct {
	ID             string    `json:"id"`
	ReminderID     string    `json:"reminder_id"`
	Channel        string    `json:"channel"`
	Provider       string    `json:"provider"`
	Recipient      string    `json:"recipient"`
	Status         string    `json:"status"`
	ProviderStatus string    `json:"provider_status,omitempty"`
	Error          string    `json:"error,omitempty"`
	RetryState     string    `json:"retry_state,omitempty"`
	Attempt        int       `json:"attempt"`
	SentAt         time.Time `json:"sent_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type NotificationBinding struct {
	ID                string     `json:"id"`
	OwnerID           string     `json:"owner_id"`
	ActorID           string     `json:"actor_id,omitempty"`
	Channel           string     `json:"channel"`
	Provider          string     `json:"provider"`
	Status            string     `json:"status"`
	DisplayName       string     `json:"display_name,omitempty"`
	ExternalUserID    string     `json:"external_user_id,omitempty"`
	ExternalChatID    string     `json:"external_chat_id,omitempty"`
	ExternalThreadID  string     `json:"external_thread_id,omitempty"`
	AccountID         string     `json:"account_id,omitempty"`
	CredentialRef     string     `json:"credential_ref,omitempty"`
	BaseURL           string     `json:"base_url,omitempty"`
	ProviderSessionID string     `json:"provider_session_id,omitempty"`
	ProviderState     string     `json:"provider_state,omitempty"`
	ContextToken      string     `json:"context_token,omitempty"`
	ProviderCursor    string     `json:"provider_cursor,omitempty"`
	QRCodeURL         string     `json:"qr_code_url,omitempty"`
	QRCodeImage       string     `json:"qr_code_image,omitempty"`
	DefaultForChannel bool       `json:"default_for_channel"`
	Scopes            []string   `json:"scopes,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
}

// ConnectorSetting is the owner's explicit opt-in for a third-party message
// channel. Account bindings remain separate so disabling a channel does not
// silently delete encrypted credentials or account setup.
type ConnectorSetting struct {
	OwnerID   string    `json:"owner_id"`
	Channel   string    `json:"channel"`
	Enabled   bool      `json:"enabled"`
	Version   int64     `json:"version"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ExternalChatSession struct {
	ID                string    `json:"id"`
	OwnerID           string    `json:"owner_id,omitempty"`
	AuthorizedOwnerID string    `json:"authorized_owner_id,omitempty"`
	AuthorizedActorID string    `json:"authorized_actor_id,omitempty"`
	WorkspaceRoot     string    `json:"workspace_root,omitempty"`
	BindingID         string    `json:"binding_id"`
	Channel           string    `json:"channel"`
	Provider          string    `json:"provider"`
	ExternalUserID    string    `json:"external_user_id,omitempty"`
	ExternalChatID    string    `json:"external_chat_id,omitempty"`
	ExternalThreadID  string    `json:"external_thread_id,omitempty"`
	DisplayName       string    `json:"display_name,omitempty"`
	LinkedSessionID   string    `json:"linked_session_id,omitempty"`
	Status            string    `json:"status"`
	ProviderCursor    string    `json:"provider_cursor,omitempty"`
	LastContextToken  string    `json:"last_context_token,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ExternalChatMessage struct {
	ID                string    `json:"id"`
	ChatSessionID     string    `json:"chat_session_id"`
	BindingID         string    `json:"binding_id"`
	Channel           string    `json:"channel"`
	Direction         string    `json:"direction"`
	Role              string    `json:"role"`
	ExternalMessageID string    `json:"external_message_id,omitempty"`
	Content           string    `json:"content"`
	ContextToken      string    `json:"context_token,omitempty"`
	LinkedRunID       string    `json:"linked_run_id,omitempty"`
	Status            string    `json:"status"`
	Error             string    `json:"error,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Transitional aliases keep old file snapshots and downstream callers
// readable while channel chat persistence moves to connector-neutral names.
type WeixinChatSession = ExternalChatSession
type WeixinChatMessage = ExternalChatMessage

type ChannelInboxUpdate struct {
	ID          string          `json:"id"`
	BindingID   string          `json:"binding_id"`
	Channel     string          `json:"channel"`
	ExternalID  string          `json:"external_id"`
	ChatKey     string          `json:"chat_key"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	AvailableAt time.Time       `json:"available_at"`
	LastError   string          `json:"last_error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type CredentialSecret struct {
	Ref       string    `json:"ref"`
	Kind      string    `json:"kind"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const (
	BrowserAuthStatusActive  = "active"
	BrowserAuthStatusExpired = "expired"
	BrowserAuthStatusRevoked = "revoked"
	BrowserAuthStatusFailed  = "failed"
)

const (
	BrowserHandoffStatusWaitingOwner      = "waiting_owner"
	BrowserHandoffStatusReopeningVisible  = "reopening_visible"
	BrowserHandoffStatusValidatingVisible = "validating_visible"
	BrowserHandoffStatusTransferring      = "transferring_profile"
	BrowserHandoffStatusValidatingHidden  = "validating_hidden"
	BrowserHandoffStatusResumingWorkflow  = "resuming_workflow"
	BrowserHandoffStatusResolved          = "resolved"
	BrowserHandoffStatusCanceled          = "canceled"
	BrowserHandoffStatusFailed            = "failed"

	BrowserLoginBlockStatusWaiting  = BrowserHandoffStatusWaitingOwner
	BrowserLoginBlockStatusResuming = BrowserHandoffStatusValidatingVisible
	BrowserLoginBlockStatusResolved = BrowserHandoffStatusResolved
	BrowserLoginBlockStatusCanceled = BrowserHandoffStatusCanceled
	BrowserLoginBlockStatusFailed   = BrowserHandoffStatusFailed
)

// browserHandoffActiveStatuses is the single source of truth for the handoff
// statuses that count as "still in progress". Store backends must derive
// their active-block predicates (the memory allowlist and the postgres SQL
// status list) from this list so the backends cannot disagree.
var browserHandoffActiveStatuses = []string{
	BrowserHandoffStatusWaitingOwner,
	BrowserHandoffStatusReopeningVisible,
	BrowserHandoffStatusValidatingVisible,
	BrowserHandoffStatusTransferring,
	BrowserHandoffStatusValidatingHidden,
	BrowserHandoffStatusResumingWorkflow,
}

// BrowserHandoffActiveStatuses returns the statuses in which a browser login
// handoff is still in progress.
func BrowserHandoffActiveStatuses() []string {
	return slices.Clone(browserHandoffActiveStatuses)
}

// BrowserHandoffStatusActive reports whether status marks a browser login
// handoff that is still in progress.
func BrowserHandoffStatusActive(status string) bool {
	return slices.Contains(browserHandoffActiveStatuses, strings.TrimSpace(status))
}

type BrowserAuthRecord struct {
	ID               string     `json:"id"`
	OwnerID          string     `json:"owner_id"`
	BrowserProfileID string     `json:"browser_profile_id"`
	SiteOrigin       string     `json:"site_origin"`
	SiteRealm        string     `json:"site_realm,omitempty"`
	AccountHint      string     `json:"account_hint,omitempty"`
	AuthStrategy     string     `json:"auth_strategy"`
	Status           string     `json:"status"`
	SessionRef       string     `json:"session_ref,omitempty"`
	CredentialRef    string     `json:"credential_ref,omitempty"`
	CookieJarRef     string     `json:"cookie_jar_ref,omitempty"`
	LastVerifiedAt   time.Time  `json:"last_verified_at,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
}

type BrowserLoginBlock struct {
	SchemaVersion        int                     `json:"schema_version"`
	Version              int64                   `json:"version"`
	TransitionOwnerID    string                  `json:"transition_owner_id,omitempty"`
	TransitionLeaseUntil *time.Time              `json:"transition_lease_until,omitempty"`
	ID                   string                  `json:"id"`
	SessionID            string                  `json:"session_id"`
	RunID                string                  `json:"run_id"`
	WorkflowID           WorkflowID              `json:"workflow_id,omitempty"`
	WorkflowRevision     int                     `json:"workflow_revision,omitempty"`
	WorkflowNodeID       WorkflowNodeID          `json:"workflow_node_id,omitempty"`
	SessionGeneration    uint64                  `json:"session_generation,omitempty"`
	Status               string                  `json:"status"`
	OriginalGoal         string                  `json:"original_goal"`
	ResumeTool           string                  `json:"resume_tool"`
	ResumeArgs           map[string]any          `json:"resume_args"`
	LastToolCallID       string                  `json:"last_tool_call_id,omitempty"`
	LoginHandoffURL      string                  `json:"login_handoff_url,omitempty"`
	LoginHandoffPageID   string                  `json:"login_handoff_page_id,omitempty"`
	LastVisiblePageID    string                  `json:"last_visible_page_id,omitempty"`
	OwnerID              string                  `json:"owner_id"`
	BrowserProfileID     string                  `json:"browser_profile_id"`
	SiteOrigin           string                  `json:"site_origin"`
	SiteRealm            string                  `json:"site_realm,omitempty"`
	AccountHint          string                  `json:"account_hint,omitempty"`
	BrowserAuthStatus    string                  `json:"browser_auth_status,omitempty"`
	Target               BrowserTargetDescriptor `json:"target,omitempty"`
	VisibleEvidence      *BrowserResultEvidence  `json:"visible_evidence,omitempty"`
	LastUserReply        string                  `json:"last_user_reply,omitempty"`
	LastError            string                  `json:"last_error,omitempty"`
	CreatedAt            time.Time               `json:"created_at"`
	UpdatedAt            time.Time               `json:"updated_at"`
	ResolvedAt           *time.Time              `json:"resolved_at,omitempty"`
}

type Memory struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	SourceID  string    `json:"source_run_id"`
	CreatedAt time.Time `json:"created_at"`
}

type MemoryCandidate struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"session_id"`
	RunID       string     `json:"run_id"`
	Kind        string     `json:"kind"`
	Content     string     `json:"content"`
	Sensitivity string     `json:"sensitivity"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	CreatedAt   time.Time  `json:"created_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

type MemoryExport struct {
	GeneratedAt      time.Time          `json:"generated_at"`
	OwnerProfile     OwnerProfile       `json:"owner_profile"`
	Memories         []Memory           `json:"memories"`
	MemoryCandidates []MemoryCandidate  `json:"memory_candidates"`
	Episodes         []EpisodeSummary   `json:"episodes"`
	Counts           MemoryExportCounts `json:"counts"`
}

type MemoryExportCounts struct {
	Memories          int `json:"memories"`
	MemoryCandidates  int `json:"memory_candidates"`
	PendingCandidates int `json:"pending_candidates"`
	Episodes          int `json:"episodes"`
}

type Message struct {
	ID          string              `json:"id"`
	SessionID   string              `json:"session_id"`
	Role        string              `json:"role"`
	Content     string              `json:"content"`
	CreatedAt   time.Time           `json:"created_at"`
	RunID       string              `json:"run_id,omitempty"`
	Attachments []MessageAttachment `json:"attachments,omitempty"`
}

type MessageAttachment struct {
	ArtifactID  string `json:"artifact_id,omitempty"`
	Name        string `json:"name"`
	RelPath     string `json:"rel_path"`
	URI         string `json:"uri,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Source      string `json:"source,omitempty"`
	Caption     string `json:"caption,omitempty"`
	Summary     string `json:"understanding_summary,omitempty"`
}

type RunFeedback struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	RunID      string    `json:"run_id"`
	MessageID  string    `json:"message_id,omitempty"`
	Rating     string    `json:"rating"`
	Note       string    `json:"note,omitempty"`
	Correction string    `json:"correction,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Session struct {
	ID            string    `json:"id"`
	OwnerID       string    `json:"owner_id,omitempty"`
	WorkspaceRoot string    `json:"workspace_root,omitempty"`
	Title         string    `json:"title"`
	Source        string    `json:"source,omitempty"`
	Hidden        bool      `json:"hidden,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Client struct {
	ID         string     `json:"id"`
	OwnerID    string     `json:"owner_id,omitempty"`
	ActorID    string     `json:"actor_id,omitempty"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type PairingCode struct {
	ID        string     `json:"id"`
	CodeHash  string     `json:"-"`
	Status    string     `json:"status"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
	ClientID  string     `json:"client_id,omitempty"`
}

const DefaultOwnerID = "owner"

type OwnerProfile struct {
	ID               string            `json:"id"`
	Source           string            `json:"source,omitempty"`
	ExternalRef      string            `json:"external_ref,omitempty"`
	WorkspaceRoot    string            `json:"workspace_root,omitempty"`
	DefaultChannel   string            `json:"default_channel,omitempty"`
	DefaultBindingID string            `json:"default_binding_id,omitempty"`
	DisplayName      string            `json:"display_name"`
	Email            string            `json:"email,omitempty"`
	Preferences      map[string]string `json:"preferences,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

func DefaultOwnerProfile() OwnerProfile {
	now := time.Now().UTC()
	return OwnerProfile{
		ID:          DefaultOwnerID,
		Source:      "web",
		DisplayName: "Owner",
		Preferences: map[string]string{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

type VerifierDecision struct {
	Verdict                  string    `json:"verdict"`
	RiskLevel                string    `json:"risk_level"`
	Lane                     string    `json:"lane"`
	Reason                   string    `json:"reason"`
	RequiredUserConfirmation bool      `json:"required_user_confirmation"`
	SafeNextAction           string    `json:"safe_next_action"`
	CreatedAt                time.Time `json:"created_at"`
}

type AgentRun struct {
	ID             string             `json:"id"`
	SessionID      string             `json:"session_id"`
	State          string             `json:"state"`
	ModelLane      string             `json:"model_lane"`
	Risk           RiskLevel          `json:"risk"`
	StartedAt      time.Time          `json:"started_at"`
	CompletedAt    *time.Time         `json:"completed_at,omitempty"`
	Summary        string             `json:"summary,omitempty"`
	MessageContext *MessageRunContext `json:"message_context,omitempty"`
	Workflow       *WorkflowState     `json:"workflow,omitempty"`
}

type ModelCall struct {
	ID             string     `json:"id"`
	SessionID      string     `json:"session_id,omitempty"`
	RunID          string     `json:"run_id,omitempty"`
	Lane           string     `json:"lane"`
	Profile        string     `json:"profile"`
	Model          string     `json:"model"`
	Operation      string     `json:"operation"`
	Mock           bool       `json:"mock"`
	Fallback       bool       `json:"fallback,omitempty"`
	Status         string     `json:"status"`
	PromptTokens   int        `json:"prompt_tokens"`
	ResponseTokens int        `json:"response_tokens"`
	TotalTokens    int        `json:"total_tokens"`
	LatencyMS      int64      `json:"latency_ms"`
	Error          string     `json:"error,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

type AuditEvent struct {
	ID        string         `json:"id"`
	Time      time.Time      `json:"time"`
	Type      string         `json:"type"`
	SessionID string         `json:"session_id,omitempty"`
	RunID     string         `json:"run_id,omitempty"`
	Actor     string         `json:"actor"`
	Summary   string         `json:"summary"`
	Fields    map[string]any `json:"fields,omitempty"`
}

type Event struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Type      string    `json:"type"`
	SessionID string    `json:"session_id,omitempty"`
	RunID     string    `json:"run_id,omitempty"`
	Payload   any       `json:"payload"`
}

type EvalRun struct {
	ID              string         `json:"id"`
	Profile         string         `json:"profile"`
	Status          string         `json:"status"`
	Summary         string         `json:"summary"`
	Cases           []EvalCase     `json:"cases"`
	FailureArchives []EvalArtifact `json:"failure_archives,omitempty"`
	StartedAt       time.Time      `json:"started_at"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
}

type EvalCase struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	DurationMS int64  `json:"duration_ms"`
}

type EvalArtifact struct {
	CaseName    string `json:"case_name"`
	URI         string `json:"uri"`
	Path        string `json:"path,omitempty"`
	Key         string `json:"key,omitempty"`
	Backend     string `json:"backend"`
	ContentType string `json:"content_type"`
	Bytes       int    `json:"bytes"`
}

type ArtifactObject struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	RunID       string    `json:"run_id,omitempty"`
	EvalID      string    `json:"eval_id,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	Backend     string    `json:"backend"`
	Bucket      string    `json:"bucket,omitempty"`
	Key         string    `json:"key"`
	URI         string    `json:"uri"`
	Path        string    `json:"path,omitempty"`
	ContentType string    `json:"content_type"`
	Bytes       int       `json:"bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

type TraceMetadata struct {
	RunID          string     `json:"run_id"`
	SessionID      string     `json:"session_id"`
	State          string     `json:"state"`
	Risk           RiskLevel  `json:"risk"`
	ModelLane      string     `json:"model_lane"`
	Summary        string     `json:"summary,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	MessageCount   int        `json:"message_count"`
	ToolCallCount  int        `json:"tool_call_count"`
	ApprovalCount  int        `json:"approval_count"`
	ModelCallCount int        `json:"model_call_count"`
	ArtifactURI    string     `json:"artifact_uri,omitempty"`
	ArtifactPath   string     `json:"artifact_path,omitempty"`
}

type EpisodeSummary struct {
	ID              string    `json:"id"`
	SessionID       string    `json:"session_id"`
	RunID           string    `json:"run_id"`
	Goal            string    `json:"goal"`
	Outcome         string    `json:"outcome"`
	Risk            RiskLevel `json:"risk"`
	ModelLane       string    `json:"model_lane"`
	Tools           []string  `json:"tools"`
	Approvals       []string  `json:"approvals"`
	Failures        []string  `json:"failures,omitempty"`
	RepairPerformed bool      `json:"repair_performed"`
	Summary         string    `json:"summary"`
	CreatedAt       time.Time `json:"created_at"`
}
