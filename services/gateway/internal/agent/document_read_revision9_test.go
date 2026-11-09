package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestDocumentReadR9TreeArgumentsAreRevisionBound(t *testing.T) {
	definition := app.ToolDefinition{Name: "document.outline", Capabilities: []app.CapabilityDescriptor{{Name: app.ToolCapabilityDocumentOutline}}}
	for revision := 4; revision <= 9; revision++ {
		t.Run(fmt.Sprint(revision), func(t *testing.T) {
			state := scopedReadState()
			state.Plan.ProfileRevision = revision
			state.ActiveNodeIDs = []app.WorkflowNodeID{documentReadNodeID}
			bound, required := scopedDocumentReadModelArguments(app.AgentRun{Workflow: state}, definition)
			if !bound["structure_version"] || bound["node_id"] != (revision < 9) || bound["depth"] != (revision < 9) || len(required) != 0 {
				t.Fatalf("incorrect model navigation arguments: bound=%v required=%v", bound, required)
			}
			args, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentOutline}, map[string]any{"structure_version": 99, "node_id": "section", "depth": 2})
			if err != nil {
				t.Fatal(err)
			}
			if revision == 9 {
				if args["structure_version"] != 2 || args["node_id"] != "section" || args["depth"] != 2 {
					t.Fatalf("revision 9 lost tree navigation: %v", args)
				}
			} else {
				for _, key := range []string{"structure_version", "node_id", "depth"} {
					if _, present := args[key]; present {
						t.Fatalf("legacy revision received new %s argument: %v", key, args)
					}
				}
			}
		})
	}
	state := scopedReadState()
	state.Plan.ProfileRevision = 9
	state.ActiveNodeIDs = []app.WorkflowNodeID{documentReadNodeID}
	state.Nodes[documentReadNodeID] = app.WorkflowNodeState{Stage: documentStructureStage}
	profile := documentReadProfile{}
	if !profile.DirectStage(state) {
		t.Fatal("root discovery unexpectedly requires a model decision")
	}
	args, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentOutline}, profile.DirectStageArguments(state))
	if err != nil || args["structure_version"] != 2 || args["node_id"] != nil || args["cursor"] != nil {
		t.Fatalf("direct discovery did not request the tree root: %v %v", args, err)
	}
}

func TestDocumentReadR9ExpansionDoesNotFreezeContentAndRecordsNavigation(t *testing.T) {
	state := scopedReadState()
	state.Plan.ProfileRevision = 9
	for i, id := range []string{"root", "section"} {
		budget := map[string]any{"max_bytes": 4000, "share_percent": 25, "leads_omitted": i == 1, "previews_trimmed": i == 1}
		output := map[string]any{
			"source_sha256": "source", "structure_generation": "tree-generation", "expanded_node_id": id, "structure_budget": budget,
			"outline":  []any{map[string]any{"id": "leaf", "preview": map[string]any{"text": "Visible preview only", "is_preview": true}}},
			"coverage": map[string]any{"scope": "structure_discovery", "status": "complete", "required": 1, "covered": 1, "missing": 0, "denominator_known": true, "task_coverage": "not_assessed"},
		}
		args, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentOutline}, map[string]any{"node_id": id})
		if err != nil {
			t.Fatal("expansion required a premature content mode", err)
		}
		call := app.ToolCall{ID: fmt.Sprint(i), Tool: "document.outline", Capability: app.ToolCapabilityDocumentOutline, Status: app.ToolCallStatusCompleted, Arguments: args, Result: output}
		outcome := adaptDocumentRetrievalOutcome(call, documentReadNodeID)
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable) {
			t.Fatal("outline previews emitted the content acquisition signal")
		}
		assessment := (documentReadProfile{}).Assess(state, outcome)
		if assessment.Status != app.AssessmentNeedsMoreEvidence || assessment.ReasonCode != "document_structure_discovered" {
			t.Fatalf("expansion did not remain in discovery: %+v", assessment)
		}
		for _, fact := range []string{"task_mode", "scope_extent", "scope_selector", "scope_status", "coverage", "scope_selection_call_id"} {
			if state.Route.Facts[documentReadFactPrefix+fact] != "" {
				t.Fatalf("outline froze or acquired %s: %v", fact, state.Route.Facts)
			}
		}
	}
	var expanded []string
	var budgets []map[string]any
	if err := json.Unmarshal([]byte(state.Route.Facts[documentReadFactPrefix+"expanded_node_ids"]), &expanded); err != nil || !reflect.DeepEqual(expanded, []string{"root", "section"}) {
		t.Fatalf("expansion path missing: %v %v", expanded, err)
	}
	if err := json.Unmarshal([]byte(state.Route.Facts[documentReadFactPrefix+"structure_budgets"]), &budgets); err != nil || len(budgets) != 2 || budgets[1]["leads_omitted"] != true || budgets[1]["previews_trimmed"] != true {
		t.Fatalf("budget degradation missing: %v %v", budgets, err)
	}
	read := scopedReadOutcome(true, "")
	read.Refs[0].Attributes["structure_generation"] = "tree-generation"
	read.Refs[0].Attributes["scope_extent"] = "whole_document"
	read.Refs[0].Attributes["task_mode"] = "calculation"
	if assessment := (documentReadProfile{}).Assess(state, read); assessment.Status != app.AssessmentComplete || state.Route.Facts[documentReadFactPrefix+"task_mode"] != "calculation" {
		t.Fatalf("actual content read did not commit its selected mode: %+v", assessment)
	}
}

