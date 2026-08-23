package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
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
		result.Run.MessageContext.IntentFusion != nil || result.Run.Workflow == nil || result.Run.Workflow.Plan.ProfileRevision != 3 {
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
	if hasModelCallOperation(testListModelCalls(st, session.ID, result.Run.ID), "workflow_answer", "deep") ||
		hasModelCallOperation(testListModelCalls(st, session.ID, result.Run.ID), "intent_tree_graph", "fast") ||
		!hasAgentAuditType(mustAgentListAudit(t, st, session.ID), "workflow.message_completed") {
		t.Fatalf("multipart publication used a chat answer model or missed its typed completion audit: calls=%#v audit=%#v", testListModelCalls(st, session.ID, result.Run.ID), mustAgentListAudit(t, st, session.ID))
	}
	messages := storetest.MustListMessages(t, st, session.ID)
	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("external media publication persisted an assistant result in WebChat: %#v", messages)
	}
	request, deliverable, err := delivery.RequestFromWorkflowResult(t.Context(), *result.WorkflowResult, exactOnlyReturnRouteResolver{})
	if err != nil || !deliverable || request.Target != returnRoute.EndpointID || len(request.Content.Parts) != len(wantKinds) {
		t.Fatalf("media result did not enter the shared delivery request: request=%#v deliverable=%v err=%v", request, deliverable, err)
	}
	replayed, err := runtime.HandleMessageWithIngress(t.Context(), session.ID, "message_publish", "run_publish", "", attachments, ingress)
	if err != nil || replayed.Message.ID != "" || len(storetest.MustListMessages(t, st, session.ID)) != 1 {
		t.Fatalf("idempotent media publication recreated a WebChat result: result=%#v err=%v messages=%#v", replayed, err, storetest.MustListMessages(t, st, session.ID))
	}
}

