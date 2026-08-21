package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	responseMediaAccessContractRevision = "response_media_filename_v1"
	documentPathAccessContractRevision  = "document_path_v1"
)

func (r Runtime) queueMCPWorkspaceDataApproval(ctx context.Context, run *app.AgentRun) (app.ToolCall, app.Approval, bool, error) {
	args, required, err := mcpWorkspaceDataAccessArguments(run)
	if err != nil || !required {
		return app.ToolCall{}, app.Approval{}, false, err
	}
	if existing, err := r.workspaceDataAccessCallForRun(ctx, run.ID); err != nil {
		return app.ToolCall{}, app.Approval{}, false, err
	} else if existing != nil {
		return app.ToolCall{}, app.Approval{}, false, nil
	}
	call, approval, _, err := r.runToolPlan(ctx, run.SessionID, run.ID, toolPlan{
		Name: app.ToolWorkspaceDataAccess,
		Args: args,
	})
	if err != nil {
		return call, app.Approval{}, false, err
	}
	if approval == nil || call.Status != "approval_pending" {
		if call.Error != "" {
			return call, app.Approval{}, false, errors.New(call.Error)
		}
		return call, app.Approval{}, false, errors.New("workspace data access did not enter approval")
	}
	run.State = "approval_pending"
	run.CompletedAt = nil
	run.Summary = "Workspace data access is waiting for owner approval."
	if saved, err := r.saveRun(ctx, *run); err != nil {
		return call, app.Approval{}, false, err
	} else {
		*run = saved
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "policy", Type: "policy.workspace_data_approval_requested",
		Summary: "External MCP workspace data access is waiting for owner approval",
		Fields: map[string]any{
			"approval_id": approval.ID, "request_digest": args["request_digest"], "contract_revision": args["contract_revision"],
		},
	})
	return call, *approval, true, nil
}

func mcpResponseMediaAccessRequest(run *app.AgentRun) ([]app.MessageMediaLocator, bool, error) {
	if run == nil || run.Workflow == nil || run.MessageContext == nil || !isExternalMCPInvocation(run.MessageContext.MCP) ||
		run.Workflow.Plan.ProfileID != app.WorkflowConversationAnswer || run.Workflow.Plan.ProfileRevision != 3 ||
		len(run.Workflow.ActiveNodeIDs) != 1 || run.Workflow.ActiveNodeIDs[0] != "detect_response_media" {
		return nil, false, nil
	}
	locators := append([]app.MessageMediaLocator(nil), run.MessageContext.MediaLocators...)
	if len(locators) == 0 && run.Workflow.Route.Slots.Operation == app.RouteOperationPublish {
		for _, part := range run.MessageContext.RequestContent.Parts {
			if !isMediaMessagePart(part.Kind) || part.Resource == nil || part.Resource.Kind != "workspace_file" {
				continue
			}
			locators = append(locators, app.MessageMediaLocator{Path: part.Resource.Ref, Caption: part.Caption})
		}
		if len(locators) == 0 {
			if locator, ok := implicitResponseMediaLocator(run.Workflow.Route.Slots.Query); ok {
				locators = []app.MessageMediaLocator{locator}
			}
		}
	}
	if len(locators) == 0 {
		return nil, false, nil
	}
	for index := range locators {
		if err := validateResponseMediaLocatorSyntax(&locators[index]); err != nil {
			return nil, false, errors.New("response media locator is invalid: " + err.Error())
		}
	}
	return locators, true, nil
}

func validateResponseMediaLocatorSyntax(locator *app.MessageMediaLocator) error {
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
	if locator.Name != "" && (locator.Name != path.Base(locator.Name) || locator.Name == "." || locator.Name == ".." || strings.Contains(locator.Name, "://")) {
		return errors.New("name must be a complete base filename")
	}
	return nil
}

func mcpWorkspaceDataAccessArguments(run *app.AgentRun) (map[string]any, bool, error) {
	if locators, required, err := mcpResponseMediaAccessRequest(run); err != nil || required {
		if err != nil {
			return nil, false, err
		}
		args, err := responseMediaAccessArguments(*run, locators)
		return args, true, err
	}
	if required, err := mcpDocumentAccessRequest(run); err != nil || required {
		if err != nil {
			return nil, false, err
		}
		args, err := documentAccessArguments(*run)
		return args, true, err
	}
	return nil, false, nil
}

