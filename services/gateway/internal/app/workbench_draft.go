package app

import "time"

// WorkbenchDraft belongs to one authenticated owner's local workbench scope.
// Revision zero means no saved draft. Saving even an empty draft advances the
// revision, so a stale tab cannot resurrect text after a send or explicit clear.
type WorkbenchDraft struct {
	Content       string    `json:"content"`
	AttachmentIDs []string  `json:"attachment_ids"`
	Revision      int64     `json:"revision"`
	UpdatedAt     time.Time `json:"updated_at"`
}
