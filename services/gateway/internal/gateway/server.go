package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/skills"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

type Server struct {
	cfg       config.Config
	store     store.Store
	tools     *toolhub.ToolHub
	runtime   agent.Runtime
	traces    *trace.Writer
	artifacts artifact.Store
	policies  policy.Engine
	bindings  binding.Router
	speech    speech.Transcriber
	mux       *http.ServeMux
	started   time.Time
	limiter   *rateLimiter
}

type Option func(*Server)

func WithSpeechTranscriber(transcriber speech.Transcriber) Option {
	return func(server *Server) {
		if transcriber != nil {
			server.speech = transcriber
		}
	}
}

func New(cfg config.Config, st store.Store, tools *toolhub.ToolHub, runtime agent.Runtime, options ...Option) *Server {
	return NewWithTrace(cfg, st, tools, runtime, trace.NewWriterFromConfig(cfg), options...)
}

func NewWithTrace(cfg config.Config, st store.Store, tools *toolhub.ToolHub, runtime agent.Runtime, traces *trace.Writer, options ...Option) *Server {
	artifacts := tools.ArtifactStore()
	if artifacts == nil {
		artifacts = artifact.NewStore(cfg.Storage)
		tools.WithArtifactStore(artifacts)
	}
	s := &Server{
		cfg:       cfg,
		store:     st,
		tools:     tools,
		runtime:   runtime,
		traces:    traces,
		artifacts: artifacts,
		policies:  policy.New(cfg),
		bindings:  binding.NewRouter(cfg),
		speech:    speech.NewDisabled(cfg.Speech),
		mux:       http.NewServeMux(),
		started:   time.Now().UTC(),
		limiter:   newRateLimiter(cfg.Gateway.RateLimit),
	}
	for _, option := range options {
		option(s)
	}
	s.applyMemoryRetention()
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return withCORS(s.withRateLimit(s.withAuth(s.mux)))
}

func (s *Server) Addr() string {
	return fmt.Sprintf("%s:%d", s.cfg.Gateway.Bind, s.cfg.Gateway.Port)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.mux.HandleFunc("GET /readyz", s.readyz)
	s.mux.HandleFunc("GET /metrics", s.metrics)
	s.mux.HandleFunc("POST /chat", s.chat)
	s.mux.HandleFunc("GET /api/config", s.getConfig)
	s.mux.HandleFunc("GET /api/owner", s.getOwnerProfile)
	s.mux.HandleFunc("POST /api/owner", s.updateOwnerProfile)
	s.mux.HandleFunc("GET /api/profiles", s.listOwnerProfiles)
	s.mux.HandleFunc("GET /api/profiles/{owner_id}", s.getOwnerProfileByID)
	s.mux.HandleFunc("PATCH /api/profiles/{owner_id}", s.patchOwnerProfile)
	s.mux.HandleFunc("GET /api/clients", s.listClients)
	s.mux.HandleFunc("POST /api/clients/{id}/revoke", s.revokeClient)
	s.mux.HandleFunc("POST /api/tool-policy", s.updateToolPolicy)
	s.mux.HandleFunc("POST /api/pairing/start", s.startPairing)
	s.mux.HandleFunc("POST /api/pairing/claim", s.claimPairing)
	s.mux.HandleFunc("GET /api/notification-bindings", s.listNotificationBindings)
	s.mux.HandleFunc("POST /api/notification-bindings/{channel}/start", s.startNotificationBinding)
	s.mux.HandleFunc("GET /api/notification-bindings/{id}", s.getNotificationBinding)
	s.mux.HandleFunc("DELETE /api/notification-bindings/{id}", s.revokeNotificationBinding)
	s.mux.HandleFunc("GET /api/sessions", s.listSessions)
	s.mux.HandleFunc("POST /api/sessions", s.createSession)
	s.mux.HandleFunc("GET /api/sessions/{id}", s.getSession)
	s.mux.HandleFunc("PATCH /api/sessions/{id}", s.updateSession)
	s.mux.HandleFunc("DELETE /api/sessions/{id}", s.deleteSession)
	s.mux.HandleFunc("GET /api/sessions/{id}/messages", s.listMessages)
	s.mux.HandleFunc("POST /api/sessions/{id}/messages/stream", s.postMessageStream)
	s.mux.HandleFunc("POST /api/sessions/{id}/messages", s.postMessage)
	s.mux.HandleFunc("GET /api/sessions/{id}/events", s.listEvents)
	s.mux.HandleFunc("GET /api/sessions/{id}/events/stream", s.streamSessionEvents)
	s.mux.HandleFunc("GET /api/sessions/{id}/model-calls", s.listSessionModelCalls)
	s.mux.HandleFunc("GET /api/sessions/{id}/tool-calls", s.listSessionToolCalls)
	s.mux.HandleFunc("GET /api/sessions/{id}/audit", s.listSessionAudit)
	s.mux.HandleFunc("GET /api/sessions/{id}/episodes", s.listSessionEpisodes)
	s.mux.HandleFunc("GET /api/runs/{id}/feedback", s.listRunFeedback)
	s.mux.HandleFunc("POST /api/runs/{id}/feedback", s.saveRunFeedback)
	s.mux.HandleFunc("GET /api/tools", s.listTools)
	s.mux.HandleFunc("GET /api/skills", s.listSkills)
	s.mux.HandleFunc("POST /api/tools/{name}/invoke", s.invokeTool)
	s.mux.HandleFunc("GET /api/tool-calls/{id}", s.getToolCall)
	s.mux.HandleFunc("GET /api/approvals", s.listApprovals)
	s.mux.HandleFunc("POST /api/approvals/{id}/approve", s.approveApproval)
	s.mux.HandleFunc("POST /api/approvals/{id}/reject", s.rejectApproval)
	s.mux.HandleFunc("POST /api/approvals/{id}/modify", s.modifyApproval)
	s.mux.HandleFunc("GET /api/memories", s.listMemories)
	s.mux.HandleFunc("GET /api/memories/export", s.getMemoryExport)
	s.mux.HandleFunc("POST /api/memories/export", s.archiveMemoryExport)
	s.mux.HandleFunc("POST /api/memories/{id}/update", s.updateMemory)
	s.mux.HandleFunc("POST /api/memories/{id}/delete", s.deleteMemory)
	s.mux.HandleFunc("GET /api/memory-candidates", s.listMemoryCandidates)
	s.mux.HandleFunc("POST /api/memory-candidates/{id}/accept", s.acceptMemoryCandidate)
	s.mux.HandleFunc("POST /api/memory-candidates/{id}/reject", s.rejectMemoryCandidate)
	s.mux.HandleFunc("GET /api/traces", s.listTraces)
	s.mux.HandleFunc("GET /api/traces/{run_id}", s.getTrace)
	s.mux.HandleFunc("GET /api/artifacts", s.listArtifacts)
	s.mux.HandleFunc("GET /api/speech/status", s.getSpeechStatus)
	s.mux.HandleFunc("POST /api/speech/transcriptions", s.postSpeechTranscription)
	s.mux.HandleFunc("POST /api/documents/upload", s.uploadDocument)
	s.mux.HandleFunc("GET /api/documents/available", s.listAvailableDocuments)
	s.mux.HandleFunc("GET /api/documents/file", s.getUploadedDocument)
	s.mux.HandleFunc("GET /api/workspace/screenshots/{name}", s.getWorkspaceScreenshot)
	s.mux.HandleFunc("GET /api/evals", s.listEvals)
	s.mux.HandleFunc("POST /api/evals/run", s.runEval)
	s.mux.HandleFunc("GET /api/evals/{id}", s.getEval)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "gateway", "uptime_seconds": int(time.Since(s.started).Seconds())})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if err := os.MkdirAll(s.cfg.Storage.TraceDir, 0o755); err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if err := os.MkdirAll(s.cfg.Workspaces.DefaultRoot, 0o755); err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if artifactBackend(s.cfg) == "filesystem" || artifactBackend(s.cfg) == "local" || artifactBackend(s.cfg) == "" {
		if err := os.MkdirAll(s.cfg.Storage.ArtifactDir, 0o755); err != nil {
			writeError(w, http.StatusServiceUnavailable, err)
			return
		}
	}
	speechStatus := s.speech.Status(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"workspace_root":   s.cfg.Workspaces.DefaultRoot,
		"trace_dir":        s.cfg.Storage.TraceDir,
		"artifact_backend": s.cfg.Storage.ArtifactBackend,
		"artifact_dir":     s.cfg.Storage.ArtifactDir,
		"artifact_bucket":  s.cfg.Storage.ArtifactBucket,
		"state_backend":    s.cfg.State.Backend,
		"state_path":       s.cfg.State.Path,
		"state_dsn":        stateDSNStatus(s.cfg),
		"auth_required":    s.authRequired(),
		"rate_limit":       publicRateLimitConfig(s.cfg.Gateway.RateLimit),
		"model_mode":       modelMode(s.cfg),
		"gateway_binding":  s.Addr(),
		"speech":           speechStatus,
	})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	s.applyMemoryRetention()
	sessions := s.store.ListSessions()
	messages := 0
	runs := 0
	allModelCalls := s.store.ListModelCalls("", "")
	modelCalls := len(allModelCalls)
	modelErrors := 0
	modelLatencyTotal := int64(0)
	modelTokensTotal := 0
	for _, session := range sessions {
		messages += len(s.store.ListMessages(session.ID))
		runs += len(s.store.ListRuns(session.ID))
	}
	for _, call := range allModelCalls {
		if call.Status == "failed" {
			modelErrors++
		}
		modelLatencyTotal += call.LatencyMS
		modelTokensTotal += call.TotalTokens
	}
	modelLatencyAverage := float64(0)
	if modelCalls > 0 {
		modelLatencyAverage = float64(modelLatencyTotal) / float64(modelCalls)
	}
	approvals := s.store.ListApprovals("")
	pendingApprovals := 0
	rateLimited := 0
	for _, approval := range approvals {
		if approval.Status == "pending" {
			pendingApprovals++
		}
	}
	if s.limiter != nil {
		rateLimited = s.limiter.rejectedCount()
	}
	lines := []string{
		"# HELP sparkclaw_gateway_uptime_seconds Gateway process uptime in seconds.",
		"# TYPE sparkclaw_gateway_uptime_seconds gauge",
		fmt.Sprintf("sparkclaw_gateway_uptime_seconds %d", int(time.Since(s.started).Seconds())),
		"# HELP sparkclaw_gateway_auth_required Whether API authentication is required.",
		"# TYPE sparkclaw_gateway_auth_required gauge",
		fmt.Sprintf("sparkclaw_gateway_auth_required %d", boolMetric(s.authRequired())),
		"# HELP sparkclaw_gateway_rate_limit_rejections_total Total HTTP requests rejected by the Gateway rate limiter.",
		"# TYPE sparkclaw_gateway_rate_limit_rejections_total counter",
		fmt.Sprintf("sparkclaw_gateway_rate_limit_rejections_total %d", rateLimited),
		"# HELP sparkclaw_sessions_total Current session count.",
		"# TYPE sparkclaw_sessions_total gauge",
		fmt.Sprintf("sparkclaw_sessions_total %d", len(sessions)),
		"# HELP sparkclaw_messages_total Current message count.",
		"# TYPE sparkclaw_messages_total gauge",
		fmt.Sprintf("sparkclaw_messages_total %d", messages),
		"# HELP sparkclaw_agent_runs_total Current agent run count.",
		"# TYPE sparkclaw_agent_runs_total gauge",
		fmt.Sprintf("sparkclaw_agent_runs_total %d", runs),
		"# HELP sparkclaw_model_calls_total Current model call count.",
		"# TYPE sparkclaw_model_calls_total gauge",
		fmt.Sprintf("sparkclaw_model_calls_total %d", modelCalls),
		"# HELP sparkclaw_model_call_errors_total Current failed model call count.",
		"# TYPE sparkclaw_model_call_errors_total gauge",
		fmt.Sprintf("sparkclaw_model_call_errors_total %d", modelErrors),
		"# HELP sparkclaw_model_call_latency_ms_avg Average stored model call latency in milliseconds.",
		"# TYPE sparkclaw_model_call_latency_ms_avg gauge",
		fmt.Sprintf("sparkclaw_model_call_latency_ms_avg %.2f", modelLatencyAverage),
		"# HELP sparkclaw_model_call_tokens_total Total stored model call token usage.",
		"# TYPE sparkclaw_model_call_tokens_total gauge",
		fmt.Sprintf("sparkclaw_model_call_tokens_total %d", modelTokensTotal),
		"# HELP sparkclaw_tool_calls_total Current tool call count.",
		"# TYPE sparkclaw_tool_calls_total gauge",
		fmt.Sprintf("sparkclaw_tool_calls_total %d", len(s.store.ListToolCalls(""))),
		"# HELP sparkclaw_approvals_total Current approval count.",
		"# TYPE sparkclaw_approvals_total gauge",
		fmt.Sprintf("sparkclaw_approvals_total %d", len(approvals)),
		"# HELP sparkclaw_approvals_pending Current pending approval count.",
		"# TYPE sparkclaw_approvals_pending gauge",
		fmt.Sprintf("sparkclaw_approvals_pending %d", pendingApprovals),
		"# HELP sparkclaw_memory_candidates_total Current memory candidate count.",
		"# TYPE sparkclaw_memory_candidates_total gauge",
		fmt.Sprintf("sparkclaw_memory_candidates_total %d", len(s.store.ListMemoryCandidates(""))),
		"# HELP sparkclaw_memories_total Current accepted memory count.",
		"# TYPE sparkclaw_memories_total gauge",
		fmt.Sprintf("sparkclaw_memories_total %d", len(s.store.SearchMemories(""))),
		"# HELP sparkclaw_episode_summaries_total Current episode summary count.",
		"# TYPE sparkclaw_episode_summaries_total gauge",
		fmt.Sprintf("sparkclaw_episode_summaries_total %d", len(s.store.ListEpisodeSummaries(""))),
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(strings.Join(lines, "\n") + "\n"))
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"gateway":     publicGatewayConfig(s.cfg.Gateway),
		"model":       publicModelConfig(s.cfg.Model),
		"speech":      publicSpeechConfig(s.cfg.Speech),
		"workspaces":  s.cfg.Workspaces,
		"security":    s.cfg.Security,
		"sandbox":     s.cfg.Sandbox,
		"storage":     publicStorageConfig(s.cfg.Storage),
		"state":       publicStateConfig(s.cfg.State),
		"adapters":    publicAdapterConfig(s.cfg.Adapters),
		"tools":       publicToolsConfig(s.cfg),
		"memory":      s.cfg.Memory,
		"skills":      s.cfg.Skills,
		"runtime":     s.cfg.Runtime,
		"tool_policy": toolPolicySummary(s.cfg.Security, s.tools.Definitions()),
	})
}

