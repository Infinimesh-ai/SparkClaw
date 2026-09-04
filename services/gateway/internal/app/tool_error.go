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
	// ToolErrorBrowserInteractionLoop: the same semantic browser control was
	// selected again after Runtime had already validated its state transition.
	ToolErrorBrowserInteractionLoop ToolErrorCode = "browser_interaction_loop"
	// ToolErrorDocumentOperationTimeout: a bounded document operation
	// exhausted its end-to-end execution deadline.
	ToolErrorDocumentOperationTimeout ToolErrorCode = "document_operation_timeout"
	// ToolErrorPPTXLayoutFitConflict: a PPTX replacement is valid but cannot
	// fit the current slide geometry without overlap or unreadable overflow.
	ToolErrorPPTXLayoutFitConflict           ToolErrorCode = "pptx_layout_fit_conflict"
	ToolErrorPPTXRenderInvalidInput          ToolErrorCode = "pptx_render_invalid_input"
	ToolErrorPPTXRenderBackendUnavailable    ToolErrorCode = "pptx_render_backend_unavailable"
	ToolErrorPPTXRenderTimeout               ToolErrorCode = "pptx_render_timeout"
	ToolErrorPPTXRenderInvalidPDF            ToolErrorCode = "pptx_render_invalid_pdf"
	ToolErrorPPTXRenderPageMismatch          ToolErrorCode = "pptx_render_page_mismatch"
	ToolErrorPPTXRenderInvalidImage          ToolErrorCode = "pptx_render_invalid_image"
	ToolErrorPPTXRenderDiagnosticInvalid     ToolErrorCode = "pptx_render_diagnostic_invalid"
	ToolErrorPPTXRenderDiagnosticUnavailable ToolErrorCode = "pptx_render_diagnostic_unavailable"
	ToolErrorPPTXRenderProfileNotReady       ToolErrorCode = "pptx_render_profile_not_ready"
	ToolErrorPPTXRenderModelUnavailable      ToolErrorCode = "pptx_render_model_unavailable"
	ToolErrorPPTXRenderModelInvalid          ToolErrorCode = "pptx_render_model_invalid"
	ToolErrorPPTXRenderRepairInvalid         ToolErrorCode = "pptx_render_repair_invalid"
	ToolErrorPPTXRenderRepairExhausted       ToolErrorCode = "pptx_render_repair_exhausted"
	ToolErrorPPTXRenderVisualBlocked         ToolErrorCode = "pptx_render_visual_blocked"
	ToolErrorPPTXRenderPreservationViolation ToolErrorCode = "pptx_render_preservation_violation"
	ToolErrorPPTXRenderSourceStale           ToolErrorCode = "pptx_render_source_stale"
	ToolErrorPPTXRenderCancelled             ToolErrorCode = "pptx_render_cancelled"
	ToolErrorPublicTargetNotFound            ToolErrorCode = "public_target_not_found"
	ToolErrorPublicTargetUnsafe              ToolErrorCode = "public_target_unsafe"
	ToolErrorPublicTargetProviderUnavailable ToolErrorCode = "public_target_provider_unavailable"
	ToolErrorBrowserSessionRequired          ToolErrorCode = "browser_session_required"
	ToolErrorDraftActionStale                ToolErrorCode = "draft_action_stale"
	ToolErrorDraftForbiddenControl           ToolErrorCode = "draft_forbidden_control"
	ToolErrorVisualEvidenceStale             ToolErrorCode = "visual_evidence_stale"
	ToolErrorMCPTool                         ToolErrorCode = "mcp_tool_error"
	ToolErrorMCPTemporarilyUnavailable       ToolErrorCode = "mcp_temporarily_unavailable"
	ToolErrorMCPTokenReissueRequired         ToolErrorCode = "mcp_token_reissue_required"
	ToolErrorMCPTokenFileMismatch            ToolErrorCode = "mcp_token_file_mismatch"
	ToolErrorMCPToolResult                   ToolErrorCode = ToolErrorMCPTool
	ToolErrorMCPAuthorization                ToolErrorCode = "mcp_authorization"
	ToolErrorMCPPersistenceUnsafe            ToolErrorCode = "mcp_persistence_unsafe"
	ToolErrorPolicyBlocked                   ToolErrorCode = "policy_blocked"
	ToolErrorInfoNotConfigured               ToolErrorCode = "info_not_configured"
	ToolErrorInfoUpdating                    ToolErrorCode = "info_updating"
	ToolErrorInfoAuthFailed                  ToolErrorCode = "info_auth_failed"
	ToolErrorInfoTemporarilyUnavailable      ToolErrorCode = "info_temporarily_unavailable"
	ToolErrorInfoCredentialsChanged          ToolErrorCode = "info_credentials_changed"
	ToolErrorLocalMindUpdating               ToolErrorCode = "localmind_updating"
	ToolErrorLocalMindCredentialsChanged     ToolErrorCode = "localmind_credentials_changed"
	ToolErrorEmailNotConfigured              ToolErrorCode = "email_not_configured"
	ToolErrorEmailLoginRequired              ToolErrorCode = "email_login_required"
	ToolErrorEmailAccountAmbiguous           ToolErrorCode = "email_account_ambiguous"
	ToolErrorEmailProviderUnavailable        ToolErrorCode = "email_provider_unavailable"
	ToolErrorEmailPageContractChanged        ToolErrorCode = "email_page_contract_changed"
	ToolErrorEmailInvalidInput               ToolErrorCode = "email_invalid_input"
	ToolErrorEmailDraftConflict              ToolErrorCode = "email_draft_conflict"
	ToolErrorEmailDraftVerificationFailed    ToolErrorCode = "email_draft_verification_failed"
	ToolErrorEmailSendControlUnverified      ToolErrorCode = "email_send_control_unverified"
	ToolErrorEmailSendOutcomeUnknown         ToolErrorCode = "email_send_outcome_unknown"
	ToolErrorEmailScriptTimeout              ToolErrorCode = "email_script_timeout"
	ToolErrorEmailScriptInvalidOutput        ToolErrorCode = "email_script_invalid_output"
	ToolErrorEmailAdmissionStale             ToolErrorCode = "email_admission_stale"
	// ToolErrorObservationBinaryContent: an observation.read window contains
	// bytes that are not valid UTF-8. The error message carries the offset to
	// retry with so the caller can skip past the binary region.
	ToolErrorObservationBinaryContent ToolErrorCode = "observation_binary_content"
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
