package mcptools

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpsafety"
)

type Policy struct {
	AllowMutations bool
	ToolAllow      []string
	ToolDeny       []string
}

type Classification struct {
	ReadOnly         bool
	Dangerous        bool
	Risk             app.RiskLevel
	RequiresApproval bool
	Idempotent       bool
}

type Decision struct {
	Visible bool
	Classification
}

type DefinitionOptions struct {
	Title                   string
	Description             string
	OutputSchema            map[string]any
	OutputSchemaSet         bool
	TimeoutMS               int
	Sandbox                 string
	Capabilities            []app.CapabilityDescriptor
	OutcomeAdapter          app.ToolOutcomeAdapter
	Directory               app.ToolDirectoryMetadata
	IncludeWorkspaceEffects bool
}

func Evaluate(tool mcpclient.Tool, policy Policy) Decision {
	classification := Classify(tool)
	visible := true
	if len(policy.ToolAllow) > 0 && !slices.Contains(policy.ToolAllow, tool.Name) {
		visible = false
	}
	if slices.Contains(policy.ToolDeny, tool.Name) {
		visible = false
	}
	if !classification.ReadOnly && !policy.AllowMutations {
		visible = false
	}
	return Decision{Visible: visible, Classification: classification}
}

func Classify(tool mcpclient.Tool) Classification {
	dangerous := AnnotationBool(tool.Annotations, "destructiveHint") || AnnotationBool(tool.Annotations, "openWorldHint")
	readOnly := AnnotationBool(tool.Annotations, "readOnlyHint") && !dangerous
	classification := Classification{
		ReadOnly: readOnly, Dangerous: dangerous,
		Risk: app.RiskReversible, RequiresApproval: true,
		Idempotent: AnnotationBool(tool.Annotations, "idempotentHint"),
	}
	if readOnly {
		classification.Risk = app.RiskRead
		classification.RequiresApproval = false
	} else if dangerous {
		classification.Risk = app.RiskDangerous
	}
	return classification
}

func Translate(discovered mcpclient.DiscoveredTool, classification Classification, options DefinitionOptions) app.ToolDefinition {
	title := options.Title
	if strings.TrimSpace(title) == "" {
		title = discovered.Tool.Title
	}
	description := options.Description
	if strings.TrimSpace(description) == "" {
		description = discovered.Tool.Description
	}
	if strings.TrimSpace(description) == "" {
		description = "Call the external MCP tool " + discovered.RemoteName + "."
	}
	outputSchema := options.OutputSchema
	if !options.OutputSchemaSet {
		outputSchema = discovered.Tool.OutputSchema
	}
	directory := options.Directory
	directory.Effects = classification.Effects(options.IncludeWorkspaceEffects)
	inputSchema := cloneMap(discovered.Tool.InputSchema)
	if inputSchema == nil {
		inputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return app.ToolDefinition{
		Name: discovered.LocalName, Title: BoundedText(title, 240), Description: BoundedText(description, 1800),
		InputSchema: inputSchema, OutputSchema: cloneMap(outputSchema), Annotations: cloneMap(discovered.Tool.Annotations),
		Risk: classification.Risk, RequiresApproval: classification.RequiresApproval, Idempotent: classification.Idempotent,
		TimeoutMS: options.TimeoutMS, Sandbox: options.Sandbox, Audit: "always",
		Capabilities: cloneCapabilities(options.Capabilities), OutcomeAdapter: options.OutcomeAdapter, Directory: directory,
	}
}

func (classification Classification) Effects(includeWorkspace bool) []app.ToolEffect {
	if classification.ReadOnly {
		effects := []app.ToolEffect{app.ToolEffectExternalRead}
		if includeWorkspace {
			effects = append(effects, app.ToolEffectWorkspaceRead)
		}
		return effects
	}
	effects := []app.ToolEffect{app.ToolEffectExternalInteract}
	if includeWorkspace {
		effects = append(effects, app.ToolEffectWorkspaceWrite)
	}
	return effects
}

func AnnotationBool(annotations map[string]any, key string) bool {
	value, _ := annotations[key].(bool)
	return value
}

func BoundedText(value string, maxBytes int) string {
	return mcpsafety.TruncateUTF8(strings.TrimSpace(value), maxBytes)
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned map[string]any
	if json.Unmarshal(raw, &cloned) != nil {
		return value
	}
	return cloned
}

func cloneCapabilities(value []app.CapabilityDescriptor) []app.CapabilityDescriptor {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return append([]app.CapabilityDescriptor(nil), value...)
	}
	var cloned []app.CapabilityDescriptor
	if json.Unmarshal(raw, &cloned) != nil {
		return append([]app.CapabilityDescriptor(nil), value...)
	}
	return cloned
}
