package agent

import (
	"context"
	"fmt"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

const (
	maxEvidenceFitAttempts = 4
	minFittedEvidenceBytes = 512
	// evidenceFitReserveTokens covers request text appended after fitting,
	// such as the structured final-answer format override.
	evidenceFitReserveTokens = 256
)

// evidencePromptRender renders one prompt whose evidence is bounded by budget
// bytes.
type evidencePromptRender func(budget int) (system, user string, err error)

// fitEvidencePrompt renders a single-shot prompt at the byte budget derived
// from model capacity and confirms it with the lane's tokenizer. Byte budgets
// assume about three bytes per token; dense content such as numeric sheets
// can cost more, so an over-window prompt is re-rendered at a budget scaled
// by the measured overflow. It returns the admitted prompt and its budget.
// Prompts admitted by ContextBuilder degrade their own sections instead.
func (r Runtime) fitEvidencePrompt(ctx context.Context, operation modelcapacity.Operation, lane string, budget int, render evidencePromptRender) (string, string, int, error) {
	profile, err := r.models.Profile(lane)
	if err != nil {
		return "", "", 0, err
	}
	spec, err := modelcapacity.Spec(operation)
	if err != nil {
		return "", "", 0, err
	}
	inputBudget := profile.InputTokens(spec.OutputClass) - evidenceFitReserveTokens
	if inputBudget <= 0 {
		return "", "", 0, fmt.Errorf("model operation %q has no input budget on lane %q", operation, lane)
	}
	for attempt := 1; ; attempt++ {
		system, user, err := render(budget)
		if err != nil {
			return "", "", 0, err
		}
		tokens, err := r.models.CountProfileChatInput(ctx, operation, lane, system, user, modelrouter.ChatOptions{})
		if err != nil {
			return "", "", 0, err
		}
		if tokens <= inputBudget {
			return system, user, budget, nil
		}
		if attempt >= maxEvidenceFitAttempts || budget <= minFittedEvidenceBytes {
			return "", "", 0, &modelrouter.InputTooLongError{
				Operation: operation, Lane: modelcapacity.Lane(lane), InputTokens: tokens, InputBudget: inputBudget, Exact: true,
			}
		}
		scaled := int(int64(budget) * int64(inputBudget) * 9 / (int64(tokens) * 10))
		budget = max(minFittedEvidenceBytes, min(scaled, budget-1))
	}
}
