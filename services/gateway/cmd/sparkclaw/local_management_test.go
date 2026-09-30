package main

import (
	"context"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostManagementSocketSurvivesAllUserRevocationsAndIsPrivate(t *testing.T) {
	provisioning := desktopProvisioningFixture(t, "desktop-client-token-that-is-long-enough")
	cfg := configtest.MustLoadDefault()
	cfg.Gateway = provisioning.Gateway
	cfg.Gateway.PairingRequired = true
	cfg.Model.Mock = true
	cfg.Storage.TraceDir = t.TempDir()
	cfg.Storage.ArtifactDir = t.TempDir()
	cfg.Workspaces.DefaultRoot = t.TempDir()
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	statePath := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerProvisionedDesktopClient(t.Context(), cfg, st); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RevokeClient(t.Context(), "client-desktop"); err != nil {
		t.Fatal(err)
	}
	st, err = store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerProvisionedDesktopClient(t.Context(), cfg, st); err == nil {
		t.Fatal("revoked desktop registration must remain rejected")
	}
	const token = "independent-local-management-test-only-token"
	managementRoot, err := os.MkdirTemp("", "sparkclaw-management-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(managementRoot) })
	cfg.Gateway.LocalManagementFile = filepath.Join(managementRoot, "local-management.json")
	bytes, err := json.Marshal(desktopClientProvisioning{SchemaVersion: 1, DeploymentID: cfg.Gateway.DeploymentID, ClientID: "local_management_test", OwnerID: "owner", ActorID: "owner", ClientName: "Local credential management", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Gateway.LocalManagementFile, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(cfg.Gateway.LocalManagementFile), "management")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	s := gateway.New(cfg, st, tools, runtime)
	server, err := startLocalCredentialManagement(t.Context(), cfg, st, s)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	socket := filepath.Join(dir, "credentials.sock")
	info, err := os.Lstat(socket)
	if err != nil || info.Mode().Perm() != 0o600 || !localFileOwnedByCurrentUser(info) {
		t.Fatalf("socket privacy info=%#v err=%v", info, err)
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest("POST", "http://local/api/clients", strings.NewReader(`{"client_name":"Replacement Mac"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "host-recovery-0001")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("recovery issue=%d", res.StatusCode)
	}
	if _, err := startLocalCredentialManagement(t.Context(), cfg, st, s); err == nil {
		t.Fatal("must not replace live socket")
	}
	revoked, found, err := st.GetClient(t.Context(), "client-desktop")
	if err != nil || !found || revoked.RevokedAt == nil {
		t.Fatal("revoked Desktop was revived")
	}
}

func TestHostManagementUnsetPreservesDefaultStartup(t *testing.T) {
	server, err := startLocalCredentialManagement(t.Context(), config.Default(), nil, nil)
	if server != nil || err != nil {
		t.Fatalf("default management listener=%#v err=%v", server, err)
	}
}

func TestProvisioningRejectsNonPrivateFilesAndSymlinkDirectories(t *testing.T) {
	cfg := desktopProvisioningFixture(t, "desktop-client-token-that-is-long-enough")
	if err := os.Chmod(cfg.Gateway.DesktopClientFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDesktopClientProvisioning(cfg.Gateway.DesktopClientFile); err == nil {
		t.Fatal("public credential accepted")
	}
	if err := os.Chmod(cfg.Gateway.DesktopClientFile, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked-runtime")
	if err := os.Symlink(filepath.Dir(cfg.Gateway.DesktopClientFile), link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDesktopClientProvisioning(filepath.Join(link, "desktop-client.json")); err == nil {
		t.Fatal("symlink ancestor accepted")
	}
}
