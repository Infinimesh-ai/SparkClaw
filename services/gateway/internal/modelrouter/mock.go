package modelrouter

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"unicode"
)

func mockResponse(lane, user string) string {
	if injected := mockInjectedResponse(user, "MOCK_OPERATION_SELECTION_RESPONSE:"); injected != "" {
		return injected
	}
	if strings.Contains(user, "WORKFLOW_FINAL_ANSWER_REQUEST") {
		if injected := mockInjectedResponse(user, "MOCK_WORKFLOW_FINAL_RESPONSE:"); injected != "" {
			return injected
		}
		if strings.Contains(user, "images.inspect") {
			return "Mock image inspection completed from the workflow evidence."
		}
		if strings.Contains(user, "browser.read") {
			return "Mock browser page answer grounded in the extracted page evidence."
		}
		return "Mock workflow answer grounded in the completed document evidence."
	}
	if strings.Contains(user, "WORKFLOW_MODEL_ANSWER_REQUEST") {
		if injected := mockInjectedResponse(user, "MOCK_CONVERSATION_RESPONSE:"); injected != "" {
			return injected
		}
		return "I can answer this directly from the current conversation."
	}
	if strings.Contains(user, "INTENT_FUSION_TREE_REPAIR_REQUEST") {
		if injected := mockInjectedResponse(user, "MOCK_INTENT_TREE_REPAIR_RESPONSE:"); injected != "" {
			return injected
		}
		return mockIntentFusionResponse(user)
	}
	if strings.Contains(user, "INTENT_FUSION_TREE_REQUEST") {
		if injected := mockInjectedResponse(user, "MOCK_INTENT_TREE_RESPONSE:"); injected != "" {
			return injected
		}
		return mockIntentFusionResponse(user)
	}
	if injected := mockInjectedResponse(user, "MOCK_TASK_HINT_RESPONSE:"); injected != "" {
		return injected
	}
	if strings.Contains(user, "WORKFLOW_STEP_REQUEST") {
		if strings.Contains(user, "WORKFLOW_SEMANTIC_REPAIR_REQUEST") {
			if injected := mockInjectedResponse(user, "MOCK_STEP_REPAIR_RESPONSE:"); injected != "" {
				return injected
			}
		}
		lower := strings.ToLower(user)
		for _, stage := range []struct {
			tool   string
			marker string
		}{
			{"weather.lookup", "MOCK_WEATHER_LOOKUP_RESPONSE:"},
			{"media.render_weather_card", "MOCK_WEATHER_RENDER_RESPONSE:"},
		} {
			if strings.Contains(lower, "model-visible tools this workflow stage: "+stage.tool) {
				if injected := mockInjectedResponse(user, stage.marker); injected != "" {
					return injected
				}
			}
		}
	}
	if injected := mockInjectedResponse(user, "MOCK_STEP_RESPONSE:"); injected != "" {
		return injected
	}
	if strings.Contains(user, "WORKFLOW_STEP_REQUEST") {
		return mockWorkflowStepResponse(user)
	}
	lower := strings.ToLower(user)
	switch {
	case strings.Contains(lower, "approval") || strings.Contains(lower, "shell") || strings.Contains(lower, "delete"):
		return "I will keep this behind SparkClaw approval policy and stage the action instead of executing it."
	case strings.Contains(lower, "remember") || strings.Contains(lower, "记住"):
		return "I will create a memory candidate for owner review."
	case strings.Contains(lower, "search") || strings.Contains(lower, "找"):
		return "I will search the allowed workspace and report only observed results."
	case lane == "deep":
		return "I will use the deep lane because this task has higher risk or complexity."
	default:
		return "I will use the fast lane for a bounded local-first response."
	}
}

