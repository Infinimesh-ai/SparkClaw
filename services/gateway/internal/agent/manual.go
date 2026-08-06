package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// ManualInvocation is the outcome of an owner-initiated tool call. Exactly one
// of the terminal shapes holds: Result is set when the tool executed, and
// Approval is set when the call was parked awaiting owner approval (for
// notify.ask_approval both are set — the approval request is the result).
type ManualInvocation struct {
	Call     app.ToolCall
	Approval *app.Approval
	Result   any
}

// ErrManualToolNotFound reports that the requested tool has no definition.
var ErrManualToolNotFound = errors.New("tool not found")

// ManualArgumentError reports that the arguments failed schema validation.
type ManualArgumentError struct{ Err error }

func (e ManualArgumentError) Error() string { return e.Err.Error() }
func (e ManualArgumentError) Unwrap() error { return e.Err }

// ManualInvocationDenied reports a policy denial. The blocked call and run
// have already been persisted.
type ManualInvocationDenied struct{ Reason string }

func (e ManualInvocationDenied) Error() string { return e.Reason }

// ManualExecutionError reports a tool execution failure. The failed call and
// run have already been persisted.
type ManualExecutionError struct{ Err error }

func (e ManualExecutionError) Error() string { return e.Err.Error() }
func (e ManualExecutionError) Unwrap() error { return e.Err }

