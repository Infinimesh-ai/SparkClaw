package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Uses the unchanged registered model configuration and strict split timeout.
// Three independent calls per case expose variation; failures are retained.
func TestRealConversationRequestSplitAcceptance(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_CONTEXT_ACCEPTANCE") != "1" {
		t.Skip("requires explicitly selected real model configuration")
	}
	filename := os.Getenv("SPARKCLAW_INTENT_CALIBRATION_CONFIG")
	if filename == "" {
		t.Fatal("private model configuration is required")
	}
	cfg, err := config.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Mock = false
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	catalog := capability.MustDefaultCatalog()
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	r := Runtime{store: store.NewMemoryStore(), models: modelrouter.New(cfg), capabilities: catalog, semanticRouter: newSemanticIntentRouter(catalog.Revision(), graph)}
	documents := documentContextResolution{References: []documentContextReference{{HandleID: "document", Ref: "conversation-files/document/report.md", Name: "report.md", SHA256: clarification.TextDigest("# Report\nSource content"), Size: 23, Format: app.DocumentFormatText, SourceID: "prior"}}}
	history := agentContextSnapshot{Messages: []app.Message{{ID: "prior", Role: "assistant", Content: "报告描述了文件身份、失败恢复和验收步骤。"}}}
	prompt := newTreeRoutingPromptContext(nil, history, documents)
	t.Logf("graph=%s catalog=%s calibration=%s model_config_sha256=%s mock=false", graph.Revision(), catalog.Revision(), r.semanticRouter.calibration.Revision, clarification.TextDigest(string(raw)))
	cases := []struct {
		name, text, business, mode, object string
		deferred                           bool
		provider, recipient                string
		joined                             string
	}{
		{"business_and_current", "完善一下发给我", "完善一下", "current_route", "business_result", false, "", "", ""},
		{"pure_history_file", "把刚才的文件发给我", "", "current_route", "conversation_file", false, "", "", ""},
		{"pure_assistant_text", "把上面的总结发给我", "", "current_route", "assistant_text", false, "", "", ""},
		{"explicit_external_business", "总结这份报告，通过 qq 发给张三", "总结这份报告", "explicit_external", "business_result", false, "qq", "张三", ""},
		{"deferred_whole_request", "明天上午九点把刚才的文件发给我", "明天上午九点把刚才的文件发给我", "none", "business_result", true, "", "", ""},
		{"ordinary_business", "读取 report.md 并概括内容", "读取 report.md 并概括内容", "none", "business_result", false, "", "", ""},
		// External sends name a channel, a recipient or both; Message Control asks
		// for the missing part and the owner approves the exact send.
		{"external_provider_only", "总结这份报告，通过微信发出去", "总结这份报告", "explicit_external", "business_result", false, "微信", "", ""},
		{"external_recipient_only", "总结这份报告发给张三", "总结这份报告", "explicit_external", "business_result", false, "", "张三", ""},
		{"external_pure_history_file", "通过微信把刚才的文件发给张三", "", "explicit_external", "conversation_file", false, "微信", "张三", ""},
		// A constraint or purpose outside the business keeps the whole request as
		// business so nothing is dropped; the target still comes from the spans.
		{"external_with_constraint", "总结这份报告，通过微信发给张三，不要附带草稿", "总结这份报告，通过微信发给张三，不要附带草稿", "explicit_external", "business_result", false, "微信", "张三", "总结这份报告，不要附带草稿"},
		{"external_with_purpose", "Summarize this report and send it via email to Alice to review", "Summarize this report and send it via email to Alice to review", "explicit_external", "business_result", false, "email", "Alice", ""},
		{"external_english", "Summarize this report and send it via email to Alice", "Summarize this report", "explicit_external", "business_result", false, "email", "Alice", ""},
	}
	for _, tc := range cases {
		for attempt := 1; attempt <= 3; attempt++ {
			started := time.Now()
			split, err := r.splitIntentRequest(t.Context(), "acceptance", tc.name, tc.text, app.MessageSourceWeb, prompt, false, time.Duration(r.semanticRouter.calibration.SplitTimeoutMS)*time.Millisecond)
			t.Logf("case=%s repeat=%d split_ms=%d business=%q mode=%s object=%s deferred=%t error=%v", tc.name, attempt, time.Since(started).Milliseconds(), split.BusinessSpan, split.DeliveryMode, split.DeliveryObjectKind, split.Deferred, err)
			t.Logf("case=%s repeat=%d provider=%q recipient=%q object_span=%q", tc.name, attempt, split.ProviderSpan, split.RecipientSpan, split.ObjectSpan)
			// A separated business may be joined; otherwise the whole request is kept.
			business := split.BusinessSpan
			if split.WholeRequest && tc.joined != "" {
				business = tc.joined
			}
			if err != nil || (business != tc.business && business != tc.joined) || split.DeliveryMode != tc.mode || split.DeliveryObjectKind != tc.object || split.Deferred != tc.deferred ||
				strings.TrimSpace(split.ProviderSpan) != tc.provider || strings.TrimSpace(split.RecipientSpan) != tc.recipient {
				t.Errorf("case=%s repeat=%d split mismatch: %+v %v", tc.name, attempt, split, err)
			}
			if err == nil {
				if _, err := splitDeliveryDirective(split); err != nil {
					t.Errorf("case=%s delivery directive: %v", tc.name, err)
				}
			}
		}
	}
}
