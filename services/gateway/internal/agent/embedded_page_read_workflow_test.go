package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
)

type embeddedPageReadHost struct {
	url        string
	operations []string
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
		h.url, _ = args["url"].(string)
		return json.Marshal(map[string]any{"page_id": binding.PageID, "url": h.url, "title": "Embedded page", "page_generation": binding.PageGeneration})
	}
	if operation == "read" {
		return json.Marshal(map[string]any{"url": h.url, "final_url": h.url, "title": "Embedded page", "text": "Verified embedded content", "rendered": true})
	}
	return nil, browserhost.ErrFence
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
	if len(host.operations) == 0 || host.operations[len(host.operations)-1] != "read" {
		t.Fatalf("workflow did not read the acquired embedded page: %v", host.operations)
	}
}
