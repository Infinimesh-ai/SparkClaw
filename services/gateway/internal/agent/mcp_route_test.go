package agent

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestHandleMCPConversationUsesOrdinarySemanticRouting(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()

	invocation := app.MCPInvocationRef{
		InvocationID: "mcp_invocation_test", OperationID: "mcp_operation_test",
		BindingRef: "mcp_binding_test", BindingRevision: 3, RequesterDeviceID: "localmind-device",
	}
	result, err := runtime.HandleMCPConversation(
		context.Background(), session.ID, "m_mcp_test", "run_mcp_test",
		app.MCPConversationRequest{
			Text: "What is the capital of France?\nMOCK_CONVERSATION_RESPONSE:Paris.", Invocation: invocation,
		},
		app.MessageIngressContext{
			Source: app.MessageSourceContext{
				Kind: app.MessageSourceThirdPartyDevice, Adapter: "mcp", EndpointID: "mcp:mcp_binding_test",
				NativeMessageID: "external-request-1", NativeThreadRef: "external-session-1",
			},
			OwnerID:       session.OwnerID,
			Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
			ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "mcp:mcp_binding_test"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched ||
		len(result.RouteDecision.CapabilityPath) != 2 || result.RouteDecision.CapabilityPath[1] != app.CapabilityConversationAnswer {
		t.Fatalf("MCP conversation did not enter ordinary semantic routing: %#v", result.RouteDecision)
	}
	foundSemanticRouting := false
	for _, call := range st.ListModelCalls(session.ID, result.Run.ID) {
		if call.Operation == "intent_embedding" || call.Operation == "intent_tree_graph" {
			foundSemanticRouting = true
		}
	}
	if !foundSemanticRouting {
		t.Fatalf("MCP conversation bypassed semantic routing: %#v", st.ListModelCalls(session.ID, result.Run.ID))
	}
	stored, ok := st.GetRun(result.Run.ID)
	if !ok || stored.MessageContext == nil || stored.MessageContext.MCP == nil || *stored.MessageContext.MCP != invocation {
		t.Fatalf("MCP invocation was not persisted on the run: %#v ok=%v", stored, ok)
	}
	if stored.MessageContext.Authorization.PrincipalID != session.OwnerID || stored.MessageContext.MCP.RequesterDeviceID == stored.MessageContext.Authorization.PrincipalID {
		t.Fatalf("external requester was promoted to local actor: %#v", stored.MessageContext)
	}
	if result.WorkflowResult == nil || result.WorkflowResult.MCP == nil || *result.WorkflowResult.MCP != invocation {
		t.Fatalf("MCP invocation was not projected to WorkflowResult: %#v", result.WorkflowResult)
	}
	if result.Message.Content != "Paris." {
		t.Fatalf("unexpected MCP workflow answer: %q", result.Message.Content)
	}
}

func TestWorkflowResultFailurePathsPreserveMCPInvocation(t *testing.T) {
	invocation := &app.MCPInvocationRef{
		InvocationID: "mcp_invocation_failure", OperationID: "mcp_operation_failure",
		BindingRef: "mcp_binding_failure", BindingRevision: 1, RequesterDeviceID: "external-device",
	}
	run := app.AgentRun{
		ID: "run_mcp_failure", State: "blocked",
		MessageContext: &app.MessageRunContext{
			OwnerID: app.DefaultOwnerID, Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID}, MCP: invocation,
		},
	}
	runtime := Runtime{store: store.NewMemoryStore(), capabilities: capability.MustDefaultCatalog()}
	matched := app.RouteDecision{
		Status: app.RouteMatched, CapabilityPath: []app.CapabilityID{"conversation", app.CapabilityConversationAnswer},
	}
	terminal := app.RouteDecision{Status: app.RouteBlocked}

	results := []*app.WorkflowResult{
		runtime.workflowResultForDispatchFailure(run, matched, app.ReturnRoute{Mode: app.ReturnToSource}, "dispatch failed"),
		runtime.workflowResultForTerminalRoute(run, terminal, app.ReturnRoute{Mode: app.ReturnToSource}, "blocked"),
		runtime.workflowResultForUnmatched(run, terminal, app.ReturnRoute{Mode: app.ReturnToSource}, "unmatched"),
	}
	for index, result := range results {
		if result == nil || result.MCP != invocation {
			t.Fatalf("failure result %d lost MCP invocation: %#v", index, result)
		}
	}
}
