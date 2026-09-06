package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

func normalizeMCPAccessConfig(access *MCPAccessConfig) error {
	access.LocalDomainID = strings.TrimSpace(access.LocalDomainID)
	if access.LocalDomainID == "" {
		return errors.New("mcp_access.local_domain_id is required")
	}
	normalized := make([]string, 0, len(access.AllowedOrigins))
	seen := make(map[string]bool, len(access.AllowedOrigins))
	for _, entry := range access.AllowedOrigins {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		origin, err := NormalizeOrigin(entry)
		if err != nil {
			return fmt.Errorf("mcp_access.allowed_origins entry %q must be an absolute HTTP(S) origin without credentials, path, query, or fragment", entry)
		}
		if !seen[origin] {
			seen[origin] = true
			normalized = append(normalized, origin)
		}
	}
	access.AllowedOrigins = normalized
	return nil
}

// NormalizeOrigin canonicalizes a web origin ("scheme://host[:port]") to its
// lowercase form. It rejects values that are not plain HTTP(S) origins, such
// as URLs carrying credentials, paths, queries, or fragments, and the opaque
// "null" origin.
func NormalizeOrigin(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse origin: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("origin must be an absolute HTTP(S) origin")
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host), nil
}

func normalizeMCPServers(servers *map[string]MCPServerConfig) error {
	if *servers == nil {
		*servers = map[string]MCPServerConfig{}
		return nil
	}
	for name, server := range *servers {
		if strings.TrimSpace(name) != name || !mcpServerNamePattern.MatchString(name) {
			return fmt.Errorf("MCP server name %q must match %s", name, mcpServerNamePattern.String())
		}
		var err error
		if name == LocalMindMCPServerKey {
			server, err = normalizeLocalMindMCPServer(name, server)
		} else {
			server, err = normalizeGenericMCPServer(name, server)
		}
		if err != nil {
			return err
		}
		(*servers)[name] = server
	}
	return nil
}

