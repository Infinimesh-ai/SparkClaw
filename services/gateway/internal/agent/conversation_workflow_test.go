package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestConversationSemanticRoutingCoversOnlySimpleNoEvidenceRequests(t *testing.T) {
	runtime, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	for _, goal := range []string{"你好", "法国的首都是什么？", "解释一下机会成本"} {
		decision := mustRouteIntent(t, runtime, goal)
		if decision.Status != app.RouteMatched || len(decision.CapabilityPath) != 2 || decision.CapabilityPath[1] != app.CapabilityConversationAnswer ||
			decision.Slots.Operation != app.RouteOperationAnswer || decision.Slots.Query != goal {
			t.Fatalf("simple conversation did not route to conversation.answer: goal=%q route=%#v", goal, decision)
		}
	}
	for _, goal := range []string{
		"收集苹果官网在售mac的种类和价格",
		"打开QQ邮箱",
		"运行项目测试",
		"读取 report.docx",
		"记住我的生日",
	} {
		decision := mustRouteIntent(t, runtime, goal)
		if decision.Status == app.RouteMatched && len(decision.CapabilityPath) == 2 && decision.CapabilityPath[1] == app.CapabilityConversationAnswer {
			t.Fatalf("tool or evidence request leaked into conversation.answer: goal=%q route=%#v", goal, decision)
		}
	}
}

func TestDualChannelRouterSelectsConversationAnswerAndFreezesQuery(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	goal := "用一句话概括机会成本"
	routing, err := runtime.routeIntent(context.Background(), session.ID, "run_conversation_fusion", goal)
	if err != nil {
		t.Fatal(err)
	}
	if routing.Route.Status != app.RouteMatched || len(routing.Route.CapabilityPath) != 2 || routing.Route.CapabilityPath[1] != app.CapabilityConversationAnswer || routing.Route.Slots.Query != goal {
		t.Fatalf("fusion conversation route did not freeze the owner question: route=%#v fusion=%+v", routing.Route, routing.Fusion)
	}
}

func TestTimerPlainNotificationRoutesToConversationAndRendersReminder(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	goal := "吃饭"
	content := goal + "\nMOCK_CONVERSATION_RESPONSE:该吃饭了。"
	result, err := runtime.HandleMessageWithIngress(t.Context(), session.ID, "timer_message", "run_timer_message", content, nil, app.MessageIngressContext{
		Source:  app.MessageSourceContext{Kind: app.MessageSourceTimer, Adapter: "timer", ScheduleID: "schedule_1"},
		OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: app.EndpointID("session:" + session.ID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched || result.RouteDecision.CapabilityPath[1] != app.CapabilityConversationAnswer ||
		result.Message.Content != "该吃饭了。" || result.Run.MessageContext == nil || result.Run.MessageContext.Source.Kind != app.MessageSourceTimer {
		t.Fatalf("Timer notification did not enter the ordinary conversation workflow: %#v", result)
	}
	prompt := conversationAnswerSystemPrompt(result.Run.MessageContext)
	if !strings.Contains(prompt, "due Timer occurrence") || !strings.Contains(prompt, "say the content to the owner now") {
		t.Fatalf("Timer conversation answer prompt is missing due-notification semantics:\n%s", prompt)
	}
}

func TestTimerSourceDoesNotOverrideSupportedSearchRequest(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	goal := "查一下今天的 AI 新闻"
	routing, err := runtime.routeIntentWithRequest(t.Context(), session.ID, "run_timer_search", goal, nil, app.MessageSourceTimer)
	if err != nil {
		t.Fatal(err)
	}
	if routing.Route.Status != app.RouteMatched || routing.Route.CapabilityPath[1] != app.CapabilityBrowserInternetSearch ||
		routing.Route.Slots.Operation != app.RouteOperationSearch || routing.Route.Slots.Query != materializeRoutedQuery(app.CapabilityBrowserInternetSearch, goal, currentSearchDate()) {
		t.Fatalf("Timer source overrode the supported search request: %#v", routing.Route)
	}
}

func TestConversationAnswerRunsWithoutToolsOrLegacyFallback(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	result, err := runtime.HandleMessage(context.Background(), session.ID, "法国的首都是什么？\nMOCK_CONVERSATION_RESPONSE:巴黎。")
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched || result.RouteDecision.CapabilityPath[1] != app.CapabilityConversationAnswer ||
		result.Run.Workflow == nil || result.Run.Workflow.Status != app.WorkflowStatusSucceeded || result.Run.State != "completed" || result.Message.Content != "巴黎。" {
		t.Fatalf("simple conversation did not complete through conversation.answer: %#v", result)
	}
	if len(result.ToolCalls) != 0 || len(result.Approvals) != 0 || !hasModelCallOperation(st.ListModelCalls(session.ID, result.Run.ID), "workflow_answer", "deep") ||
		hasWorkflowStepModelCall(st.ListModelCalls(session.ID, result.Run.ID)) {
		t.Fatalf("conversation.answer used tools, approvals, or the step loop: result=%#v calls=%#v", result, st.ListModelCalls(session.ID, result.Run.ID))
	}
	if !hasAgentAuditType(st.ListAudit(session.ID), "tools.exposure.none") || !hasAgentAuditType(st.ListAudit(session.ID), "workflow.model_answer_completed") {
		t.Fatalf("no-tool workflow boundary was not audited: %#v", st.ListAudit(session.ID))
	}
}

func TestHandleMessagePersistsFinalTopTwoIntentFusionEvidence(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	result, err := runtime.HandleMessage(context.Background(), session.ID, "法国的首都是什么？\nMOCK_CONVERSATION_RESPONSE:巴黎。")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := st.GetRun(result.Run.ID)
	if !ok || stored.MessageContext == nil || stored.MessageContext.IntentFusion == nil {
		t.Fatalf("AgentRun lost semantic fusion evidence: %#v ok=%v", stored, ok)
	}
	fusion := stored.MessageContext.IntentFusion
	if fusion.SchemaVersion != app.IntentFusionDecisionSchemaVersion || fusion.GraphRevision == "" || fusion.CalibrationRevision == "" ||
		fusion.Channels.Embedding.Status != "healthy" || fusion.Channels.Tree.Status != "healthy" ||
		len(fusion.Candidates) != 2 || fusion.Candidates[0].CandidateID != "conversation.answer#answer" || fusion.Verdict != "clear" {
		t.Fatalf("persisted semantic fusion evidence is incomplete: %#v", fusion)
	}
}