func mockWorkflowStepResponse(user string) string {
	goal := mockWorkflowStepGoal(user)
	lowerGoal := strings.ToLower(goal)
	lowerPrompt := strings.ToLower(user)
	if action, ok := mockCodingAgentWorkflowAction(lowerPrompt, lowerGoal); ok {
		return action
	}
	switch {
	case strings.Contains(lowerPrompt, "model-visible tools this workflow stage: weather.lookup"):
		return mockWorkflowStepAction("weather.lookup", map[string]any{"location": "workflow-bound location"})
	case strings.Contains(lowerPrompt, "model-visible tools this workflow stage: media.render_weather_card"):
		return mockWorkflowStepAction("media.render_weather_card", map[string]any{"weather_payload_ref": "workflow-bound weather payload"})
	}
	if stage := mockBrowserInteractionStage(lowerPrompt); stage != "" {
		return mockBrowserInteractionAction(user, goal, stage)
	}
	switch {
	case strings.Contains(lowerPrompt, "model-visible tools this workflow stage: browser.list_tabs") && !strings.Contains(lowerPrompt, "browser.list_tabs observation"):
		return mockWorkflowStepAction("browser.list_tabs", map[string]any{})
	case strings.Contains(lowerPrompt, "model-visible tools this workflow stage: browser.focus") && !strings.Contains(lowerPrompt, "browser.focus observation"):
		return mockWorkflowStepAction("browser.focus", map[string]any{"page_id": mockWorkflowPageID(user)})
	case strings.Contains(lowerPrompt, "model-visible tools this workflow stage: browser.open") && !strings.Contains(lowerPrompt, "browser.open observation"):
		urls := mockURLs(goal)
		if len(urls) > 0 {
			return mockWorkflowStepAction("browser.open", map[string]any{"url": urls[0]})
		}
	}
	if strings.Contains(lowerPrompt, "previous observation summaries") {
		if strings.Contains(lowerPrompt, "workflow_requirement: source_page_required") && !strings.Contains(lowerPrompt, "browser.read observation") {
			urls := mockURLs(user)
			if len(urls) > 0 {
				return mockWorkflowStepAction("browser.read", map[string]any{"url": urls[0]})
			}
		}
		if strings.Contains(lowerGoal, "failing test") || strings.Contains(lowerGoal, "failed test") {
			if strings.Contains(lowerPrompt, "files.search observation") {
				return mockWorkflowStepAction("shell.exec_sandboxed", map[string]any{"command": "npm test"})
			}
			return mockWorkflowStepAction("files.search", map[string]any{"query": "test"})
		}
		if strings.Contains(lowerGoal, "compare") {
			paths := mockPaths(goal)
			readCount := strings.Count(lowerPrompt, "files.read observation")
			if readCount < len(paths) {
				return mockWorkflowStepAction("files.read", map[string]any{"path": paths[readCount]})
			}
		}
		if strings.Contains(lowerPrompt, "browser.read observation") && strings.Contains(lowerGoal, "compare") && strings.Count(lowerPrompt, "browser.read observation") < 2 {
			urls := mockURLs(goal)
			if len(urls) > 1 {
				return mockWorkflowStepAction("browser.read", map[string]any{"url": urls[1]})
			}
		}
		if strings.Contains(lowerPrompt, "browser.type") && (strings.Contains(lowerGoal, "截图") || strings.Contains(lowerGoal, "screenshot")) && !strings.Contains(lowerPrompt, "browser.screenshot") {
			return mockWorkflowStepAction("browser.screenshot", map[string]any{})
		}
		if strings.Contains(lowerGoal, "detail") && strings.Contains(lowerPrompt, "web.search observation") {
			return `{"type":"final","answer":"I reviewed the observed web search evidence and prepared the bounded answer."}`
		}
		return `{"type":"final","answer":"I reviewed the observed evidence and prepared the bounded answer."}`
	}
	switch {
	case (strings.Contains(lowerGoal, "输入") || strings.Contains(lowerGoal, "type")) && (strings.Contains(lowerGoal, "截图") || strings.Contains(lowerGoal, "screenshot")):
		return mockWorkflowStepAction("browser.type", map[string]any{"text": "苹果"})
	case strings.Contains(lowerGoal, "inspect repo"):
		if strings.Contains(lowerGoal, "failing test") || strings.Contains(lowerGoal, "failed test") {
			return mockWorkflowStepAction("files.search", map[string]any{"query": "test"})
		}
		return mockWorkflowStepAction("files.search", map[string]any{"query": "repo"})
	case strings.Contains(lowerGoal, "shell command") || strings.Contains(lowerGoal, "run tests"):
		return mockWorkflowStepAction("shell.exec_sandboxed", map[string]any{"command": mockShellCommand(goal)})
	case strings.Contains(lowerGoal, "remember"):
		return mockWorkflowStepAction("memory.write_candidate", map[string]any{
			"content":     goal,
			"kind":        "note",
			"sensitivity": "normal",
			"reason":      "User asked SparkClaw to remember this.",
		})
	case len(mockURLs(goal)) > 0:
		return mockWorkflowStepAction("browser.read", map[string]any{"url": mockURLs(goal)[0]})
	case strings.Contains(lowerGoal, "web") || strings.Contains(lowerGoal, "internet") || strings.Contains(lowerGoal, "news") || strings.Contains(lowerGoal, "latest") || strings.Contains(lowerGoal, "today") || strings.Contains(lowerGoal, "search online") || strings.Contains(lowerGoal, "网上") || strings.Contains(lowerGoal, "联网") || strings.Contains(lowerGoal, "查一下") || strings.Contains(lowerGoal, "最新"):
		return mockWorkflowStepAction("web.search", map[string]any{"query": mockSearchQuery(goal)})
	case strings.Contains(lowerGoal, "compare"):
		paths := mockPaths(goal)
		if len(paths) > 0 {
			return mockWorkflowStepAction("files.read", map[string]any{"path": paths[0]})
		}
		return mockWorkflowStepAction("files.search", map[string]any{"query": mockSearchQuery(goal)})
	case strings.Contains(lowerGoal, "search") || strings.Contains(lowerGoal, "find"):
		return mockWorkflowStepAction("files.search", map[string]any{"query": mockSearchQuery(goal)})
	case strings.Contains(lowerGoal, "read") || strings.Contains(lowerGoal, "summarize"):
		return mockWorkflowStepAction("files.read", map[string]any{"path": mockPath(goal)})
	default:
		return `{"type":"final","answer":"I can answer this directly from the current conversation."}`
	}
}

