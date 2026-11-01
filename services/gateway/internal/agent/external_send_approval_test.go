package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// A text-requested external send waits for owner approval of the exact result
// and resolved endpoint; nothing reaches delivery before that.
func sendExternallyForApproval(t *testing.T) (Runtime, *store.MemoryStore, app.Session, Result) {
	t.Helper()
	runtime, st, session := defaultWorkflowRuntime(t)
	runtime = runtime.WithMessageControlRouter(fixedMessageControlRouter{selection: DeliveryTargetSelection{
		Status: TargetResolved, RequestedProviderKey: "future chat", RequestedRecipientText: "Alice",
		ResolvedEndpointID: "endpoint_alice", ResolutionRule: "exact_recipient_match",
	}})
	directive := DeliveryDirective{ExplicitExternal: true, RequestedProviderKey: "future chat", RequestedRecipientText: "Alice"}
	result, err := runtime.HandleMessage(context.Background(), session.ID, routedMessage(t, runtime, "Explain opportunity cost and send it to Alice via Future Chat", directive))
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.State != "approval_pending" || len(result.Approvals) != 1 || result.WorkflowResult == nil {
		t.Fatalf("external send did not wait for approval: %#v", result)
	}
	approval := result.Approvals[0]
	if approval.Arguments["message_control_action"] != externalSendApprovalAction || approval.Arguments["endpoint_id"] != "endpoint_alice" ||
		approval.Arguments["content_sha256"] == "" || !approval.ArgumentsImmutable || approval.Status != app.ApprovalStatusPending {
		t.Fatalf("external send approval does not bind the endpoint and content: %#v", approval)
	}
	if result.WorkflowResult.Status != app.WorkflowResultWaiting || result.WorkflowResult.ReturnRoute.Mode == app.ReturnToEndpoint {
		t.Fatalf("pending external send kept delivery authority: %#v", result.WorkflowResult)
	}
	if _, deliverable, err := delivery.RequestFromWorkflowResult(context.Background(), *result.WorkflowResult, exactOnlyReturnRouteResolver{}); err != nil || deliverable {
		t.Fatalf("pending external send reached delivery: deliverable=%v err=%v", deliverable, err)
	}
	return runtime, st, session, result
}

