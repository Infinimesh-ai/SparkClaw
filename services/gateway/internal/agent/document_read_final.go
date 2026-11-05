package agent

import (
	"encoding/json"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// An unavailable visual object must keep coverage partial, but must not hide
// successfully acquired text. Source/version failures and unfinished traversal
// cannot enter this path. The run remains blocked and carries its limitation.
func documentCanPresentPartialEvidence(run app.AgentRun, calls []app.ToolCall) bool {
	if !isScopedDocumentRead(run.Workflow) || run.Workflow.Plan.ProfileRevision < 7 {
		return false
	}
	facts := run.Workflow.Route.Facts
	if facts[documentReadFactPrefix+"scope_status"] != "partial" || facts[documentReadFactPrefix+"next_cursor"] != "" {
		return false
	}
	assessment := run.Workflow.Nodes[documentReadNodeID].LastAssessment
	if assessment == nil || assessment.ReasonCode != "document_scope_incomplete" {
		return false
	}
	for _, call := range calls {
		if output, ok := anyMap(call.Result); ok && toolCallCompleted(call) && documentContentCapability(call.Capability) && strings.TrimSpace(firstNonEmptyString(output["content"], output["summary"])) != "" {
			return true
		}
	}
	return false
}

func applyDocumentScopeToFinalProjection(run app.AgentRun, projection *workflowFinalEvidenceProjection) {
	if !isScopedDocumentRead(run.Workflow) {
		return
	}
	facts := run.Workflow.Route.Facts
	manifest := map[string]any{}
	for _, key := range []string{"task_mode", "scope_reason", "scope_extent", "scope_status", "scope_selection_call_id", "source_sha256", "structure_generation"} {
		manifest[key] = facts[documentReadFactPrefix+key]
	}
	for _, key := range []string{"scope_selector", "coverage", "structure_coverage", "structure"} {
		var value any
		if json.Unmarshal([]byte(facts[documentReadFactPrefix+key]), &value) == nil {
			manifest[key] = value
		}
	}
	if run.Workflow.Plan.ProfileRevision >= 7 {
		manifest["outline_discovery_coverage"] = manifest["structure_coverage"]
		delete(manifest, "structure_coverage")
	}
	manifest["continuation_remaining"] = facts[documentReadFactPrefix+"next_cursor"] != ""
	if run.Workflow.Plan.ProfileRevision >= 7 {
		manifest["selected_headings_include_descendants"] = true
	}
	manifest["semantic_correctness"] = "not_assessed"
	if facts[documentReadFactPrefix+"scope_status"] != "complete" {
		projection.Coverage.Claim = workflowCoveragePartial
		projection.Coverage.CompleteForConsumer = false
		projection.Coverage.Omissions = appendUniqueString(projection.Coverage.Omissions, "declared_task_scope_incomplete")
	}
	if projection.Coverage.Claim == workflowCoveragePartial {
		projection.Coverage.CompleteForConsumer = false
	}
	manifest["model_exposure_complete"] = projection.Coverage.Claim == workflowCoverageComplete
	if facts[documentReadFactPrefix+"scope_selector"] != `{"all":true}` {
		projection.Coverage.Source = workflowCoverageBounded
	}
	raw, _ := json.Marshal(manifest)
	projection.Evidence = append([]string{"task_scope_manifest: " + string(raw)}, projection.Evidence...)
}

// Values and their required context form one semantic object. Headings may
// carry units or qualifications even when the source node is plain text.
func scopedDocumentFinalEvidence(output map[string]any, text string, budget int) (string, bool) {
	structured := len(anySlice(output["dependencies"])) > 0
	for _, key := range []string{"nodes", "dependencies"} {
		for _, raw := range anySlice(output[key]) {
			if node, ok := anyMap(raw); ok && (len(anySlice(node["values"])) > 0 || len(stringListValue(node["dependencies"])) > 0) {
				structured = true
			}
		}
	}
	if !structured {
		projected := sampledDocumentEvidence(text, budget)
		return projected, projected != text
	}
	raw, err := json.Marshal(map[string]any{"nodes": output["nodes"], "dependencies": output["dependencies"], "source_sha256": output["source_sha256"], "structure_generation": output["structure_generation"]})
	if err != nil || len(raw) > budget {
		return boundedUTF8Prefix([]byte("[Source values and their required context exceed the final model budget and were not projected.]"), max(0, budget)), true
	}
	return string(raw), false
}
