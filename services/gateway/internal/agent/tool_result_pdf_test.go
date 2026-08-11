package agent

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestPDFWorkspaceReadOutcomePreservesPartialCoverage(t *testing.T) {
	call := pdfCoverageToolCall("partial", "covered page", false)
	outcome := adaptWorkspaceReadOutcome(call, "document_read")
	if !containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable) || len(outcome.Refs) != 1 {
		t.Fatalf("partial PDF evidence should remain usable: %#v", outcome)
	}
	attributes := outcome.Refs[0].Attributes
	if attributes["read_complete"] != "false" || attributes["coverage_status"] != "partial" || attributes["missing_page_indexes"] != "[2]" || !strings.Contains(attributes["page_status_counts"], "ocr_failed") {
		t.Fatalf("partial PDF coverage attributes are incomplete: %#v", attributes)
	}
}

func TestPDFWorkspaceReadOutcomeBlocksUnavailableCoverage(t *testing.T) {
	call := pdfCoverageToolCall("unavailable", "", false)
	outcome := adaptWorkspaceReadOutcome(call, "document_read")
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable) {
		t.Fatalf("unavailable PDF evidence produced a content signal: %#v", outcome)
	}
	assessment := (documentReadProfile{}).Assess(nil, outcome)
	if assessment.Status != app.AssessmentBlocked || assessment.ReasonCode != "document_read_failed" {
		t.Fatalf("unavailable PDF evidence did not block deterministically: %#v", assessment)
	}
}

func TestPDFToolResultAndFinalEvidenceProjectCoverage(t *testing.T) {
	call := pdfCoverageToolCall("partial", "covered page", false)
	message := adaptToolResult(toolResultAdapterInput{Call: call, MaxBytes: 5000})
	var decoded toolResultMessage
	if err := json.Unmarshal([]byte(message), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Category != "file" || decoded.Structured["read_complete"] != false || decoded.Structured["coverage_status"] != "partial" {
		t.Fatalf("PDF read used mutation semantics or lost coverage: %#v", decoded)
	}
	evidence := workflowFinalEvidence([]app.ToolCall{call}, nil)
	if len(evidence) != 1 || !strings.Contains(evidence[0], "read_complete=false") ||
		!strings.Contains(evidence[0], "claim_coverage=partial") || !strings.Contains(evidence[0], "limitation_required=true") ||
		!strings.Contains(evidence[0], "missing_page_indexes=[2]") || !strings.Contains(evidence[0], "ocr_failed:1") {
		t.Fatalf("PDF finalization manifest is incomplete: %#v", evidence)
	}
}

func TestDocumentFinalEvidenceClaimCoverageDistinguishesSourceAndProjectionLimits(t *testing.T) {
	tests := []struct {
		name           string
		call           app.ToolCall
		wantSource     string
		wantClaim      string
		wantOmission   string
		wantLimitation bool
	}{
		{
			name: "complete source and projection", call: pdfCoverageToolCall("complete", "complete document", true),
			wantSource: workflowCoverageComplete, wantClaim: workflowCoverageComplete,
		},
		{
			name: "incomplete source", call: pdfCoverageToolCall("partial", "covered pages", false),
			wantSource: workflowCoveragePartial, wantClaim: workflowCoveragePartial,
			wantOmission: "source_read_incomplete", wantLimitation: true,
		},
		{
			name: "complete source exceeds finalizer window", call: pdfCoverageToolCall("complete", strings.Repeat("projected content ", workflowFinalEvidenceMaxRunes), true),
			wantSource: workflowCoverageComplete, wantClaim: workflowCoveragePartial,
			wantOmission: "finalizer_content_truncated", wantLimitation: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection := buildWorkflowFinalEvidenceProjection(app.AgentRun{}, []app.ToolCall{test.call}, nil, nil)
			if projection.Coverage.Source != test.wantSource || projection.Coverage.Claim != test.wantClaim ||
				!projection.Coverage.CompleteForConsumer ||
				(test.wantOmission != "" && !containsString(projection.Coverage.Omissions, test.wantOmission)) {
				t.Fatalf("unexpected finalizer coverage: %#v", projection.Coverage)
			}
			wantLimitation := "limitation_required=" + strconv.FormatBool(test.wantLimitation)
			if len(projection.Evidence) != 1 || !strings.Contains(projection.Evidence[0], wantLimitation) {
				t.Fatalf("finalizer evidence omitted %q: %#v", wantLimitation, projection.Evidence)
			}
		})
	}
}

func pdfCoverageToolCall(status, content string, complete bool) app.ToolCall {
	missing := []any{}
	counts := map[string]any{"native": float64(2)}
	if !complete {
		missing = []any{float64(2)}
		counts = map[string]any{"native": float64(1), "ocr_failed": float64(1)}
	}
	return app.ToolCall{
		ID: "tc_pdf", Tool: "pdf.extract_text", Status: "completed", Capability: app.ToolCapabilityDocumentRead,
		Arguments: map[string]any{"path": "report.pdf"},
		Result: map[string]any{
			"path": "report.pdf", "content": content, "truncated": false, "read_complete": complete,
			"coverage_status": status, "missing_page_indexes": missing,
			"page_status_counts": counts,
			"document": map[string]any{
				"format": "pdf", "stats": map[string]any{"pages": float64(2)},
			},
		},
	}
}
