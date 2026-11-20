package contract

import "encoding/json"

// SessionHeader is the list form of a conversation (design 4.2 sync.changes).
type SessionHeader struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Revision  int64  `json:"revision"`
	LastSeq   int64  `json:"last_seq"`
	Preview   string `json:"preview,omitempty"`
	Archived  bool   `json:"archived,omitempty"`
	Deleted   bool   `json:"deleted,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	// Running is true while a run is in progress in this conversation.
	Running bool `json:"running,omitempty"`
}

// AttachmentRef is how a message refers to a stored object. Lists and
// message pages carry only the name, size and type.
type AttachmentRef struct {
	ObjectID    string `json:"object_id,omitempty"`
	Name        string `json:"name"`
	Size        int64  `json:"size,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

// Message is one stored message.
type Message struct {
	ID              string          `json:"id"`
	SessionID       string          `json:"session_id"`
	Seq             int64           `json:"seq"`
	Role            string          `json:"role"`
	Content         string          `json:"content"`
	RunID           string          `json:"run_id,omitempty"`
	ClientMessageID string          `json:"client_message_id,omitempty"`
	Attachments     []AttachmentRef `json:"attachments,omitempty"`
	CreatedAt       string          `json:"created_at"`
	// Extra carries presentation data the original client already renders
	// (clarification, client actions). Opaque to the link layer.
	Extra json.RawMessage `json:"extra,omitempty"`
}

// SyncChangesParams asks for conversation headers changed after Cursor.
type SyncChangesParams struct {
	Cursor int64 `json:"cursor"`
	Limit  int   `json:"limit,omitempty"`
}

// SyncChangesResult returns changed headers ordered by revision.
type SyncChangesResult struct {
	Sessions []SessionHeader `json:"sessions"`
	Cursor   int64           `json:"cursor"`
	More     bool            `json:"more,omitempty"`
}

// SessionCreateParams creates a conversation. ClientRequestID makes it
// idempotent.
type SessionCreateParams struct {
	Title           string `json:"title,omitempty"`
	ClientRequestID string `json:"client_request_id"`
}

// SessionRef names a conversation.
type SessionRef struct {
	SessionID string `json:"session_id"`
}

// SessionRenameParams renames a conversation.
type SessionRenameParams struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
}

// SessionArchiveParams archives or restores a conversation.
type SessionArchiveParams struct {
	SessionID string `json:"session_id"`
	Archived  bool   `json:"archived"`
}

