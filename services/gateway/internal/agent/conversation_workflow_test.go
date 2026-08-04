package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
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
		"为什么文件发送失败",
	} {
		decision := mustRouteIntent(t, runtime, goal)
		if decision.Status == app.RouteMatched && len(decision.CapabilityPath) == 2 && decision.CapabilityPath[1] == app.CapabilityConversationAnswer {
			t.Fatalf("tool or evidence request leaked into conversation.answer: goal=%q route=%#v", goal, decision)
		}
	}
}

func TestConversationPublishWithoutSelectedEndpointUsesTheSameMultipartWorkflow(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		path := filepath.Join(cfg.root, "uploads", "note.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("note bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	defer closeRuntime()
	result, err := runtime.HandleMessageWithAttachments(t.Context(), session.ID, "", []MessageAttachment{{
		ArtifactID: "workspace:uploads/note.txt", Name: "note.txt", RelPath: "uploads/note.txt", ContentType: "text/plain", Bytes: len("note bytes"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched || result.RouteDecision.Slots.Operation != app.RouteOperationPublish ||
		result.Run.State != "completed" || len(result.Approvals) != 0 || result.WorkflowResult == nil ||
		result.WorkflowResult.ReturnRoute.Mode != app.ReturnToSource || len(result.WorkflowResult.Content.Parts) != 1 ||
		result.WorkflowResult.Content.Parts[0].Kind != app.MessagePartFile || result.Message.Content != "" || len(result.Message.Attachments) != 1 {
		t.Fatalf("default Web target changed the ordinary multipart workflow: %#v", result)
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

func TestConversationPublishSendsOnlyMediaToSelectedExternalEndpoint(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		if err := os.MkdirAll(filepath.Join(cfg.root, "uploads"), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{
			"photo.png": "image bytes", "voice.wav": "audio bytes", "presentation.pptx": "presentation bytes",
		} {
			if err := os.WriteFile(filepath.Join(cfg.root, "uploads", name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	defer closeRuntime()
	returnRoute := app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "endpoint_selected"}
	attachments := []MessageAttachment{
		{ArtifactID: "workspace:uploads/photo.png", Name: "photo.png", RelPath: "uploads/photo.png", ContentType: "image/png", Bytes: len("image bytes")},
		{ArtifactID: "workspace:uploads/voice.wav", Name: "voice.wav", RelPath: "uploads/voice.wav", ContentType: "audio/wav", Bytes: len("audio bytes")},
		{
			ArtifactID: "workspace:uploads/presentation.pptx", Name: "presentation.pptx", RelPath: "uploads/presentation.pptx",
			ContentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation", Bytes: len("presentation bytes"),
		},
	}
	ingress := app.MessageIngressContext{
		Source:  app.MessageSourceContext{Kind: app.MessageSourceWeb, Adapter: "web", EndpointID: app.EndpointID("session:" + session.ID)},
		OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID}, ReturnRoute: returnRoute,
	}
	streamedText := strings.Builder{}
	result, err := runtime.handleMessage(t.Context(), session.ID, "", attachments, func(event StreamEvent) error {
		if event.Type == "text_delta" {
			streamedText.WriteString(event.Text)
		}
		return nil
	}, "message_publish", "run_publish", &ingress, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched || result.RouteDecision.CapabilityPath[1] != app.CapabilityConversationAnswer ||
		result.RouteDecision.Slots.Operation != app.RouteOperationPublish || result.RouteDecision.Slots.Query != "" || result.RouteDecision.Reason != "media_only_message" ||
		result.Run.MessageContext.IntentFusion != nil || result.Run.Workflow == nil || result.Run.Workflow.Plan.ProfileRevision != 2 {
		t.Fatalf("multipart message did not route through ordinary conversation: route=%#v fusion=%#v", result.RouteDecision, result.Run.MessageContext.IntentFusion)
	}
	if result.Run.State != "completed" || len(result.Approvals) != 0 || result.WorkflowResult == nil || result.WorkflowResult.ReturnRoute != returnRoute {
		t.Fatalf("external media publication did not complete on the selected endpoint: %#v", result)
	}
	if streamedText.Len() != 0 || result.Message.ID != "" || result.Message.Role != "" {
		t.Fatalf("external media publication created a WebChat result: streamed=%q message=%#v", streamedText.String(), result.Message)
	}
	parts := result.WorkflowResult.Content.Parts
	wantKinds := []app.MessagePartKind{app.MessagePartImage, app.MessagePartAudio, app.MessagePartFile}
	if len(parts) != len(wantKinds) {
		t.Fatalf("media publication retained command text or lost media: %#v", result.WorkflowResult.Content)
	}
	for index, part := range parts {
		if part.Kind != wantKinds[index] || part.ArtifactID == "" || strings.HasPrefix(part.ArtifactID, "workspace:") ||
			part.Resource == nil || part.Resource.Ref != attachments[index].RelPath || part.Bytes != attachments[index].Bytes || part.SHA256 == "" {
			t.Fatalf("ordinary media part was not governed from the source workspace: index=%d part=%#v", index, part)
		}
	}
	if hasModelCallOperation(st.ListModelCalls(session.ID, result.Run.ID), "workflow_answer", "deep") ||
		hasModelCallOperation(st.ListModelCalls(session.ID, result.Run.ID), "intent_tree_graph", "fast") ||
		!hasAgentAuditType(st.ListAudit(session.ID), "workflow.message_completed") {
		t.Fatalf("multipart publication used a chat answer model or missed its typed completion audit: calls=%#v audit=%#v", st.ListModelCalls(session.ID, result.Run.ID), st.ListAudit(session.ID))
	}
	messages := st.ListMessages(session.ID)
	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("external media publication persisted an assistant result in WebChat: %#v", messages)
	}
	request, deliverable, err := delivery.RequestFromWorkflowResult(t.Context(), *result.WorkflowResult, exactOnlyReturnRouteResolver{})
	if err != nil || !deliverable || request.Target != returnRoute.EndpointID || len(request.Content.Parts) != len(wantKinds) {
		t.Fatalf("media result did not enter the shared delivery request: request=%#v deliverable=%v err=%v", request, deliverable, err)
	}
	replayed, err := runtime.HandleMessageWithIngress(t.Context(), session.ID, "message_publish", "run_publish", "", attachments, ingress)
	if err != nil || replayed.Message.ID != "" || len(st.ListMessages(session.ID)) != 1 {
		t.Fatalf("idempotent media publication recreated a WebChat result: result=%#v err=%v messages=%#v", replayed, err, st.ListMessages(session.ID))
	}
}

func TestConversationTextPublishToExternalEndpointStillRequiresApproval(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	result, err := runtime.HandleMessageWithIngress(t.Context(), session.ID, "message_text_publish", "run_text_publish", "把这段文字作为消息发送", nil, app.MessageIngressContext{
		Source:  app.MessageSourceContext{Kind: app.MessageSourceWeb, Adapter: "web", EndpointID: app.EndpointID("session:" + session.ID)},
		OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: "endpoint_selected"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.State != "approval_pending" || len(result.Approvals) != 1 || result.WorkflowResult == nil ||
		result.WorkflowResult.ReturnRoute.Mode != app.ReturnNowhere || len(result.WorkflowResult.Content.Parts) != 1 ||
		result.WorkflowResult.Content.Parts[0].Kind != app.MessagePartText {
		t.Fatalf("pure text publication bypassed the external-send approval boundary: %#v", result)
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
		result.Message.Content != "该吃饭了。" || result.Run.MessageContext == nil || result.Run.MessageContext.Source.Kind != app.MessageSourceTimer ||
		result.Run.State != "completed" || len(result.Approvals) != 0 || result.WorkflowResult == nil ||
		result.WorkflowResult.ReturnRoute.Mode != app.ReturnToEndpoint || result.WorkflowResult.ReturnRoute.EndpointID != app.EndpointID("session:"+session.ID) {
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
