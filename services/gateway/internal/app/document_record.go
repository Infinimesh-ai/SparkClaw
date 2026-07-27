package app

import "time"

const (
	DocumentStatusAvailable = "available"

	DocumentSourceAttachment = "attachment"
	DocumentSourceWorkspace  = "workspace"
	DocumentSourceToolOutput = "tool_output"

	DocumentActivityAttached  = "attached"
	DocumentActivityConfirmed = "confirmed"
	DocumentActivityRead      = "read"
	DocumentActivityEdited    = "edited"
)

// DocumentRecord is the durable identity and provenance record for one
// governed document. Parsed content is intentionally stored elsewhere and may
// be replaced or regenerated without changing this record's identity.
type DocumentRecord struct {
	ID               string    `json:"id"`
	OwnerID          string    `json:"owner_id"`
	SessionID        string    `json:"session_id"`
	GovernedPath     string    `json:"governed_path"`
	Name             string    `json:"name"`
	ContentType      string    `json:"content_type,omitempty"`
	Format           string    `json:"format,omitempty"`
	SizeBytes        int64     `json:"size_bytes,omitempty"`
	SHA256           string    `json:"sha256,omitempty"`
	Status           string    `json:"status"`
	Source           string    `json:"source"`
	SourceMessageID  string    `json:"source_message_id,omitempty"`
	SourceRunID      string    `json:"source_run_id,omitempty"`
	SourceToolCallID string    `json:"source_tool_call_id,omitempty"`
	ParentDocumentID string    `json:"parent_document_id,omitempty"`
	LastActivity     string    `json:"last_activity"`
	LastActivityID   string    `json:"last_activity_id"`
	LastActivityAt   time.Time `json:"last_activity_at"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
