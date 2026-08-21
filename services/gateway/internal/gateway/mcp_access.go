package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
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
	issued, err := s.mcpAccess.IssueTicket(r.Context(), principal.OwnerID, principal.ActorID, input, time.Now().UTC())
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
	transport, err := s.mcpTransportStatus(r.Context(), principalForRequest(r).OwnerID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
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

func (s *Server) mcpTransportStatus(ctx context.Context, ownerID string) (app.ConnectorStatus, error) {
	if s.connectors == nil {
		return app.ConnectorStatus{}, errors.New("connector control is unavailable")
	}
	return s.connectors.Status(ctx, ownerID, "mcp")
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
	tickets, err := s.store.ListMCPAccessTickets(r.Context(), principal.OwnerID)
	if err != nil {
		writeMCPStoreError(w, err)
		return
	}
	for index := range tickets {
		tickets[index].SecretHash = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

func (s *Server) revokeMCPAccessTicket(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	ticket, ok, err := s.store.GetMCPAccessTicket(r.Context(), r.PathValue("id"))
	if err != nil {
		writeMCPStoreError(w, err)
		return
	}
	if !ok || ticket.OwnerID != principal.OwnerID {
		writeError(w, http.StatusNotFound, errors.New("MCP access ticket not found"))
		return
	}
	revokedAt := time.Now().UTC()
	ticket, err = s.store.RevokeMCPAccessTicket(r.Context(), ticket.ID, revokedAt)
	ticket, err = store.ReconcileMCPAccessTicketRevoke(r.Context(), s.store, ticket, err)
	if err != nil {
		writeMCPStoreError(w, err)
		return
	}
	s.addAudit(r.Context(), app.AuditEvent{Actor: principal.ActorID, Type: "mcp.access_ticket.revoked", Summary: "Revoked a pending MCP access ticket", Fields: map[string]any{
		"ticket_id": ticket.ID, "domain_id": ticket.DomainID,
	}})

	ticket.SecretHash = ""
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Server) deleteMCPAccessTicket(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	ticket, err := s.store.DeleteMCPAccessTicket(r.Context(), principal.OwnerID, r.PathValue("id"))
	ticket, err = store.ReconcileMCPAccessTicketDelete(r.Context(), s.store, ticket, err)
	if err != nil {
		writeMCPStoreError(w, err)
		return
	}
	s.addAudit(r.Context(), app.AuditEvent{Actor: principal.ActorID, Type: "mcp.access_ticket.deleted", Summary: "Deleted an MCP access ticket record", Fields: map[string]any{
		"ticket_id": ticket.ID, "domain_id": ticket.DomainID, "status": ticket.Status,
	}})

	ticket.SecretHash = ""
	writeJSON(w, http.StatusOK, ticket)
}

func (s *Server) listMCPBindings(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	bindings, err := s.store.ListMCPBindings(r.Context(), principal.OwnerID)
	if err != nil {
		writeMCPStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": bindings})
}

func (s *Server) revokeMCPBinding(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	binding, ok, err := s.store.GetMCPBinding(r.Context(), r.PathValue("id"))
	if err != nil {
		writeMCPStoreError(w, err)
		return
	}
	if !ok || binding.OwnerID != principal.OwnerID {
		writeError(w, http.StatusNotFound, errors.New("MCP binding not found"))
		return
	}
	if s.mcpAccess == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP access service is unavailable"))
		return
	}
	binding, err = s.mcpAccess.RevokeBinding(r.Context(), binding.ID, time.Now().UTC())
	if err != nil {
		writeMCPStoreError(w, err)
		return
	}
	s.addAudit(r.Context(), app.AuditEvent{SessionID: binding.LinkedSessionID, Actor: principal.ActorID, Type: "mcp.binding.revoked", Summary: "Revoked an MCP binding", Fields: map[string]any{
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
	binding, err := s.mcpAccess.DeleteBinding(r.Context(), principal.OwnerID, r.PathValue("id"), time.Now().UTC())
	if err != nil {
		writeMCPStoreError(w, err)
		return
	}
	s.addAudit(r.Context(), app.AuditEvent{SessionID: binding.LinkedSessionID, Actor: principal.ActorID, Type: "mcp.binding.deleted", Summary: "Deleted an MCP binding record", Fields: map[string]any{
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
	deleted, err := s.mcpAccess.DeleteAccessRecords(r.Context(), principal.OwnerID, time.Now().UTC())
	if err != nil {
		writeMCPStoreError(w, err)
		return
	}
	if deleted.DeletedTickets > 0 || deleted.DeletedBindings > 0 {
		s.addAudit(r.Context(), app.AuditEvent{Actor: principal.ActorID, Type: "mcp.access_records.deleted", Summary: "Deleted all MCP access records", Fields: map[string]any{
			"deleted_tickets": deleted.DeletedTickets, "deleted_bindings": deleted.DeletedBindings,
		}})

	}
	writeJSON(w, http.StatusOK, deleted)
}

func writeMCPStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrMCPBindingUnavailable) {
		writeError(w, http.StatusNotFound, errors.New("MCP record was not found"))
		return
	}
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("MCP request is invalid"))
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("MCP record was not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("MCP record conflicts with existing state"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, errors.New("MCP request was canceled"))
	case store.StoreErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, errors.New("MCP operation timed out"))
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP state is temporarily unavailable"))
	}
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
	transport, err := s.mcpTransportStatus(r.Context(), principal.OwnerID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	if !transport.ISCPEnabled {
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

// mcpOriginAllowed reports whether a browser Origin may reach /mcp. Allowed
// are loopback origins (a page served from this machine), the gateway's own
// bind-address origin, and the operator allowlist in
// mcp_access.allowed_origins. Everything else — including DNS-rebinding
// hostnames that resolve to this machine and the opaque "null" origin — is
// rejected.
func (s *Server) mcpOriginAllowed(rawOrigin string) bool {
	origin, err := config.NormalizeOrigin(rawOrigin)
	if err != nil {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	if s.matchesBindOrigin(parsed) {
		return true
	}
	for _, allowed := range s.cfg.MCPAccess.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

// matchesBindOrigin reports whether the origin points at the gateway's own
// concrete bind address and port. Wildcard binds cannot name a browser origin
// and derive nothing.
func (s *Server) matchesBindOrigin(origin *url.URL) bool {
	bind := strings.ToLower(strings.Trim(strings.TrimSpace(s.cfg.Gateway.Bind), "[]"))
	if bind == "" || bind == "0.0.0.0" || bind == "::" {
		return false
	}
	if origin.Hostname() != bind {
		return false
	}
	port := origin.Port()
	if port == "" {
		if origin.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return port == strconv.Itoa(s.cfg.Gateway.Port)
}

func (s *Server) dispatchLANDirectMCP(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		http.NotFound(w, r)
		return
	}
	transport, err := s.mcpTransportStatus(r.Context(), app.DefaultOwnerID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	if !transport.LANAccessEnabled {
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
	if _, ok := s.mcpAccess.ActiveBindingForPeer(r.Context(), peer); !ok {
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
	if _, err := s.mcpAccess.RedeemAccessTicket(r.Context(), ticket, peer); err != nil {
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