func (s *Server) getOwnerProfile(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.GetOwnerProfile())
}

func (s *Server) updateOwnerProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName string            `json:"display_name"`
		Email       string            `json:"email"`
		Preferences map[string]string `json:"preferences"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	current := s.store.GetOwnerProfile()
	profile, err := normalizeOwnerProfileInput(current, input.DisplayName, input.Email, input.Preferences)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.store.UpdateOwnerProfile(profile))
}

func (s *Server) listOwnerProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"profiles": s.store.ListOwnerProfiles()})
}

func (s *Server) getOwnerProfileByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("owner_id"))
	profile, ok := s.store.GetOwnerProfileByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("profile not found"))
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (s *Server) patchOwnerProfile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("owner_id"))
	current, ok := s.store.GetOwnerProfileByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("profile not found"))
		return
	}
	var input struct {
		DisplayName      string            `json:"display_name"`
		Email            string            `json:"email"`
		Preferences      map[string]string `json:"preferences"`
		Source           string            `json:"source"`
		ExternalRef      string            `json:"external_ref"`
		WorkspaceRoot    string            `json:"workspace_root"`
		DefaultChannel   string            `json:"default_channel"`
		DefaultBindingID string            `json:"default_binding_id"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	profile, err := normalizeOwnerProfileInput(current, input.DisplayName, input.Email, input.Preferences)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	profile.Source = strings.TrimSpace(input.Source)
	profile.ExternalRef = strings.TrimSpace(input.ExternalRef)
	profile.WorkspaceRoot = strings.TrimSpace(input.WorkspaceRoot)
	profile.DefaultChannel = strings.TrimSpace(input.DefaultChannel)
	profile.DefaultBindingID = strings.TrimSpace(input.DefaultBindingID)
	writeJSON(w, http.StatusOK, s.store.SaveOwnerProfile(profile))
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"clients": s.store.ListClients()})
}

