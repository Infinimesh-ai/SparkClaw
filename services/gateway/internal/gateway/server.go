package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationconfig"
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
)

const (
	sseHeartbeatInterval = 15 * time.Second
)

type streamMessageExecutor func(context.Context, string, string, []agent.MessageAttachment, app.MessageIngressContext, agent.StreamHandler) (agent.Result, error)

type Repository interface {
	store.ISCPOnboardingRepository
	store.OwnerRepository
	store.ClientRepository
	store.ConnectorRepository
	store.SessionRepository
	store.ConversationRepository
	store.RunRepository
	store.ApprovalRepository
	store.AuditRepository
	store.EvaluationRepository
	store.ArtifactMetadataRepository
	store.MemoryRepository
	store.ScheduleRepository
	store.PassiveNotificationRepository
	store.DeliveryRecordRepository
	store.ExternalChatRepository
	store.MCPRepository
}

type Server struct {
	cfg                      config.Config
	store                    Repository
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
	integrations             IntegrationController
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
	storeRuntime             StoreRuntimeMonitor
}

func (s *Server) addAudit(ctx context.Context, event app.AuditEvent) {
	if err := s.store.AddAudit(context.WithoutCancel(ctx), event); err != nil {
		slog.Warn("gateway audit unavailable", "type", event.Type, "run_id", event.RunID, "code", store.StoreErrorCodeOf(err))
	}
}

type Option func(*Server)

type StoreRuntimeMonitor interface {
	Status() store.RuntimeStatus
	Metrics() []store.OperationMetric
}

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

type IntegrationController interface {
	List(context.Context) []integrationconfig.Status
	Get(context.Context, string) (integrationconfig.Status, error)
	AddInfoCredential(context.Context, integrationconfig.AddInfoCredentialInput) (integrationconfig.Status, error)
	AddLocalMindCredential(context.Context, integrationconfig.AddLocalMindCredentialInput) (integrationconfig.Status, error)
	Activate(context.Context, string, string, bool) (integrationconfig.Status, error)
	Check(context.Context, string, string) (integrationconfig.Status, error)
	Delete(context.Context, string, string) (integrationconfig.Status, error)
}

type ExternalApprovalResolver interface {
	Resolve(context.Context, app.Approval, app.ApprovalStatus) (resolvedElsewhere bool, err error)
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

func WithIntegrationController(controller IntegrationController) Option {
	return func(server *Server) {
		server.integrations = controller
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
			server.speech = speech.WithModelCallRecording(transcriber, server.store, server.cfg.Speech)
		}
	}
}

func WithManagedBrowserWindows(controller ManagedBrowserWindowController) Option {
	return func(server *Server) {
		server.managedBrowserWindows = controller
	}
}

func WithStoreRuntime(runtime StoreRuntimeMonitor) Option {
	return func(server *Server) {
		server.storeRuntime = runtime
	}
}

func WithMessageDelivery(endpoints *messagecontrol.EndpointRegistry, providers *delivery.ProviderRegistry, gateway *delivery.Gateway) Option {
	return func(server *Server) {
		server.endpoints = endpoints
		server.providers = providers
		server.delivery = gateway
	}
}

func New(cfg config.Config, st Repository, tools *toolhub.ToolHub, runtime agent.Runtime, options ...Option) *Server {
	return NewWithTrace(cfg, st, tools, runtime, trace.NewWriterFromConfig(cfg), options...)
}

func NewWithTrace(cfg config.Config, st Repository, tools *toolhub.ToolHub, runtime agent.Runtime, traces *trace.Writer, options ...Option) *Server {
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
	s.mux.HandleFunc("GET /api/integrations", s.listIntegrations)
	s.mux.HandleFunc("GET /api/integrations/{id}", s.getIntegration)
	s.mux.HandleFunc("POST /api/integrations/infinimesh-info/credentials", s.addInfoCredential)
	s.mux.HandleFunc("POST /api/integrations/localmind/credentials", s.addLocalMindCredential)
	s.mux.HandleFunc("PUT /api/integrations/{id}/active-credential", s.activateIntegrationCredential)
	s.mux.HandleFunc("POST /api/integrations/{id}/credentials/{credential_id}/check", s.checkIntegrationCredential)
	s.mux.HandleFunc("DELETE /api/integrations/{id}/credentials/{credential_id}", s.deleteIntegrationCredential)
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
	s.mux.HandleFunc("GET /api/jingsi/v0/readyz", s.jingSiLANGuard(s.readyJingSiLAN))
	s.mux.HandleFunc("POST /api/jingsi/v0/messages/stream", s.jingSiLANGuard(s.postJingSiMessageStream))
	s.mux.HandleFunc("GET /api/jingsi/v0/client-events/head", s.jingSiLANGuard(s.headJingSiEvents))
	s.mux.HandleFunc("GET /api/jingsi/v0/client-events", s.jingSiLANGuard(s.listJingSiEvents))
	s.mux.HandleFunc("GET /api/jingsi/v0/client-events/stream", s.jingSiLANGuard(s.streamJingSiEvents))
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
	var storeStatus *store.RuntimeStatus
	if s.storeRuntime != nil {
		status := s.storeRuntime.Status()
		storeStatus = &status
		if !status.Ready {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "store": status})
			return
		}
	}
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
	residentServices, err := s.residentServiceStatuses(r.Context(), speechStatus)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	payload := map[string]any{
		"ok":                true,
		"workspace_root":    s.cfg.Workspaces.DefaultRoot,
		"trace_dir":         s.cfg.Storage.TraceDir,
		"artifact_backend":  s.cfg.Storage.ArtifactBackend,
		"artifact_dir":      s.cfg.Storage.ArtifactDir,
		"artifact_bucket":   s.cfg.Storage.ArtifactBucket,
		"state_backend":     s.cfg.State.Backend,
		"state_path":        s.cfg.State.Path,
		"state_dsn":         stateDSNStatus(s.cfg),
		"auth_required":     s.authRequired(),
		"rate_limit":        publicRateLimitConfig(s.cfg.Gateway.RateLimit),
		"model_mode":        modelMode(s.cfg),
		"gateway_binding":   s.Addr(),
		"speech":            speechStatus,
		"resident_services": residentServices,
	}
	if storeStatus != nil {
		payload["store"] = storeStatus
	}
	writeJSON(w, http.StatusOK, payload)
}
