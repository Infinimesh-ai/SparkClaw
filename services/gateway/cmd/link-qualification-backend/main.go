// Command link-qualification-backend is the fixture backend of the desktop
// qualification launcher (apps/desktop/test/run-isolated-qualification.mjs):
// a Gateway with an in-memory store and the mock model, reached only over
// the LAN connector, with a local device directory. It joins one desktop by
// writing that desktop's link-helper state, so no console and no Relay are
// involved. It prints one JSON ready line and serves until it is stopped.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/devicestate"
)

const domainID = "dom_qualification"

func main() {
	root := flag.String("dir", "", "private fixture directory (backend state and workspace)")
	deviceState := flag.String("device-state", "", "the desktop's link-helper state directory to write (<userData>/link)")
	deviceName := flag.String("device-name", "SparkX Qualification", "the desktop's device name")
	flag.Parse()
	if err := run(*root, *deviceState, *deviceName); err != nil {
		fmt.Fprintln(os.Stderr, "link-qualification-backend:", err)
		os.Exit(1)
	}
}

func run(root, deviceState, deviceName string) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(deviceState) {
		return fmt.Errorf("-dir and -device-state must be absolute")
	}
	stateDir := filepath.Join(root, "link")
	for _, dir := range []string{stateDir, deviceState, filepath.Join(root, "workspace")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	provider := crypto.NewProvider()
	host, err := identity.NewDevice(provider, domainID, "sparkclaw-qualification-host", now)
	if err != nil {
		return err
	}
	desktop, err := identity.NewDevice(provider, domainID, "desktop-qualification", now)
	if err != nil {
		return err
	}
	desktopRecord := consoleapi.DeviceRecord{DeviceID: desktop.Identity.DeviceID, DomainID: domainID, Name: deviceName, Type: contract.ClassDesktop,
		Role: consoleapi.RoleOwnerDevice, Identity: desktop.Identity, State: consoleapi.StateTrusted, CreatedAt: now, UpdatedAt: now}
	hostRecord := consoleapi.DeviceRecord{DeviceID: host.Identity.DeviceID, DomainID: domainID, Name: "SparkClaw qualification", Type: consoleapi.TypeSparkClaw,
		Role: consoleapi.RoleDomainHost, Identity: host.Identity, State: consoleapi.StateTrusted, CreatedAt: now, UpdatedAt: now}
	if err := link.WriteJSONAtomic(filepath.Join(stateDir, link.MembershipFile), consoleapi.Membership{DomainID: domainID, Device: hostRecord}); err != nil {
		return err
	}
	if err := link.WriteJSONAtomic(filepath.Join(stateDir, link.DirectoryFile), consoleapi.Directory{DomainID: domainID, Revision: now.UnixNano(), Devices: []consoleapi.DeviceRecord{desktopRecord}, SyncedAt: now}); err != nil {
		return err
	}

	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Gateway.DeploymentID = "qualification-deployment"
	cfg.Workspaces.DefaultRoot = filepath.Join(root, "workspace")
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	cfg.State.Path = filepath.Join(root, "workbench-state.json")
	cfg.Storage.TraceDir = filepath.Join(root, "traces")
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	repository := store.NewMemoryStore()
	tools := toolhub.New(cfg, repository)
	defer tools.Close()
	runtime := agent.NewRuntime(repository, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := gateway.New(cfg, repository, tools, runtime)
	lifecycle, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server.BindLifecycleContext(lifecycle)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	if err := server.StartLink(gateway.LinkOptions{StateDir: stateDir, LANAddr: addr, LANHosts: []string{"127.0.0.1"}, OwnerID: app.DefaultOwnerID}); err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.CloseLink(ctx)
	}()
	raw, err := os.ReadFile(filepath.Join(stateDir, link.LANFile))
	if err != nil {
		return err
	}
	var lan consoleapi.LAN
	if err := json.Unmarshal(raw, &lan); err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(addr)
	lan.Endpoints = []string{"wss://127.0.0.1:" + port + contract.LANPath}

	// The desktop is joined: its membership names this backend and its LAN
	// endpoint. No Grant and no Relay, so only the LAN path opens.
	membership := consoleapi.Membership{DomainID: domainID, DomainName: "Qualification", Device: desktopRecord,
		Host: &consoleapi.Host{DeviceID: host.Identity.DeviceID, Identity: host.Identity, Name: "SparkClaw qualification", LAN: lan}}
	state := devicestate.State{Membership: membership, Identity: desktop.Identity, Private: crypto.Base64URL(desktop.Private.BytesForDevStore()),
		Class: contract.ClassDesktop, Name: deviceName, JoinedAt: now}
	if err := devicestate.WritePrivate(filepath.Join(deviceState, devicestate.StateFile), state); err != nil {
		return err
	}
	ready, _ := json.Marshal(map[string]any{"event": "link_qualification_backend_ready", "lan": lan.Endpoints[0], "device_id": desktop.Identity.DeviceID,
		"deployment_id": cfg.Gateway.DeploymentID, "owner_id": app.DefaultOwnerID})
	fmt.Println(string(ready))
	<-lifecycle.Done()
	return nil
}