func TestConversationTextPublishToExternalEndpointUsesHumanInstructionAuthority(t *testing.T) {
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
	if result.Run.State != "completed" || len(result.Approvals) != 0 || result.WorkflowResult == nil ||
		result.WorkflowResult.ReturnRoute.Mode != app.ReturnToEndpoint || result.WorkflowResult.ReturnRoute.EndpointID != "endpoint_selected" || len(result.WorkflowResult.Content.Parts) != 1 ||
		result.WorkflowResult.Content.Parts[0].Kind != app.MessagePartText {
		t.Fatalf("human-explicit text publication gained a destination approval: %#v", result)
	}
	request, deliverable, err := delivery.RequestFromWorkflowResult(t.Context(), *result.WorkflowResult, exactOnlyReturnRouteResolver{})
	if err != nil || !deliverable || request.Target != "endpoint_selected" {
		t.Fatalf("authorized text publication did not enter shared delivery: request=%#v deliverable=%v err=%v", request, deliverable, err)
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
	routing, err := runtime.routeIntentWithRequest(t.Context(), session.ID, "run_timer_search", goal, nil, nil, app.MessageSourceTimer)
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
	if len(result.ToolCalls) != 0 || len(result.Approvals) != 0 || !hasModelCallOperation(testListModelCalls(st, session.ID, result.Run.ID), "workflow_answer", "deep") ||
		hasWorkflowStepModelCall(testListModelCalls(st, session.ID, result.Run.ID)) {
		t.Fatalf("conversation.answer used tools, approvals, or the step loop: result=%#v calls=%#v", result, testListModelCalls(st, session.ID, result.Run.ID))
	}
	if !hasAgentAuditType(mustAgentListAudit(t, st, session.ID), "tools.exposure.none") || !hasAgentAuditType(mustAgentListAudit(t, st, session.ID), "workflow.model_answer_completed") {
		t.Fatalf("no-tool workflow boundary was not audited: %#v", mustAgentListAudit(t, st, session.ID))
	}
}

func TestMCPConversationMediaLocatorResolvesExactPath(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		if err := os.MkdirAll(filepath.Join(cfg.root, "exports"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg.root, "exports", "report.pdf"), []byte("pdf bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	defer closeRuntime()
	result, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_locator_path", "run_locator_path", app.MCPConversationRequest{
		Media:      []app.MessageMediaLocator{{Path: "exports/report.pdf", Caption: "Annual report"}},
		Invocation: app.MCPInvocationRef{InvocationID: "inv_path", OperationID: "op_path", BindingRef: "binding_path", BindingRevision: 1, RequesterDeviceID: "device_path"},
	}, mcpTestIngress(session, "binding_path"))
	if err != nil {
		t.Fatal(err)
	}
	assertPendingMCPWorkspaceApproval(t, st, result)
	messages := storetest.MustListMessages(t, st, session.ID)
	if len(messages) != 1 || strings.TrimSpace(messages[0].Content) != "" || len(messages[0].Attachments) != 0 ||
		len(messages[0].RequestedMedia) != 1 || messages[0].RequestedMedia[0].Path != "exports/report.pdf" ||
		messages[0].RequestedMedia[0].Caption != "Annual report" {
		t.Fatalf("pure-media MCP request was not persisted as a visible unverified requirement: %#v", messages)
	}
	result = approveMCPWorkspaceAccess(t, runtime, st, result)
	if result.Run.Workflow == nil || result.Run.Workflow.Plan.ProfileRevision != 3 || result.Run.MessageContext.ResponseMedia == nil ||
		result.Run.MessageContext.ResponseMedia.Status != app.ResponseMediaSelected || len(result.WorkflowResult.Content.Parts) != 1 ||
		result.WorkflowResult.Content.Parts[0].Resource == nil || result.WorkflowResult.Content.Parts[0].Resource.Ref != "exports/report.pdf" ||
		result.WorkflowResult.Content.Parts[0].Caption != "Annual report" || result.WorkflowResult.Content.Parts[0].SHA256 == "" {
		t.Fatalf("exact MCP media path was not frozen and returned: %#v", result)
	}
}

func TestMCPConversationMediaNameAndQuerySelectStableTopOne(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		for _, rel := range []string{"a/annual-report-final.pdf", "b/annual-report-final.pdf", "c/annual-report-draft.pdf"} {
			path := filepath.Join(cfg.root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	defer closeRuntime()
	for _, test := range []struct {
		name    string
		locator app.MessageMediaLocator
		want    string
	}{
		{name: "exact basename tie", locator: app.MessageMediaLocator{Name: "annual-report-final.pdf"}, want: "a/annual-report-final.pdf"},
		{name: "fuzzy top one", locator: app.MessageMediaLocator{Query: "annual report final"}, want: "a/annual-report-final.pdf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			id := strings.ReplaceAll(test.name, " ", "_")
			result, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_"+id, "run_"+id, app.MCPConversationRequest{
				Media:      []app.MessageMediaLocator{test.locator},
				Invocation: app.MCPInvocationRef{InvocationID: "inv_" + id, OperationID: "op_" + id, BindingRef: "binding_" + id, BindingRevision: 1, RequesterDeviceID: "device"},
			}, mcpTestIngress(session, "binding_"+id))
			if err != nil {
				t.Fatal(err)
			}
			assertPendingMCPWorkspaceApproval(t, st, result)
			result = approveMCPWorkspaceAccess(t, runtime, st, result)
			part := result.WorkflowResult.Content.Parts[0]
			if part.Resource == nil || part.Resource.Ref != test.want || len(result.Run.MessageContext.ResponseMedia.Resources) != 1 {
				t.Fatalf("locator did not select stable Top-1: want=%q part=%#v decision=%#v", test.want, part, result.Run.MessageContext.ResponseMedia)
			}
		})
	}
	audits := mustAgentListAudit(t, st, session.ID)
	if !hasAgentAuditType(audits, "workflow.response_media_lookup") {
		t.Fatalf("response-media filename lookup was not audited: %#v", audits)
	}
}

