package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestLocalWebChatSocketUsesPrivateProvisioningAndDefaultFileStore(t *testing.T) {
	temporary, err := os.MkdirTemp("", "sc-lw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(temporary) })
	root, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Gateway.DeploymentID = "local-webchat-test"
	cfg.Gateway.LocalWebChatEnabled = true
	cfg.Gateway.LocalWebChatFile = filepath.Join(root, "local-webchat.json")
	cfg.Gateway.PairingRequired = true
	cfg.Storage.TraceDir = t.TempDir()
	cfg.Storage.ArtifactDir = t.TempDir()
	cfg.Workspaces.DefaultRoot = t.TempDir()
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	const secret = "independent-private-ingress-secret-test-only"
	p := desktopClientProvisioning{SchemaVersion: 1, DeploymentID: cfg.Gateway.DeploymentID, ClientID: "local_webchat_test", OwnerID: "owner", ActorID: "owner", ClientName: "Local WebChat", Token: secret}
	raw, _ := json.Marshal(p)
	if err := os.WriteFile(cfg.Gateway.LocalWebChatFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "local-webchat")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFileStore(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	s := gateway.New(cfg, st, tools, runtime)
	server, err := startLocalWebChat(t.Context(), cfg, st, s)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	socket := filepath.Join(directory, "workbench.sock")
	info, err := os.Stat(socket)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket: %v %v", info, err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	req, _ := http.NewRequest("GET", "http://localhost:18794/api/workbench/identity", nil)
	req.Header.Set("X-SparkClaw-Local-Ingress", secret)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("file backend local identity: %d", res.StatusCode)
	}
	if _, err := startLocalWebChat(t.Context(), cfg, st, s); err == nil {
		t.Fatal("replaced live socket")
	}
	if _, found, _ := st.GetClient(t.Context(), p.ClientID); found {
		t.Fatal("private principal persisted as Client")
	}
	server.Close()
	if err := os.Chmod(cfg.Gateway.LocalWebChatFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := startLocalWebChat(t.Context(), cfg, st, s); err == nil {
		t.Fatal("unsafe credentials accepted")
	}
	if disabled, err := startLocalWebChat(t.Context(), config.Default(), nil, nil); disabled != nil || err != nil {
		t.Fatalf("disabled: %v %v", disabled, err)
	}
}
