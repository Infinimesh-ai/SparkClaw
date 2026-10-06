package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

func (s *Server) executionPrincipal(r *http.Request) (requestPrincipal, error) {
	p := principalForRequest(r)
	if !p.Authenticated || p.ClientID == "" {
		return p, errors.New("workbench requires an issued device credential")
	}
	service, err := s.executionService()
	if err != nil {
		return p, err
	}
	if r.URL.Path != "/api/v1/installations" {
		err = service.Installation(p.OwnerID, p.ClientID, r.Header.Get("X-SparkClaw-Installation"))
	}
	return p, err
}
func (s *Server) executionService() (*execution.Service, error) {
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	if s.executions != nil {
		return s.executions, nil
	}
	root := s.executionRoot
	if root == "" {
		root = s.cfg.State.Path + ".execution"
	}
	if strings.TrimSpace(root) == ".execution" {
		return nil, execution.ErrUnavailable
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	executor := s.executionExecutor
	if executor == nil {
		executor = s.executeWorkbenchWorkflow
	}
	service, err := execution.New(absolute, executor)
	if err != nil {
		return nil, execution.ErrUnavailable
	}
	// Memory-backed workspaces belong to the installed execution adapter.
	// Host workbench admission needs only the common durable control service.
	if err = s.prepareExecutionWorkspaces(absolute); err != nil {
		service.Close()
		return nil, err
	}
	service.Start(s.executionContext())
	s.executions = service
	return service, nil
}
func WithExecutions(root string, execute execution.Executor) Option {
	return func(s *Server) { s.executionRoot = root; s.executionExecutor = execute }
}
func (s *Server) registerExecutionRoutes() {
	s.mux.HandleFunc("POST /api/v1/installations", s.installExecutionClient)
	s.mux.HandleFunc("PUT /api/v1/inputs/{request}/files/{file}", s.uploadExecutionInput)
	s.mux.HandleFunc("POST /api/v1/executions", s.submitExecution)
	s.mux.HandleFunc("GET /api/v1/executions/{request}", s.lookupExecution)
	s.mux.HandleFunc("POST /api/v1/executions/{request}/ack", s.ackExecution)
	s.mux.HandleFunc("POST /api/v1/executions/{request}/approvals/{approval}", s.resolveExecutionApproval)
	s.mux.HandleFunc("POST /api/v1/executions/{request}/cancel", s.cancelExecution)
	s.mux.HandleFunc("GET /api/v1/executions/{request}/files/{file}", s.executionFile)
}
func writeExecutionError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, execution.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, execution.ErrCapacity):
		status = http.StatusTooManyRequests
	case errors.Is(err, execution.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, execution.ErrExpired):
		status = http.StatusGone
	case errors.Is(err, execution.ErrUnavailable):
		status = http.StatusServiceUnavailable
	}
	writeError(w, status, err)
}
func (s *Server) installExecutionClient(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var input struct {
		SchemaVersion  int    `json:"schema_version"`
		InstallationID string `json:"installation_id"`
	}
	if err = readExecutionJSON(r, &input); err != nil || input.SchemaVersion != 1 {
		writeError(w, 400, errors.New("invalid workbench installation"))
		return
	}
	if err = s.executions.Bind(p.OwnerID, p.ClientID, input.InstallationID); err != nil {
		writeExecutionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"schema_version": 1, "installation_id": input.InstallationID, "owner_id": p.OwnerID, "client_id": p.ClientID, "deployment_id": s.cfg.Gateway.DeploymentID})
}
func (s *Server) uploadExecutionInput(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, execution.ResultBytes))
	if err != nil {
		writeError(w, 413, errors.New("workbench file is oversized"))
		return
	}
	err = s.executions.Upload(p.OwnerID, p.ClientID, r.Header.Get("X-SparkClaw-Installation"), r.PathValue("request"), r.PathValue("file"), r.Header.Get("X-SparkClaw-Digest"), raw)
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"stored": true})
}
func (s *Server) submitExecution(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, execution.ContextBytes))
	if err != nil {
		writeError(w, 413, errors.New("workbench context is oversized"))
		return
	}
	e, err := execution.Decode(raw, r.Header.Get("X-SparkClaw-Digest"))
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	if e.DeploymentID != s.cfg.Gateway.DeploymentID || e.OwnerID != p.OwnerID || e.ClientID != p.ClientID || e.InstallationID != r.Header.Get("X-SparkClaw-Installation") {
		writeExecutionError(w, execution.ErrConflict)
		return
	}
	status, err := s.executions.Submit(s.executionContext(), e, r.Header.Get("X-SparkClaw-Digest"))
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}
func (s *Server) lookupExecution(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	status, err := s.executions.Lookup(p.OwnerID, p.ClientID, r.PathValue("request"))
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, status)
}
func (s *Server) ackExecution(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	var input struct {
		Sequence int    `json:"sequence"`
		Digest   string `json:"digest"`
		Durable  bool   `json:"durable"`
	}
	if err = readExecutionJSON(r, &input); err != nil {
		writeError(w, 400, errors.New("invalid workbench receipt"))
		return
	}
	if err = s.executions.Ack(p.OwnerID, p.ClientID, r.PathValue("request"), input.Sequence, input.Digest, input.Durable); err != nil {
		writeExecutionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"state": "delivered"})
}
func (s *Server) cancelExecution(w http.ResponseWriter, r *http.Request) {
	if !emptyExecutionRequest(w, r) {
		return
	}
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	if err = s.executions.Cancel(p.OwnerID, p.ClientID, r.PathValue("request")); err != nil {
		writeExecutionError(w, err)
		return
	}
	status, err := s.executions.Lookup(p.OwnerID, p.ClientID, r.PathValue("request"))
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	writeJSON(w, 200, status)
}
func (s *Server) executionFile(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	raw, err := s.executions.File(p.OwnerID, p.ClientID, r.PathValue("request"), r.PathValue("file"))
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-SHA256", execution.Digest(raw))
	_, _ = w.Write(raw)
}

// Retain compile-time callback context checking in this assembly file.
var _ execution.Executor = func(context.Context, execution.Envelope, map[string][]byte) (execution.Output, error) {
	return execution.Output{}, nil
}

func (s *Server) resolveExecutionApproval(w http.ResponseWriter, r *http.Request) {
	p, err := s.executionPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	var input struct {
		Digest   string `json:"digest"`
		Decision string `json:"decision"`
	}
	if err = readExecutionJSON(r, &input); err != nil {
		writeError(w, 400, errors.New("invalid workbench approval decision"))
		return
	}
	if err = s.executions.DecideApproval(p.OwnerID, p.ClientID, r.PathValue("request"), r.PathValue("approval"), input.Digest, input.Decision); err != nil {
		writeExecutionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"resolved": true})
}

func (s *Server) prepareExecutionWorkspaces(root string) error {
	// A host-only Gateway may run without Linux tmpfs. Installed execution still
	// requires executionMemoryWorkspace and fails explicitly when that resource is absent.
	if _, err := os.Stat("/dev/shm"); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return executionMemorySweep(root)
}