func TestMCPConversationAnswerCombinesModelTextWithFrozenMedia(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		path := filepath.Join(cfg.root, "answer.pdf")
		if err := os.WriteFile(path, []byte("answer media"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	defer closeRuntime()
	result, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_answer_media", "run_answer_media", app.MCPConversationRequest{
		Text:       "What is idempotency?",
		Media:      []app.MessageMediaLocator{{Name: "answer.pdf"}},
		Invocation: app.MCPInvocationRef{InvocationID: "inv_answer_media", OperationID: "op_answer_media", BindingRef: "binding_answer_media", BindingRevision: 1, RequesterDeviceID: "device"},
	}, mcpTestIngress(session, "binding_answer_media"))
	if err != nil {
		t.Fatal(err)
	}
	assertPendingMCPWorkspaceApproval(t, st, result)
	result = approveMCPWorkspaceAccess(t, runtime, st, result)
	parts := result.WorkflowResult.Content.Parts
	if result.Run.State != "completed" || result.Run.MessageContext.ResponseMedia.Status != app.ResponseMediaSelected || len(parts) != 2 ||
		parts[0].Kind != app.MessagePartText || !strings.Contains(parts[0].Text, "answer this directly") ||
		parts[1].Kind != app.MessagePartFile || parts[1].Resource == nil || parts[1].Resource.Ref != "answer.pdf" {
		t.Fatalf("conversation answer did not combine model text and frozen media: %#v", result)
	}
}

func TestFrozenResponseMediaIdentityRejectsArtifactSubstitution(t *testing.T) {
	before := app.ResponseMediaDecision{Resources: []app.ResourceRef{{
		Kind: "workspace_file", Ref: "report.pdf", Attributes: map[string]string{
			"artifact_id": "object-a", "name": "report.pdf", "content_type": "application/pdf", "bytes": "4", "sha256": "abcd",
		},
	}}}
	after := before
	after.Resources = append([]app.ResourceRef(nil), before.Resources...)
	after.Resources[0].Attributes = map[string]string{
		"artifact_id": "object-b", "name": "report.pdf", "content_type": "application/pdf", "bytes": "4", "sha256": "abcd",
	}
	if sameFrozenResponseMedia(before, after) {
		t.Fatal("frozen response media accepted an artifact identity substitution")
	}
}

func TestMCPConversationMissingMediaAsksForRefinement(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	result, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_missing", "run_missing", app.MCPConversationRequest{
		Media:      []app.MessageMediaLocator{{Query: "file that does not exist"}},
		Invocation: app.MCPInvocationRef{InvocationID: "inv_missing", OperationID: "op_missing", BindingRef: "binding_missing", BindingRevision: 1, RequesterDeviceID: "device"},
	}, mcpTestIngress(session, "binding_missing"))
	if err != nil {
		t.Fatal(err)
	}
	assertPendingMCPWorkspaceApproval(t, st, result)
	result = approveMCPWorkspaceAccess(t, runtime, st, result)
	if result.Run.MessageContext.ResponseMedia == nil || result.Run.MessageContext.ResponseMedia.Status != app.ResponseMediaClarify ||
		result.Run.MessageContext.ResponseMedia.ReasonCode != "file_not_found" || len(result.Run.MessageContext.ResponseMedia.Resources) != 0 ||
		len(result.WorkflowResult.Content.Parts) != 1 || result.WorkflowResult.Content.Parts[0].Kind != app.MessagePartText {
		t.Fatalf("zero-result locator did not return a text clarification: %#v", result)
	}
}

func TestMCPConversationQueuesApprovalWhenWorkspaceRootIsUnavailable(t *testing.T) {
	var root string
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		root = cfg.root
	})
	defer closeRuntime()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_unavailable_root", "run_unavailable_root", app.MCPConversationRequest{
		Media: []app.MessageMediaLocator{{Path: "private/report.pdf"}},
		Invocation: app.MCPInvocationRef{
			InvocationID: "inv_unavailable_root", OperationID: "op_unavailable_root", BindingRef: "binding_unavailable_root",
			BindingRevision: 1, RequesterDeviceID: "device",
		},
	}, mcpTestIngress(session, "binding_unavailable_root"))
	if err != nil {
		t.Fatal(err)
	}
	assertPendingMCPWorkspaceApproval(t, st, result)
}

func TestMCPDocumentReadQueuesApprovalBeforePreflight(t *testing.T) {
	var root string
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		root = cfg.root
	})
	defer closeRuntime()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_document_missing_root", "run_document_missing_root", app.MCPConversationRequest{
		Text: `Summarize the document private/report.txt
MOCK_STEP_RESPONSE:{"type":"action","tool":"files.read","arguments":{"path":"private/report.txt"}}`,
		Invocation: app.MCPInvocationRef{
			InvocationID: "inv_document_missing_root", OperationID: "op_document_missing_root", BindingRef: "binding_document_missing_root",
			BindingRevision: 1, RequesterDeviceID: "device",
		},
	}, mcpTestIngress(session, "binding_document_missing_root"))
	if err != nil {
		t.Fatal(err)
	}
	assertPendingMCPWorkspaceApproval(t, st, result)
	documentRecords := mustListAgentDocumentRecords(t, st, "", session.ID, 10)
	if result.Run.Workflow == nil || result.Run.Workflow.Plan.ProfileID != app.WorkflowDocumentRead ||
		result.Approvals[0].Arguments["contract_revision"] != documentPathAccessContractRevision ||
		len(documentRecords) != 0 {
		t.Fatalf("external MCP document request performed pre-approval preflight: %#v", result)
	}
}