// SessionMessagesParams pages backward through history. BeforeSeq 0 means
// the latest page.
type SessionMessagesParams struct {
	SessionID string `json:"session_id"`
	BeforeSeq int64  `json:"before_seq,omitempty"`
	AfterSeq  int64  `json:"after_seq,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// SessionMessagesResult is one page, oldest first.
type SessionMessagesResult struct {
	Session  SessionHeader `json:"session"`
	Messages []Message     `json:"messages"`
	// More is true when older messages exist before the first one.
	More bool `json:"more,omitempty"`
}

// MessageSendParams sends a user message. The same ClientMessageID returns
// the original result (design 4.2).
type MessageSendParams struct {
	SessionID       string          `json:"session_id"`
	ClientMessageID string          `json:"client_message_id"`
	Content         string          `json:"content"`
	Attachments     []AttachmentRef `json:"attachments,omitempty"`
	ClientTimezone  string          `json:"client_timezone,omitempty"`
	// ClientPresentation declares a display the sending desktop can run
	// itself (its local schedules). The backend may answer with a client
	// action in the final message's extra instead of reading the data.
	ClientPresentation *ClientPresentation `json:"client_presentation,omitempty"`
	// ReplayOnly asks for the original admission of ClientMessageID
	// without ever admitting it: not_found means the backend never
	// received it. A client retries a send whose reply was lost this way,
	// so a message that never arrived is not run late.
	ReplayOnly bool `json:"replay_only,omitempty"`
}

// ClientPresentation is the desktop's local display capability for one
// message. LocalConversationID is the desktop's own conversation id, which
// the returned client action names.
type ClientPresentation struct {
	SchemaVersion       int    `json:"schema_version"`
	ScheduleShow        bool   `json:"schedule_show"`
	Timezone            string `json:"timezone"`
	RequestedAt         string `json:"requested_at"`
	LocalConversationID string `json:"local_conversation_id"`
}

// MessageSendResult acknowledges admission; the reply arrives through
// run.subscribe, run.status or session.messages.
type MessageSendResult struct {
	Message Message `json:"message"`
	RunID   string  `json:"run_id"`
	// Replayed is true when the idempotency key matched an earlier send.
	Replayed bool `json:"replayed,omitempty"`
}

// Run states.
const (
	RunRunning         = "running"
	RunApprovalPending = "approval_pending"
	RunCompleted       = "completed"
	RunFailed          = "failed"
	RunCancelled       = "cancelled"
	RunBlocked         = "blocked"
	RunIdle            = "idle"
)

// RunStatus is the current snapshot of a conversation's run.
type RunStatus struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id,omitempty"`
	State     string `json:"state"`
	// Preview is the streaming output produced so far. It exists only in
	// memory and has no seq; on completion it becomes one final message.
	Preview string `json:"preview,omitempty"`
	// Progress is a short human status line (tool name, stage).
	Progress string `json:"progress,omitempty"`
	// FinalMessage is set once the run completed.
	FinalMessage *Message `json:"final_message,omitempty"`
	Error        *Error   `json:"error,omitempty"`
	// PendingApprovals lists approvals blocking this run.
	PendingApprovals []Approval `json:"pending_approvals,omitempty"`
	UpdatedAt        string     `json:"updated_at,omitempty"`
	// Seq is the sequence of the last run event this snapshot includes.
	// Events with a higher seq follow it; a gap means events were lost and
	// the client reads run.status again.
	Seq int64 `json:"seq,omitempty"`
}

// RunSubscribeParams subscribes to a conversation's run output.
type RunSubscribeParams struct {
	SessionID string `json:"session_id"`
}

// RunSubscribeResult returns the current snapshot first; increments follow
// as events on topic "run".
type RunSubscribeResult struct {
	Subscription string    `json:"subscription"`
	Status       RunStatus `json:"status"`
}

// RunEvent is the data of a "run" event.
type RunEvent struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id,omitempty"`
	// Kind: delta | progress | state | final
	Kind   string     `json:"kind"`
	Text   string     `json:"text,omitempty"`
	Status *RunStatus `json:"status,omitempty"`
	// Seq increases by one per event of a conversation, across runs and
	// connections, so a client orders events and detects gaps.
	Seq int64 `json:"seq"`
}

// RunRef names a conversation's run.
type RunRef struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id,omitempty"`
}

// Approval is one pending or decided approval.
type Approval struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id,omitempty"`
	RunID     string          `json:"run_id,omitempty"`
	Title     string          `json:"title"`
	Summary   string          `json:"summary,omitempty"`
	Risk      string          `json:"risk,omitempty"`
	Status    string          `json:"status"`
	ToolName  string          `json:"tool_name,omitempty"`
	CreatedAt string          `json:"created_at,omitempty"`
	ExpiresAt string          `json:"expires_at,omitempty"`
	Detail    json.RawMessage `json:"detail,omitempty"`
}

// ApprovalListParams filters approvals.
type ApprovalListParams struct {
	SessionID string `json:"session_id,omitempty"`
	Pending   bool   `json:"pending,omitempty"`
}

// ApprovalListResult lists approvals.
type ApprovalListResult struct {
	Approvals []Approval `json:"approvals"`
}

