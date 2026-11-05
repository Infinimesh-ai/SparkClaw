package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestScopedFinalPromptUsesCumulativePDFCoverage(t *testing.T) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	requests := make(chan []message, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- request.Messages
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Completed summary."}}}})
	}))
	defer server.Close()
	cfg := agentTestConfig()
	cfg.Model.Mock = true
	cfg.Model.Fast.BaseURL = server.URL
	cfg.Workspaces.DefaultRoot = t.TempDir()
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	st := store.NewMemoryStore()
	hub := toolhub.New(cfg, st)
	defer hub.Close()
	runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	cfg.Model.Mock = false
	runtime.models = modelrouter.New(cfg)
	for _, revision := range []int{6, 5} {
		state := scopedReadState()
		state.Plan.ProfileRevision = revision
		state.Route.Facts["document_format"] = app.DocumentFormatPDF
		state.Route.Facts[documentReadFactPrefix+"scope_status"] = "complete"
		state.Route.Facts[documentReadFactPrefix+"scope_selector"] = `{"all":true}`
		state.Route.Facts[documentReadFactPrefix+"task_mode"] = "whole_summary"
		run := app.AgentRun{ID: app.NewID("run"), SessionID: "session", Workflow: state}
		calls := []app.ToolCall{
			{ID: "first", Tool: "document.read_range", Capability: app.ToolCapabilityDocumentReadRange, Status: app.ToolCallStatusCompleted, Result: map[string]any{"kind": "pdf", "content": "First required page.", "read_complete": false}},
			{ID: "last", Tool: "document.read_range", Capability: app.ToolCapabilityDocumentReadRange, Status: app.ToolCallStatusCompleted, Result: map[string]any{"kind": "pdf", "content": "Last required page.", "read_complete": true}},
		}
		if _, _, err := runtime.synthesizeWorkflowFinalAnswer(t.Context(), run, "Summarize the complete PDF", calls, nil, "fast", nil); err != nil {
			t.Fatal(err)
		}
		messages := <-requests
		system, user := "", ""
		for _, m := range messages {
			if m.Role == "system" {
				system += m.Content
			}
			if m.Role == "user" {
				user += m.Content
			}
		}
		legacyRule := strings.Contains(system, "never describe the answer as a complete-PDF summary")
		if revision == 5 {
			if !legacyRule {
				t.Fatal("legacy PDF completion rule changed")
			}
			continue
		}
		if legacyRule || !strings.Contains(system, "An intermediate read_complete=false does not override the cumulative task_scope_manifest") {
			t.Fatalf("conflicting final completion instruction: %s", system)
		}
		for _, required := range []string{`"scope_status":"complete"`, `"model_exposure_complete":true`, "per_call_coverage={read_complete=false", "Last required page."} {
			if !strings.Contains(user, required) {
				t.Fatalf("final prompt lost %q: %s", required, user)
			}
		}
	}
}

func TestPartialDocumentPresentationRetainsFailureBoundaries(t *testing.T) {
	state := scopedReadState()
	state.Plan.ProfileRevision = 7
	state.Route.Facts[documentReadFactPrefix+"scope_status"] = "partial"
	state.Nodes[documentReadNodeID] = app.WorkflowNodeState{LastAssessment: &app.NodeAssessment{ReasonCode: "document_scope_incomplete"}}
	run := app.AgentRun{Workflow: state}
	calls := []app.ToolCall{{Status: app.ToolCallStatusCompleted, Capability: app.ToolCapabilityDocumentReadRange, Result: map[string]any{"content": "Actual slide text", "read_complete": false}}}
	if !documentCanPresentPartialEvidence(run, calls) {
		t.Fatal("read text was discarded due to a visual gap")
	}
	for _, reason := range []string{"document_source_version_changed", "document_task_scope_invalid"} {
		state.Nodes[documentReadNodeID] = app.WorkflowNodeState{LastAssessment: &app.NodeAssessment{ReasonCode: reason}}
		if documentCanPresentPartialEvidence(run, calls) {
			t.Fatalf("unsafe partial projection for %s", reason)
		}
	}
	state.Nodes[documentReadNodeID] = app.WorkflowNodeState{LastAssessment: &app.NodeAssessment{ReasonCode: "document_scope_incomplete"}}
	state.Route.Facts[documentReadFactPrefix+"next_cursor"] = "pending"
	if documentCanPresentPartialEvidence(run, calls) {
		t.Fatal("unfinished traversal presented as terminal source gap")
	}
	delete(state.Route.Facts, documentReadFactPrefix+"next_cursor")
	state.Plan.ProfileRevision = 6
	if documentCanPresentPartialEvidence(run, calls) {
		t.Fatal("legacy partial behavior changed")
	}
}
