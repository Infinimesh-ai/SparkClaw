package gateway

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
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
	return server, repository, transport, linkDomainTestChain(t, server, transport)
}

func workbenchISCPRequest(operation string, body []byte) iscpworkbench.Request {
	return iscpworkbench.Request{Type: iscpworkbench.RequestType, Profile: iscpworkbench.Profile, ID: fmt.Sprintf("55555555-5555-4555-8555-%012x", iscpInvocationSequence.Add(1)), Operation: operation, InstallationID: iscpTestInstallation, RequestID: iscpTestRequest, Body: body, InputDigest: execution.Digest(body)}
}

func workbenchISCPEnvelope() execution.Envelope {
	return execution.Envelope{SchemaVersion: 2, DeploymentID: "iscp-test-deployment", OwnerID: "iscp-owner", ClientID: "iscp-desktop-client", InstallationID: iscpTestInstallation, ConversationID: "22222222-2222-4222-8222-222222222222", TaskID: "33333333-3333-4333-8333-333333333333", RequestID: iscpTestRequest, Messages: []execution.Message{{ID: "fixture-message-69-1", Role: "assistant", Content: "synthetic desktop original history"}, {ID: "fixture-message-69-2", Role: "user", Content: "hello synthetic desktop original request"}}}
}

func bindWorkbenchISCP(t *testing.T, handler iscpworkbench.Handler) {
	t.Helper()
	raw := []byte(`{"schema_version":1,"installation_id":"` + iscpTestInstallation + `"}`)
	if result := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationBind, raw)); result.Status != http.StatusOK {
		t.Fatalf("bind: %+v", result)
	}
}
