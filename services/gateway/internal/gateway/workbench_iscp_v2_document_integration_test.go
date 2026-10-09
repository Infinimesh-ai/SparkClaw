package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// Only model HTTP and Relay queue delivery are synthetic. This retains the
// actual scoped workflow, ToolHub reader/editor, Policy, durable approval,
// artifact commit, encrypted chunk download and final delivery ACK.
func TestWorkbenchISCPV2RealDocumentToolRequiresApproval(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("actual document execution requires Linux /dev/shm for the protected transient workspace")
	}
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			root, err := os.MkdirTemp("/dev/shm", "sparkclaw-iscp-v2-document-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			model, calls := executionDocumentApprovalModel(t)
			t.Cleanup(model.Close)
			cfg := testConfig(root)
			cfg.Model.Mock = false
			cfg.Model.Fast.BaseURL = model.URL
			cfg.Model.Deep.BaseURL = model.URL
			cfg.Model.Embedding.BaseURL = model.URL
			cfg.Model.Guard.BaseURL = model.URL
			cfg.Gateway.PairingRequired = true
			cfg.Gateway.WorkbenchISCPLocalTest = true
			cfg.Gateway.DeploymentID = "iscp-test-deployment"
			cfg.Security.ApprovalRequiredTools = append(cfg.Security.ApprovalRequiredTools, "text.replace_text")
			repository, err := store.NewFileStore(filepath.Join(root, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repository.RegisterClient(t.Context(), app.Client{ID: "iscp-desktop-client", OwnerID: "iscp-owner", ActorID: "iscp-actor", Name: "isolated document fixture", TokenHash: hashSecret("synthetic-never-used-business-http-token")}); err != nil {
				t.Fatal(err)
			}
			tools := toolhub.New(cfg, repository)
			t.Cleanup(func() { _ = tools.Close() })
			runtime, err := agent.NewRuntimeWithContext(t.Context(), repository, tools, policy.New(cfg), modelrouter.New(cfg), nil)
			if err != nil {
				t.Fatal(err)
			}
			server := New(cfg, repository, tools, runtime, WithExecutions(filepath.Join(root, "execution"), nil))
			lifecycle, cancel := context.WithCancel(t.Context())
			server.BindLifecycleContext(lifecycle)
			t.Cleanup(func() {
				cancel()
				if server.executions != nil {
					server.executions.Close()
				}
			})
			local := wb.Config{SchemaVersion: 1, Mode: "local-test", Role: wb.RoleResponder, Binding: &wb.Binding{DeploymentID: cfg.Gateway.DeploymentID, OwnerID: "iscp-owner", ClientID: "iscp-desktop-client"}}
			f := startV2EncryptedFixture(t, server, local, []string{"tools.invoke", "tool.files.read", "tool.observation.read", "tool.text.replace_text"}, []string{wb.OperationToolsInvoke})
			f.bind(t)
			const original = "# Notes\nOriginal reflection\n"
			const updated = "# Notes\nImproved reflection\n"
			input := execution.File{ID: "99999999-9999-4999-8999-999999999999", Name: "notes.md", Size: len(original), SHA256: execution.Digest([]byte(original))}
			ref := f.upload(t, "execution_input", input.Name, "text/markdown", []byte(original))
			stage := v2Request(wb.OperationExecutionInputPut, nil)
			stage.Object = &ref
			stage.InputDigest = ref.SHA256
			stage.Params = map[string]string{"request_id": iscpTestRequest, "file_id": input.ID}
			requireV2Status(t, f.call(t, stage), 200)
			envelope := workbenchISCPEnvelope()
			envelope.Messages = []execution.Message{{Role: "user", Content: "Edit notes.md and replace Original reflection with Improved reflection."}}
			envelope.InputFiles = []execution.File{input}
			raw, _ := json.Marshal(envelope)
			requireV2Status(t, f.call(t, v2Request(wb.OperationSubmit, raw)), 202)
			lookup := func() execution.Status {
				t.Helper()
				r := f.call(t, v2Request(wb.OperationLookup, nil))
				requireV2Status(t, r, 200)
				body := r.Body
				if r.Object != nil {
					body = f.download(t, *r.Object)
				}
				var status execution.Status
				if err = json.Unmarshal(body, &status); err != nil {
					t.Fatal(err)
				}
				return status
			}
			var status execution.Status
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				status = lookup()
				if len(status.PendingApprovals) > 0 || (status.State != "running" && status.State != "accepted") {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if status.State != "running" || len(status.PendingApprovals) != 1 || status.Result != nil {
				t.Fatalf("real editor did not reach approval: %+v result=%+v (model calls %v)", status, status.Result, calls.snapshot())
			}
			approval := status.PendingApprovals[0]
			if approval.Tool != "text.replace_text" {
				t.Fatalf("unexpected tool %s", approval.Tool)
			}
			inputPath, _ := approval.Arguments["path"].(string)
			outputPath, _ := approval.Arguments["output_path"].(string)
			if inputPath != "notes.md" || outputPath != "notes-2.md" {
				t.Fatalf("editor paths escaped governed files: %+v", approval.Arguments)
			}
			workspaces, err := filepath.Glob(filepath.Join("/dev/shm", executionWorkspacePrefix(server.executionRoot)+"*"))
			if err != nil || len(workspaces) != 1 {
				t.Fatalf("tmpfs workspace: %v %v", workspaces, err)
			}
			workspace := filepath.Join(workspaces[0], "workspace")
			before, err := os.ReadFile(filepath.Join(workspace, inputPath))
			if err != nil || string(before) != original {
				t.Fatal("actual workflow did not read uploaded source")
			}
			if _, err = os.Stat(filepath.Join(workspace, outputPath)); !os.IsNotExist(err) {
				t.Fatal("editor wrote output before approval")
			}
			reads := 0
			err = filepath.WalkDir(filepath.Join(workspaces[0], "artifacts"), func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil || entry.IsDir() {
					return walkErr
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				var row struct {
					Tool   string
					Status app.ToolCallStatus
				}
				if json.Unmarshal(raw, &row) == nil && row.Tool == "files.read" {
					if row.Status != app.ToolCallStatusCompleted || !bytes.Contains(raw, []byte("Original reflection")) {
						t.Error("actual reader receipt lacks source evidence")
					}
					reads++
				}
				return nil
			})
			if err != nil || reads != 1 {
				t.Fatalf("actual reader count %d: %v", reads, err)
			}
			body, _ := json.Marshal(map[string]string{"digest": approval.Digest, "decision": decision, "input_digest": status.InputDigest})
			q := v2Request(wb.OperationExecutionApproval, body)
			q.ExpectedRevision = fmt.Sprint(status.Revision)
			q.Params = map[string]string{"request_id": iscpTestRequest, "approval_id": approval.ApprovalID}
			accepted := f.call(t, q)
			requireV2Status(t, accepted, 200)
			deadline = time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				status = lookup()
				if status.State != "running" && status.State != "accepted" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if status.State != "completed" || status.Result == nil {
				t.Fatalf("actual editor did not complete %+v", status)
			}
			var payload execution.Payload
			if json.Unmarshal([]byte(status.Result.Payload), &payload) != nil {
				t.Fatal("invalid result payload")
			}
			if decision == "approve" {
				if len(payload.Files) != 1 || payload.Files[0].Name != outputPath || payload.Files[0].SHA256 != execution.Digest([]byte(updated)) {
					t.Fatalf("actual edited artifact missing %+v", payload)
				}
				get := v2Request(wb.OperationExecutionFileGet, nil)
				get.Params = map[string]string{"request_id": iscpTestRequest, "file_id": payload.Files[0].ID}
				result := f.call(t, get)
				requireV2Status(t, result, 200)
				if result.Object == nil || string(f.download(t, *result.Object)) != updated {
					t.Fatal("downloaded actual editor bytes disagree")
				}
			} else if len(payload.Files) != 0 {
				t.Fatal("rejected edit produced files")
			}
			q.ID = v2Request(wb.OperationIdentity, nil).ID
			replay := f.call(t, q)
			requireV2Status(t, replay, 200)
			if !bytes.Equal(replay.Body, accepted.Body) {
				t.Fatal("decision replay lost original receipt")
			}
			if got := calls.snapshot(); got != [3]int32{1, 0, 1} {
				t.Fatalf("editor rerouted or repeated model proposal: %v", got)
			}
			requireV2Status(t, f.jsonCall(t, wb.OperationAck, map[string]any{"sequence": status.Result.Sequence, "digest": status.Result.Digest, "durable": true}), 200)
			if status = lookup(); status.State != "delivered" || status.Result != nil {
				t.Fatal("durable artifact ACK did not finish delivery")
			}
			if _, err = os.Stat(workspaces[0]); !os.IsNotExist(err) {
				t.Fatal("terminal workflow retained transient source")
			}
			if sessions, err := repository.ListSessions(t.Context()); err != nil || len(sessions) != 0 {
				t.Fatal("desktop execution leaked into backend sessions")
			}
		})
	}
}