func TestApprovedMCPDocumentReadUsesOneWorkspaceApproval(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		if err := os.WriteFile(filepath.Join(cfg.root, "report.txt"), []byte("approved workspace evidence"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	defer closeRuntime()
	pending, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_document_once", "run_document_once", app.MCPConversationRequest{
		Text: `Summarize the document report.txt
MOCK_STEP_RESPONSE:{"type":"action","tool":"files.read","arguments":{"path":"report.txt"}}`,
		Invocation: app.MCPInvocationRef{
			InvocationID: "inv_document_once", OperationID: "op_document_once", BindingRef: "binding_document_once",
			BindingRevision: 1, RequesterDeviceID: "device",
		},
	}, mcpTestIngress(session, "binding_document_once"))
	if err != nil {
		t.Fatal(err)
	}
	assertPendingMCPWorkspaceApproval(t, st, pending)
	result := approveMCPWorkspaceAccess(t, runtime, st, pending)
	if result.Run.State != "completed" || result.Run.Workflow == nil || result.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		t.Fatalf("approved external MCP document read did not complete: %#v", result)
	}
	approvals := approvalsForRun(storetest.MustListApprovals(t, st, ""), result.Run.ID)
	calls := toolCallsForRun(testListToolCalls(st, session.ID), result.Run.ID)
	if len(approvals) != 1 || len(calls) != 2 || calls[0].Tool != app.ToolWorkspaceDataAccess || calls[1].Tool != "files.read" || calls[1].Status != "completed" {
		t.Fatalf("approved document operation did not reuse its single data-boundary approval: approvals=%#v calls=%#v", approvals, calls)
	}
}

func TestApprovedMCPDocumentReadDoesNotCoverDifferentPathDerivative(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		if err := os.WriteFile(filepath.Join(cfg.root, "report.txt"), []byte("approved workspace evidence"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	defer closeRuntime()
	pending, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_document_derivative_scope", "run_document_derivative_scope", app.MCPConversationRequest{
		Text: `Summarize the document report.txt
MOCK_STEP_RESPONSE:{"type":"action","tool":"files.read","arguments":{"path":"report.txt"}}`,
		Invocation: app.MCPInvocationRef{
			InvocationID: "inv_document_derivative_scope", OperationID: "op_document_derivative_scope", BindingRef: "binding_document_derivative_scope",
			BindingRevision: 1, RequesterDeviceID: "device",
		},
	}, mcpTestIngress(session, "binding_document_derivative_scope"))
	if err != nil {
		t.Fatal(err)
	}
	result := approveMCPWorkspaceAccess(t, runtime, st, pending)
	foreignArtifact := "artifact://sparkclaw/observations/foreign-path.json"
	testSaveToolCall(st, app.ToolCall{
		ID: "tc_foreign_path", SessionID: session.ID, RunID: result.Run.ID, Tool: "files.read", Status: "completed",
		Arguments: map[string]any{"path": "other.txt"}, ObservationRef: foreignArtifact, StartedAt: time.Now().UTC(),
	})

	call, approval, _, _ := runtime.runToolPlan(t.Context(), session.ID, result.Run.ID, toolPlan{
		Name: "observation.read", Args: map[string]any{"artifact_uri": foreignArtifact, "max_bytes": 100},
	})
	if approval == nil || call.Status != "approval_pending" || call.PolicyContext == nil ||
		call.PolicyContext.ResourceClass != app.PolicyResourceSparkClawWorkspaceData {
		t.Fatalf("different-path derivative reused the document approval: call=%#v approval=%#v", call, approval)
	}
}

