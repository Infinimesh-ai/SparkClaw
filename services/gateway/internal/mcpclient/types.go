package mcpclient

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const ProtocolVersion = "2025-06-18"

var localToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      ServerInfo     `json:"serverInfo"`
	Instructions    string         `json:"instructions,omitempty"`
}

type Tool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
	Meta         map[string]any `json:"_meta,omitempty"`
}

type ToolList struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type ContentBlock map[string]any

type ToolResult struct {
	Content           []ContentBlock `json:"content"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
	Meta              map[string]any `json:"_meta,omitempty"`
}

type Resource struct {
	URI         string         `json:"uri"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	MimeType    string         `json:"mimeType,omitempty"`
	Size        int64          `json:"size,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

type ResourceList struct {
	Resources  []Resource `json:"resources"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

type ResourceTemplate struct {
	URITemplate string         `json:"uriTemplate"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	MimeType    string         `json:"mimeType,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

type ResourceTemplateList struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	NextCursor        string             `json:"nextCursor,omitempty"`
}

type ResourceReadResult struct {
	Contents []ContentBlock `json:"contents"`
	Meta     map[string]any `json:"_meta,omitempty"`
}

type DiscoveredTool struct {
	LocalName  string `json:"localName"`
	RemoteName string `json:"remoteName"`
	Tool       Tool   `json:"tool"`
}

type Discovery struct {
	Initialize        InitializeResult   `json:"initialize"`
	Tools             []DiscoveredTool   `json:"tools"`
	Resources         []Resource         `json:"resources,omitempty"`
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates,omitempty"`
	RefreshedAt       time.Time          `json:"refreshedAt"`
}

func NamespacedToolName(namespace, remoteName string) (string, error) {
	namespace = strings.Trim(strings.TrimSpace(namespace), ".")
	remoteName = strings.TrimSpace(remoteName)
	if remoteName == "" {
		return "", fmt.Errorf("MCP remote tool name is required")
	}
	name := remoteName
	if namespace != "" {
		name = namespace + "." + remoteName
	}
	if !localToolNamePattern.MatchString(name) {
		return "", fmt.Errorf("MCP local tool name %q must match %s", name, localToolNamePattern.String())
	}
	return name, nil
}

func cloneDiscovery(value Discovery) Discovery {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned Discovery
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return value
	}
	return cloned
}