func normalizeGenericMCPServer(name string, server MCPServerConfig) (MCPServerConfig, error) {
	if hasLocalMindOnlyMCPSettings(server) {
		return MCPServerConfig{}, fmt.Errorf("unsupported MCP server %q with LocalMind-specific configuration", name)
	}
	server.URL = strings.TrimSpace(server.URL)
	endpoint, err := url.Parse(server.URL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.Fragment != "" {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q URL must be absolute HTTP(S) without credentials or fragment", name)
	}
	server.TokenEnv = strings.TrimSpace(server.TokenEnv)
	server.TokenFile = strings.TrimSpace(server.TokenFile)
	if server.TokenEnv != "" && server.TokenFile != "" {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q must use only one of token_env or token_file", name)
	}
	if server.TokenEnv != "" && !environmentNamePattern.MatchString(server.TokenEnv) {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q token_env is invalid", name)
	}
	server.Namespace = strings.Trim(strings.TrimSpace(server.Namespace), ".")
	if server.Namespace == "" {
		server.Namespace = "mcp." + name
	}
	server.ExpectedServerName = strings.TrimSpace(server.ExpectedServerName)
	server.ToolAllow = normalizeStringSet(server.ToolAllow)
	server.ToolDeny = normalizeStringSet(server.ToolDeny)
	for _, allowed := range server.ToolAllow {
		if slicesContains(server.ToolDeny, allowed) {
			return MCPServerConfig{}, fmt.Errorf("MCP server %q tool %q cannot be both allowed and denied", name, allowed)
		}
	}
	if server.RequestTimeoutSeconds <= 0 {
		server.RequestTimeoutSeconds = 30
	}
	if server.RequestTimeoutSeconds > 3600 {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q request_timeout_seconds must not exceed 3600", name)
	}
	if server.DiscoveryRefreshSeconds <= 0 {
		server.DiscoveryRefreshSeconds = 60
	}
	if server.DiscoveryRefreshSeconds > 86400 {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q discovery_refresh_seconds must not exceed 86400", name)
	}
	if server.ResponseBodyMaxBytes <= 0 {
		server.ResponseBodyMaxBytes = 4 << 20
	}
	if server.ResponseBodyMaxBytes > 32<<20 {
		return MCPServerConfig{}, fmt.Errorf("MCP server %q response_body_max_bytes must not exceed 33554432", name)
	}
	return server, nil
}

func normalizeLocalMindMCPServer(name string, server MCPServerConfig) (MCPServerConfig, error) {
	if hasGenericMCPSettings(server) {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s must use url_env and bearer_token_env instead of generic MCP endpoint settings", name)
	}
	defaults := MCPServerConfig{
		Transport:              "streamable-http",
		Namespace:              LocalMindMCPDefaultNamespace,
		ExpectedServerName:     LocalMindMCPServerName,
		ProtocolVersion:        LocalMindMCPProtocolVersion,
		RequestTimeoutSeconds:  30,
		LongCallGraceSeconds:   10,
		MaxResponseBytes:       LocalMindMCPDefaultMaxResponse,
		StateOutputMaxBytes:    16 << 10,
		ArchiveOutputMaxBytes:  16 << 20,
		RefreshIntervalSeconds: 300,
	}
	server.Transport = strings.ToLower(strings.TrimSpace(server.Transport))
	if server.Transport == "" {
		server.Transport = defaults.Transport
	}
	if server.Transport != defaults.Transport {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.transport must be %q", name, defaults.Transport)
	}
	server.URLEnv = strings.TrimSpace(server.URLEnv)
	server.BearerTokenEnv = strings.TrimSpace(server.BearerTokenEnv)
	if !environmentNamePattern.MatchString(server.URLEnv) || !environmentNamePattern.MatchString(server.BearerTokenEnv) {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s url_env and bearer_token_env must be valid environment variable names", name)
	}
	server.Namespace = strings.Trim(strings.TrimSpace(server.Namespace), ".")
	if server.Namespace == "" {
		server.Namespace = defaults.Namespace
	}
	if server.Namespace != LocalMindMCPDefaultNamespace {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.namespace must be %q", name, LocalMindMCPDefaultNamespace)
	}
	server.ExpectedServerName = strings.TrimSpace(server.ExpectedServerName)
	if server.ExpectedServerName == "" {
		server.ExpectedServerName = defaults.ExpectedServerName
	}
	if server.ExpectedServerName != LocalMindMCPServerName {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.expected_server_name must be %q", name, LocalMindMCPServerName)
	}
	server.ProtocolVersion = strings.TrimSpace(server.ProtocolVersion)
	if server.ProtocolVersion == "" {
		server.ProtocolVersion = defaults.ProtocolVersion
	}
	if server.ProtocolVersion != LocalMindMCPProtocolVersion {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.protocol_version must be %q", name, LocalMindMCPProtocolVersion)
	}
	if len(server.ToolAllow) != 0 || len(server.ToolDeny) != 0 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s tool_allow and tool_deny are not supported by the fixed LocalMind task contract", name)
	}
	if server.AllowMutations {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s allow_mutations is not supported by the fixed LocalMind task contract", name)
	}
	if server.RequestTimeoutSeconds <= 0 {
		server.RequestTimeoutSeconds = defaults.RequestTimeoutSeconds
	}
	if server.RequestTimeoutSeconds > 120 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.request_timeout_seconds must not exceed 120", name)
	}
	if server.LongCallGraceSeconds <= 0 {
		server.LongCallGraceSeconds = defaults.LongCallGraceSeconds
	}
	if server.LongCallGraceSeconds > 120 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.long_call_grace_seconds must not exceed 120", name)
	}
	if server.MaxResponseBytes <= 0 {
		server.MaxResponseBytes = defaults.MaxResponseBytes
	}
	if server.MaxResponseBytes < 1024 || server.MaxResponseBytes > 32<<20 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.max_response_bytes must be between 1024 and 33554432", name)
	}
	if server.StateOutputMaxBytes <= 0 {
		server.StateOutputMaxBytes = defaults.StateOutputMaxBytes
	}
	if server.StateOutputMaxBytes < 1024 || server.StateOutputMaxBytes > 64<<10 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.state_output_max_bytes must be between 1024 and 65536", name)
	}
	if server.ArchiveOutputMaxBytes <= 0 {
		server.ArchiveOutputMaxBytes = defaults.ArchiveOutputMaxBytes
	}
	if server.ArchiveOutputMaxBytes < server.StateOutputMaxBytes || server.ArchiveOutputMaxBytes > 32<<20 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.archive_output_max_bytes must be between state_output_max_bytes and 33554432", name)
	}
	if server.RefreshIntervalSeconds <= 0 {
		server.RefreshIntervalSeconds = defaults.RefreshIntervalSeconds
	}
	if server.RefreshIntervalSeconds < 30 || server.RefreshIntervalSeconds > 86400 {
		return MCPServerConfig{}, fmt.Errorf("mcp_servers.%s.refresh_interval_seconds must be between 30 and 86400", name)
	}
	return server, nil
}

func hasLocalMindOnlyMCPSettings(server MCPServerConfig) bool {
	return server.Transport != "" || server.URLEnv != "" || server.BearerTokenEnv != "" ||
		server.ProtocolVersion != "" || server.AllowPrivateHTTP || server.LongCallGraceSeconds != 0 ||
		server.MaxResponseBytes != 0 || server.StateOutputMaxBytes != 0 ||
		server.ArchiveOutputMaxBytes != 0 || server.RefreshIntervalSeconds != 0
}

func hasGenericMCPSettings(server MCPServerConfig) bool {
	return server.URL != "" || server.TokenEnv != "" || server.TokenFile != "" ||
		server.DiscoveryRefreshSeconds != 0 || server.ResponseBodyMaxBytes != 0
}

func normalizeStringSet(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || slicesContains(out, value) {
			continue
		}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
