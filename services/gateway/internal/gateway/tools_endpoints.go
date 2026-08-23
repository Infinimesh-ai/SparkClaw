package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
)

func (s *Server) updateToolPolicy(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Deny             []string `json:"deny"`
		ApprovalRequired []string `json:"approval_required"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	deny, err := normalizePolicyToolList(input.Deny)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	approvalRequired, err := normalizePolicyToolList(input.ApprovalRequired)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	for _, tool := range deny {
		if slices.Contains(approvalRequired, tool) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("tool %q cannot be both denied and approval-required", tool))
			return
		}
	}
	if err := writeToolPolicyFile(s.cfg.Security.ToolPolicyPath, deny, approvalRequired); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.cfg.Security.DeniedTools = deny
	s.cfg.Security.ApprovalRequiredTools = approvalRequired
	s.policies = policy.New(s.cfg)
	s.runtime = s.runtime.WithPolicy(s.policies)
	s.addAudit(r.Context(), app.AuditEvent{
		Actor:   "owner",
		Type:    "tool_policy.updated",
		Summary: "Tool policy updated",
		Fields: map[string]any{
			"deny":              deny,
			"approval_required": approvalRequired,
			"path":              s.cfg.Security.ToolPolicyPath,
		},
	})
	writeJSON(w, http.StatusOK, toolPolicySummary(s.cfg.Security, s.tools.Definitions()))
}

func (s *Server) listTools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tools": s.tools.Definitions()})
}

func (s *Server) invokeTool(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SessionID string         `json:"session_id"`
		Args      map[string]any `json:"args"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	invocation, err := s.runtime.InvokeToolManually(r.Context(), r.PathValue("name"), input.Args, input.SessionID)
	if err != nil {
		var argErr agent.ManualArgumentError
		var denied agent.ManualInvocationDenied
		var execErr agent.ManualExecutionError
		switch {
		case errors.Is(err, agent.ErrManualToolNotFound):
			writeError(w, http.StatusNotFound, err)
		case errors.As(err, &argErr):
			writeError(w, http.StatusBadRequest, argErr.Err)
		case errors.As(err, &denied):
			writeError(w, http.StatusForbidden, err)
		case errors.As(err, &execErr):
			writeError(w, http.StatusBadRequest, execErr.Err)
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	s.refreshTrace(r.Context(), invocation.Call.RunID)
	if invocation.Approval != nil && invocation.Result == nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"tool_call": invocation.Call, "approval": invocation.Approval})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool_call": invocation.Call, "result": invocation.Result})
}

func (s *Server) getToolCall(w http.ResponseWriter, r *http.Request) {
	call, ok, err := s.store.GetToolCall(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("tool call not found"))
		return
	}
	writeJSON(w, http.StatusOK, call)
}

var policyToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func normalizePolicyToolList(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		tool := strings.TrimSpace(value)
		if tool == "" {
			continue
		}
		if !policyToolNamePattern.MatchString(tool) {
			return nil, fmt.Errorf("invalid tool name %q", tool)
		}
		if seen[tool] {
			continue
		}
		seen[tool] = true
		out = append(out, tool)
	}
	slices.Sort(out)
	return out, nil
}

func writeToolPolicyFile(path string, deny, approvalRequired []string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("tool policy path is not configured")
	}
	raw, err := json.MarshalIndent(map[string]any{
		"deny":              deny,
		"approval_required": approvalRequired,
	}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
