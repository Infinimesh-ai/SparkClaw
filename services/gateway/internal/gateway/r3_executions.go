package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3execution"
)

func (s *Server) r3Principal(r *http.Request) (requestPrincipal, error) {
	p := principalForRequest(r)
	if !p.Authenticated || p.ClientID == "" {
		return p, errors.New("R3 requires an issued device credential")
	}
	service, err := s.r3ExecutionService()
	if err != nil {
		return p, err
	}
	if r.URL.Path != "/api/r3/installations" {
		err = service.Installation(p.OwnerID, p.ClientID, r.Header.Get("X-SparkClaw-Installation"))
	}
	return p, err
}
func (s *Server) r3ExecutionService() (*r3execution.Service, error) {
	s.r3Mu.Lock()
	defer s.r3Mu.Unlock()
	if s.r3Executions != nil {
		return s.r3Executions, nil
	}
	root := s.r3Root
	if root == "" {
		root = s.cfg.State.Path + ".r3"
	}
	if strings.TrimSpace(root) == ".r3" {
		return nil, r3execution.ErrUnavailable
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	executor := s.r3Executor
	if executor == nil {
		executor = s.executeR3Workflow
	}
	service, err := r3execution.New(absolute, executor)
	if err != nil {
		return nil, r3execution.ErrUnavailable
	}
	if err = r3MemorySweep(absolute); err != nil {
		service.Close()
		return nil, r3execution.ErrUnavailable
	}
	service.Start(s.executionContext())
	s.r3Executions = service
	return service, nil
}
func WithR3Executions(root string, execute r3execution.Executor) Option {
	return func(s *Server) { s.r3Root = root; s.r3Executor = execute }
}
func (s *Server) registerR3ExecutionRoutes() {
	s.mux.HandleFunc("POST /api/r3/installations", s.r3Install)
	s.mux.HandleFunc("PUT /api/r3/inputs/{request}/files/{file}", s.r3Upload)
	s.mux.HandleFunc("POST /api/r3/executions", s.r3Submit)
	s.mux.HandleFunc("GET /api/r3/executions/{request}", s.r3Lookup)
	s.mux.HandleFunc("POST /api/r3/executions/{request}/ack", s.r3Ack)
	s.mux.HandleFunc("POST /api/r3/executions/{request}/cancel", s.r3Cancel)
	s.mux.HandleFunc("GET /api/r3/executions/{request}/files/{file}", s.r3File)
}
func r3Error(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, r3execution.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, r3execution.ErrCapacity):
		status = http.StatusTooManyRequests
	case errors.Is(err, r3execution.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, r3execution.ErrExpired):
		status = http.StatusGone
	case errors.Is(err, r3execution.ErrUnavailable):
		status = http.StatusServiceUnavailable
	}
	writeError(w, status, err)
}
func (s *Server) r3Install(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var input struct {
		SchemaVersion  int    `json:"schema_version"`
		InstallationID string `json:"installation_id"`
	}
	if err = readJSON(r, &input); err != nil || input.SchemaVersion != 1 {
		writeError(w, 400, errors.New("invalid R3 installation"))
		return
	}
	if err = s.r3Executions.Bind(p.OwnerID, p.ClientID, input.InstallationID); err != nil {
		r3Error(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"schema_version": 1, "installation_id": input.InstallationID, "owner_id": p.OwnerID, "client_id": p.ClientID, "deployment_id": s.cfg.Gateway.DeploymentID})
}
func (s *Server) r3Upload(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, r3execution.ResultBytes))
	if err != nil {
		writeError(w, 413, errors.New("R3 file is oversized"))
		return
	}
	err = s.r3Executions.Upload(p.OwnerID, p.ClientID, r.Header.Get("X-SparkClaw-Installation"), r.PathValue("request"), r.PathValue("file"), r.Header.Get("X-R3-Digest"), raw)
	if err != nil {
		r3Error(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"stored": true})
}
func (s *Server) r3Submit(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, r3execution.ContextBytes))
	if err != nil {
		writeError(w, 413, errors.New("R3 context is oversized"))
		return
	}
	e, err := r3execution.Decode(raw, r.Header.Get("X-R3-Digest"))
	if err != nil {
		r3Error(w, err)
		return
	}
	if e.DeploymentID != s.cfg.Gateway.DeploymentID || e.OwnerID != p.OwnerID || e.ClientID != p.ClientID || e.InstallationID != r.Header.Get("X-SparkClaw-Installation") {
		r3Error(w, r3execution.ErrConflict)
		return
	}
	status, err := s.r3Executions.Submit(s.executionContext(), e, r.Header.Get("X-R3-Digest"))
	if err != nil {
		r3Error(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}
func (s *Server) r3Lookup(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	status, err := s.r3Executions.Lookup(p.OwnerID, p.ClientID, r.PathValue("request"))
	if err != nil {
		r3Error(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, status)
}
func (s *Server) r3Ack(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	var input struct {
		Sequence int    `json:"sequence"`
		Digest   string `json:"digest"`
		Durable  bool   `json:"durable"`
	}
	if err = readJSON(r, &input); err != nil {
		writeError(w, 400, errors.New("invalid R3 receipt"))
		return
	}
	if err = s.r3Executions.Ack(p.OwnerID, p.ClientID, r.PathValue("request"), input.Sequence, input.Digest, input.Durable); err != nil {
		r3Error(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"state": "delivered"})
}
func (s *Server) r3Cancel(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	if err = s.r3Executions.Cancel(p.OwnerID, p.ClientID, r.PathValue("request")); err != nil {
		r3Error(w, err)
		return
	}
	status, err := s.r3Executions.Lookup(p.OwnerID, p.ClientID, r.PathValue("request"))
	if err != nil {
		r3Error(w, err)
		return
	}
	writeJSON(w, 200, status)
}
func (s *Server) r3File(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	raw, err := s.r3Executions.File(p.OwnerID, p.ClientID, r.PathValue("request"), r.PathValue("file"))
	if err != nil {
		r3Error(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-SHA256", r3execution.Digest(raw))
	_, _ = w.Write(raw)
}

// Retain compile-time callback context checking in this assembly file.
var _ r3execution.Executor = func(context.Context, r3execution.Envelope, map[string][]byte) (r3execution.Output, error) {
	return r3execution.Output{}, nil
}
