package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestSplitIntentRequestRepairAndPromptBoundary(t *testing.T) {
	valid := `{"deferred":false,"business_span":"完善一下","delivery_mode":"current_route","delivery_object_kind":"business_result","object_span":"","provider_span":"","recipient_span":""}`
	cases := []struct {
		name      string
		responses []string
		calls     int32
		success   bool
	}{
		{"valid", []string{valid}, 1, true},
		{"repair", []string{`{"deferred":false}`, valid}, 2, true},
		{"invalid twice", []string{`{}`, `{}`}, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				messages, _ := json.Marshal(body["messages"])
				if strings.Contains(string(messages), "Semantic graph:") || strings.Contains(string(messages), "registered candidates") {
					t.Error("split prompt exposed scoring graph or contract")
				}
				responseFormat, _ := body["response_format"].(map[string]any)
				schema, _ := responseFormat["json_schema"].(map[string]any)
				if responseFormat["type"] != "json_schema" || schema["strict"] != true {
					t.Errorf("missing strict schema: %#v", responseFormat)
				}
				index := int(calls.Add(1)) - 1
				if index >= len(tc.responses) {
					index = len(tc.responses) - 1
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": tc.responses[index]}}}})
			}))
			defer server.Close()
			cfg := configtest.MustLoadDefault()
			cfg.Model.Mock = false
			cfg.Model.Fast.BaseURL = server.URL
			cfg.Model.Fast.Name = "sparkclaw-fast"
			cfg.Model.Fast.Model = "Qwen/Fast"
			runtime := Runtime{store: store.NewMemoryStore(), models: modelrouter.New(cfg)}
			result, err := runtime.splitIntentRequest(t.Context(), "session", "run", "完善一下发给我", app.MessageSourceWeb, treeRoutingPromptContext{}, false, 5*time.Second)
			if (err == nil) != tc.success {
				t.Fatalf("error=%v result=%#v", err, result)
			}
			if calls.Load() != tc.calls {
				t.Fatalf("calls=%d want=%d", calls.Load(), tc.calls)
			}
			if tc.success && result.BusinessSpan != "完善一下" {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func scriptedSplitRuntime(t *testing.T, responses ...string) (Runtime, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		index := int(calls.Add(1)) - 1
		if index >= len(responses) {
			index = len(responses) - 1
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": responses[index]}}}})
	}))
	t.Cleanup(server.Close)
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	cfg.Model.Fast.Name = "sparkclaw-fast"
	cfg.Model.Fast.Model = "Qwen/Fast"
	return Runtime{store: store.NewMemoryStore(), models: modelrouter.New(cfg)}, &calls
}

// Separated business pieces that together with the target cover the request
// are scored as the joined business, exactly as safe as an ordinary split.
func TestExternalSendJoinsSeparatedBusinessPieces(t *testing.T) {
	text := "总结这份报告，通过微信发给张三，不要附带草稿"
	runtime, _ := scriptedSplitRuntime(t, `{"deferred":false,"business_span":"总结这份报告，不要附带草稿","delivery_mode":"explicit_external","delivery_object_kind":"business_result","object_span":"","provider_span":"微信","recipient_span":"张三"}`)
	result, err := runtime.splitIntentRequest(t.Context(), "session", "run", text, app.MessageSourceWeb, treeRoutingPromptContext{}, false, 5*time.Second)
	if err != nil || result.WholeRequest || result.BusinessSpan != "总结这份报告，不要附带草稿" || result.ProviderSpan != "微信" || result.RecipientSpan != "张三" {
		t.Fatalf("separated business was not joined: %#v %v", result, err)
	}
	// A joined business that drops or invents text is not accepted as joined.
	for _, business := range []string{"总结这份报告，附带草稿", "总结这份报告，不要附带草稿，加上封面"} {
		if _, ok := joinedBusinessExternalSplit(text, requestSplit{BusinessSpan: business, DeliveryMode: "explicit_external", DeliveryObjectKind: "business_result", ProviderSpan: "微信", RecipientSpan: "张三"}); ok {
			t.Fatalf("joined business %q was accepted", business)
		}
	}
}

