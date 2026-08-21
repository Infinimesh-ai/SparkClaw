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
	"log/slog"
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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpintegration"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

const (
	sseHeartbeatInterval = 15 * time.Second
)

type streamMessageExecutor func(context.Context, string, string, []agent.MessageAttachment, app.MessageIngressContext, agent.StreamHandler) (agent.Result, error)

type Server struct {
	cfg                      config.Config
	store                    store.Store
	tools                    *toolhub.ToolHub
	runtime                  agent.Runtime
	models                   modelrouter.Router
	traces                   *trace.Writer
	artifacts                artifact.Store
	policies                 policy.Engine
	speech                   speech.Transcriber
	speechRealtimeMu         sync.Mutex
	speechRealtimeTickets    map[string]*speechRealtimeTicket
	speechRealtimeTicketIDs  map[string]string
	managedBrowserWindows    ManagedBrowserWindowController
	delivery                 *delivery.Gateway
	endpoints                *messagecontrol.EndpointRegistry
	providers                *delivery.ProviderRegistry
	connectors               ConnectorController
	mcp                      MCPController
	mcpAccess                *mcpaccess.Service
	iscpPairing              *iscppairing.Service
	externalApprovalResolver ExternalApprovalResolver
	bridge                   *iscpbridge.GatewayAdapter
	deliveryMu               sync.Mutex
	mux                      *http.ServeMux
	started                  time.Time
	limiter                  *rateLimiter
	lifecycleMu              sync.RWMutex
	lifecycleCtx             context.Context
	passiveStreamMu          sync.Mutex
	passiveStreams           map[string]int
	streamMessage            streamMessageExecutor
	streamWG                 sync.WaitGroup
	approvalLocks            sync.Map
	pairing                  *pairingCoordinator
}

func (s *Server) addAudit(ctx context.Context, event app.AuditEvent) {
	if err := s.store.AddAudit(context.WithoutCancel(ctx), event); err != nil {
		slog.Warn("gateway audit unavailable", "type", event.Type, "run_id", event.RunID, "code", store.StoreErrorCodeOf(err))
	}
}

type Option func(*Server)

type ConnectorController interface {
	Enabled(ownerID, channel string) bool
	ListStatus(context.Context, string) ([]app.ConnectorStatus, error)
	Status(context.Context, string, string) (app.ConnectorStatus, error)
	SetEnabled(ctx context.Context, ownerID, actorID, channel string, enabled bool, expectedVersion int64) (app.ConnectorStatus, error)
	SetMCPTransports(ctx context.Context, ownerID, actorID string, iscpEnabled, lanAccessEnabled bool, expectedVersion int64) (app.ConnectorStatus, error)
	StartNotificationBinding(context.Context, app.NotificationBinding, binding.StartOptions) (app.NotificationBinding, error)
	PollNotificationBinding(context.Context, string) (app.NotificationBinding, error)
	RevokeNotificationBinding(context.Context, string) (app.NotificationBinding, error)
}

type MCPController interface {
	ListStatus() []mcpintegration.Status
	Refresh(context.Context, string) (mcpintegration.Status, error)
}

type ExternalApprovalResolver interface {
	Resolve(context.Context, app.Approval, string) (resolvedElsewhere bool, err error)
}

type ManagedBrowserWindowController interface {
	OpenManagedBrowserWindow(context.Context, string, string, string, time.Time) error
	CloseManagedBrowserWindow(context.Context, string, string) error
}

func WithConnectorController(controller ConnectorController) Option {
	return func(server *Server) {
		server.connectors = controller
	}
}

func WithMCPController(controller MCPController) Option {
	return func(server *Server) {
		server.mcp = controller
	}
}

func WithISCPPairing(service *iscppairing.Service) Option {
	return func(server *Server) {
		server.iscpPairing = service
	}
}

func WithExternalApprovalResolver(resolver ExternalApprovalResolver) Option {
	return func(server *Server) {
		server.externalApprovalResolver = resolver
	}
}

func WithSpeechTranscriber(transcriber speech.Transcriber) Option {
	return func(server *Server) {
		if transcriber != nil {
			server.speech = transcriber
		}
	}
}

func WithManagedBrowserWindows(controller ManagedBrowserWindowController) Option {
	return func(server *Server) {
		server.managedBrowserWindows = controller
	}
}

func WithMessageDelivery(endpoints *messagecontrol.EndpointRegistry, providers *delivery.ProviderRegistry, gateway *delivery.Gateway) Option {
	return func(server *Server) {
		server.endpoints = endpoints
		server.providers = providers
		server.delivery = gateway
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
		cfg:                     cfg,
		store:                   st,
		tools:                   tools,
		runtime:                 runtime,
		models:                  modelrouter.New(cfg),
		traces:                  traces,
		artifacts:               artifacts,
		policies:                policy.New(cfg),
		speech:                  speech.NewDisabled(cfg.Speech),
		mux:                     http.NewServeMux(),
		started:                 time.Now().UTC(),
		limiter:                 newRateLimiter(cfg.Gateway.RateLimit),
		lifecycleCtx:            context.Background(),
		passiveStreams:          map[string]int{},
		speechRealtimeTickets:   map[string]*speechRealtimeTicket{},
		speechRealtimeTicketIDs: map[string]string{},
		pairing:                 newPairingCoordinator(),
	}
	s.streamMessage = func(ctx context.Context, sessionID, content string, attachments []agent.MessageAttachment, ingress app.MessageIngressContext, emit agent.StreamHandler) (agent.Result, error) {
		return s.runtime.HandleMessageStreamWithIngress(ctx, sessionID, content, attachments, ingress, emit)
	}
	for _, option := range options {
		option(s)
	}
	s.bridge = iscpbridge.NewGatewayAdapter(st, func() iscpbridge.AgentRuntime { return s.runtime })
	s.bridge.ConfigureNotificationRetention(cfg.PassiveNotifications.MaxPerOwner, cfg.PassiveNotifications.RetentionDays)
	s.mcpAccess = mcpaccess.New(st, s.runtime, func(ctx context.Context, result agent.Result) error {
		if s.endpoints == nil || s.delivery == nil {
			return errors.New("MCP result delivery is unavailable")
		}
		_, err := s.deliverAgentResult(ctx, result)
		return err
	})
	s.mcpAccess.WithExecutionContext(s.executionContext)
	if s.iscpPairing == nil {
		s.iscpPairing = iscppairing.New(st, iscppairing.Options{
			Enabled: cfg.ISCPPairing.Enabled, DomainID: cfg.ISCPPairing.DomainID,
			ExpectedTicketType: cfg.ISCPPairing.ExpectedTicketType,
		})
	}
	if s.connectors != nil {
		s.mcpAccess.WithChannelEnabled(func(ownerID string) bool {
			return s.connectors.Enabled(ownerID, "mcp")
		})
	}
	s.applyMemoryRetention()
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.withCORS(s.withRateLimit(s.withAuth(s.mux)))
}

func (s *Server) Addr() string {
	return fmt.Sprintf("%s:%d", s.cfg.Gateway.Bind, s.cfg.Gateway.Port)
}

func (s *Server) BindLifecycleContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.lifecycleMu.Lock()
	s.lifecycleCtx = ctx
	s.lifecycleMu.Unlock()
}

func (s *Server) executionContext() context.Context {
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	return s.lifecycleCtx
}

// detachedExecutionGraceSeconds covers the gap between the agent's own
// run-budget check and the moment an in-flight model or tool request
// actually returns: the budget is only consulted between requests.
const detachedExecutionGraceSeconds = 60

// detachedExecutionContext bounds work that outlives its HTTP request. The
// agent's run budget is the graceful stop; this deadline is the hard backstop
// so a client-disconnected execution can never ride the process lifetime
// context indefinitely.
func (s *Server) detachedExecutionContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.executionContext(), detachedExecutionTimeout(s.cfg))
}

func detachedExecutionTimeout(cfg config.Config) time.Duration {
	runSeconds := cfg.Runtime.RunMaxDurationSeconds
	if runSeconds <= 0 {
		runSeconds = config.Default().Runtime.RunMaxDurationSeconds
	}
	modelSeconds := cfg.Model.HTTPTimeoutSeconds
	if modelSeconds <= 0 {
		modelSeconds = config.Default().Model.HTTPTimeoutSeconds
	}
	return time.Duration(runSeconds+modelSeconds+detachedExecutionGraceSeconds) * time.Second
}

