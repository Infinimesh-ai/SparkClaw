package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

// This opt-in acceptance harness runs the actual router/workflow, retains every
// attempt, and exports the recorder's frozen inputs. It never calls a reviewer.
// Supply an isolated config/workspace containing COPIES, never original files.
func TestRealDocumentWorkflowEvaluation(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_DOCUMENT_EVAL") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_DOCUMENT_EVAL=1 and supply evaluation config/cases/output")
	}
	if os.Getenv("SPARKCLAW_DOCUMENT_EVAL_CONFIG") == "" {
		t.Fatal("explicit isolated evaluation config required")
	}
	cfg, err := config.Load(os.Getenv("SPARKCLAW_DOCUMENT_EVAL_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Mock || !cfg.Storage.ExecutionEvidence.Enabled {
		t.Fatal("evaluation requires real models and enabled execution evidence")
	}
	output := os.Getenv("SPARKCLAW_DOCUMENT_EVAL_OUTPUT")
	if !filepath.IsAbs(output) {
		t.Fatal("evaluation output must be an absolute new directory")
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID           string     `json:"id"`
		Request      string     `json:"request"`
		Workflow     string     `json:"workflow"`
		NotContains  []string   `json:"not_contains"`
		Contains     [][]string `json:"contains"`
		Edit         bool       `json:"edit"`
		AllowPartial bool       `json:"allow_partial"`
	}
	raw, err := os.ReadFile(os.Getenv("SPARKCLAW_DOCUMENT_EVAL_CASES"))
	if err != nil || json.Unmarshal(raw, &cases) != nil || len(cases) == 0 {
		t.Fatalf("invalid evaluation cases: %v", err)
	}
	st, err := store.NewFileStore(filepath.Join(output, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	hub := toolhub.New(cfg, st).WithArtifactStore(artifact.NewStore(cfg.Storage))
	defer hub.Close()
	writer := trace.NewWriterFromConfig(cfg)
	runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), writer).WithArtifactStore(artifact.NewStore(cfg.Storage))
	selected := "," + os.Getenv("SPARKCLAW_DOCUMENT_EVAL_FILTER") + ","
	count := 0
	for _, tc := range cases {
		if selected != ",," && !strings.Contains(selected, ","+tc.ID+",") {
			continue
		}
		count++
		t.Run(tc.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
			defer cancel()
			session, err := st.CreateSessionWithScope(ctx, tc.ID, app.DefaultOwnerID, cfg.Workspaces.DefaultRoot, "web", false)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			result, runErr := runtime.HandleMessage(ctx, session.ID, tc.Request)
			for attempt := 0; tc.Edit && len(result.Approvals) > 0 && attempt < 4 && runErr == nil; attempt++ {
				for _, approval := range result.Approvals {
					if result.Run.Workflow == nil || result.Run.Workflow.Plan.ProfileID != app.WorkflowDocumentEdit {
						t.Fatal("only isolated document edits may be approved by this harness")
					}
					resolved, err := st.ResolveApproval(ctx, approval.ID, app.ApprovalStatusApproved, "authorized evaluation on copied fixtures")
					if err != nil {
						runErr = err
						break
					}
					if _, err = runtime.ExecuteApprovedToolCall(ctx, resolved); err != nil {
						runErr = err
						break
					}
				}
				if runErr == nil {
					result, _, runErr = runtime.ResumeRunAfterApproval(ctx, session.ID, result.Run.ID)
				}
			}
			allTools, toolsErr := st.ListToolCalls(context.Background(), session.ID)
			if toolsErr != nil {
				t.Error(toolsErr)
			}
			result.ToolCalls = toolCallsForRun(allTools, result.Run.ID)
			calls, callsErr := st.ListModelCalls(context.Background(), session.ID, result.Run.ID)
			scope := executionevidence.Scope{OwnerID: session.OwnerID, SessionID: session.ID, RunID: result.Run.ID}
			manifest, captureErr := writer.ExecutionEvidence().Read(context.Background(), scope)
			exportErr := captureErr
			if captureErr == nil {
				_, exportErr = writer.ExecutionEvidence().Export(context.Background(), scope, filepath.Join(output, tc.ID+".zip"), !manifest.Capture.Complete || !manifest.Capture.Sealed)
			}
			record := map[string]any{"id": tc.ID, "request": tc.Request, "result": result, "model_calls": calls, "elapsed_ms": time.Since(started).Milliseconds(), "capture": manifest.Capture}
			for key, e := range map[string]error{"run_error": runErr, "model_calls_error": callsErr, "capture_error": captureErr, "export_error": exportErr} {
				if e != nil {
					record[key] = e.Error()
				}
			}
			encoded, err := json.MarshalIndent(record, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(output, tc.ID+".json"), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			t.Logf("run=%s state=%s calls=%d elapsed=%s capture_complete=%v export_error=%v answer=%s", result.Run.ID, result.Run.State, len(calls), time.Since(started).Round(time.Millisecond), manifest.Capture.Complete, exportErr, result.Message.Content)
			if runErr != nil || callsErr != nil || captureErr != nil || exportErr != nil {
				t.Errorf("execution/capture failed: %v / %v / %v / %v", runErr, callsErr, captureErr, exportErr)
			}
			if !manifest.Capture.Complete || !manifest.Capture.Sealed {
				t.Error("capture must be complete and sealed, even for a failed task")
			}
			if result.Run.Workflow == nil || string(result.Run.Workflow.Plan.ProfileID) != tc.Workflow {
				t.Errorf("wrong workflow: %+v", result.RouteDecision)
			}
			if result.Run.State != "completed" && !(tc.AllowPartial && result.Run.State == "blocked") {
				t.Errorf("run not completed: %s", result.Run.State)
			}
			if tc.Edit && len(result.Message.Attachments) == 0 {
				t.Error("edit did not return a maintained document")
			}
			for _, call := range calls {
				if call.Mock {
					t.Error("mock model used in real evaluation")
				}
			}
			for _, forbidden := range tc.NotContains {
				if strings.Contains(strings.ToLower(result.Message.Content), strings.ToLower(forbidden)) {
					t.Errorf("answer invented or misstated evidence: %q", forbidden)
				}
			}
			for _, alternatives := range tc.Contains {
				found := false
				for _, fragment := range alternatives {
					found = found || strings.Contains(strings.ToLower(result.Message.Content), strings.ToLower(fragment))
				}
				if !found {
					t.Errorf("answer missing expected evidence (one of %q)", alternatives)
				}
			}
		})
	}
	if count == 0 {
		t.Fatal("evaluation filter selected no cases")
	}
}
