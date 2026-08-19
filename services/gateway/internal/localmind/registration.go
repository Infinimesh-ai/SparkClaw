package localmind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

const maxTaskPollWaitMS = 30_000

func (m *Manager) taskRegistrations(client *mcpclient.Client, snapshot Snapshot) []toolhub.DynamicToolRegistration {
	delegate := toolhub.DynamicToolRegistration{
		Definition: app.ToolDefinition{
			Name: delegateLocalName, Title: "Delegate a LocalMind task",
			Description: "Start one self-contained task in the configured LocalMind workspace.",
			InputSchema: objectSchema([]string{"request"}, map[string]any{
				"request": map[string]any{"type": "string", "minLength": 1, "maxLength": 12000},
				"document_ids": map[string]any{
					"type": "array", "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, "maxItems": 20,
				},
			}),
			Risk: app.RiskDangerous, RequiresApproval: true, Idempotent: true,
			TimeoutMS: m.requestTimeoutMS(), Sandbox: "remote", Audit: "always",
			Capabilities: []app.CapabilityDescriptor{taskCapability(snapshot, "mutation", "delegate")},
			Directory: app.ToolDirectoryMetadata{
				Summary:      "Start one bounded task in the configured LocalMind workspace.",
				WhenToUse:    "Reserved for a later LocalMind workflow plan.",
				WhenNotToUse: "Do not select this tool from an unplanned natural-language workflow.",
				Effects:      []app.ToolEffect{app.ToolEffectExternalInteract, app.ToolEffectWorkspaceWrite},
			},
		},
		RemoteName: delegateRemoteName,
		Execute: func(ctx context.Context, args map[string]any, sessionID, runID string) (toolhub.Result, error) {
			return m.executeDelegate(ctx, client, snapshot, args, sessionID, runID)
		},
	}
	getTask := toolhub.DynamicToolRegistration{
		Definition: app.ToolDefinition{
			Name: getTaskLocalName, Title: "Get a LocalMind task",
			Description: "Read or long-poll the current state of one LocalMind task.",
			InputSchema: objectSchema([]string{"task_id"}, map[string]any{
				"task_id":             map[string]any{"type": "string", "minLength": 1, "maxLength": 512},
				"known_state_version": map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"wait_ms":             map[string]any{"type": "integer", "minimum": 0, "maximum": maxTaskPollWaitMS},
			}),
			Risk: app.RiskRead, Idempotent: true, TimeoutMS: m.longPollTimeoutMS(maxTaskPollWaitMS), Sandbox: "remote", Audit: "always",
			Capabilities: []app.CapabilityDescriptor{taskCapability(snapshot, "read", "get")},
			Directory: app.ToolDirectoryMetadata{
				Summary:      "Read or long-poll one known LocalMind task.",
				WhenToUse:    "Reserved for a later LocalMind workflow plan.",
				WhenNotToUse: "Do not use to start work or discover workspace content.",
				Effects:      []app.ToolEffect{app.ToolEffectExternalRead},
			},
		},
		RemoteName: getTaskRemoteName,
		Execute: func(ctx context.Context, args map[string]any, _, _ string) (toolhub.Result, error) {
			return m.executeGetTask(ctx, client, args)
		},
	}
	cancelTask := toolhub.DynamicToolRegistration{
		Definition: app.ToolDefinition{
			Name: cancelLocalName, Title: "Cancel a LocalMind task",
			Description: "Cancel one unfinished LocalMind task.",
			InputSchema: objectSchema([]string{"task_id"}, map[string]any{
				"task_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 512},
				"reason":  map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
			}),
			Risk: app.RiskDangerous, RequiresApproval: true, Idempotent: true,
			TimeoutMS: m.requestTimeoutMS(), Sandbox: "remote", Audit: "always",
			Capabilities: []app.CapabilityDescriptor{taskCapability(snapshot, "mutation", "cancel")},
			Directory: app.ToolDirectoryMetadata{
				Summary:      "Cancel one explicitly identified unfinished LocalMind task.",
				WhenToUse:    "Reserved for a later LocalMind workflow plan.",
				WhenNotToUse: "Do not use for approval, rejection, retry, or resume.",
				Effects:      []app.ToolEffect{app.ToolEffectExternalInteract},
			},
		},
		RemoteName: controlRemoteName,
		Execute: func(ctx context.Context, args map[string]any, sessionID, runID string) (toolhub.Result, error) {
			return m.executeCancelTask(ctx, client, snapshot, args, sessionID, runID)
		},
	}
	return []toolhub.DynamicToolRegistration{delegate, getTask, cancelTask}
}