// InvokeToolManually runs one tool outside an agent run on behalf of the
// owner: policy evaluation, approval parking, execution, observation
// compression/archival, and run/tool-call persistence — the same flow the
// agent loop applies, so the manual HTTP path cannot drift from it.
func (r Runtime) InvokeToolManually(ctx context.Context, name string, args map[string]any, sessionID string) (ManualInvocation, error) {
	if args == nil {
		args = map[string]any{}
	}
	def, ok := r.tools.Definition(name)
	if !ok {
		return ManualInvocation{}, fmt.Errorf("%w: %q", ErrManualToolNotFound, name)
	}
	if err := r.tools.Validate(name, args); err != nil {
		return ManualInvocation{}, ManualArgumentError{Err: err}
	}
	runID := app.NewID("manual")
	now := time.Now().UTC()
	call := app.ToolCall{
		ID:        app.NewID("tc"),
		SessionID: sessionID,
		RunID:     runID,
		Tool:      name,
		Risk:      def.Risk,
		Status:    "started",
		Arguments: args,
		StartedAt: now,
	}
	decision := r.policy.Decide(def, args)
	if !decision.Allowed {
		done := time.Now().UTC()
		r.store.SaveRun(app.AgentRun{
			ID:          runID,
			SessionID:   sessionID,
			State:       "failed",
			Risk:        def.Risk,
			StartedAt:   now,
			CompletedAt: &done,
			Summary:     decision.Reason,
		})
		call.Status = "blocked"
		call.Error = decision.Reason
		call.CompletedAt = &done
		r.store.SaveToolCall(call)
		return ManualInvocation{Call: call}, ManualInvocationDenied{Reason: decision.Reason}
	}
	if name == "notify.ask_approval" {
		summary, _ := args["summary"].(string)
		reason, _ := args["reason"].(string)
		if reason == "" {
			reason = "Manual confirmation requested."
		}
		r.store.SaveRun(app.AgentRun{
			ID:        runID,
			SessionID: sessionID,
			State:     "approval_pending",
			Risk:      def.Risk,
			StartedAt: now,
			Summary:   summary,
		})
		approval := app.Approval{
			ID:         app.NewID("ap"),
			Source:     app.ApprovalSourceTool,
			SessionID:  sessionID,
			RunID:      runID,
			ToolCallID: call.ID,
			Tool:       name,
			Risk:       def.Risk,
			Status:     "pending",
			Summary:    summary,
			Reason:     reason,
			Resources:  []string{},
			Arguments:  args,
			CreatedAt:  time.Now().UTC(),
		}
		call.Status = "approval_pending"
		call.ApprovalID = approval.ID
		r.store.SaveToolCall(call)
		r.store.SaveApproval(approval)
		return ManualInvocation{
			Call:     call,
			Approval: &approval,
			Result: map[string]any{
				"status":      "approval_requested",
				"approval_id": approval.ID,
				"tool_call":   call.ID,
			},
		}, nil
	}
	if decision.RequiresApproval {
		r.store.SaveRun(app.AgentRun{
			ID:        runID,
			SessionID: sessionID,
			State:     "approval_pending",
			Risk:      def.Risk,
			StartedAt: now,
			Summary:   "Manual tool invocation requires approval: " + name,
		})
		approvalArgs := args
		if verifier, ok := policy.VerifierDecision(def, decision, time.Now().UTC()); ok {
			approvalArgs = policy.AttachVerifier(args, verifier)
			call.Arguments = approvalArgs
			r.store.AddAudit(app.AuditEvent{
				SessionID: sessionID,
				RunID:     runID,
				Actor:     "verifier",
				Type:      "verifier.deep_check",
				Summary:   "Deep verifier queued owner confirmation for " + name,
				Fields: map[string]any{
					"tool":          name,
					"risk":          def.Risk,
					"verdict":       "ask_user",
					"requires_deep": decision.RequiresDeep,
					"manual":        true,
				},
			})
		}
		approval := app.Approval{
			ID:         app.NewID("ap"),
			Source:     app.ApprovalSourceTool,
			SessionID:  sessionID,
			RunID:      runID,
			ToolCallID: call.ID,
			Tool:       name,
			Risk:       def.Risk,
			Status:     "pending",
			Summary:    "Manual tool invocation requires approval: " + name,
			Reason:     decision.Reason,
			Resources:  decision.Resources,
			Arguments:  approvalArgs,
			CreatedAt:  time.Now().UTC(),
		}
		call.Status = "approval_pending"
		call.ApprovalID = approval.ID
		r.store.SaveToolCall(call)
		r.store.SaveApproval(approval)
		return ManualInvocation{Call: call, Approval: &approval}, nil
	}
	output, err := r.tools.Execute(ctx, name, args, sessionID, runID)
	done := time.Now().UTC()
	call.CompletedAt = &done
	if err != nil {
		r.store.SaveRun(app.AgentRun{
			ID:          runID,
			SessionID:   sessionID,
			State:       "failed",
			Risk:        def.Risk,
			StartedAt:   now,
			CompletedAt: &done,
			Summary:     err.Error(),
		})
		call.Status = "failed"
		call.Error = err.Error()
		call.ErrorCode = string(app.ToolErrorCodeFrom(err))
		if output.Output != nil {
			call.Result = output.Output
			call.ObservationRef = store.ArchiveToolObservation(ctx, r.store, r.artifacts, call, output.Output)
			call.ObservationSummary = adaptToolResult(toolResultAdapterInput{Call: call, Output: output.Output, ObservationRef: call.ObservationRef, Err: err, MaxBytes: r.observationSummaryLimit()})
		}
		r.store.SaveToolCall(call)
		return ManualInvocation{Call: call}, ManualExecutionError{Err: err}
	}
	call.Status = "completed"
	call.Result = output.Output
	call.ObservationSummary = CompressObservation(name, output.Output, r.observationSummaryLimit())
	call.ObservationRef = store.ArchiveToolObservation(ctx, r.store, r.artifacts, call, output.Output)
	r.store.SaveToolCall(call)
	r.store.SaveRun(app.AgentRun{
		ID:          runID,
		SessionID:   sessionID,
		State:       "completed",
		Risk:        def.Risk,
		StartedAt:   now,
		CompletedAt: &done,
		Summary:     call.ObservationSummary,
	})
	return ManualInvocation{Call: call, Result: output.Output}, nil
}
