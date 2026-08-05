package app

import "errors"

// ToolErrorCode classifies a failed tool call so consumers can branch on the
// failure's semantics instead of matching error prose, which may change or be
// rewritten by redaction. An empty code means the producer did not classify
// the failure (external adapter errors, records persisted before the field
// existed); consumers may then fall back to documented prose matching.
type ToolErrorCode string

const (
	// ToolErrorUnsafeClickTarget: the click target was rejected by the
	// bounded browser.interaction contract (consequential action label).
	ToolErrorUnsafeClickTarget ToolErrorCode = "unsafe_click_target"
	// ToolErrorSnapshotStale: the referenced browser snapshot no longer
	// binds to live page state; the caller must take a fresh snapshot.
	ToolErrorSnapshotStale ToolErrorCode = "snapshot_stale"
	// ToolErrorDocumentOperationTimeout: a bounded document operation
	// exhausted its end-to-end execution deadline.
	ToolErrorDocumentOperationTimeout ToolErrorCode = "document_operation_timeout"
)

// CodedToolError attaches a ToolErrorCode to a tool failure. The message is
// unchanged from the wrapped error so user-facing output and persisted
// records keep their existing prose.
type CodedToolError struct {
	Code ToolErrorCode
	Err  error
}

func (e *CodedToolError) Error() string { return e.Err.Error() }
func (e *CodedToolError) Unwrap() error { return e.Err }

// ToolErrorCodeFrom extracts the classification from an error chain, or ""
// when the failure was not classified.
func ToolErrorCodeFrom(err error) ToolErrorCode {
	var coded *CodedToolError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}