func taskCapability(snapshot Snapshot, mode, operation string) app.CapabilityDescriptor {
	return app.CapabilityDescriptor{
		Name: app.ToolCapabilityExternalMCPWorkspace,
		Qualifiers: map[string]string{
			app.CapabilityQualifierProvider:         app.CapabilityProviderLocalMind,
			app.CapabilityQualifierMode:             mode,
			app.CapabilityQualifierOperation:        operation,
			app.CapabilityQualifierEndpointID:       snapshot.EndpointID,
			app.CapabilityQualifierSnapshotRevision: snapshot.Revision,
		},
	}
}

func (m *Manager) executeDelegate(ctx context.Context, client *mcpclient.Client, snapshot Snapshot, args map[string]any, sessionID, runID string) (toolhub.Result, error) {
	request := strings.TrimSpace(stringValue(args["request"]))
	if request == "" || utf8.RuneCountInString(request) > 12000 {
		return toolhub.Result{}, errors.New("LocalMind request must contain between 1 and 12000 characters")
	}
	documentIDs, err := parseDocumentIDs(args["document_ids"])
	if err != nil {
		return toolhub.Result{}, err
	}
	key := deterministicTaskKey(snapshot.EndpointID, sessionID, runID, "delegate", request, strings.Join(documentIDs, "\x00"))
	return m.callTaskTool(ctx, client, delegateRemoteName, map[string]any{
		"request": request, "documentIds": documentIDs, "idempotencyKey": key,
	}, "")
}

func (m *Manager) executeGetTask(ctx context.Context, client *mcpclient.Client, args map[string]any) (toolhub.Result, error) {
	taskID := strings.TrimSpace(stringValue(args["task_id"]))
	if taskID == "" {
		return toolhub.Result{}, errors.New("LocalMind task_id is required")
	}
	remoteArgs := map[string]any{"taskId": taskID}
	if value := strings.TrimSpace(stringValue(args["known_state_version"])); value != "" {
		remoteArgs["knownStateVersion"] = value
	}
	waitMS := 0
	if args["wait_ms"] != nil {
		value, ok := integerValue(args["wait_ms"])
		if !ok || value < 0 || value > maxTaskPollWaitMS {
			return toolhub.Result{}, errors.New("LocalMind wait_ms must be between 0 and 30000")
		}
		waitMS = value
		remoteArgs["waitMs"] = value
	}
	timeout := time.Duration(m.longPollTimeoutMS(waitMS)) * time.Millisecond
	remoteResult, err := client.CallToolWithTimeout(ctx, getTaskRemoteName, remoteArgs, timeout)
	return m.finishTaskCall(remoteResult, err, getTaskRemoteName, taskID)
}

func (m *Manager) executeCancelTask(ctx context.Context, client *mcpclient.Client, snapshot Snapshot, args map[string]any, sessionID, runID string) (toolhub.Result, error) {
	taskID := strings.TrimSpace(stringValue(args["task_id"]))
	reason := strings.TrimSpace(stringValue(args["reason"]))
	if taskID == "" {
		return toolhub.Result{}, errors.New("LocalMind task_id is required")
	}
	remoteArgs := map[string]any{
		"taskId": taskID, "action": "cancel",
		"idempotencyKey": deterministicTaskKey(snapshot.EndpointID, sessionID, runID, "cancel", taskID, reason),
	}
	if reason != "" {
		remoteArgs["reason"] = reason
	}
	return m.callTaskTool(ctx, client, controlRemoteName, remoteArgs, taskID)
}

func (m *Manager) callTaskTool(ctx context.Context, client *mcpclient.Client, remoteName string, args map[string]any, expectedTaskID string) (toolhub.Result, error) {
	remoteResult, err := client.CallTool(ctx, remoteName, args)
	return m.finishTaskCall(remoteResult, err, remoteName, expectedTaskID)
}

func (m *Manager) finishTaskCall(remoteResult mcpclient.ToolResult, callErr error, remoteName, expectedTaskID string) (toolhub.Result, error) {
	if callErr != nil {
		return toolhub.Result{}, m.safeCurrentError(callErr)
	}
	projected := m.projectToolResult(remoteResult, remoteName)
	if remoteResult.IsError {
		return projected, remoteToolResultError(remoteName, remoteResult)
	}
	if _, err := parseTaskState(remoteResult, expectedTaskID); err != nil {
		return projected, err
	}
	return projected, nil
}

