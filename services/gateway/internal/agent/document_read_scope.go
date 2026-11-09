package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func documentReadOutcomeAttributes(call app.ToolCall, output map[string]any) map[string]string {
	attributes := map[string]string{"capability": call.Capability}
	for _, key := range []string{"source_sha256", "structure_generation", "next_cursor", "generation_rebased", "previous_structure_generation", "read_complete"} {
		if value, ok := output[key]; ok {
			attributes[key] = stringValue(value)
		}
	}
	for _, key := range []string{"task_mode", "scope_reason", "scope_extent", "cursor", "include_descendants"} {
		if value, ok := call.Arguments[key]; ok {
			attributes[key] = stringValue(value)
		}
	}
	if call.Capability == app.ToolCapabilityDocumentSummarize {
		attributes["scope_selector"] = `{"all":true}`
	} else if selector := documentReadSelector(call.Arguments); len(selector) > 0 {
		raw, _ := json.Marshal(selector)
		attributes["scope_selector"] = string(raw)
	}
	if structure, ok := anyMap(output["structure"]); ok {
		raw, _ := json.Marshal(structure)
		attributes["structure"] = string(raw)
	}
	if coverage, ok := anyMap(output["coverage"]); ok {
		raw, _ := json.Marshal(coverage)
		attributes["coverage"] = string(raw)
		for _, key := range []string{"scope", "status", "required", "covered", "missing", "denominator_known"} {
			attributes["coverage_"+key] = stringValue(coverage[key])
		}
	}
	return attributes
}

func documentReadSelector(args map[string]any) map[string]any {
	selector := map[string]any{}
	for _, key := range []string{"node_ids", "chunk_ids", "all", "page_start", "page_end", "section"} {
		if value, ok := args[key]; ok && value != nil && stringValue(value) != "" && stringValue(value) != "false" {
			selector[key] = value
		}
	}
	return selector
}

func (documentReadProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	if state != nil && state.Route.Facts["document_format"] == app.DocumentFormatImage {
		return (documentReadProfileV5{}).Assess(state, outcome)
	}
	assessment := baseNodeAssessment(outcome)
	assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "document_scope_incomplete"
	if !isScopedDocumentRead(state) {
		return assessment
	}
	if state.Route.Facts == nil {
		state.Route.Facts = map[string]string{}
	}
	if !outcome.Status.Completed() {
		if documentReadOutcomeCapability(outcome) == app.ToolCapabilityDocumentSearch {
			failures, _ := strconv.Atoi(state.Route.Facts[documentReadFactPrefix+"query_failures"])
			state.Route.Facts[documentReadFactPrefix+"query_failures"] = strconv.Itoa(failures + 1)
			if failures < 2 {
				assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "document_scope_query_failed"
			}
		}
		// Only an initial selection may be corrected. A frozen scope or cursor
		// failure must stop; retries retain the original source version pin.
		if state.Plan.ProfileRevision >= 7 && documentReadOutcomeCapability(outcome) == app.ToolCapabilityDocumentReadRange && state.Route.Facts[documentReadFactPrefix+"scope_selector"] == "" && state.Route.Facts[documentReadFactPrefix+"next_cursor"] == "" {
			failures, _ := strconv.Atoi(state.Route.Facts[documentReadFactPrefix+"selection_failures"])
			state.Route.Facts[documentReadFactPrefix+"selection_failures"] = strconv.Itoa(failures + 1)
			if failures < 2 {
				assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "document_scope_selection_failed"
			}
		}
		return assessment
	}
	var ref app.ResourceRef
	for _, candidate := range outcome.Refs {
		if candidate.Kind == documentOutlineResourceKind || candidate.Kind == documentSearchResourceKind || candidate.Kind == documentRangeResourceKind {
			ref = candidate
			break
		}
	}
	if err := bindDocumentReadVersion(state, ref.Attributes); err != nil {
		assessment.ReasonCode = "document_source_version_changed"
		return assessment
	}
	if ref.Kind == documentOutlineResourceKind {
		state.Route.Facts[documentReadFactPrefix+"structure_coverage"] = ref.Attributes["coverage"]
		state.Route.Facts[documentReadFactPrefix+"outline_next_cursor"] = ref.Attributes["next_cursor"]
		assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "document_structure_discovered"
		return assessment
	}
	// Locating evidence is not a task-scope commitment. In r8 the content
	// decision can switch from exploratory search to whole-source summary.
	if !(state.Plan.ProfileRevision >= 8 && ref.Kind == documentSearchResourceKind) && !freezeDocumentReadMode(state, ref.Attributes, outcome.ToolCallID) {
		assessment.ReasonCode = "document_task_scope_invalid"
		return assessment
	}
	if ref.Kind == documentSearchResourceKind {
		queries, _ := strconv.Atoi(state.Route.Facts[documentReadFactPrefix+"search_queries"])
		state.Route.Facts[documentReadFactPrefix+"search_queries"] = strconv.Itoa(queries + 1)
		if queries < documentRetrievalMaxQueries {
			assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "document_scope_located"
		}
		return assessment
	}
	return assessDocumentReadCoverage(state, outcome, ref, assessment)
}

