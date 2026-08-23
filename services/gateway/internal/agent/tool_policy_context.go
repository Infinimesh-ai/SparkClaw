package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const policyExecutionContextSchemaVersion = 1

func (r Runtime) toolPolicyExecutionContext(ctx context.Context, runID string, definition app.ToolDefinition, args map[string]any) (app.PolicyExecutionContext, error) {
	run, ok, err := r.store.GetRun(ctx, runID)
	if err != nil {
		return app.PolicyExecutionContext{}, err
	}
	if !ok || run.MessageContext == nil || !isExternalMCPInvocation(run.MessageContext.MCP) {
		return app.PolicyExecutionContext{}, nil
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
	readsWorkspace, err := toolReadsSparkClawWorkspaceData(ctx, r, run, definition, args)
	if err != nil {
		return app.PolicyExecutionContext{}, err
	}
	covered, err := r.approvedWorkspaceContractCoversTool(ctx, run, definition, args)
	if err != nil {
		return app.PolicyExecutionContext{}, err
	}
	if readsWorkspace && !covered {
		context.ResourceClass = app.PolicyResourceSparkClawWorkspaceData
		context.AccessClass = app.PolicyAccessWorkspaceSourceRead
		if definition.Name == app.ToolWorkspaceDataAccess {
			if requested := app.PolicyAccessClass(strings.TrimSpace(stringValue(args["access_class"]))); requested == app.PolicyAccessWorkspaceDerivativeDisclosure {
				context.AccessClass = requested
			}
		}
		context.ContractDigest = policyExecutionContractDigest(definition.Name, args, context)
	}
	return context, nil
}

func (r Runtime) approvedWorkspaceContractCoversTool(ctx context.Context, run app.AgentRun, definition app.ToolDefinition, args map[string]any) (bool, error) {
	if definition.Name == app.ToolWorkspaceDataAccess || run.Workflow == nil ||
		(run.Workflow.Plan.ProfileID != app.WorkflowDocumentRead && run.Workflow.Plan.ProfileID != app.WorkflowDocumentEdit) {
		return false, nil
	}
	if toolDefinitionHasCapability(definition, app.ToolCapabilityObservationRead) {
		source, err := r.workspaceObservationSourceCall(ctx, run, strings.TrimSpace(stringValue(args["artifact_uri"])))
		if err != nil {
			return false, err
		}
		if source == nil || !toolCallCompleted(*source) || source.Capability == app.ToolCapabilityObservationRead {
			return false, nil
		}
		sourceDefinition, ok := r.tools.Definition(source.Tool)
		if !ok {
			return false, nil
		}
		return r.approvedWorkspaceContractCoversTool(ctx, run, sourceDefinition, source.Arguments)
	}
	path := strings.TrimSpace(stringValue(args["path"]))
	if path != "" && path != strings.TrimSpace(run.Workflow.Route.Slots.TargetRef) {
		return false, nil
	}
	if path == "" {
		return false, nil
	}
	call, err := r.workspaceDataAccessCallForRun(ctx, run.ID)
	if err != nil {
		return false, err
	}
	if call == nil || call.Status != app.ToolCallStatusCompletedAfterApproval {
		return false, nil
	}
	approval, ok, err := r.store.GetApproval(ctx, call.ApprovalID)
	if err != nil {
		return false, err
	}
	return ok && approval.Status == app.ApprovalStatusApproved && r.validateWorkspaceDataAccessApproval(ctx, *call, approval) == nil, nil
}

func (r Runtime) workspaceObservationSourceCall(ctx context.Context, run app.AgentRun, artifactURI string) (*app.ToolCall, error) {
	if artifactURI == "" {
		return nil, nil
	}
	storedCalls, err := r.store.ListToolCalls(ctx, run.SessionID)
	if err != nil {
		return nil, err
	}
	for _, call := range toolCallsForRun(storedCalls, run.ID) {
		if strings.TrimSpace(call.ObservationRef) == artifactURI {
			copy := call
			return &copy, nil
		}
	}
	return nil, nil
}

func isExternalMCPInvocation(ref *app.MCPInvocationRef) bool {
	return ref != nil && strings.TrimSpace(ref.InvocationID) != "" && strings.TrimSpace(ref.OperationID) != "" &&
		strings.TrimSpace(ref.BindingRef) != "" && ref.BindingRevision > 0 && strings.TrimSpace(ref.RequesterDeviceID) != ""
}

func toolReadsSparkClawWorkspaceData(ctx context.Context, r Runtime, run app.AgentRun, definition app.ToolDefinition, args map[string]any) (bool, error) {
	if containsToolEffect(definition.Directory.Effects, app.ToolEffectWorkspaceRead) {
		return true, nil
	}
	if !toolDefinitionHasCapability(definition, app.ToolCapabilityObservationRead) {
		return false, nil
	}
	artifactURI := strings.TrimSpace(stringValue(args["artifact_uri"]))
	if artifactURI == "" {
		return false, nil
	}
	call, err := r.workspaceObservationSourceCall(ctx, run, artifactURI)
	if err != nil {
		return false, err
	}
	if call == nil {
		return false, nil
	}
	source, ok := r.tools.Definition(call.Tool)
	return ok && containsToolEffect(source.Directory.Effects, app.ToolEffectWorkspaceRead), nil
}

func toolDefinitionHasCapability(definition app.ToolDefinition, name string) bool {
	for _, capability := range definition.Capabilities {
		if capability.Name == name {
			return true
		}
	}
	return false
}

func toolPolicyOutputClass(definition app.ToolDefinition, args map[string]any) app.PolicyOutputClass {
	if output := strings.TrimSpace(stringValue(args["output_class"])); output != "" {
		return app.PolicyOutputClass(output)
	}
	values := make([]string, 0, len(definition.Directory.OutputKinds))
	for _, kind := range definition.Directory.OutputKinds {
		if value := strings.TrimSpace(string(kind)); value != "" {
			values = append(values, value)
		}
	}
	if len(values) > 0 {
		return app.PolicyOutputClass(strings.Join(values, ","))
	}
	if definition.OutcomeAdapter != "" {
		return app.PolicyOutputClass(definition.OutcomeAdapter)
	}
	return app.PolicyOutputToolResult
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

func (r Runtime) validateContextBoundToolApproval(ctx context.Context, call app.ToolCall, approval app.Approval, definition app.ToolDefinition) error {
	if call.PolicyContext == nil || approval.PolicyContext == nil ||
		compactToolArgsFingerprint(call.Arguments) != compactToolArgsFingerprint(approval.Arguments) ||
		!samePolicyExecutionContext(call.PolicyContext, approval.PolicyContext) {
		return errors.New("context-bound tool approval contract was modified")
	}
	execution, err := r.toolPolicyExecutionContext(ctx, call.RunID, definition, call.Arguments)
	if err != nil {
		return err
	}
	current := persistedPolicyExecutionContext(execution)
	if !samePolicyExecutionContext(current, call.PolicyContext) {
		return errors.New("context-bound tool approval no longer matches the authenticated execution context")
	}
	return nil
}
