package agent

import (
	"encoding/json"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func applyDocumentScopeToFinalProjection(run app.AgentRun, projection *workflowFinalEvidenceProjection) {
	if !isScopedDocumentRead(run.Workflow) {
		return
	}
	facts := run.Workflow.Route.Facts
	manifest := map[string]any{}
	for _, key := range []string{"task_mode", "scope_reason", "scope_status", "scope_selection_call_id", "source_sha256", "structure_generation"} {
		manifest[key] = facts[documentReadFactPrefix+key]
	}
	for _, key := range []string{"scope_selector", "coverage", "structure_coverage", "structure"} {
		var value any
		if json.Unmarshal([]byte(facts[documentReadFactPrefix+key]), &value) == nil {
			manifest[key] = value
		}
	}
	manifest["continuation_remaining"] = facts[documentReadFactPrefix+"next_cursor"] != ""
	manifest["model_exposure_complete"] = projection.Coverage.Claim == workflowCoverageComplete
	manifest["semantic_correctness"] = "not_assessed"
	if facts[documentReadFactPrefix+"scope_status"] != "complete" {
		projection.Coverage.Claim = workflowCoveragePartial
		projection.Coverage.CompleteForConsumer = false
		projection.Coverage.Omissions = appendUniqueString(projection.Coverage.Omissions, "declared_task_scope_incomplete")
	}
	if facts[documentReadFactPrefix+"scope_selector"] != `{"all":true}` {
		projection.Coverage.Source = workflowCoverageBounded
	}
	raw, _ := json.Marshal(manifest)
	projection.Evidence = append([]string{"task_scope_manifest: " + string(raw)}, projection.Evidence...)
}

// Typed values and their dependencies form one semantic object. Never expose
// a cached formula value while dropping the formula/provenance flags around it.
func scopedDocumentFinalEvidence(output map[string]any, text string, budget int) (string, bool) {
	typed := false
	for _, key := range []string{"nodes", "dependencies"} {
		for _, raw := range anySlice(output[key]) {
			if node, ok := anyMap(raw); ok && len(anySlice(node["values"])) > 0 {
				typed = true
			}
		}
	}
	if !typed {
		projected := sampledDocumentEvidence(text, budget)
		return projected, projected != text
	}
	raw, err := json.Marshal(map[string]any{"nodes": output["nodes"], "dependencies": output["dependencies"], "source_sha256": output["source_sha256"], "structure_generation": output["structure_generation"]})
	if err != nil || len(raw) > budget {
		return boundedUTF8Prefix([]byte("[Typed source values and their required context exceed the final model budget and were not projected.]"), max(0, budget)), true
	}
	return string(raw), false
}
