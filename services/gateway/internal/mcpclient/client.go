package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultRequestTimeout = 30 * time.Second
	defaultLongCallGrace  = 10 * time.Second
	defaultMaxBodyBytes   = int64(4 << 20)
	maxDiscoveryPages     = 1000
)

type Config struct {
	Endpoint           string
	BearerToken        string
	Namespace          string
	ExpectedServerName string
	RequestTimeout     time.Duration
	LongCallGrace      time.Duration
	MaxResponseBytes   int64
	ClientInfo         ClientInfo
}

type Client struct {
	config Config
	http   *http.Client
	nextID atomic.Uint64

	mu                sync.RWMutex
	sessionID         string
	negotiatedVersion string
	discovery         Discovery
	hasDiscovery      bool
}

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("MCP HTTP request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("MCP HTTP request failed with status %d: %s", e.StatusCode, e.Body)
}

func (e *HTTPError) Unauthorized() bool { return e.StatusCode == http.StatusUnauthorized }

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("MCP JSON-RPC error %d: %s", e.Code, e.Message)
}

type UnexpectedServerError struct {
	Expected string
	Actual   string
}

func (e *UnexpectedServerError) Error() string {
	return fmt.Sprintf("MCP server name mismatch: expected %q, got %q", e.Expected, e.Actual)
}

func New(config Config, httpClient *http.Client) (*Client, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("MCP endpoint must be an absolute HTTP(S) URL without credentials or fragment")
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = defaultRequestTimeout
	}
	if config.LongCallGrace <= 0 {
		config.LongCallGrace = defaultLongCallGrace
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxBodyBytes
	}
	if strings.TrimSpace(config.ClientInfo.Name) == "" {
		config.ClientInfo.Name = "sparkclaw"
	}
	if strings.TrimSpace(config.ClientInfo.Version) == "" {
		config.ClientInfo.Version = "0.1.0"
	}
	config.Endpoint = endpoint
	config.Namespace = strings.Trim(strings.TrimSpace(config.Namespace), ".")
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{config: config, http: httpClient}, nil
}

func (c *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      c.config.ClientInfo,
	}
	var result InitializeResult
	if err := c.request(ctx, "initialize", params, &result, c.config.RequestTimeout); err != nil {
		return InitializeResult{}, err
	}
	if expected := strings.TrimSpace(c.config.ExpectedServerName); expected != "" && result.ServerInfo.Name != expected {
		return InitializeResult{}, &UnexpectedServerError{Expected: expected, Actual: result.ServerInfo.Name}
	}
	if strings.TrimSpace(result.ProtocolVersion) == "" {
		return InitializeResult{}, errors.New("MCP initialize result omitted protocolVersion")
	}
	c.mu.Lock()
	c.negotiatedVersion = result.ProtocolVersion
	c.mu.Unlock()
	if err := c.notify(ctx, "notifications/initialized", nil, c.config.RequestTimeout); err != nil {
		return InitializeResult{}, err
	}
	return result, nil
}

func (c *Client) ListTools(ctx context.Context, cursor string) (ToolList, error) {
	var result ToolList
	err := c.request(ctx, "tools/list", cursorParams(cursor), &result, c.config.RequestTimeout)
	return result, err
}

func (c *Client) CallTool(ctx context.Context, remoteName string, args map[string]any) (ToolResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	var result ToolResult
	timeout := c.toolCallTimeout(remoteName, args)
	err := c.request(ctx, "tools/call", map[string]any{"name": remoteName, "arguments": args}, &result, timeout)
	return result, err
}

func (c *Client) ListResources(ctx context.Context, cursor string) (ResourceList, error) {
	var result ResourceList
	err := c.request(ctx, "resources/list", cursorParams(cursor), &result, c.config.RequestTimeout)
	return result, err
}

func (c *Client) ListResourceTemplates(ctx context.Context, cursor string) (ResourceTemplateList, error) {
	var result ResourceTemplateList
	err := c.request(ctx, "resources/templates/list", cursorParams(cursor), &result, c.config.RequestTimeout)
	return result, err
}

func (c *Client) ReadResource(ctx context.Context, uri string) (ResourceReadResult, error) {
	var result ResourceReadResult
	err := c.request(ctx, "resources/read", map[string]any{"uri": uri}, &result, c.config.RequestTimeout)
	return result, err
}

func (c *Client) Refresh(ctx context.Context) (Discovery, error) {
	initialized, err := c.Initialize(ctx)
	if err != nil {
		return Discovery{}, err
	}
	discovery := Discovery{Initialize: initialized, RefreshedAt: time.Now().UTC()}
	tools, err := collectPages(func(cursor string) ([]Tool, string, error) {
		page, pageErr := c.ListTools(ctx, cursor)
		return page.Tools, page.NextCursor, pageErr
	})
	if err != nil {
		return Discovery{}, err
	}
	for _, tool := range tools {
		localName, nameErr := NamespacedToolName(c.config.Namespace, tool.Name)
		if nameErr != nil {
			return Discovery{}, nameErr
		}
		discovery.Tools = append(discovery.Tools, DiscoveredTool{LocalName: localName, RemoteName: tool.Name, Tool: tool})
	}
	if _, ok := initialized.Capabilities["resources"]; ok {
		discovery.Resources, err = collectPages(func(cursor string) ([]Resource, string, error) {
			page, pageErr := c.ListResources(ctx, cursor)
			return page.Resources, page.NextCursor, pageErr
		})
		if err != nil {
			return Discovery{}, err
		}
		discovery.ResourceTemplates, err = collectPages(func(cursor string) ([]ResourceTemplate, string, error) {
			page, pageErr := c.ListResourceTemplates(ctx, cursor)
			return page.ResourceTemplates, page.NextCursor, pageErr
		})
		if err != nil {
			return Discovery{}, err
		}
	}
	c.mu.Lock()
	c.discovery = cloneDiscovery(discovery)
	c.hasDiscovery = true
	c.mu.Unlock()
	return cloneDiscovery(discovery), nil
}

