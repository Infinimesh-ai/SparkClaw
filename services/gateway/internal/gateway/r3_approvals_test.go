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
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// Exercise the installed-client HTTP decision channel with a real policy-gated
// tool and the same continuation helper used by transient agent workflows.
func TestR3HTTPApprovalRunsOriginalToolOnlyAfterExplicitDecision(t *testing.T) {
	for _, decision := range []string{"approve", "reject", "approve_failed"} {
		t.Run(decision, func(t *testing.T) {
			root := t.TempDir()
			cfg := testConfig(root)
			cfg.Gateway.PairingRequired = true
			cfg.Gateway.DeploymentID = "r3-approval"
			cfg.Security.ApprovalRequiredTools = append(cfg.Security.ApprovalRequiredTools, "files.write_draft")
			shared := store.NewMemoryStore()
			const token = "synthetic-r3-approval-device-token-long-enough"
			const install = "11111111-1111-4111-8111-111111111111"
			if _, err := shared.RegisterClient(t.Context(), app.Client{ID: "client", OwnerID: "owner", Name: "fixture", TokenHash: hashSecret(token)}); err != nil {
				t.Fatal(err)
			}
			tools := toolhub.New(cfg, shared)
			defer tools.Close()
			var instance *Server
			execute := func(ctx context.Context, e r3execution.Envelope, _ map[string][]byte) (r3execution.Output, error) {
				budget := r3execution.NewBudget(r3execution.TaskBytes)
				transient := store.NewMemoryStore().WithTransientContentAdmission(budget.Admit)
				localTools := toolhub.New(cfg, transient)
				defer localTools.Close()
				runtime := agent.NewRuntime(transient, localTools, policy.New(cfg), modelrouter.New(cfg), nil)
				session, err := transient.CreateSessionWithScope(ctx, "temporary approval", e.OwnerID, root, "webchat", false)
				if err != nil {
					return r3execution.Output{}, err
				}
				target := "approved.txt"
				if decision == "approve_failed" {
					target = filepath.Join("..", filepath.Base(root)+"-outside.txt")
				}
				invocation, err := runtime.InvokeToolManually(ctx, "files.write_draft", map[string]any{"path": target, "content": "synthetic approved bytes"}, session.ID)
				if err != nil || invocation.Approval == nil {
					return r3execution.Output{}, r3execution.ErrUnavailable
				}
				run, _, err := transient.GetRun(ctx, invocation.Call.RunID)
				if err != nil {
					return r3execution.Output{}, err
				}
				result, err := instance.continueR3Approvals(ctx, instance.r3Executions, e, transient, runtime, budget, agent.Result{Run: run, Message: app.Message{Content: "pending"}})
				if err != nil {
					return r3execution.Output{}, err
				}
				output := r3execution.Output{Content: result.Message.Content, Files: map[string][]byte{}}
				if raw, err := os.ReadFile(filepath.Join(root, "approved.txt")); err == nil {
					output.Files["approved.txt"] = raw
				}
				return output, nil
			}
			instance = New(cfg, shared, tools, agent.Runtime{}, WithR3Executions(filepath.Join(root, "r3"), execute))
			instance.BindLifecycleContext(t.Context())
			server := httptest.NewServer(instance.Handler())
			defer server.Close()
			request := func(method, path string, raw []byte, installation string) (int, []byte) {
				t.Helper()
				req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("X-SparkClaw-Installation", installation)
				req.Header.Set("X-R3-Digest", r3execution.Digest(raw))
				req.Header.Set("Content-Type", "application/json")
				res, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				return res.StatusCode, body
			}
			if code, raw := request("POST", "/api/r3/installations", []byte(`{"schema_version":1,"installation_id":"`+install+`"}`), install); code != 200 {
				t.Fatal(code, string(raw))
			}
			e := r3execution.Envelope{SchemaVersion: 1, DeploymentID: cfg.Gateway.DeploymentID, OwnerID: "owner", ClientID: "client", InstallationID: install, ConversationID: "22222222-2222-4222-8222-222222222222", TaskID: "33333333-3333-4333-8333-333333333333", RequestID: "44444444-4444-4444-8444-444444444444", Messages: []r3execution.Message{{Role: "user", Content: "synthetic context"}}}
			raw, _ := json.Marshal(e)
			if code, body := request("POST", "/api/r3/executions", raw, install); code != 202 {
				t.Fatal(code, string(body))
			}
			var status r3execution.Status
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				_, raw = request("GET", "/api/r3/executions/"+e.RequestID, nil, install)
				_ = json.Unmarshal(raw, &status)
				if len(status.PendingApprovals) > 0 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if status.State != "running" || len(status.PendingApprovals) != 1 || status.ExecutionExpiresAt == nil {
				t.Fatalf("no pending approval: %s", raw)
			}
			if _, err := os.Stat(filepath.Join(root, "approved.txt")); !os.IsNotExist(err) {
				t.Fatal("tool ran before approval", err)
			}
			approval := status.PendingApprovals[0]
			route := "/api/r3/executions/" + e.RequestID + "/approvals/" + approval.ApprovalID
			explicitDecision := decision
			if decision == "approve_failed" {
				explicitDecision = "approve"
			}
			body, _ := json.Marshal(map[string]any{"digest": approval.Digest, "decision": explicitDecision})
			if code, _ := request("POST", route, body, "55555555-5555-4555-8555-555555555555"); code != 403 {
				t.Fatal("wrong installation", code)
			}
			if code, _ := request("POST", route, []byte(`{"digest":"`+approval.Digest+`","decision":"approve","arguments":{}}`), install); code != 400 {
				t.Fatal("mutable approval", code)
			}
			if code, raw := request("POST", route, body, install); code != 200 || !bytes.Contains(raw, []byte(`"resolved":true`)) {
				t.Fatal(code, string(raw))
			}
			instance.r3Executions.Wait()
			_, raw = request("GET", "/api/r3/executions/"+e.RequestID, nil, install)
			_ = json.Unmarshal(raw, &status)
			if decision == "approve_failed" {
				if status.State != "unknown" || status.Result != nil {
					t.Fatal("failed tool falsely completed", string(raw))
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(root), filepath.Base(root)+"-outside.txt")); !os.IsNotExist(err) {
					t.Fatal("failed tool wrote outside workspace", err)
				}
				return
			}
			if status.State != "completed" || status.Result == nil {
				t.Fatal(string(raw))
			}
			var payload r3execution.Payload
			_ = json.Unmarshal([]byte(status.Result.Payload), &payload)
			if decision == "approve" {
				got, err := os.ReadFile(filepath.Join(root, "approved.txt"))
				if err != nil || string(got) != "synthetic approved bytes" || len(payload.Files) != 1 {
					t.Fatal("approved tool failed", err, payload)
				}
			} else if len(payload.Files) != 0 || !bytes.Contains([]byte(payload.Content), []byte("rejected")) {
				t.Fatal("rejected tool affected output", payload)
			}
			if sessions, err := shared.ListSessions(t.Context()); err != nil || len(sessions) != 0 {
				t.Fatal("legacy state leak", sessions, err)
			}
		})
	}
}