func bindDocumentReadVersion(state *app.WorkflowState, attributes map[string]string) error {
	sha, generation := attributes["source_sha256"], attributes["structure_generation"]
	if sha == "" || generation == "" {
		return fmt.Errorf("document source version is missing")
	}
	facts := state.Route.Facts
	previousSHA, previousGeneration := facts[documentReadFactPrefix+"source_sha256"], facts[documentReadFactPrefix+"structure_generation"]
	if previousSHA != "" && previousSHA != sha {
		return fmt.Errorf("document source changed")
	}
	if previousGeneration != "" && previousGeneration != generation {
		if attributes["generation_rebased"] != "true" || attributes["previous_structure_generation"] != previousGeneration || attributes["cursor"] != "" {
			return fmt.Errorf("document structure changed")
		}
	}
	facts[documentReadFactPrefix+"source_sha256"], facts[documentReadFactPrefix+"structure_generation"] = sha, generation
	if attributes["structure"] != "" {
		facts[documentReadFactPrefix+"structure"] = attributes["structure"]
	}
	return nil
}

func freezeDocumentReadMode(state *app.WorkflowState, attributes map[string]string, callID string) bool {
	mode := attributes["task_mode"]
	switch mode {
	case "targeted", "exhaustive", "whole_summary", "comparison", "calculation":
	default:
		return false
	}
	facts := state.Route.Facts
	if state.Plan.ProfileRevision >= 7 && attributes["capability"] != app.ToolCapabilityDocumentSearch {
		extent := attributes["scope_extent"]
		if extent != "whole_document" && extent != "selected_range" {
			return false
		}
		if frozen := facts[documentReadFactPrefix+"scope_extent"]; frozen != "" && frozen != extent {
			return false
		}
		facts[documentReadFactPrefix+"scope_extent"] = extent
	}
	if current := facts[documentReadFactPrefix+"task_mode"]; current != "" && current != mode {
		return false
	}
	if facts[documentReadFactPrefix+"task_mode"] == "" {
		facts[documentReadFactPrefix+"task_mode"] = mode
		facts[documentReadFactPrefix+"scope_reason"] = attributes["scope_reason"]
		facts[documentReadFactPrefix+"scope_selection_call_id"] = callID
	}
	return true
}