func mockCodingAgentWorkflowAction(prompt, goal string) (string, bool) {
	if !strings.Contains(prompt, "mcp.happy-") {
		return "", false
	}
	targets := []string{}
	switch {
	case containsAnyTerm(goal, "create task", "创建", "新建"):
		targets = []string{"create_task"}
	case containsAnyTerm(goal, "spawn", "启动", "新会话"):
		targets = []string{"spawn_session"}
	case containsAnyTerm(goal, "send message", "发消息", "发送消息"):
		targets = []string{"send_message"}
	case containsAnyTerm(goal, "stop session", "停止会话"):
		targets = []string{"stop_session"}
	case containsAnyTerm(goal, "cancel task", "取消任务"):
		targets = []string{"cancel_task"}
	case containsAnyTerm(goal, "session messages", "session transcript", "会话消息", "消息记录", "完整过程"):
		targets = []string{"get_session_messages"}
	case containsAnyTerm(goal, "task", "任务"):
		targets = []string{"list_tasks", "get_task"}
	case containsAnyTerm(goal, "session", "会话"):
		targets = []string{"list_sessions", "get_session"}
	}
	for _, target := range targets {
		if tool := mockVisibleToolWithRemoteName(prompt, target); tool != "" {
			return mockWorkflowStepAction(tool, map[string]any{}), true
		}
	}
	return "", false
}

func mockVisibleToolWithRemoteName(prompt, remoteName string) string {
	const marker = "model-visible tools this workflow stage: "
	index := strings.LastIndex(prompt, marker)
	if index < 0 {
		return ""
	}
	line := prompt[index+len(marker):]
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	for _, name := range strings.Split(line, ",") {
		name = strings.TrimSpace(name)
		if name == remoteName || strings.HasSuffix(name, "."+remoteName) {
			return name
		}
	}
	return ""
}

func mockBrowserInteractionStage(prompt string) string {
	marker := "workflow_stage:"
	index := strings.LastIndex(prompt, marker)
	if index < 0 {
		return ""
	}
	value := strings.TrimSpace(prompt[index+len(marker):])
	if end := strings.IndexAny(value, " .\t\r\n,;}"); end >= 0 {
		value = value[:end]
	}
	switch value {
	case "health_check", "scan_tabs", "focus_existing", "navigate_blank", "open_new",
		"snapshot_before_action", "choose_and_click", "snapshot_after_action",
		"assess_goal_initial", "choose_and_draft", "assess_goal_after_action", "assess_goal_visible":
		return value
	default:
		return ""
	}
}

func mockBrowserInteractionAction(prompt, goal, stage string) string {
	switch stage {
	case "health_check":
		return mockWorkflowStepAction("browser.status", map[string]any{})
	case "scan_tabs":
		return mockWorkflowStepAction("browser.list_tabs", map[string]any{})
	case "focus_existing":
		return mockWorkflowStepAction("browser.focus", map[string]any{"page_id": mockWorkflowPageID(prompt)})
	case "navigate_blank":
		urls := mockURLs(goal)
		if len(urls) > 0 {
			return mockWorkflowStepAction("browser.navigate", map[string]any{"page_id": mockWorkflowPageID(prompt), "url": urls[0]})
		}
	case "open_new":
		urls := mockURLs(goal)
		if len(urls) > 0 {
			return mockWorkflowStepAction("browser.open", map[string]any{"url": urls[0]})
		}
	case "snapshot_before_action", "snapshot_after_action":
		return mockWorkflowStepAction("browser.snapshot", map[string]any{})
	case "choose_and_draft":
		snapshotID, pageID, elementRef := mockLatestBrowserSnapshot(prompt)
		return mockWorkflowStepAction("browser.type", map[string]any{
			"page_id": pageID, "snapshot_id": snapshotID, "uid": elementRef, "text": mockBrowserDraftValue(goal),
		})
	case "choose_and_click":
		snapshotID, pageID, elementRef := mockLatestBrowserSnapshot(prompt)
		return mockWorkflowStepAction("browser.click", map[string]any{
			"page_id": pageID, "snapshot_id": snapshotID, "uid": elementRef,
			"expected_effect": "Advance the frozen browser interaction goal.",
		})
	case "assess_goal_initial", "assess_goal_after_action", "assess_goal_visible":
		snapshotID, _, elementRef := mockLatestBrowserSnapshot(prompt)
		verdict := "satisfied"
		if stage == "assess_goal_initial" {
			verdict = "progress"
		}
		return mockWorkflowStepAction("browser.assess_goal", map[string]any{
			"snapshot_id": snapshotID, "verdict": verdict,
			"evidence_refs": []string{elementRef},
			"reason":        "The cited control in the current snapshot supports the bounded goal assessment.",
		})
	}
	return `{"type":"final","answer":"The browser interaction workflow could not select its required next action."}`
}

