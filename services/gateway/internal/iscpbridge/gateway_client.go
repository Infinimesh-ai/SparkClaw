package iscpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
)

const (
	defaultGatewayURL     = "http://127.0.0.1:18789"
	defaultGatewayTimeout = 30 * time.Second
	maxGatewayResponse    = 4 << 20
)

type GatewayClientOptions struct {
	BaseURL    string
	UnixSocket string
	Token      string
	Timeout    time.Duration
}

func (c *GatewayClient) DispatchMCP(ctx context.Context, request mcpaccess.PeerRequest) (mcpaccess.TransportResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return mcpaccess.TransportResponse{}, fmt.Errorf("encode Gateway MCP request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/bridge/v1/mcp/dispatch", bytes.NewReader(body))
	if err != nil {
		return mcpaccess.TransportResponse{}, fmt.Errorf("create Gateway MCP request: %w", err)
	}
	if c.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.token)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(httpRequest)
	if err != nil {
		return mcpaccess.TransportResponse{}, fmt.Errorf("call Gateway MCP service: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxGatewayResponse+1))
	if err != nil {
		return mcpaccess.TransportResponse{}, fmt.Errorf("read Gateway MCP response: %w", err)
	}
	if len(raw) > maxGatewayResponse {
		return mcpaccess.TransportResponse{}, errors.New("Gateway MCP response is too large")
	}
	var out mcpaccess.TransportResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode Gateway MCP response status %d", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return out, fmt.Errorf("Gateway MCP service returned status %d", response.StatusCode)
	}
	if out.ProtocolVersion != mcpaccess.TransportProtocolVersion || out.Type != mcpaccess.TransportTypeResponse {
		return out, errors.New("Gateway returned an invalid MCP bridge response")
	}
	return out, nil
}

type GatewayClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewGatewayClient(options GatewayClientOptions) (*GatewayClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultGatewayURL
	}
	token := strings.TrimSpace(options.Token)
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultGatewayTimeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if socket := strings.TrimSpace(options.UnixSocket); socket != "" {
		baseURL = "http://unix"
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		}
	} else if err := validateLoopbackURL(baseURL); err != nil {
		return nil, err
	}
	return &GatewayClient{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Transport: transport, Timeout: timeout},
	}, nil
}

func (c *GatewayClient) Dispatch(ctx context.Context, request Request) (Response, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return Response{}, fmt.Errorf("encode Gateway request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/bridge/v1/dispatch", bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("create Gateway request: %w", err)
	}
	if c.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.token)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("call Gateway: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxGatewayResponse+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return Response{}, fmt.Errorf("read Gateway response: %w", err)
	}
	if len(raw) > maxGatewayResponse {
		return Response{}, errors.New("Gateway response is too large")
	}
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("decode Gateway response status %d", response.StatusCode)
	}
	if out.ProtocolVersion != ProtocolVersion || out.Type != TypeResponse {
		return Response{}, errors.New("Gateway returned an invalid bridge response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if out.Error != nil {
			return out, out.Error
		}
		return out, fmt.Errorf("Gateway returned status %d", response.StatusCode)
	}
	return out, nil
}

func validateLoopbackURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse Gateway URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("Gateway URL must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("Gateway URL must not contain credentials, query, fragment, or path")
	}
	host := strings.TrimSpace(parsed.Hostname())
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("Gateway URL must resolve directly to a loopback address")
	}
	return nil
}