func (s *Server) WaitForBackgroundWork(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.streamWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	s.mux.HandleFunc("GET /api/notifications", s.listPassiveNotifications)
	s.mux.HandleFunc("POST /api/notifications/read-all", s.markAllPassiveNotificationsRead)
	s.mux.HandleFunc("POST /api/notifications/{id}/read", s.markPassiveNotificationRead)
	s.mux.HandleFunc("GET /api/notifications/events/stream", s.streamPassiveNotifications)
	s.mux.HandleFunc("GET /api/connectors", s.listConnectors)
	s.mux.HandleFunc("PATCH /api/connectors/{channel}", s.updateConnector)
	s.mux.HandleFunc("GET /api/mcp-servers", s.listMCPServers)
	s.mux.HandleFunc("POST /api/mcp-servers/{name}/refresh", s.refreshMCPServer)
	s.mux.HandleFunc("POST /api/notification-bindings/{channel}/start", s.startNotificationBinding)
	s.mux.HandleFunc("GET /api/notification-bindings/{id}", s.getNotificationBinding)
	s.mux.HandleFunc("POST /api/notification-bindings/{id}/poll", s.pollNotificationBinding)
	s.mux.HandleFunc("POST /api/notification-bindings/{id}/browser", s.openNotificationBindingBrowser)
	s.mux.HandleFunc("DELETE /api/notification-bindings/{id}", s.revokeNotificationBinding)
	s.mux.HandleFunc("GET /api/delivery-endpoints", s.listDeliveryEndpoints)
	s.mux.HandleFunc("GET /api/deliveries", s.listDeliveries)
	s.mux.HandleFunc("POST /api/deliveries", s.createDelivery)
	s.mux.HandleFunc("GET /api/deliveries/{id}", s.getDelivery)
	s.mux.HandleFunc("POST /api/deliveries/{id}/retry", s.retryDelivery)
	s.mux.HandleFunc("GET /api/message-history", s.listMessageHistory)
	s.mux.HandleFunc("GET /api/schedules", s.listCurrentSchedules)
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
	s.mux.HandleFunc("GET /api/jingsi/v0/readyz", s.readyJingSiLAN)
	s.mux.HandleFunc("POST /api/messages/stream", s.postJingSiMessageStream)
	s.mux.HandleFunc("GET /api/client-events/v0/head", s.headJingSiEvents)
	s.mux.HandleFunc("GET /api/client-events/v0", s.listJingSiEvents)
	s.mux.HandleFunc("GET /api/client-events/v0/stream", s.streamJingSiEvents)
	s.mux.HandleFunc("POST /api/bridge/v1/dispatch", s.dispatchBridgeRequest)
	s.mux.HandleFunc("POST /api/bridge/v1/mcp/dispatch", s.dispatchMCPBridgeRequest)
	s.mux.HandleFunc("POST /mcp", s.dispatchLANDirectMCP)
	s.mux.HandleFunc("PATCH /api/mcp-access/transports", s.updateMCPTransports)
	s.mux.HandleFunc("GET /api/mcp-access/tickets", s.listMCPAccessTickets)
	s.mux.HandleFunc("GET /api/mcp-access/catalog", s.listMCPAccessCatalog)
	s.mux.HandleFunc("POST /api/mcp-access/tickets", s.issueMCPAccessTicket)
	s.mux.HandleFunc("POST /api/mcp-access/tickets/{id}/revoke", s.revokeMCPAccessTicket)
	s.mux.HandleFunc("DELETE /api/mcp-access/tickets/{id}", s.deleteMCPAccessTicket)
	s.mux.HandleFunc("GET /api/mcp-access/bindings", s.listMCPBindings)
	s.mux.HandleFunc("POST /api/mcp-access/bindings/{id}/revoke", s.revokeMCPBinding)
	s.mux.HandleFunc("DELETE /api/mcp-access/bindings/{id}", s.deleteMCPBinding)
	s.mux.HandleFunc("DELETE /api/mcp-access/records", s.deleteMCPAccessRecords)
	s.mux.HandleFunc("GET /api/iscp-pairing/status", s.getISCPPairingStatus)
	s.mux.HandleFunc("GET /api/iscp-pairing/onboardings", s.listISCPOnboardings)
	s.mux.HandleFunc("POST /api/iscp-pairing/start", s.startISCPPairing)
	s.mux.HandleFunc("GET /api/sessions/{id}/model-calls", s.listSessionModelCalls)
	s.mux.HandleFunc("GET /api/sessions/{id}/tool-calls", s.listSessionToolCalls)
	s.mux.HandleFunc("GET /api/sessions/{id}/audit", s.listSessionAudit)
	s.mux.HandleFunc("GET /api/sessions/{id}/episodes", s.listSessionEpisodes)
	s.mux.HandleFunc("GET /api/runs/{id}/feedback", s.listRunFeedback)
	s.mux.HandleFunc("POST /api/runs/{id}/feedback", s.saveRunFeedback)
	s.mux.HandleFunc("GET /api/tools", s.listTools)
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
	s.mux.HandleFunc("POST /api/speech/realtime-sessions", s.postSpeechRealtimeSession)
	s.mux.HandleFunc("DELETE /api/speech/realtime-sessions/{id}", s.deleteSpeechRealtimeSession)
	s.mux.HandleFunc("GET /api/speech/realtime", s.getSpeechRealtime)
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
	sessions, err := s.store.ListSessions(r.Context())
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	messages := 0
	runs := 0
	allModelCalls, err := s.store.ListModelCalls(r.Context(), "", "")
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	allToolCalls, err := s.store.ListToolCalls(r.Context(), "")
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	allEpisodes, err := s.store.ListEpisodeSummaries(r.Context(), "")
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	modelCalls := len(allModelCalls)
	modelErrors := 0
	modelLatencyTotal := int64(0)
	modelTokensTotal := 0
	for _, session := range sessions {
		storedMessages, err := s.store.ListMessages(r.Context(), session.ID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		messages += len(storedMessages)
		storedRuns, err := s.store.ListRuns(r.Context(), session.ID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		runs += len(storedRuns)
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
	approvals, err := s.store.ListApprovals(r.Context(), "")
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
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
		fmt.Sprintf("sparkclaw_tool_calls_total %d", len(allToolCalls)),
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
		fmt.Sprintf("sparkclaw_episode_summaries_total %d", len(allEpisodes)),
	}
	lines = append(lines, s.tools.DocumentMetrics()...)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(strings.Join(lines, "\n") + "\n"))
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	toolsConfig, err := s.publicToolsConfig(r.Context(), principal.OwnerID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"gateway":             publicGatewayConfig(s.cfg.Gateway),
		"model":               publicModelConfig(s.cfg.Model),
		"speech":              publicSpeechConfig(s.cfg.Speech),
		"iscp_pairing":        s.iscpPairing.Status(r.Context()),
		"mcp_servers":         publicMCPServersConfig(s.cfg.MCPServers),
		"workspaces":          s.cfg.Workspaces,
		"security":            s.cfg.Security,
		"sandbox":             s.cfg.Sandbox,
		"storage":             publicStorageConfig(s.cfg.Storage),
		"state":               publicStateConfig(s.cfg.State),
		"adapters":            publicAdapterConfig(s.cfg.Adapters, s.tools.DocumentOCRReadiness()),
		"tools":               toolsConfig,
		"mcp_server_statuses": s.mcpServerStatuses(),
		"memory":              s.cfg.Memory,
		"runtime":             s.cfg.Runtime,
		"tool_policy":         toolPolicySummary(s.cfg.Security, s.tools.Definitions()),
	})
}

func (s *Server) listMCPServers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"servers": s.mcpServerStatuses()})
}

func (s *Server) refreshMCPServer(w http.ResponseWriter, r *http.Request) {
	if s.mcp == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP integration is unavailable"))
		return
	}
	status, err := s.mcp.Refresh(r.Context(), strings.TrimSpace(r.PathValue("name")))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"server": status, "error": status.ErrorCode})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": status})
}

func (s *Server) mcpServerStatuses() []mcpintegration.Status {
	if s.mcp == nil {
		return []mcpintegration.Status{}
	}
	return s.mcp.ListStatus()
}

