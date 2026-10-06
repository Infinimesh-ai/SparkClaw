package gateway

import (
	"errors"
	"net/http"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

func (s *Server) browserHostBroker() (*browserhost.Broker, error) {
	service, err := s.executionService()
	if err != nil {
		return nil, err
	}
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	if s.browserBroker != nil {
		return s.browserBroker, nil
	}
	broker, err := browserhost.NewBroker(filepath.Join(service.Root(), "browser-control"))
	if err != nil {
		return nil, execution.ErrUnavailable
	}
	s.browserBroker = broker
	ctx := s.executionContext()
	go func() { <-ctx.Done(); _ = broker.Close() }()
	return broker, nil
}
func hostIdentity(p requestPrincipal, r *http.Request) browserhost.Identity {
	return browserhost.Identity{OwnerID: p.OwnerID, ClientID: p.ClientID, InstallationID: r.Header.Get("X-SparkClaw-Installation")}
}
func (s *Server) registerBrowserHostRoutes() {
	s.mux.HandleFunc("POST /api/r3/hosts/grants", s.browserHostGrant)
	s.mux.HandleFunc("GET /api/r3/hosts/connect", s.browserHostConnect)
	s.mux.HandleFunc("GET /api/r3/hosts/fences", s.browserHostFences)
	s.mux.HandleFunc("POST /api/r3/hosts/reconcile", s.browserHostReconcile)
	s.mux.HandleFunc("POST /api/r3/hosts/{host}/revoke", s.browserHostRevoke)
}
func (s *Server) browserHostGrant(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	broker, err := s.browserHostBroker()
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	var input struct{}
	if err = readExecutionJSON(r, &input); err != nil {
		writeError(w, 400, errors.New("invalid browser grant request"))
		return
	}
	grant, err := broker.IssueGrant(hostIdentity(p, r))
	if err != nil {
		writeError(w, 409, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, grant)
}
func (s *Server) browserHostConnect(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	broker, err := s.browserHostBroker()
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	broker.ServeHost(r.Context(), w, r, hostIdentity(p, r))
}
func (s *Server) browserHostFences(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	broker, err := s.browserHostBroker()
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"fences": broker.Fences(hostIdentity(p, r))})
}
func (s *Server) browserHostReconcile(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	broker, err := s.browserHostBroker()
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	var input struct {
		CommandID string `json:"command_id"`
		Digest    string `json:"digest"`
		Outcome   string `json:"outcome"`
	}
	if err = readExecutionJSON(r, &input); err != nil {
		writeError(w, 400, errors.New("invalid browser reconciliation"))
		return
	}
	if err = broker.Reconcile(hostIdentity(p, r), input.CommandID, input.Digest, input.Outcome); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"recorded": true})
}
func (s *Server) browserHostRevoke(w http.ResponseWriter, r *http.Request) {
	if !emptyExecutionRequest(w, r) {
		return
	}
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	broker, err := s.browserHostBroker()
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	if err = broker.RevokeGrant(hostIdentity(p, r), r.PathValue("host")); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"revoked": true})
}
