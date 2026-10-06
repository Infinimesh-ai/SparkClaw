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
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Envelope struct {
	SchemaVersion  int       `json:"schema_version"`
	DeploymentID   string    `json:"deployment_id"`
	OwnerID        string    `json:"owner_id"`
	ClientID       string    `json:"client_id"`
	InstallationID string    `json:"installation_id"`
	ConversationID string    `json:"local_conversation_id"`
	TaskID         string    `json:"local_task_id"`
	RequestID      string    `json:"request_id"`
	Messages       []Message `json:"messages"`
	InputFiles     []File    `json:"input_files,omitempty"`
}
type File struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}
type Payload struct {
	Content string `json:"content"`
	Files   []File `json:"files"`
}
type Output struct {
	Content string
	Files   map[string][]byte
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

type Status struct {
	ExecutionExpiresAt *time.Time        `json:"execution_expires_at,omitempty"`
	PendingApprovals   []PendingApproval `json:"pending_approvals,omitempty"`
	SchemaVersion      int               `json:"schema_version"`
	RequestID          string            `json:"request_id"`
	InputDigest        string            `json:"input_digest"`
	State              string            `json:"state"`
	ExpiresAt          *time.Time        `json:"expires_at,omitempty"`
	Result             *Result           `json:"result,omitempty"`
}

// Fence is the complete durable control field allowlist. No maps, arbitrary
// diagnostic strings, context, titles, prompts or tool outputs may be added.
type Fence struct {
	OwnerID        string     `json:"owner_id"`
	ClientID       string     `json:"client_id"`
	InstallationID string     `json:"installation_id"`
	RequestID      string     `json:"request_id"`
	InputDigest    string     `json:"input_digest"`
	State          string     `json:"state"`
	CreatedAt      time.Time  `json:"created_at"`
	Deadline       time.Time  `json:"deadline"`
	GeneratedAt    *time.Time `json:"generated_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	ResultDigest   string     `json:"result_digest,omitempty"`
}

func Digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func UUID(value string) bool   { return uuidPattern.MatchString(value) }
func Decode(raw []byte, digest string) (Envelope, error) {
	var e Envelope
	if len(raw) > ContextBytes || !utf8.Valid(raw) || !digestPattern.MatchString(digest) || Digest(raw) != digest {
		return e, errors.New("invalid workbench context digest or size")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&e); err != nil {
		return e, errors.New("invalid workbench context fields")
	}
	// json.Valid rejects a trailing second value. Decode alone does not.
	if !json.Valid(raw) || e.SchemaVersion != 1 || !identityPattern.MatchString(e.OwnerID) || !identityPattern.MatchString(e.ClientID) || !identityPattern.MatchString(e.DeploymentID) || !UUID(e.InstallationID) || !UUID(e.RequestID) || !UUID(e.ConversationID) || !UUID(e.TaskID) || len(e.Messages) == 0 || len(e.Messages) > ContextMessages {
		return e, errors.New("invalid workbench execution identity or context")
	}
	fileNames := map[string]bool{}
	for _, file := range e.InputFiles {
		if !validName(file.Name) || fileNames[file.Name] {
			return e, errors.New("workbench input filenames must be valid and unique")
		}
		fileNames[file.Name] = true
	}
	for _, m := range e.Messages {
		if (m.Role != "user" && m.Role != "assistant") || len(m.Content) > InputBytes || strings.ContainsRune(m.Content, 0) {
			return e, errors.New("invalid workbench context message")
		}
	}
	if e.Messages[len(e.Messages)-1].Role != "user" || strings.TrimSpace(e.Messages[len(e.Messages)-1].Content) == "" {
		return e, errors.New("workbench execution needs a final user input")
	}
	return e, nil
}