func resolveExternalSend(t *testing.T, runtime Runtime, st *store.MemoryStore, approval app.Approval, status app.ApprovalStatus) (Result, bool) {
	t.Helper()
	resolved, err := st.ResolveApproval(context.Background(), approval.ID, status, "test")
	if err != nil {
		t.Fatal(err)
	}
	if status == app.ApprovalStatusApproved {
		if _, err := runtime.ExecuteApprovedToolCall(context.Background(), resolved); err != nil {
			t.Fatal(err)
		}
	}
	after, resumed, err := runtime.ResumeRunAfterApproval(context.Background(), approval.SessionID, approval.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return after, resumed
}

func TestExternalSendDeliversOnlyAfterApproval(t *testing.T) {
	runtime, st, _, pending := sendExternallyForApproval(t)
	after, resumed := resolveExternalSend(t, runtime, st, pending.Approvals[0], app.ApprovalStatusApproved)
	if !resumed || after.Run.State != "completed" || len(after.Approvals) != 0 || after.WorkflowResult == nil {
		t.Fatalf("approved external send did not complete: %#v", after)
	}
	if after.WorkflowResult.Status != app.WorkflowResultSucceeded || after.WorkflowResult.ReturnRoute.EndpointID != "endpoint_alice" {
		t.Fatalf("approved external send lost its endpoint: %#v", after.WorkflowResult)
	}
	request, deliverable, err := delivery.RequestFromWorkflowResult(context.Background(), *after.WorkflowResult, exactOnlyReturnRouteResolver{})
	if err != nil || !deliverable || request.Target != "endpoint_alice" {
		t.Fatalf("approved external send was not deliverable: %#v %v %v", request, deliverable, err)
	}
	if !reflect.DeepEqual(after.WorkflowResult.Content, pending.WorkflowResult.Content) {
		t.Fatalf("approved content differs from the reviewed content: %#v", after.WorkflowResult.Content)
	}
}

func TestRejectedExternalSendIsNotDelivered(t *testing.T) {
	runtime, st, _, pending := sendExternallyForApproval(t)
	after, resumed := resolveExternalSend(t, runtime, st, pending.Approvals[0], app.ApprovalStatusRejected)
	if !resumed || after.Run.State != "blocked" || after.WorkflowResult == nil || after.WorkflowResult.ReturnRoute.Mode == app.ReturnToEndpoint {
		t.Fatalf("rejected external send kept delivery authority: %#v", after)
	}
}

func TestChangedContentInvalidatesExternalSendApproval(t *testing.T) {
	runtime, st, _, pending := sendExternallyForApproval(t)
	run := pending.Run
	run.Summary = "A different answer"
	testSaveRun(st, run)
	after, resumed := resolveExternalSend(t, runtime, st, pending.Approvals[0], app.ApprovalStatusApproved)
	if !resumed || after.Run.State != "blocked" || after.WorkflowResult == nil || after.WorkflowResult.ReturnRoute.Mode == app.ReturnToEndpoint {
		t.Fatalf("changed content was delivered under the old approval: %#v", after)
	}
	if !hasAgentAuditField(mustAgentListAudit(t, st, run.SessionID), "message.control.send_approval_invalid", "endpoint_id", "endpoint_alice") {
		t.Fatal("invalid external send approval was not audited")
	}
}

func TestExternalSendApprovalAppliesOnlyToTextRequestedSends(t *testing.T) {
	endpoint := app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "endpoint_alice"}
	external := DeliveryDirective{ExplicitExternal: true, RequestedProviderKey: "future chat", RequestedRecipientText: "Alice"}
	for _, tc := range []struct {
		name     string
		context  app.MessageRunContext
		delivery DeliveryDirective
		route    app.ReturnRoute
		want     bool
	}{
		{"text requested", app.MessageRunContext{Source: app.MessageSourceContext{Kind: app.MessageSourceWeb}}, external, endpoint, true},
		{"composer selected target", app.MessageRunContext{Source: app.MessageSourceContext{Kind: app.MessageSourceWeb}}, DeliveryDirective{}, endpoint, false},
		{"current conversation", app.MessageRunContext{Source: app.MessageSourceContext{Kind: app.MessageSourceWeb}}, external, app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "session:web"}, false},
		{"schedule approved at creation", app.MessageRunContext{Source: app.MessageSourceContext{Kind: app.MessageSourceTimer}}, external, endpoint, false},
		{"inbound MCP already approved", app.MessageRunContext{MCP: &app.MCPInvocationRef{InvocationID: "i", OperationID: "o", BindingRef: "b", BindingRevision: 1, RequesterDeviceID: "d"}}, external, endpoint, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			context := tc.context
			run := app.AgentRun{MessageContext: &context}
			markExternalSendApproval(&run, tc.delivery, tc.route)
			if got := externalSendRequirement(run) != nil; got != tc.want {
				t.Fatalf("requirement %v, want %v", got, tc.want)
			}
		})
	}
}

// The business model is told SparkClaw delivers the approved result, so the
// content to send carries no refusal or remark about sending.
func TestExternalSendExecutionContentExplainsDelivery(t *testing.T) {
	run := app.AgentRun{MessageContext: &app.MessageRunContext{Source: app.MessageSourceContext{Kind: app.MessageSourceWeb}}}
	if got := executionContentForRun("Explain opportunity cost", run); got != "Explain opportunity cost" {
		t.Fatalf("ordinary request gained a delivery note: %q", got)
	}
	markExternalSendApproval(&run, DeliveryDirective{ExplicitExternal: true, RequestedProviderKey: "weixin", RequestedRecipientText: "Alice"},
		app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "endpoint_alice"})
	got := executionContentForRun("Explain opportunity cost and send it via Weixin to Alice", run)
	if !strings.HasPrefix(got, "Explain opportunity cost and send it via Weixin to Alice\n\nDelivery (handled by SparkClaw after owner approval") ||
		!strings.Contains(got, "weixin · Alice") || !strings.Contains(got, "Do not refuse") {
		t.Fatalf("external send execution content lacks the delivery note: %q", got)
	}
}