func mcpDocumentAccessRequest(run *app.AgentRun) (bool, error) {
	if run == nil || run.Workflow == nil || run.MessageContext == nil || !isExternalMCPInvocation(run.MessageContext.MCP) ||
		(run.Workflow.Plan.ProfileID != app.WorkflowDocumentRead && run.Workflow.Plan.ProfileID != app.WorkflowDocumentEdit) {
		return false, nil
	}
	if run.Workflow.Route.Slots.TargetKind != string(app.TargetKindWorkspacePath) {
		return false, errors.New("external MCP document workflow has no frozen workspace locator")
	}
	preflight, err := preflightExternalMCPDocumentPath(
		run.Workflow.Route.Slots.TargetRef,
		run.Workflow.Route.Slots.Operation == app.RouteOperationEdit || run.Workflow.Route.Slots.Operation == app.RouteOperationTransform,
	)
	if err != nil {
		return false, err
	}
	if preflight.InputRef != run.Workflow.Route.Slots.TargetRef || preflight.Format != run.Workflow.Route.Facts["document_format"] {
		return false, errors.New("external MCP document locator changed after routing")
	}
	return true, nil
}

func documentAccessArguments(run app.AgentRun) (map[string]any, error) {
	if run.Workflow == nil || run.MessageContext == nil || !isExternalMCPInvocation(run.MessageContext.MCP) {
		return nil, errors.New("workspace data approval context is unavailable")
	}
	args := workspaceAccessBaseArguments(run)
	args["contract_revision"] = documentPathAccessContractRevision
	args["locators"] = []any{map[string]any{"path": run.Workflow.Route.Slots.TargetRef}}
	args["access_class"] = string(app.PolicyAccessWorkspaceSourceRead)
	switch run.Workflow.Route.Slots.Operation {
	case app.RouteOperationEdit, app.RouteOperationTransform:
		args["output_class"] = "document_derivative"
	default:
		args["output_class"] = "document_content"
	}
	return sealWorkspaceAccessArguments(args)
}

func responseMediaAccessArguments(run app.AgentRun, locators []app.MessageMediaLocator) (map[string]any, error) {
	if run.Workflow == nil || run.MessageContext == nil || !isExternalMCPInvocation(run.MessageContext.MCP) {
		return nil, errors.New("workspace data approval context is unavailable")
	}
	locatorValues := make([]any, 0, len(locators))
	for _, locator := range locators {
		locatorValues = append(locatorValues, map[string]any{
			"path": locator.Path, "name": locator.Name, "query": locator.Query, "caption": locator.Caption,
		})
	}
	args := workspaceAccessBaseArguments(run)
	args["contract_revision"] = responseMediaAccessContractRevision
	args["locators"] = locatorValues
	args["access_class"] = string(app.PolicyAccessWorkspaceDerivativeDisclosure)
	args["output_class"] = "response_media"
	return sealWorkspaceAccessArguments(args)
}

func workspaceAccessBaseArguments(run app.AgentRun) map[string]any {
	invocation := run.MessageContext.MCP
	return map[string]any{
		"invocation": map[string]any{
			"invocation_id": invocation.InvocationID, "operation_id": invocation.OperationID,
			"binding_ref": invocation.BindingRef, "binding_revision": invocation.BindingRevision,
			"requester_device_id": invocation.RequesterDeviceID,
		},
		"workflow": map[string]any{
			"id": run.Workflow.Plan.ProfileID, "revision": run.Workflow.Plan.ProfileRevision, "plan_digest": run.Workflow.PlanDigest,
		},
		"return_route": map[string]any{
			"mode": run.MessageContext.ReturnRoute.Mode, "source_endpoint_id": run.MessageContext.ReturnRoute.SourceEndpointID,
			"endpoint_id": run.MessageContext.ReturnRoute.EndpointID, "source_admitted": run.MessageContext.ReturnRoute.SourceAdmitted,
		},
	}
}

func sealWorkspaceAccessArguments(args map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	args["request_digest"] = hex.EncodeToString(digest[:])
	return args, nil
}

func (r Runtime) workspaceDataAccessCallForRun(ctx context.Context, runID string) (*app.ToolCall, error) {
	run, ok, err := r.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	storedCalls, err := r.store.ListToolCalls(ctx, run.SessionID)
	if err != nil {
		return nil, err
	}
	for _, call := range toolCallsForRun(storedCalls, runID) {
		if call.Tool == app.ToolWorkspaceDataAccess {
			copy := call
			return &copy, nil
		}
	}
	return nil, nil
}

func (r Runtime) validateWorkspaceDataAccessApproval(ctx context.Context, call app.ToolCall, approval app.Approval) error {
	if call.Tool != app.ToolWorkspaceDataAccess || approval.ToolCallID != call.ID || approval.Tool != call.Tool {
		return errors.New("workspace data approval does not match its confirmation call")
	}
	run, ok, err := r.store.GetRun(ctx, call.RunID)
	if err != nil {
		return err
	}
	if !ok || run.SessionID != call.SessionID {
		return errors.New("workspace data approval run is unavailable")
	}
	expected, required, err := mcpWorkspaceDataAccessArguments(&run)
	if err != nil || !required {
		return errors.New("workspace data approval request is no longer active")
	}
	if compactToolArgsFingerprint(expected) != compactToolArgsFingerprint(call.Arguments) {
		return errors.New("workspace data approval no longer matches the frozen request")
	}
	definition, ok := r.tools.Definition(call.Tool)
	if !ok {
		return errors.New("workspace data confirmation tool is unavailable")
	}
	if err := r.validateContextBoundToolApproval(ctx, call, approval, definition); err != nil {
		return err
	}
	return nil
}

