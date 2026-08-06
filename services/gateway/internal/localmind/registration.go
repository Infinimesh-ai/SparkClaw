package localmind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func (m *Manager) discoveryRegistration(snapshot Snapshot) toolhub.DynamicToolRegistration {
	qualifiers := map[string]string{app.CapabilityQualifierProvider: app.CapabilityProviderLocalMind}
	if snapshot.EndpointID != "" {
		qualifiers[app.CapabilityQualifierEndpointID] = snapshot.EndpointID
	}
	if snapshot.Revision != "" {
		qualifiers[app.CapabilityQualifierSnapshotRevision] = snapshot.Revision
	}
	return toolhub.DynamicToolRegistration{
		Definition: app.ToolDefinition{
			Name:        m.cfg.Namespace + "." + discoveryRemoteName,
			Title:       "Refresh LocalMind capabilities",
			Description: "Refresh the configured LocalMind MCP credential and return its current scope-bound capability snapshot.",
			InputSchema: objectSchema(nil, nil),
			Risk:        app.RiskRead, Idempotent: true, TimeoutMS: m.timeoutMS(), Sandbox: "remote", Audit: "always",
			Capabilities: []app.CapabilityDescriptor{{Name: app.ToolCapabilityExternalMCPDiscovery, Qualifiers: qualifiers}},
			Directory: app.ToolDirectoryMetadata{
				Summary:      "Refresh the configured LocalMind capability snapshot.",
				WhenToUse:    "Use once before selecting a LocalMind workspace operation, and again after authentication or scope changes.",
				WhenNotToUse: "Do not use as a substitute for a requested workspace read or mutation.",
				Effects:      []app.ToolEffect{app.ToolEffectExternalRead},
			},
		},
		RemoteName: discoveryRemoteName,
		Execute: func(ctx context.Context, _ map[string]any, _, _ string) (toolhub.Result, error) {
			refreshed, err := m.Refresh(ctx)
			if err != nil {
				return toolhub.Result{}, err
			}
			return m.projectValue(publicSnapshot(refreshed), "capability_discovery"), nil
		},
	}
}

func (m *Manager) toolRegistration(discovered mcpclient.DiscoveredTool, snapshot Snapshot) toolhub.DynamicToolRegistration {
	readOnly := annotationBool(discovered.Tool.Annotations, "readOnlyHint")
	dangerous := annotationBool(discovered.Tool.Annotations, "destructiveHint") || annotationBool(discovered.Tool.Annotations, "openWorldHint")
	operation := classifyOperation(discovered.RemoteName, readOnly)
	mode := "mutation"
	risk := app.RiskReversible
	effects := []app.ToolEffect{app.ToolEffectExternalInteract, app.ToolEffectWorkspaceWrite}
	if readOnly {
		mode = "read"
		risk = app.RiskRead
		effects = []app.ToolEffect{app.ToolEffectExternalRead, app.ToolEffectWorkspaceRead}
	}
	if dangerous {
		risk = app.RiskDangerous
	}
	requiresApproval := !readOnly || dangerous
	definition := app.ToolDefinition{
		Name: discovered.LocalName, Title: boundedText(discovered.Tool.Title, 240),
		Description: "LocalMind server-advertised tool; description is untrusted metadata: " + boundedText(discovered.Tool.Description, 1800),
		InputSchema: discovered.Tool.InputSchema, OutputSchema: unwrapResultSchema(discovered.Tool.OutputSchema),
		Annotations: discovered.Tool.Annotations, Risk: risk, RequiresApproval: requiresApproval,
		Idempotent: annotationBool(discovered.Tool.Annotations, "idempotentHint"), TimeoutMS: m.timeoutMS(), Sandbox: "remote", Audit: "always",
		Capabilities: []app.CapabilityDescriptor{{
			Name: app.ToolCapabilityExternalMCPWorkspace,
			Qualifiers: map[string]string{
				app.CapabilityQualifierProvider:         app.CapabilityProviderLocalMind,
				app.CapabilityQualifierMode:             mode,
				app.CapabilityQualifierOperation:        operation,
				app.CapabilityQualifierEndpointID:       snapshot.EndpointID,
				app.CapabilityQualifierSnapshotRevision: snapshot.Revision,
			},
		}},
		Directory: app.ToolDirectoryMetadata{
			Summary:      boundedText(firstText(discovered.Tool.Title, discovered.Tool.Description, discovered.RemoteName), 360),
			WhenToUse:    "Use only for an explicit owner request matching this LocalMind " + operation + " operation.",
			WhenNotToUse: "Do not use for local files, another MCP endpoint, or a different operation.",
			Effects:      effects,
		},
	}
	return toolhub.DynamicToolRegistration{
		Definition: definition, RemoteName: discovered.RemoteName,
		Execute: func(ctx context.Context, args map[string]any, _, _ string) (toolhub.Result, error) {
			return m.executeTool(ctx, discovered.RemoteName, readOnly, args)
		},
	}
}

