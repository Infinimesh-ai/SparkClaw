package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
)

type embeddedPageReadHost struct {
	url           string
	operations    []string
	navigateError error
	finalURL      string
}

func (*embeddedPageReadHost) Capabilities() browserhost.Capabilities {
	return browserhost.Capabilities{Role: browserhost.ClientEmbedded}
}
func (*embeddedPageReadHost) Acquire(_ context.Context, scope browserhost.Scope) (browserhost.Binding, error) {
	return browserhost.Binding{Scope: scope, HostID: "host", RuntimeGeneration: "runtime", ConnectionEpoch: "epoch", LeaseID: "lease", PageID: "page", PageGeneration: 1, LeaseExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (*embeddedPageReadHost) Renew(_ context.Context, binding browserhost.Binding) (browserhost.Binding, error) {
	return binding, nil
}
func (h *embeddedPageReadHost) Dispatch(_ context.Context, binding browserhost.Binding, _ string, operation string, args map[string]any) (json.RawMessage, error) {
	h.operations = append(h.operations, operation)
	if operation == "navigate" {
		if h.navigateError != nil {
			return nil, h.navigateError
		}
		h.url, _ = args["url"].(string)
		if h.finalURL != "" {
			h.url = h.finalURL
		}
		return json.Marshal(map[string]any{"page_id": binding.PageID, "url": h.url, "title": "Embedded page", "page_generation": binding.PageGeneration})
	}
	if operation == "read" {
		return json.Marshal(map[string]any{"url": h.url, "final_url": h.url, "title": "Embedded page", "text": "Verified embedded content", "rendered": true})
	}
	return nil, browserhost.ErrFence
}

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
			adapter := browserhost.NewScopedAdapter(host, browserhost.Scope{Identity: browserhost.Identity{OwnerID: "owner", ClientID: "client", InstallationID: "installation"}, ConversationID: "conversation", TaskID: "task"})
			runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
				cfg.browserAdapter = adapter
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
func (*embeddedPageReadHost) Release(context.Context, browserhost.Binding) error { return nil }

func TestExecutionEmbeddedPageReadUsesScopedAdapterThroughWorkflow(t *testing.T) {
	host := &embeddedPageReadHost{}
	adapter := browserhost.NewScopedAdapter(host, browserhost.Scope{Identity: browserhost.Identity{OwnerID: "owner", ClientID: "client", InstallationID: "installation"}, ConversationID: "conversation", TaskID: "task"})
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.browserAdapter = adapter
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
