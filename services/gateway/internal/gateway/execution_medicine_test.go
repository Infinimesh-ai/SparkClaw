package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// This covers the production executor with a transient conversation repository
// and the default durable file inventory. Only model HTTP replies are fixtures.
func TestExecutionMedicineCompletionCommitsToSharedInventory(t *testing.T) {
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			if _, err := os.Stat("/dev/shm"); err != nil {
				t.Skip("production execution workspaces require Linux tmpfs")
			}
			root := t.TempDir()
			model, calls := executionMedicineModel(t)
			t.Cleanup(model.Close)
			cfg := testConfig(root)
			cfg.Model.Mock = false
			cfg.Model.Fast.BaseURL, cfg.Model.Deep.BaseURL = model.URL, model.URL
			cfg.Model.Embedding.BaseURL, cfg.Model.Guard.BaseURL = model.URL, model.URL
			cfg.Gateway.PairingRequired = true
			cfg.Gateway.DeploymentID = "execution-medicine-test"
			statePath := filepath.Join(root, "state.json")
			shared, err := store.NewFileStore(statePath)
			if err != nil {
				t.Fatal(err)
			}
			const token = "synthetic-medicine-workflow-test-token"
			const install = "11111111-1111-4111-8111-111111111111"
			if _, err := shared.RegisterClient(t.Context(), app.Client{ID: "medicine-client", OwnerID: "medicine-owner", Name: "fixture", TokenHash: hashSecret(token)}); err != nil {
				t.Fatal(err)
			}
			hub := toolhub.New(cfg, shared)
			t.Cleanup(func() { _ = hub.Close() })
			runtime, err := agent.NewRuntimeWithContext(t.Context(), shared, hub, policy.New(cfg), modelrouter.New(cfg), nil)
			if err != nil {
				t.Fatal(err)
			}
			controlRoot := filepath.Join(root, "control")
			server := New(cfg, shared, hub, runtime, WithExecutions(controlRoot, nil))
			ctx, cancel := context.WithCancel(t.Context())
			server.BindLifecycleContext(ctx)
			t.Cleanup(func() {
				cancel()
				if server.executions != nil {
					server.executions.Close()
				}
			})
			request := func(method, path string, body any) (int, []byte) {
				t.Helper()
				var raw []byte
				if body != nil {
					raw, _ = json.Marshal(body)
				}
				req := httptest.NewRequest(method, path, bytes.NewReader(raw))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("X-SparkClaw-Installation", install)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-SparkClaw-Digest", execution.Digest(raw))
				res := httptest.NewRecorder()
				server.Handler().ServeHTTP(res, req)
				return res.Code, res.Body.Bytes()
			}
			if code, body := request("POST", "/api/v1/installations", map[string]any{"schema_version": 1, "installation_id": install}); code != 200 {
				t.Fatalf("bind %d %s", code, body)
			}
			e := execution.Envelope{SchemaVersion: 2, DeploymentID: cfg.Gateway.DeploymentID, OwnerID: "medicine-owner", ClientID: "medicine-client", InstallationID: install, ConversationID: "22222222-2222-4222-8222-222222222222", TaskID: "33333333-3333-4333-8333-333333333333", RequestID: "44444444-4444-4444-8444-444444444444", Messages: []execution.Message{{ID: "medicine-input", Role: "user", Content: "往药箱添加布洛芬24粒；碘伏1瓶，无有效期"}}}
			path := "/api/v1/executions/" + e.RequestID
			if code, body := request("POST", "/api/v1/executions", e); code != 202 {
				t.Fatalf("submit %d %s", code, body)
			}
			lookup := func() execution.Status {
				t.Helper()
				code, body := request("GET", path, nil)
				var status execution.Status
				if err := json.Unmarshal(body, &status); err != nil || code != 200 {
					t.Fatalf("lookup %d %s %v", code, body, err)
				}
				return status
			}
			wait := func(pending bool) execution.Status {
				t.Helper()
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					s := lookup()
					if len(s.PendingApprovals) > 0 && pending || s.State != "running" && s.State != "accepted" {
						return s
					}
					time.Sleep(5 * time.Millisecond)
				}
				t.Fatal("execution timeout")
				return execution.Status{}
			}
			status := wait(true)
			if status.State != "running" || len(status.PendingApprovals) != 1 {
				t.Fatalf("missing completion form: %+v result=%+v calls=%v", status, status.Result, calls.snapshot())
			}
			pending := status.PendingApprovals[0]
			if pending.InputForm == nil || len(pending.InputForm.Entries) != 2 {
				t.Fatalf("incomplete batch form: %+v", pending)
			}
			box, err := shared.ReadMedicineBox(t.Context(), "medicine-owner")
			if err != nil || len(box.Items) != 0 {
				t.Fatalf("partial write before approval: %+v %v", box, err)
			}
			approvalPath := path + "/approvals/" + pending.ApprovalID
			body := map[string]any{"digest": pending.Digest, "decision": decision}
			if decision == "approve" {
				if code, _ := request("POST", approvalPath, body); code != 409 {
					t.Fatalf("missing input bypassed form: %d", code)
				}
				body["input_schema_version"] = 1
				body["inputs"] = app.ApprovalInputValues{"entry_1": {"expiry": "2028-06-30"}}
			}
			if code, raw := request("POST", approvalPath, body); code != 200 {
				t.Fatalf("decision %d %s", code, raw)
			}
			status = wait(false)
			if status.State != "completed" || status.Result == nil {
				t.Fatalf("continuation failed: %+v result=%+v", status, status.Result)
			}
			server.executions.Wait()
			// Terminal decisions are no longer actionable; the durable receipt is
			// authoritative for a lost-response lookup, without replaying the task.
			if len(status.ApprovalReceipts) != 1 || status.ApprovalReceipts[0].Decision != decision || status.ApprovalReceipts[0].Digest != pending.Digest {
				t.Fatalf("missing original approval receipt: %+v", status.ApprovalReceipts)
			}
			if code, raw := request("POST", approvalPath, body); code != 410 {
				t.Fatalf("terminal decision replay %d %s", code, raw)
			}
			if code, raw := request("POST", "/api/v1/executions", e); code != 202 {
				t.Fatalf("execution replay %d %s", code, raw)
			}
			reopened, err := store.NewFileStore(statePath)
			if err != nil {
				t.Fatal(err)
			}
			box, err = reopened.ReadMedicineBox(t.Context(), "medicine-owner")
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if decision == "approve" {
				want = 2
			}
			if len(box.Items) != want || box.Sequence != int64(want) {
				t.Fatalf("durable batch/replay: %+v", box)
			}
			for _, item := range box.Items {
				if item.Name == "布洛芬" && item.ExpiresOn != "2028-06-30" {
					t.Fatalf("completion lost: %+v", item)
				}
			}
			other, err := shared.ReadMedicineBox(t.Context(), "other-owner")
			if err != nil || len(other.Items) != 0 {
				t.Fatal("owner partition leaked", err)
			}
			if sessions, err := shared.ListSessions(t.Context()); err != nil || len(sessions) != 0 {
				t.Fatal("ephemeral conversation leaked", err)
			}
			if got := calls.snapshot(); got[0] != 1 || got[2] != 1 {
				t.Fatalf("continuation rerouted or replanned: %v", got)
			}
			control, err := os.ReadFile(filepath.Join(controlRoot, "control.json"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(control, []byte("2028-06-30")) || bytes.Contains(control, []byte("布洛芬")) {
				t.Fatal("execution control persisted raw medicine inputs")
			}
		})
	}
}

