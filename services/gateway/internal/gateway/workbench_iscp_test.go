package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

const iscpTestInstallation = "11111111-1111-4111-8111-111111111111"
const iscpTestRequest = "44444444-4444-4444-8444-444444444444"

var iscpInvocationSequence atomic.Uint64

func workbenchISCPFixture(t *testing.T, execute execution.Executor) (*Server, *store.FileStore, iscpworkbench.Config, iscpworkbench.Handler) {
	t.Helper()
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Gateway.PairingRequired, cfg.Gateway.WorkbenchISCPLocalTest = true, true
	cfg.Gateway.DeploymentID = "iscp-test-deployment"
	repository, err := store.NewFileStore(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.RegisterClient(t.Context(), app.Client{ID: "iscp-desktop-client", OwnerID: "iscp-owner", ActorID: "iscp-actor", Name: "ISCP desktop", TokenHash: hashSecret("synthetic-issued-client-credential-never-used-by-iscp")}); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, repository)
	runtime := agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	server := New(cfg, repository, tools, runtime, WithExecutions(filepath.Join(root, "execution"), execute))
	ctx, cancel := context.WithCancel(t.Context())
	server.BindLifecycleContext(ctx)
	t.Cleanup(func() {
		cancel()
		if server.executions != nil {
			server.executions.Wait()
			server.executions.Close()
		}
		_ = tools.Close()
	})
	transport := iscpworkbench.Config{SchemaVersion: 1, Mode: "local-test", Role: iscpworkbench.RoleResponder, Binding: &iscpworkbench.Binding{DeploymentID: cfg.Gateway.DeploymentID, OwnerID: "iscp-owner", ClientID: "iscp-desktop-client"}}
	handler, err := server.NewWorkbenchISCPHandler(transport)
	if err != nil {
		t.Fatal(err)
	}
	return server, repository, transport, handler
}

func workbenchISCPRequest(operation string, body []byte) iscpworkbench.Request {
	return iscpworkbench.Request{Type: iscpworkbench.RequestType, Profile: iscpworkbench.Profile, ID: fmt.Sprintf("55555555-5555-4555-8555-%012x", iscpInvocationSequence.Add(1)), Operation: operation, InstallationID: iscpTestInstallation, RequestID: iscpTestRequest, Body: body, InputDigest: execution.Digest(body)}
}

func workbenchISCPEnvelope() execution.Envelope {
	return execution.Envelope{SchemaVersion: 1, DeploymentID: "iscp-test-deployment", OwnerID: "iscp-owner", ClientID: "iscp-desktop-client", InstallationID: iscpTestInstallation, ConversationID: "22222222-2222-4222-8222-222222222222", TaskID: "33333333-3333-4333-8333-333333333333", RequestID: iscpTestRequest, Messages: []execution.Message{{Role: "assistant", Content: "synthetic desktop original history"}, {Role: "user", Content: "hello synthetic desktop original request"}}}
}

func bindWorkbenchISCP(t *testing.T, handler iscpworkbench.Handler) {
	t.Helper()
	raw := []byte(`{"schema_version":1,"installation_id":"` + iscpTestInstallation + `"}`)
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationBind, raw)); result.Status != http.StatusOK {
		t.Fatalf("bind: %+v", result)
	}
}

