package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

func TestFitEvidencePromptShrinksDenseEvidenceToTheInputWindow(t *testing.T) {
	runtime, _, _, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	// Twice the derived budget of one-token-per-two-bytes text overflows the
	// window, as numeric sheets do on real tokenizers.
	initial := 2 * runtime.workflowFinalEvidenceLimit(documentWorkflowModelLane)
	renders := 0
	_, user, budget, err := runtime.fitEvidencePrompt(context.Background(), modelcapacity.OperationWorkflowFinalAnswer, documentWorkflowModelLane, initial,
		func(budget int) (string, string, error) {
			renders++
			return "system", strings.Repeat("7 ", budget/2), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if renders < 2 || budget >= initial || len(user) > budget {
		t.Fatalf("dense evidence was not refitted: renders=%d budget=%d initial=%d user=%d", renders, budget, initial, len(user))
	}
	tokens, err := runtime.models.CountProfileChatInput(context.Background(), modelcapacity.OperationWorkflowFinalAnswer, documentWorkflowModelLane, "system", user, modelrouter.ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := runtime.models.Profile(documentWorkflowModelLane)
	if tokens > profile.InputTokens(modelcapacity.OutputAnswer) {
		t.Fatalf("fitted prompt still exceeds the input window: %d tokens", tokens)
	}
}

func TestFitEvidencePromptFailsClosedWhenFixedTextOverflows(t *testing.T) {
	runtime, _, _, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	fixed := strings.Repeat("x ", 200000)
	_, _, _, err := runtime.fitEvidencePrompt(context.Background(), modelcapacity.OperationWorkflowFinalAnswer, documentWorkflowModelLane, 4096,
		func(int) (string, string, error) { return "system", fixed, nil })
	var tooLong *modelrouter.InputTooLongError
	if !errors.As(err, &tooLong) {
		t.Fatalf("oversized fixed prompt was not rejected as input too long: %v", err)
	}
}

func TestWorkflowFinalEvidenceCarriesDocumentsUpToTheModelBudget(t *testing.T) {
	runtime, st, session, closeRuntime := newObservationManagementRuntime(t)
	defer closeRuntime()
	limit := runtime.workflowFinalEvidenceLimit(documentWorkflowModelLane)
	if limit <= defaultWorkflowFinalEvidenceMaxBytes {
		t.Fatalf("document finalizer budget %d did not grow past the fixed 8000-byte default", limit)
	}
	for _, test := range []struct {
		name      string
		content   string
		truncated bool
	}{
		{name: "fits", content: strings.Repeat("第三章讲述传输层协议。", 1200)},
		{name: "exceeds", content: strings.Repeat("第三章讲述传输层协议。", limit/20), truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := pdfCoverageToolCall("complete", test.content, true)
			run, call := archivedEvidenceFixture(t, runtime, st, session.ID, fixture.Tool, fixture.Result)
			call.Capability = app.ToolCapabilityDocumentRead
			testSaveToolCall(st, call)
			run.Workflow = &app.WorkflowState{Plan: app.WorkflowPlan{ProfileID: app.WorkflowDocumentRead}}
			testSaveRun(st, run)

			projection, err := runtime.workflowFinalEvidence(context.Background(), run, []app.ToolCall{call}, nil)
			if err != nil {
				t.Fatal(err)
			}
			payload := projection.modelPayload()
			if got := strings.Contains(payload, "model_evidence_truncated=true"); got != test.truncated {
				t.Fatalf("model_evidence_truncated=%t, want %t (content %d bytes, budget %d)", got, test.truncated, len(test.content), limit)
			}
			if !test.truncated && !strings.Contains(payload, test.content) {
				t.Fatal("a document inside the model budget did not reach the finalizer whole")
			}
		})
	}
}
