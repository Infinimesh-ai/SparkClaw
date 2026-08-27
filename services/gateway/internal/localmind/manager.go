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

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationrun"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

const (
	DynamicSource = "mcp.localmind"

	delegateRemoteName = "delegate_to_localmind"
	getTaskRemoteName  = "get_localmind_task"
	controlRemoteName  = "control_localmind_task"

	delegateReadLocalName  = "localmind.task.delegate_read"
	delegateWriteLocalName = "localmind.task.delegate"
	getTaskLocalName       = "localmind.task.get"
	cancelLocalName        = "localmind.task.cancel"
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

type Credentials struct {
	Endpoint    string
	BearerToken string
}

type Manager struct {
	cfg  config.MCPServerConfig
	hub  *toolhub.ToolHub
	http *http.Client
	env  func(string) string

	refreshMu  sync.Mutex
	configMu   sync.RWMutex
	managed    *Credentials
	runtimeMu  sync.Mutex
	runs       *integrationrun.Registry
	generation uint64
	updating   bool
	nextCallID uint64
	calls      map[uint64]context.CancelCauseFunc
	mu         sync.RWMutex
	snapshot   Snapshot
	hasState   bool
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
	manager := &Manager{cfg: cfg, hub: hub, http: &clientCopy, env: os.Getenv, generation: 1, calls: map[uint64]context.CancelCauseFunc{}}
	if err := hub.ReplaceDynamicTools(DynamicSource, nil); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) WithIntegrationRuns(registry *integrationrun.Registry) *Manager {
	if m == nil {
		return nil
	}
	m.runtimeMu.Lock()
	m.runs = registry
	m.runtimeMu.Unlock()
	return m
}

func (m *Manager) Refresh(ctx context.Context) (snapshot Snapshot, err error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	return m.refreshLocked(ctx)
}

func (m *Manager) refreshLocked(ctx context.Context) (snapshot Snapshot, err error) {
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
	snapshot, registrations, err := m.prepare(ctx, runtime)
	if err != nil {
		return Snapshot{}, err
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

// CheckCredentials validates the fixed LocalMind MCP identity and task
// contract without changing the selected source or published ToolHub tools.
func (m *Manager) CheckCredentials(ctx context.Context, credentials Credentials) (Snapshot, error) {
	runtime, err := m.resolveRuntime(credentials.Endpoint, credentials.BearerToken)
	if err != nil {
		return Snapshot{}, err
	}
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	snapshot, _, err := m.prepare(ctx, runtime)
	return snapshot, err
}

// ActivateCredentials commits a managed credential as the selected source
// before refreshing. Failure intentionally leaves that selection in place.
func (m *Manager) ActivateCredentials(ctx context.Context, credentials Credentials) (Snapshot, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	m.beginCredentialSwitch()
	defer m.finishCredentialSwitch()
	copy := credentials
	m.configMu.Lock()
	m.managed = &copy
	m.configMu.Unlock()
	return m.refreshLocked(ctx)
}

// ActivateOperator selects the operator environment source. It never falls
// back to a previously selected managed credential.
func (m *Manager) ActivateOperator(ctx context.Context) (Snapshot, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	m.beginCredentialSwitch()
	defer m.finishCredentialSwitch()
	m.configMu.Lock()
	m.managed = nil
	m.configMu.Unlock()
	return m.refreshLocked(ctx)
}

func (m *Manager) beginCredentialSwitch() {
	cause := &app.CodedToolError{Code: app.ToolErrorLocalMindCredentialsChanged, Err: errors.New("LocalMind credentials changed; the task was stopped")}
	m.runtimeMu.Lock()
	m.updating = true
	oldGeneration := m.generation
	calls := make([]context.CancelCauseFunc, 0, len(m.calls))
	for _, cancel := range m.calls {
		calls = append(calls, cancel)
	}
	runs := m.runs
	m.runtimeMu.Unlock()
	if runs != nil {
		runs.CancelGeneration(LocalMindIntegrationID, oldGeneration, cause)
	}
	for _, cancel := range calls {
		cancel(cause)
	}
	m.runtimeMu.Lock()
	m.generation++
	m.runtimeMu.Unlock()
}

func (m *Manager) finishCredentialSwitch() {
	m.runtimeMu.Lock()
	m.updating = false
	m.runtimeMu.Unlock()
}

func (m *Manager) ClearRuntime() error {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	return m.clearState()
}

func (m *Manager) OperatorConfigured() bool {
	return strings.TrimSpace(m.env(m.cfg.URLEnv)) != "" && strings.TrimSpace(m.env(m.cfg.BearerTokenEnv)) != ""
}

func (m *Manager) prepare(ctx context.Context, runtime resolvedRuntime) (Snapshot, []toolhub.DynamicToolRegistration, error) {
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
		return Snapshot{}, nil, safeError(err, runtime)
	}
	initialized, err := client.Initialize(ctx)
	if err != nil {
		return Snapshot{}, nil, safeError(err, runtime)
	}
	if initialized.ProtocolVersion != m.cfg.ProtocolVersion {
		return Snapshot{}, nil, fmt.Errorf("LocalMind negotiated unsupported MCP protocol version %q", initialized.ProtocolVersion)
	}
	if _, advertised := initialized.Capabilities["resources"]; advertised {
		return Snapshot{}, nil, errors.New("LocalMind task MCP must not advertise Resources")
	}
	if _, advertised := initialized.Capabilities["tools"]; !advertised {
		return Snapshot{}, nil, errors.New("LocalMind task MCP did not advertise tools")
	}
	listed, err := client.ListTools(ctx, "")
	if err != nil {
		return Snapshot{}, nil, safeError(err, runtime)
	}
	if strings.TrimSpace(listed.NextCursor) != "" {
		return Snapshot{}, nil, errors.New("LocalMind task MCP returned a paginated tool contract")
	}
	if err := validateTaskToolContract(listed.Tools); err != nil {
		return Snapshot{}, nil, err
	}

	snapshot := Snapshot{
		ServerInfo: initialized.ServerInfo, ProtocolVersion: initialized.ProtocolVersion,
		EndpointID: runtime.endpointID, Revision: taskContractRevision(runtime.endpointID, initialized, listed.Tools),
		RemoteToolNames: []string{delegateRemoteName, getTaskRemoteName, controlRemoteName},
		RefreshedAt:     time.Now().UTC(),
	}
	registrations := m.taskRegistrations(client, snapshot)
	for _, registration := range registrations {
		snapshot.RegisteredToolNames = append(snapshot.RegisteredToolNames, registration.Definition.Name)
	}
	return snapshot, registrations, nil
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
			if !m.Configured() {
				continue
			}
			if _, err := m.Refresh(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("LocalMind MCP refresh failed", "error", err)
			}
		}
	}
}

func (m *Manager) Configured() bool {
	m.configMu.RLock()
	managed := m.managed != nil
	m.configMu.RUnlock()
	return managed || m.OperatorConfigured()
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
	m.configMu.RLock()
	managed := m.managed
	if managed != nil {
		copy := *managed
		managed = &copy
	}
	m.configMu.RUnlock()
	if managed != nil {
		return m.resolveRuntime(managed.Endpoint, managed.BearerToken)
	}
	return m.resolveRuntime(m.env(m.cfg.URLEnv), m.env(m.cfg.BearerTokenEnv))
}

func (m *Manager) resolveRuntime(endpoint, token string) (resolvedRuntime, error) {
	endpoint = strings.TrimSpace(endpoint)
	token = strings.TrimSpace(token)
	if endpoint == "" {
		return resolvedRuntime{}, errors.New("LocalMind endpoint is not configured")
	}
	if token == "" {
		return resolvedRuntime{}, errors.New("LocalMind bearer token is not configured")
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