func (s *Server) getOwnerProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.store.GetOwnerProfile(r.Context())
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
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
	current, err := s.store.GetOwnerProfile(r.Context())
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	profile, err := normalizeOwnerProfileInput(current, input.DisplayName, input.Email, input.Preferences)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.store.UpdateOwnerProfile(r.Context(), profile)
	updated, err = store.ReconcileOwnerProfileWrite(r.Context(), s.store, updated, err)
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) listOwnerProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.ListOwnerProfiles(r.Context())
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (s *Server) getOwnerProfileByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("owner_id"))
	profile, ok, err := s.store.GetOwnerProfileByID(r.Context(), id)
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("profile not found"))
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (s *Server) patchOwnerProfile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("owner_id"))
	current, ok, err := s.store.GetOwnerProfileByID(r.Context(), id)
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
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
	updated, err := s.store.SaveOwnerProfile(r.Context(), profile)
	updated, err = store.ReconcileOwnerProfileWrite(r.Context(), s.store, updated, err)
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func writeOwnerStoreError(w http.ResponseWriter, err error) {
	if store.StoreErrorCodeOf(err) == store.StoreErrorTimeout {
		writeError(w, http.StatusGatewayTimeout, errors.New("owner profile request timed out"))
		return
	}
	writeError(w, http.StatusServiceUnavailable, errors.New("owner profiles are temporarily unavailable"))
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.store.ListClients(r.Context())
	if err != nil {
		if store.StoreErrorCodeOf(err) == store.StoreErrorTimeout {
			writeError(w, http.StatusGatewayTimeout, errors.New("client list request timed out"))
			return
		}
		writeError(w, http.StatusServiceUnavailable, errors.New("clients are temporarily unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": clients})
}

func (s *Server) revokeClient(w http.ResponseWriter, r *http.Request) {
	client, err := s.store.RevokeClient(r.Context(), r.PathValue("id"))
	if err != nil {
		switch store.StoreErrorCodeOf(err) {
		case store.StoreErrorNotFound:
			writeError(w, http.StatusNotFound, errors.New("client not found"))
		case store.StoreErrorTimeout:
			writeError(w, http.StatusGatewayTimeout, errors.New("client revoke request timed out"))
		default:
			writeError(w, http.StatusServiceUnavailable, errors.New("clients are temporarily unavailable"))
		}
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
	result, err := s.models.ChatWithProfile(r.Context(), profile, system, content)
	completed := time.Now().UTC()
	if _, saveErr := s.store.SaveModelCall(r.Context(), modelCallFromChat("", "", "direct_chat", result, err, started, completed)); saveErr != nil {
		slog.Warn("direct chat model call persistence unavailable", "code", store.StoreErrorCodeOf(saveErr))
	}
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
	if err := s.pairing.gate.Acquire(r.Context(), 1); err != nil {
		writeError(w, pairingStoreStatus(err), pairingStorePublicError(err))
		return
	}
	defer s.pairing.gate.Release(1)
	principal := principalForRequest(r)
	fingerprint := pairingStartFingerprint(principal.OwnerID)
	if pending := s.pairing.pending; pending != nil {
		if pending.kind != pairingPendingStart || pending.fingerprint != fingerprint {
			writeError(w, http.StatusConflict, errors.New("another pairing request is pending"))
			return
		}
		response, status, err := s.reconcilePendingStartLocked(r.Context(), pending)
		if err != nil {
			writeError(w, status, err)
			return
		}
		writeJSON(w, status, response)
		return
	}
	code, err := randomSecret(8)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	now := s.pairing.currentTime()
	pairing := app.PairingCode{
		ID:        app.NewID("pair"),
		CodeHash:  hashSecret(code),
		Status:    "pending",
		ExpiresAt: now.Add(5 * time.Minute),
	}
	pending := &pairingPending{
		kind: pairingPendingStart, fingerprint: fingerprint, expiresAt: pairing.ExpiresAt,
		plaintextCode: code, attemptedPairing: pairing,
	}
	s.installPairingPendingLocked(pending)
	saved, err := s.store.SavePairingCode(r.Context(), pairing)
	if saved.ID != "" {
		pending.pairingCandidate = &saved
	}
	if err != nil {
		if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome {
			response, status, reconcileErr := s.reconcilePendingStartLocked(r.Context(), pending)
			if reconcileErr != nil {
				writeError(w, status, reconcileErr)
				return
			}
			writeJSON(w, status, response)
			return
		}
		s.clearPairingPendingLocked()
		writeError(w, pairingStoreStatus(err), pairingStorePublicError(err))
		return
	}
	response, status, err := s.completePendingStartLocked(pending, saved)
	if err != nil {
		writeError(w, status, err)
		return
	}
	writeJSON(w, status, response)
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
		writeError(w, http.StatusBadRequest, errors.New("invalid pairing request"))
		return
	}
	if err := s.pairing.gate.Acquire(r.Context(), 1); err != nil {
		writeError(w, pairingStoreStatus(err), pairingStorePublicError(err))
		return
	}
	defer s.pairing.gate.Release(1)
	pairingID := strings.TrimSpace(input.PairingID)
	clientName := strings.TrimSpace(input.ClientName)
	if clientName == "" {
		clientName = "SparkClaw Client"
	}
	principal := principalForRequest(r)
	submittedHash := hashSecret(input.Code)
	fingerprint := pairingClaimFingerprint(principal.OwnerID, pairingID, submittedHash, clientName)
	if pending := s.pairing.pending; pending != nil {
		if pending.kind != pairingPendingClaim {
			writeError(w, http.StatusConflict, errors.New("another pairing request is pending"))
			return
		}
		if !pairingCodesMatch(pending.submittedCodeHash, submittedHash) {
			writeError(w, http.StatusUnauthorized, errors.New("invalid pairing code"))
			return
		}
		if pending.fingerprint != fingerprint {
			writeError(w, http.StatusConflict, errors.New("another pairing request is pending"))
			return
		}
		response, status, err := s.reconcilePendingClaimLocked(r.Context(), pending)
		if err != nil {
			writeError(w, status, err)
			return
		}
		writeJSON(w, status, response)
		return
	}
	pairing, ok, err := s.store.GetPairingCode(r.Context(), pairingID)
	if err != nil {
		writeError(w, pairingStoreStatus(err), pairingStorePublicError(err))
		return
	}
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("pairing code not found"))
		return
	}
	if pairing.Status != "pending" || !pairing.ExpiresAt.After(s.pairing.currentTime()) {
		writeError(w, http.StatusBadRequest, errors.New("pairing code is not active"))
		return
	}
	if !pairingCodesMatch(pairing.CodeHash, submittedHash) {
		writeError(w, http.StatusUnauthorized, errors.New("invalid pairing code"))
		return
	}
	token, err := randomSecret(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	client := app.Client{
		ID:        app.NewID("client"),
		OwnerID:   principal.OwnerID,
		ActorID:   principal.ActorID,
		Name:      clientName,
		TokenHash: hashSecret(token),
	}
	pending := &pairingPending{
		kind: pairingPendingClaim, fingerprint: fingerprint, expiresAt: pairing.ExpiresAt,
		plaintextToken: token, preClaimPairing: pairing, attemptedClient: client, submittedCodeHash: pairing.CodeHash,
	}
	s.installPairingPendingLocked(pending)
	claimedPairing, claimedClient, err := s.store.ClaimPairingCode(r.Context(), pairing.ID, client)
	if claimedPairing.ID != "" {
		pending.pairingCandidate = &claimedPairing
	}
	if claimedClient.ID != "" {
		pending.clientCandidate = &claimedClient
	}
	if err != nil {
		if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome {
			response, status, reconcileErr := s.reconcilePendingClaimLocked(r.Context(), pending)
			if reconcileErr != nil {
				writeError(w, status, reconcileErr)
				return
			}
			writeJSON(w, status, response)
			return
		}
		s.clearPairingPendingLocked()
		if store.StoreErrorCodeOf(err) == store.StoreErrorConflict || store.StoreErrorCodeOf(err) == store.StoreErrorNotFound {
			writeError(w, http.StatusConflict, errors.New("pairing state changed"))
			return
		}
		writeError(w, pairingStoreStatus(err), pairingStorePublicError(err))
		return
	}
	response, status, err := s.completePendingClaimLocked(pending, claimedPairing, claimedClient)
	if err != nil {
		writeError(w, status, err)
		return
	}
	writeJSON(w, status, response)
}

func (s *Server) listNotificationBindings(w http.ResponseWriter, r *http.Request) {
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	principal := principalForRequest(r)
	visible := []app.NotificationBinding{}
	bindings, err := s.store.ListNotificationBindings(r.Context(), channel, status)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	for _, binding := range bindings {
		actorID := strings.TrimSpace(binding.ActorID)
		if actorID == "" {
			actorID = binding.OwnerID
		}
		if binding.OwnerID == principal.OwnerID && actorID == principal.ActorID {
			visible = append(visible, binding)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bindings": publicNotificationBindings(visible),
	})
}

func (s *Server) listConnectors(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("connector control is unavailable"))
		return
	}
	principal := principalForRequest(r)
	statuses, err := s.connectors.ListStatus(r.Context(), principal.OwnerID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connectors": statuses})
}