func executionMedicineModel(t *testing.T) (*httptest.Server, *executionApprovalModelCalls) {
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
		if r.URL.Path == "/tokenize" {
			_ = json.NewEncoder(w).Encode(map[string]int{"count": 1000})
			return
		}
		if r.URL.Path == "/embeddings" {
			data := []map[string]any{}
			for index, input := range request.Input {
				vector := []float32{0, 1}
				lower := strings.ToLower(input)
				if strings.Contains(lower, "药箱") && (strings.Contains(lower, "加") || strings.Contains(lower, "记")) {
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
		case request.ResponseFormat.JSONSchema.Name == "intent_request_split":
			_, owner, ok := strings.Cut(prompt, "Owner semantic query:\n")
			if !ok {
				t.Error("split request omitted owner query")
				http.Error(w, "missing owner query", 400)
				return
			}
			owner, _, _ = strings.Cut(owner, "\n\n")
			answer = map[string]any{"deferred": false, "business_span": strings.TrimSpace(owner), "delivery_mode": "none", "delivery_object_kind": "business_result", "object_span": "", "provider_span": "", "recipient_span": ""}
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
				if id == "medicine.box_edit#create" {
					score = 0.99
				}
				candidates = append(candidates, map[string]any{"candidate_id": id, "tree_score": score})
			}
			answer = map[string]any{"graph_revision": properties.Revision.Enum[0], "candidates": candidates}
		case strings.Contains(prompt, "Classify the user content for SparkClaw safety"):
			answer = map[string]any{"verdict": "allow", "categories": []string{}, "reason": "synthetic bounded document edit"}
		case request.ResponseFormat.JSONSchema.Name == "medicine_edit_v4":
			calls.edit.Add(1)
			answer = json.RawMessage(`{"items":[{"action":"create","changes":[{"field":"name","value":"布洛芬","evidence":"布洛芬"},{"field":"quantity","value":24,"evidence":"24粒"},{"field":"unit","value":"capsule","evidence":"24粒"}],"unresolved":[{"field":"expiry","reason":"missing"}]},{"action":"create","changes":[{"field":"name","value":"碘伏","evidence":"碘伏"},{"field":"quantity","value":1,"evidence":"1瓶"},{"field":"unit","value":"bottle","evidence":"1瓶"},{"field":"no_expiry","value":true,"evidence":"无有效期"}],"unresolved":[]}]}`)
		default:
			t.Errorf("unexpected synthetic model request path=%s schema=%q: %.160s", r.URL.Path, request.ResponseFormat.JSONSchema.Name, prompt)
			http.Error(w, "unexpected synthetic model request", 500)
			return
		}
		content, _ := json.Marshal(answer)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": string(content)}}}})
	}))
	return server, calls
}