func mockBrowserDraftValue(goal string) string {
	for _, marker := range []string{"输入", "填写", "填入", "type", "fill"} {
		if index := strings.Index(strings.ToLower(goal), marker); index >= 0 {
			value := strings.TrimSpace(goal[index+len(marker):])
			if end := strings.IndexAny(value, "，,。.;；\n"); end >= 0 {
				value = value[:end]
			}
			if fields := strings.Fields(value); len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return "owner supplied value"
}

func mockLatestBrowserSnapshot(prompt string) (string, string, string) {
	normalized := strings.ReplaceAll(prompt, `\"`, `"`)
	elementRef := mockLastBrowserFieldValue(normalized, "ref")
	if elementRef == "" {
		return "snapshot_missing", mockWorkflowPageID(prompt), "element_missing"
	}
	snapshotID := ""
	if marker := strings.Index(elementRef, ":e"); marker > 0 {
		snapshotID = elementRef[:marker]
	}
	return snapshotID, "", elementRef
}

func mockBrowserFieldValues(prompt, key string) []string {
	normalized := strings.ReplaceAll(prompt, `\"`, `"`)
	marker := `"` + key + `":"`
	values := []string{}
	seen := map[string]bool{}
	for offset := 0; offset < len(normalized); {
		index := strings.Index(normalized[offset:], marker)
		if index < 0 {
			break
		}
		index += offset + len(marker)
		end := strings.IndexByte(normalized[index:], '"')
		if end < 0 {
			break
		}
		value := normalized[index : index+end]
		if value != "" && !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
		offset = index + end + 1
	}
	return values
}

func mockLastBrowserFieldValue(prompt, key string) string {
	values := mockBrowserFieldValues(prompt, key)
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func mockWorkflowPageID(prompt string) string {
	marker := "page_id="
	index := strings.LastIndex(prompt, marker)
	if index < 0 {
		return "1"
	}
	value := prompt[index+len(marker):]
	if end := strings.IndexAny(value, " \t\r\n,;}"); end >= 0 {
		value = value[:end]
	}
	return strings.TrimSpace(value)
}

func mockWorkflowStepAction(tool string, args map[string]any) string {
	raw, _ := json.Marshal(map[string]any{
		"type":      "action",
		"tool":      tool,
		"arguments": args,
		"reason":    "mock workflow step action for test coverage",
	})
	return string(raw)
}

func mockWorkflowStepGoal(user string) string {
	marker := "User goal:"
	idx := strings.Index(user, marker)
	if idx < 0 {
		return strings.TrimSpace(user)
	}
	rest := strings.TrimSpace(user[idx+len(marker):])
	if next := strings.Index(rest, "\n\n"); next >= 0 {
		rest = rest[:next]
	}
	return strings.TrimSpace(rest)
}

func mockURLs(content string) []string {
	fields := strings.Fields(content)
	urls := []string{}
	for _, field := range fields {
		cleaned := strings.Trim(field, ".,;:()[]{}<>\"'`")
		if strings.HasPrefix(cleaned, "http://") || strings.HasPrefix(cleaned, "https://") {
			urls = append(urls, cleaned)
		}
	}
	return urls
}

func mockPath(content string) string {
	paths := mockPaths(content)
	if len(paths) > 0 {
		return paths[0]
	}
	return "missing.txt"
}

func mockPaths(content string) []string {
	paths := []string{}
	for _, field := range strings.Fields(content) {
		cleaned := strings.Trim(field, ".,;:()[]{}<>\"'`")
		if strings.Contains(cleaned, ".") && !strings.HasPrefix(cleaned, "http") {
			paths = append(paths, cleaned)
		}
	}
	return paths
}

func mockSearchQuery(content string) string {
	lower := strings.ToLower(content)
	if idx := strings.Index(lower, "search email for "); idx >= 0 {
		return strings.TrimSpace(content[idx+len("search email for "):])
	}
	for _, prefix := range []string{"search for ", "find ", "search "} {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			query := strings.TrimSpace(content[idx+len(prefix):])
			query = strings.TrimSuffix(query, " in the workspace")
			if query != "" {
				return query
			}
		}
	}
	return strings.TrimSpace(content)
}

func mockShellCommand(content string) string {
	if start := strings.Index(content, "`"); start >= 0 {
		if end := strings.Index(content[start+1:], "`"); end >= 0 {
			return content[start+1 : start+1+end]
		}
	}
	if strings.Contains(strings.ToLower(content), "run tests") {
		return "npm test"
	}
	return "ls -la"
}

func mockInjectedResponse(user, marker string) string {
	idx := strings.Index(user, marker)
	if idx < 0 {
		return ""
	}
	value := strings.TrimSpace(user[idx+len(marker):])
	if newline := strings.Index(value, "\n"); newline >= 0 {
		value = strings.TrimSpace(value[:newline])
	}
	return value
}

func mockGuard(content string) GuardResult {
	lower := strings.ToLower(content)
	categories := []string{}
	verdict := "allow"
	reason := "No guard trigger matched."
	if containsAnyTerm(lower, "ignore previous instructions", "ignore all previous instructions", "developer message", "system prompt", "jailbreak", "bypass policy") {
		verdict = "review"
		categories = append(categories, "prompt_injection")
		reason = "Content appears to request instruction override or policy bypass."
	}
	if containsAnyTerm(lower, "api_key", "password", "ssh_key", "secret", "token") && containsAnyTerm(lower, "send", "exfiltrate", "leak", "print", "reveal") {
		verdict = "block"
		categories = append(categories, "secret_exfiltration")
		reason = "Content appears to request secret disclosure or exfiltration."
	}
	if containsAnyTerm(lower, "rm -rf /", "delete everything", "format disk") {
		if verdict != "block" {
			verdict = "review"
		}
		categories = append(categories, "destructive_action")
		reason = "Content references destructive host or file operations."
	}
	return GuardResult{
		Verdict:    verdict,
		Categories: uniqueStrings(categories),
		Reason:     reason,
	}
}

func mockEmbeddings(inputs []string) [][]float32 {
	vectors := make([][]float32, 0, len(inputs))
	for _, input := range inputs {
		vector := make([]float32, 64)
		terms := mockSemanticTerms(input)
		if len(terms) == 0 {
			terms = []string{input}
		}
		for _, term := range terms {
			sum := sha256.Sum256([]byte(term))
			idx := int(binary.BigEndian.Uint16(sum[:2]) % uint16(len(vector)))
			sign := float32(1)
			if sum[2]%2 == 1 {
				sign = -1
			}
			vector[idx] += sign
		}
		normalize(vector)
		vectors = append(vectors, vector)
	}
	return vectors
}

type mockIntentGraphCandidate struct {
	CandidateID       string   `json:"candidate_id"`
	SemanticBoundary  string   `json:"semantic_boundary"`
	PositiveSemantics []string `json:"positive_semantics"`
	HardNegatives     []string `json:"hard_negatives"`
}

func mockIntentFusionResponse(user string) string {
	revision := mockPromptValue(user, "Graph revision: ")
	query := mockPromptSection(user, "Owner semantic query:\n", "\n\nReturn the scored registered candidates now.")
	graphJSON := mockPromptSection(user, "Semantic graph:\n", "\n\nOwner semantic query:")
	var graph []mockIntentGraphCandidate
	if json.Unmarshal([]byte(graphJSON), &graph) != nil || len(graph) == 0 {
		encoded, _ := json.Marshal(map[string]any{"graph_revision": revision, "candidates": []any{}})
		return string(encoded)
	}
	type scoredCandidate struct {
		ID    string
		Score float64
	}
	scored := make([]scoredCandidate, 0, len(graph))
	for _, candidate := range graph {
		positive := mockSemanticSimilarity(query, candidate.SemanticBoundary)
		for _, example := range candidate.PositiveSemantics {
			positive = max(positive, mockSemanticSimilarity(query, example))
		}
		negative := 0.0
		for _, example := range candidate.HardNegatives {
			negative = max(negative, mockSemanticSimilarity(query, example))
		}
		score := min(0.99, max(0.01, 0.08+1.25*positive-0.55*negative))
		if prior := mockIntentCandidatePrior(query, candidate.CandidateID); prior > 0 {
			score = prior
		} else {
			score = min(score, 0.25)
		}
		scored = append(scored, scoredCandidate{ID: candidate.CandidateID, Score: score})
	}
	slices.SortFunc(scored, func(left, right scoredCandidate) int {
		if left.Score > right.Score {
			return -1
		}
		if left.Score < right.Score {
			return 1
		}
		return strings.Compare(left.ID, right.ID)
	})
	candidates := make([]map[string]any, 0, len(scored))
	for _, candidate := range scored {
		candidates = append(candidates, map[string]any{
			"candidate_id": candidate.ID, "tree_score": candidate.Score,
		})
	}
	encoded, _ := json.Marshal(map[string]any{"graph_revision": revision, "candidates": candidates})
	return string(encoded)
}

func mockPromptValue(prompt, prefix string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), prefix))
		}
	}
	return ""
}