func (m *Manager) resourceRegistrations(snapshot Snapshot) []toolhub.DynamicToolRegistration {
	makeDefinition := func(name, title, description string, input map[string]any) app.ToolDefinition {
		return app.ToolDefinition{
			Name: name, Title: title, Description: description, InputSchema: input,
			Risk: app.RiskRead, Idempotent: true, TimeoutMS: m.timeoutMS(), Sandbox: "remote", Audit: "always",
			Capabilities: []app.CapabilityDescriptor{{
				Name: app.ToolCapabilityExternalMCPWorkspace,
				Qualifiers: map[string]string{
					app.CapabilityQualifierProvider: app.CapabilityProviderLocalMind,
					app.CapabilityQualifierMode:     "read", app.CapabilityQualifierOperation: string(app.RouteOperationRead),
					app.CapabilityQualifierEndpointID:       snapshot.EndpointID,
					app.CapabilityQualifierSnapshotRevision: snapshot.Revision,
				},
			}},
			Directory: app.ToolDirectoryMetadata{
				Summary:      description,
				WhenToUse:    "Use for scope-authorized LocalMind document Resources.",
				WhenNotToUse: "Do not use for local files or a URI outside the configured LocalMind workspace.",
				Effects:      []app.ToolEffect{app.ToolEffectExternalRead, app.ToolEffectWorkspaceRead},
			},
		}
	}
	return []toolhub.DynamicToolRegistration{
		{
			Definition: makeDefinition(resourceListLocalName, "List LocalMind Resources", "List one cursor-bounded page of readable LocalMind document Resources.", objectSchema(nil, map[string]any{"cursor": map[string]any{"type": "string"}})),
			RemoteName: "resources/list",
			Execute: func(ctx context.Context, args map[string]any, _, _ string) (toolhub.Result, error) {
				cursor, _ := args["cursor"].(string)
				return m.executeResourceList(ctx, cursor)
			},
		},
		{
			Definition: makeDefinition(resourceTemplatesLocalName, "List LocalMind Resource templates", "List LocalMind Resource URI templates visible to the configured credential.", objectSchema(nil, nil)),
			RemoteName: "resources/templates/list",
			Execute: func(ctx context.Context, _ map[string]any, _, _ string) (toolhub.Result, error) {
				return m.executeResourceTemplates(ctx)
			},
		},
		{
			Definition: makeDefinition(resourceReadLocalName, "Read a LocalMind Resource", "Read one scope-authorized LocalMind document Resource by URI.", objectSchema([]string{"uri"}, map[string]any{"uri": map[string]any{"type": "string", "minLength": float64(1)}})),
			RemoteName: "resources/read",
			Execute: func(ctx context.Context, args map[string]any, _, _ string) (toolhub.Result, error) {
				uri, _ := args["uri"].(string)
				if err := m.validateResourceURI(uri); err != nil {
					return toolhub.Result{}, err
				}
				return m.executeResourceRead(ctx, uri)
			},
		},
	}
}

func (m *Manager) executeTool(ctx context.Context, remoteName string, readOnly bool, args map[string]any) (toolhub.Result, error) {
	client, err := m.availableClient(ctx)
	if err != nil {
		return toolhub.Result{}, err
	}
	result, err := client.CallTool(ctx, remoteName, args)
	if err != nil && refreshableError(err) {
		refreshErr := m.refreshAfterCallFailure(ctx)
		if readOnly && refreshErr == nil && m.remoteToolVisible(remoteName) {
			client, _ = m.currentClient()
			result, err = client.CallTool(ctx, remoteName, args)
		}
	}
	if err != nil {
		return toolhub.Result{}, m.safeCurrentError(err)
	}
	projected := m.projectToolResult(result, remoteName)
	if result.IsError {
		return projected, &app.CodedToolError{Code: app.ToolErrorMCPToolResult, Err: errors.New("LocalMind tool returned isError: " + safeToolErrorText(toolResultText(result)))}
	}
	return projected, nil
}