func (s *Server) revokeClient(w http.ResponseWriter, r *http.Request) {
	client, err := s.store.RevokeClient(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, client)
}

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
	s.store.AddAudit(app.AuditEvent{
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

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Message string `json:"message"`
		Content string `json:"content"`
		System  string `json:"system"`
		Profile string `json:"profile"`
		Model   string `json:"model"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	content := strings.TrimSpace(input.Message)
	if content == "" {
		content = strings.TrimSpace(input.Content)
	}
	if content == "" {
		writeError(w, http.StatusBadRequest, errors.New("message is required"))
		return
	}
	profile := strings.TrimSpace(input.Profile)
	if profile == "" {
		profile = strings.TrimSpace(input.Model)
	}
	if profile == "" {
		profile = "fast"
	}
	system := strings.TrimSpace(input.System)
	if system == "" {
		system = "You are SparkClaw chat, a local-first model router endpoint. Answer directly and do not claim that tools were executed."
	}
	started := time.Now().UTC()
	result, err := modelrouter.New(s.cfg).ChatWithProfile(r.Context(), profile, system, content)
	completed := time.Now().UTC()
	s.store.SaveModelCall(modelCallFromChat("", "", "direct_chat", result, err, started, completed))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": result.Content,
		"model":   result,
	})
}

func (s *Server) startPairing(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Gateway.PairingRequired {
		writeError(w, http.StatusBadRequest, errors.New("pairing is not required"))
		return
	}
	if !isLocalRequest(r) {
		writeError(w, http.StatusForbidden, errors.New("pairing can only be started locally"))
		return
	}
	code, err := randomSecret(8)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	pairing := app.PairingCode{
		ID:        app.NewID("pair"),
		CodeHash:  hashSecret(code),
		Status:    "pending",
		ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
		CreatedAt: time.Now().UTC(),
	}
	s.store.SavePairingCode(pairing)
	writeJSON(w, http.StatusCreated, map[string]any{
		"pairing_id": pairing.ID,
		"code":       code,
		"expires_at": pairing.ExpiresAt,
	})
}

func (s *Server) claimPairing(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Gateway.PairingRequired {
		writeError(w, http.StatusBadRequest, errors.New("pairing is not required"))
		return
	}
	var input struct {
		PairingID  string `json:"pairing_id"`
		Code       string `json:"code"`
		ClientName string `json:"client_name"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	pairing, ok := s.store.GetPairingCode(strings.TrimSpace(input.PairingID))
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("pairing code not found"))
		return
	}
	if pairing.Status != "pending" || time.Now().UTC().After(pairing.ExpiresAt) {
		writeError(w, http.StatusBadRequest, errors.New("pairing code is not active"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(pairing.CodeHash), []byte(hashSecret(input.Code))) != 1 {
		writeError(w, http.StatusUnauthorized, errors.New("invalid pairing code"))
		return
	}
	token, err := randomSecret(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	clientName := strings.TrimSpace(input.ClientName)
	if clientName == "" {
		clientName = "SparkClaw Client"
	}
	client := app.Client{
		ID:        app.NewID("client"),
		Name:      clientName,
		TokenHash: hashSecret(token),
		CreatedAt: time.Now().UTC(),
	}
	s.store.SaveClient(client)
	if _, err := s.store.ClaimPairingCode(pairing.ID, client.ID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client": client,
		"token":  token,
	})
}

func (s *Server) listNotificationBindings(w http.ResponseWriter, r *http.Request) {
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	writeJSON(w, http.StatusOK, map[string]any{
		"bindings": publicNotificationBindings(s.store.ListNotificationBindings(channel, status)),
	})
}

func (s *Server) startNotificationBinding(w http.ResponseWriter, r *http.Request) {
	channel := strings.TrimSpace(r.PathValue("channel"))
	if channel == "" {
		writeError(w, http.StatusBadRequest, errors.New("channel is required"))
		return
	}
	var input struct {
		Scopes            []string `json:"scopes"`
		DefaultForChannel bool     `json:"default_for_channel"`
	}
	if r.Body != nil && r.Body != http.NoBody {
		_ = json.NewDecoder(r.Body).Decode(&input)
	}
	if len(input.Scopes) == 0 {
		input.Scopes = []string{"reminder_send_self"}
	}
	now := time.Now().UTC()
	binding := app.NotificationBinding{
		ID:                app.NewID("bind"),
		OwnerID:           app.DefaultOwnerID,
		Channel:           channel,
		Status:            "waiting_scan",
		DefaultForChannel: input.DefaultForChannel,
		Scopes:            input.Scopes,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	started, err := s.bindings.Start(r.Context(), binding)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	started = s.store.SaveNotificationBinding(started)
	writeJSON(w, http.StatusCreated, publicNotificationBinding(started))
}

func (s *Server) getNotificationBinding(w http.ResponseWriter, r *http.Request) {
	bindingID := strings.TrimSpace(r.PathValue("id"))
	binding, ok := s.store.GetNotificationBinding(bindingID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	if binding.Status == "waiting_scan" || binding.Status == "waiting_confirm" {
		poll, err := s.bindings.Poll(r.Context(), binding)
		if err == nil && poll.Status != "" && poll.Status != binding.Status {
			binding.Status = poll.Status
			binding.DisplayName = poll.DisplayName
			binding.ExternalUserID = poll.ExternalUserID
			binding.AccountID = poll.AccountID
			binding.CredentialRef = poll.CredentialRef
			binding.BaseURL = poll.BaseURL
			binding.LastError = poll.LastError
			binding.UpdatedAt = time.Now().UTC()
			if poll.CredentialRef != "" && poll.CredentialSecret != "" {
				s.store.SaveCredentialSecret(app.CredentialSecret{
					Ref:   poll.CredentialRef,
					Kind:  poll.CredentialKind,
					Value: poll.CredentialSecret,
				})
			}
			if binding.Status == "active" {
				if binding.DefaultForChannel || !s.hasActiveDefaultNotificationBinding(binding.Channel, binding.ID) {
					binding.DefaultForChannel = true
				}
				binding.QRCodeURL = ""
				binding.QRCodeImage = ""
			}
			binding = s.store.SaveNotificationBinding(binding)
		} else if err != nil {
			binding.LastError = err.Error()
			binding.UpdatedAt = time.Now().UTC()
			binding = s.store.SaveNotificationBinding(binding)
		}
	}
	writeJSON(w, http.StatusOK, publicNotificationBinding(binding))
}

func (s *Server) hasActiveDefaultNotificationBinding(channel, exceptID string) bool {
	for _, binding := range s.store.ListNotificationBindings(channel, "active") {
		if binding.ID != exceptID && binding.DefaultForChannel {
			return true
		}
	}
	return false
}

func (s *Server) revokeNotificationBinding(w http.ResponseWriter, r *http.Request) {
	bindingID := strings.TrimSpace(r.PathValue("id"))
	binding, ok := s.store.GetNotificationBinding(bindingID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	_ = s.bindings.Cancel(r.Context(), binding)
	revoked, err := s.store.RevokeNotificationBinding(bindingID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(binding.CredentialRef) != "" {
		_ = s.store.DeleteCredentialSecret(binding.CredentialRef)
	}
	writeJSON(w, http.StatusOK, publicNotificationBinding(revoked))
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	ownerID := queryOwnerID(r)
	sessions := []app.Session{}
	for _, session := range s.store.ListSessions() {
		if sessionOwnerID(session) == ownerID {
			sessions = append(sessions, session)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title   string `json:"title"`
		OwnerID string `json:"owner_id"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ownerID := strings.TrimSpace(input.OwnerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	profile, ok := s.store.GetOwnerProfileByID(ownerID)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("profile not found"))
		return
	}
	session := s.store.CreateSessionWithScope(input.Title, profile.ID, profile.WorkspaceRoot, "webchat", false)
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.store.GetSession(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) updateSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title string `json:"title"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	session, err := s.store.UpdateSessionTitle(r.PathValue("id"), input.Title)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.DeleteSession(r.PathValue("id"))
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"messages": s.store.ListMessages(r.PathValue("id"))})
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, ok := s.store.GetSession(sessionID); !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	var input struct {
		Content     string                    `json:"content"`
		Attachments []agent.MessageAttachment `json:"attachments"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(input.Content) == "" {
		writeError(w, http.StatusBadRequest, errors.New("content is required"))
		return
	}
	result, err := s.runtime.HandleMessageWithAttachments(r.Context(), sessionID, input.Content, sanitizeMessageAttachments(input.Attachments))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) postMessageStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, ok := s.store.GetSession(sessionID); !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	var input struct {
		Content     string                    `json:"content"`
		Attachments []agent.MessageAttachment `json:"attachments"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(input.Content) == "" {
		writeError(w, http.StatusBadRequest, errors.New("content is required"))
		return
	}
	after := lastEventID(s.store.EventsAfter(sessionID, ""))
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("message streaming is unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusCreated)

	send := func(name string, value any) error {
		if err := writeNamedSSE(w, name, value); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := send("message.stream.started", map[string]string{"session_id": sessionID}); err != nil {
		return
	}
	attachments := sanitizeMessageAttachments(input.Attachments)

	type streamResult struct {
		result agent.Result
		err    error
	}
	modelEvents := make(chan agent.StreamEvent, 16)
	results := make(chan streamResult, 1)
	go func() {
		result, err := s.runtime.HandleMessageStreamWithAttachments(r.Context(), sessionID, input.Content, attachments, func(event agent.StreamEvent) error {
			select {
			case <-r.Context().Done():
				return r.Context().Err()
			case modelEvents <- event:
				return nil
			}
		})
		results <- streamResult{result: result, err: err}
		close(modelEvents)
	}()

	sendRuntimeEvents := func() bool {
		for _, event := range s.store.EventsAfter(sessionID, after) {
			if event.ID == after {
				continue
			}
			after = event.ID
			if !streamVisibleEvent(event.Type) {
				continue
			}
			if err := send(event.Type, event); err != nil {
				return false
			}
		}
		return true
	}

	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-modelEvents:
			if !ok {
				modelEvents = nil
				continue
			}
			name := strings.TrimSpace(event.Type)
			if name == "" {
				name = "model.event"
			}
			if err := send(name, event); err != nil {
				return
			}
		case <-poll.C:
			if !sendRuntimeEvents() {
				return
			}
		case result := <-results:
			if !sendRuntimeEvents() {
				return
			}
			if result.err != nil {
				_ = send("error", map[string]string{"error": result.err.Error(), "session_id": sessionID})
				return
			}
			_ = send("message.stream.final", result.result)
			return
		}
	}
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"events": s.store.EventsAfter(r.PathValue("id"), r.URL.Query().Get("after"))})
}

func (s *Server) streamSessionEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, ok := s.store.GetSession(sessionID); !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("event streaming is unavailable"))
		return
	}
	after := r.URL.Query().Get("after")
	if after == "" {
		after = r.Header.Get("Last-Event-ID")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func() bool {
		for _, event := range s.store.EventsAfter(sessionID, after) {
			if event.ID == after {
				continue
			}
			if err := writeSSEEvent(w, event); err != nil {
				return false
			}
			after = event.ID
		}
		flusher.Flush()
		return true
	}
	if !send() {
		return
	}
	poll := time.NewTicker(750 * time.Millisecond)
	ping := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			if !send() {
				return
			}
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) listSessionToolCalls(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tool_calls": s.store.ListToolCalls(r.PathValue("id"))})
}

func (s *Server) listSessionAudit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"audit_events": s.store.ListAudit(r.PathValue("id"))})
}

