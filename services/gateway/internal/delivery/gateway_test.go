package delivery

import (
	"context"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type recordingProvider struct {
	key       string
	caps      app.DeliveryCapabilities
	endpoints []app.MessageEndpoint
	requests  []app.DeliveryRequest
}

func (p *recordingProvider) Key() string                            { return p.key }
func (p *recordingProvider) Capabilities() app.DeliveryCapabilities { return p.caps }
func (p *recordingProvider) Deliver(_ context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	p.endpoints = append(p.endpoints, endpoint)
	p.requests = append(p.requests, request)
	now := time.Now().UTC()
	return app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliverySucceeded, ProviderRef: p.key, AttemptedAt: now, DeliveredAt: &now}, nil
}

func TestSourceReplyUsesFrozenExactThirdPartyEndpoint(t *testing.T) {
	st := store.NewMemoryStore()
	binding := st.SaveNotificationBinding(app.NotificationBinding{
		ID: "bind-source", OwnerID: "owner-a", ActorID: "web-actor", Channel: "fake", Status: "active",
		Scopes: []string{app.BindingScopeMessageSendSelf},
	})
	st.SaveExternalChatSession(app.ExternalChatSession{
		ID: "chat-source", OwnerID: "external-actor-a", AuthorizedOwnerID: "owner-a", AuthorizedActorID: "web-actor",
		BindingID: binding.ID, Channel: "fake", ExternalUserID: "user-a", ExternalChatID: "chat-a",
		ExternalThreadID: "thread-a", LastContextToken: "context-a", DisplayName: "Alex", Status: "active",
	})
	st.SaveExternalChatSession(app.ExternalChatSession{
		ID: "chat-other", OwnerID: "external-actor-b", AuthorizedOwnerID: "owner-a", AuthorizedActorID: "web-actor",
		BindingID: binding.ID, Channel: "fake", ExternalUserID: "user-b", ExternalChatID: "chat-b",
		ExternalThreadID: "thread-b", LastContextToken: "context-b", DisplayName: "Alex", Status: "active",
	})
	endpoints := messagecontrol.NewEndpointRegistry(st)
	routes := messagecontrol.NewReturnRouteResolver(endpoints)
	content := app.MessageContent{Parts: []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "reply"}}}
	request, deliver, err := RequestFromWorkflowResult(t.Context(), app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "result-source", OwnerID: "external-actor-a",
		Authorization: app.MessageAuthorization{PrincipalID: "external-actor-a"}, Status: app.WorkflowResultBlocked,
		Error: &app.WorkflowResultError{Code: "policy_blocked", Message: "blocked"}, Content: content,
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "chat-source"},
	}, routes)
	if err != nil || !deliver || request.Target != "chat-source" || request.Origin != app.DeliveryOriginSourceReply ||
		request.ResultStatus != app.WorkflowResultBlocked || request.ResultError == nil || request.ResultError.Code != "policy_blocked" {
		t.Fatalf("source reply request was not frozen: request=%#v deliver=%v err=%v", request, deliver, err)
	}
	providers := NewProviderRegistry()
	fake := &recordingProvider{key: "fake", caps: app.DeliveryCapabilities{
		Kinds: []app.MessagePartKind{app.MessagePartText}, Dispositions: []app.MessagePartDisposition{app.MessageDispositionInline},
	}}
	if err := providers.Register(fake); err != nil {
		t.Fatal(err)
	}
	receipt, err := NewGateway(endpoints, providers, nil).Deliver(t.Context(), request)
	if err != nil || receipt.Status != app.DeliverySucceeded || len(fake.endpoints) != 1 {
		t.Fatalf("source reply delivery failed: receipt=%#v endpoints=%#v err=%v", receipt, fake.endpoints, err)
	}
	got := fake.endpoints[0]
	if got.ID != "chat-source" || got.Address != "chat-a" || got.ThreadRef != "thread-a" || got.ContextRef != "context-a" {
		t.Fatalf("source reply crossed an exact endpoint boundary: %#v", got)
	}
	request.OwnerID = "owner-b"
	if _, err := NewGateway(endpoints, providers, nil).Deliver(t.Context(), request); err == nil || len(fake.endpoints) != 1 {
		t.Fatalf("source reply crossed its authorized owner: endpoints=%#v err=%v", fake.endpoints, err)
	}
}

