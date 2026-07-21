package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestEveryRoutedMessagePersistsCanonicalRequest(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()

	result, err := runtime.HandleMessage(context.Background(), session.ID, `帮我看看现在的状态
MOCK_NORMALIZATION_RESPONSE:{"canonical_request":"请检查当前状态并给出简洁结论"}
MOCK_REACT_RESPONSE:{"type":"final","answer":"状态正常"}`)
	if err != nil {
		t.Fatal(err)
	}
	request := result.Run.MessageContext.Request
	if request.SchemaVersion != app.RequestNormalizationSchemaVersion || request.Source != requestNormalizationFastModel ||
		request.Canonical != "请检查当前状态并给出简洁结论\nMOCK_REACT_RESPONSE:{\"type\":\"final\",\"answer\":\"状态正常\"}" {
		t.Fatalf("canonical request was not frozen on the run: %#v", request)
	}
	if !hasModelCallOperation(st.ListModelCalls(session.ID, result.Run.ID), "request_normalization", "fast") {
		t.Fatalf("request normalization model call was not persisted: %#v", st.ListModelCalls(session.ID, result.Run.ID))
	}
	if !hasAgentAuditType(st.ListAudit(session.ID), "message.request.normalized") {
		t.Fatalf("request normalization audit is missing: %#v", st.ListAudit(session.ID))
	}
}

func TestRequestNormalizationRejectsChangedDeterministicFacts(t *testing.T) {
	tests := []struct {
		name      string
		original  string
		candidate string
	}{
		{name: "url", original: "Open https://example.com/a", candidate: "Open https://example.com/b"},
		{name: "path", original: "Read `notes/a.txt`", candidate: "Read `notes/b.txt`"},
		{name: "number", original: "Return 5 results", candidate: "Return 10 results"},
		{name: "quoted literal", original: `Replace "alpha"`, candidate: `Replace "beta"`},
		{name: "negation", original: "Do not delete the file", candidate: "Delete the file"},
		{name: "language translation", original: "最新国家人工智能大会讲了什么", candidate: "Summarize the latest national artificial intelligence conference"},
		{name: "risk widening", original: "Read the file", candidate: "Delete the file"},
		{name: "delivery target", original: "通过微信发送给张三", candidate: "通过微信发送给李四"},
		{name: "delivery provider invention", original: "发送给张三", candidate: "通过微信发送给张三"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if requestNormalizationPreservesFacts(test.original, test.candidate, currentSearchDate()) {
				t.Fatalf("changed deterministic fact was accepted: original=%q candidate=%q", test.original, test.candidate)
			}
		})
	}
	if !requestNormalizationPreservesFacts(
		`Open https://example.com/a and return 5 results containing "alpha"`,
		`Please open https://example.com/a and return exactly 5 results containing "alpha".`,
		currentSearchDate(),
	) {
		t.Fatal("professional rewrite that preserved deterministic facts was rejected")
	}
	if !requestNormalizationPreservesFacts("今天杭州天气", "查询今天杭州天气 2026年7月17日", "2026-07-17") {
		t.Fatal("normalization rejected the supplied current date for a relative-time request")
	}
	if requestNormalizationPreservesFacts("今天杭州天气", "查询今天杭州天气 2026年7月18日", "2026-07-17") {
		t.Fatal("normalization accepted a date that differs from the supplied current date")
	}
}

func TestRequestNormalizationFallsBackWhenModelTranslatesChineseRequest(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	original := "最新国家人工智能大会讲了什么"
	request := runtime.normalizeOwnerRequest(context.Background(), session.ID, "run_normalization_language_fallback", original+`
MOCK_NORMALIZATION_RESPONSE:{"canonical_request":"Summarize the key topics from the latest National Artificial Intelligence Conference"}`, "", currentSearchDate())
	want := original + " " + currentSearchDate()
	if request.Source != requestNormalizationFallback || request.Canonical != want {
		t.Fatalf("translated normalization did not fall back to the Chinese request: got %#v want %q", request, want)
	}
}

func TestFinalAnswerLanguageUsesOriginalRequestInsteadOfCanonicalQuery(t *testing.T) {
	run := app.AgentRun{MessageContext: &app.MessageRunContext{Request: app.RequestNormalization{
		Original:  "最新国家人工智能大会讲了什么",
		Canonical: "Summarize the latest National Artificial Intelligence Conference",
	}}}
	goal := finalAnswerGoal(run, run.MessageContext.Request.Canonical)
	if goal != run.MessageContext.Request.Original {
		t.Fatalf("final answer language source was %q, want original request %q", goal, run.MessageContext.Request.Original)
	}
	instruction := finalAnswerLanguageInstruction(goal)
	if !strings.Contains(instruction, "entire final answer in Chinese") || !strings.Contains(instruction, "translating non-Chinese evidence") {
		t.Fatalf("Chinese final answer instruction is incomplete: %q", instruction)
	}
}

func TestRequestNormalizationFallsBackWhenModelChangesURL(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	request := runtime.normalizeOwnerRequest(context.Background(), session.ID, "run_normalization_fallback", `Open https://example.com/a
MOCK_NORMALIZATION_RESPONSE:{"canonical_request":"Open https://example.com/b"}`, "", currentSearchDate())
	if request.Source != requestNormalizationFallback || request.Canonical != "Open https://example.com/a" {
		t.Fatalf("unsafe normalization did not fall back to the original deterministic facts: %#v", request)
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
MOCK_REACT_RESPONSE:{"type":"action","tool":"files.read","arguments":{"path":"note.txt"}}`, []MessageAttachment{{
		Name: "note.txt", RelPath: "note.txt", ContentType: "text/plain",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched || result.RouteDecision.Slots.TargetRef != "note.txt" {
		t.Fatalf("structured attachment did not ground the document route: %#v", result.RouteDecision)
	}
	if result.Run.MessageContext.Request.ResourceContext == "" || strings.Contains(semanticRoutingContent(result.Run.MessageContext.Request.Canonical), "note.txt") {
		t.Fatalf("resource context leaked into canonical owner text: %#v", result.Run.MessageContext.Request)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Arguments["path"] != "note.txt" {
		t.Fatalf("workflow did not freeze the structured attachment path: %#v", result.ToolCalls)
	}
}

func TestAttachmentOnlyDocumentStillRoutesFromStructuredResource(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		if err := os.WriteFile(filepath.Join(cfg.root, "note.txt"), []byte("attachment evidence"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	defer closeRuntime()
	route, err := runtime.recognizeCapabilityRouteWithResources(session.ID, "turn_attachment_only", "", []app.MessagePart{{
		Kind: app.MessagePartFile, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: "note.txt"},
	}}, agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if route.Status != app.RouteMatched || route.Slots.TargetRef != "note.txt" {
		t.Fatalf("attachment-only document route regressed: %#v", route)
	}
}

func TestResumeContentUsesPersistedCanonicalRequest(t *testing.T) {
	run := app.AgentRun{MessageContext: &app.MessageRunContext{Request: app.RequestNormalization{
		SchemaVersion: app.RequestNormalizationSchemaVersion,
		Original:      "今天杭州天气",
		Canonical:     "查询今天杭州天气 " + currentSearchDate(),
		Source:        requestNormalizationFastModel,
	}}}
	got := requestContentForRun(nil, run)
	if got != run.MessageContext.Request.Canonical {
		t.Fatalf("resume returned %q, want frozen canonical request %q", got, run.MessageContext.Request.Canonical)
	}
}
