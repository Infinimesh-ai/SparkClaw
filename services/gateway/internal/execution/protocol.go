// Package execution owns durable request control for workbench execution.
// Submitted execution content is temporary; host content stays in the host Store.
package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
)

const (
	InputBytes      = app.WorkbenchInputBytes
	ContextBytes    = app.WorkbenchContextBytes
	ContextMessages = app.WorkbenchContextMessages
	TaskBytes       = 32 << 20
	OwnerBytes      = 256 << 20
	ResultBytes     = app.WorkbenchResultBytes
	MaxFences       = 100000
	ExecutionBudget = 15 * time.Minute
	ResultRetention = 24 * time.Hour
)

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
var identityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var ErrConflict = errors.New("workbench request conflicts with its durable binding")
var ErrCapacity = errors.New("workbench capacity exhausted; existing fences are retained")
var ErrNotFound = errors.New("workbench execution not found")
var ErrUnavailable = errors.New("workbench control persistence unavailable")
var ErrExpired = errors.New("workbench delivery content unavailable")

type Message struct {
	ID               string                `json:"id"`
	FilesUnavailable bool                  `json:"files_unavailable,omitempty"`
	Role             string                `json:"role"`
	Content          string                `json:"content"`
	Files            []File                `json:"files,omitempty"`
	Clarification    *clarification.Record `json:"clarification,omitempty"`
}
type Envelope struct {
	SchemaVersion      int                     `json:"schema_version"`
	DeploymentID       string                  `json:"deployment_id"`
	OwnerID            string                  `json:"owner_id"`
	ClientID           string                  `json:"client_id"`
	InstallationID     string                  `json:"installation_id"`
	ConversationID     string                  `json:"local_conversation_id"`
	TaskID             string                  `json:"local_task_id"`
	RequestID          string                  `json:"request_id"`
	Messages           []Message               `json:"messages"`
	InputFiles         []File                  `json:"input_files,omitempty"`
	ClientPresentation *app.ClientPresentation `json:"client_presentation,omitempty"`
}
type File struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}
type Payload struct {
	ClientAction  *app.ClientAction     `json:"client_action,omitempty"`
	Clarification *clarification.Record `json:"clarification,omitempty"`
	Content       string                `json:"content"`
	Files         []File                `json:"files"`
}
type Output struct {
	ClientAction  *app.ClientAction
	Clarification *clarification.Record
	Content       string
	Files         map[string][]byte
}
type Result struct {
	Sequence int    `json:"sequence"`
	Digest   string `json:"digest"`
	Payload  string `json:"payload"`
}
type PendingApproval struct {
	ApprovalID string         `json:"approval_id"`
	Digest     string         `json:"digest"`
	Tool       string         `json:"tool"`
	Summary    string         `json:"summary"`
	Arguments  map[string]any `json:"arguments"`
}

