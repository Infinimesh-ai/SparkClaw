package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestRoutedMessageUsesOwnerRequestWithoutNormalization(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()

	result, err := runtime.HandleMessage(context.Background(), session.ID, `帮我看看现在的状态
MOCK_STEP_RESPONSE:{"type":"final","answer":"状态正常"}`)
	if err != nil {
		t.Fatal(err)
	}
	if hasModelCallOperation(testListModelCalls(st, session.ID, result.Run.ID), "request_normalization", "fast") {
		t.Fatalf("removed request normalization still made a model call: %#v", testListModelCalls(st, session.ID, result.Run.ID))
	}
	if hasAgentAuditType(st.ListAudit(session.ID), "message.request.normalized") {
		t.Fatalf("removed request normalization still emitted an audit: %#v", st.ListAudit(session.ID))
	}
	raw, err := json.Marshal(result.Run.MessageContext)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"request"`) || strings.Contains(string(raw), "canonical") {
		t.Fatalf("message context retained the removed normalized-request structure: %s", raw)
	}
}

func TestFinalAnswerLanguageUsesOwnerRequest(t *testing.T) {
	goal := finalAnswerGoal(app.AgentRun{}, "最新国家人工智能大会讲了什么")
	if goal != "最新国家人工智能大会讲了什么" {
		t.Fatalf("final answer goal = %q", goal)
	}
	instruction := finalAnswerLanguageInstruction(goal)
	if !strings.Contains(instruction, "entire final answer in Chinese") || !strings.Contains(instruction, "translating non-Chinese evidence") {
		t.Fatalf("Chinese final answer instruction is incomplete: %q", instruction)
	}
}

func TestSemanticChannelInputsKeepEmbeddingQuestionContextFree(t *testing.T) {
	question := "作为23级学生要注意什么"
	context := strings.Join([]string{
		"Resolved governed document context:",
		`- document_id="doc_notice" name="通识选修课提醒.docx" format="docx" source="attachment" recent_activity="read"`,
		"Recent Agent context:",
		"- assistant: 已读取说明文档",
	}, "\n")
	inputs := newSemanticChannelInputs(question, context)
	if inputs.EmbeddingQuery != question || inputs.TreeQuery != question {
		t.Fatalf("dual channels did not receive the same owner question: %#v", inputs)
	}
	if strings.Contains(inputs.EmbeddingQuery, "doc_notice") || strings.Contains(inputs.EmbeddingQuery, "docx") ||
		strings.Contains(inputs.EmbeddingQuery, "已读取") {
		t.Fatalf("Embedding input leaked Fast-only context: %q", inputs.EmbeddingQuery)
	}
	if !strings.Contains(inputs.TreeContext, "doc_notice") || !strings.Contains(inputs.TreeContext, "recent_activity") {
		t.Fatalf("Fast input omitted typed document context: %q", inputs.TreeContext)
	}
}

func TestAttachedDocumentRoutesFromStructuredResourceWithoutTextLeakage(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		if err := os.WriteFile(filepath.Join(cfg.root, "note.txt"), []byte("attachment evidence"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	defer closeRuntime()

	result, err := runtime.HandleMessageWithAttachments(context.Background(), session.ID, `Summarize this attachment
MOCK_STEP_RESPONSE:{"type":"action","tool":"files.read","arguments":{"path":"note.txt"}}`, []MessageAttachment{{
		Name: "note.txt", RelPath: "note.txt", ContentType: "text/plain",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched || result.RouteDecision.Slots.TargetRef != "note.txt" {
		t.Fatalf("structured attachment did not ground the document route: route=%#v fusion=%#v", result.RouteDecision, result.Run.MessageContext.IntentFusion)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Arguments["path"] != "note.txt" {
		t.Fatalf("workflow did not freeze the structured attachment path: %#v", result.ToolCalls)
	}
}

func TestAttachmentOnlyDocumentRoutesAsOrdinaryMediaPublication(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		if err := os.WriteFile(filepath.Join(cfg.root, "note.txt"), []byte("attachment evidence"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	defer closeRuntime()
	route := mustRouteIntentWithResources(t, runtime, session.ID, "", []app.MessagePart{{
		Kind: app.MessagePartFile, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "note.txt"},
	}}, app.MessageSourceWeb)
	if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityConversationAnswer ||
		route.Slots.Operation != app.RouteOperationPublish || route.Slots.Query != "" || route.Reason != "media_only_message" {
		t.Fatalf("an attachment without an owner question did not remain an ordinary media message: %#v", route)
	}
}

func TestAttachedDocumentContextRoutesImplicitQuestionToDocumentRead(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		writeTestOfficePackage(t, filepath.Join(cfg.root, "student-notice.docx"), "word/document.xml")
	})
	defer closeRuntime()

	routing := mustRouteIntentOutput(t, runtime, session.ID, "作为23级学生要注意什么", []app.MessagePart{{
		Kind: app.MessagePartFile,
		Name: "关于通识选修课模块的特别提醒.docx",
		Resource: &app.ResourceRef{
			Kind: "workspace_file",
			Ref:  "student-notice.docx",
		},
	}}, app.MessageSourceWeb)
	route := routing.Route
	if route.Status != app.RouteMatched || len(route.CapabilityPath) != 2 ||
		route.CapabilityPath[1] != app.CapabilityDocumentRead || route.Slots.Query != "作为23级学生要注意什么" ||
		route.Slots.TargetRef != "student-notice.docx" {
		t.Fatalf("implicit question with one governed document did not route to document.read: route=%#v fusion=%#v", route, routing.Fusion)
	}
}

func TestAttachedDocumentEditRetainsOwnerRequest(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		writeDOCXParagraphFixture(t, filepath.Join(cfg.root, "report.docx"), []string{
			"五、心得与体会",
			"本次实验完成了 Selenium 自动化测试实践。",
		})
	})
	defer closeRuntime()

	request := "完善心得与体会"
	routing := mustRouteIntentOutput(t, runtime, session.ID, request, []app.MessagePart{{
		Kind: app.MessagePartFile,
		Name: "实验报告.docx",
		Resource: &app.ResourceRef{
			Kind: "workspace_file",
			Ref:  "report.docx",
		},
	}}, app.MessageSourceWeb)
	route := routing.Route
	if route.Status != app.RouteMatched || len(route.CapabilityPath) != 2 ||
		route.CapabilityPath[1] != app.CapabilityDocumentEdit || route.Slots.Query != request ||
		route.Slots.TargetRef != "report.docx" {
		t.Fatalf("attached document edit lost the owner request: route=%#v fusion=%#v", route, routing.Fusion)
	}
}

func TestRecentAttachedDocumentRoutesFollowUpWithoutPriorToolCall(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		writeTestOfficePackage(t, filepath.Join(cfg.root, "student-notice.docx"), "word/document.xml")
	})
	defer closeRuntime()

	storetest.MustAddMessage(t, st, app.Message{
		SessionID: session.ID,
		Role:      "user",
		Content:   "这个里面讲了什么",
		Attachments: []app.MessageAttachment{{
			Name:        "关于通识选修课模块的特别提醒.docx",
			RelPath:     "student-notice.docx",
			ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		}},
	})
	storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "assistant", Content: "这是一份通识选修课说明。"})
	current := "对于23级的选课有什么注意事项"
	storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "user", Content: current})

	routingContext := mustSemanticRoutingContext(t, runtime, session.ID, "run_current", current, nil)
	if !strings.Contains(routingContext, "student-notice.docx") ||
		!strings.Contains(routingContext, "关于通识选修课模块的特别提醒.docx") {
		t.Fatalf("routing context omitted the recent governed document:\n%s", routingContext)
	}

	routing := mustRouteIntentOutput(t, runtime, session.ID, current, nil, app.MessageSourceWeb)
	route := routing.Route
	if route.Status != app.RouteMatched || len(route.CapabilityPath) != 2 ||
		route.CapabilityPath[1] != app.CapabilityDocumentRead || route.Slots.TargetRef != "student-notice.docx" {
		t.Fatalf("follow-up did not recover the recent governed document: route=%#v fusion=%#v", route, routing.Fusion)
	}
}

func TestRecentDocumentToolContextRoutesFollowUpQuestion(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		writeTestOfficePackage(t, filepath.Join(cfg.root, "student-notice.docx"), "word/document.xml")
	})
	defer closeRuntime()

	storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "user", Content: "请看一下这份通识选修课说明"})
	storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "assistant", Content: "已读取说明文档"})
	testSaveToolCall(st, app.ToolCall{
		ID: "tc_student_notice", SessionID: session.ID, RunID: "run_previous",
		Tool: "files.read", Risk: app.RiskRead, Status: "completed",
		Arguments: map[string]any{"path": "student-notice.docx"},
	})

	current := "作为23级学生要注意什么"
	storetest.MustAddMessage(t, st, app.Message{SessionID: session.ID, Role: "user", Content: current})

	context := mustSemanticRoutingContext(t, runtime, session.ID, "run_current", current, nil)
	if !strings.Contains(context, "student-notice.docx") || !strings.Contains(context, "已读取说明文档") ||
		strings.Contains(context, current) {
		t.Fatalf("Fast routing context did not preserve prior evidence or exclude the duplicated current message:\n%s", context)
	}

	routing := mustRouteIntentOutput(t, runtime, session.ID, current, nil, app.MessageSourceWeb)
	route := routing.Route
	if route.Status != app.RouteMatched || len(route.CapabilityPath) != 2 ||
		route.CapabilityPath[1] != app.CapabilityDocumentRead || route.Slots.TargetRef != "student-notice.docx" {
		t.Fatalf("follow-up question did not use recent document context: route=%#v fusion=%#v", route, routing.Fusion)
	}
}

func TestRecentDocumentResolverPrefersDurableRecordMetadata(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	observedAt := time.Now().UTC()
	record := st.SaveDocumentRecord(app.DocumentRecord{
		ID: "doc_latest", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "reports/latest.docx", Name: "最新报告.docx",
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Format:      app.DocumentFormatDOCX, Status: app.DocumentStatusAvailable,
		Source: app.DocumentSourceToolOutput, SourceToolCallID: "tc_edit",
		LastActivity: app.DocumentActivityEdited, LastActivityID: "tc_edit", LastActivityAt: observedAt,
	})
	st.SaveDocumentRecord(app.DocumentRecord{
		ID: "doc_latest_legacy_absolute", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: filepath.Join(session.WorkspaceRoot, "reports", "latest.docx"), Name: "最新报告.docx",
		Source: app.DocumentSourceToolOutput, SourceToolCallID: "tc_edit",
		LastActivity: app.DocumentActivityEdited, LastActivityID: "tc_edit", LastActivityAt: observedAt,
	})
	storetest.MustAddMessage(t, st, app.Message{
		SessionID: session.ID, Role: "user", CreatedAt: observedAt.Add(-time.Minute),
		Attachments: []app.MessageAttachment{{Name: "旧报告.pdf", RelPath: "old.pdf"}},
	})

	resolution := mustResolveDocumentContext(t, runtime, session.ID, "run_current", "继续检查修改后的内容", nil)
	if len(resolution.References) != 1 {
		t.Fatalf("durable record did not become the recent document: %#v", resolution)
	}
	reference := resolution.References[0]
	if reference.DocumentID != record.ID || reference.Ref != record.GovernedPath ||
		reference.Name != record.Name || reference.Format != app.DocumentFormatDOCX ||
		reference.Source != app.DocumentSourceToolOutput || reference.Activity != app.DocumentActivityEdited {
		t.Fatalf("durable document metadata was not preserved for Fast routing: %#v", reference)
	}
}

func TestUnrelatedQuestionDoesNotInheritRecentDocumentTarget(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	st.SaveDocumentRecord(app.DocumentRecord{
		ID: "doc_recent", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "notice.docx", Name: "通知.docx", Format: app.DocumentFormatDOCX,
		Status: app.DocumentStatusAvailable, Source: app.DocumentSourceAttachment,
		LastActivity: app.DocumentActivityRead, LastActivityID: "tc_read", LastActivityAt: time.Now().UTC(),
	})

	thanks := mustRouteIntentOutput(t, runtime, session.ID, "谢谢", nil, app.MessageSourceWeb).Route
	if thanks.Status == app.RouteMatched && len(thanks.CapabilityPath) > 1 && thanks.CapabilityPath[1] == app.CapabilityDocumentRead {
		t.Fatalf("gratitude inherited an unrelated document target: %#v", thanks)
	}
	weather := mustRouteIntentOutput(t, runtime, session.ID, "今天杭州天气", nil, app.MessageSourceWeb).Route
	if weather.Status != app.RouteMatched || len(weather.CapabilityPath) < 2 ||
		weather.CapabilityPath[1] != app.CapabilityBrowserWeather || weather.Slots.Location != "杭州" {
		t.Fatalf("weather question was distorted by recent document context: %#v", weather)
	}
}

func TestRecentDocumentResolverPrefersNewestSource(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	base := time.Now().UTC().Add(-time.Minute)
	toolCompletedAt := base.Add(10 * time.Second)
	testSaveToolCall(st, app.ToolCall{
		ID: "tc_old_document", SessionID: session.ID, RunID: "run_old",
		Tool: "files.read", Status: "completed", StartedAt: base,
		CompletedAt:        &toolCompletedAt,
		Arguments:          map[string]any{"path": "old.docx"},
		ObservationSummary: "Read old.docx.",
	})

	storetest.MustAddMessage(t, st, app.Message{
		SessionID: session.ID, Role: "user", Content: "", CreatedAt: base.Add(20 * time.Second),
		Attachments: []app.MessageAttachment{
			{Name: "first.docx", RelPath: "first.docx"},
			{Name: "second.docx", RelPath: "second.docx"},
		},
	})

	resolution := mustResolveDocumentContext(t, runtime, session.ID, "run_current", "请比较这两份文档", nil)
	if len(resolution.References) != 2 {
		t.Fatalf("latest multi-document message was not preserved as ambiguous: %#v", resolution)
	}
	for _, reference := range resolution.References {
		if reference.Provenance != documentProvenanceRecentMessage {
			t.Fatalf("newer message did not override older tool context: %#v", resolution)
		}
	}
}

func TestRecentDocumentResolverPrefersNewerToolOutput(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	base := time.Now().UTC().Add(-time.Minute)
	storetest.MustAddMessage(t, st, app.Message{
		SessionID: session.ID, Role: "user", Content: "修改这份文档", CreatedAt: base,
		Attachments: []app.MessageAttachment{{Name: "input.docx", RelPath: "input.docx"}},
	})
	toolCompletedAt := base.Add(20 * time.Second)
	testSaveToolCall(st, app.ToolCall{
		ID: "tc_new_output", SessionID: session.ID, RunID: "run_edit",
		Tool: "docx.replace_paragraph", Status: "completed", StartedAt: base.Add(10 * time.Second),
		CompletedAt:        &toolCompletedAt,
		Arguments:          map[string]any{"path": "input.docx", "output_path": "input-sparkclaw-edit.docx"},
		ObservationSummary: "Wrote input-sparkclaw-edit.docx.",
	})

	resolution := mustResolveDocumentContext(t, runtime, session.ID, "run_current", "继续检查修改后的内容", nil)
	if len(resolution.References) != 1 ||
		resolution.References[0].Ref != "input-sparkclaw-edit.docx" ||
		resolution.References[0].Provenance != documentProvenanceRecentTool {
		t.Fatalf("newer tool output did not become the recent document: %#v", resolution)
	}
}

func TestRecentDocumentResolverKeepsCurrentExplicitPathAuthoritative(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	storetest.MustAddMessage(t, st, app.Message{
		SessionID: session.ID, Role: "user", Content: "旧文档",
		Attachments: []app.MessageAttachment{{Name: "old.docx", RelPath: "old.docx"}},
	})

	resolution := mustResolveDocumentContext(t, runtime, session.ID, "run_current", "总结 current.pdf", nil)
	if len(resolution.References) != 1 ||
		resolution.References[0].Ref != "current.pdf" ||
		resolution.References[0].Provenance != documentProvenanceExplicitCurrent {
		t.Fatalf("current explicit path did not override recent context: %#v", resolution)
	}
}

func TestDocumentEditOutputsShareOneTraceableRecentActivity(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	parent := st.SaveDocumentRecord(app.DocumentRecord{
		ID: "doc_parent", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "input.pdf", Name: "input.pdf", Format: app.DocumentFormatPDF,
		Status: app.DocumentStatusAvailable, Source: app.DocumentSourceAttachment,
		LastActivity: app.DocumentActivityConfirmed, LastActivityID: "run_edit", LastActivityAt: time.Now().UTC().Add(-time.Second),
	})
	completedAt := time.Now().UTC()
	call := app.ToolCall{
		ID: "tc_split", SessionID: session.ID, RunID: "run_edit",
		Tool: "pdf.transform", Capability: app.ToolCapabilityDocumentEdit, Status: "completed",
		Arguments: map[string]any{"path": parent.GovernedPath},
		Result:    map[string]any{"outputs": []any{"split-1.pdf", "split-2.pdf"}},
		StartedAt: completedAt.Add(-time.Second), CompletedAt: &completedAt,
	}
	if err := runtime.recordDocumentToolActivity(t.Context(), call); err != nil {
		t.Fatal(err)
	}

	records := st.ListDocumentRecords(session.OwnerID, session.ID, 10)
	if len(records) < 3 || records[0].LastActivityID != call.ID || records[1].LastActivityID != call.ID ||
		records[0].ParentDocumentID != parent.ID || records[1].ParentDocumentID != parent.ID {
		t.Fatalf("multi-output edit did not preserve one traceable activity and lineage: %#v", records)
	}
	resolution := mustResolveDocumentContext(t, runtime, session.ID, "run_follow_up", "比较刚才拆分的结果", nil)
	if len(resolution.References) != 2 {
		t.Fatalf("latest multi-output activity was not preserved as ambiguous: %#v", resolution)
	}
}

func TestResumeContentUsesOriginalUserMessage(t *testing.T) {
	now := time.Now().UTC()
	messages := []app.Message{{
		ID: "m_owner", Role: "user", Content: "今天杭州天气", CreatedAt: now,
	}}
	run := app.AgentRun{StartedAt: now.Add(time.Second)}
	if got := requestContentForRun(messages, run); got != "今天杭州天气" {
		t.Fatalf("resume returned %q, want original owner message", got)
	}
}