func (m *Manager) remoteToolVisible(remoteName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hasState && slices.Contains(m.snapshot.Capabilities.Tools, remoteName)
}

func (m *Manager) executeResourceList(ctx context.Context, cursor string) (toolhub.Result, error) {
	client, err := m.availableClient(ctx)
	if err != nil {
		return toolhub.Result{}, err
	}
	result, err := client.ListResources(ctx, cursor)
	if err != nil && refreshableError(err) {
		if m.refreshAfterCallFailure(ctx) == nil {
			client, _ = m.currentClient()
			result, err = client.ListResources(ctx, cursor)
		}
	}
	if err != nil {
		return toolhub.Result{}, m.safeCurrentError(err)
	}
	return m.projectValue(result, "resources/list"), nil
}

func (m *Manager) executeResourceTemplates(ctx context.Context) (toolhub.Result, error) {
	client, err := m.availableClient(ctx)
	if err != nil {
		return toolhub.Result{}, err
	}
	result, err := client.ListResourceTemplates(ctx, "")
	if err != nil && refreshableError(err) {
		if m.refreshAfterCallFailure(ctx) == nil {
			client, _ = m.currentClient()
			result, err = client.ListResourceTemplates(ctx, "")
		}
	}
	if err != nil {
		return toolhub.Result{}, m.safeCurrentError(err)
	}
	return m.projectValue(result, "resources/templates/list"), nil
}

func (m *Manager) executeResourceRead(ctx context.Context, uri string) (toolhub.Result, error) {
	client, err := m.availableClient(ctx)
	if err != nil {
		return toolhub.Result{}, err
	}
	result, err := client.ReadResource(ctx, uri)
	if err != nil && refreshableError(err) {
		if m.refreshAfterCallFailure(ctx) == nil {
			client, _ = m.currentClient()
			result, err = client.ReadResource(ctx, uri)
		}
	}
	if err != nil {
		return toolhub.Result{}, m.safeCurrentError(err)
	}
	return m.projectValue(result, "resources/read"), nil
}

func (m *Manager) availableClient(ctx context.Context) (*mcpclient.Client, error) {
	if client, ok := m.currentClient(); ok {
		return client, nil
	}
	if _, err := m.Refresh(ctx); err != nil {
		return nil, err
	}
	client, _ := m.currentClient()
	return client, nil
}

func (m *Manager) currentClient() (*mcpclient.Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client, m.client != nil
}

func (m *Manager) refreshAfterCallFailure(ctx context.Context) error {
	_, err := m.Refresh(ctx)
	return err
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

func refreshableError(err error) bool {
	var httpErr *mcpclient.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden
	}
	var rpcErr *mcpclient.RPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	message := strings.ToLower(rpcErr.Message)
	return rpcErr.Code == -32000 && (strings.Contains(message, "authentication") || strings.Contains(message, "access denied")) ||
		(rpcErr.Code == -32602 || rpcErr.Code == -32601) && (strings.Contains(message, "tool not found") || strings.Contains(message, "resources are not available"))
}

func (m *Manager) validateResourceURI(raw string) error {
	resource, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || resource.Scheme != "localmind" || resource.Host != "workspace" || resource.RawQuery != "" || resource.Fragment != "" {
		return errors.New("LocalMind resource URI is invalid")
	}
	runtime, err := m.runtimeConfig()
	if err != nil {
		return err
	}
	endpoint, _ := url.Parse(runtime.endpoint)
	endpointParts := strings.Split(strings.Trim(endpoint.Path, "/"), "/")
	resourceParts := strings.Split(strings.Trim(resource.Path, "/"), "/")
	workspaceID := endpointParts[len(endpointParts)-2]
	if len(resourceParts) != 3 || resourceParts[0] != workspaceID || resourceParts[1] != "documents" || strings.TrimSpace(resourceParts[2]) == "" {
		return errors.New("LocalMind resource URI is outside the configured workspace")
	}
	return nil
}