func mockPromptSection(prompt, start, end string) string {
	index := strings.Index(prompt, start)
	if index < 0 {
		return ""
	}
	section := prompt[index+len(start):]
	if endIndex := strings.Index(section, end); endIndex >= 0 {
		section = section[:endIndex]
	}
	return strings.TrimSpace(section)
}

func mockSemanticSimilarity(left, right string) float64 {
	leftTerms := mockSemanticTermSet(left)
	rightTerms := mockSemanticTermSet(right)
	if len(leftTerms) == 0 || len(rightTerms) == 0 {
		return 0
	}
	intersection := 0
	for term := range leftTerms {
		if rightTerms[term] {
			intersection++
		}
	}
	return float64(intersection) / math.Sqrt(float64(len(leftTerms)*len(rightTerms)))
}

func mockSemanticTermSet(value string) map[string]bool {
	terms := mockSemanticTerms(value)
	set := make(map[string]bool, len(terms))
	for _, term := range terms {
		set[term] = true
	}
	return set
}

func mockSemanticTerms(value string) []string {
	lower := strings.ToLower(value)
	terms := strings.FieldsFunc(lower, func(char rune) bool {
		return !(unicode.IsLetter(char) || unicode.IsDigit(char))
	})
	for _, run := range strings.FieldsFunc(lower, func(char rune) bool { return !unicode.In(char, unicode.Han) }) {
		runes := []rune(run)
		for size := 1; size <= 3; size++ {
			for start := 0; start+size <= len(runes); start++ {
				terms = append(terms, string(runes[start:start+size]))
			}
		}
	}
	if len(terms) == 0 {
		return []string{lower}
	}
	return terms
}

