package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestWebsiteURLQuestionRouting(t *testing.T) {
	for _, text := range []string{
		"苹果官网的网址是什么？", "请查一下 Apple 中国大陆官网首页网址", "What is the official URL for Apple UK?",
		"Apple 官网（不要美国站，要中国大陆站）的网址是什么？", "要访问 Apple 中国大陆官网，网址是什么？",
	} {
		if !isWebsiteURLQuestion(text) {
			t.Errorf("%q did not route to a URL answer", text)
		}
	}
	for _, text := range []string{"查一下苹果官网最新价格", "打开苹果官网链接", "帮我看看文档里的链接", "苹果官网是什么", "打开苹果官网并点击 Mac"} {
		if isWebsiteURLQuestion(text) {
			t.Errorf("%q was intercepted as a URL question", text)
		}
	}
}

func TestWebsiteURLLookupCallsModelOnce(t *testing.T) {
	const answer = "Apple 官网（不要美国站，要中国大陆站）的网址是什么？"
	const unknown = `{"status":"unknown","url":""}`
	for _, tc := range []struct {
		name       string
		operation  app.RouteOperation
		request    string
		wantTarget string
		wantAsk    string
		output     string
		wantURL    string
	}{
		{"found", app.RouteOperationAnswer, answer, answer, "完整网址", `{"status":"found","url":"https://8.8.8.8/example"}`, "https://8.8.8.8/example"},
		{"unknown", app.RouteOperationAnswer, answer, answer, "完整网址", unknown, ""},
		{"private destination", app.RouteOperationAnswer, answer, answer, "完整网址", `{"status":"found","url":"https://127.0.0.1/"}`, ""},
		{"unexpected fields", app.RouteOperationAnswer, answer, answer, "完整网址", `{"status":"found","url":"https://8.8.8.8/","explanation":"guess"}`, ""},
		{"country constraint", app.RouteOperationOpen, "打开 Apple 官网（不要美国站，要中国大陆站）", "打开 Apple 官网（不要美国站，要中国大陆站）", "要打开描述中指定的页面", unknown, ""},
		{"open before click", app.RouteOperationInteract, "打开 Apple 的中国大陆官网并点击 Mac", "打开 Apple 的中国大陆官网并点击 Mac", "在点击之前，用户要求先打开的页面网址是什么", unknown, ""},
		{"read subpage", app.RouteOperationRead, "阅读 Apple 中国大陆官网的 iPhone 产品页面", "阅读 Apple 中国大陆官网的 iPhone 产品页面", "要阅读描述中指定的页面", unknown, ""},
		{"draft omits field value", app.RouteOperationDraft, "在 Apple 中国大陆官网的联系表单填写私人姓名", "在 Apple 中国大陆官网的联系表单", "填写表单之前", unknown, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				var body struct {
					Messages       []map[string]string `json:"messages"`
					ResponseFormat struct {
						Type   string `json:"type"`
						Schema struct {
							Name   string `json:"name"`
							Strict bool   `json:"strict"`
						} `json:"json_schema"`
					} `json:"response_format"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if len(body.Messages) != 2 {
					t.Errorf("expected only lookup instructions and question, got %#v", body.Messages)
					return
				}
				prompt := body.Messages[1]["content"]
				ask, description, ok := strings.Cut(prompt, "\n目标描述：")
				var original string
				if !ok || !strings.Contains(ask, tc.wantAsk) || json.Unmarshal([]byte(description), &original) != nil || original != tc.wantTarget {
					t.Errorf("lookup did not ask for the right address with the preserved target: %q", prompt)
				}
				if body.ResponseFormat.Type != "json_schema" || body.ResponseFormat.Schema.Name != "website_url_lookup" || !body.ResponseFormat.Schema.Strict {
					t.Errorf("expected strict URL response contract: %#v", body.ResponseFormat)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": tc.output}}}})
			}))
			defer server.Close()
			cfg := configtest.MustLoadDefault()
			cfg.Model.Mock = false
			cfg.Model.Fast.BaseURL = server.URL
			state := store.NewMemoryStore()
			runtime := Runtime{store: state, models: modelrouter.New(cfg), capabilities: capability.MustDefaultCatalog()}
			input := app.RouteDecision{Status: app.RouteMatched, Slots: app.RouteSlots{
				Operation: tc.operation, TargetKind: string(app.TargetKindPublicNamedTarget), TargetRef: tc.request,
			}}
			if tc.operation == app.RouteOperationAnswer {
				input = runtime.websiteURLAnswerRoute(tc.request)
			}
			run := app.AgentRun{ID: "run", SessionID: "session"}
			route, err := runtime.resolveWebsiteURL(t.Context(), run, input)
			if err != nil || calls.Load() != 1 || route.Facts["url"] != tc.wantURL {
				t.Fatalf("route=%#v calls=%d error=%v", route, calls.Load(), err)
			}
			if tc.wantURL == "" && (route.Status != app.RouteClarify || route.Reason != websiteURLNeedsInput) || tc.wantURL != "" && route.Status != app.RouteMatched {
				t.Fatalf("unexpected route for model result: %#v", route)
			}
		})
	}
}

func TestWebsiteURLClarificationAcceptsOwnerLink(t *testing.T) {
	for _, tc := range []struct {
		name, request string
		open          bool
	}{
		{"answer", "苹果官网的网址是什么？", false},
		{"answer with region constraint", "Apple 官网（不要美国站，要中国大陆站）的网址是什么？", false},
		{"navigate", "打开苹果官网", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, state, session, closeTools := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
				cfg.config.Tools.BrowserAutomation.Enabled = true
				cfg.browser = true
			})
			defer closeTools()
			first, err := runtime.HandleMessage(t.Context(), session.ID, tc.request)
			if err != nil || first.Run.State != "clarification_required" || !strings.Contains(first.Message.Content, "请提供完整的网址") || len(first.ToolCalls) != 0 {
				t.Fatalf("first = %#v, error = %v", first, err)
			}
			if calls := testListModelCalls(state, session.ID, first.Run.ID); countModelCalls(calls, "website_url_lookup", "fast") != 1 {
				t.Fatalf("expected one URL lookup, got %#v", calls)
			}
			const link = "https://example.com/"
			second, err := runtime.HandleMessage(t.Context(), session.ID, link)
			if err != nil || second.Run.State != "completed" || second.RouteDecision.Facts[websiteURLSourceFact] != "owner" {
				t.Fatalf("follow-up = %#v, route = %+v, error = %v", second, second.RouteDecision, err)
			}
			if calls := testListModelCalls(state, session.ID, second.Run.ID); countModelCalls(calls, "website_url_lookup", "fast") != 0 {
				t.Fatalf("owner-supplied URL triggered a new lookup: %#v", calls)
			}
			if tc.open {
				if second.Run.Workflow.Plan.ProfileID != app.WorkflowBrowserAutomation || second.RouteDecision.Slots.TargetRef != link || len(second.ToolCalls) == 0 {
					t.Fatalf("follow-up did not open the supplied URL: %#v", second)
				}
			} else if second.Message.Content != link || len(second.ToolCalls) != 0 {
				t.Fatalf("URL answer did not return the supplied URL alone: %#v", second)
			}
		})
	}
}

func TestWebsiteURLQuestionsIgnoreContextReferences(t *testing.T) {
	for _, text := range []string{
		"当前页面的网址是什么", "当前标签页的URL", "what is the current page's url", "上一条消息里的链接", "我昨天发你的链接",
		"这篇文章的链接", "邀请链接", "网盘下载链接", "give me the link to my last order", "刚才那个网站的网址是什么？",
	} {
		if isWebsiteURLQuestion(text) {
			t.Errorf("%q was answered from model knowledge instead of its context", text)
		}
	}
}

func TestWebsiteURLLookupOutageSaysTheModelIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	runtime := Runtime{store: store.NewMemoryStore(), models: modelrouter.New(cfg), capabilities: capability.MustDefaultCatalog()}
	for _, input := range []app.RouteDecision{
		runtime.websiteURLAnswerRoute("苹果官网的网址是什么？"),
		{Status: app.RouteMatched, Slots: app.RouteSlots{Operation: app.RouteOperationOpen, TargetKind: string(app.TargetKindPublicNamedTarget), TargetRef: "打开苹果官网"}},
	} {
		route, err := runtime.resolveWebsiteURL(t.Context(), app.AgentRun{ID: "run", SessionID: "session"}, input)
		if err != nil || route.Status != app.RouteBlocked || routeReasonCode(route.Reason) != "model_unavailable" {
			t.Fatalf("outage route = %#v, error = %v", route, err)
		}
		if got := routingFailureMessages[routeReasonCode(route.Reason)].text(true, false); got != "模型暂时不可用，请稍后重试。" {
			t.Fatalf("outage reply = %q", got)
		}
	}
}

func TestWebsiteURLQuestionOutageRepliesThatTheModelIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	runtime, _, session, closeTools := newWorkflowE2ERuntime(t, nil)
	defer closeTools()
	cfg := agentTestConfig()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	runtime.models = modelrouter.New(cfg)
	result, err := runtime.HandleMessage(t.Context(), session.ID, "苹果官网的网址是什么？")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.State != "blocked" || result.Message.Content != "模型暂时不可用，请稍后重试。" {
		t.Fatalf("state %q message %q route %#v", result.Run.State, result.Message.Content, result.RouteDecision)
	}
}
