// Package executionevidence records bounded execution facts and exports them
// on request. It does not run models, repeat business operations, or judge
// semantic quality. All identifiers are scoped to one owner, session and run.
package executionevidence

import (
	"context"
	"errors"
	"time"
)

const SchemaVersion = "document_evaluation_v1"

var (
	ErrUnbound    = errors.New("execution evidence context is not bound")
	ErrScope      = errors.New("execution evidence scope is invalid or inaccessible")
	ErrLimit      = errors.New("execution evidence resource limit exceeded")
	ErrIncomplete = errors.New("execution evidence is incomplete; diagnostic export is required")
	ErrIntegrity  = errors.New("execution evidence integrity check failed")
	ErrSealed     = errors.New("execution evidence run is sealed")
)

type Scope struct {
	OwnerID   string `json:"owner_id"`
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
}

// Options uses finite defaults for nonpositive values. Redact receives valid
// JSON, or UTF-8 text for a text payload, and must preserve its encoding. It is
// not applied to kind=source/output snapshots, whose bytes remain authoritative.
// Record calls report capture errors; callers retain business-result ownership.
// There is no automatic eviction: quota exhaustion is explicit.
type Options struct {
	MaxPayloadBytes  int64
	MaxRunBytes      int64
	MaxTotalBytes    int64
	MaxExportBytes   int64
	MaxEventBytes    int64
	MaxEvents        int
	OperationTimeout time.Duration
	ExportTimeout    time.Duration
	Redact           func([]byte) []byte
}

type Event struct {
	ID         string         `json:"event_id"`
	RunID      string         `json:"run_id"`
	Seq        uint64         `json:"seq"`
	Kind       string         `json:"kind"`
	ParentID   string         `json:"parent_id,omitempty"`
	CauseIDs   []string       `json:"cause_ids,omitempty"`
	SpanID     string         `json:"span_id,omitempty"`
	CallID     string         `json:"call_id,omitempty"`
	NodeID     string         `json:"node_id,omitempty"`
	Stage      string         `json:"stage,omitempty"`
	Attempt    int            `json:"attempt,omitempty"`
	Status     string         `json:"status,omitempty"`
	PayloadIDs []string       `json:"payload_ids,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	At         time.Time      `json:"at"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	ErrorCode  string         `json:"error_code,omitempty"`
}

type Payload struct {
	ID              string    `json:"payload_id"`
	Kind            string    `json:"kind"`
	Name            string    `json:"display_name"`
	ContentType     string    `json:"content_type"`
	Path            string    `json:"bundle_entry"`
	Bytes           int64     `json:"bytes"`
	SHA256          string    `json:"sha256"`
	OriginalSHA256  string    `json:"original_sha256,omitempty"`
	Redacted        bool      `json:"redacted"`
	RedactionReason string    `json:"redaction_reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Gap struct {
	Code   string    `json:"code"`
	Detail string    `json:"detail"`
	At     time.Time `json:"at"`
}

type CaptureStatus struct {
	Complete  bool     `json:"capture_complete"`
	Sealed    bool     `json:"sealed"`
	Gaps      []Gap    `json:"gaps"`
	OpenSpans []string `json:"open_spans"`
}

type ExportStatus struct {
	Complete       bool      `json:"package_complete"`
	Diagnostic     bool      `json:"diagnostic_only"`
	MissingEntries []string  `json:"missing_entries"`
	GeneratedAt    time.Time `json:"generated_at,omitempty"`
}

type ReviewStatus struct {
	Mode   string `json:"mode"`
	Status string `json:"status"`
}

type Manifest struct {
	SchemaVersion    string        `json:"schema_version"`
	Scope            Scope         `json:"scope"`
	Status           string        `json:"business_status"`
	StartedAt        time.Time     `json:"started_at"`
	CompletedAt      *time.Time    `json:"completed_at,omitempty"`
	RequestPayloadID string        `json:"request_payload_id,omitempty"`
	EventCount       uint64        `json:"event_count"`
	EventBytes       int64         `json:"event_bytes"`
	Payloads         []Payload     `json:"payloads"`
	Capture          CaptureStatus `json:"capture"`
	Export           ExportStatus  `json:"export"`
	Review           ReviewStatus  `json:"review"`
}

type binding struct {
	recorder *Recorder
	scope    Scope
	parentID string
}
type bindingKey struct{}

func (r *Recorder) Bind(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, bindingKey{}, binding{recorder: r, scope: scope})
}
func WithRecorder(ctx context.Context, recorder *Recorder, scope Scope) context.Context {
	return recorder.Bind(ctx, scope)
}
func WithParent(ctx context.Context, eventID string) context.Context {
	b, ok := ctx.Value(bindingKey{}).(binding)
	if !ok {
		return ctx
	}
	b.parentID = eventID
	return context.WithValue(ctx, bindingKey{}, b)
}
func ScopeFromContext(ctx context.Context) (Scope, bool) {
	b, ok := ctx.Value(bindingKey{}).(binding)
	return b.scope, ok && b.recorder != nil
}
