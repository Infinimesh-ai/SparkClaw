package mcpaccess

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const conversationToolName = "sparkclaw.conversation.send"

func conversationTool() Tool {
	locator := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "maxLength": 4096},
			"name":    map[string]any{"type": "string", "maxLength": 255},
			"query":   map[string]any{"type": "string", "maxLength": 255},
			"caption": map[string]any{"type": "string", "maxLength": 2000},
		},
		"oneOf": []any{
			map[string]any{"required": []string{"path"}},
			map[string]any{"required": []string{"name"}},
			map[string]any{"required": []string{"query"}},
		},
		"additionalProperties": false,
	}
	return Tool{
		Name:        conversationToolName,
		Title:       "Send a SparkClaw conversation message",
		Description: "Send ordinary text and existing workspace media to the linked SparkClaw conversation.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":  map[string]any{"type": "string", "maxLength": 65536},
				"media": map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "items": locator},
			},
			"anyOf": []any{
				map[string]any{"required": []string{"text"}},
				map[string]any{"required": []string{"media"}},
			},
			"additionalProperties": false,
		},
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false},
	}
}

func operationTools() []Tool {
	tool := func(name, description string) Tool {
		return Tool{Name: name, Description: description, InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"operation_id": map[string]any{"type": "string"}}, "required": []string{"operation_id"}, "additionalProperties": false,
		}, Annotations: map[string]any{"readOnlyHint": name != "sparkclaw.operation.cancel", "destructiveHint": false}}
	}
	return []Tool{
		tool("sparkclaw.operation.get", "Return durable state for one operation owned by this MCP binding."),
		tool("sparkclaw.operation.result", "Return the bounded terminal result for one operation when ready."),
		tool("sparkclaw.operation.cancel", "Request cancellation of one operation without promising rollback."),
	}
}

func conversationArguments(arguments map[string]any) (app.MCPConversationRequest, error) {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return app.MCPConversationRequest{}, errors.New("conversation arguments are invalid")
	}
	var input struct {
		Text  string                    `json:"text"`
		Media []app.MessageMediaLocator `json:"media"`
	}
	if err := strictJSON(raw, &input); err != nil {
		return app.MCPConversationRequest{}, errors.New("conversation arguments do not match the fixed schema")
	}
	input.Text = strings.TrimSpace(input.Text)
	if len(input.Text) > 65536 || len(input.Media) > 8 {
		return app.MCPConversationRequest{}, errors.New("conversation argument exceeds its size limit")
	}
	if input.Text == "" && len(input.Media) == 0 {
		return app.MCPConversationRequest{}, errors.New("text or media is required")
	}
	for index := range input.Media {
		if err := validateMediaLocator(&input.Media[index]); err != nil {
			return app.MCPConversationRequest{}, fmt.Errorf("media item %d: %w", index, err)
		}
	}
	return app.MCPConversationRequest{Text: input.Text, Media: append([]app.MessageMediaLocator(nil), input.Media...)}, nil
}

func validateMediaLocator(locator *app.MessageMediaLocator) error {
	if locator == nil {
		return errors.New("locator is required")
	}
	locator.Path = strings.TrimSpace(strings.ReplaceAll(locator.Path, "\\", "/"))
	locator.Name = strings.TrimSpace(locator.Name)
	locator.Query = strings.TrimSpace(locator.Query)
	locator.Caption = strings.TrimSpace(locator.Caption)
	count := 0
	for _, value := range []string{locator.Path, locator.Name, locator.Query} {
		if value != "" {
			count++
		}
	}
	if count != 1 {
		return errors.New("exactly one of path, name, or query is required")
	}
	if len(locator.Path) > 4096 || len(locator.Name) > 255 || len(locator.Query) > 255 || len(locator.Caption) > 2000 {
		return errors.New("locator exceeds its size limit")
	}
	if strings.ContainsRune(locator.Path+locator.Name+locator.Query, 0) {
		return errors.New("locator contains a NUL byte")
	}
	if locator.Path != "" {
		cleaned := path.Clean(locator.Path)
		if strings.HasPrefix(cleaned, "/") || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(locator.Path, "://") {
			return errors.New("path must be workspace-relative")
		}
		locator.Path = cleaned
	}
	if locator.Name != "" {
		if locator.Name != path.Base(locator.Name) || locator.Name == "." || locator.Name == ".." || strings.Contains(locator.Name, "://") {
			return errors.New("name must be a complete base filename")
		}
	}
	if locator.Query != "" && strings.Contains(locator.Query, "://") {
		return errors.New("query must be owner-authored filename text")
	}
	return nil
}