func (r Runtime) resumeMCPWorkspaceDataApproval(ctx context.Context, run app.AgentRun, content string) (Result, bool, error) {
	call, err := r.workspaceDataAccessCallForRun(ctx, run.ID)
	if err != nil {
		return Result{}, false, err
	}
	if call == nil {
		return Result{}, false, nil
	}
	approval, ok, err := r.store.GetApproval(ctx, call.ApprovalID)
	if err != nil {
		return Result{}, false, err
	}
	if !ok || approval.Status != "approved" {
		return Result{}, false, nil
	}
	if call.Status != "completed_after_approval" {
		result, resultErr := r.blockPersistedWorkflowResume(ctx, run, content, errors.New("workspace data approval did not complete safely"))
		return result, true, resultErr
	}
	if err := r.validateWorkspaceDataAccessApproval(ctx, *call, approval); err != nil {
		result, resultErr := r.blockPersistedWorkflowResume(ctx, run, content, err)
		return result, true, resultErr
	}
	var completeErr error
	switch strings.TrimSpace(stringValue(call.Arguments["contract_revision"])) {
	case responseMediaAccessContractRevision:
		completeErr = r.completeConversationMediaDetection(ctx, &run)
	case documentPathAccessContractRevision:
		completeErr = r.completeMCPDocumentPreflight(ctx, &run)
	default:
		completeErr = errors.New("workspace data approval contract revision is unsupported")
	}
	if completeErr != nil {
		result, resultErr := r.blockPersistedWorkflowResume(ctx, run, content, completeErr)
		return result, true, resultErr
	}
	if refreshed, ok, err := r.store.GetRun(ctx, run.ID); err != nil {
		return Result{}, true, err
	} else if ok {
		run = refreshed
	}
	return r.resumeMatchedWorkflow(ctx, run, content, nil, "workflow.resumed_after_workspace_data_approval")
}

func (r Runtime) completeMCPDocumentPreflight(ctx context.Context, run *app.AgentRun) error {
	required, err := mcpDocumentAccessRequest(run)
	if err != nil || !required {
		return errors.New("external MCP document access request is no longer active")
	}
	edit := run.Workflow.Route.Slots.Operation == app.RouteOperationEdit || run.Workflow.Route.Slots.Operation == app.RouteOperationTransform
	workspaceRoot, err := r.workspaceRootForSession(ctx, run.SessionID)
	if err != nil {
		return err
	}
	preflight, err := preflightDocumentPath(workspaceRoot, run.Workflow.Route.Slots.TargetRef, edit)
	if err != nil {
		return err
	}
	if preflight.InputRef != run.Workflow.Route.Slots.TargetRef || preflight.Format != run.Workflow.Route.Facts["document_format"] {
		return errors.New("approved document no longer matches the frozen locator and format")
	}
	if edit && !registeredAgentDocumentFormatPolicies().allowsRouteOperation(preflight.Format, run.Workflow.Route.Slots.Operation) {
		return errors.New("approved document format does not support the requested operation")
	}
	reference := documentContextReference{
		Ref: preflight.InputRef, Format: preflight.Format, Provenance: documentProvenanceExplicitCurrent,
	}
	record, err := r.confirmDocumentRecord(ctx, run.SessionID, run.ID, reference, preflight)
	if err != nil {
		return err
	}
	facts := cloneFacts(run.Workflow.Route.Facts)
	if facts == nil {
		facts = map[string]string{}
	}
	facts["path"] = preflight.InputRef
	facts["document_format"] = preflight.Format
	facts["document_id"] = record.ID
	facts["document_source"] = record.Source
	facts["document_source_id"] = firstNonEmptyString(record.SourceToolCallID, record.SourceMessageID, record.SourceRunID, record.ID)
	facts["document_activity"] = record.LastActivity
	if edit {
		facts["output_path"] = preflight.OutputRef
	}
	run.Workflow.Route.Facts = facts
	run.Workflow.Route.Slots.OutputRef = preflight.OutputRef
	run.Workflow.Route.Slots.Format = preflight.Format
	if len(run.Workflow.Intent.Objectives) > 0 {
		run.Workflow.Intent.Objectives[0].Target.Ref = preflight.InputRef
	}
	run.MessageContext.Route = run.Workflow.Route
	profile, err := r.profiles.Get(run.Workflow.Plan.ProfileID, run.Workflow.Plan.ProfileRevision)
	if err != nil {
		return err
	}
	if err := prepareWorkflowState(profile, run.Workflow); err != nil {
		return err
	}
	saved, err := r.saveRun(ctx, *run)
	if err != nil {
		return err
	}
	*run = saved
	return nil
}
