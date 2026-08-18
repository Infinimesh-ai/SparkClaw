package agent

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type workflowFailureCode string

const (
	workflowFailureRequiredToolNotCalled       workflowFailureCode = "required_tool_not_called"
	workflowFailureEvidenceUnavailable         workflowFailureCode = "required_evidence_unavailable"
	workflowFailureToolOutsideActiveScope      workflowFailureCode = "tool_outside_active_scope"
	workflowFailureSemanticPreflight           workflowFailureCode = "semantic_preflight_failed"
	workflowFailureSemanticOutputInvalid       workflowFailureCode = "semantic_output_invalid"
	workflowFailurePromptFixedOversized        workflowFailureCode = "workflow_prompt_fixed_sections_oversized"
	workflowFailureObservationReadLimit        workflowFailureCode = "observation_read_limit_exceeded"
	workflowFailureDirectToolInvocationInvalid workflowFailureCode = "direct_tool_invocation_invalid"
	workflowFailureSetup                       workflowFailureCode = "workflow_setup_failed"
	workflowFailureModelUnavailable            workflowFailureCode = "workflow_model_unavailable"
	workflowFailureStateInvalid                workflowFailureCode = "workflow_state_invalid"
	workflowFailureOutcomeInvalid              workflowFailureCode = "workflow_outcome_invalid"
	workflowFailureTransitionFailed            workflowFailureCode = "workflow_transition_failed"
	workflowFailureFinalizationFailed          workflowFailureCode = "workflow_finalization_failed"
	workflowFailureMessageContentInvalid       workflowFailureCode = "message_content_invalid"
	workflowFailureConversationOutputInvalid   workflowFailureCode = "conversation_output_invalid"
)

func (result *workflowExecutionResult) fail(code workflowFailureCode, diagnostic error) {
	result.FailureCode = code
	result.FailureDiagnostic = workflowFailureDiagnostic(diagnostic)
	result.FinalAnswer = ""
	result.Completed = false
}

func (result workflowExecutionResult) withPublicFailureProjection() workflowExecutionResult {
	if result.FailureCode == "" {
		return result
	}
	result.FinalAnswer = publicWorkflowFailureMessage(result.FailureCode)
	result.FinalAnswerStreamed = false
	result.Completed = false
	return result
}

func workflowFailureDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func publicWorkflowFailureMessage(code workflowFailureCode) string {
	switch code {
	case workflowFailureRequiredToolNotCalled:
		return "The workflow is blocked: required tool not called."
	case workflowFailureEvidenceUnavailable:
		return "The workflow could not continue because its required evidence is unavailable."
	case workflowFailureToolOutsideActiveScope:
		return "The workflow was blocked because a requested tool is outside the active capability scope."
	case workflowFailureSemanticPreflight:
		return "The workflow could not safely prepare the requested action."
	case workflowFailureSemanticOutputInvalid:
		return "The workflow was blocked because the generated action did not satisfy its semantic contract."
	case workflowFailurePromptFixedOversized:
		return "The workflow could not call the model because its required prompt sections exceed the context limit."
	case workflowFailureObservationReadLimit:
		return "The workflow was blocked after exceeding the stage observation-read limit."
	case workflowFailureDirectToolInvocationInvalid:
		return "The workflow could not invoke its bound tool because the active direct-tool contract is invalid."
	case workflowFailureModelUnavailable:
		return "The workflow model is currently unavailable."
	case workflowFailureStateInvalid:
		return "The workflow could not continue because its persisted state is unavailable or invalid."
	case workflowFailureOutcomeInvalid:
		return "The workflow could not safely interpret the tool result."
	case workflowFailureTransitionFailed:
		return "The workflow could not safely apply the tool result to its persisted state."
	case workflowFailureFinalizationFailed:
		return "The completed workflow result could not be rendered safely."
	case workflowFailureMessageContentInvalid:
		return "The message could not be prepared for delivery."
	case workflowFailureConversationOutputInvalid:
		return "The conversation model returned no usable answer."
	case workflowFailureSetup:
		return "The matched workflow could not be prepared safely."
	default:
		return "The workflow could not continue safely."
	}
}

func (r Runtime) auditWorkflowExecutionFailure(sessionID, runID, eventType string, code workflowFailureCode, diagnostic string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["reason_code"] = code
	if strings.TrimSpace(diagnostic) != "" {
		fields["diagnostic"] = diagnostic
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     runID,
		Actor:     "runtime",
		Type:      eventType,
		Summary:   "Workflow execution failed inside its runtime boundary",
		Fields:    fields,
	})
}