func TestWorkbenchISCPRealHandlersAndWorkflowPreserveDesktopDurableResult(t *testing.T) {
	server, repository, _, handler := workbenchISCPFixture(t, nil)
	identity := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationIdentity, nil))
	if identity.Status != http.StatusOK || !bytes.Contains(identity.Body, []byte(`"client_id":"iscp-desktop-client"`)) || !bytes.Contains(identity.Body, []byte(`"owner_id":"iscp-owner"`)) {
		t.Fatalf("identity: %+v", identity)
	}
	for _, op := range []string{iscpworkbench.OperationConfig, iscpworkbench.OperationOwner, iscpworkbench.OperationReady} {
		if result := handler(t.Context(), workbenchISCPRequest(op, nil)); result.Status != http.StatusOK {
			t.Fatalf("startup %s: %+v", op, result)
		}
	}
	bindWorkbenchISCP(t, handler)
	raw, err := json.MarshalIndent(workbenchISCPEnvelope(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	submit := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationSubmit, raw))
	if submit.Status != http.StatusAccepted {
		t.Fatalf("submit: %+v", submit)
	}
	server.executions.Wait()
	lookup := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationLookup, nil))
	var status execution.Status
	if err = json.Unmarshal(lookup.Body, &status); err != nil || lookup.Status != http.StatusOK || status.State != "completed" || status.Result == nil || status.InputDigest != execution.Digest(raw) {
		t.Fatalf("actual workflow lookup: %+v %v", lookup, err)
	}
	payload := []byte(status.Result.Payload)
	if execution.Digest(payload) != status.Result.Digest {
		t.Fatal("result digest does not match payload")
	}
	var answer execution.Payload
	if err = json.Unmarshal(payload, &answer); err != nil || strings.TrimSpace(answer.Content) == "" || len(answer.Files) != 0 {
		t.Fatalf("text answer: %+v %v", answer, err)
	}
	// Lost acceptance performs lookup/replay with the identical persisted body.
	replay := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationSubmit, raw))
	if replay.Status != http.StatusAccepted {
		t.Fatalf("same request replay: %+v", replay)
	}
	ack, _ := json.Marshal(map[string]any{"sequence": status.Result.Sequence, "digest": status.Result.Digest, "durable": true})
	volatile, _ := json.Marshal(map[string]any{"sequence": status.Result.Sequence, "digest": status.Result.Digest, "durable": false})
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationAck, volatile)); result.Status != http.StatusConflict {
		t.Fatalf("non-durable ACK was accepted: %+v", result)
	}
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationAck, ack)); result.Status != http.StatusOK {
		t.Fatalf("durable ACK: %+v", result)
	}
	lookup = handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationLookup, nil))
	if !bytes.Contains(lookup.Body, []byte(`"state":"delivered"`)) || bytes.Contains(lookup.Body, []byte(`"result"`)) {
		t.Fatalf("delivered state: %+v", lookup)
	}
	reader, err := execution.Read(server.executionRoot)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := reader.Lookup("iscp-owner", "iscp-desktop-client", iscpTestRequest)
	if err != nil || durable.State != "delivered" || durable.InputDigest != execution.Digest(raw) {
		t.Fatalf("durable receipt: %+v %v", durable, err)
	}
	sessions, err := repository.ListSessions(t.Context())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("desktop history entered backend sessions: %v %v", sessions, err)
	}
	ledger, err := os.ReadFile(filepath.Join(server.executionRoot, "control.json"))
	if err != nil || bytes.Contains(ledger, []byte("synthetic desktop original")) {
		t.Fatalf("control ledger leaked content: %v", err)
	}
}

func TestWorkbenchISCPRejectsWrongScopeRevocationAndUnregisteredOperations(t *testing.T) {
	server, repository, cfg, handler := workbenchISCPFixture(t, nil)
	for _, mutate := range []func(*iscpworkbench.Config){
		func(c *iscpworkbench.Config) { c.Role = iscpworkbench.RoleInitiator },
		func(c *iscpworkbench.Config) { c.Binding.DeploymentID = "other-deployment" },
		func(c *iscpworkbench.Config) { c.Binding.OwnerID = "other-owner" },
		func(c *iscpworkbench.Config) { c.Binding.ClientID = "missing-client" },
	} {
		candidate := cfg
		copy := *cfg.Binding
		candidate.Binding = &copy
		mutate(&candidate)
		if _, err := server.NewWorkbenchISCPHandler(candidate); err == nil {
			t.Fatalf("wrong responder binding accepted: %+v", candidate)
		}
	}
	if result := handler(t.Context(), workbenchISCPRequest("https://example.invalid/api/owners", nil)); result.Status != http.StatusNotImplemented {
		t.Fatalf("arbitrary route accepted: %+v", result)
	}
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationOwner, []byte(`{"owner_id":"other-owner"}`))); result.Status != http.StatusBadRequest {
		t.Fatalf("owner override accepted: %+v", result)
	}
	bindWorkbenchISCP(t, handler)
	connected, release, err := server.clientConnectionContext(t.Context(), cfg.Binding.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = repository.RevokeClient(t.Context(), cfg.Binding.ClientID); err != nil {
		t.Fatal(err)
	}
	server.cancelClientConnections(cfg.Binding.ClientID)
	if connected.Err() == nil {
		t.Fatal("Client revocation did not cancel the responder lifecycle")
	}
	for _, op := range iscpworkbench.Operations() {
		if result := handler(t.Context(), workbenchISCPRequest(op, []byte(`{}`))); result.Status != http.StatusUnauthorized {
			t.Errorf("revoked binding reached %s: %+v", op, result)
		}
	}
}