func TestMCPSourceReplyUsesBindingAndRequesterIdentity(t *testing.T) {
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	ticket, err := st.SaveMCPAccessTicket(app.MCPAccessTicket{
		SchemaVersion: app.MCPAccessTicketSchemaVersion,
		SecretHash:    "mcp-delivery-secret", OwnerID: "owner-a", ActorID: "owner-a", DomainID: "domain-a",
		Scope:  app.MCPAccessConversation,
		Status: app.MCPAccessPending, MaxUses: 1, IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := st.RedeemMCPAccessTicket(ticket.SecretHash, app.MCPPeerIdentity{DomainID: "domain-a", DeviceID: "device-a", KeyThumbprint: "thumb-a", ISCPSessionID: "session-a"}, now)
	if err != nil {
		t.Fatal(err)
	}
	providers := NewProviderRegistry()
	fake := &recordingProvider{key: "mcp", caps: app.DeliveryCapabilities{Kinds: []app.MessagePartKind{app.MessagePartText}}}
	if err := providers.Register(fake); err != nil {
		t.Fatal(err)
	}
	request := app.DeliveryRequest{
		SchemaVersion: app.DeliveryRequestSchemaVersion, ID: "delivery-mcp", IdempotencyKey: "result:mcp",
		OwnerID: "owner-a", ActorID: "owner-a", Authorization: app.MessageAuthorization{PrincipalID: "owner-a"},
		Target: app.EndpointID("mcp:" + binding.ID), Origin: app.DeliveryOriginSourceReply,
		Content: app.MessageContent{Parts: []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Text: "done"}}},
		MCP:     &app.MCPInvocationRef{InvocationID: "inv-a", OperationID: "op-a", BindingRef: binding.ID, RequesterDeviceID: "device-a"},
	}
	enabled := true
	endpoints := messagecontrol.NewEndpointRegistry(st).WithChannelEnabled(func(_, channel string) bool {
		return enabled && channel == "mcp"
	})
	gateway := NewGateway(endpoints, providers, nil)
	if _, err := gateway.Deliver(t.Context(), request); err != nil {
		t.Fatalf("authorized MCP reply failed: %v", err)
	}
	enabled = false
	if _, err := gateway.Deliver(t.Context(), request); err == nil || len(fake.requests) != 1 {
		t.Fatalf("disabled MCP connector delivered a source reply: requests=%#v err=%v", fake.requests, err)
	}
	enabled = true
	request.MCP.RequesterDeviceID = "device-b"
	if _, err := gateway.Deliver(t.Context(), request); ErrorCode(err) != CodeCrossUserDenied {
		t.Fatalf("requester substitution error = %v", err)
	}
}

func TestGatewayDeliversAllPartsThroughRegisteredProvider(t *testing.T) {
	st := store.NewMemoryStore()
	binding := st.SaveNotificationBinding(app.NotificationBinding{ID: "bind_fake", Channel: "fake", Provider: "anything", Status: "active"})
	endpoints := messagecontrol.NewEndpointRegistry(st)
	providers := NewProviderRegistry()
	fake := &recordingProvider{key: "fake", caps: app.DeliveryCapabilities{
		Kinds:        []app.MessagePartKind{app.MessagePartText, app.MessagePartImage, app.MessagePartAudio, app.MessagePartFile},
		Dispositions: []app.MessagePartDisposition{app.MessageDispositionInline, app.MessageDispositionVoiceNote, app.MessageDispositionAttachment},
	}}
	if err := providers.Register(fake); err != nil {
		t.Fatal(err)
	}
	content := app.MessageContent{Parts: []app.MessagePart{
		{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "hello"},
		{ID: "image", Kind: app.MessagePartImage, Disposition: app.MessageDispositionAttachment, ArtifactID: "img"},
		{ID: "voice", Kind: app.MessagePartAudio, Disposition: app.MessageDispositionVoiceNote, ArtifactID: "voice"},
		{ID: "file", Kind: app.MessagePartFile, Disposition: app.MessageDispositionAttachment, ArtifactID: "file"},
	}}
	request := app.DeliveryRequest{SchemaVersion: app.DeliveryRequestSchemaVersion, ID: "del_fake", IdempotencyKey: "once", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID}, Target: app.EndpointID(binding.ID), Content: content, CreatedAt: time.Now().UTC()}
	receipt, err := NewGateway(endpoints, providers, nil).Deliver(t.Context(), request)
	if err != nil || receipt.Status != app.DeliverySucceeded {
		t.Fatalf("deliver: receipt=%#v err=%v", receipt, err)
	}
	if len(fake.requests) != 1 || len(fake.requests[0].Content.Parts) != len(content.Parts) {
		t.Fatalf("provider did not receive the complete ordered payload: %#v", fake.requests)
	}
}