func (s *Server) listSessionEpisodes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"episodes": s.store.ListEpisodeSummaries(r.PathValue("id"))})
}

func (s *Server) listSessionModelCalls(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"model_calls": s.store.ListModelCalls(r.PathValue("id"), r.URL.Query().Get("run_id"))})
}

func (s *Server) listRunFeedback(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"feedback": s.store.ListRunFeedback(r.PathValue("id"))})
}

func (s *Server) saveRunFeedback(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	run, ok := s.store.GetRun(runID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("run not found"))
		return
	}
	var input struct {
		MessageID  string `json:"message_id"`
		Rating     string `json:"rating"`
		Note       string `json:"note"`
		Correction string `json:"correction"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rating := strings.TrimSpace(strings.ToLower(input.Rating))
	if rating != "up" && rating != "down" && rating != "corrected" {
		writeError(w, http.StatusBadRequest, errors.New("feedback rating must be up, down, or corrected"))
		return
	}
	feedback := s.store.SaveRunFeedback(app.RunFeedback{
		SessionID:  run.SessionID,
		RunID:      run.ID,
		MessageID:  strings.TrimSpace(input.MessageID),
		Rating:     rating,
		Note:       input.Note,
		Correction: input.Correction,
	})
	s.refreshTrace(r.Context(), run.ID)
	writeJSON(w, http.StatusOK, feedback)
}

func (s *Server) listTools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tools": s.tools.Definitions()})
}

func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	found, err := skills.NewRegistry(s.cfg).List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": found})
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
	call, ok := s.store.GetToolCall(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("tool call not found"))
		return
	}
	writeJSON(w, http.StatusOK, call)
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"approvals": s.store.ListApprovals(r.URL.Query().Get("status"))})
}

func (s *Server) approveApproval(w http.ResponseWriter, r *http.Request) {
	s.resolveApproval(w, r, "approved")
}

func (s *Server) rejectApproval(w http.ResponseWriter, r *http.Request) {
	s.resolveApproval(w, r, "rejected")
}

func (s *Server) modifyApproval(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Note      string         `json:"note"`
		Args      map[string]any `json:"args"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	newArgs := input.Arguments
	if newArgs == nil {
		newArgs = input.Args
	}
	if len(newArgs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("modify requires args or arguments"))
		return
	}
	approval, ok := s.findApproval(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("approval not found"))
		return
	}
	if approval.Status != "pending" {
		writeError(w, http.StatusBadRequest, errors.New("approval already resolved"))
		return
	}
	call, ok := s.store.GetToolCall(approval.ToolCallID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("tool call not found"))
		return
	}
	if call.Status != "approval_pending" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("tool call cannot be modified from status %q", call.Status))
		return
	}
	args := mergeApprovalArgs(approval.Arguments, newArgs)
	if def, ok := s.tools.Definition(approval.Tool); ok {
		if err := s.tools.Validate(approval.Tool, args); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		decision := s.policies.Decide(def, args)
		if !decision.Allowed {
			writeError(w, http.StatusForbidden, errors.New(decision.Reason))
			return
		}
		approval.Resources = decision.Resources
	}
	approval.Arguments = args
	call.Arguments = args
	s.store.SaveToolCall(call)
	s.store.SaveApproval(approval)
	s.store.AddAudit(app.AuditEvent{
		SessionID: approval.SessionID,
		RunID:     approval.RunID,
		Actor:     "owner",
		Type:      "approval.modified",
		Summary:   approval.Summary,
		Fields: map[string]any{
			"tool": approval.Tool,
			"note": input.Note,
		},
	})
	s.refreshTrace(r.Context(), approval.RunID)
	writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "tool_call": call})
}

func (s *Server) resolveApproval(w http.ResponseWriter, r *http.Request, status string) {
	var input struct {
		Note string `json:"note"`
	}
	_ = readJSON(r, &input)
	approval, err := s.store.ResolveApproval(r.PathValue("id"), status, input.Note)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var call *app.ToolCall
	resumed := false
	if status == "approved" {
		executed, err := s.runtime.ExecuteApprovedToolCall(r.Context(), approval)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		call = &executed
		if _, ok, err := s.runtime.ResumeRunAfterApproval(r.Context(), approval.SessionID, approval.RunID); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		} else if ok {
			resumed = true
		}
	}
	if status == "rejected" {
		if rejected, ok := s.store.GetToolCall(approval.ToolCallID); ok {
			now := time.Now().UTC()
			rejected.Status = "rejected"
			rejected.Error = "owner rejected approval"
			rejected.CompletedAt = &now
			s.store.SaveToolCall(rejected)
			call = &rejected
		}
	}
	if !resumed {
		s.runtime.CompleteRunIfApprovalsResolved(approval.RunID)
	}
	s.refreshTrace(r.Context(), approval.RunID)
	writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "tool_call": call})
}

func (s *Server) findApproval(id string) (app.Approval, bool) {
	for _, approval := range s.store.ListApprovals("") {
		if approval.ID == id {
			return approval, true
		}
	}
	return app.Approval{}, false
}

func mergeApprovalArgs(current, patch map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range current {
		out[key] = value
	}
	for key, value := range patch {
		if strings.HasPrefix(key, "_") {
			continue
		}
		out[key] = value
	}
	return out
}

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	s.applyMemoryRetention()
	writeJSON(w, http.StatusOK, map[string]any{"memories": s.store.SearchMemories(r.URL.Query().Get("query"))})
}

func (s *Server) getMemoryExport(w http.ResponseWriter, r *http.Request) {
	s.applyMemoryRetention()
	writeJSON(w, http.StatusOK, s.buildMemoryExport())
}