type PendingInput struct {
	FileID string `json:"file_id"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

type TerminationReason string

const (
	TerminationGatewayRestartedAwaitingApproval TerminationReason = "gateway_restarted_awaiting_approval"
	TerminationGatewayRestartedAfterApproval    TerminationReason = "gateway_restarted_after_approval"
)

type ApprovalState string

const (
	ApprovalPending         ApprovalState = "pending"
	ApprovalDecided         ApprovalState = "decided"
	ApprovalDecisionUnknown ApprovalState = "decision_unknown"
	ApprovalInvalidated     ApprovalState = "invalidated"
)

// ApprovalRecord is a bounded control receipt, never a continuation. Tool names,
// summaries, arguments and model context remain in the live computation only.
type ApprovalRecord struct {
	ApprovalID string        `json:"approval_id"`
	Digest     string        `json:"digest"`
	Revision   uint64        `json:"revision"`
	State      ApprovalState `json:"state"`
	Decision   string        `json:"decision,omitempty"`
	DecidedAt  *time.Time    `json:"decided_at,omitempty"`
}

type Status struct {
	ClientActionReceipt *app.ClientActionReceipt `json:"client_action_receipt,omitempty"`
	Revision            uint64                   `json:"revision,omitempty"`
	TerminationReason   TerminationReason        `json:"termination_reason,omitempty"`
	ApprovalReceipts    []ApprovalRecord         `json:"approval_receipts,omitempty"`
	ExecutionExpiresAt  *time.Time               `json:"execution_expires_at,omitempty"`
	PendingApprovals    []PendingApproval        `json:"pending_approvals,omitempty"`
	PendingInputs       []PendingInput           `json:"pending_inputs,omitempty"`
	SchemaVersion       int                      `json:"schema_version"`
	RequestID           string                   `json:"request_id"`
	InputDigest         string                   `json:"input_digest"`
	State               string                   `json:"state"`
	ExpiresAt           *time.Time               `json:"expires_at,omitempty"`
	Result              *Result                  `json:"result,omitempty"`
}

// Fence is the complete durable control field allowlist. No maps, arbitrary
// diagnostic strings, context, titles, prompts or tool outputs may be added.
type Fence struct {
	ClientActionIssued  bool                     `json:"client_action_issued,omitempty"`
	ClientActionReceipt *app.ClientActionReceipt `json:"client_action_receipt,omitempty"`
	// Process-only progress cursor survives preview eviction, never JSON or restart.
	progressRevision  uint64
	progressState     string
	progressTruncated bool
	Revision          uint64            `json:"revision,omitempty"`
	TerminationReason TerminationReason `json:"termination_reason,omitempty"`
	Approvals         []ApprovalRecord  `json:"approvals,omitempty"`
	OwnerID           string            `json:"owner_id"`
	ClientID          string            `json:"client_id"`
	InstallationID    string            `json:"installation_id"`
	RequestID         string            `json:"request_id"`
	InputDigest       string            `json:"input_digest"`
	State             string            `json:"state"`
	CreatedAt         time.Time         `json:"created_at"`
	Deadline          time.Time         `json:"deadline"`
	GeneratedAt       *time.Time        `json:"generated_at,omitempty"`
	ExpiresAt         *time.Time        `json:"expires_at,omitempty"`
	ResultDigest      string            `json:"result_digest,omitempty"`
}

func Digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func UUID(value string) bool   { return uuidPattern.MatchString(value) }
func Decode(raw []byte, digest string) (Envelope, error) {
	var e Envelope
	if len(raw) > ContextBytes || !utf8.Valid(raw) || !digestPattern.MatchString(digest) || Digest(raw) != digest {
		return e, errors.New("invalid workbench context digest or size")
	}
	if err := appstream.DecodeStrict(raw, &e); err != nil {
		return e, errors.New("invalid workbench context fields")
	}
	// json.Valid rejects a trailing second value. Decode alone does not.
	if !json.Valid(raw) || e.SchemaVersion != 2 || !identityPattern.MatchString(e.OwnerID) || !identityPattern.MatchString(e.ClientID) || !identityPattern.MatchString(e.DeploymentID) || !UUID(e.InstallationID) || !UUID(e.RequestID) || !UUID(e.ConversationID) || !UUID(e.TaskID) || len(e.Messages) == 0 || len(e.Messages) > ContextMessages {
		return e, errors.New("invalid workbench execution identity or context")
	}
	if e.ClientPresentation != nil && !e.ClientPresentation.Valid() {
		return e, errors.New("invalid client presentation capability")
	}
	if len(e.InputFiles) > app.WorkbenchResultFiles {
		return e, errors.New("too many workbench input files")
	}
	fileNames := map[string]bool{}
	currentFiles := map[string]File{}
	for _, file := range e.InputFiles {
		if !UUID(file.ID) || file.Size < 0 || file.Size > ResultBytes || !digestPattern.MatchString(file.SHA256) || !validName(file.Name) || file.Name == "conversation-files" || fileNames[file.Name] || currentFiles[file.ID].ID != "" {
			return e, errors.New("workbench input filenames must be valid and unique")
		}
		fileNames[file.Name] = true
		currentFiles[file.ID] = file
	}
	handleIDs := map[string]bool{}
	messageIDs := map[string]bool{}
	for _, m := range e.Messages {
		if !identityPattern.MatchString(m.ID) || messageIDs[m.ID] {
			return e, errors.New("invalid or duplicate workbench message identity")
		}
		messageIDs[m.ID] = true
		if m.Clarification != nil && m.Role != "assistant" {
			return e, errors.New("clarification must be attached to an assistant message")
		}
		if len(m.Files) > app.WorkbenchResultFiles {
			return e, errors.New("too many message file handles")
		}
		for _, file := range m.Files {
			if !UUID(file.ID) || !validName(file.Name) || file.Size < 0 || file.Size > ResultBytes || !digestPattern.MatchString(file.SHA256) || handleIDs[file.ID] {
				return e, errors.New("invalid conversation file handle")
			}
			if current, exists := currentFiles[file.ID]; exists && current != file {
				return e, errors.New("conflicting current and historical file identity")
			}
			handleIDs[file.ID] = true
		}
		if len(handleIDs) > 32 {
			return e, errors.New("too many conversation file handles")
		}
		if (m.Role != "user" && m.Role != "assistant") || len(m.Content) > InputBytes || strings.ContainsRune(m.Content, 0) {
			return e, errors.New("invalid workbench context message")
		}
	}
	if e.Messages[len(e.Messages)-1].Role != "user" || strings.TrimSpace(e.Messages[len(e.Messages)-1].Content) == "" {
		return e, errors.New("workbench execution needs a final user input")
	}
	return e, nil
}