func (c *Client) Discovery() (Discovery, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hasDiscovery {
		return Discovery{}, false
	}
	return cloneDiscovery(c.discovery), true
}

func collectPages[T any](fetch func(string) ([]T, string, error)) ([]T, error) {
	items := []T{}
	cursor := ""
	seen := map[string]struct{}{}
	for page := 0; page < maxDiscoveryPages; page++ {
		pageItems, next, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, pageItems...)
		next = strings.TrimSpace(next)
		if next == "" {
			return items, nil
		}
		if _, ok := seen[next]; ok {
			return nil, fmt.Errorf("MCP discovery cursor repeated: %q", next)
		}
		seen[next] = struct{}{}
		cursor = next
	}
	return nil, fmt.Errorf("MCP discovery exceeded %d pages", maxDiscoveryPages)
}

func cursorParams(cursor string) map[string]any {
	if strings.TrimSpace(cursor) == "" {
		return map[string]any{}
	}
	return map[string]any{"cursor": cursor}
}

func (c *Client) toolCallTimeout(remoteName string, args map[string]any) time.Duration {
	if remoteName != "wait_for_idle" {
		return c.config.RequestTimeout
	}
	seconds := 300.0
	switch value := args["timeoutSeconds"].(type) {
	case float64:
		seconds = value
	case int:
		seconds = float64(value)
	case json.Number:
		if parsed, err := value.Float64(); err == nil {
			seconds = parsed
		}
	}
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 3600 {
		seconds = 3600
	}
	timeout := time.Duration(seconds*float64(time.Second)) + c.config.LongCallGrace
	if timeout < c.config.RequestTimeout {
		return c.config.RequestTimeout
	}
	return timeout
}

func (c *Client) request(ctx context.Context, method string, params any, result any, timeout time.Duration) error {
	id := c.nextID.Add(1)
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	raw, status, err := c.post(ctx, payload, timeout)
	if err != nil {
		return err
	}
	if status == http.StatusAccepted && len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("MCP request %s returned 202 without a JSON-RPC response", method)
	}
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode MCP JSON-RPC response: %w", err)
	}
	if response.JSONRPC != "2.0" {
		return fmt.Errorf("MCP response has invalid jsonrpc version %q", response.JSONRPC)
	}
	responseID, validID := parseNumericID(response.ID)
	if !validID || responseID != id {
		return fmt.Errorf("MCP response id does not match request %d", id)
	}
	if response.Error != nil {
		return response.Error
	}
	if len(response.Result) == 0 || bytes.Equal(response.Result, []byte("null")) {
		return fmt.Errorf("MCP response for %s omitted result", method)
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("decode MCP %s result: %w", method, err)
	}
	return nil
}

func (c *Client) notify(ctx context.Context, method string, params any, timeout time.Duration) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		payload["params"] = params
	}
	raw, status, err := c.post(ctx, payload, timeout)
	if err != nil {
		return err
	}
	if status == http.StatusAccepted || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var response struct {
		Error *RPCError `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode MCP notification response: %w", err)
	}
	if response.Error != nil {
		return response.Error
	}
	return nil
}

func (c *Client) post(ctx context.Context, payload any, timeout time.Duration) ([]byte, int, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	requestCtx, cancel := boundedContext(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.config.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", c.protocolVersion())
	if token := strings.TrimSpace(c.config.BearerToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if sessionID := c.currentSessionID(); sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, c.config.MaxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if int64(len(body)) > c.config.MaxResponseBytes {
		return nil, resp.StatusCode, fmt.Errorf("MCP response exceeds %d bytes", c.config.MaxResponseBytes)
	}
	if sessionID := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sessionID != "" {
		c.mu.Lock()
		c.sessionID = sessionID
		c.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, &HTTPError{StatusCode: resp.StatusCode, Body: boundedErrorBody(body)}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, resp.StatusCode, nil
	}
	decoded, err := decodeResponseBody(body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return decoded, resp.StatusCode, nil
}

func boundedContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func (c *Client) protocolVersion() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.negotiatedVersion != "" {
		return c.negotiatedVersion
	}
	return ProtocolVersion
}

func (c *Client) currentSessionID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionID
}

func decodeResponseBody(body []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return trimmed, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, len(body)+1)
	data := []string{}
	flush := func() ([]byte, bool) {
		if len(data) == 0 {
			return nil, false
		}
		joined := strings.TrimSpace(strings.Join(data, "\n"))
		data = data[:0]
		if joined == "" || joined == "[DONE]" {
			return nil, false
		}
		return []byte(joined), true
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if event, ok := flush(); ok {
				return event, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse MCP SSE response: %w", err)
	}
	if event, ok := flush(); ok {
		return event, nil
	}
	return nil, errors.New("MCP response was neither JSON nor an SSE data event")
}

func boundedErrorBody(body []byte) string {
	const limit = 1000
	value := strings.TrimSpace(string(body))
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func parseNumericID(raw json.RawMessage) (uint64, bool) {
	value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	return value, err == nil
}