type taskState struct {
	TaskID       string
	StateVersion string
	Status       string
	Terminal     bool
}

func parseTaskState(result mcpclient.ToolResult, expectedTaskID string) (taskState, error) {
	value, ok := result.StructuredContent["result"]
	if !ok {
		return taskState{}, errors.New("LocalMind task result omitted structuredContent.result")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return taskState{}, errors.New("LocalMind task result is not JSON serializable")
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return taskState{}, errors.New("LocalMind task result is malformed")
	}
	protocol := strings.TrimSpace(stringValue(fields["protocolVersion"]))
	taskID := strings.TrimSpace(stringValue(fields["taskId"]))
	stateVersion := strings.TrimSpace(stringValue(fields["stateVersion"]))
	status := strings.TrimSpace(stringValue(fields["status"]))
	terminal, terminalPresent := fields["terminal"].(bool)
	if protocol != taskProtocolVersion || taskID == "" || stateVersion == "" || status == "" || !terminalPresent {
		return taskState{}, errors.New("LocalMind task result does not match localmind.task.v1")
	}
	if expectedTaskID != "" && taskID != expectedTaskID {
		return taskState{}, errors.New("LocalMind task result changed taskId")
	}
	return taskState{TaskID: taskID, StateVersion: stateVersion, Status: status, Terminal: terminal}, nil
}

func remoteToolResultError(name string, result mcpclient.ToolResult) error {
	return &app.CodedToolError{
		Code: app.ToolErrorMCPToolResult,
		Err:  errors.New("LocalMind " + name + " returned isError: " + safeToolErrorText(toolResultText(result))),
	}
}

func deterministicTaskKey(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return "sparkclaw:v1:" + hex.EncodeToString(hash.Sum(nil))
}

func parseDocumentIDs(value any) ([]string, error) {
	if value == nil {
		return []string{}, nil
	}
	items := []any{}
	switch typed := value.(type) {
	case []any:
		items = typed
	case []string:
		for _, item := range typed {
			items = append(items, item)
		}
	default:
		return nil, errors.New("LocalMind document_ids must be an array")
	}
	if len(items) > 20 {
		return nil, errors.New("LocalMind document_ids exceeds 20 items")
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		id, ok := item.(string)
		id = strings.TrimSpace(id)
		if !ok || id == "" || utf8.RuneCountInString(id) > 256 {
			return nil, errors.New("LocalMind document_ids contains an invalid document ID")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (m *Manager) safeCurrentError(err error) error {
	runtime, runtimeErr := m.runtimeConfig()
	if runtimeErr != nil {
		return runtimeErr
	}
	return safeError(err, runtime)
}

func safeError(err error, runtime resolvedRuntime) error {
	if err == nil {
		return nil
	}
	var httpErr *mcpclient.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden {
			return &app.CodedToolError{Code: app.ToolErrorMCPAuthorization, Err: errors.New("LocalMind authentication or workspace authorization failed")}
		}
		return fmt.Errorf("LocalMind MCP HTTP request failed with status %d", httpErr.StatusCode)
	}
	message := strings.ReplaceAll(err.Error(), runtime.token, "[REDACTED]")
	message = strings.ReplaceAll(message, runtime.endpoint, "[REDACTED_ENDPOINT]")
	return errors.New(boundedText(message, 1000))
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	return map[string]any{"type": "object", "required": required, "properties": properties, "additionalProperties": false}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func integerValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), int64(int(typed)) == typed
	case float64:
		integer := int(typed)
		return integer, float64(integer) == typed
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil && int64(int(parsed)) == parsed
	default:
		return 0, false
	}
}

func boundedText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes]) + "..."
}

func (m *Manager) requestTimeoutMS() int {
	seconds := m.cfg.RequestTimeoutSeconds
	if seconds <= 0 {
		seconds = 30
	}
	return seconds * 1000
}

func (m *Manager) longPollTimeoutMS(waitMS int) int {
	timeoutMS := waitMS + m.cfg.LongCallGraceSeconds*1000
	if timeoutMS < m.requestTimeoutMS() {
		return m.requestTimeoutMS()
	}
	return timeoutMS
}