func TestRejectedMCPWorkspaceApprovalExposesNoResourceFacts(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	pending, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_rejected_workspace", "run_rejected_workspace", app.MCPConversationRequest{
		Media: []app.MessageMediaLocator{{Query: "private quarterly report"}},
		Invocation: app.MCPInvocationRef{
			InvocationID: "inv_rejected_workspace", OperationID: "op_rejected_workspace", BindingRef: "binding_rejected_workspace",
			BindingRevision: 1, RequesterDeviceID: "device",
		},
	}, mcpTestIngress(session, "binding_rejected_workspace"))
	if err != nil {
		t.Fatal(err)
	}
	assertPendingMCPWorkspaceApproval(t, st, pending)
	if _, err := st.ResolveApproval(t.Context(), pending.Approvals[0].ID, "rejected", "not authorized"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CompleteRunIfApprovalsResolved(t.Context(), pending.Run.ID); err != nil {
		t.Fatal(err)
	}
	blocked, _ := testGetRun(st, pending.Run.ID)
	if blocked.State != "blocked" || blocked.MessageContext.ResponseMedia != nil {
		t.Fatalf("rejected workspace approval exposed a resource decision: %#v", blocked)
	}
	for _, event := range mustAgentListAudit(t, st, session.ID) {
		if event.RunID == blocked.ID && event.Type == "workflow.response_media_lookup" {
			t.Fatalf("rejected workspace approval performed discovery: %#v", event)
		}
	}
}

func TestMCPWorkspaceApprovalCannotBeReusedAfterLocatorOrTargetChange(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*app.AgentRun)
	}{
		{name: "locator", mutate: func(run *app.AgentRun) {
			run.MessageContext.MediaLocators[0].Path = "different.pdf"
		}},
		{name: "return target", mutate: func(run *app.AgentRun) {
			run.MessageContext.ReturnRoute.SourceEndpointID = "mcp:different-binding"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
			defer closeRuntime()
			id := strings.ReplaceAll(test.name, " ", "_")
			pending, err := runtime.HandleMCPConversation(t.Context(), session.ID, "m_changed_"+id, "run_changed_"+id, app.MCPConversationRequest{
				Media: []app.MessageMediaLocator{{Path: "report.pdf"}},
				Invocation: app.MCPInvocationRef{
					InvocationID: "inv_changed_" + id, OperationID: "op_changed_" + id, BindingRef: "binding_changed_" + id,
					BindingRevision: 1, RequesterDeviceID: "device",
				},
			}, mcpTestIngress(session, "binding_changed_"+id))
			if err != nil {
				t.Fatal(err)
			}
			assertPendingMCPWorkspaceApproval(t, st, pending)
			approved, err := st.ResolveApproval(t.Context(), pending.Approvals[0].ID, "approved", "owner approved original contract")
			if err != nil {
				t.Fatal(err)
			}
			changed, _ := testGetRun(st, pending.Run.ID)
			test.mutate(&changed)
			testSaveRun(st, changed)
			call, err := runtime.ExecuteApprovedToolCall(t.Context(), approved)
			if err != nil || call.Status != "failed_after_approval" {
				t.Fatalf("changed workspace contract reused approval: call=%#v err=%v", call, err)
			}
			result, resumed, err := runtime.ResumeRunAfterApproval(t.Context(), session.ID, pending.Run.ID)
			if err != nil || !resumed || result.Run.State != "blocked" || result.Run.MessageContext.ResponseMedia != nil {
				t.Fatalf("changed workspace contract did not fail closed: resumed=%v result=%#v err=%v", resumed, result, err)
			}
			for _, event := range mustAgentListAudit(t, st, session.ID) {
				if event.RunID == pending.Run.ID && event.Type == "workflow.response_media_lookup" {
					t.Fatalf("changed workspace contract performed discovery: %#v", event)
				}
			}
		})
	}
}