// A constraint outside the business span used to fail the split and silently
// return an external send to this conversation. The whole request now stays the
// business, with the verbatim channel and recipient as the delivery target.
func TestExternalSendWithConstraintKeepsWholeRequest(t *testing.T) {
	for _, tc := range []struct{ text, output, provider, recipient string }{
		{"总结这份报告，通过微信发给张三，不要附带草稿",
			`{"deferred":false,"business_span":"总结这份报告","delivery_mode":"explicit_external","delivery_object_kind":"business_result","object_span":"","provider_span":"微信","recipient_span":"张三"}`, "微信", "张三"},
		{"Summarize this report and send it via email to Alice to review",
			`{"deferred":false,"business_span":"Summarize this report","delivery_mode":"explicit_external","delivery_object_kind":"business_result","object_span":"this report","provider_span":"email","recipient_span":"Alice"}`, "email", "Alice"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			runtime, calls := scriptedSplitRuntime(t, tc.output)
			result, err := runtime.splitIntentRequest(t.Context(), "session", "run", tc.text, app.MessageSourceWeb, treeRoutingPromptContext{}, false, 5*time.Second)
			if err != nil || calls.Load() != 2 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
			if !result.WholeRequest || result.BusinessSpan != tc.text || result.DeliveryMode != "explicit_external" ||
				result.DeliveryObjectKind != "business_result" || result.ProviderSpan != tc.provider || result.RecipientSpan != tc.recipient {
				t.Fatalf("constrained external send lost its business or target: %#v", result)
			}
			directive, err := splitDeliveryDirective(result)
			if err != nil || !directive.ExplicitExternal {
				t.Fatalf("whole request lost its external directive: %#v %v", directive, err)
			}
		})
	}
}

// External sends that cannot be kept whole must ask, never fall back to an
// ordinary reply in this conversation.
func TestIncompleteExternalSplitAsksInsteadOfReplyingHere(t *testing.T) {
	for _, tc := range []struct{ name, text, output string }{
		{"pure delivery with constraint", "通过微信把刚才的文件发给张三，不要带封面",
			`{"deferred":false,"business_span":"","delivery_mode":"explicit_external","delivery_object_kind":"conversation_file","object_span":"刚才的文件","provider_span":"微信","recipient_span":"张三"}`},
		{"invented recipient", "总结这份报告，通过微信发出去，不要附带草稿",
			`{"deferred":false,"business_span":"总结这份报告","delivery_mode":"explicit_external","delivery_object_kind":"business_result","object_span":"","provider_span":"微信","recipient_span":"李四"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, _ := scriptedSplitRuntime(t, tc.output)
			result, err := runtime.splitIntentRequest(t.Context(), "session", "run", tc.text, app.MessageSourceWeb, treeRoutingPromptContext{}, false, 5*time.Second)
			if !errors.Is(err, errExternalRequestIncomplete) || result.WholeRequest {
				t.Fatalf("incomplete external send was not reported: %#v %v", result, err)
			}
		})
	}
	runtime, _ := scriptedSplitRuntime(t, `{}`)
	if _, err := runtime.splitIntentRequest(t.Context(), "session", "run", "完善一下发给我", app.MessageSourceWeb, treeRoutingPromptContext{}, false, 5*time.Second); err == nil || errors.Is(err, errExternalRequestIncomplete) {
		t.Fatalf("a non-external split failure became an external clarification: %v", err)
	}
}

func TestRouterAsksWhenExternalSplitIsIncomplete(t *testing.T) {
	runtime, st, session := defaultWorkflowRuntime(t)
	scripted, _ := scriptedSplitRuntime(t, `{"deferred":false,"business_span":"","delivery_mode":"explicit_external","delivery_object_kind":"conversation_file","object_span":"刚才的文件","provider_span":"微信","recipient_span":"张三"}`)
	runtime.models = scripted.models
	envelope := app.MessageRunContext{
		Source: app.MessageSourceContext{Kind: app.MessageSourceWeb}, OwnerID: session.OwnerID,
		Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID},
		ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: app.EndpointID("session:" + session.ID)},
	}
	testSaveRun(st, app.AgentRun{ID: "run_incomplete_external", SessionID: session.ID, StartedAt: time.Now().UTC(), MessageContext: &envelope})
	routing, err := runtime.routeIntent(t.Context(), session.ID, "run_incomplete_external", "通过微信把刚才的文件发给张三，不要带封面")
	if err != nil {
		t.Fatal(err)
	}
	if routing.Route.Status != app.RouteClarify || routing.Route.Reason != "delivery_split_unavailable" || routing.Delivery.ExplicitExternal {
		t.Fatalf("incomplete external send was not turned into a question: %#v", routing)
	}
}
