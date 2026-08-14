package gateway

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Server) issueMCPAccessTicket(w http.ResponseWriter, r *http.Request) {
	if s.mcpAccess == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP access service is unavailable"))
		return
	}
	var input mcpaccess.IssueTicketRequest
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	configuredDomain := s.mcpAccessDomainID()
	if strings.TrimSpace(input.DomainID) == "" {
		input.DomainID = configuredDomain
	}
	if input.DomainID != configuredDomain {
		writeError(w, http.StatusBadRequest, errors.New("MCP access ticket domain must match the configured MCP Domain"))
		return
	}
	principal := principalForRequest(r)
	issued, err := s.mcpAccess.IssueTicket(principal.OwnerID, principal.ActorID, input, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, issued)
}

func (s *Server) listMCPAccessCatalog(w http.ResponseWriter, r *http.Request) {
	if s.mcpAccess == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP access service is unavailable"))
		return
	}
	transport := s.mcpTransportStatus(principalForRequest(r).OwnerID)
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":              app.MCPAccessConversation,
		"business_tool":      "sparkclaw.conversation.send",
		"iscp_enabled":       transport.ISCPEnabled,
		"lan_access_enabled": transport.LANAccessEnabled,
		"transport_version":  transport.Version,
		"domain_id":          s.mcpAccessDomainID(),
		"endpoint_path":      "/mcp",
	})
}

func (s *Server) mcpAccessDomainID() string {
	if domainID := strings.TrimSpace(s.cfg.ISCPPairing.DomainID); domainID != "" {
		return domainID
	}
	return s.cfg.MCPAccess.LocalDomainID
}

func (s *Server) mcpTransportStatus(ownerID string) app.ConnectorStatus {
	if s.connectors == nil {
		return app.ConnectorStatus{}
	}
	status, err := s.connectors.Status(ownerID, "mcp")
	if err != nil {
		return app.ConnectorStatus{}
	}
	return status
}

