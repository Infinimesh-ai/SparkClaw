package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// Keep the production executor and HandleMessageWithAttachments intact. Only
// the model HTTP endpoint is synthetic; document routing, evidence, policy,
// approval continuation, the actual editor and result delivery are real.
func TestExecutionRealDocumentWorkflowWaitsForInstalledClientApproval(t *testing.T) {
	for _, fixture := range []struct{ name, goal, decision string }{
		{"explicit_name_approve", "Edit notes.md and replace Original reflection with Improved reflection.", "approve"},
		{"explicit_name_reject", "Edit notes.md and replace Original reflection with Improved reflection.", "reject"},
		{"attached_document_approve", "Edit the attached document and replace Original reflection with Improved reflection.", "approve"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root, err := os.MkdirTemp("/dev/shm", "sparkclaw-execution-workflow-approval-test-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			model, modelCalls := executionDocumentApprovalModel(t)
			t.Cleanup(model.Close)
			cfg := testConfig(root)
			cfg.Model.Mock = false
			cfg.Model.Fast.BaseURL, cfg.Model.Deep.BaseURL = model.URL, model.URL
			cfg.Model.Embedding.BaseURL, cfg.Model.Guard.BaseURL = model.URL, model.URL
			cfg.Gateway.PairingRequired = true
			cfg.Gateway.DeploymentID = "execution-real-workflow-approval"
			cfg.Security.ApprovalRequiredTools = append(cfg.Security.ApprovalRequiredTools, "text.replace_text")
			shared := store.NewMemoryStore()
			const token = "synthetic-execution-real-document-workflow-installed-client-token"
			const install = "11111111-1111-4111-8111-111111111111"
			if _, err = shared.RegisterClient(t.Context(), app.Client{ID: "workflow-client", OwnerID: "workflow-owner", Name: "synthetic fixture", TokenHash: hashSecret(token)}); err != nil {
				t.Fatal(err)
			}
			tools := toolhub.New(cfg, shared)
			t.Cleanup(func() { _ = tools.Close() })
			runtime, err := agent.NewRuntimeWithContext(t.Context(), shared, tools, policy.New(cfg), modelrouter.New(cfg), nil)
			if err != nil {
				t.Fatal(err)
			}
			controlRoot := filepath.Join(root, "execution")
			// A nil executor selects the real executeWorkbenchWorkflow implementation.
			instance := New(cfg, shared, tools, runtime, WithExecutions(controlRoot, nil))
			lifecycle, cancel := context.WithCancel(t.Context())
			instance.BindLifecycleContext(lifecycle)
			t.Cleanup(func() {
				cancel()
				if instance.executions != nil {
					instance.executions.Close()
				}
			})
			server := httptest.NewServer(instance.Handler())
			t.Cleanup(server.Close)
			server.Client().Timeout = 3 * time.Second
			request := func(method, route string, raw []byte) (int, []byte) {
				t.Helper()
				req, err := http.NewRequest(method, server.URL+route, bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("X-SparkClaw-Installation", install)
				req.Header.Set("X-SparkClaw-Digest", execution.Digest(raw))
				req.Header.Set("Content-Type", "application/json")
				res, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				return res.StatusCode, body
			}
			if code, raw := request("POST", "/api/v1/installations", []byte(`{"schema_version":1,"installation_id":"`+install+`"}`)); code != 200 {
				t.Fatalf("installation binding: %d %s", code, raw)
			}
			const original = "# Notes\nOriginal reflection\n"
			const updated = "# Notes\nImproved reflection\n"
			input := execution.File{ID: "55555555-5555-4555-8555-555555555555", Name: "notes.md", Size: len(original), SHA256: execution.Digest([]byte(original))}
			e := execution.Envelope{SchemaVersion: 1, DeploymentID: cfg.Gateway.DeploymentID, OwnerID: "workflow-owner", ClientID: "workflow-client", InstallationID: install, ConversationID: "22222222-2222-4222-8222-222222222222", TaskID: "33333333-3333-4333-8333-333333333333", RequestID: "44444444-4444-4444-8444-444444444444", Messages: []execution.Message{{Role: "user", Content: fixture.goal}}, InputFiles: []execution.File{input}}
			if code, raw := request("PUT", "/api/v1/inputs/"+e.RequestID+"/files/"+input.ID, []byte(original)); code != 200 {
				t.Fatalf("frozen input upload: %d %s", code, raw)
			}
			raw, _ := json.Marshal(e)
			if code, body := request("POST", "/api/v1/executions", raw); code != 202 {
				t.Fatalf("real workflow submit: %d %s", code, body)
			}
			lookup := func() execution.Status {
				t.Helper()
				code, body := request("GET", "/api/v1/executions/"+e.RequestID, nil)
				var status execution.Status
				if err := json.Unmarshal(body, &status); err != nil || code != 200 {
					t.Fatalf("execution status: %d %s %v", code, body, err)
				}
				return status
			}
			var status execution.Status
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				status = lookup()
				if len(status.PendingApprovals) > 0 || (status.State != "running" && status.State != "accepted") {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if status.State != "running" || len(status.PendingApprovals) != 1 || status.Result != nil {
				t.Fatalf("real document workflow did not reach approval: %+v result=%+v (model calls %+v)", status, status.Result, modelCalls.snapshot())
			}
			approval := status.PendingApprovals[0]
			if approval.Tool != "text.replace_text" || approval.Digest == "" {
				t.Fatalf("wrong workflow approval: %+v", approval)
			}
			inputPath, _ := approval.Arguments["path"].(string)
			outputPath, _ := approval.Arguments["output_path"].(string)
			if inputPath != "notes.md" || outputPath != "notes-2.md" {
				t.Fatalf("workflow did not bind its actual governed document paths: %+v", approval.Arguments)
			}
			workspaces, err := filepath.Glob(filepath.Join("/dev/shm", executionWorkspacePrefix(controlRoot)+"*"))
			if err != nil || len(workspaces) != 1 {
				t.Fatalf("expected one live tmpfs workflow workspace: %v %v", workspaces, err)
			}
			workspace := filepath.Join(workspaces[0], "workspace")
			if source, err := os.ReadFile(filepath.Join(workspace, inputPath)); err != nil || string(source) != original {
				t.Fatalf("real workflow did not use frozen uploaded document: %q %v", source, err)
			}
			if _, err := os.Stat(filepath.Join(workspace, outputPath)); !os.IsNotExist(err) {
				t.Fatalf("real editor ran before the explicit decision: %v", err)
			}
			// document.edit performs its bound read deterministically, without a
			// model tool proposal. Verify the actual completed reader receipt.
			reads := 0
			err = filepath.WalkDir(filepath.Join(workspaces[0], "artifacts"), func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil || entry.IsDir() {
					return walkErr
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				var receipt struct {
					Tool   string
					Status app.ToolCallStatus
				}
				if json.Unmarshal(raw, &receipt) == nil && receipt.Tool == "files.read" {
					if receipt.Status != app.ToolCallStatusCompleted || !bytes.Contains(raw, []byte("Original reflection")) {
						t.Errorf("reader did not produce actual uploaded document evidence: %s", raw)
					}
					reads++
				}
				return nil
			})
			if err != nil || reads != 1 {
				t.Fatalf("expected one real deterministic document read: %d %v", reads, err)
			}
			if counts := modelCalls.snapshot(); counts != [3]int32{1, 0, 1} {
				t.Fatalf("expected real semantic routing and one editor proposal: %v", counts)
			}
			decisionJSON, _ := json.Marshal(map[string]string{"digest": approval.Digest, "decision": fixture.decision})
			if code, body := request("POST", "/api/v1/executions/"+e.RequestID+"/approvals/"+approval.ApprovalID, decisionJSON); code != 200 {
				t.Fatalf("explicit installed-client decision: %d %s", code, body)
			}
			deadline = time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				status = lookup()
				if status.State != "running" && status.State != "accepted" {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if status.State != "completed" || status.Result == nil || len(status.PendingApprovals) != 0 {
				t.Fatalf("real workflow did not complete after decision: %+v", status)
			}
			var payload execution.Payload
			if err := json.Unmarshal([]byte(status.Result.Payload), &payload); err != nil {
				t.Fatal(err)
			}
			if fixture.decision == "approve" {
				if len(payload.Files) != 1 || payload.Files[0].Name != outputPath || payload.Files[0].SHA256 != execution.Digest([]byte(updated)) {
					t.Fatalf("approved real document editor did not return its actual copy: %+v", payload)
				}
				if code, document := request("GET", "/api/v1/executions/"+e.RequestID+"/files/"+payload.Files[0].ID, nil); code != 200 || string(document) != updated {
					t.Fatalf("approved real document bytes: %d %q", code, document)
				}
			} else if len(payload.Files) != 0 || !strings.Contains(payload.Content, "rejected") {
				t.Fatalf("rejected document mutation produced an output: %+v", payload)
			}
			if counts := modelCalls.snapshot(); counts != [3]int32{1, 0, 1} {
				t.Fatalf("approval continuation rerouted or replayed the editor: %v", counts)
			}
			if _, err := os.Stat(workspaces[0]); !os.IsNotExist(err) {
				t.Fatalf("terminal workflow retained its tmpfs workspace: %v", err)
			}
			if sessions, err := shared.ListSessions(t.Context()); err != nil || len(sessions) != 0 {
				t.Fatalf("temporary document workflow leaked into legacy sessions: %v %v", sessions, err)
			}
			ledger, err := os.ReadFile(filepath.Join(controlRoot, "control.json"))
			if err != nil || bytes.Contains(ledger, []byte("Original reflection")) || bytes.Contains(ledger, []byte("Improved reflection")) {
				t.Fatalf("control fence retained document content: %v", err)
			}
		})
	}
}

type executionApprovalModelCalls struct{ routing, read, edit atomic.Int32 }

func (c *executionApprovalModelCalls) snapshot() [3]int32 {
	return [3]int32{c.routing.Load(), c.read.Load(), c.edit.Load()}
}

func executionDocumentApprovalModel(t *testing.T) (*httptest.Server, *executionApprovalModelCalls) {
	t.Helper()
	calls := &executionApprovalModelCalls{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input    []string `json:"input"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			ResponseFormat struct {
				JSONSchema struct {
					Name   string `json:"name"`
					Schema struct {
						Properties struct {
							Revision struct {
								Enum []string `json:"enum"`
							} `json:"graph_revision"`
							Candidates struct {
								Items struct {
									Properties struct {
										ID struct {
											Enum []string `json:"enum"`
										} `json:"candidate_id"`
									} `json:"properties"`
								} `json:"items"`
							} `json:"candidates"`
						} `json:"properties"`
					} `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid synthetic model request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/embeddings" {
			data := []map[string]any{}
			for index, input := range request.Input {
				vector := []float32{0, 1}
				lower := strings.ToLower(input)
				if (strings.Contains(lower, "notes.md") || strings.Contains(lower, "attached document")) && strings.Contains(lower, "replace") {
					vector = []float32{1, 0}
				}
				data = append(data, map[string]any{"index": index, "embedding": vector})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		prompt := ""
		for _, message := range request.Messages {
			prompt += message.Content + "\n"
		}
		var answer any
		switch {
		case request.ResponseFormat.JSONSchema.Name == "intent_tree_candidate_scores":
			calls.routing.Add(1)
			properties := request.ResponseFormat.JSONSchema.Schema.Properties
			if len(properties.Revision.Enum) != 1 {
				t.Error("routing request omitted immutable graph revision")
				http.Error(w, "missing graph revision", 400)
				return
			}
			candidates := []map[string]any{}
			for _, id := range properties.Candidates.Items.Properties.ID.Enum {
				score := 0.01
				if id == "document.edit#edit" {
					score = 0.99
				}
				candidates = append(candidates, map[string]any{"candidate_id": id, "tree_score": score})
			}
			answer = map[string]any{"graph_revision": properties.Revision.Enum[0], "candidates": candidates}
		case strings.Contains(prompt, "Classify the user content for SparkClaw safety"):
			answer = map[string]any{"verdict": "allow", "categories": []string{}, "reason": "synthetic bounded document edit"}
		case strings.Contains(prompt, "WORKFLOW_STEP_REQUEST") && strings.Contains(prompt, "Model-visible tools this workflow stage: files.read"):
			calls.read.Add(1)
			answer = map[string]any{"type": "action", "tool": "files.read", "arguments": map[string]any{"path": "notes.md"}, "reason": "read the bound uploaded document before editing"}
		case strings.Contains(prompt, "WORKFLOW_STEP_REQUEST") && strings.Contains(prompt, "Model-visible tools this workflow stage: text.replace_text"):
			calls.edit.Add(1)
			answer = map[string]any{"type": "action", "tool": "text.replace_text", "arguments": map[string]any{"path": "notes.md", "output_path": "notes-2.md", "expected_replacements": 1, "replacements": []map[string]string{{"find": "Original reflection", "replace": "Improved reflection"}}}, "reason": "write one bounded document copy after owner approval"}
		default:
			t.Errorf("unexpected synthetic model request: %.160s", prompt)
			http.Error(w, "unexpected synthetic model request", 500)
			return
		}
		content, _ := json.Marshal(answer)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": string(content)}}}})
	}))
	return server, calls
}
