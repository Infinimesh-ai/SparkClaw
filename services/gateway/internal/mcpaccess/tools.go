package mcpaccess

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
)

func toolName(id app.CapabilityID) string { return "sparkclaw.route." + string(id) }

func toolForNode(node capability.Node, grant app.MCPLeafGrant) Tool {
	properties := map[string]any{}
	required := []string{}
	if len(grant.Operations) > 1 {
		values := make([]string, len(grant.Operations))
		for i, op := range grant.Operations {
			values[i] = string(op)
		}
		properties["operation"] = map[string]any{"type": "string", "enum": values}
		required = append(required, "operation")
	}
	if node.Route.RequireQuery {
		properties["query"] = map[string]any{"type": "string", "maxLength": 65536}
		required = append(required, "query")
	}
	if node.Route.RequireLocation {
		properties["location"] = map[string]any{"type": "string", "maxLength": 500}
		required = append(required, "location")
	}
	if node.Route.RequireTarget {
		properties["target_kind"] = map[string]any{"type": "string", "enum": node.Route.TargetKinds}
		properties["target_ref"] = map[string]any{"type": "string", "maxLength": 4096}
		required = append(required, "target_kind", "target_ref")
	}
	effects := make([]string, len(grant.Effects))
	for i, effect := range grant.Effects {
		effects[i] = string(effect)
	}
	return Tool{
		Name: toolName(node.ID), Title: string(node.ID), Description: node.Description,
		InputSchema: map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false},
		Annotations: map[string]any{"readOnlyHint": allReadEffects(grant.Effects), "destructiveHint": false},
		Meta:        map[string]any{"catalogRevision": capability.DefaultCatalogRevision, "workflow": grant.Workflow, "projectionRevision": grant.ProjectionRevision, "effects": effects},
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

func routeArguments(node capability.Node, grant app.MCPLeafGrant, arguments map[string]any) (app.RouteSlots, string, app.RouteOperation, app.ToolEffect, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	allowed := map[string]bool{"operation": true, "query": true, "location": true, "target_kind": true, "target_ref": true}
	for key := range arguments {
		if !allowed[key] {
			return app.RouteSlots{}, "", "", "", fmt.Errorf("unsupported argument %q", key)
		}
	}
	operation := app.RouteOperation("")
	if len(grant.Operations) == 1 {
		if _, supplied := arguments["operation"]; supplied {
			return app.RouteSlots{}, "", "", "", errors.New("operation is fixed by this tool")
		}
		operation = grant.Operations[0]
	} else if value, ok := arguments["operation"].(string); ok {
		operation = app.RouteOperation(value)
	}
	if operation == "" || !slices.Contains(grant.Operations, operation) {
		return app.RouteSlots{}, "", "", "", errors.New("operation is not granted")
	}
	effect, ok := node.RemoteMCP.Effect(operation)
	if !ok {
		return app.RouteSlots{}, "", "", "", errors.New("operation is not remotely exposed")
	}
	slots := app.RouteSlots{Operation: operation}
	var valid bool
	if slots.Query, valid = optionalString(arguments, "query"); !valid {
		return slots, "", "", "", errors.New("query must be a string")
	}
	if slots.Location, valid = optionalString(arguments, "location"); !valid {
		return slots, "", "", "", errors.New("location must be a string")
	}
	if slots.TargetKind, valid = optionalString(arguments, "target_kind"); !valid {
		return slots, "", "", "", errors.New("target_kind must be a string")
	}
	if slots.TargetRef, valid = optionalString(arguments, "target_ref"); !valid {
		return slots, "", "", "", errors.New("target_ref must be a string")
	}
	if node.ID == app.CapabilityBrowserInternetSearch {
		slots.FactScope = app.RouteFactScopeCurrentInternet
	}
	if node.ID == app.CapabilityBrowserWeather {
		slots.FactScope = app.RouteFactScopeWeatherSnapshot
		slots.TargetKind, slots.TargetRef = string(app.TargetKindLocation), slots.Location
	}
	content := strings.TrimSpace(slots.Query)
	if content == "" {
		content = strings.TrimSpace(slots.TargetRef)
	}
	if content == "" {
		return slots, "", "", "", errors.New("tool arguments contain no bounded request content")
	}
	if len(content) > 65536 || len(slots.Location) > 500 || len(slots.TargetRef) > 4096 {
		return slots, "", "", "", errors.New("tool argument exceeds its size limit")
	}
	return slots, content, operation, effect, nil
}

func routeFacts(id app.CapabilityID, slots app.RouteSlots) map[string]string {
	if id == app.CapabilityBrowserWeather && slots.Location != "" {
		return map[string]string{"location_source": "mcp_argument"}
	}
	return nil
}
func optionalString(arguments map[string]any, key string) (string, bool) {
	value, exists := arguments[key]
	if !exists {
		return "", true
	}
	text, ok := value.(string)
	return strings.TrimSpace(text), ok
}
func allReadEffects(effects []app.ToolEffect) bool {
	for _, effect := range effects {
		if effect != app.ToolEffectExternalRead && effect != app.ToolEffectLocalRead && effect != app.ToolEffectLocalCompute {
			return false
		}
	}
	return true
}