func (s *Server) updateConnector(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("connector control is unavailable"))
		return
	}
	channel := strings.ToLower(strings.TrimSpace(r.PathValue("channel")))
	if channel == "" {
		writeError(w, http.StatusBadRequest, errors.New("channel is required"))
		return
	}
	var input struct {
		Enabled         *bool  `json:"enabled"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := readJSON(r, &input); err != nil || input.Enabled == nil || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 {
		writeError(w, http.StatusBadRequest, errors.New("enabled and a non-negative expected_version are required"))
		return
	}
	principal := principalForRequest(r)
	status, err := s.connectors.SetEnabled(r.Context(), principal.OwnerID, principal.ActorID, channel, *input.Enabled, *input.ExpectedVersion)
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

func (s *Server) startNotificationBinding(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("connector control is unavailable"))
		return
	}
	channel := strings.ToLower(strings.TrimSpace(r.PathValue("channel")))
	if channel == "" {
		writeError(w, http.StatusBadRequest, errors.New("channel is required"))
		return
	}
	var input struct {
		DefaultForChannel bool   `json:"default_for_channel"`
		CredentialSecret  string `json:"credential_secret"`
		BotToken          string `json:"bot_token"`
	}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := readJSON(r, &input); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, errors.New("invalid notification binding request"))
			return
		}
	}
	principal := principalForRequest(r)
	requested := app.NotificationBinding{
		OwnerID:           principal.OwnerID,
		ActorID:           principal.ActorID,
		Channel:           channel,
		DefaultForChannel: input.DefaultForChannel,
		Scopes:            app.DefaultMessagingBindingScopes(),
	}
	credentialSecret := strings.TrimSpace(input.CredentialSecret)
	if credentialSecret == "" {
		credentialSecret = input.BotToken
	}
	started, err := s.connectors.StartNotificationBinding(r.Context(), requested, binding.StartOptions{CredentialSecret: credentialSecret})
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, publicNotificationBinding(started, true))
}

func (s *Server) getNotificationBinding(w http.ResponseWriter, r *http.Request) {
	bindingID := strings.TrimSpace(r.PathValue("id"))
	binding, ok, err := s.store.GetNotificationBinding(r.Context(), bindingID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	principal := principalForRequest(r)
	if !ok || binding.OwnerID != principal.OwnerID || firstEndpointActor(binding) != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	writeJSON(w, http.StatusOK, publicNotificationBinding(binding))
}

func (s *Server) pollNotificationBinding(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("connector control is unavailable"))
		return
	}
	bindingID := strings.TrimSpace(r.PathValue("id"))
	current, ok, err := s.store.GetNotificationBinding(r.Context(), bindingID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	principal := principalForRequest(r)
	if !ok || current.OwnerID != principal.OwnerID || firstEndpointActor(current) != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	updated, err := s.connectors.PollNotificationBinding(r.Context(), bindingID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	if !isPendingNotificationBinding(updated.Status) {
		s.closeNotificationBindingBrowser(r.Context(), updated)
	}
	writeJSON(w, http.StatusOK, publicNotificationBinding(updated))
}

func (s *Server) openNotificationBindingBrowser(w http.ResponseWriter, r *http.Request) {
	bindingID := strings.TrimSpace(r.PathValue("id"))
	binding, ok, err := s.store.GetNotificationBinding(r.Context(), bindingID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	principal := principalForRequest(r)
	if !ok || binding.OwnerID != principal.OwnerID || firstEndpointActor(binding) != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	if binding.Channel != "weixin" || !weixinproto.IsQRLoginProvider(binding.Provider) || !isPendingNotificationBinding(binding.Status) ||
		(binding.ExpiresAt != nil && !binding.ExpiresAt.After(time.Now().UTC())) {
		writeError(w, http.StatusConflict, errors.New("notification binding has no pending Weixin login"))
		return
	}
	if !weixinproto.IsQRLoginURL(binding.QRCodeURL) {
		writeError(w, http.StatusBadRequest, errors.New("notification binding has no valid Weixin login URL"))
		return
	}
	if s.managedBrowserWindows == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("managed Chromium is unavailable"))
		return
	}
	bindingExpiresAt := time.Time{}
	if binding.ExpiresAt != nil {
		bindingExpiresAt = *binding.ExpiresAt
	}
	if err := s.managedBrowserWindows.OpenManagedBrowserWindow(r.Context(), binding.OwnerID, binding.ID, binding.QRCodeURL, bindingExpiresAt); err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("open managed Chromium: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opened": true})
}

func isPendingNotificationBinding(status string) bool {
	return status == "waiting_scan" || status == "waiting_confirm"
}

func (s *Server) closeNotificationBindingBrowser(ctx context.Context, binding app.NotificationBinding) {
	if s.managedBrowserWindows == nil || binding.Channel != "weixin" || !weixinproto.IsQRLoginProvider(binding.Provider) {
		return
	}
	if err := s.managedBrowserWindows.CloseManagedBrowserWindow(ctx, binding.OwnerID, binding.ID); err != nil {
		slog.Warn("failed to close managed Weixin login window", "binding_id", binding.ID, "error", err)
	}
}

func (s *Server) revokeNotificationBinding(w http.ResponseWriter, r *http.Request) {
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("connector control is unavailable"))
		return
	}
	bindingID := strings.TrimSpace(r.PathValue("id"))
	binding, ok, err := s.store.GetNotificationBinding(r.Context(), bindingID)
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	principal := principalForRequest(r)
	if !ok || binding.OwnerID != principal.OwnerID || firstEndpointActor(binding) != principal.ActorID {
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
		return
	}
	revoked, err := s.connectors.RevokeNotificationBinding(r.Context(), bindingID)
	if err != nil {
		s.closeNotificationBindingBrowser(r.Context(), binding)
		writeConnectorError(w, err)
		return
	}
	s.closeNotificationBindingBrowser(r.Context(), binding)
	writeJSON(w, http.StatusOK, publicNotificationBinding(revoked))
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	ownerID := queryOwnerID(r)
	sessions := []app.Session{}
	listed, err := s.store.ListSessions(r.Context())
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	for _, session := range listed {
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
	profile, ok, err := s.store.GetOwnerProfileByID(r.Context(), ownerID)
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("profile not found"))
		return
	}
	session, err := s.store.CreateSessionWithScope(r.Context(), input.Title, profile.ID, profile.WorkspaceRoot, "webchat", false)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, ok, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
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
	current, ok, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if ok && current.Source == "mcp" {
		writeError(w, http.StatusConflict, errors.New("MCP conversation titles are managed by the MCP binding"))
		return
	}
	session, err := s.store.UpdateSessionTitle(r.Context(), r.PathValue("id"), input.Title)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	current, ok, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if ok && current.Source == "mcp" {
		writeError(w, http.StatusConflict, errors.New("MCP conversations are managed by the MCP binding"))
		return
	}
	session, err := s.store.DeleteSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	messages, err := s.store.ListMessages(r.Context(), r.PathValue("id"))
	if err != nil {
		writeConversationError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	session, ok, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	if session.Source == "mcp" {
		writeError(w, http.StatusConflict, errors.New("MCP conversations receive requirements through their managed binding"))
		return
	}
	var input webMessageInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(input.Content) == "" && len(input.Attachments) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("content or an attachment is required"))
		return
	}
	var result agent.Result
	if input.Schedule != nil {
		ingress, ingressErr := s.webMessageIngress(r.Context(), r, session, "", input.ClientTimezone)
		if ingressErr != nil {
			writeError(w, http.StatusBadRequest, ingressErr)
			return
		}
		result, err = s.runtime.HandleScheduleActionWithIngress(r.Context(), sessionID, input.Content, input.Schedule.agentAction(), ingress)
	} else {
		ingress, ingressErr := s.webMessageIngress(r.Context(), r, session, input.TargetEndpointID, input.ClientTimezone)
		if ingressErr != nil {
			status := deliveryHTTPStatus(errorCode(ingressErr))
			if input.TargetEndpointID != "" && errorCode(ingressErr) == "" {
				status = http.StatusServiceUnavailable
			}
			writeError(w, status, ingressErr)
			return
		}
		result, err = s.runtime.HandleMessageWithIngress(r.Context(), sessionID, "", "", input.Content, sanitizeMessageAttachments(input.Attachments), ingress)
	}
	if err != nil {
		writeConversationError(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := s.deliverAgentResult(r.Context(), result); err != nil {
		writeConversationError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

type scheduleActionInput struct {
	Operation         app.RouteOperation `json:"operation"`
	ScheduleID        string             `json:"schedule_id"`
	ExpectedUpdatedAt string             `json:"expected_updated_at"`
	Text              *string            `json:"text,omitempty"`
	DueTime           *string            `json:"due_time,omitempty"`
	Timezone          *string            `json:"timezone,omitempty"`
	Recurrence        *string            `json:"recurrence,omitempty"`
}

func (input scheduleActionInput) agentAction() agent.ScheduleAction {
	return agent.ScheduleAction{
		Operation: input.Operation, ScheduleID: input.ScheduleID, ExpectedUpdatedAt: input.ExpectedUpdatedAt,
		Text: input.Text, DueTime: input.DueTime, Timezone: input.Timezone, Recurrence: input.Recurrence,
	}
}

func (s *Server) postMessageStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	session, ok, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	if session.Source == "mcp" {
		writeError(w, http.StatusConflict, errors.New("MCP conversations receive requirements through their managed binding"))
		return
	}
	var input webMessageInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(input.Content) == "" && len(input.Attachments) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("content or an attachment is required"))
		return
	}
	ingress, err := s.webMessageIngress(r.Context(), r, session, input.TargetEndpointID, input.ClientTimezone)
	if err != nil {
		status := deliveryHTTPStatus(errorCode(err))
		if input.TargetEndpointID != "" && errorCode(err) == "" {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, err)
		return
	}
	initialEvents, err := s.store.EventsAfter(r.Context(), sessionID, "")
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	after := lastEventID(initialEvents)
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
	// Event name must stay in sync with MESSAGE_STREAM_STARTED_EVENT in
	// apps/webchat/src/lib/messageStream.ts: the webchat client keys its
	// accepted/not-accepted failure disposition on this exact string.
	if err := send("message.stream.started", map[string]string{"session_id": sessionID}); err != nil {
		return
	}
	attachments := sanitizeMessageAttachments(input.Attachments)

	type streamResult struct {
		result agent.Result
		err    error
		// deliveryErr reports a post-run delivery failure: the run itself
		// finished and its result is persisted, only handing it to the
		// selected external endpoint failed. It must stay separate from err
		// so the client can tell a real delivery failure apart from a benign
		// dropped stream on an accepted run.
		deliveryErr error
	}
	modelEvents := make(chan agent.StreamEvent, 16)
	results := make(chan streamResult, 1)
	executionCtx, finishExecution := s.detachedExecutionContext()
	s.streamWG.Add(1)
	go func() {
		defer s.streamWG.Done()
		defer finishExecution()
		result, err := s.streamMessage(executionCtx, sessionID, input.Content, attachments, ingress, func(event agent.StreamEvent) error {
			select {
			case <-r.Context().Done():
				return nil
			case modelEvents <- event:
				return nil
			}
		})
		var deliveryErr error
		if err == nil {
			_, deliveryErr = s.deliverAgentResult(executionCtx, result)
		}
		results <- streamResult{result: result, err: err, deliveryErr: deliveryErr}
		close(modelEvents)
	}()

	sendRuntimeEvents := func() bool {
		events, err := s.store.EventsAfter(r.Context(), sessionID, after)
		if err != nil {
			slog.Warn("message stream events unavailable", "session_id", sessionID, "code", store.StoreErrorCodeOf(err))
			return false
		}
		for _, event := range events {
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
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer poll.Stop()
	defer heartbeat.Stop()
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
		case <-heartbeat.C:
			if err := writeSSEHeartbeat(w); err != nil {
				return
			}
			flusher.Flush()
		case result := <-results:
			if !sendRuntimeEvents() {
				return
			}
			if result.err != nil {
				_ = send("error", map[string]string{"error": publicConversationError(result.err).Error(), "session_id": sessionID})
				return
			}
			if result.deliveryErr != nil {
				// Event name must stay in sync with
				// MESSAGE_STREAM_DELIVERY_FAILED_EVENT in
				// apps/webchat/src/lib/messageStream.ts: a plain "error"
				// event on an accepted stream would be presented as a benign
				// stream detach, hiding the delivery failure.
				_ = send("message.stream.delivery_failed", map[string]string{"error": publicConversationError(result.deliveryErr).Error(), "session_id": sessionID})
				return
			}
			_ = send("message.stream.final", result.result)
			return
		}
	}
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.EventsAfter(r.Context(), r.PathValue("id"), r.URL.Query().Get("after"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) streamSessionEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	_, ok, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
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
		events, err := s.store.EventsAfter(r.Context(), sessionID, after)
		if err != nil {
			slog.Warn("session events unavailable", "session_id", sessionID, "code", store.StoreErrorCodeOf(err))
			return false
		}
		for _, event := range events {
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
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			if !send() {
				return
			}
		case <-heartbeat.C:
			if err := writeSSEHeartbeat(w); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) listSessionToolCalls(w http.ResponseWriter, r *http.Request) {
	toolCalls, err := s.store.ListToolCalls(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool_calls": toolCalls})
}

func (s *Server) listSessionAudit(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListAudit(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_events": events})
}

func (s *Server) listSessionEpisodes(w http.ResponseWriter, r *http.Request) {
	episodes, err := s.store.ListEpisodeSummaries(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": episodes})
}

func (s *Server) listSessionModelCalls(w http.ResponseWriter, r *http.Request) {
	modelCalls, err := s.store.ListModelCalls(r.Context(), r.PathValue("id"), r.URL.Query().Get("run_id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model_calls": modelCalls})
}

func (s *Server) listRunFeedback(w http.ResponseWriter, r *http.Request) {
	feedback, err := s.store.ListRunFeedback(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"feedback": feedback})
}

func (s *Server) saveRunFeedback(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	run, ok, err := s.store.GetRun(r.Context(), runID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
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
	feedback, err := s.store.SaveRunFeedback(r.Context(), app.RunFeedback{
		SessionID:  run.SessionID,
		RunID:      run.ID,
		MessageID:  strings.TrimSpace(input.MessageID),
		Rating:     rating,
		Note:       input.Note,
		Correction: input.Correction,
	})
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	s.refreshTrace(r.Context(), run.ID)
	writeJSON(w, http.StatusOK, feedback)
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

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	approvals, err := s.store.ListApprovals(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	for index := range approvals {
		presentation, err := s.approvalPresentation(r.Context(), approvals[index])
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		approvals[index].Presentation = presentation
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvals})
}

func (s *Server) approvalPresentation(ctx context.Context, approval app.Approval) (*app.ApprovalPresentation, error) {
	if approval.Tool != app.ToolWorkspaceDataAccess || approval.PolicyContext == nil ||
		approval.PolicyContext.PrincipalClass != app.PolicyPrincipalExternalMCPAI ||
		approval.PolicyContext.ResourceClass != app.PolicyResourceSparkClawWorkspaceData {
		return nil, nil
	}
	locatorsRaw, ok := approval.Arguments["locators"]
	if !ok {
		return nil, nil
	}
	raw, err := json.Marshal(locatorsRaw)
	if err != nil {
		return nil, nil
	}
	var locators []app.MessageMediaLocator
	if err := json.Unmarshal(raw, &locators); err != nil || len(locators) == 0 {
		return nil, nil
	}
	requester := "AI"
	session, ok, err := s.store.GetSession(ctx, approval.SessionID)
	if err != nil {
		return nil, err
	}
	if ok && strings.TrimSpace(session.Title) != "" {
		requester = session.Title
	}
	return &app.ApprovalPresentation{
		Kind:          "external_mcp_workspace_data_access",
		SessionID:     approval.SessionID,
		Requester:     requester,
		Locators:      locators,
		LocatorStatus: "unverified",
		AccessClass:   approval.PolicyContext.AccessClass,
		OutputClass:   approval.PolicyContext.OutputClass,
		ReturnRoute:   approval.PolicyContext.ReturnRoute,
		Scope:         "single_operation",
	}, nil
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
		Plan      *string        `json:"plan"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	unlock := s.lockApproval(r.PathValue("id"))
	defer unlock()
	newArgs := input.Arguments
	if newArgs == nil {
		newArgs = input.Args
	}
	approval, ok, err := s.findApproval(r.Context(), r.PathValue("id"))
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("approval not found"))
		return
	}
	if approval.Status != "pending" {
		writeError(w, http.StatusBadRequest, errors.New("approval already resolved"))
		return
	}
	if approval.Source == app.ApprovalSourceHappyTeamPlan {
		s.modifyHappyPlanApproval(w, r, approval, input.Plan, newArgs, input.Note)
		return
	}
	expectedApproval := approval
	if len(newArgs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("modify requires args or arguments"))
		return
	}
	call, ok, err := s.store.GetToolCall(r.Context(), approval.ToolCallID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("tool call not found"))
		return
	}
	if call.Status != "approval_pending" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("tool call cannot be modified from status %q", call.Status))
		return
	}
	if approval.PolicyContext != nil {
		writeError(w, http.StatusConflict, errors.New("context-bound workspace data approvals cannot be modified; reject and submit a new request"))
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
	persistedCall, saveErr := s.store.SaveToolCall(r.Context(), call)
	persistedCall, saveErr = store.ReconcileToolCallWrite(r.Context(), s.store, persistedCall, saveErr)
	if saveErr != nil {
		writeSessionStoreError(w, saveErr)
		return
	}
	call = persistedCall
	approvalCandidate, approvalErr := s.store.UpdatePendingApproval(r.Context(), store.NewApprovalUpdateWithNote(expectedApproval, approval, input.Note))
	approval, approvalErr = store.ReconcileApprovalWrite(r.Context(), s.store, approvalCandidate, approvalErr)
	if approvalErr != nil {
		writeApprovalStoreError(w, approvalErr)
		return
	}
	s.refreshTrace(r.Context(), approval.RunID)
	writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "tool_call": call})
}

