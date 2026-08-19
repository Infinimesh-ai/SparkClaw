package localmind

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
)

const taskProtocolVersion = "localmind.task.v1"

func validateTaskToolContract(tools []mcpclient.Tool) error {
	expected := expectedTaskTools()
	if len(tools) != len(expected) {
		return fmt.Errorf("LocalMind task MCP must expose exactly %d tools, got %d", len(expected), len(tools))
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		contract, ok := expected[tool.Name]
		if !ok {
			return fmt.Errorf("LocalMind task MCP exposed unsupported tool %q", tool.Name)
		}
		if seen[tool.Name] {
			return fmt.Errorf("LocalMind task MCP exposed duplicate tool %q", tool.Name)
		}
		seen[tool.Name] = true
		if err := exactContractValue(tool.Name+" inputSchema", tool.InputSchema, contract.InputSchema); err != nil {
			return err
		}
		if err := exactContractValue(tool.Name+" outputSchema", tool.OutputSchema, taskOutputSchema()); err != nil {
			return err
		}
		if !reflect.DeepEqual(tool.Annotations, contract.Annotations) {
			return fmt.Errorf("LocalMind tool %q annotations do not match the task contract", tool.Name)
		}
	}
	return nil
}

type expectedTaskTool struct {
	InputSchema map[string]any
	Annotations map[string]any
}

func expectedTaskTools() map[string]expectedTaskTool {
	stringField := func(min, max int) map[string]any {
		return map[string]any{"type": "string", "minLength": min, "maxLength": max}
	}
	return map[string]expectedTaskTool{
		delegateRemoteName: {
			InputSchema: contractObject([]string{"request", "idempotencyKey"}, map[string]any{
				"request": stringField(1, 12000),
				"documentIds": map[string]any{
					"type": "array", "items": stringField(1, 256), "maxItems": 20,
				},
				"idempotencyKey": stringField(1, 256),
			}),
			Annotations: contractAnnotations(false, false, false, false),
		},
		getTaskRemoteName: {
			InputSchema: contractObject([]string{"taskId"}, map[string]any{
				"taskId":            stringField(1, 512),
				"knownStateVersion": stringField(1, 128),
				"waitMs": map[string]any{
					"type": "integer", "minimum": 0, "maximum": 30000,
				},
			}),
			Annotations: contractAnnotations(true, false, true, false),
		},
		controlRemoteName: {
			InputSchema: contractObject([]string{"taskId", "action", "idempotencyKey"}, map[string]any{
				"taskId":         stringField(1, 512),
				"action":         map[string]any{"type": "string", "const": "cancel"},
				"idempotencyKey": stringField(1, 256),
				"reason":         stringField(1, 500),
			}),
			Annotations: contractAnnotations(false, true, true, false),
		},
	}
}

func contractObject(required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"type": "object", "properties": properties, "required": required, "additionalProperties": false,
	}
}

func taskOutputSchema() map[string]any {
	return contractObject([]string{"result"}, map[string]any{"result": map[string]any{}})
}

func contractAnnotations(readOnly, destructive, idempotent, openWorld bool) map[string]any {
	return map[string]any{
		"readOnlyHint": readOnly, "destructiveHint": destructive,
		"idempotentHint": idempotent, "openWorldHint": openWorld,
	}
}

func exactContractValue(label string, actual, expected any) error {
	actual = stripContractDocumentation(actual)
	expected = stripContractDocumentation(expected)
	actualJSON, actualErr := json.Marshal(actual)
	expectedJSON, expectedErr := json.Marshal(expected)
	if actualErr != nil || expectedErr != nil || string(actualJSON) != string(expectedJSON) {
		return fmt.Errorf("LocalMind %s does not match the required task contract", label)
	}
	return nil
}

func stripContractDocumentation(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if key == "description" || key == "default" {
				continue
			}
			out[key] = stripContractDocumentation(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = stripContractDocumentation(child)
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = child
		}
		return out
	default:
		return value
	}
}

func taskContractRevision(endpointID string, initialized mcpclient.InitializeResult, tools []mcpclient.Tool) string {
	payload := struct {
		EndpointID      string
		ProtocolVersion string
		ServerInfo      mcpclient.ServerInfo
		Capabilities    map[string]any
		Tools           []mcpclient.Tool
	}{endpointID, initialized.ProtocolVersion, initialized.ServerInfo, initialized.Capabilities, tools}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(errors.New("LocalMind task contract is not JSON serializable"))
	}
	sum := sha256.Sum256(raw)
	return "lmt_" + hex.EncodeToString(sum[:12])
}