func assessDocumentReadCoverage(state *app.WorkflowState, outcome app.ToolOutcome, ref app.ResourceRef, assessment app.NodeAssessment) app.NodeAssessment {
	facts, attributes := state.Route.Facts, ref.Attributes
	selector, previous := attributes["scope_selector"], facts[documentReadFactPrefix+"scope_selector"]
	if previous == "" && selector == "" || previous != "" && selector != "" && previous != selector {
		assessment.ReasonCode = "document_task_scope_invalid"
		return assessment
	}
	if previous == "" {
		facts[documentReadFactPrefix+"scope_selector"] = selector
	}
	mode := facts[documentReadFactPrefix+"task_mode"]
	if mode == "whole_summary" && facts[documentReadFactPrefix+"scope_selector"] != `{"all":true}` {
		assessment.ReasonCode = "document_task_scope_invalid"
		return assessment
	}
	if documentReadOutcomeCapability(outcome) == app.ToolCapabilityDocumentSummarize && mode != "whole_summary" {
		assessment.ReasonCode = "document_task_scope_invalid"
		return assessment
	}
	previousCursor := facts[documentReadFactPrefix+"next_cursor"]
	facts[documentReadFactPrefix+"next_cursor"] = attributes["next_cursor"]
	facts[documentReadFactPrefix+"coverage"] = attributes["coverage"]
	facts[documentReadFactPrefix+"scope_status"] = "partial"
	complete := attributes["read_complete"] == "true" && attributes["coverage_status"] == "complete" && attributes["coverage_denominator_known"] == "true" && attributes["coverage_missing"] == "0" && attributes["coverage_required"] == attributes["coverage_covered"] && attributes["coverage_required"] != "" && attributes["next_cursor"] == ""
	if complete && containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable) {
		facts[documentReadFactPrefix+"scope_status"] = "complete"
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "document_declared_scope_complete"
		return assessment
	}
	if cursor := attributes["next_cursor"]; cursor != "" && cursor != previousCursor {
		assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "document_scope_continuation"
		assessment.Signals = []app.OutcomeSignal{app.OutcomeSignalContentAvailable}
	}
	return assessment
}

func documentScopedReadMessage(state *app.WorkflowState, goal string) string {
	if !isScopedDocumentRead(state) {
		return ""
	}
	for _, node := range state.Nodes {
		if node.LastAssessment != nil && node.LastAssessment.ReasonCode == "document_source_version_changed" {
			if chineseRequest(goal) {
				return "文档版本在读取过程中发生变化，已停止读取；不能把不同版本的内容合并为完整结论。"
			}
			return "The document version changed during reading. Reading stopped; different versions cannot be combined into a complete conclusion."
		}
	}
	coverage := map[string]any{}
	_ = json.Unmarshal([]byte(state.Route.Facts[documentReadFactPrefix+"coverage"]), &coverage)
	if state.Plan.ProfileRevision >= 8 && state.Route.Facts["document_format"] == app.DocumentFormatPPTX {
		if chineseRequest(goal) {
			return "幻灯片的原生对象解析尚不完整；以下结论需结合已读取的文字、页图像及各自的限制。"
		}
		return "Native slide-object parsing is incomplete; conclusions depend on the acquired text, slide images and their respective limitations."
	}
	remaining := strings.TrimSpace(stringValue(coverage["missing"]))
	if remaining == "" || remaining == "<nil>" || coverage["denominator_known"] != true || remaining == "0" && coverage["status"] != "complete" {
		remaining = "unknown"
	}
	if chineseRequest(goal) {
		if remaining == "unknown" {
			return "文档读取尚未覆盖所需范围，目前不能给出完整结论。剩余范围尚未确定。"
		}
		return fmt.Sprintf("文档读取仅完成部分范围，仍有 %s 个范围单元未覆盖，不能据此给出完整结论。", remaining)
	}
	return fmt.Sprintf("The document task is incomplete. Remaining scope units: %s. The available passages do not support a complete conclusion.", remaining)
}

func documentReadOutcomeCapability(outcome app.ToolOutcome) string {
	for _, ref := range outcome.Refs {
		if capability := ref.Attributes["capability"]; capability != "" {
			return capability
		}
	}
	return ""
}
