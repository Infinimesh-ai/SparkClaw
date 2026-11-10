package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
)

type embeddedPageReadHost struct {
	url           string
	operations    []string
	navigateError error
	finalURL      string
}

func (*embeddedPageReadHost) Health(context.Context, map[string]any) (browserautomation.Result, error) {
	return browserautomation.Result{Tool: "browser.status", Output: map[string]any{
		"ok": true, "configured": true, "status": "ready", "role": "client_embedded",
	}, Provider: "browser_host", Untrusted: true}, nil
}

func (h *embeddedPageReadHost) Call(_ context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	if tool != "browser.open" && tool != "browser.navigate" {
		return browserautomation.Result{}, errors.New("unexpected browser tool")
	}
	h.operations = append(h.operations, "navigate")
	if h.navigateError != nil {
		return browserautomation.Result{}, h.navigateError
	}
	h.url, _ = args["url"].(string)
	if h.finalURL != "" {
		h.url = h.finalURL
	}
	presentation, _ := args["presentation"].(string)
	page := map[string]any{"page_id": "page", "url": h.url, "title": "Embedded page", "selected": true,
		"session_generation": 1, "page_generation": 1, "provider_session_ref": "browser_host:lease",
		"owner_id": "owner", "profile_id": "client_embedded", "presentation": presentation}
	return browserautomation.Result{Tool: tool, RawTool: "navigate", Output: page, Pages: []any{page},
		SessionGeneration: 1, Provider: "browser_host", Presentation: presentation, Untrusted: true}, nil
}

func (h *embeddedPageReadHost) ReadPage(_ context.Context, target string, args map[string]any) (browserautomation.PageReadResult, error) {
	h.operations = append(h.operations, "read")
	if target != h.url || args["reuse_active_page"] != true {
		return browserautomation.PageReadResult{}, errors.New("read must reuse the opened page")
	}
	return browserautomation.PageReadResult{URL: target, FinalURL: target, Title: "Embedded page",
		Text: "Verified embedded content", Rendered: true, SessionGeneration: 1, Provider: "browser_host",
		ReadSource: "client_embedded_webcontents", Untrusted: true, Actions: []string{}}, nil
}

func (*embeddedPageReadHost) Close() error { return nil }

func TestEmbeddedPageReadPreservesNavigationFailureAndRejectsDifferentFinalRoute(t *testing.T) {
	for _, test := range []struct {
		name     string
		code     app.ToolErrorCode
		finalURL string
		want     string
	}{
		{"dns", app.ToolErrorBrowserDNSResolutionFailed, "", "browser dns resolution failed"},
		{"native_load", app.ToolErrorBrowserNavigationFailed, "", "browser navigation failed"},
		{"deadline", app.ToolErrorBrowserCommandTimeout, "", "browser command timeout"},
		{"different_path", "", "https://example.com/other", "browser open failed"},
		{"different_origin", "", "https://other.test/article", "browser open failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := &embeddedPageReadHost{finalURL: test.finalURL}
			if test.code != "" {
				host.navigateError = &app.CodedToolError{Code: test.code, Err: errors.New("bounded navigation failure")}
			}
			runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
				cfg.browserAdapter = host
				cfg.config.Tools.BrowserAutomation.Enabled = true
				cfg.config.Security.BrowserReadAllowHosts = []string{"example.com"}
			})
			defer closeRuntime()
			result, err := runtime.HandleMessage(t.Context(), session.ID, "Read and summarize https://example.com/article")
			if err != nil {
				t.Fatal(err)
			}
			if result.Run.Workflow == nil || result.Run.Workflow.Status == app.WorkflowStatusSucceeded || !strings.Contains(result.Message.Content, test.want) {
				t.Fatalf("failure was erased: %q workflow=%+v", result.Message.Content, result.Run.Workflow)
			}
			calls := toolCallsForRun(testListToolCalls(st, session.ID), result.Run.ID)
			if len(calls) != 2 || !reflect.DeepEqual(host.operations, []string{"navigate"}) {
				t.Fatalf("failed open advanced or retried: calls=%+v operations=%v", calls, host.operations)
			}
		})
	}
}

func TestExecutionEmbeddedPageReadUsesOpenedPageThroughWorkflow(t *testing.T) {
	host := &embeddedPageReadHost{}
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.browserAdapter = host
		cfg.config.Tools.BrowserAutomation.Enabled = true
		cfg.config.Security.BrowserReadAllowHosts = []string{"example.com"}
	})
	defer closeRuntime()
	result, err := runtime.HandleMessage(context.Background(), session.ID, "Read and summarize https://example.com/article")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.Workflow == nil || result.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		t.Fatalf("scoped embedded page read blocked after navigation: %#v", toolCallsForRun(testListToolCalls(st, session.ID), result.Run.ID))
	}
	calls := toolCallsForRun(testListToolCalls(st, session.ID), result.Run.ID)
	if len(calls) != 3 || calls[1].Tool != "browser.open" || calls[2].Tool != "browser.read" {
		t.Fatalf("expected fixed status/open/read chain, got %#v", calls)
	}
	if !reflect.DeepEqual(host.operations, []string{"navigate", "read"}) {
		t.Fatalf("workflow must read the opened page without a second navigation: %v", host.operations)
	}
}
