package localmind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

const (
	DynamicSource = "mcp.localmind"

	delegateRemoteName = "delegate_to_localmind"
	getTaskRemoteName  = "get_localmind_task"
	controlRemoteName  = "control_localmind_task"

	delegateLocalName = "localmind.task.delegate"
	getTaskLocalName  = "localmind.task.get"
	cancelLocalName   = "localmind.task.cancel"
)

type Snapshot struct {
	ServerInfo          mcpclient.ServerInfo `json:"server_info"`
	ProtocolVersion     string               `json:"protocol_version"`
	EndpointID          string               `json:"endpoint_id"`
	Revision            string               `json:"revision"`
	RemoteToolNames     []string             `json:"remote_tool_names"`
	RegisteredToolNames []string             `json:"registered_tool_names"`
	RefreshedAt         time.Time            `json:"refreshed_at"`
}

type Manager struct {
	cfg  config.MCPServerConfig
	hub  *toolhub.ToolHub
	http *http.Client
	env  func(string) string

	refreshMu sync.Mutex
	mu        sync.RWMutex
	snapshot  Snapshot
	hasState  bool
}

func New(cfg config.MCPServerConfig, hub *toolhub.ToolHub, httpClient *http.Client) (*Manager, error) {
	if hub == nil {
		return nil, errors.New("LocalMind requires a ToolHub")
	}
	if strings.TrimSpace(cfg.URLEnv) == "" || strings.TrimSpace(cfg.BearerTokenEnv) == "" {
		return nil, errors.New("LocalMind url_env and bearer_token_env are required")
	}
	if cfg.ProtocolVersion != config.LocalMindMCPProtocolVersion || cfg.ExpectedServerName != config.LocalMindMCPServerName {
		return nil, errors.New("LocalMind MCP identity was not normalized")
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	manager := &Manager{cfg: cfg, hub: hub, http: &clientCopy, env: os.Getenv}
	if err := hub.ReplaceDynamicTools(DynamicSource, nil); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Refresh(ctx context.Context) (snapshot Snapshot, err error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	defer func() {
		if err == nil {
			return
		}
		if clearErr := m.clearState(); clearErr != nil {
			err = errors.Join(err, clearErr)
		}
	}()

	runtime, err := m.runtimeConfig()
	if err != nil {
		return Snapshot{}, err
	}
	client, err := mcpclient.New(mcpclient.Config{
		Endpoint:           runtime.endpoint,
		BearerToken:        runtime.token,
		Namespace:          m.cfg.Namespace,
		ExpectedServerName: m.cfg.ExpectedServerName,
		RequestTimeout:     time.Duration(m.cfg.RequestTimeoutSeconds) * time.Second,
		LongCallGrace:      time.Duration(m.cfg.LongCallGraceSeconds) * time.Second,
		MaxResponseBytes:   m.cfg.MaxResponseBytes,
		ClientInfo:         mcpclient.ClientInfo{Name: "sparkclaw-localmind", Version: "0.1.0"},
	}, m.http)
	if err != nil {
		return Snapshot{}, safeError(err, runtime)
	}
	initialized, err := client.Initialize(ctx)
	if err != nil {
		return Snapshot{}, safeError(err, runtime)
	}
	if initialized.ProtocolVersion != m.cfg.ProtocolVersion {
		return Snapshot{}, fmt.Errorf("LocalMind negotiated unsupported MCP protocol version %q", initialized.ProtocolVersion)
	}
	if _, advertised := initialized.Capabilities["resources"]; advertised {
		return Snapshot{}, errors.New("LocalMind task MCP must not advertise Resources")
	}
	if _, advertised := initialized.Capabilities["tools"]; !advertised {
		return Snapshot{}, errors.New("LocalMind task MCP did not advertise tools")
	}
	listed, err := client.ListTools(ctx, "")
	if err != nil {
		return Snapshot{}, safeError(err, runtime)
	}
	if strings.TrimSpace(listed.NextCursor) != "" {
		return Snapshot{}, errors.New("LocalMind task MCP returned a paginated tool contract")
	}
	if err := validateTaskToolContract(listed.Tools); err != nil {
		return Snapshot{}, err
	}

	snapshot = Snapshot{
		ServerInfo: initialized.ServerInfo, ProtocolVersion: initialized.ProtocolVersion,
		EndpointID: runtime.endpointID, Revision: taskContractRevision(runtime.endpointID, initialized, listed.Tools),
		RemoteToolNames: []string{delegateRemoteName, getTaskRemoteName, controlRemoteName},
		RefreshedAt:     time.Now().UTC(),
	}
	registrations := m.taskRegistrations(client, snapshot)
	for _, registration := range registrations {
		snapshot.RegisteredToolNames = append(snapshot.RegisteredToolNames, registration.Definition.Name)
	}
	if err := m.hub.ReplaceDynamicTools(DynamicSource, registrations); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.snapshot = cloneSnapshot(snapshot)
	m.hasState = true
	m.mu.Unlock()
	return cloneSnapshot(snapshot), nil
}

func (m *Manager) Run(ctx context.Context) {
	interval := time.Duration(m.cfg.RefreshIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.Refresh(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("LocalMind MCP refresh failed", "error", err)
			}
		}
	}
}

func (m *Manager) Snapshot() (Snapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.hasState {
		return Snapshot{}, false
	}
	return cloneSnapshot(m.snapshot), true
}

type resolvedRuntime struct {
	endpoint   string
	token      string
	endpointID string
}

func (m *Manager) runtimeConfig() (resolvedRuntime, error) {
	endpoint := strings.TrimSpace(m.env(m.cfg.URLEnv))
	token := strings.TrimSpace(m.env(m.cfg.BearerTokenEnv))
	if endpoint == "" {
		return resolvedRuntime{}, fmt.Errorf("LocalMind endpoint environment variable %s is empty", m.cfg.URLEnv)
	}
	if token == "" {
		return resolvedRuntime{}, fmt.Errorf("LocalMind bearer token environment variable %s is empty", m.cfg.BearerTokenEnv)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return resolvedRuntime{}, errors.New("LocalMind endpoint must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return resolvedRuntime{}, errors.New("LocalMind endpoint cannot contain credentials, query parameters, or a fragment")
	}
	if !validWorkspaceEndpointPath(parsed.EscapedPath()) {
		return resolvedRuntime{}, errors.New("LocalMind endpoint must end with /api/workspaces/<workspace-id>/mcp")
	}
	if parsed.Scheme == "http" && (!m.cfg.AllowPrivateHTTP || !privateHTTPHost(parsed.Hostname())) {
		return resolvedRuntime{}, errors.New("LocalMind plain HTTP requires allow_private_http and a loopback, private, or container host")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	endpoint = parsed.String()
	sum := sha256.Sum256([]byte(endpoint))
	return resolvedRuntime{endpoint: endpoint, token: token, endpointID: "lm_" + hex.EncodeToString(sum[:12])}, nil
}

func validWorkspaceEndpointPath(escapedPath string) bool {
	parts := strings.Split(strings.Trim(strings.TrimSpace(escapedPath), "/"), "/")
	workspaceID := ""
	if len(parts) >= 2 {
		workspaceID = parts[len(parts)-2]
	}
	return len(parts) >= 4 && parts[len(parts)-4] == "api" && parts[len(parts)-3] == "workspaces" &&
		workspaceID != "" && workspaceID != "." && workspaceID != ".." &&
		!strings.Contains(strings.ToLower(workspaceID), "%2f") && parts[len(parts)-1] == "mcp"
}

func privateHTTPHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" || host == "host.docker.internal" || host == "gateway.docker.internal" || (!strings.Contains(host, ".") && !strings.Contains(host, ":")) {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

func (m *Manager) clearState() error {
	clearErr := m.hub.ReplaceDynamicTools(DynamicSource, nil)
	m.mu.Lock()
	m.snapshot = Snapshot{}
	m.hasState = false
	m.mu.Unlock()
	return clearErr
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.RemoteToolNames = append([]string(nil), snapshot.RemoteToolNames...)
	snapshot.RegisteredToolNames = append([]string(nil), snapshot.RegisteredToolNames...)
	return snapshot
}
