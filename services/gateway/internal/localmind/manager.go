package localmind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcptools"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

const (
	DynamicSource                = "mcp.localmind"
	discoveryRemoteName          = "discover_localmind_capabilities"
	resourceListLocalName        = "localmind.resources.list"
	resourceTemplatesLocalName   = "localmind.resources.templates.list"
	resourceReadLocalName        = "localmind.resources.read"
	defaultExternalToolTimeoutMS = 30_000
)

type CapabilitySnapshot struct {
	GrantedCapabilities   []string `json:"granted_capabilities"`
	SupportedCapabilities []any    `json:"supported_capabilities,omitempty"`
	Tools                 []string `json:"tools"`
	Resources             bool     `json:"resources"`
}

type Snapshot struct {
	ServerInfo       mcpclient.ServerInfo `json:"server_info"`
	ProtocolVersion  string               `json:"protocol_version"`
	EndpointID       string               `json:"endpoint_id"`
	Revision         string               `json:"revision"`
	Capabilities     CapabilitySnapshot   `json:"capabilities"`
	VisibleToolNames []string             `json:"visible_tool_names"`
	RefreshedAt      time.Time            `json:"refreshed_at"`
}

type Manager struct {
	cfg  config.MCPServerConfig
	hub  *toolhub.ToolHub
	http *http.Client
	env  func(string) string

	refreshMu sync.Mutex
	mu        sync.RWMutex
	client    *mcpclient.Client
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
	if err := hub.ReplaceDynamicTools(DynamicSource, []toolhub.DynamicToolRegistration{manager.discoveryRegistration(Snapshot{})}); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Refresh(ctx context.Context) (snapshot Snapshot, err error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	defer func() {
		if err != nil {
			m.mu.Lock()
			m.client = nil
			m.snapshot = Snapshot{}
			m.hasState = false
			m.mu.Unlock()
			if clearErr := m.retainDiscoveryOnly(); clearErr != nil {
				err = errors.Join(err, clearErr)
			}
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
	discovery, err := client.Refresh(ctx)
	if err != nil {
		return Snapshot{}, safeError(err, runtime)
	}
	if discovery.Initialize.ProtocolVersion != m.cfg.ProtocolVersion {
		return Snapshot{}, fmt.Errorf("LocalMind negotiated unsupported MCP protocol version %q", discovery.Initialize.ProtocolVersion)
	}
	capabilityResult, err := client.CallTool(ctx, discoveryRemoteName, map[string]any{})
	if err != nil {
		return Snapshot{}, safeError(err, runtime)
	}
	if capabilityResult.IsError {
		return Snapshot{}, errors.New("LocalMind capability discovery returned an MCP tool error")
	}
	capabilities, err := parseCapabilitySnapshot(capabilityResult)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateCapabilityTools(discovery.Tools, capabilities.Tools); err != nil {
		return Snapshot{}, err
	}
	_, resourceCapability := discovery.Initialize.Capabilities["resources"]
	if capabilities.Resources != resourceCapability {
		return Snapshot{}, errors.New("LocalMind initialize and capability snapshot resource availability differ")
	}
	revision := snapshotRevision(runtime.endpointID, discovery, capabilities)
	snapshot = Snapshot{
		ServerInfo: discovery.Initialize.ServerInfo, ProtocolVersion: discovery.Initialize.ProtocolVersion,
		EndpointID: runtime.endpointID, Revision: revision, Capabilities: capabilities,
		RefreshedAt: discovery.RefreshedAt,
	}
	registrations := []toolhub.DynamicToolRegistration{m.discoveryRegistration(snapshot)}
	for _, discovered := range discovery.Tools {
		decision := m.toolDecision(discovered.Tool)
		if discovered.RemoteName == discoveryRemoteName || !decision.Visible {
			continue
		}
		registration := m.toolRegistration(discovered, snapshot, decision.Classification)
		registrations = append(registrations, registration)
		snapshot.VisibleToolNames = append(snapshot.VisibleToolNames, discovered.LocalName)
	}
	if capabilities.Resources {
		registrations = append(registrations, m.resourceRegistrations(snapshot)...)
		snapshot.VisibleToolNames = append(snapshot.VisibleToolNames, resourceListLocalName, resourceTemplatesLocalName, resourceReadLocalName)
	}
	slices.Sort(snapshot.VisibleToolNames)
	registrations[0] = m.discoveryRegistration(snapshot)
	if err := m.hub.ReplaceDynamicTools(DynamicSource, registrations); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.client = client
	m.snapshot = snapshot
	m.hasState = true
	m.mu.Unlock()
	return snapshot, nil
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

func (m *Manager) retainDiscoveryOnly() error {
	return m.hub.ReplaceDynamicTools(DynamicSource, []toolhub.DynamicToolRegistration{m.discoveryRegistration(Snapshot{})})
}

func (m *Manager) toolAllowed(tool mcpclient.Tool) bool {
	return m.toolDecision(tool).Visible
}

func (m *Manager) toolDecision(tool mcpclient.Tool) mcptools.Decision {
	return mcptools.Evaluate(tool, mcptools.Policy{
		AllowMutations: m.cfg.AllowMutations,
		ToolAllow:      m.cfg.ToolAllow,
		ToolDeny:       m.cfg.ToolDeny,
	})
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return snapshot
	}
	var cloned Snapshot
	if json.Unmarshal(raw, &cloned) != nil {
		return snapshot
	}
	return cloned
}