func TestWorkbenchISCPRejectsUnsupportedInputBeforeExecutionAndWholeOversizeResult(t *testing.T) {
	var admitted atomic.Int32
	server, _, _, handler := workbenchISCPFixture(t, func(context.Context, execution.Envelope, map[string][]byte) (execution.Output, error) {
		admitted.Add(1)
		return execution.Output{Content: strings.Repeat("x", iscpworkbench.MaxResponseBytes)}, nil
	})
	bindWorkbenchISCP(t, handler)
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationSubmit, []byte(strings.Repeat("x", iscpworkbench.MaxBodyBytes+1)))); result.Status != http.StatusRequestEntityTooLarge || admitted.Load() != 0 {
		t.Fatalf("oversized input was admitted: %+v", result)
	}
	e := workbenchISCPEnvelope()
	e.InputFiles = []execution.File{{ID: "66666666-6666-4666-8666-666666666666", Name: "attached.txt", Size: 1, SHA256: execution.Digest([]byte("x"))}}
	raw, _ := json.Marshal(e)
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationSubmit, raw)); result.Status != http.StatusNotImplemented || admitted.Load() != 0 {
		t.Fatalf("unsupported attachment admitted: %+v", result)
	}
	e.InputFiles = nil
	e.OwnerID = "other-owner"
	raw, _ = json.Marshal(e)
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationSubmit, raw)); result.Status != http.StatusConflict || admitted.Load() != 0 {
		t.Fatalf("wrong execution owner admitted: %+v", result)
	}
	raw, _ = json.Marshal(workbenchISCPEnvelope())
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationSubmit, raw)); result.Status != http.StatusAccepted {
		t.Fatalf("text input: %+v", result)
	}
	server.executions.Wait()
	result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationLookup, nil))
	if result.Status != http.StatusRequestEntityTooLarge || len(result.Body) != 0 {
		t.Fatalf("oversized result was trimmed: %+v", result)
	}
	status, err := server.executions.Lookup("iscp-owner", "iscp-desktop-client", iscpTestRequest)
	if err != nil || status.State != "completed" || status.Result == nil || admitted.Load() != 1 {
		t.Fatalf("oversized result lost durable state: %+v %v", status, err)
	}
}

func TestWorkbenchISCPCancellationUsesExistingExecutionControl(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	server, _, _, handler := workbenchISCPFixture(t, func(ctx context.Context, _ execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return execution.Output{}, ctx.Err()
	})
	bindWorkbenchISCP(t, handler)
	raw, _ := json.Marshal(workbenchISCPEnvelope())
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationSubmit, raw)); result.Status != http.StatusAccepted {
		t.Fatalf("submit: %+v", result)
	}
	<-started
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationCancel, []byte(`{}`))); result.Status != http.StatusOK || !bytes.Contains(result.Body, []byte(`"state":"unknown"`)) {
		t.Fatalf("cancel: %+v", result)
	}
	<-cancelled
	server.executions.Wait()
}