func TestProviderPreflightRejectsWholePayloadBeforeSend(t *testing.T) {
	st := store.NewMemoryStore()
	binding := st.SaveNotificationBinding(app.NotificationBinding{ID: "bind_text", Channel: "text-only", Status: "active"})
	endpoints := messagecontrol.NewEndpointRegistry(st)
	providers := NewProviderRegistry()
	fake := &recordingProvider{key: "text-only", caps: app.DeliveryCapabilities{Kinds: []app.MessagePartKind{app.MessagePartText}}}
	if err := providers.Register(fake); err != nil {
		t.Fatal(err)
	}
	request := app.DeliveryRequest{
		SchemaVersion: app.DeliveryRequestSchemaVersion,
		ID:            "del_unsupported", IdempotencyKey: "once", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID}, Target: app.EndpointID(binding.ID), CreatedAt: time.Now().UTC(),
		Content: app.MessageContent{Parts: []app.MessagePart{
			{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "must not send"},
			{ID: "audio", Kind: app.MessagePartAudio, Disposition: app.MessageDispositionVoiceNote, ArtifactID: "audio"},
		}},
	}
	receipt, err := NewGateway(endpoints, providers, nil).Deliver(t.Context(), request)
	if err == nil || receipt.Status != app.DeliveryFailed {
		t.Fatalf("expected explicit capability failure, receipt=%#v err=%v", receipt, err)
	}
	if len(fake.requests) != 0 {
		t.Fatalf("provider sent a partial payload: %#v", fake.requests)
	}
}

func TestWorkflowAndExplicitSendShareDeliveryRequest(t *testing.T) {
	st := store.NewMemoryStore()
	session := st.CreateSession("Web")
	routes := messagecontrol.NewReturnRouteResolver(messagecontrol.NewEndpointRegistry(st))
	content := app.MessageContent{Parts: []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "done"}}}
	route := app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: messagecontrol.WebEndpointID(session.ID)}
	result := app.WorkflowResult{SchemaVersion: app.WorkflowResultSchemaVersion, ID: "result_1", OwnerID: app.DefaultOwnerID, Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID}, Content: content, ReturnRoute: route}
	workflowRequest, deliver, err := RequestFromWorkflowResult(t.Context(), result, routes)
	if err != nil || !deliver {
		t.Fatalf("workflow request: deliver=%v err=%v", deliver, err)
	}
	explicitRequest, deliver, err := RequestForMessage(t.Context(), "send_1", app.DefaultOwnerID, app.MessageAuthorization{PrincipalID: app.DefaultOwnerID}, content, route, routes)
	if err != nil || !deliver {
		t.Fatalf("explicit request: deliver=%v err=%v", deliver, err)
	}
	if workflowRequest.Target != explicitRequest.Target || len(workflowRequest.Content.Parts) != len(explicitRequest.Content.Parts) {
		t.Fatalf("delivery paths diverged: workflow=%#v explicit=%#v", workflowRequest, explicitRequest)
	}
	if workflowRequest.Origin != app.DeliveryOriginAgentWorkflow {
		t.Fatalf("Web return-to-source was mislabeled as a third-party source reply: %#v", workflowRequest)
	}
}

func TestGatewayRejectsEndpointOwnedByAnotherPrincipal(t *testing.T) {
	st := store.NewMemoryStore()
	binding := st.SaveNotificationBinding(app.NotificationBinding{ID: "bind_other", OwnerID: "owner-b", Channel: "fake", Status: "active"})
	providers := NewProviderRegistry()
	fake := &recordingProvider{key: "fake", caps: app.DeliveryCapabilities{Kinds: []app.MessagePartKind{app.MessagePartText}}}
	if err := providers.Register(fake); err != nil {
		t.Fatal(err)
	}
	request := app.DeliveryRequest{
		SchemaVersion: app.DeliveryRequestSchemaVersion, ID: "del_cross_owner", IdempotencyKey: "cross-owner",
		OwnerID: "owner-a", ActorID: "owner-a", Authorization: app.MessageAuthorization{PrincipalID: "owner-a"}, Target: app.EndpointID(binding.ID),
		Content: app.MessageContent{Parts: []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "private"}}},
	}
	_, err := NewGateway(messagecontrol.NewEndpointRegistry(st), providers, nil).Deliver(t.Context(), request)
	if err == nil || len(fake.requests) != 0 {
		t.Fatalf("cross-owner endpoint was delivered: requests=%#v err=%v", fake.requests, err)
	}
}
