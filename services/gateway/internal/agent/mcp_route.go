package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messageplane"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

func (r Runtime) MCPRouteAvailable(capabilityID app.CapabilityID, workflow app.WorkflowContractRef) bool {
	node, ok := r.capabilities.Node(capabilityID)
	if !ok || node.Workflow == nil || *node.Workflow != workflow || node.RemoteMCP == nil {
		return false
	}
	profile, err := r.profiles.Get(workflow.ID, workflow.Revision)
	return err == nil && profile.Capability() == capabilityID
}

// HandleMCPBoundRoute executes one exact Catalog leaf selected by an authorized
// MCP tool definition. It deliberately does not invoke semantic intent routing.
func (r Runtime) HandleMCPBoundRoute(ctx context.Context, sessionID, messageID, runID string, request app.MCPBoundRouteRequest, ingress app.MessageIngressContext) (Result, error) {
	if request.Invocation.InvocationID == "" || request.Invocation.OperationID == "" || request.Invocation.BindingRef == "" || request.Invocation.RequesterDeviceID == "" {
		return Result{}, errors.New("MCP invocation identity is incomplete")
	}
	if existing, ok := r.store.GetRun(runID); ok && existing.SessionID == sessionID && existing.State != "received" {
		return r.resultForExistingRun(existing), nil
	}
	if messageID == "" || runID == "" || strings.TrimSpace(request.Content) == "" {
		return Result{}, errors.New("MCP message, run, and bounded content are required")
	}
	session, ok := r.store.GetSession(sessionID)
	if !ok {
		return Result{}, errors.New("MCP session is unavailable")
	}
	message := app.Message{ID: messageID, SessionID: sessionID, Role: "user", Content: request.Content, CreatedAt: time.Now().UTC()}
	envelope, err := messageplane.Normalize(messageplane.Ingress{
		Session: session, Message: message, OwnerID: ingress.OwnerID, SourceKind: ingress.Source.Kind,
		Adapter: ingress.Source.Adapter, EndpointID: ingress.Source.EndpointID, NativeMessageID: ingress.Source.NativeMessageID,
		NativeThreadRef: ingress.Source.NativeThreadRef, ReturnRoute: &ingress.ReturnRoute, Authorization: ingress.Authorization,
	})
	if err != nil {
		return Result{}, fmt.Errorf("normalize MCP message ingress: %w", err)
	}
	path, err := r.capabilities.PathTo(request.CapabilityID)
	if err != nil {
		return Result{}, err
	}
	route := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched,
		CatalogRevision: r.capabilities.Revision(), CapabilityPath: path,
		Slots: request.Slots, Facts: request.Facts, Confidence: 1,
		Reason: "mcp_bound_leaf",
	}
	if err := r.capabilities.ValidateDecision(route); err != nil {
		return Result{}, fmt.Errorf("validate MCP bound route: %w", err)
	}
	userMessage := r.store.AddMessage(message)
	r.recordMessageDocuments(session, userMessage)
	run := app.AgentRun{
		ID: runID, SessionID: sessionID, State: "received", Risk: classifyRisk(request.Content), StartedAt: time.Now().UTC(),
		MessageContext: &app.MessageRunContext{
			OwnerID: envelope.OwnerID, Authorization: envelope.Authorization, Source: envelope.Source,
			RequestContent: envelope.Content, ReturnRoute: envelope.ReturnRoute, Route: route, MCP: &request.Invocation,
		},
	}
	r.store.SaveRun(run)
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID, RunID: runID, Actor: "mcp", Type: "mcp.bound_leaf.selected",
		Summary: "Selected the MCP tool's exact authorized Catalog leaf without semantic candidate scoring",
		Fields: map[string]any{
			"invocation_id": request.Invocation.InvocationID, "operation_id": request.Invocation.OperationID,
			"binding_ref": request.Invocation.BindingRef, "requester_device_id": request.Invocation.RequesterDeviceID,
			"capability_path": route.CapabilityPath, "catalog_revision": route.CatalogRevision,
		},
	})
	dispatch, err := r.dispatchMatchedWorkflow(ctx, run, route, envelope.ReturnRoute, userMessage.ID)
	if err != nil {
		result := r.blockWorkflowSetup(ctx, run, request.Content, err)
		result.RouteDecision = &route
		result.WorkflowResult = r.workflowResultForDispatchFailure(result.Run, route, envelope.ReturnRoute, result.Message.Content)
		return result, nil
	}
	run = dispatch.Run
	run.State = "executing"
	r.store.SaveRun(run)
	execution := r.runWorkflowStream(ctx, sessionID, run, request.Content, dispatch.Profile, dispatch.Context, dispatch.Tools, nil)
	r.exposure.releaseRun(run.ID)
	if refreshed, ok := r.store.GetRun(run.ID); ok {
		run = refreshed
	}
	currentToolCalls := toolCallsForRun(r.store.ListToolCalls(sessionID), run.ID)
	now := time.Now().UTC()
	finalizeWorkflowRunState(&run, execution, now)
	run.ModelLane = execution.Chat.Lane
	run.Summary = summarizeRun(execution.Chat, execution.Observations, execution.Approvals)
	if strings.TrimSpace(execution.FinalAnswer) != "" {
		run.Summary = execution.FinalAnswer
	}
	run.Summary = r.applyGroundedSummary(sessionID, run.ID, request.Content, run.Summary, currentToolCalls)
	if call, approval, queued := r.queueExternalSendApproval(&run); queued {
		execution.ToolCalls = append(execution.ToolCalls, call)
		execution.Approvals = append(execution.Approvals, approval)
		currentToolCalls = append(currentToolCalls, call)
	}
	r.store.SaveRun(run)
	allApprovals := approvalsForRun(r.store.ListApprovals(""), run.ID)
	episode := summarizeEpisode(request.Content, run, currentToolCalls, allApprovals, run.Summary, now)
	r.store.SaveEpisodeSummary(episode)
	workflowResult := r.workflowResultForRun(run, route, envelope.ReturnRoute, run.Summary)
	assistant := r.persistWorkflowAssistantMessage(run, workflowResult, now)
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, currentToolCalls, allApprovals, r.store.ListRunFeedback(run.ID), &episode)
	return Result{Run: run, Message: assistant, ToolCalls: execution.ToolCalls, Approvals: execution.Approvals, RouteDecision: &route, WorkflowResult: workflowResult}, nil
}