func (s *Server) archiveMemoryExport(w http.ResponseWriter, r *http.Request) {
	s.applyMemoryRetention()
	export := s.buildMemoryExport()
	raw, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	now := time.Now().UTC()
	object, err := s.artifacts.Put(r.Context(), filepath.Join("memory-exports", now.Format("20060102T150405Z")+"-"+app.NewID("snapshot")+".json"), "application/json", raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	artifactObject := app.ArtifactObject{
		ID:          app.NewID("obj"),
		Kind:        "memory_export",
		Backend:     object.Backend,
		Bucket:      object.Bucket,
		Key:         object.Key,
		URI:         object.URI,
		Path:        object.Path,
		ContentType: object.ContentType,
		Bytes:       object.Bytes,
		CreatedAt:   now,
	}
	s.store.SaveArtifactObject(artifactObject)
	s.store.AddAudit(app.AuditEvent{
		Type:    "memory.exported",
		Actor:   "owner",
		Summary: artifactObject.URI,
		Fields: map[string]any{
			"artifact_id":        artifactObject.ID,
			"memory_count":       export.Counts.Memories,
			"candidate_count":    export.Counts.MemoryCandidates,
			"episode_count":      export.Counts.Episodes,
			"pending_candidates": export.Counts.PendingCandidates,
		},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"export": export, "artifact": artifactObject})
}

func (s *Server) buildMemoryExport() app.MemoryExport {
	s.applyMemoryRetention()
	candidates := s.store.ListMemoryCandidates("")
	pending := 0
	for _, candidate := range candidates {
		if candidate.Status == "pending" {
			pending++
		}
	}
	memories := s.store.SearchMemories("")
	episodes := s.store.ListEpisodeSummaries("")
	return app.MemoryExport{
		GeneratedAt:      time.Now().UTC(),
		OwnerProfile:     s.store.GetOwnerProfile(),
		Memories:         memories,
		MemoryCandidates: candidates,
		Episodes:         episodes,
		Counts: app.MemoryExportCounts{
			Memories:          len(memories),
			MemoryCandidates:  len(candidates),
			PendingCandidates: pending,
			Episodes:          len(episodes),
		},
	}
}

func (s *Server) updateMemory(w http.ResponseWriter, r *http.Request) {
	s.applyMemoryRetention()
	var req struct {
		Kind    string `json:"kind"`
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.Content = strings.TrimSpace(req.Content)
	if req.Kind == "" {
		writeError(w, http.StatusBadRequest, errors.New("memory kind is required"))
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, errors.New("memory content is required"))
		return
	}
	if !s.cfg.Memory.AllowSensitiveMemory {
		if pattern, ok := memorySensitivePattern(req.Content, s.cfg.Memory.RedactPatterns); ok {
			writeError(w, http.StatusBadRequest, fmt.Errorf("memory appears sensitive (%s); sensitive memory is disabled", pattern))
			return
		}
	}
	memory, err := s.store.UpdateMemory(r.PathValue("id"), req.Kind, req.Content)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, memory)
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	s.applyMemoryRetention()
	memory, err := s.store.DeleteMemory(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, memory)
}

func (s *Server) applyMemoryRetention() []app.Memory {
	if s.cfg.Memory.RetentionDays <= 0 {
		return []app.Memory{}
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -s.cfg.Memory.RetentionDays)
	return s.store.PruneMemories(cutoff)
}

func memorySensitivePattern(content string, patterns []string) (string, bool) {
	lower := strings.ToLower(content)
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if strings.Contains(lower, pattern) {
			return pattern, true
		}
	}
	return "", false
}

func (s *Server) listMemoryCandidates(w http.ResponseWriter, r *http.Request) {
	ownerID := queryOwnerID(r)
	candidates := []app.MemoryCandidate{}
	for _, candidate := range s.store.ListMemoryCandidates(r.URL.Query().Get("status")) {
		if s.sessionIDVisibleToOwner(candidate.SessionID, ownerID) {
			candidates = append(candidates, candidate)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"memory_candidates": candidates})
}

func (s *Server) acceptMemoryCandidate(w http.ResponseWriter, r *http.Request) {
	candidate, memory, err := s.store.ResolveMemoryCandidate(r.PathValue("id"), "accepted")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidate": candidate, "memory": memory})
}

func (s *Server) rejectMemoryCandidate(w http.ResponseWriter, r *http.Request) {
	candidate, _, err := s.store.ResolveMemoryCandidate(r.PathValue("id"), "rejected")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, candidate)
}

func (s *Server) getTrace(w http.ResponseWriter, r *http.Request) {
	runID := filepath.Base(r.PathValue("run_id"))
	path := filepath.Join(s.cfg.Storage.TraceDir, runID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("trace not found"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) listTraces(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	runs := s.store.ListRuns("")
	out := make([]app.TraceMetadata, 0, min(limit, len(runs)))
	for _, run := range runs {
		if len(out) >= limit {
			break
		}
		toolCalls := toolCallsForRun(s.store.ListToolCalls(run.SessionID), run.ID)
		approvals := approvalsForRun(s.store.ListApprovals(""), run.ID)
		modelCalls := s.store.ListModelCalls(run.SessionID, run.ID)
		meta := app.TraceMetadata{
			RunID:          run.ID,
			SessionID:      run.SessionID,
			State:          run.State,
			Risk:           run.Risk,
			ModelLane:      run.ModelLane,
			Summary:        run.Summary,
			StartedAt:      run.StartedAt,
			CompletedAt:    run.CompletedAt,
			MessageCount:   len(s.store.ListMessages(run.SessionID)),
			ToolCallCount:  len(toolCalls),
			ApprovalCount:  len(approvals),
			ModelCallCount: len(modelCalls),
		}
		if artifactURI, artifactPath := s.traceArtifactRef(run.ID); artifactURI != "" || artifactPath != "" {
			meta.ArtifactURI = artifactURI
			meta.ArtifactPath = artifactPath
		}
		out = append(out, meta)
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": out})
}

func (s *Server) traceArtifactRef(runID string) (string, string) {
	raw, err := os.ReadFile(filepath.Join(s.cfg.Storage.TraceDir, filepath.Base(runID)+".json"))
	if err != nil {
		return "", ""
	}
	var current trace.RunTrace
	if err := json.Unmarshal(raw, &current); err != nil || current.Artifact == nil {
		return "", ""
	}
	return current.Artifact.URI, current.Artifact.Path
}

func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	ownerID := queryOwnerID(r)
	objects := []app.ArtifactObject{}
	for _, object := range s.store.ListArtifactObjects(0) {
		if s.artifactVisibleToOwner(object, ownerID) {
			objects = append(objects, object)
		}
		if limit > 0 && len(objects) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": objects})
}

func (s *Server) uploadDocument(w http.ResponseWriter, r *http.Request) {
	const maxUploadBytes = 25 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("multipart field \"file\" is required"))
		return
	}
	defer file.Close()
	if r.MultipartForm != nil && len(r.MultipartForm.File["file"]) > 1 {
		writeError(w, http.StatusBadRequest, errors.New("only one file may be uploaded per request"))
		return
	}
	name := cleanUploadFilename(header.Filename)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("uploaded filename is required"))
		return
	}
	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	var sniff bytes.Buffer
	if _, err := io.Copy(&sniff, io.LimitReader(file, 512)); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	detectedContentType := http.DetectContentType(sniff.Bytes())
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = detectedContentType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	name = ensureUploadFilenameExtension(name, contentType, detectedContentType)
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	workspaceRoot := s.workspaceRootForSession(sessionID)
	now := time.Now().UTC()
	isImage := isImageContentType(contentType)
	rootName := "uploads"
	idPrefix := "upload"
	artifactKind := "document_upload"
	if isImage {
		rootName = "media"
		idPrefix = "media"
		artifactKind = "media_image_upload"
	}
	relPath := filepath.Join(rootName, now.Format("20060102"), app.NewID(idPrefix)+"-"+name)
	path := filepath.Join(workspaceRoot, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	bytesWritten, copyErr := io.Copy(out, io.MultiReader(bytes.NewReader(sniff.Bytes()), file))
	closeErr := out.Close()
	if copyErr != nil {
		writeError(w, http.StatusInternalServerError, copyErr)
		return
	}
	if closeErr != nil {
		writeError(w, http.StatusInternalServerError, closeErr)
		return
	}
	object := app.ArtifactObject{
		ID:          app.NewID("obj"),
		Kind:        artifactKind,
		SessionID:   sessionID,
		Backend:     "workspace",
		Key:         relPath,
		URI:         "workspace://" + filepath.ToSlash(relPath),
		Path:        path,
		ContentType: contentType,
		Bytes:       int(bytesWritten),
		CreatedAt:   now,
	}
	s.store.SaveArtifactObject(object)
	s.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		Actor:     "owner",
		Type:      uploadAuditType(isImage),
		Summary:   relPath,
		Fields: map[string]any{
			"artifact_id":  object.ID,
			"path":         path,
			"rel_path":     relPath,
			"content_type": contentType,
			"bytes":        bytesWritten,
		},
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"artifact": object,
		"path":     path,
		"rel_path": relPath,
		"bytes":    bytesWritten,
		"media":    uploadedMediaMetadata(path, relPath, contentType, isImage),
	})
}

