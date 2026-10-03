package gateway

import (
	"errors"
	"net/http"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3browser"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3execution"
)

func (s *Server) r3HostBroker() (*r3browser.Broker, error) {
	service, err := s.r3ExecutionService()
	if err != nil {
		return nil, err
	}
	s.r3Mu.Lock()
	defer s.r3Mu.Unlock()
	if s.r3Broker != nil {
		return s.r3Broker, nil
	}
	broker, err := r3browser.NewBroker(filepath.Join(service.Root(), "browser-control"))
	if err != nil {
		return nil, r3execution.ErrUnavailable
	}
	s.r3Broker = broker
	ctx := s.executionContext()
	go func() { <-ctx.Done(); _ = broker.Close() }()
	return broker, nil
}
func hostIdentity(p requestPrincipal, r *http.Request) r3browser.Identity {
	return r3browser.Identity{OwnerID: p.OwnerID, ClientID: p.ClientID, InstallationID: r.Header.Get("X-SparkClaw-Installation")}
}
func (s *Server) registerR3HostRoutes() {
	s.mux.HandleFunc("POST /api/r3/hosts/grants", s.r3HostGrant)
	s.mux.HandleFunc("GET /api/r3/hosts/connect", s.r3HostConnect)
	s.mux.HandleFunc("GET /api/r3/hosts/fences", s.r3HostFences)
	s.mux.HandleFunc("POST /api/r3/hosts/reconcile", s.r3HostReconcile)
	s.mux.HandleFunc("POST /api/r3/hosts/{host}/revoke", s.r3HostRevoke)
}
func (s *Server) r3HostGrant(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	broker, err := s.r3HostBroker()
	if err != nil {
		r3Error(w, err)
		return
	}
	var input struct{}
	if err = readR3JSON(r, &input); err != nil {
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
func (s *Server) r3HostConnect(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	broker, err := s.r3HostBroker()
	if err != nil {
		r3Error(w, err)
		return
	}
	broker.ServeHost(r.Context(), w, r, hostIdentity(p, r))
}
func (s *Server) r3HostFences(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	broker, err := s.r3HostBroker()
	if err != nil {
		r3Error(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"fences": broker.Fences(hostIdentity(p, r))})
}
func (s *Server) r3HostReconcile(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	broker, err := s.r3HostBroker()
	if err != nil {
		r3Error(w, err)
		return
	}
	var input struct {
		CommandID string `json:"command_id"`
		Digest    string `json:"digest"`
		Outcome   string `json:"outcome"`
	}
	if err = readR3JSON(r, &input); err != nil {
		writeError(w, 400, errors.New("invalid browser reconciliation"))
		return
	}
	if err = broker.Reconcile(hostIdentity(p, r), input.CommandID, input.Digest, input.Outcome); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"recorded": true})
}
func (s *Server) r3HostRevoke(w http.ResponseWriter, r *http.Request) {
	if !emptyR3Request(w, r) {
		return
	}
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	broker, err := s.r3HostBroker()
	if err != nil {
		r3Error(w, err)
		return
	}
	if err = broker.RevokeGrant(hostIdentity(p, r), r.PathValue("host")); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"revoked": true})
}
