package main

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestEndpointMessageControlRouterMapsTypedDirectivesToCanonicalResolution(t *testing.T) {
	st := store.NewMemoryStore()
	web := storetest.MustCreateSessionWithScope(t, st, "Web", "owner-a", t.TempDir(), "webchat", false)
	saveMessageControlEndpoint(t, st, "bind-source", "chat-source", "owner-a", "actor-a", "telegram", "Source", "source-user", "source-chat")
	saveMessageControlEndpoint(t, st, "bind-a", "chat-a", "owner-a", "actor-a", "telegram", "Alex", "user-1", "chat-1")
	saveMessageControlEndpoint(t, st, "bind-b", "chat-b", "owner-a", "actor-a", "telegram", "Alex", "user-2", "chat-2")
	saveMessageControlEndpoint(t, st, "bind-c", "chat-c", "owner-a", "actor-a", "weixin", "Chen", "user-3", "chat-3")
	router := endpointMessageControlRouter{endpoints: messagecontrol.NewEndpointRegistry(st)}

	webRequest := agent.MessageControlRouteRequest{
		SessionID: web.ID, Source: app.MessageSourceContext{Kind: app.MessageSourceWeb, EndpointID: messagecontrol.WebEndpointID(web.ID)},
		OwnerID: "owner-a", ActorID: "actor-a", Authorization: app.MessageAuthorization{PrincipalID: "actor-a"},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: messagecontrol.WebEndpointID(web.ID)},
	}
	sourceRequest := webRequest
	sourceRequest.Source = app.MessageSourceContext{Kind: app.MessageSourceThirdPartyDevice, EndpointID: "chat-source"}
	sourceRequest.ReturnRoute = app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "chat-source"}
	selectedRequest := webRequest
	selectedRequest.ReturnRoute = app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "chat-c"}
	timerRequest := webRequest
	timerRequest.Source = app.MessageSourceContext{Kind: app.MessageSourceTimer, Adapter: "timer", ScheduleID: "schedule-a"}
	timerRequest.ReturnRoute = app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "chat-c"}

	tests := []struct {
		name       string
		request    agent.MessageControlRouteRequest
		status     app.TargetResolutionStatus
		endpointID app.EndpointID
		candidates int
	}{
		{name: "web_default", request: webRequest, status: app.TargetDefaultWeb, endpointID: messagecontrol.WebEndpointID(web.ID)},
		{name: "frozen_source_reply", request: sourceRequest, status: app.TargetSourceReply, endpointID: "chat-source"},
		{name: "frozen_web_selection", request: selectedRequest, status: app.TargetResolved, endpointID: "chat-c"},
		{name: "frozen_timer_target", request: timerRequest, status: app.TargetResolved, endpointID: "chat-c"},
		{name: "software_and_recipient", request: withDeliveryDirective(webRequest, "weixin", "Chen"), status: app.TargetResolved, endpointID: "chat-c", candidates: 1},
		{name: "software_only_unique", request: withDeliveryDirective(webRequest, "weixin", ""), status: app.TargetResolved, endpointID: "chat-c", candidates: 1},
		{name: "software_only_multiple", request: withDeliveryDirective(webRequest, "telegram", ""), status: app.TargetNeedsRecipient, candidates: 3},
		{name: "same_name_ambiguous", request: withDeliveryDirective(webRequest, "telegram", "Alex"), status: app.TargetAmbiguous, candidates: 2},
		{name: "software_unavailable", request: withDeliveryDirective(webRequest, "future-chat", ""), status: app.TargetUnavailable},
		{name: "software_required", request: withDeliveryDirective(webRequest, "", ""), status: app.TargetNeedsChannel},
		{name: "explicit_send_does_not_collapse_to_source_reply", request: withDeliveryDirective(sourceRequest, "weixin", "Chen"), status: app.TargetResolved, endpointID: "chat-c", candidates: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection, err := router.ResolveMessageControl(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			if selection.Status != test.status || selection.ResolvedEndpointID != test.endpointID || len(selection.CandidateEndpointIDs) != test.candidates {
				t.Fatalf("unexpected canonical selection: %#v", selection)
			}
		})
	}
}

