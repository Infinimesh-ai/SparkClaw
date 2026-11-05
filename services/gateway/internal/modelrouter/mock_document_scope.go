package modelrouter

import "strings"

// This deterministic fixture stands in for the step model only in mock mode.
// Production task modes come from the model's structured action arguments.
func mockDocumentScopedReadAction(user, goal, prompt string) (string, bool) {
	if !strings.Contains(prompt, "structure-first reading, revision 6") || !strings.Contains(prompt, "document.search") {
		return "", false
	}
	lower := strings.ToLower(goal)
	specific := strings.Contains(lower, "about") || strings.Contains(lower, "关于") || strings.Contains(lower, "慢开始")
	whole := strings.Contains(lower, "whole") || strings.Contains(lower, "全文") || strings.Contains(lower, "整本") || !specific && (strings.Contains(lower, "summar") || strings.Contains(lower, "总结"))
	if whole {
		return mockWorkflowStepAction("document.summarize", map[string]any{"scope_reason": "The request asks for a whole-document summary."}), true
	}
	mode := "targeted"
	if strings.Contains(lower, "compare") || strings.Contains(lower, "比较") {
		mode = "comparison"
	}
	if strings.Contains(lower, "calculate") || strings.Contains(lower, "计算") {
		mode = "calculation"
	}
	if !specific || mode != "targeted" {
		if mode == "targeted" {
			mode = "exhaustive"
		}
		return mockWorkflowStepAction("document.read_range", map[string]any{"all": true, "task_mode": mode, "scope_reason": "The request requires the declared whole-source scope."}), true
	}
	if !strings.Contains(user, "document.search keywords=") {
		return mockWorkflowStepAction("document.search", map[string]any{"keywords": []string{goal}, "top_k": 5, "task_mode": mode, "scope_reason": "Locate the passage needed for the targeted question."}), true
	}
	chunks := []string{}
	for _, match := range mockSearchHitChunk.FindAllStringSubmatch(user, 3) {
		chunks = append(chunks, match[1])
	}
	if len(chunks) == 0 {
		return mockWorkflowStepAction("document.read_range", map[string]any{"all": true, "task_mode": mode}), true
	}
	return mockWorkflowStepAction("document.read_range", map[string]any{"chunk_ids": chunks, "task_mode": mode}), true
}
