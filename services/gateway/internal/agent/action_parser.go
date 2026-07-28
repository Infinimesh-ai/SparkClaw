package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func parseWorkflowStepOutput(content string, visible []app.ToolDefinition) (workflowStepOutput, error) {
	raw := extractJSONObject(content)
	if raw == "" {
		return workflowStepOutput{}, fmt.Errorf("workflow step output is not a JSON object")
	}
	var envelope struct {
		Type      string         `json:"type"`
		Tool      string         `json:"tool"`
		Arguments map[string]any `json:"arguments"`
		Reason    string         `json:"reason"`
		Answer    string         `json:"answer"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return workflowStepOutput{}, fmt.Errorf("workflow step output JSON parse failed: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(envelope.Type)) {
	case "action":
		tool := strings.TrimSpace(envelope.Tool)
		if tool == "" {
			return workflowStepOutput{}, fmt.Errorf("workflow step action missing tool")
		}
		if !toolVisible(tool, visible) {
			return workflowStepOutput{}, fmt.Errorf("tool_not_visible: %s", tool)
		}
		if envelope.Arguments == nil {
			envelope.Arguments = map[string]any{}
		}
		return workflowStepOutput{
			Kind: "action",
			Action: workflowStepAction{
				Type:      "action",
				Tool:      tool,
				Arguments: envelope.Arguments,
				Reason:    strings.TrimSpace(envelope.Reason),
			},
		}, nil
	case "final":
		answer := strings.TrimSpace(envelope.Answer)
		if answer == "" {
			return workflowStepOutput{}, fmt.Errorf("workflow step final missing answer")
		}
		return workflowStepOutput{
			Kind: "final",
			Final: workflowStepFinal{
				Type:   "final",
				Answer: answer,
			},
		}, nil
	default:
		return workflowStepOutput{}, fmt.Errorf("workflow step output type must be action or final")
	}
}

func toolVisible(name string, visible []app.ToolDefinition) bool {
	for _, def := range visible {
		if def.Name == name {
			return true
		}
	}
	return false
}

func extractJSONObject(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	if strings.HasPrefix(content, "{") && strings.HasSuffix(content, "}") {
		return content
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		return strings.TrimSpace(content[start : end+1])
	}
	return ""
}

func extractJSONObjects(content string) []string {
	objects := []string{}
	start := -1
	depth := 0
	inString := false
	escaped := false
	for index := 0; index < len(content); index++ {
		char := content[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			if depth > 0 {
				inString = true
			}
		case '{':
			if depth == 0 {
				start = index
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				objects = append(objects, strings.TrimSpace(content[start:index+1]))
				start = -1
			}
		}
	}
	return objects
}
