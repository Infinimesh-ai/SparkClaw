package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func parseReActOutput(content string, visible []app.ToolDefinition) (reactOutput, error) {
	raw := extractJSONObject(content)
	if raw == "" {
		return reactOutput{}, fmt.Errorf("react output is not a JSON object")
	}
	var envelope struct {
		Type      string         `json:"type"`
		Tool      string         `json:"tool"`
		Arguments map[string]any `json:"arguments"`
		Reason    string         `json:"reason"`
		Answer    string         `json:"answer"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return reactOutput{}, fmt.Errorf("react output JSON parse failed: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(envelope.Type)) {
	case "action":
		tool := strings.TrimSpace(envelope.Tool)
		if tool == "" {
			return reactOutput{}, fmt.Errorf("react action missing tool")
		}
		if !toolVisible(tool, visible) {
			return reactOutput{}, fmt.Errorf("tool_not_visible: %s", tool)
		}
		if envelope.Arguments == nil {
			envelope.Arguments = map[string]any{}
		}
		return reactOutput{
			Kind: "action",
			Action: reactAction{
				Type:      "action",
				Tool:      tool,
				Arguments: envelope.Arguments,
				Reason:    strings.TrimSpace(envelope.Reason),
			},
		}, nil
	case "final":
		answer := strings.TrimSpace(envelope.Answer)
		if answer == "" {
			return reactOutput{}, fmt.Errorf("react final missing answer")
		}
		return reactOutput{
			Kind: "final",
			Final: reactFinal{
				Type:   "final",
				Answer: answer,
			},
		}, nil
	default:
		return reactOutput{}, fmt.Errorf("react output type must be action or final")
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
