package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

func TestTaskSelectionRequiresAnExactAliasPartition(t *testing.T) {
	selected, err := parseDocumentTaskSelection(`{"n3":false,"n2":true,"n1":false}`, 3)
	if err != nil || len(selected) != 1 || selected[0] != 1 {
		t.Fatalf("valid selection: %v %v", selected, err)
	}
	for _, raw := range []string{
		`{"n1":true,"n2":false}`,
		`{"n1":true,"n2":false,"n2":true,"n3":false}`,
		`{"n1":true,"n2":false,"n4":false}`,
		`{"n1":true,"n2":false,"n3":null}`,
		`{"n1":true,"n2":false,"n3":"false"}`,
		`{"n1":true,"n2":false,"n3":false,"answer":"invented"}`,
		`{"n1":true,"n2":false,"n3":false} trailing`,
	} {
		if _, err := parseDocumentTaskSelection(raw, 3); err == nil {
			t.Fatalf("accepted invalid selection: %s", raw)
		}
	}
}

func TestDocumentTaskSelectionKeepsOriginalEvidenceAndEveryBatch(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", 400)
			return
		}
		if len(request.Messages) == 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 100})
			return
		}
		requests++
		user := request.Messages[len(request.Messages)-1].Content
		position := strings.LastIndex(user, "\n{")
		if position < 0 {
			t.Error("missing bounded source batch")
			http.Error(w, "missing batch", 400)
			return
		}
		var batch struct {
			Fragments []struct {
				Alias  string         `json:"alias"`
				Source map[string]any `json:"source"`
			} `json:"fragments"`
			Dependencies []any `json:"dependencies"`
		}
		if err := json.Unmarshal([]byte(user[position+1:]), &batch); err != nil {
			t.Error(err)
			http.Error(w, "bad batch", 400)
			return
		}
		if len(batch.Dependencies) != 1 {
			t.Errorf("batch lost units/qualification dependency: %+v", batch.Dependencies)
		}
		decisions := map[string]bool{}
		for _, fragment := range batch.Fragments {
			decisions[fragment.Alias] = strings.Contains(stringValue(fragment.Source["text"]), "MATCH")
		}
		response, _ := json.Marshal(decisions)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(response)}}}})
	}))
	defer server.Close()
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	cfg := runtime.tools.Config()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	runtime.models = modelrouter.New(cfg)
	state := scopedReadState()
	state.Plan.ProfileRevision = 7
	state.Route.Facts[documentReadFactPrefix+"scope_status"] = "complete"
	state.Route.Facts[documentReadFactPrefix+"task_mode"] = "exhaustive"
	state.Route.Facts[documentReadFactPrefix+"scope_selector"] = `{"all":true}`
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "running", StartedAt: time.Now().UTC(), Workflow: state}
	testSaveRun(st, run)
	header := map[string]any{"id": "header", "text": "Amounts: USD; estimates only.", "kind": "heading"}
	nodes := []any{}
	text := ""
	for i := 0; i < 15; i++ {
		body := strings.Repeat("irrelevant source detail ", 20)
		if i == 0 || i == 14 {
			body = fmt.Sprintf("MATCH original item %d — retain exact wording.", i)
		}
		text += body
		nodes = append(nodes, map[string]any{"id": fmt.Sprintf("source-%d", i), "text": body, "locator": map[string]any{"block": fmt.Sprintf("document.p[%d]", i+1)}, "dependencies": []string{"header"}})
	}
	inputs := workflowFinalEvidenceInputs{Calls: []app.ToolCall{{ID: "read", Status: app.ToolCallStatusCompleted, Capability: app.ToolCapabilityDocumentReadRange, Result: map[string]any{"nodes": nodes, "dependencies": []any{header}, "content": text, "read_complete": true}}}, ArchivedBytesByCall: map[string]int{"read": len(text)}}
	projected, selection, err := runtime.selectDocumentTaskEvidence(t.Context(), run, "List all MATCH items and cite their paragraphs.", inputs, 2400)
	if err != nil || selection == nil {
		t.Fatalf("selection failed: %+v %v", selection, err)
	}
	if selection.Processed != 15 || selection.Selected != 2 || selection.Batches < 2 || selection.Batches != requests || len(selection.ModelCalls) != requests {
		t.Fatalf("lost batch/telemetry: %+v requests=%d", selection, requests)
	}
	output, _ := anyMap(projected.Calls[0].Result)
	if len(anySlice(output["nodes"])) != 2 || !strings.Contains(stringValue(output["content"]), "item 14") || len(anySlice(output["dependencies"])) != 1 {
		t.Fatalf("missing late item or dependencies: %+v", output)
	}
	original, _ := anyMap(inputs.Calls[0].Result)
	if len(anySlice(original["nodes"])) != 15 || original["content"] != text {
		t.Fatal("selection mutated the archived source")
	}
	final := buildWorkflowFinalEvidenceProjection(run, projected.Calls, nil, nil, 2400)
	applyDocumentTaskSelection(&final, selection)
	if final.Coverage.Claim != workflowCoverageBounded || !strings.Contains(final.modelPayload(), `"model_exposure_complete":false`) || !strings.Contains(final.modelPayload(), `"task_selection_complete":true`) || !strings.Contains(final.modelPayload(), "Amounts: USD") || !strings.Contains(final.modelPayload(), "source-14") {
		t.Fatalf("final lost original source/selection lineage: %s", final.modelPayload())
	}
}

func TestUnevenRangeBatchesUseTheirActualFinalEvidenceCosts(t *testing.T) {
	state := scopedReadState()
	state.Plan.ProfileRevision = 7
	state.Route.Facts[documentReadFactPrefix+"scope_status"] = "complete"
	state.Route.Facts[documentReadFactPrefix+"scope_selector"] = `{"all":true}`
	calls := []app.ToolCall{}
	for _, text := range []string{strings.Repeat("large exact source ", 100), "small final exception"} {
		calls = append(calls, app.ToolCall{ID: fmt.Sprint(len(calls)), Status: app.ToolCallStatusCompleted, Capability: app.ToolCapabilityDocumentReadRange, Result: map[string]any{"content": text, "dependencies": []any{map[string]any{"id": "header", "text": "USD"}}, "nodes": []any{map[string]any{"id": "value", "text": text}}, "read_complete": true}})
	}
	projection := buildWorkflowFinalEvidenceProjection(app.AgentRun{Workflow: state}, calls, nil, nil, 2800)
	if projection.Coverage.Claim != workflowCoverageComplete || !strings.Contains(projection.modelPayload(), "large exact source") || !strings.Contains(projection.modelPayload(), "small final exception") {
		t.Fatalf("equal-share budget dropped a source that fits: %s", projection.modelPayload())
	}
}