func (s *Server) updateMCPTransports(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP connector control is unavailable"))
		return
	}
	var input struct {
		ISCPEnabled      *bool  `json:"iscp_enabled"`
		LANAccessEnabled *bool  `json:"lan_access_enabled"`
		ExpectedVersion  *int64 `json:"expected_version"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := readJSON(r, &input); err != nil || input.ISCPEnabled == nil || input.LANAccessEnabled == nil || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 {
		writeError(w, http.StatusBadRequest, errors.New("iscp_enabled, lan_access_enabled, and a non-negative expected_version are required"))
		return
	}
	principal := principalForRequest(r)
	status, err := s.connectors.SetMCPTransports(r.Context(), principal.OwnerID, principal.ActorID, *input.ISCPEnabled, *input.LANAccessEnabled, *input.ExpectedVersion)
	if err != nil {
		httpStatus := http.StatusBadRequest
		if errors.Is(err, store.ErrConnectorSettingConflict) {
			httpStatus = http.StatusConflict
		}
		writeError(w, httpStatus, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) listMCPAccessTickets(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	tickets := s.store.ListMCPAccessTickets(principal.OwnerID)
	for index := range tickets {
		tickets[index].SecretHash = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

func (s *Server) revokeMCPAccessTicket(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	ticket, ok := s.store.GetMCPAccessTicket(r.PathValue("id"))
	if !ok || ticket.OwnerID != principal.OwnerID {
		writeError(w, http.StatusNotFound, errors.New("MCP access ticket not found"))
		return
	}
	ticket, err := s.store.RevokeMCPAccessTicket(ticket.ID, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	s.store.AddAudit(app.AuditEvent{Actor: principal.ActorID, Type: "mcp.access_ticket.revoked", Summary: "Revoked a pending MCP access ticket", Fields: map[string]any{
		"ticket_id": ticket.ID, "domain_id": ticket.DomainID,
	}})
	ticket.SecretHash = ""
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Server) deleteMCPAccessTicket(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	ticket, err := s.store.DeleteMCPAccessTicket(principal.OwnerID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("MCP access ticket not found"))
		return
	}
	s.store.AddAudit(app.AuditEvent{Actor: principal.ActorID, Type: "mcp.access_ticket.deleted", Summary: "Deleted an MCP access ticket record", Fields: map[string]any{
		"ticket_id": ticket.ID, "domain_id": ticket.DomainID, "status": ticket.Status,
	}})
	ticket.SecretHash = ""
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Server) listMCPBindings(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	writeJSON(w, http.StatusOK, map[string]any{"bindings": s.store.ListMCPBindings(principal.OwnerID)})
}

func (s *Server) revokeMCPBinding(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	binding, ok := s.store.GetMCPBinding(r.PathValue("id"))
	if !ok || binding.OwnerID != principal.OwnerID {
		writeError(w, http.StatusNotFound, errors.New("MCP binding not found"))
		return
	}
	if s.mcpAccess == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP access service is unavailable"))
		return
	}
	binding, err := s.mcpAccess.RevokeBinding(binding.ID, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	s.store.AddAudit(app.AuditEvent{SessionID: binding.LinkedSessionID, Actor: principal.ActorID, Type: "mcp.binding.revoked", Summary: "Revoked an MCP binding", Fields: map[string]any{
		"binding_id": binding.ID, "domain_id": binding.DomainID, "requester_device_id": binding.RequesterDeviceID,
		"binding_revision": binding.AuthorizationRevision,
	}})
	writeJSON(w, http.StatusOK, binding)
}

func (s *Server) deleteMCPBinding(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	if s.mcpAccess == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP access service is unavailable"))
		return
	}
	binding, err := s.mcpAccess.DeleteBinding(principal.OwnerID, r.PathValue("id"), time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("MCP binding not found"))
		return
	}
	s.store.AddAudit(app.AuditEvent{SessionID: binding.LinkedSessionID, Actor: principal.ActorID, Type: "mcp.binding.deleted", Summary: "Deleted an MCP binding record", Fields: map[string]any{
		"binding_id": binding.ID, "domain_id": binding.DomainID, "requester_device_id": binding.RequesterDeviceID,
		"status": binding.Status, "binding_revision": binding.AuthorizationRevision,
	}})
	writeJSON(w, http.StatusOK, binding)
}

func (s *Server) deleteMCPAccessRecords(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	if s.mcpAccess == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP access service is unavailable"))
		return
	}
	deleted, err := s.mcpAccess.DeleteAccessRecords(principal.OwnerID, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if deleted.DeletedTickets > 0 || deleted.DeletedBindings > 0 {
		s.store.AddAudit(app.AuditEvent{Actor: principal.ActorID, Type: "mcp.access_records.deleted", Summary: "Deleted all MCP access records", Fields: map[string]any{
			"deleted_tickets": deleted.DeletedTickets, "deleted_bindings": deleted.DeletedBindings,
		}})
	}
	writeJSON(w, http.StatusOK, deleted)
}

func (s *Server) dispatchMCPBridgeRequest(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		writeError(w, http.StatusForbidden, errors.New("MCP bridge API is restricted to loopback clients"))
		return
	}
	if s.mcpAccess == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP access service is unavailable"))
		return
	}
	principal := principalForRequest(r)
	if !s.mcpTransportStatus(principal.OwnerID).ISCPEnabled {
		writeError(w, http.StatusForbidden, errors.New("MCP over ISCP is disabled"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mcpaccess.MaxRequestBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request mcpaccess.PeerRequest
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid MCP bridge request"))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, errors.New("invalid MCP bridge request"))
		return
	}
	response, err := s.mcpAccess.Dispatch(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

const (
	mcpSessionHeader  = "Mcp-Session-Id"
	mcpProtocolHeader = "MCP-Protocol-Version"
)

func (s *Server) dispatchLANDirectMCP(w http.ResponseWriter, r *http.Request) {
	if !s.mcpTransportStatus(app.DefaultOwnerID).LANAccessEnabled {
		http.NotFound(w, r)
		return
	}
	if s.mcpAccess == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP access service is unavailable"))
		return
	}
	if protocol := strings.TrimSpace(r.Header.Get(mcpProtocolHeader)); protocol != "" && protocol != mcpaccess.MCPProtocolVersion {
		writeError(w, http.StatusBadRequest, errors.New("unsupported MCP protocol version"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mcpaccess.MaxRequestBytes)
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("invalid MCP request"))
		return
	}
	var rpc mcpaccess.JSONRPCRequest
	if json.Unmarshal(raw, &rpc) != nil || rpc.JSONRPC != mcpaccess.JSONRPCVersion || strings.TrimSpace(rpc.Method) == "" {
		writeMCPJSONRPCError(w, http.StatusBadRequest, rpc.ID, -32600, "invalid JSON-RPC request")
		return
	}

	sessionSecret := strings.TrimSpace(r.Header.Get(mcpSessionHeader))
	if sessionSecret == "" {
		s.startLANDirectMCPSession(w, r, raw, rpc)
		return
	}
	peer := lanDirectPeer(s.mcpAccessDomainID(), sessionSecret)
	if _, ok := s.mcpAccess.ActiveBindingForPeer(peer); !ok {
		writeMCPJSONRPCError(w, http.StatusUnauthorized, rpc.ID, -32003, "active MCP session is required")
		return
	}
	s.dispatchLANDirectJSONRPC(w, r, raw, rpc, peer)
}

func (s *Server) startLANDirectMCPSession(w http.ResponseWriter, r *http.Request, raw []byte, rpc mcpaccess.JSONRPCRequest) {
	if rpc.Method != "initialize" {
		writeMCPJSONRPCError(w, http.StatusUnauthorized, rpc.ID, -32002, "MCP access ticket is required to initialize a session")
		return
	}
	ticket := bearerCredential(r.Header.Get("Authorization"))
	if ticket == "" {
		writeMCPJSONRPCError(w, http.StatusUnauthorized, rpc.ID, -32002, "MCP access ticket is required to initialize a session")
		return
	}
	sessionSecret, err := randomMCPDirectSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("MCP session could not be created"))
		return
	}
	peer := lanDirectPeer(s.mcpAccessDomainID(), sessionSecret)
	response, err := s.mcpAccess.Dispatch(r.Context(), directPeerRequest(raw, rpc, peer))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var initialized mcpaccess.JSONRPCResponse
	if json.Unmarshal(response.JSONRPC, &initialized) != nil || initialized.Error != nil {
		writeMCPDirectResponse(w, http.StatusOK, response.JSONRPC)
		return
	}
	if _, err := s.mcpAccess.RedeemAccessTicket(ticket, peer); err != nil {
		writeMCPJSONRPCError(w, http.StatusUnauthorized, rpc.ID, -32002, "MCP access ticket is invalid or unavailable")
		return
	}
	w.Header().Set(mcpSessionHeader, sessionSecret)
	w.Header().Set(mcpProtocolHeader, mcpaccess.MCPProtocolVersion)
	w.Header().Set("Cache-Control", "no-store")
	writeMCPDirectResponse(w, http.StatusOK, response.JSONRPC)
}

func (s *Server) dispatchLANDirectJSONRPC(w http.ResponseWriter, r *http.Request, raw []byte, rpc mcpaccess.JSONRPCRequest, peer app.MCPPeerIdentity) {
	request := directPeerRequest(raw, rpc, peer)
	request.Request.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if request.Request.IdempotencyKey == "" && len(rpc.ID) > 0 {
		digest := sha256.Sum256(append([]byte(peer.ISCPSessionID+"\x00"), rpc.ID...))
		request.Request.IdempotencyKey = "mcp-http-" + base64.RawURLEncoding.EncodeToString(digest[:])
	}
	response, err := s.mcpAccess.Dispatch(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set(mcpProtocolHeader, mcpaccess.MCPProtocolVersion)
	if len(response.JSONRPC) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeMCPDirectResponse(w, http.StatusOK, response.JSONRPC)
}

func directPeerRequest(raw []byte, _ mcpaccess.JSONRPCRequest, peer app.MCPPeerIdentity) mcpaccess.PeerRequest {
	return mcpaccess.PeerRequest{Peer: peer, Request: mcpaccess.TransportRequest{
		ProtocolVersion: mcpaccess.TransportProtocolVersion,
		Type:            mcpaccess.TransportTypeRequest,
		SessionID:       peer.ISCPSessionID,
		JSONRPC:         append(json.RawMessage(nil), raw...),
	}}
}

func lanDirectPeer(domainID, sessionSecret string) app.MCPPeerIdentity {
	digest := sha256.Sum256([]byte(sessionSecret))
	encoded := base64.RawURLEncoding.EncodeToString(digest[:])
	return app.MCPPeerIdentity{
		DomainID:      domainID,
		DeviceID:      "lan-" + encoded[:20],
		KeyThumbprint: encoded,
		ISCPSessionID: "lan-" + encoded,
	}
}

func randomMCPDirectSession() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func bearerCredential(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
}

func writeMCPJSONRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	raw, _ := json.Marshal(mcpaccess.JSONRPCResponse{JSONRPC: mcpaccess.JSONRPCVersion, ID: id, Error: &mcpaccess.JSONRPCError{Code: code, Message: message}})
	writeMCPDirectResponse(w, status, raw)
}

func writeMCPDirectResponse(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