func TestEndpointMessageControlRouterRejectsMismatchedSourceRoute(t *testing.T) {
	router := endpointMessageControlRouter{endpoints: messagecontrol.NewEndpointRegistry(store.NewMemoryStore())}
	_, err := router.ResolveMessageControl(context.Background(), agent.MessageControlRouteRequest{
		SessionID: "session", Source: app.MessageSourceContext{Kind: app.MessageSourceThirdPartyDevice, EndpointID: "chat-a"},
		ActorID: "actor-a", Authorization: app.MessageAuthorization{PrincipalID: "actor-a"},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "chat-b"},
	})
	if err == nil {
		t.Fatal("mismatched third-party source endpoint was accepted")
	}
}

func TestTypedRouterFreezesEndpointWithoutDestinationApproval(t *testing.T) {
	cfg := integrationTestConfig(t)
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	session := storetest.MustCreateSessionWithScope(t, st, "Web", "owner-a", t.TempDir(), "webchat", false)
	saveMessageControlEndpoint(t, st, "bind-c", "chat-c", "owner-a", "owner-a", "weixin", "Chen", "user-3", "chat-3")
	endpoints := messagecontrol.NewEndpointRegistry(st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil).
		WithMessageControlRouter(endpointMessageControlRouter{endpoints: endpoints})

	result, err := runtime.HandleMessage(context.Background(), session.ID, `Send a greeting to Chen via Weixin
MOCK_CONVERSATION_RESPONSE:Greeting ready.`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.State != "completed" || result.Run.MessageContext == nil ||
		result.Run.MessageContext.ReturnRoute.Mode != app.ReturnToEndpoint || result.Run.MessageContext.ReturnRoute.EndpointID != "chat-c" ||
		result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched || len(result.RouteDecision.CapabilityPath) != 2 ||
		result.RouteDecision.CapabilityPath[1] != app.CapabilityConversationAnswer || len(result.Approvals) != 0 || len(result.ToolCalls) != 0 ||
		result.WorkflowResult == nil || result.WorkflowResult.Status != app.WorkflowResultSucceeded ||
		result.WorkflowResult.ReturnRoute.Mode != app.ReturnToEndpoint || result.WorkflowResult.ReturnRoute.EndpointID != "chat-c" {
		t.Fatalf("conversation send did not preserve the exact authorized endpoint: %#v", result)
	}
	if _, deliverable, err := delivery.RequestFromWorkflowResult(context.Background(), *result.WorkflowResult, messagecontrol.NewReturnRouteResolver(endpoints)); err != nil || !deliverable {
		t.Fatalf("authorized conversation result was not deliverable: deliverable=%v err=%v", deliverable, err)
	}
}

func withDeliveryDirective(request agent.MessageControlRouteRequest, provider, recipient string) agent.MessageControlRouteRequest {
	request.Directive = agent.DeliveryDirective{
		ExplicitExternal: true, RequestedProviderKey: provider, RequestedRecipientText: recipient,
	}
	return request
}

func saveMessageControlEndpoint(t testing.TB, st *store.MemoryStore, bindingID, chatID, ownerID, actorID, channel, displayName, externalUserID, externalChatID string) {
	t.Helper()
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: bindingID, OwnerID: ownerID, ActorID: actorID, Channel: channel, Provider: channel + "-provider",
		Status: string(app.EndpointActive), DisplayName: channel + " account", Scopes: []string{app.BindingScopeMessageSendSelf},
	})
	st.SaveExternalChatSession(app.ExternalChatSession{
		ID: chatID, OwnerID: actorID, AuthorizedOwnerID: ownerID, AuthorizedActorID: actorID,
		BindingID: bindingID, Channel: channel, ExternalUserID: externalUserID, ExternalChatID: externalChatID,
		DisplayName: displayName, Status: string(app.EndpointActive),
	})
}

var _ agent.MessageControlRouter = endpointMessageControlRouter{}
