package mcpaccess

import (
	"encoding/json"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	MCPProtocolVersion       = "2025-06-18"
	TransportProtocolVersion = "sparkclaw.mcp.iscp.v1"
	TransportTypeRequest     = "mcp.request"
	TransportTypeResponse    = "mcp.response"
	JSONRPCVersion           = "2.0"
	MaxRequestBytes          = 1 << 20
)

type TransportRequest struct {
	ProtocolVersion string          `json:"protocol_version"`
	Type            string          `json:"type"`
	SessionID       string          `json:"session_id,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	Deadline        time.Time       `json:"deadline,omitempty"`
	JSONRPC         json.RawMessage `json:"jsonrpc"`
}

type TransportResponse struct {
	ProtocolVersion string          `json:"protocol_version"`
	Type            string          `json:"type"`
	SessionID       string          `json:"session_id,omitempty"`
	JSONRPC         json.RawMessage `json:"jsonrpc,omitempty"`
}

type PeerRequest struct {
	Peer    app.MCPPeerIdentity `json:"peer"`
	Request TransportRequest    `json:"request"`
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

type RequestedGrant struct {
	CapabilityID  app.CapabilityID     `json:"capability_id"`
	Operations    []app.RouteOperation `json:"operations"`
	AllowApproval bool                 `json:"allow_approval"`
}

type GrantOperationOption struct {
	Operation app.RouteOperation `json:"operation"`
	Effect    app.ToolEffect     `json:"effect"`
}

type GrantOption struct {
	CapabilityID       app.CapabilityID        `json:"capability_id"`
	Description        string                  `json:"description"`
	Operations         []GrantOperationOption  `json:"operations"`
	Workflow           app.WorkflowContractRef `json:"workflow"`
	ProjectionRevision int                     `json:"projection_revision"`
}

type IssueTicketRequest struct {
	DomainID   string           `json:"domain_id"`
	Grants     []RequestedGrant `json:"grants"`
	TTLSeconds int              `json:"ttl_seconds,omitempty"`
}

type IssuedTicket struct {
	Ticket app.MCPAccessTicket `json:"ticket"`
	Secret string              `json:"secret"`
}

type RedeemParams struct {
	Ticket string `json:"ticket"`
}
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      ClientInfo     `json:"clientInfo"`
	Meta            map[string]any `json:"_meta,omitempty"`
}
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Meta      map[string]any `json:"_meta,omitempty"`
}

type CallToolResult struct {
	Content           []CallToolContent `json:"content"`
	StructuredContent any               `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError,omitempty"`
}

type CallToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type OperationParams struct {
	OperationID string `json:"operation_id"`
}

func responseEnvelope(request TransportRequest, response JSONRPCResponse) (TransportResponse, error) {
	raw, err := json.Marshal(response)
	if err != nil {
		return TransportResponse{}, err
	}
	return TransportResponse{ProtocolVersion: TransportProtocolVersion, Type: TransportTypeResponse, SessionID: request.SessionID, JSONRPC: raw}, nil
}