// ApprovalResolveParams decides an approval. IdempotencyKey makes a retry
// return the original outcome.
type ApprovalResolveParams struct {
	ApprovalID     string          `json:"approval_id"`
	Decision       string          `json:"decision"` // approve | reject
	Inputs         json.RawMessage `json:"inputs,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
}

// ApprovalResolveResult returns the decided approval.
type ApprovalResolveResult struct {
	Approval Approval `json:"approval"`
}

// ObjectUploadBeginParams starts a chunked, resumable upload.
type ObjectUploadBeginParams struct {
	UploadKey   string `json:"upload_key"` // client idempotency key; resumes
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
	SHA256      string `json:"sha256"` // hex digest of the whole content
	SessionID   string `json:"session_id,omitempty"`
	// Purpose makes the upload a domain object (for example a mail
	// attachment or a recording) instead of a conversation attachment. A
	// domain object gets a UUID object_id, is passed to domain methods by
	// reference and expires after 24 hours.
	Purpose string `json:"purpose,omitempty"`
}

// ObjectUploadState reports how much the backend already holds.
type ObjectUploadState struct {
	UploadID  string `json:"upload_id"`
	Received  int64  `json:"received"`
	ChunkSize int    `json:"chunk_size"`
	// ObjectID is set once committed.
	ObjectID string `json:"object_id,omitempty"`
}

// ObjectUploadChunkParams appends one chunk at Offset.
type ObjectUploadChunkParams struct {
	UploadID string `json:"upload_id"`
	Offset   int64  `json:"offset"`
	Data     []byte `json:"data"`
}

// ObjectUploadCommitParams finishes an upload after the digest check.
type ObjectUploadCommitParams struct {
	UploadID string `json:"upload_id"`
}

// ObjectRef names a stored object.
type ObjectRef struct {
	ObjectID string `json:"object_id"`
}

// ObjectReadParams reads a range.
type ObjectReadParams struct {
	ObjectID string `json:"object_id"`
	Offset   int64  `json:"offset"`
	Length   int    `json:"length,omitempty"`
}

// ObjectReadResult returns one chunk.
type ObjectReadResult struct {
	Object AttachmentRef `json:"object"`
	Offset int64         `json:"offset"`
	Data   []byte        `json:"data"`
	EOF    bool          `json:"eof"`
}

// DomainCallParams invokes one business-domain method.
type DomainCallParams struct {
	Domain         string          `json:"domain"`
	Method         string          `json:"method"`
	Args           json.RawMessage `json:"args,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	// ReplayOnly asks for the original outcome of IdempotencyKey without
	// ever starting it: not_found means it never ran.
	ReplayOnly bool `json:"replay_only,omitempty"`
	// ArgsObject names a domain object (uploaded with purpose domain_args)
	// that holds arguments too large for one frame. It replaces Args.
	ArgsObject string `json:"args_object,omitempty"`
}

// DomainCallResult carries the method's result.
type DomainCallResult struct {
	Result json.RawMessage `json:"result,omitempty"`
}

// DeviceDescribeResult tells a device how the backend sees it.
type DeviceDescribeResult struct {
	Principal Principal `json:"principal"`
	Features  []string  `json:"features"`
	Domains   []string  `json:"domains,omitempty"`
	Backend   string    `json:"backend,omitempty"`
	// DeploymentID is the backend installation's persistent identity.
	DeploymentID string `json:"deployment_id,omitempty"`
}

// ProjectionParams reads a device projection.
type ProjectionParams struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// DomainEvent invalidates business-domain reads: the client reads the
// named domains again. Like every event it only accelerates.
type DomainEvent struct {
	Domains []string `json:"domains"`
}

// Event topics.
const (
	TopicRun      = "run"
	TopicSync     = "sync"     // a conversation changed; data {cursor}
	TopicApproval = "approval" // approvals changed
	TopicDomain   = "domain"   // business-domain data changed; data DomainEvent
	TopicDevice   = "device"   // this device was revoked or changed
)

// SyncEvent is the data of a "sync" event.
type SyncEvent struct {
	Cursor int64 `json:"cursor"`
}