func (s *Server) listAvailableDocuments(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 50)
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	out := []app.ArtifactObject{}
	seen := map[string]bool{}
	workspaceRoot := s.workspaceRootForSession(strings.TrimSpace(r.URL.Query().Get("session_id")))
	for _, rootName := range []string{"uploads", "media"} {
		if len(out) >= limit {
			break
		}
		uploadRoot := filepath.Join(workspaceRoot, rootName)
		_ = filepath.WalkDir(uploadRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || len(out) >= limit {
				return nil
			}
			if path != uploadRoot && strings.HasPrefix(entry.Name(), ".") {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if !pathWithinRoot(uploadRoot, path) || !pathWithinRoot(workspaceRoot, path) {
				return nil
			}
			rel, err := filepath.Rel(workspaceRoot, path)
			if err != nil {
				return nil
			}
			key := filepath.ToSlash(rel)
			if seen[key] {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			contentType := "application/octet-stream"
			if file, err := os.Open(path); err == nil {
				var sniff [512]byte
				n, _ := file.Read(sniff[:])
				_ = file.Close()
				contentType = http.DetectContentType(sniff[:n])
			}
			out = append(out, app.ArtifactObject{
				ID:          "workspace:" + key,
				Kind:        availableObjectKind(key, contentType),
				Backend:     "workspace",
				Key:         key,
				URI:         "workspace://" + key,
				Path:        path,
				ContentType: contentType,
				Bytes:       int(info.Size()),
				CreatedAt:   info.ModTime().UTC(),
			})
			seen[key] = true
			return nil
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": out})
}

func isImageContentType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func uploadAuditType(isImage bool) string {
	if isImage {
		return "media.image.uploaded"
	}
	return "document.uploaded"
}

func availableObjectKind(key, contentType string) string {
	if strings.HasPrefix(filepath.ToSlash(key), "media/") || isImageContentType(contentType) {
		return "media_image_upload"
	}
	return "document_upload"
}

func uploadedMediaMetadata(path, relPath, contentType string, isImage bool) map[string]any {
	if !isImage {
		return nil
	}
	meta := map[string]any{
		"rel_path":     filepath.ToSlash(relPath),
		"content_type": contentType,
	}
	if raw, err := os.ReadFile(path); err == nil {
		sum := sha256.Sum256(raw)
		meta["sha256"] = hex.EncodeToString(sum[:])
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err == nil {
			meta["width"] = cfg.Width
			meta["height"] = cfg.Height
		}
	}
	return meta
}

func (s *Server) getUploadedDocument(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if relPath == "" {
		writeError(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	clean, ok := cleanWorkspaceRelativePath(relPath)
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("invalid document path"))
		return
	}
	workspaceRoot := s.workspaceRootForSession(strings.TrimSpace(r.URL.Query().Get("session_id")))
	path := filepath.Join(workspaceRoot, clean)
	if !pathWithinRoot(workspaceRoot, path) {
		writeError(w, http.StatusBadRequest, errors.New("document path escapes workspace"))
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, path)
}

func (s *Server) workspaceRootForSession(sessionID string) string {
	if strings.TrimSpace(sessionID) != "" {
		if session, ok := s.store.GetSession(sessionID); ok && strings.TrimSpace(session.WorkspaceRoot) != "" {
			return strings.TrimSpace(session.WorkspaceRoot)
		}
	}
	return strings.TrimSpace(s.cfg.Workspaces.DefaultRoot)
}

func (s *Server) getWorkspaceScreenshot(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(strings.TrimSpace(r.PathValue("name")))
	if name == "" || name == "." || name != strings.TrimSpace(r.PathValue("name")) {
		writeError(w, http.StatusBadRequest, errors.New("invalid screenshot name"))
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		writeError(w, http.StatusBadRequest, errors.New("unsupported screenshot type"))
		return
	}
	dir := filepath.Join(s.cfg.Workspaces.DefaultRoot, ".sparkclaw", "screenshots")
	path := filepath.Join(dir, name)
	cleanDir, err := filepath.Abs(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !strings.HasPrefix(cleanPath, cleanDir+string(os.PathSeparator)) {
		writeError(w, http.StatusBadRequest, errors.New("invalid screenshot path"))
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, cleanPath)
}

func sanitizeMessageAttachments(attachments []agent.MessageAttachment) []agent.MessageAttachment {
	out := []agent.MessageAttachment{}
	for _, attachment := range attachments {
		clean, ok := cleanWorkspaceRelativePath(attachment.RelPath)
		if !ok {
			continue
		}
		attachment.RelPath = filepath.ToSlash(clean)
		attachment.Name = strings.TrimSpace(attachment.Name)
		if attachment.Name == "" {
			attachment.Name = filepath.Base(clean)
		}
		attachment.ArtifactID = strings.TrimSpace(attachment.ArtifactID)
		attachment.URI = strings.TrimSpace(attachment.URI)
		attachment.ContentType = strings.TrimSpace(attachment.ContentType)
		out = append(out, attachment)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func cleanWorkspaceRelativePath(value string) (string, bool) {
	value = strings.TrimSpace(filepath.ToSlash(value))
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\x00") {
		return "", false
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return clean, true
}

func pathWithinRoot(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (s *Server) refreshTrace(ctx context.Context, runID string) {
	if s.traces == nil || runID == "" {
		return
	}
	run, ok := s.store.GetRun(runID)
	if !ok {
		return
	}
	current := trace.RunTrace{}
	if raw, err := os.ReadFile(filepath.Join(s.cfg.Storage.TraceDir, filepath.Base(runID)+".json")); err == nil {
		_ = json.Unmarshal(raw, &current)
	}
	if current.Episode == nil {
		for _, episode := range s.store.ListEpisodeSummaries(run.SessionID) {
			if episode.RunID == run.ID {
				current.Episode = &episode
				break
			}
		}
	}
	current.Run = run
	current.ModelCalls = s.store.ListModelCalls(run.SessionID, run.ID)
	current.ToolCalls = toolCallsForRun(s.store.ListToolCalls(run.SessionID), run.ID)
	current.Approvals = approvalsForRun(s.store.ListApprovals(""), run.ID)
	current.Feedback = s.store.ListRunFeedback(run.ID)
	current.Messages = s.store.ListMessages(run.SessionID)
	current.Audit = s.store.ListAudit(run.SessionID)
	object, _ := s.traces.WriteRunObject(ctx, current)
	if object != nil {
		s.store.SaveArtifactObject(app.ArtifactObject{
			ID:          app.NewID("obj"),
			Kind:        "trace",
			RunID:       run.ID,
			SessionID:   run.SessionID,
			Backend:     object.Backend,
			Bucket:      object.Bucket,
			Key:         object.Key,
			URI:         object.URI,
			Path:        object.Path,
			ContentType: object.ContentType,
			Bytes:       object.Bytes,
			CreatedAt:   time.Now().UTC(),
		})
	}
}

func toolCallsForRun(calls []app.ToolCall, runID string) []app.ToolCall {
	out := []app.ToolCall{}
	for _, call := range calls {
		if call.RunID == runID {
			out = append(out, call)
		}
	}
	return out
}

func approvalsForRun(approvals []app.Approval, runID string) []app.Approval {
	out := []app.Approval{}
	for _, approval := range approvals {
		if approval.RunID == runID {
			out = append(out, approval)
		}
	}
	return out
}

func modelCallFromChat(sessionID, runID, operation string, chat modelrouter.ChatResult, err error, started, completed time.Time) app.ModelCall {
	status := "completed"
	errorText := ""
	if err != nil {
		status = "failed"
		errorText = err.Error()
	}
	if chat.Lane == "" {
		chat.Lane = "unknown"
	}
	if chat.Profile == "" {
		chat.Profile = "unknown"
	}
	if chat.Model == "" {
		chat.Model = "unknown"
	}
	return app.ModelCall{
		ID:             app.NewID("mcall"),
		SessionID:      sessionID,
		RunID:          runID,
		Lane:           chat.Lane,
		Profile:        chat.Profile,
		Model:          chat.Model,
		Operation:      operation,
		Mock:           chat.Mock,
		Fallback:       chat.Fallback,
		Status:         status,
		PromptTokens:   chat.PromptTokens,
		ResponseTokens: chat.ResponseTokens,
		TotalTokens:    chat.TotalTokens,
		LatencyMS:      completed.Sub(started).Milliseconds(),
		Error:          errorText,
		StartedAt:      started,
		CompletedAt:    &completed,
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authRequired() || s.isPublicRoute(r) {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if s.isPairingBootstrapRequest(r) && got == "" {
			next.ServeHTTP(w, r)
			return
		}
		if got == "" || !s.validBearerToken(got) {
			writeError(w, http.StatusUnauthorized, errors.New("valid bearer token required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isPublicRoute(r) || s.limiter == nil {
			next.ServeHTTP(w, r)
			return
		}
		allowed, retryAfter := s.limiter.allow(rateLimitKey(r))
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retryAfter.Seconds())))))
			writeError(w, http.StatusTooManyRequests, errors.New("rate limit exceeded"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) isPublicRoute(r *http.Request) bool {
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/healthz", "/readyz", "/metrics":
			return true
		}
	}
	return false
}

func (s *Server) authRequired() bool {
	return s.cfg.Gateway.PairingRequired || strings.TrimSpace(s.cfg.Gateway.APIToken) != ""
}

func (s *Server) isPairingBootstrapRequest(r *http.Request) bool {
	if !s.cfg.Gateway.PairingRequired {
		return false
	}
	if r.Method == http.MethodPost && (r.URL.Path == "/api/pairing/start" || r.URL.Path == "/api/pairing/claim") {
		return true
	}
	return false
}

func (s *Server) validBearerToken(token string) bool {
	if configured := strings.TrimSpace(s.cfg.Gateway.APIToken); configured != "" {
		if subtle.ConstantTimeCompare([]byte(token), []byte(configured)) == 1 {
			return true
		}
	}
	client, ok := s.store.FindClientByTokenHash(hashSecret(token))
	if !ok {
		return false
	}
	s.store.TouchClient(client.ID)
	return true
}

func readJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}

func queryInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func queryOwnerID(r *http.Request) string {
	ownerID := strings.TrimSpace(r.URL.Query().Get("owner_id"))
	if ownerID == "" {
		return app.DefaultOwnerID
	}
	return ownerID
}

func sessionOwnerID(session app.Session) string {
	if strings.TrimSpace(session.OwnerID) == "" {
		return app.DefaultOwnerID
	}
	return strings.TrimSpace(session.OwnerID)
}

func (s *Server) sessionIDVisibleToOwner(sessionID, ownerID string) bool {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ownerID == app.DefaultOwnerID
	}
	session, ok := s.store.GetSession(sessionID)
	if !ok {
		return ownerID == app.DefaultOwnerID
	}
	return sessionOwnerID(session) == ownerID
}

func (s *Server) artifactVisibleToOwner(object app.ArtifactObject, ownerID string) bool {
	return s.sessionIDVisibleToOwner(object.SessionID, ownerID)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeSSEEvent(w http.ResponseWriter, event app.Event) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %s\n", event.ID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event.Type); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		return err
	}
	return nil
}

func writeNamedSSE(w http.ResponseWriter, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", name); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		return err
	}
	return nil
}

func lastEventID(events []app.Event) string {
	if len(events) == 0 {
		return ""
	}
	return events[len(events)-1].ID
}

func streamVisibleEvent(typ string) bool {
	return strings.HasPrefix(typ, "tool_call.") || strings.HasPrefix(typ, "approval.")
}

func isLocalRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		host = strings.Split(forwarded, ",")[0]
	}
	if parsed := strings.TrimSpace(host); parsed != "" {
		if h, _, err := net.SplitHostPort(parsed); err == nil {
			host = h
		} else {
			host = parsed
		}
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1" || host == "localhost" || host == ""
}

func randomSecret(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(secret)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func boolMetric(value bool) int {
	if value {
		return 1
	}
	return 0
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func modelMode(cfg config.Config) string {
	if cfg.Model.Mock {
		return "mock"
	}
	return "external"
}

func artifactBackend(cfg config.Config) string {
	return strings.ToLower(strings.TrimSpace(cfg.Storage.ArtifactBackend))
}

func stateDSNStatus(cfg config.Config) string {
	if cfg.State.Backend != "postgres" {
		return ""
	}
	if strings.TrimSpace(cfg.State.DSN) == "" {
		return "missing"
	}
	return "configured"
}

func publicGatewayConfig(cfg config.GatewayConfig) config.GatewayConfig {
	cfg.APIToken = ""
	return cfg
}

func publicModelConfig(cfg config.ModelConfig) map[string]any {
	return map[string]any{
		"mock":                 cfg.Mock,
		"http_timeout_seconds": cfg.HTTPTimeoutSeconds,
		"disable_thinking":     cfg.DisableThinking,
		"fast":                 publicModelProfile(cfg.Fast),
		"deep":                 publicModelProfile(cfg.Deep),
		"embedding":            publicModelProfile(cfg.Embedding),
		"reranker":             publicModelProfile(cfg.Reranker),
		"guard":                publicModelProfile(cfg.Guard),
	}
}

func publicModelProfile(profile config.ModelProfile) map[string]any {
	return map[string]any{
		"name":           profile.Name,
		"base_url":       profile.BaseURL,
		"model":          profile.Model,
		"context_tokens": profile.ContextTokens,
		"mtp":            profile.MTP,
		"max_tokens":     profile.MaxTokens,
	}
}

func publicSpeechConfig(cfg config.SpeechConfig) map[string]any {
	return map[string]any{
		"enabled":           cfg.Enabled,
		"backend":           cfg.Backend,
		"model":             cfg.Model,
		"default_language":  cfg.DefaultLanguage,
		"max_audio_seconds": cfg.MaxAudioSeconds,
		"max_upload_bytes":  cfg.MaxUploadBytes,
		"retain_audio":      false,
	}
}

func publicStorageConfig(cfg config.StorageConfig) map[string]any {
	return map[string]any{
		"trace_dir":        cfg.TraceDir,
		"log_dir":          cfg.LogDir,
		"artifact_backend": cfg.ArtifactBackend,
		"artifact_dir":     cfg.ArtifactDir,
		"artifact_bucket":  cfg.ArtifactBucket,
		"s3_endpoint":      cfg.S3Endpoint,
		"s3_region":        cfg.S3Region,
		"s3_access_key":    configuredStatus(cfg.S3AccessKey),
		"s3_secret_key":    configuredStatus(cfg.S3SecretKey),
	}
}

func publicStateConfig(cfg config.StateConfig) map[string]any {
	return map[string]any{
		"backend":             cfg.Backend,
		"path":                cfg.Path,
		"dsn":                 configuredStatus(cfg.DSN),
		"encrypt_at_rest":     cfg.EncryptAtRest,
		"encryption_key":      stateEncryptionStatus(cfg.EncryptionKey),
		"encryption_key_file": stateEncryptionStatus(cfg.EncryptionKeyFile),
	}
}

func publicAdapterConfig(cfg config.AdapterConfig) map[string]any {
	return map[string]any{
		"email": map[string]any{
			"backend":  cfg.Email.Backend,
			"base_url": cfg.Email.BaseURL,
			"token":    configuredStatus(cfg.Email.Token),
		},
		"calendar": map[string]any{
			"backend":  cfg.Calendar.Backend,
			"base_url": cfg.Calendar.BaseURL,
			"token":    configuredStatus(cfg.Calendar.Token),
		},
		"browserAutomation": map[string]any{
			"mcp_command":    cfg.BrowserAutomation.MCPCommand,
			"mcp_args_count": len(cfg.BrowserAutomation.MCPArgs),
			"timeout_ms":     cfg.BrowserAutomation.TimeoutMS,
		},
	}
}

func publicToolsConfig(cfg config.Config) map[string]any {
	webSearch := cfg.Plugins.Entries.Parallel.Config.WebSearch
	notificationChannels := map[string]any{}
	for name, channel := range cfg.Tools.Notifications.Channels {
		notificationChannels[name] = map[string]any{
			"enabled":          channel.Enabled,
			"provider":         channel.Provider,
			"base_url":         channel.BaseURL,
			"token_configured": strings.TrimSpace(channel.Token) != "",
			"recipient_set":    strings.TrimSpace(channel.Recipient) != "",
		}
	}
	return map[string]any{
		"web": map[string]any{
			"search": map[string]any{
				"enabled":            cfg.Tools.Web.Search.Enabled,
				"provider":           cfg.Tools.Web.Search.Provider,
				"base_url":           webSearch.BaseURL,
				"api_key_configured": strings.TrimSpace(webSearch.APIKey) != "",
				"max_results":        webSearch.MaxResults,
			},
		},
		"browserAutomation": map[string]any{
			"enabled":  cfg.Tools.BrowserAutomation.Enabled,
			"provider": cfg.Tools.BrowserAutomation.Provider,
			"profile":  cfg.Tools.BrowserAutomation.Profile,
		},
		"reminders": map[string]any{
			"enabled":         cfg.Tools.Reminders.Enabled,
			"default_channel": cfg.Tools.Reminders.DefaultChannel,
		},
		"notifications": map[string]any{
			"channels": notificationChannels,
		},
	}
}

func publicNotificationBindings(bindings []app.NotificationBinding) []map[string]any {
	out := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, publicNotificationBinding(binding))
	}
	return out
}

func publicNotificationBinding(binding app.NotificationBinding) map[string]any {
	return map[string]any{
		"id":                  binding.ID,
		"owner_id":            binding.OwnerID,
		"channel":             binding.Channel,
		"provider":            binding.Provider,
		"status":              binding.Status,
		"display_name":        binding.DisplayName,
		"external_user_id":    redactExternalID(binding.ExternalUserID),
		"account_id":          redactExternalID(binding.AccountID),
		"credential_ref":      configuredStatus(binding.CredentialRef),
		"context_token":       configuredStatus(binding.ContextToken),
		"base_url":            binding.BaseURL,
		"qr_code_url":         binding.QRCodeURL,
		"qr_code_image":       binding.QRCodeImage,
		"default_for_channel": binding.DefaultForChannel,
		"scopes":              binding.Scopes,
		"created_at":          binding.CreatedAt,
		"updated_at":          binding.UpdatedAt,
		"expires_at":          binding.ExpiresAt,
		"revoked_at":          binding.RevokedAt,
		"last_error":          binding.LastError,
	}
}

func redactExternalID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 6 {
		return value
	}
	return string(runes[:3]) + "***" + string(runes[len(runes)-2:])
}

