package modelrouter

import (
	"encoding/json"
	"regexp"
	"strings"
)

// This is an explicit-pair fixture for the mock provider. Routing and file/path
// authority have already been fixed by the real workflow before this stage.
func mockTextReplaceWorkflowAction(prompt, goal string) (string, bool) {
	if mockVisibleToolWithRemoteName(strings.ToLower(prompt), "text.replace_text") != "text.replace_text" {
		return "", false
	}
	find, replacement, ok := mockExplicitTextReplacement(goal)
	if !ok {
		return "", false
	}
	readEvidence, alreadyActed := mockTextReadEvidence(prompt)
	if alreadyActed || !strings.Contains(readEvidence, find) {
		return "", false
	}
	return mockWorkflowStepAction("text.replace_text", map[string]any{
		"path": "workflow-bound text input", "output_path": "workflow-bound text output",
		"replacements": []map[string]string{{"find": find, "replace": replacement}},
	}), true
}

var mockTextReplacementPatterns = func() []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, 0, 10)
	for _, quotes := range [][2]string{{"“", "”"}, {"‘", "’"}, {"「", "」"}, {`"`, `"`}, {"'", "'"}} {
		open, close := regexp.QuoteMeta(quotes[0]), regexp.QuoteMeta(quotes[1])
		quoted := open + "([^" + close + "\\r\\n]+)" + close
		patterns = append(patterns,
			regexp.MustCompile(quoted+`\s*(?:替换为|替换成|改为)\s*`+quoted),
			regexp.MustCompile(`(?i)\breplace\s+`+quoted+`\s+with\s+`+quoted),
		)
	}
	return patterns
}()

func mockExplicitTextReplacement(goal string) (string, string, bool) {
	lower := strings.ToLower(goal)
	for _, negative := range []string{"不要", "不得", "禁止", "别把", "do not", "don't", "never"} {
		if strings.Contains(lower, negative) {
			return "", "", false
		}
	}
	var find, replacement string
	matches := 0
	for _, pattern := range mockTextReplacementPatterns {
		for _, match := range pattern.FindAllStringSubmatch(goal, -1) {
			matches++
			find, replacement = match[1], match[2]
		}
	}
	return find, replacement, matches == 1 && find != replacement && len(find) <= 4096 && len(replacement) <= 4096
}

// Only successful read payloads and provisioned files.read evidence supply the
// source text. The owner goal, tool descriptions and failed observations cannot
// manufacture read evidence. A completed/pending mutation is never proposed twice.
func mockTextReadEvidence(prompt string) (string, bool) {
	var evidence strings.Builder
	observations := ""
	if at := strings.Index(prompt, "Previous observation summaries / tool result messages"); at >= 0 {
		observations = prompt[at:]
		for _, marker := range []string{"\n\nsource=", "\n\nWorkflow execution instruction:", "\n\nModel-visible tools this workflow stage:", "\n\nWorkflow step output contract:"} {
			if end := strings.Index(observations, marker); end >= 0 {
				observations = observations[:end]
			}
		}
	}
	for _, line := range strings.Split(observations, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- {") {
			continue
		}
		var message struct {
			Role     string `json:"role"`
			Tool     string `json:"tool"`
			Status   string `json:"status"`
			Evidence []struct {
				Text string `json:"text"`
			} `json:"evidence"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "- ")), &message) != nil || message.Role != "tool" {
			continue
		}
		if message.Tool == "text.replace_text" {
			return "", true
		}
		if message.Tool != "files.read" || (message.Status != "completed" && message.Status != "completed_after_approval") {
			continue
		}
		for _, item := range message.Evidence {
			evidence.WriteString(item.Text)
			evidence.WriteByte('\n')
		}
	}
	// Provisioned evidence is emitted by the workflow as a bounded source block,
	// separate from previous observation messages and subsequent instructions.
	remaining := prompt
	for {
		at := strings.Index(remaining, "\n\nsource=")
		if at < 0 {
			break
		}
		remaining = remaining[at+2:]
		headerEnd := strings.IndexByte(remaining, '\n')
		if headerEnd < 0 {
			break
		}
		header := remaining[:headerEnd]
		body := remaining[headerEnd+1:]
		end := len(body)
		for _, marker := range []string{"\n\nsource=", "\n\nWorkflow execution instruction:", "\n\nModel-visible tools this workflow stage:", "\n\nWorkflow step output contract:"} {
			if i := strings.Index(body, marker); i >= 0 && i < end {
				end = i
			}
		}
		if strings.Contains(header, " tool=files.read mode=") {
			evidence.WriteString(body[:end])
			evidence.WriteByte('\n')
		}
		remaining = body[end:]
	}
	return evidence.String(), false
}
