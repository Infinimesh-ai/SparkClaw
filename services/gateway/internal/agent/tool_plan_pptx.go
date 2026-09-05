package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// sealPPTXMutationPlan binds a PPTX mutation plan to its sealed candidate
// before approval or execution. Plans for other tools pass through unchanged.
// When candidate preparation is refused the blocked call is persisted and
// stop is true so runToolPlan returns it as the tool outcome; a failure to
// load the visual-QA warning returns an empty call with the error.
func (r Runtime) sealPPTXMutationPlan(ctx context.Context, sessionID, runID string, plan toolPlan, call app.ToolCall, sealedBinding toolhub.PPTXSealedCandidateBinding, hasSealedBinding bool) (toolPlan, app.ToolCall, string, bool, error) {
	if !r.tools.IsPPTXMutationTool(plan.Name, plan.Args) {
		return plan, call, "", false, nil
	}
	if !hasSealedBinding {
		var err error
		sealedBinding, err = r.tools.PreparePPTXCandidate(ctx, plan.Name, plan.Args, sessionID, runID)
		if err != nil {
			call.Status = app.ToolCallStatusBlocked
			call.Error = err.Error()
			call.ErrorCode = string(app.ToolErrorCodeFrom(err))
			done := time.Now().UTC()
			call.CompletedAt = &done
			call.ObservationSummary = adaptToolResult(toolResultAdapterInput{Call: call, Err: err, MaxBytes: r.tools.Config().Runtime.ObservationSummaryMaxBytes})
			if _, saveErr := r.saveToolCall(ctx, call); saveErr != nil {
				return plan, call, "", true, fmt.Errorf("persist failed PPTX candidate preparation: %w", saveErr)
			}
			return plan, call, "", true, nil
		}
	}
	plan.Args = toolhub.AttachPPTXSealedCandidate(plan.Args, sealedBinding)
	call.Arguments = plan.Args
	pptxVisualWarning, err := r.tools.PPTXSealedCandidateWarningSummary(ctx, plan.Args)
	if err != nil {
		return plan, app.ToolCall{}, "", false, fmt.Errorf("load sealed PPTX visual warning: %w", err)
	}
	return plan, call, pptxVisualWarning, false, nil
}
