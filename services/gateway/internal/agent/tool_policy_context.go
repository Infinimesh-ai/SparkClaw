package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const policyExecutionContextSchemaVersion = 1

func (r Runtime) toolPolicyExecutionContext(runID string, definition app.ToolDefinition, args map[string]any) app.PolicyExecutionContext {
	run, ok := r.store.GetRun(runID)
	if !ok || run.MessageContext == nil || !isExternalMCPInvocation(run.MessageContext.MCP) {
		return app.PolicyExecutionContext{}
	}
	context := app.PolicyExecutionContext{
		SchemaVersion:  policyExecutionContextSchemaVersion,
		PrincipalClass: app.PolicyPrincipalExternalMCPAI,
		RunID:          run.ID,
		OwnerID:        run.MessageContext.OwnerID,
		Authorization: app.MessageAuthorization{
			PrincipalID: run.MessageContext.Authorization.PrincipalID,
			Scope:       append([]string(nil), run.MessageContext.Authorization.Scope...),
		},
		MCP:         cloneMCPInvocationRef(run.MessageContext.MCP),
		ReturnRoute: run.MessageContext.ReturnRoute,
		OutputClass: toolPolicyOutputClass(definition, args),
	}
	if run.Workflow != nil {
		context.WorkflowID = run.Workflow.Plan.ProfileID
		context.WorkflowRevision = run.Workflow.Plan.ProfileRevision
		context.PlanDigest = run.Workflow.PlanDigest
	}
	if toolReadsSparkClawWorkspaceData(r, run, definition, args) && !r.approvedWorkspaceContractCoversTool(run, definition, args) {
		context.ResourceClass = app.PolicyResourceSparkClawWorkspaceData
		context.AccessClass = app.PolicyAccessWorkspaceSourceRead
		if definition.Name == app.ToolWorkspaceDataAccess {
			if requested := app.PolicyAccessClass(strings.TrimSpace(stringValue(args["access_class"]))); requested == app.PolicyAccessWorkspaceDerivativeDisclosure {
				context.AccessClass = requested
			}
		}
		context.ContractDigest = policyExecutionContractDigest(definition.Name, args, context)
	}
	return context
}

func (r Runtime) approvedWorkspaceContractCoversTool(run app.AgentRun, definition app.ToolDefinition, args map[string]any) bool {
	if definition.Name == app.ToolWorkspaceDataAccess || run.Workflow == nil ||
		(run.Workflow.Plan.ProfileID != app.WorkflowDocumentRead && run.Workflow.Plan.ProfileID != app.WorkflowDocumentEdit) {
		return false
	}
	if definition.Name == "observation.read" {
		source := r.workspaceObservationSourceCall(run, strings.TrimSpace(stringValue(args["artifact_uri"])))
		if source == nil || !toolCallCompleted(*source) || source.Tool == "observation.read" {
			return false
		}
		sourceDefinition, ok := r.tools.Definition(source.Tool)
		return ok && r.approvedWorkspaceContractCoversTool(run, sourceDefinition, source.Arguments)
	}
	path := strings.TrimSpace(stringValue(args["path"]))
	if path != "" && path != strings.TrimSpace(run.Workflow.Route.Slots.TargetRef) {
		return false
	}
	if path == "" {
		return false
	}
	call := r.workspaceDataAccessCallForRun(run.ID)
	if call == nil || call.Status != "completed_after_approval" {
		return false
	}
	approval, ok := r.store.GetApproval(call.ApprovalID)
	return ok && approval.Status == "approved" && r.validateWorkspaceDataAccessApproval(*call, approval) == nil
}

func (r Runtime) workspaceObservationSourceCall(run app.AgentRun, artifactURI string) *app.ToolCall {
	if artifactURI == "" {
		return nil
	}
	for _, call := range toolCallsForRun(r.store.ListToolCalls(run.SessionID), run.ID) {
		if strings.TrimSpace(call.ObservationRef) == artifactURI {
			copy := call
			return &copy
		}
	}
	return nil
}

func isExternalMCPInvocation(ref *app.MCPInvocationRef) bool {
	return ref != nil && strings.TrimSpace(ref.InvocationID) != "" && strings.TrimSpace(ref.OperationID) != "" &&
		strings.TrimSpace(ref.BindingRef) != "" && ref.BindingRevision > 0 && strings.TrimSpace(ref.RequesterDeviceID) != ""
}

func toolReadsSparkClawWorkspaceData(r Runtime, run app.AgentRun, definition app.ToolDefinition, args map[string]any) bool {
	if containsToolEffect(definition.Directory.Effects, app.ToolEffectWorkspaceRead) {
		return true
	}
	if definition.Name != "observation.read" {
		return false
	}
	artifactURI := strings.TrimSpace(stringValue(args["artifact_uri"]))
	if artifactURI == "" {
		return false
	}
	call := r.workspaceObservationSourceCall(run, artifactURI)
	if call == nil {
		return false
	}
	source, ok := r.tools.Definition(call.Tool)
	return ok && containsToolEffect(source.Directory.Effects, app.ToolEffectWorkspaceRead)
}

func toolPolicyOutputClass(definition app.ToolDefinition, args map[string]any) string {
	if output := strings.TrimSpace(stringValue(args["output_class"])); output != "" {
		return output
	}
	values := make([]string, 0, len(definition.Directory.OutputKinds))
	for _, kind := range definition.Directory.OutputKinds {
		if value := strings.TrimSpace(string(kind)); value != "" {
			values = append(values, value)
		}
	}
	if len(values) > 0 {
		return strings.Join(values, ",")
	}
	if definition.OutcomeAdapter != "" {
		return string(definition.OutcomeAdapter)
	}
	return "tool_result"
}

func policyExecutionContractDigest(tool string, args map[string]any, execution app.PolicyExecutionContext) string {
	boundArgs := make(map[string]any, len(args))
	for key, value := range args {
		if strings.HasPrefix(key, "_") {
			continue
		}
		boundArgs[key] = value
	}
	execution.ContractDigest = ""
	payload := struct {
		Tool      string                     `json:"tool"`
		Arguments map[string]any             `json:"arguments"`
		Context   app.PolicyExecutionContext `json:"context"`
	}{Tool: tool, Arguments: boundArgs, Context: execution}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func persistedPolicyExecutionContext(context app.PolicyExecutionContext) *app.PolicyExecutionContext {
	if context.ResourceClass == "" || context.ContractDigest == "" {
		return nil
	}
	copy := context
	copy.MCP = cloneMCPInvocationRef(context.MCP)
	copy.Authorization.Scope = append([]string(nil), context.Authorization.Scope...)
	return &copy
}

func cloneMCPInvocationRef(ref *app.MCPInvocationRef) *app.MCPInvocationRef {
	if ref == nil {
		return nil
	}
	copy := *ref
	return &copy
}

func samePolicyExecutionContext(left, right *app.PolicyExecutionContext) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}

func (r Runtime) validateContextBoundToolApproval(call app.ToolCall, approval app.Approval, definition app.ToolDefinition) error {
	if call.PolicyContext == nil || approval.PolicyContext == nil ||
		compactToolArgsFingerprint(call.Arguments) != compactToolArgsFingerprint(approval.Arguments) ||
		!samePolicyExecutionContext(call.PolicyContext, approval.PolicyContext) {
		return errors.New("context-bound tool approval contract was modified")
	}
	current := persistedPolicyExecutionContext(r.toolPolicyExecutionContext(call.RunID, definition, call.Arguments))
	if !samePolicyExecutionContext(current, call.PolicyContext) {
		return errors.New("context-bound tool approval no longer matches the authenticated execution context")
	}
	return nil
}