func (s *Server) validateMCPApproval(ctx context.Context, approval app.Approval) error {
	run, ok, err := s.store.GetRun(ctx, approval.RunID)
	if err != nil {
		return err
	}
	if !ok || run.MessageContext == nil || run.MessageContext.MCP == nil {
		return nil
	}
	operation, ok := s.store.GetMCPOperation(run.MessageContext.MCP.OperationID)
	if !ok || operation.Invocation.RunID != run.ID {
		return errors.New("MCP operation is unavailable for this approval")
	}
	switch operation.State {
	case app.MCPOperationApprovalRequired, app.MCPOperationRunning:
		return nil
	default:
		return errors.New("MCP operation is no longer waiting for approval")
	}
}

func (s *Server) modifyHappyPlanApproval(w http.ResponseWriter, r *http.Request, approval app.Approval, plan *string, args map[string]any, note string) {
	if plan == nil || len(args) != 0 {
		writeError(w, http.StatusBadRequest, errors.New("Happy plan modification requires only the plan field"))
		return
	}
	if len(*plan) > app.MaxExternalApprovalPlanBytes {
		writeError(w, http.StatusRequestEntityTooLarge, errors.New("edited Happy plan exceeds the 1 MiB limit"))
		return
	}
	if approval.ExternalContext == nil || approval.ExternalContext.PlanAvailability != app.ExternalPlanAvailable {
		writeError(w, http.StatusConflict, errors.New("Happy task plan is temporarily unavailable; retry after the member machine reconnects"))
		return
	}
	expectedApproval := approval
	contextCopy := *approval.ExternalContext
	contextCopy.Plan = *plan
	contextCopy.PlanEdited = true
	approval.ExternalContext = &contextCopy
	candidate, err := s.store.UpdatePendingApproval(r.Context(), store.NewApprovalUpdateWithNote(expectedApproval, approval, note))
	approval, err = store.ReconcileApprovalWrite(r.Context(), s.store, candidate, err)
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "tool_call": nil})
}