func assertPendingMCPWorkspaceApproval(t *testing.T, st *store.MemoryStore, result Result) {
	t.Helper()
	if result.Run.State != "approval_pending" || result.Run.MessageContext == nil || result.Run.MessageContext.ResponseMedia != nil ||
		result.WorkflowResult == nil || result.WorkflowResult.Status != app.WorkflowResultWaiting ||
		result.WorkflowResult.ReturnRoute.Mode != app.ReturnToSource || len(result.Approvals) != 1 ||
		len(result.ToolCalls) != 1 || result.ToolCalls[0].Tool != app.ToolWorkspaceDataAccess || result.ToolCalls[0].PolicyContext == nil ||
		result.Approvals[0].PolicyContext == nil || result.Approvals[0].PolicyContext.ResourceClass != app.PolicyResourceSparkClawWorkspaceData {
		t.Fatalf("MCP workspace request did not pause at the data boundary: %#v", result)
	}
	for _, event := range mustAgentListAudit(t, st, result.Run.SessionID) {
		if event.RunID == result.Run.ID && event.Type == "workflow.response_media_lookup" {
			t.Fatalf("MCP workspace lookup ran before approval: %#v", event)
		}
	}
}

func approveMCPWorkspaceAccess(t *testing.T, runtime Runtime, st *store.MemoryStore, pending Result) Result {
	t.Helper()
	approval, err := st.ResolveApproval(t.Context(), pending.Approvals[0].ID, "approved", "owner approved exact workspace request")
	if err != nil {
		t.Fatal(err)
	}
	call, err := runtime.ExecuteApprovedToolCall(t.Context(), approval)
	if err != nil || call.Status != "completed_after_approval" {
		t.Fatalf("workspace approval confirmation failed: call=%#v err=%v", call, err)
	}
	result, resumed, err := runtime.ResumeRunAfterApproval(t.Context(), pending.Run.SessionID, pending.Run.ID)
	if err != nil || !resumed || result.Run.State == "approval_pending" {
		t.Fatalf("workspace request did not resume after approval: resumed=%v result=%#v err=%v", resumed, result, err)
	}
	return result
}

func mcpTestIngress(session app.Session, bindingID string) app.MessageIngressContext {
	endpoint := app.EndpointID("mcp:" + bindingID)
	return app.MessageIngressContext{
		Source:  app.MessageSourceContext{Kind: app.MessageSourceThirdPartyDevice, Adapter: "mcp", EndpointID: endpoint},
		OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: endpoint},
	}
}

func TestHandleMessagePersistsFinalTopTwoIntentFusionEvidence(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	result, err := runtime.HandleMessage(context.Background(), session.ID, "法国的首都是什么？\nMOCK_CONVERSATION_RESPONSE:巴黎。")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := testGetRun(st, result.Run.ID)
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

func TestHandleMessagePersistsClientTimezoneFromWebIngress(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	ingress := app.MessageIngressContext{
		Source:  app.MessageSourceContext{Kind: app.MessageSourceWeb, Adapter: "web", EndpointID: app.EndpointID("session:" + session.ID)},
		OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
		ReturnRoute:    app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: app.EndpointID("session:" + session.ID)},
		ClientTimezone: "America/New_York",
	}
	result, err := runtime.HandleMessageWithIngress(t.Context(), session.ID, "message_timezone", "run_timezone", "法国的首都是什么？\nMOCK_CONVERSATION_RESPONSE:巴黎。", nil, ingress)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := testGetRun(st, result.Run.ID)
	if !ok || stored.MessageContext == nil || stored.MessageContext.ClientTimezone != "America/New_York" {
		t.Fatalf("client timezone was not persisted with the run: %#v ok=%t", stored.MessageContext, ok)
	}
}

func TestHandleScheduleActionPersistsClientTimezoneFromWebIngress(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	ingress := app.MessageIngressContext{
		Source:         app.MessageSourceContext{Kind: app.MessageSourceWeb, Adapter: "web", EndpointID: app.EndpointID("session:" + session.ID)},
		OwnerID:        session.OwnerID,
		Authorization:  app.MessageAuthorization{PrincipalID: session.OwnerID},
		ReturnRoute:    app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: app.EndpointID("session:" + session.ID)},
		ClientTimezone: "America/New_York",
	}
	result, err := runtime.HandleScheduleActionWithIngress(t.Context(), session.ID, "删除定时任务", ScheduleAction{
		Operation: app.RouteOperationDelete, ScheduleID: "missing-schedule", ExpectedUpdatedAt: "2026-08-19T01:00:00Z",
	}, ingress)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := testGetRun(st, result.Run.ID)
	if !ok || stored.MessageContext == nil || stored.MessageContext.ClientTimezone != "America/New_York" {
		t.Fatalf("schedule action lost the client timezone: %#v ok=%t", stored.MessageContext, ok)
	}
}