func TestDocumentReadR9OutlineCursorCannotCrossContainers(t *testing.T) {
	for _, requested := range []string{"section-a", "section-b", ""} {
		t.Run(requested, func(t *testing.T) {
			state := scopedReadState()
			state.Plan.ProfileRevision = 9
			for key, value := range map[string]string{"source_sha256": "source", "structure_generation": "tree-generation", "outline_node_id": "section-a", "outline_next_cursor": "trusted-a-cursor"} {
				state.Route.Facts[documentReadFactPrefix+key] = value
			}
			args := map[string]any{"source_sha256": "forged-source", "structure_generation": "forged-generation", "cursor": "forged-cursor"}
			if requested != "" {
				args["node_id"] = requested
			}
			args, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentOutline}, args)
			if err != nil || args["source_sha256"] != "source" || args["structure_generation"] != "tree-generation" {
				t.Fatalf("source pin changed: %v %v", args, err)
			}
			if requested == "section-a" {
				if args["cursor"] != "trusted-a-cursor" {
					t.Fatalf("same container did not resume: %v", args)
				}
			} else if args["cursor"] != nil {
				t.Fatalf("another container reused the old cursor: %v", args)
			}
		})
	}
}

func TestDocumentReadR9FileStoreRuntimeExpandsThenReadsSubtree(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	st, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := agentTestConfig()
	cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = root, []string{root}
	cfg.Storage.TraceDir, cfg.Storage.ArtifactDir = filepath.Join(root, "traces"), filepath.Join(root, "artifacts")
	hub := toolhub.New(cfg, st)
	defer hub.Close()
	runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	session := storetest.MustCreateSessionWithScope(t, st, "tree runtime", app.DefaultOwnerID, root, "web", false)
	if err := os.WriteFile(filepath.Join(root, "tree.md"), []byte("# Selected\n\nSELECTED_BODY.\n\n## Detail\n\nLAST_REQUIRED_DETAIL.\n\n# Other\n\nUNREAD_SIBLING_BODY.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	initial, err := hub.Execute(t.Context(), "document.outline", map[string]any{"path": "tree.md", "structure_version": 2, "depth": 1}, session.ID, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	var tree docindex.TreeOutlineResult
	if err := json.Unmarshal([]byte(initial.Output.(map[string]any)["content"].(string)), &tree); err != nil || len(tree.Nodes) != 2 {
		t.Fatalf("fixture root unavailable: %+v %v", tree, err)
	}
	selectedID := tree.Nodes[0].ID
	var steps atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/tokenize") {
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 100})
			return
		}
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(body.Messages) == 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 100})
			return
		}
		prompt := body.Messages[len(body.Messages)-1].Content
		response := "The selected section includes SELECTED_BODY and LAST_REQUIRED_DETAIL."
		if strings.Contains(prompt, "WORKFLOW_STEP_REQUEST") {
			tool, args := "document.outline", map[string]any{"node_id": selectedID, "depth": 2}
			if steps.Add(1) > 1 {
				tool, args = "document.read_range", map[string]any{"node_ids": []string{selectedID}, "task_mode": "targeted", "scope_extent": "selected_range", "scope_reason": "Read the complete requested native section."}
			}
			raw, _ := json.Marshal(map[string]any{"type": "action", "tool": tool, "arguments": args})
			response = string(raw)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": response}}}})
	}))
	defer server.Close()
	goal := "Read the Selected section in tree.md"
	message, err := st.AddMessage(t.Context(), app.Message{ID: app.NewID("message"), SessionID: session.ID, Role: "user", Content: goal, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	route, err := runtime.routeIntentForTest(session.ID, message.ID, goal, agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runtime.profiles.Get(app.WorkflowDocumentRead, 9)
	if err != nil {
		t.Fatal(err)
	}
	intent, plan, err := profile.Resolve(route, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	workflow := newWorkflowState(route, app.ReturnRoute{Mode: app.ReturnToSource}, intent, plan)
	if err := prepareWorkflowState(profile, workflow); err != nil {
		t.Fatal(err)
	}
	run, err := st.SaveRun(t.Context(), app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "running", StartedAt: time.Now().UTC(), Workflow: workflow})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL, cfg.Model.Deep.BaseURL = server.URL, server.URL
	runtime.models = modelrouter.New(cfg)
	result, handled, err := runtime.resumeMatchedWorkflow(t.Context(), run, goal, nil, "test.tree_document_resumed")
	if err != nil || !handled || result.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		t.Fatalf("tree runtime failed: handled=%v err=%v steps=%d state=%s message=%s", handled, err, steps.Load(), result.Run.State, result.Message.Content)
	}
	calls, err := st.ListToolCalls(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	assessmentState := scopedReadState()
	assessmentState.Plan.ProfileRevision = 9
	for _, call := range calls {
		if call.RunID != run.ID {
			continue
		}
		names = append(names, call.Tool)
		if stringValue(call.Arguments["structure_version"]) != "2" {
			t.Fatalf("runtime omitted tree version: %+v", call.Arguments)
		}
		assessment := profile.Assess(assessmentState, adaptDocumentRetrievalOutcome(call, documentReadNodeID))
		if call.Tool == "document.outline" {
			if assessment.Status == app.AssessmentComplete || assessmentState.Route.Facts[documentReadFactPrefix+"coverage"] != "" || assessmentState.Route.Facts[documentReadFactPrefix+"task_mode"] != "" {
				t.Fatal("runtime navigation established content scope or coverage")
			}
			output, _ := anyMap(call.Result)
			projected, err := sliceWorkflowEvidenceForRun(run, call.Tool, output, workflowEvidenceStructured, cfg.StageEvidenceBytes(cfg.Model.Fast), goal)
			if err != nil || !json.Valid([]byte(projected)) {
				t.Fatalf("tree model evidence is not complete JSON: %s %v", projected, err)
			}
		} else {
			output, _ := anyMap(call.Result)
			content := stringValue(output["content"])
			if assessment.Status != app.AssessmentComplete || !strings.Contains(content, "SELECTED_BODY") || !strings.Contains(content, "LAST_REQUIRED_DETAIL") || strings.Contains(content, "UNREAD_SIBLING_BODY") || call.Arguments["include_descendants"] != true {
				t.Fatalf("runtime did not acquire the full selected subtree: %s %+v", content, assessment)
			}
		}
	}
	if strings.Join(names, ",") != "document.outline,document.outline,document.read_range" || steps.Load() != 2 {
		t.Fatalf("runtime skipped layered discovery: calls=%v steps=%d", names, steps.Load())
	}
	reopened, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	saved, found, err := reopened.GetRun(t.Context(), run.ID)
	if err != nil || !found {
		t.Fatalf("run did not survive file reopen: %v", err)
	}
	var expanded []string
	var budgets []docindex.StructureBudget
	facts := saved.Workflow.Route.Facts
	if err := json.Unmarshal([]byte(facts[documentReadFactPrefix+"expanded_node_ids"]), &expanded); err != nil || !reflect.DeepEqual(expanded, []string{tree.Node.ID, selectedID}) {
		t.Fatalf("navigation path did not persist: %v %v", expanded, err)
	}
	if err := json.Unmarshal([]byte(facts[documentReadFactPrefix+"structure_budgets"]), &budgets); err != nil || len(budgets) != 2 || budgets[0].SharePercent != 25 || budgets[0].MaxBytes != cfg.StageEvidenceBytes(cfg.Model.Fast)/4 {
		t.Fatalf("structure budgets did not persist: %+v %v", budgets, err)
	}
	if facts[documentReadFactPrefix+"scope_status"] != "complete" || facts[documentReadFactPrefix+"scope_extent"] != "selected_range" || !strings.Contains(facts[documentReadFactPrefix+"scope_selector"], selectedID) {
		t.Fatalf("selected content coverage did not persist: %v", facts)
	}
}