func configuredStatus(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "configured"
}

func stateEncryptionStatus(value string) string {
	if strings.TrimSpace(value) == "" {
		return "missing"
	}
	return "configured"
}

func publicRateLimitConfig(cfg config.RateLimitConfig) map[string]any {
	return map[string]any{
		"enabled":             cfg.Enabled,
		"requests_per_minute": cfg.RequestsPerMinute,
		"burst":               cfg.Burst,
	}
}

func toolPolicySummary(security config.SecurityConfig, defs []app.ToolDefinition) map[string]any {
	riskCounts := map[string]int{}
	approvalRequired := []string{}
	for _, def := range defs {
		riskCounts[string(def.Risk)]++
		if def.RequiresApproval {
			approvalRequired = append(approvalRequired, def.Name)
		}
	}
	slices.Sort(approvalRequired)
	return map[string]any{
		"policy_path":                           security.ToolPolicyPath,
		"external_content_untrusted":            security.ExternalContentUntrusted,
		"approval_required_for_dangerous_tools": security.ApprovalRequiredForDangerousTools,
		"sandbox_required_for_mutating_tools":   security.SandboxRequiredForMutatingTools,
		"dangerous_tools_deep_verification":     security.DangerousToolsRequireDeepVerification,
		"definition_count":                      len(defs),
		"risk_counts":                           riskCounts,
		"definition_approval_required_tools":    approvalRequired,
		"configured_approval_required_tools":    security.ApprovalRequiredTools,
		"denied_tools":                          security.DeniedTools,
		"browser_read_allow_hosts":              security.BrowserReadAllowHosts,
	}
}

var policyToolNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var ownerEmailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type rateLimiter struct {
	mu       sync.Mutex
	enabled  bool
	rate     float64
	burst    float64
	buckets  map[string]rateLimitBucket
	rejected int
}

type rateLimitBucket struct {
	Tokens     float64
	LastRefill time.Time
}

func newRateLimiter(cfg config.RateLimitConfig) *rateLimiter {
	if !cfg.Enabled || cfg.RequestsPerMinute <= 0 || cfg.Burst <= 0 {
		return nil
	}
	return &rateLimiter{
		enabled: true,
		rate:    float64(cfg.RequestsPerMinute) / 60,
		burst:   float64(cfg.Burst),
		buckets: map[string]rateLimitBucket{},
	}
}

func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	if l == nil || !l.enabled {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	bucket := l.buckets[key]
	if bucket.LastRefill.IsZero() {
		bucket = rateLimitBucket{Tokens: l.burst, LastRefill: now}
	} else {
		elapsed := now.Sub(bucket.LastRefill).Seconds()
		bucket.Tokens = math.Min(l.burst, bucket.Tokens+elapsed*l.rate)
		bucket.LastRefill = now
	}
	if bucket.Tokens >= 1 {
		bucket.Tokens--
		l.buckets[key] = bucket
		return true, 0
	}
	l.rejected++
	l.buckets[key] = bucket
	waitSeconds := (1 - bucket.Tokens) / l.rate
	return false, time.Duration(math.Ceil(waitSeconds*1000)) * time.Millisecond
}

func (l *rateLimiter) rejectedCount() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rejected
}

func rateLimitKey(r *http.Request) string {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token != "" {
		return "token:" + hashSecret(token)
	}
	host := r.RemoteAddr
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		host = strings.Split(forwarded, ",")[0]
	}
	if parsed := strings.TrimSpace(host); parsed != "" {
		if h, _, err := net.SplitHostPort(parsed); err == nil {
			host = h
		} else {
			host = parsed
		}
	}
	return "remote:" + strings.Trim(host, "[]")
}

func normalizeOwnerProfileInput(current app.OwnerProfile, displayName, email string, preferences map[string]string) (app.OwnerProfile, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return app.OwnerProfile{}, errors.New("display_name is required")
	}
	if len([]rune(displayName)) > 80 {
		return app.OwnerProfile{}, errors.New("display_name must be 80 characters or fewer")
	}
	email = strings.TrimSpace(email)
	if len(email) > 254 {
		return app.OwnerProfile{}, errors.New("email must be 254 characters or fewer")
	}
	if email != "" && !ownerEmailPattern.MatchString(email) {
		return app.OwnerProfile{}, errors.New("email must be a valid address")
	}
	normalizedPreferences := map[string]string{}
	for key, value := range preferences {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return app.OwnerProfile{}, errors.New("preference keys must be non-empty")
		}
		if strings.HasPrefix(key, "_") {
			return app.OwnerProfile{}, errors.New("preference keys must not start with underscore")
		}
		if len([]rune(key)) > 80 {
			return app.OwnerProfile{}, errors.New("preference keys must be 80 characters or fewer")
		}
		if len([]rune(value)) > 500 {
			return app.OwnerProfile{}, errors.New("preference values must be 500 characters or fewer")
		}
		normalizedPreferences[key] = value
	}
	if len(normalizedPreferences) > 50 {
		return app.OwnerProfile{}, errors.New("preferences must include 50 entries or fewer")
	}
	if current.ID == "" {
		current = app.DefaultOwnerProfile()
	}
	current.DisplayName = displayName
	current.Email = email
	current.Preferences = normalizedPreferences
	return current, nil
}

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

func cleanUploadFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, string(os.PathSeparator), "_")
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
	name = strings.Trim(name, "._-")
	if len(name) > 120 {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		if len(ext) > 20 {
			ext = ""
		}
		if len(base) > 100 {
			base = base[:100]
		}
		name = base + ext
	}
	return name
}

func ensureUploadFilenameExtension(name, contentType, detectedContentType string) string {
	if filepath.Ext(name) != "" {
		return name
	}
	ext := extensionForUploadContentType(contentType)
	if ext == "" {
		ext = extensionForUploadContentType(detectedContentType)
	}
	if ext == "" {
		return name
	}
	return name + ext
}

func extensionForUploadContentType(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch contentType {
	case "application/pdf":
		return ".pdf"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "text/plain":
		return ".txt"
	case "text/markdown", "text/x-markdown":
		return ".md"
	case "text/csv":
		return ".csv"
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}
