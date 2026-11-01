package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
)

func TestClarificationFileBindingSurvivesInputPromotion(t *testing.T) {
	original := documentContextReference{HandleID: "file", Name: "same.md", SHA256: strings.Repeat("a", 64), Size: 10, SourceID: "owner:part:1", Ref: "same.md"}
	promoted := original
	promoted.SourceID = "owner"
	promoted.Ref = "conversation-files/file/same.md"
	if fileBinding(original) != fileBinding(promoted) {
		t.Fatal("temporary path changed immutable file binding")
	}
	promoted.SHA256 = strings.Repeat("b", 64)
	if fileBinding(original) == fileBinding(promoted) {
		t.Fatal("new content retained old file binding")
	}
}
func TestClarificationTopTwoFiltersWithoutBackfill(t *testing.T) {
	r, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	signer := testClarificationSigner{key: make([]byte, 32), claims: &sync.Map{}}
	r = r.WithClarificationScope(signer, clarification.Binding{OwnerID: "owner", ClientID: "client", InstallationID: "install", ConversationID: "conversation"}, "request", nil).WithClarificationContext("source", strings.Repeat("a", 64))
	read, _ := r.semanticRouter.graph.Candidate("document.read#read")
	answer, _ := r.semanticRouter.graph.Candidate("conversation.answer#answer")
	decision := semanticrouting.Decision{Verdict: semanticrouting.VerdictAmbiguous, Candidates: []semanticrouting.CandidateScore{{Candidate: read, FusionScore: 0.8}, {Candidate: answer, FusionScore: 0.4}, {Candidate: answer, FusionScore: 0.7}}}
	if p := r.routingClarification(t.Context(), "处理一下", app.MessageSourceWeb, decision, intentGroundingProjection{}, documentContextResolution{}, app.RouteDecision{Status: app.RouteClarify}, DeliveryDirective{}); p != nil {
		t.Fatal("invalid top2 was filled from a lower candidate")
	}
	decision.Candidates[1].FusionScore = 0.6
	p := r.routingClarification(t.Context(), "处理一下", app.MessageSourceWeb, decision, intentGroundingProjection{}, documentContextResolution{}, app.RouteDecision{Status: app.RouteClarify}, DeliveryDirective{})
	if p == nil || len(p.Record.Options) != 1 || p.Record.Options[0].CandidateID != answer.ID || strings.Contains(p.Text, answer.ID) {
		t.Fatalf("invalid candidate labels/choice: %+v", p)
	}
	for _, source := range []app.MessageSourceKind{app.MessageSourceThirdPartyDevice, app.MessageSourceTimer} {
		if p := r.routingClarification(t.Context(), "处理一下", source, decision, intentGroundingProjection{}, documentContextResolution{}, app.RouteDecision{Status: app.RouteClarify}, DeliveryDirective{}); p != nil {
			t.Fatalf("interactive selection escaped source boundary: %s", source)
		}
	}
}
func TestClarificationTimestampTiesPreserveOriginalSnapshot(t *testing.T) {
	r, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	signer := testClarificationSigner{key: make([]byte, 32), claims: &sync.Map{}}
	binding := clarification.Binding{OwnerID: "owner", ClientID: "client", InstallationID: "install", ConversationID: "conversation"}
	earlier := app.Message{ID: "a", SessionID: "conversation", Role: "assistant", Content: "before", CreatedAt: time.Unix(1, 0)}
	source := app.Message{ID: "b", SessionID: "conversation", Role: "user", Content: "do something", CreatedAt: earlier.CreatedAt}
	prompt := app.Message{ID: "c", SessionID: "conversation", Role: "assistant", Content: "Choose:\n1. Answer", CreatedAt: time.Unix(2, 0)}
	digest, err := clarification.ContextDigest(ClarificationSnapshot([]app.Message{earlier, source}), "web")
	if err != nil {
		t.Fatal(err)
	}
	r = r.WithClarificationScope(signer, binding, "request", nil).WithClarificationContext(source.ID, digest)
	proposal := r.signProposal(prompt.Content, source.Content, []clarification.Option{{Number: 1, Kind: "route", CandidateID: "conversation.answer#answer", BindingSHA256: strings.Repeat("a", 64)}})
	if proposal == nil {
		t.Fatal("proposal unavailable")
	}
	r = r.WithClarificationScope(signer, binding, "next", []clarification.Record{*proposal.Record})
	history := invocationHistory{MessageCandidates: []app.Message{prompt, source, earlier}}
	_, ctx, text, _, restored, err := r.restoreClarification(context.Background(), "1", history, app.MessageEnvelope{OwnerID: "owner"})
	if err != nil || text != source.Content || len(restored.Selected.Messages) != 1 || restored.Selected.Messages[0].ID != "a" || ctx.Value(clarificationSelectionKey{}) == nil {
		t.Fatalf("tied original history not restored: %q %+v %v", text, restored, err)
	}
}

type changingEndpointRouter struct{ calls int }

func (r *changingEndpointRouter) ResolveMessageControl(_ context.Context, request MessageControlRouteRequest) (DeliveryTargetSelection, error) {
	r.calls++
	id := app.EndpointID("first")
	if r.calls > 1 {
		id = "replacement"
	}
	return DeliveryTargetSelection{Status: TargetResolved, RequestedProviderKey: request.Directive.RequestedProviderKey, RequestedRecipientText: request.Directive.RequestedRecipientText, ResolvedEndpointID: id, ResolutionRule: "test_unique"}, nil
}
func TestDeliveryEndpointCannotChangeAfterBinding(t *testing.T) {
	r, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	resolver := &changingEndpointRouter{}
	r = r.WithMessageControlRouter(resolver)
	result, err := r.HandleMessage(t.Context(), session.ID, "查询今天杭州的天气，通过 qq 发给 Bob")
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls < 2 || result.RouteDecision == nil || result.RouteDecision.Status == app.RouteMatched || result.Run.Workflow != nil || len(testListToolCalls(st, session.ID)) != 0 {
		t.Fatalf("changed endpoint was dispatched: calls=%d route=%+v", resolver.calls, result.RouteDecision)
	}
	if result.Run.MessageContext.ReturnRoute.Mode == app.ReturnToEndpoint {
		t.Fatal("unbound external endpoint was frozen")
	}
}