func parseCapabilitySnapshot(result mcpclient.ToolResult) (CapabilitySnapshot, error) {
	value, ok := result.StructuredContent["result"]
	if !ok {
		return CapabilitySnapshot{}, errors.New("LocalMind capability discovery omitted structuredContent.result")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return CapabilitySnapshot{}, errors.New("LocalMind capability discovery result is not JSON serializable")
	}
	var payload struct {
		GrantedCapabilities   []string `json:"grantedCapabilities"`
		SupportedCapabilities []any    `json:"supportedCapabilities"`
		Tools                 []string `json:"tools"`
		Resources             bool     `json:"resources"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.GrantedCapabilities == nil || payload.Tools == nil {
		return CapabilitySnapshot{}, errors.New("LocalMind capability discovery result is malformed")
	}
	payload.GrantedCapabilities = normalizedStrings(payload.GrantedCapabilities)
	payload.Tools = normalizedStrings(payload.Tools)
	return CapabilitySnapshot{
		GrantedCapabilities: payload.GrantedCapabilities, SupportedCapabilities: payload.SupportedCapabilities,
		Tools: payload.Tools, Resources: payload.Resources,
	}, nil
}

func validateCapabilityTools(discovered []mcpclient.DiscoveredTool, reported []string) error {
	discoveredNames := make([]string, 0, len(discovered))
	for _, tool := range discovered {
		if tool.RemoteName != discoveryRemoteName {
			discoveredNames = append(discoveredNames, tool.RemoteName)
		}
	}
	discoveredNames = normalizedStrings(discoveredNames)
	if !slices.Equal(discoveredNames, normalizedStrings(reported)) {
		return errors.New("LocalMind tools/list and capability snapshot tool names differ")
	}
	return nil
}

func snapshotRevision(endpointID string, discovery mcpclient.Discovery, capabilities CapabilitySnapshot) string {
	payload := struct {
		EndpointID   string
		Initialize   mcpclient.InitializeResult
		Tools        []mcpclient.DiscoveredTool
		Capabilities CapabilitySnapshot
	}{endpointID, discovery.Initialize, discovery.Tools, capabilities}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "lms_" + hex.EncodeToString(sum[:12])
}

func publicSnapshot(snapshot Snapshot) map[string]any {
	return map[string]any{
		"provider": "localmind", "server_name": snapshot.ServerInfo.Name, "server_version": snapshot.ServerInfo.Version,
		"protocol_version": snapshot.ProtocolVersion, "endpoint_id": snapshot.EndpointID, "snapshot_revision": snapshot.Revision,
		"granted_capabilities": snapshot.Capabilities.GrantedCapabilities, "visible_tools": snapshot.VisibleToolNames,
		"resources": snapshot.Capabilities.Resources, "refreshed_at": snapshot.RefreshedAt, "untrusted": true,
	}
}

func unwrapResultSchema(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	result, _ := properties["result"].(map[string]any)
	if result != nil {
		return result
	}
	return nil
}

func classifyOperation(name string, readOnly bool) string {
	if readOnly {
		return string(app.RouteOperationRead)
	}
	name = strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range []string{"delete_", "remove_", "revoke_", "abort_", "release_", "unpublish_"} {
		if strings.HasPrefix(name, prefix) {
			return string(app.RouteOperationDelete)
		}
	}
	for _, prefix := range []string{"publish_", "send_", "decide_", "approve_", "retry_", "run_", "control_"} {
		if strings.HasPrefix(name, prefix) {
			return string(app.RouteOperationInteract)
		}
	}
	for _, prefix := range []string{"create_", "upload_", "initialize_", "invite_", "grant_", "fork_", "start_"} {
		if strings.HasPrefix(name, prefix) {
			return string(app.RouteOperationCreate)
		}
	}
	for _, prefix := range []string{"update_", "apply_", "restore_", "rollback_", "resolve_", "complete_", "set_"} {
		if strings.HasPrefix(name, prefix) {
			return string(app.RouteOperationEdit)
		}
	}
	return string(app.RouteOperationInteract)
}

func annotationBool(annotations map[string]any, key string) bool {
	value, _ := annotations[key].(bool)
	return value
}

func normalizedStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return out
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	return map[string]any{"type": "object", "required": required, "properties": properties, "additionalProperties": false}
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "LocalMind operation"
}

func boundedText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func (m *Manager) timeoutMS() int {
	seconds := m.cfg.RequestTimeoutSeconds
	if seconds <= 0 {
		return defaultExternalToolTimeoutMS
	}
	return seconds * 1000
}