func (s *Server) resolveApproval(w http.ResponseWriter, r *http.Request, status string) {
	var input struct {
		Note string `json:"note"`
	}
	_ = readJSON(r, &input)
	unlock := s.lockApproval(r.PathValue("id"))
	defer unlock()
	approval, ok, err := s.findApproval(r.Context(), r.PathValue("id"))
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("approval not found"))
		return
	}
	if approval.Status != "pending" {
		writeError(w, http.StatusBadRequest, errors.New("approval already resolved"))
		return
	}
	mcpRun := false
	if run, ok, err := s.store.GetRun(r.Context(), approval.RunID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	} else if ok && run.MessageContext != nil && run.MessageContext.MCP != nil {
		mcpRun = true
	}
	if status == "approved" {
		if err := s.validateMCPApproval(r.Context(), approval); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
	}
	if approval.Source == app.ApprovalSourceHappyTeamPlan {
		s.resolveHappyPlanApproval(w, r, approval, status, input.Note)
		return
	}
	candidate, err := s.store.ResolveApproval(r.Context(), approval.ID, status, input.Note)
	approval, err = store.ReconcileApprovalWrite(r.Context(), s.store, candidate, err)
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	if status == "approved" && mcpRun && s.mcpAccess != nil {
		operation, executionCtx, finishExecution, beginErr := s.mcpAccess.StartApprovalExecution(r.Context(), approval.RunID)
		if beginErr != nil {
			s.refreshTrace(r.Context(), approval.RunID)
			writeJSON(w, http.StatusOK, map[string]any{
				"approval": approval, "approval_status": approval.Status, "execution_status": operation.State,
				"execution_error": beginErr.Error(), "tool_call": nil, "workflow_result": nil, "delivery_receipt": nil,
			})
			return
		}
		s.startApprovedMCPExecution(approval, executionCtx, finishExecution)
		s.refreshTrace(r.Context(), approval.RunID)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"approval": approval, "approval_status": approval.Status, "execution_status": operation.State,
			"operation": operation, "tool_call": nil, "workflow_result": nil, "delivery_receipt": nil,
		})
		return
	}
	var call *app.ToolCall
	var workflowResult *app.WorkflowResult
	var deliveryReceipt *app.DeliveryReceipt
	executionStatus := "not_started"
	resumed := false
	if status == "approved" {
		executed, err := s.runtime.ExecuteApprovedToolCall(r.Context(), approval)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		call = &executed
		executionStatus = "succeeded"
		if strings.HasPrefix(executed.Status, "failed") {
			executionStatus = "failed"
		}
		if result, ok, err := s.runtime.ResumeRunAfterApproval(r.Context(), approval.SessionID, approval.RunID); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		} else if ok {
			resumed = true
			workflowResult = result.WorkflowResult
			if workflowResult != nil {
				executionStatus = string(workflowResult.Status)
			}
			if receipt, err := s.deliverAgentResult(r.Context(), result); err != nil {
				writeError(w, http.StatusBadGateway, err)
				return
			} else {
				deliveryReceipt = receipt
			}
		}
	}
	if status == "rejected" {
		if rejected, ok, err := s.store.GetToolCall(r.Context(), approval.ToolCallID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		} else if ok {
			now := time.Now().UTC()
			rejected.Status = "rejected"
			rejected.Error = "owner rejected approval"
			rejected.CompletedAt = &now
			persisted, saveErr := s.store.SaveToolCall(r.Context(), rejected)
			persisted, saveErr = store.ReconcileToolCallWrite(r.Context(), s.store, persisted, saveErr)
			if saveErr != nil {
				writeError(w, http.StatusInternalServerError, saveErr)
				return
			}
			rejected = persisted
			call = &rejected
		}
	}
	if !resumed {
		if err := s.runtime.CompleteRunIfApprovalsResolved(r.Context(), approval.RunID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	if status == "rejected" && mcpRun && s.mcpAccess != nil {
		if err := s.mcpAccess.FailApprovalExecution(r.Context(), approval.RunID, "approval_rejected", "The local owner rejected the pending action"); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		executionStatus = string(app.MCPOperationFailed)
	}
	s.refreshTrace(r.Context(), approval.RunID)
	writeJSON(w, http.StatusOK, map[string]any{
		"approval": approval, "approval_status": approval.Status, "execution_status": executionStatus,
		"tool_call": call, "workflow_result": workflowResult, "delivery_receipt": deliveryReceipt,
	})
}

func (s *Server) startApprovedMCPExecution(approval app.Approval, executionCtx context.Context, finishExecution func()) {
	s.streamWG.Add(1)
	go func() {
		defer s.streamWG.Done()
		defer finishExecution()
		defer s.refreshTrace(executionCtx, approval.RunID)

		executed, err := s.runtime.ExecuteApprovedToolCall(executionCtx, approval)
		if err != nil {
			s.recordMCPApprovalExecutionFailure(executionCtx, approval.RunID, "approval_execution_failed", "Approved tool execution could not be started")
			return
		}
		if err := executionCtx.Err(); err != nil {
			s.failCancelledMCPApprovalExecution(approval.RunID, err)
			return
		}
		if strings.HasPrefix(executed.Status, "failed") {
			s.recordMCPApprovalExecutionFailure(executionCtx, approval.RunID, "approval_tool_failed", "The approved tool execution failed")
			return
		}
		result, resumed, err := s.runtime.ResumeRunAfterApproval(executionCtx, approval.SessionID, approval.RunID)
		if err != nil {
			if executionCtx.Err() != nil {
				s.failCancelledMCPApprovalExecution(approval.RunID, executionCtx.Err())
				return
			}
			s.recordMCPApprovalExecutionFailure(executionCtx, approval.RunID, "workflow_resume_failed", "SparkClaw workflow resume failed after approval")
			return
		}
		if !resumed {
			pending, pendingErr := runHasPendingApproval(executionCtx, s.store, approval.RunID)
			if pendingErr != nil {
				slog.Error("failed to load pending MCP approvals", "run_id", approval.RunID, "code", store.StoreErrorCodeOf(pendingErr))
				if err := s.mcpAccess.RestoreApprovalRequired(executionCtx, approval.RunID); err != nil {
					slog.Error("failed to restore MCP approval-required state", "run_id", approval.RunID, "error", err)
				}
				return
			}
			if pending {
				if err := s.mcpAccess.RestoreApprovalRequired(executionCtx, approval.RunID); err != nil {
					slog.Error("failed to restore MCP approval-required state", "run_id", approval.RunID, "error", err)
				}
				return
			}
			if err := s.runtime.CompleteRunIfApprovalsResolved(executionCtx, approval.RunID); err != nil {
				slog.Error("failed to complete MCP run after approval", "run_id", approval.RunID, "error", err)
			}
			s.recordMCPApprovalExecutionFailure(executionCtx, approval.RunID, "workflow_resume_unavailable", "SparkClaw workflow could not continue after approval")
			return
		}
		if err := executionCtx.Err(); err != nil {
			s.failCancelledMCPApprovalExecution(approval.RunID, err)
			return
		}
		if _, err := s.deliverAgentResult(executionCtx, result); err != nil {
			if executionCtx.Err() != nil {
				s.failCancelledMCPApprovalExecution(approval.RunID, executionCtx.Err())
				return
			}
			s.recordMCPApprovalExecutionFailure(executionCtx, approval.RunID, "delivery_failed", "Approved MCP result delivery failed")
			return
		}
		if err := s.mcpAccess.RecordWorkflowResult(executionCtx, result); err != nil {
			slog.Error("failed to record MCP workflow result", "run_id", approval.RunID, "error", err)
		}
	}()
}

func (s *Server) failCancelledMCPApprovalExecution(runID string, err error) {
	code := "approval_execution_cancelled"
	message := "Approved MCP execution was cancelled"
	if errors.Is(err, context.DeadlineExceeded) {
		code = "approval_execution_expired"
		message = "Approved MCP execution exceeded the operation deadline"
	}
	s.recordMCPApprovalExecutionFailure(context.WithoutCancel(s.lifecycleCtx), runID, code, message)
}

func (s *Server) recordMCPApprovalExecutionFailure(ctx context.Context, runID, code, message string) {
	if err := s.mcpAccess.FailApprovalExecution(context.WithoutCancel(ctx), runID, code, message); err != nil {
		slog.Error("failed to persist MCP approval execution failure", "run_id", runID, "code", code, "error", err)
	}
}

func runHasPendingApproval(ctx context.Context, st store.Store, runID string) (bool, error) {
	approvals, err := st.ListApprovals(ctx, "pending")
	if err != nil {
		return false, err
	}
	for _, approval := range approvals {
		if approval.RunID == runID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) resolveHappyPlanApproval(w http.ResponseWriter, r *http.Request, approval app.Approval, status, note string) {
	if s.externalApprovalResolver == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("Happy approval integration is unavailable"))
		return
	}
	resolvedElsewhere, err := s.externalApprovalResolver.Resolve(r.Context(), approval, status)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	localStatus := status
	if resolvedElsewhere {
		localStatus = "resolved_elsewhere"
		if strings.TrimSpace(note) == "" {
			note = "Happy task was already resolved elsewhere"
		}
	}
	candidate, err := s.store.ResolveApproval(r.Context(), approval.ID, localStatus, note)
	resolved, err := store.ReconcileApprovalWrite(r.Context(), s.store, candidate, err)
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approval": resolved, "tool_call": nil, "workflow_result": nil, "delivery_receipt": nil,
	})
}

func (s *Server) findApproval(ctx context.Context, id string) (app.Approval, bool, error) {
	return s.store.GetApproval(ctx, id)
}

func (s *Server) lockApproval(id string) func() {
	value, _ := s.approvalLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
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
	export, err := s.buildMemoryExport(r.Context())
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, export)
}