func mockIntentCandidatePrior(query, candidateID string) float64 {
	lower := strings.ToLower(strings.TrimSpace(query))
	contains := func(terms ...string) bool { return containsAnyTerm(lower, terms...) }
	temporal := contains("秒后", "分钟后", "小时后", "天后", "明天", "后天", "稍后", "到时候", "每天", "每周", "tomorrow", "later", "every ")
	scheduleDiscussion := contains("为什么", "失败", "没有触发", "没触发", "why", "failed", "failure")
	scheduleStatement := contains("我会", "我将", "我参加", "i will ", "i am going")
	switch candidateID {
	case "localmind.read#delegate_read":
		localMind := contains("localmind")
		management := contains("状态", "进度", "结果", "取消", "停止", "status", "progress", "result", "cancel", "stop")
		mutation := contains("创建", "新建", "更新", "修改", "编辑", "重命名", "转换", "导出", "删除", "create", "update", "modify", "edit", "rename", "convert", "export", "delete")
		if localMind && !management && !mutation && contains("让", "请", "交给", "委派", "阅读", "总结", "调研", "比较", "回答", "ask", "delegate", "read", "summarize", "research", "compare", "answer") {
			return 0.99
		}
	case "localmind.write#delegate_write":
		if contains("localmind") && contains("创建", "新建", "更新", "修改", "编辑", "重命名", "转换", "导出", "删除", "create", "update", "modify", "edit", "rename", "convert", "export", "delete") {
			return 0.99
		}
	case "localmind.query#query_task":
		if contains("localmind") && contains("状态", "进度", "完成了吗", "结果", "status", "progress", "finished", "result") {
			return 0.99
		}
	case "localmind.cancel#cancel_task":
		if contains("localmind") && contains("取消", "停止", "停掉", "cancel", "stop") {
			return 0.99
		}
	case "coding.agent_manage#read":
		codingTarget := contains("happy", "coding agent", "编码任务", "编码会话", "agent 任务", "agent 会话")
		approvalDecision := contains("批准", "同意计划", "拒绝计划", "approve plan", "reject plan")
		if codingTarget && !approvalDecision && contains("查看", "列出", "为什么", "状态", "消息", "记录", "过程", "show", "list", "why", "status", "messages", "transcript") {
			return 0.98
		}
	case "coding.agent_manage#interact":
		codingTarget := contains("happy", "coding agent", "编码任务", "编码会话", "agent 任务", "agent 会话")
		approvalDecision := contains("批准", "同意计划", "拒绝计划", "approve plan", "reject plan")
		if codingTarget && !approvalDecision && contains("创建", "新建", "启动", "发消息", "发送消息", "停止", "取消", "create", "spawn", "send message", "stop", "cancel") {
			return 0.98
		}
	case "schedule.manage#create":
		if temporal && !scheduleDiscussion && !scheduleStatement && !contains("查看", "列出", "有哪些", "show", "list") && contains("提醒", "告知", "叫我", "跟我说", "通知", "查一下", "查询", "remind", "tell me", "notify", "search") {
			return 0.97
		}
	case "schedule.manage#read":
		scheduleTarget := contains("提醒", "定时任务", "计划任务", "schedule", "scheduled", "reminder")
		if !scheduleDiscussion && scheduleTarget && contains("查看", "列出", "有哪些", "show", "list", "view") {
			return 0.96
		}
	case "schedule.manage#edit":
		scheduleTarget := contains("提醒", "定时任务", "计划任务", "schedule", "reminder")
		if !scheduleDiscussion && scheduleTarget && contains("修改", "改到", "改为", "推迟", "提前", "reschedule", "edit reminder") {
			return 0.97
		}
	case "schedule.manage#delete":
		if !scheduleDiscussion && contains("取消", "删除", "不要再", "cancel", "delete reminder") && contains("提醒", "定时", "任务", "reminder", "schedule") {
			return 0.97
		}
	case "browser.weather#read":
		if contains("天气", "气温", "温度", "下雨", "下雪", "weather", "forecast") && !contains("预警", "新闻", "空气质量", "对比", "比较", "alert", "news", "compare", "air quality") {
			return 0.97
		}
	case "browser.internet_search#search":
		ordinaryWeather := contains("天气", "气温", "温度", "下雨", "下雪", "weather", "forecast") && !contains("预警", "新闻", "空气质量", "对比", "比较", "alert", "news", "air quality", "compare")
		localDocument := contains(".pdf", ".docx", ".xlsx", ".pptx", ".txt", ".md", "本地文件", "工作区", "workspace", "local file")
		browserAction := contains("点击", "点开", "按钮", "当前页面", "当前标签", "页面结构", "chrome 页面", "勾选", "输入", "click", "tap", "button", "current page", "current tab", "page structure", "check", "type")
		conceptual := contains("概念", "是什么意思", "是什么概念", "解释", "what is", "explain")
		if !ordinaryWeather && !localDocument && !browserAction && !conceptual && contains("查一下", "查询一下", "搜索", "联网", "浏览器查询", "最新", "今天", "今日", "现在", "当前", "实时", "最近", "新闻", "价格", "售价", "汇率", "指数", "比分", "在售", "上架", "预警", "空气质量", "对比", "比较", "search", "look up", "online", "current", "latest", "today", "news", "price", "pricing", "exchange rate", "score", "available", "compare") {
			return 0.96
		}
	case "browser.automation#open":
		if contains("打开", "访问", "切换到", "open", "visit", "focus") && !contains("点击", "点开", "输入", "填写", "选择", "勾选", "登录", "认证", "草稿箱", "收件箱", "click", "type", "select", "check", "login", "sign in", "authenticate", "drafts", "inbox") {
			return 0.96
		}
	case "browser.page_read#read":
		readRequest := contains("读取", "阅读", "总结", "提取", "read", "summarize", "extract")
		browserTarget := contains("网页", "网址", "官网", "页面", "http://", "https://", "website", "web page", "official site", "current tab")
		if readRequest && browserTarget && !contains("本地文件", "工作区", "附件", "local file", "workspace", "attached") {
			return 0.97
		}
	case "browser.form_draft#draft":
		draftAction := contains("输入", "填写", "填入", "选择", "type", "fill", "select")
		browserContext := contains("网页", "网站", "页面", "表单", "字段", "输入框", "搜索框", "下拉", "当前标签", "http://", "https://", "browser", "web form", "website", "web page", "field", "textbox", "search box", "dropdown", "current tab")
		commitAction := contains("提交", "发送", "发布", "购买", "付款", "支付", "登录", "验证码", "密码", "submit", "send", "publish", "purchase", "pay", "login", "password", "captcha")
		checkboxClick := contains("勾选", "checkbox", "check the")
		if draftAction && browserContext && !commitAction && !checkboxClick {
			return 0.98
		}
	case "browser.interaction#interact":
		if contains("点击", "点开", "按钮", "勾选", "草稿箱", "收件箱", "click", "tap", "check", "drafts", "inbox") {
			return 0.97
		}
	case "document.read#read":
		documentTarget := contains("附件", "文档", "文件", "图片", "图像", ".pdf", ".docx", ".xlsx", ".pptx", ".txt", ".md", ".png", ".jpg", ".jpeg", "document", "file", "image", "attached")
		readOnly := contains("不要修改", "不修改", "保持不变", "只读", "without changing", "do not change", "read only", "read-only")
		mutation := !readOnly && contains("修改", "编辑", "替换", "润色", "完善", "改写", "扩写", "填入", "填写", "新增", "添加", "插入", "追加", "删除", "移除", "更新", "调整", "edit", "modify", "replace", "rewrite", "expand", "revise", "polish", "improve", "fill", "add", "insert", "append", "delete", "remove", "update")
		explicitRead := contains("读取", "阅读", "查看", "总结", "概括", "解释", "什么内容", "什么文字", "分析", "read", "summarize", "inspect", "explain", "analyze")
		contextualQuestion := contains("routing context (data only") &&
			contains("current-turn governed resources:", "recent agent context:") &&
			contains("什么", "哪些", "注意", "要求", "要点", "告诉我", "回答", "what", "which", "tell me", "answer")
		if documentTarget && (explicitRead || contextualQuestion) && !mutation {
			return 0.96
		}
	case "document.edit#edit":
		readOnly := contains("不要修改", "不修改", "保持不变", "只读", "without changing", "do not change", "read only", "read-only")
		mutation := !readOnly && contains("修改", "编辑", "替换", "改为", "设为", "加粗", "润色", "完善", "改写", "扩写", "填入", "填写", "新增", "添加", "增加", "插入", "追加", "删除", "移除", "更新", "调整", "edit", "modify", "replace", "rewrite", "expand", "revise", "bold", "style", "polish", "improve", "fill", "add", "insert", "append", "delete", "remove", "update")
		browserContext := contains("按钮", "页面", "账户", "网页", "button", "page", "account", "browser")
		fileLifecycle := contains("删除", "移除", "delete", "remove") && !contains("内容", "文字", "文本", "段落", "段", "行", "单元格", "幻灯片", "页面内容", "content", "text", "paragraph", "row", "cell", "slide")
		if mutation && !browserContext && !fileLifecycle && !contains("pdf") {
			return 0.97
		}
	case "document.edit#transform":
		if contains("pdf") && contains("修改", "旋转", "拆分", "调整", "transform", "rotate", "split", "edit") {
			return 0.97
		}
	case "conversation.answer#publish":
		publish := contains("发送", "发出去", "转发", "投递", "send", "forward", "deliver", "publish")
		troubleshooting := contains("为什么", "失败", "没发", "无法发送", "why", "failed", "failure", "cannot send")
		contentWork := contains("总结", "读取", "查看", "修改", "编辑", "分析", "summarize", "read", "inspect", "edit", "analyze")
		browserAction := contains("点击", "按钮", "当前页面", "当前标签", "click", "button", "current page", "current tab")
		if publish && !troubleshooting && !contentWork && !browserAction && !temporal {
			return 0.97
		}
	case "conversation.answer#answer":
		reserved := contains(
			"打开", "点击", "登录", "提醒", "定时", "文件", "文档", "附件", "图片", "图像", "照片", "天气", "气温", "温度", "下雨", "下雪", "预报", "空气质量",
			"新闻", "价格", "金价", "售价", "汇率", "指数", "比分", "现在", "当前", "实时", "最新", "运行", "测试", "代码", "仓库", "项目", "记住", "完善", "修改", "编辑", "扩写",
			"open", "click", "login", "remind", "schedule", "file", "document", "image", "photo", "weather", "forecast", "air quality", "news", "price", "exchange rate", "current", "latest", "run test", "code", "repo", "repository", "project", "remember", "edit", "improve", "expand",
		)
		if !reserved && (contains("你好", "您好", "谢谢", "解释", "概括", "是什么", "为什么", "区别", "hello", "thanks", "explain", "what is", "why") || len([]rune(lower)) <= 12) {
			return 0.95
		}
	}
	return 0
}

func containsAnyTerm(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func normalize(vector []float32) {
	var sum float32
	for _, value := range vector {
		sum += value * value
	}
	if sum == 0 {
		return
	}
	scale := float32(math.Sqrt(float64(sum)))
	for i := range vector {
		vector[i] /= scale
	}
}