func (s *Server) archiveMemoryExport(w http.ResponseWriter, r *http.Request) {
	s.applyMemoryRetention()
	export, err := s.buildMemoryExport(r.Context())
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
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
	s.addAudit(r.Context(), app.AuditEvent{
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

func (s *Server) buildMemoryExport(ctx context.Context) (app.MemoryExport, error) {
	s.applyMemoryRetention()
	candidates := s.store.ListMemoryCandidates("")
	pending := 0
	for _, candidate := range candidates {
		if candidate.Status == "pending" {
			pending++
		}
	}
	memories := s.store.SearchMemories("")
	episodes, err := s.store.ListEpisodeSummaries(ctx, "")
	if err != nil {
		return app.MemoryExport{}, err
	}
	ownerProfile, err := s.store.GetOwnerProfile(ctx)
	if err != nil {
		return app.MemoryExport{}, err
	}
	return app.MemoryExport{
		GeneratedAt:      time.Now().UTC(),
		OwnerProfile:     ownerProfile,
		Memories:         memories,
		MemoryCandidates: candidates,
		Episodes:         episodes,
		Counts: app.MemoryExportCounts{
			Memories:          len(memories),
			MemoryCandidates:  len(candidates),
			PendingCandidates: pending,
			Episodes:          len(episodes),
		},
	}, nil
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
		visible, err := s.sessionIDVisibleToOwner(r.Context(), candidate.SessionID, ownerID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		if visible {
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
	runs, err := s.store.ListRuns(r.Context(), "")
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	storedApprovals, err := s.store.ListApprovals(r.Context(), "")
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	out := make([]app.TraceMetadata, 0, min(limit, len(runs)))
	for _, run := range runs {
		if len(out) >= limit {
			break
		}
		storedToolCalls, err := s.store.ListToolCalls(r.Context(), run.SessionID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		toolCalls := toolCallsForRun(storedToolCalls, run.ID)
		approvals := approvalsForRun(storedApprovals, run.ID)
		modelCalls, err := s.store.ListModelCalls(r.Context(), run.SessionID, run.ID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		messages, err := s.store.ListMessages(r.Context(), run.SessionID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		meta := app.TraceMetadata{
			RunID:          run.ID,
			SessionID:      run.SessionID,
			State:          run.State,
			Risk:           run.Risk,
			ModelLane:      run.ModelLane,
			Summary:        run.Summary,
			StartedAt:      run.StartedAt,
			CompletedAt:    run.CompletedAt,
			MessageCount:   len(messages),
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
		visible, err := s.artifactVisibleToOwner(r.Context(), object, ownerID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		if visible {
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
	workspaceRoot, err := s.workspaceRootForSession(r.Context(), sessionID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
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
	s.addAudit(r.Context(), app.AuditEvent{
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
	workspaceRoot, err := s.workspaceRootForSession(r.Context(), strings.TrimSpace(r.URL.Query().Get("session_id")))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
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
	workspaceRoot, err := s.workspaceRootForSession(r.Context(), strings.TrimSpace(r.URL.Query().Get("session_id")))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	path := filepath.Join(workspaceRoot, clean)
	if !pathWithinRoot(workspaceRoot, path) {
		writeError(w, http.StatusBadRequest, errors.New("document path escapes workspace"))
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, path)
}

func (s *Server) workspaceRootForSession(ctx context.Context, sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) != "" {
		session, ok, err := s.store.GetSession(ctx, sessionID)
		if err != nil {
			return "", err
		}
		if ok && strings.TrimSpace(session.WorkspaceRoot) != "" {
			return strings.TrimSpace(session.WorkspaceRoot), nil
		}
	}
	return strings.TrimSpace(s.cfg.Workspaces.DefaultRoot), nil
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
	run, ok, err := s.store.GetRun(ctx, runID)
	if err != nil {
		slog.Warn("trace run refresh unavailable", "run_id", runID, "code", store.StoreErrorCodeOf(err))
		return
	}
	if !ok {
		return
	}
	current := trace.RunTrace{}
	if raw, err := os.ReadFile(filepath.Join(s.cfg.Storage.TraceDir, filepath.Base(runID)+".json")); err == nil {
		_ = json.Unmarshal(raw, &current)
	}
	if current.Episode == nil {
		episodes, err := s.store.ListEpisodeSummaries(ctx, run.SessionID)
		if err != nil {
			slog.Warn("trace episode refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
			return
		}
		for _, episode := range episodes {
			if episode.RunID == run.ID {
				current.Episode = &episode
				break
			}
		}
	}
	current.Run = run
	current.ModelCalls, err = s.store.ListModelCalls(ctx, run.SessionID, run.ID)
	if err != nil {
		slog.Warn("trace model call refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	storedToolCalls, err := s.store.ListToolCalls(ctx, run.SessionID)
	if err != nil {
		slog.Warn("trace tool call refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	current.ToolCalls = toolCallsForRun(storedToolCalls, run.ID)
	storedApprovals, err := s.store.ListApprovals(ctx, "")
	if err != nil {
		slog.Warn("trace approval refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	current.Approvals = approvalsForRun(storedApprovals, run.ID)
	current.Feedback, err = s.store.ListRunFeedback(ctx, run.ID)
	if err != nil {
		slog.Warn("trace feedback refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	messages, err := s.store.ListMessages(ctx, run.SessionID)
	if err != nil {
		slog.Warn("trace message refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
	current.Messages = messages
	current.Audit, err = s.store.ListAudit(ctx, run.SessionID)
	if err != nil {
		slog.Warn("trace audit refresh unavailable", "run_id", run.ID, "code", store.StoreErrorCodeOf(err))
		return
	}
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

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			// DNS-rebinding defense for the LAN MCP endpoint: a browser
			// context always sends an Origin header, which must match the
			// gateway's own loopback/bind origins or the operator allowlist
			// (mcp_access.allowed_origins). Requests without an Origin header
			// (curl, native MCP clients) pass through untouched — this is not
			// an authentication layer; /mcp still requires access tickets.
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" && !s.mcpOriginAllowed(origin) {
				writeError(w, http.StatusForbidden, errors.New("origin is not allowed on the MCP endpoint"))
				return
			}
			w.Header().Add("Vary", "Origin")
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id, MCP-Protocol-Version, Idempotency-Key")
		w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id, MCP-Protocol-Version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, bridgeRoutePrefix) {
			// Bridge dispatch fails closed: its ISCP adapter surface includes
			// approval resolution, so it must never be reachable without a
			// credential just because the gateway runs in the default no-auth
			// posture. A configured gateway.bridge_token is the dedicated
			// (and then exclusive) bridge credential; otherwise the request
			// falls through to the standard gateway bearer validation below.
			if token := strings.TrimSpace(s.cfg.Gateway.BridgeToken); token != "" {
				presented := bearerCredential(r.Header.Get("Authorization"))
				if presented == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
					writeError(w, http.StatusUnauthorized, errors.New("valid bridge token required"))
					return
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestPrincipalContextKey{}, defaultRequestPrincipal())))
				return
			}
			if !s.authRequired() {
				writeError(w, http.StatusServiceUnavailable, errors.New("bridge API requires Gateway authentication or a configured gateway.bridge_token"))
				return
			}
		}
		if s.isPublicRoute(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/speech/realtime" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.authRequired() {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestPrincipalContextKey{}, defaultRequestPrincipal())))
			return
		}
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if s.isPairingBootstrapRequest(r) && got == "" {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestPrincipalContextKey{}, defaultRequestPrincipal())))
			return
		}
		if got == "" {
			writeError(w, http.StatusUnauthorized, errors.New("valid bearer token required"))
			return
		}
		principal, ok, err := s.authenticateBearer(r.Context(), got)
		if err != nil {
			if store.StoreErrorCodeOf(err) == store.StoreErrorTimeout {
				writeError(w, http.StatusGatewayTimeout, errors.New("authentication request timed out"))
				return
			}
			writeError(w, http.StatusServiceUnavailable, errors.New("authentication is temporarily unavailable"))
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, errors.New("valid bearer token required"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestPrincipalContextKey{}, principal)))
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

func (s *Server) authenticateBearer(ctx context.Context, token string) (requestPrincipal, bool, error) {
	if configured := strings.TrimSpace(s.cfg.Gateway.APIToken); configured != "" {
		if subtle.ConstantTimeCompare([]byte(token), []byte(configured)) == 1 {
			return defaultRequestPrincipal(), true, nil
		}
	}
	client, ok, err := s.store.FindClientByTokenHash(ctx, hashSecret(token))
	if err != nil {
		return requestPrincipal{}, false, err
	}
	if !ok {
		return requestPrincipal{}, false, nil
	}
	client, ok, err = s.store.TouchClient(ctx, client.ID)
	if err != nil {
		return requestPrincipal{}, false, err
	}
	if !ok {
		return requestPrincipal{}, false, nil
	}
	ownerID := strings.TrimSpace(client.OwnerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	actorID := strings.TrimSpace(client.ActorID)
	if actorID == "" {
		actorID = ownerID
	}
	return requestPrincipal{OwnerID: ownerID, ActorID: actorID, ClientID: client.ID}, true, nil
}

type requestPrincipalContextKey struct{}

type requestPrincipal struct {
	OwnerID  string
	ActorID  string
	ClientID string
}

func defaultRequestPrincipal() requestPrincipal {
	return requestPrincipal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}
}

func principalForRequest(r *http.Request) requestPrincipal {
	if principal, ok := r.Context().Value(requestPrincipalContextKey{}).(requestPrincipal); ok && principal.OwnerID != "" && principal.ActorID != "" {
		return principal
	}
	return defaultRequestPrincipal()
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

func (s *Server) sessionIDVisibleToOwner(ctx context.Context, sessionID, ownerID string) (bool, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ownerID == app.DefaultOwnerID, nil
	}
	session, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return false, err
	}
	if !ok {
		return ownerID == app.DefaultOwnerID, nil
	}
	return sessionOwnerID(session) == ownerID, nil
}

func (s *Server) artifactVisibleToOwner(ctx context.Context, object app.ArtifactObject, ownerID string) (bool, error) {
	return s.sessionIDVisibleToOwner(ctx, object.SessionID, ownerID)
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

func writeSSEHeartbeat(w io.Writer) error {
	_, err := io.WriteString(w, ": ping\n\n")
	return err
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
	payload := map[string]any{"error": err.Error()}
	if code := errorCode(err); code != "" {
		payload["code"] = code
	}
	var retryable interface{ Retryable() bool }
	if errors.As(err, &retryable) {
		payload["retryable"] = retryable.Retryable()
	}
	writeJSON(w, status, payload)
}

func errorCode(err error) string {
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return ""
}

func connectorStartStatus(code string) int {
	switch code {
	case binding.CodeOperatorDisabled:
		return http.StatusForbidden
	case binding.CodeUserDisabled:
		return http.StatusConflict
	case binding.CodeBindingInProgress, binding.CodeBindingActive:
		return http.StatusConflict
	case binding.CodeConnectorUnavailable:
		return http.StatusServiceUnavailable
	case credential.CodeInvalid:
		return http.StatusBadRequest
	case credential.CodeCanceled:
		return http.StatusRequestTimeout
	case credential.CodeUnavailable, credential.CodeKeyUnavailable, credential.CodeUnsealFailed:
		return http.StatusServiceUnavailable
	case binding.CodeInvalidBotToken:
		return http.StatusBadRequest
	case binding.CodeTelegramRateLimited:
		return http.StatusTooManyRequests
	case binding.CodeTelegramUnavailable:
		return http.StatusServiceUnavailable
	case binding.CodeTelegramUnreachable, binding.CodeTelegramVerifyFailed:
		return http.StatusBadGateway
	default:
		return http.StatusBadRequest
	}
}

func writeConnectorError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("notification binding not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("notification binding changed"))
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("notification binding request is invalid"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, &credential.Error{Code: credential.CodeCanceled})
	case store.StoreErrorTimeout, store.StoreErrorUnavailable, store.StoreErrorDurability,
		store.StoreErrorUnknownOutcome, store.StoreErrorCorrupt, store.StoreErrorInternal:
		writeError(w, http.StatusServiceUnavailable, &binding.BindingError{Code: binding.CodeConnectorUnavailable})
	default:
		writeError(w, connectorStartStatus(errorCode(err)), err)
	}
}

func writeSessionStoreError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("session request is invalid"))
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("session not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("session cannot be changed"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, errors.New("session request was canceled"))
	case store.StoreErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, errors.New("session operation timed out"))
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("session service is unavailable"))
	}
}

func writeApprovalStoreError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("approval request is invalid"))
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("approval not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("approval changed or was already resolved"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, errors.New("approval request was canceled"))
	case store.StoreErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, errors.New("approval operation timed out"))
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("approval service is unavailable"))
	}
}

func writeConversationError(w http.ResponseWriter, fallbackStatus int, err error) {
	status, publicErr, ok := conversationErrorProjection(err)
	if !ok {
		writeError(w, fallbackStatus, err)
		return
	}
	writeError(w, status, publicErr)
}

func publicConversationError(err error) error {
	_, publicErr, ok := conversationErrorProjection(err)
	if ok {
		return publicErr
	}
	return err
}

func conversationErrorProjection(err error) (int, error, bool) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		return http.StatusBadRequest, errors.New("conversation request is invalid"), true
	case store.StoreErrorNotFound:
		return http.StatusNotFound, errors.New("conversation not found"), true
	case store.StoreErrorConflict:
		return http.StatusConflict, errors.New("conversation cannot be changed"), true
	case store.StoreErrorCanceled:
		return http.StatusRequestTimeout, errors.New("conversation request was canceled"), true
	case store.StoreErrorTimeout:
		return http.StatusGatewayTimeout, errors.New("conversation operation timed out"), true
	case store.StoreErrorUnavailable, store.StoreErrorDurability, store.StoreErrorUnknownOutcome,
		store.StoreErrorCorrupt, store.StoreErrorInternal:
		return http.StatusServiceUnavailable, errors.New("conversation service is unavailable"), true
	default:
		return 0, nil, false
	}
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

func publicMCPServersConfig(servers map[string]config.MCPServerConfig) map[string]any {
	out := map[string]any{}
	for name, server := range servers {
		projected := map[string]any{
			"configured":           true,
			"transport":            server.Transport,
			"namespace":            server.Namespace,
			"expected_server_name": server.ExpectedServerName,
			"protocol_version":     server.ProtocolVersion,
			"allow_private_http":   server.AllowPrivateHTTP,
		}
		if name == config.LocalMindMCPServerKey {
			projected["task_contract"] = "localmind.task.v1"
		} else {
			projected["allow_mutations"] = server.AllowMutations
			projected["tool_allow"] = append([]string(nil), server.ToolAllow...)
			projected["tool_deny"] = append([]string(nil), server.ToolDeny...)
		}
		out[name] = projected
	}
	return out
}

func publicModelConfig(cfg config.ModelConfig) map[string]any {
	return map[string]any{
		"mock":                 cfg.Mock,
		"http_timeout_seconds": cfg.HTTPTimeoutSeconds,
		"disable_thinking":     cfg.DisableThinking,
		"fast":                 publicModelProfile(cfg.Fast),
		"deep":                 publicModelProfile(cfg.Deep),
		"embedding":            publicModelProfile(cfg.Embedding),
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
		"backend":                     cfg.Backend,
		"path":                        cfg.Path,
		"dsn":                         configuredStatus(cfg.DSN),
		"startup_timeout_seconds":     cfg.StartupTimeoutSeconds,
		"read_timeout_seconds":        cfg.ReadTimeoutSeconds,
		"write_timeout_seconds":       cfg.WriteTimeoutSeconds,
		"transaction_timeout_seconds": cfg.TransactionTimeoutSeconds,
		"encrypt_at_rest":             cfg.EncryptAtRest,
		"encryption_key":              stateEncryptionStatus(cfg.EncryptionKey),
		"encryption_key_file":         stateEncryptionStatus(cfg.EncryptionKeyFile),
	}
}

func publicAdapterConfig(cfg config.AdapterConfig, ocrReadiness documentocr.RuntimeReadiness) map[string]any {
	return map[string]any{
		"browserAutomation": map[string]any{
			"command":                cfg.BrowserAutomation.Command,
			"timeout_ms":             cfg.BrowserAutomation.TimeoutMS,
			"startup_timeout_ms":     cfg.BrowserAutomation.StartupTimeoutMS,
			"daemon_idle_timeout_ms": cfg.BrowserAutomation.DaemonIdleTimeoutMS,
		},
		"documentOCR": map[string]any{
			"configured_enabled":    ocrReadiness.ConfiguredEnabled,
			"adapter_ready":         ocrReadiness.AdapterReady,
			"runtime_status":        ocrReadiness.RuntimeStatus,
			"reason_code":           ocrReadiness.ReasonCode,
			"provider":              cfg.DocumentOCR.Provider,
			"model":                 cfg.DocumentOCR.Model,
			"last_call_status":      ocrReadiness.LastCallStatus,
			"last_call_reason_code": ocrReadiness.LastCallReason,
			"last_call_at":          ocrReadiness.LastCallAt,
			"timeout_seconds":       cfg.DocumentOCR.TimeoutSeconds,
			"max_upload_bytes":      cfg.DocumentOCR.MaxUploadBytes,
			"max_output_bytes":      cfg.DocumentOCR.MaxOutputBytes,
			"max_tokens":            cfg.DocumentOCR.MaxTokens,
			"max_concurrency":       cfg.DocumentOCR.MaxConcurrency,
			"max_pending":           cfg.DocumentOCR.MaxPending,
		},
	}
}

func (s *Server) publicToolsConfig(ctx context.Context, ownerID string) (map[string]any, error) {
	cfg := s.cfg
	connectorStatuses := map[string]app.ConnectorStatus{}
	if s.connectors != nil {
		statuses, err := s.connectors.ListStatus(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		for _, status := range statuses {
			connectorStatuses[status.Channel] = status
		}
	}
	notificationChannels := map[string]any{}
	for name, channel := range cfg.Tools.Notifications.Channels {
		publicChannel := map[string]any{
			"enabled":          channel.Enabled,
			"provider":         channel.Provider,
			"base_url":         channel.BaseURL,
			"token_configured": strings.TrimSpace(channel.Token) != "",
			"recipient_set":    strings.TrimSpace(channel.Recipient) != "",
		}
		publicChannel["available"] = false
		publicChannel["operator_enabled"] = channel.Enabled
		publicChannel["binding_status"] = ""
		publicChannel["startable"] = false
		publicChannel["disabled_reason"] = binding.CodeConnectorUnavailable
		if status, ok := connectorStatuses[name]; ok {
			publicChannel["enabled"] = status.Enabled
			publicChannel["available"] = status.Available
			publicChannel["binding_status"] = status.BindingStatus
			publicChannel["startable"] = status.BindingStartable
			publicChannel["disabled_reason"] = status.DisabledReason
		}
		notificationChannels[name] = publicChannel
	}
	return map[string]any{
		"web": map[string]any{
			"search": map[string]any{
				"enabled":    cfg.Tools.Web.Search.Enabled,
				"provider":   cfg.Tools.Web.Search.Provider,
				"configured": webSearchConfigured(cfg),
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
	}, nil
}

func webSearchConfigured(cfg config.Config) bool {
	switch strings.ToLower(strings.TrimSpace(cfg.Tools.Web.Search.Provider)) {
	case "", "infinimesh-info":
		return cfg.Plugins.Entries.InfinimeshInfo.Config.Configured()
	default:
		return false
	}
}

func publicNotificationBindings(bindings []app.NotificationBinding) []map[string]any {
	out := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, publicNotificationBinding(binding))
	}
	return out
}

func publicNotificationBinding(binding app.NotificationBinding, includeActivation ...bool) map[string]any {
	qrCodeURL := binding.QRCodeURL
	if binding.Status == "waiting_confirm" && (len(includeActivation) == 0 || !includeActivation[0]) {
		qrCodeURL = ""
	}
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
		"qr_code_url":         qrCodeURL,
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

func firstEndpointActor(binding app.NotificationBinding) string {
	if actorID := strings.TrimSpace(binding.ActorID); actorID != "" {
		return actorID
	}
	return strings.TrimSpace(binding.OwnerID)
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
