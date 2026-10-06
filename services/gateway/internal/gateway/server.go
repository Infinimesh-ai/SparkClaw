package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/aichatexport"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/binding"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationconfig"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/jingsiruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpintegration"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3browser"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3mail"
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
	DeleteMCPInvocationSession(context.Context, string) (app.Session, error)
}

type Server struct {
	r3Broker                  *r3browser.Broker
	r3Mail                    *r3mail.Service
	r3Schedules               *r3ScheduleRegistry
	r3Mu                      sync.Mutex
	r3Root                    string
	r3Executor                r3execution.Executor
	r3Executions              *r3execution.Service
	cfg                       config.Config
	store                     Repository
	tools                     *toolhub.ToolHub
	runtime                   agent.Runtime
	models                    modelrouter.Router
	traces                    *trace.Writer
	artifacts                 artifact.Store
	policies                  policy.Engine
	speech                    speech.Transcriber
	speechRealtimeMu          sync.Mutex
	speechRealtimeTickets     map[string]*speechRealtimeTicket
	speechRealtimeTicketIDs   map[string]string
	managedBrowserWindows     ManagedBrowserWindowController
	delivery                  *delivery.Gateway
	endpoints                 *messagecontrol.EndpointRegistry
	providers                 *delivery.ProviderRegistry
	connectors                ConnectorController
	mcp                       MCPController
	integrations              IntegrationController
	email                     EmailController
	emailManagement           *emailmanagement.Service
	aiPlatformLogin           *aichatexport.LoginManager
	browserControl            BrowserControlController
	mcpAccess                 *mcpaccess.Service
	iscpPairing               *iscppairing.Service
	externalApprovalResolver  ExternalApprovalResolver
	bridge                    *iscpbridge.GatewayAdapter
	deliveryMu                sync.Mutex
	mux                       *http.ServeMux
	localWorkbenchMux         *http.ServeMux
	started                   time.Time
	limiter                   *rateLimiter
	lifecycleMu               sync.RWMutex
	lifecycleCtx              context.Context
	passiveStreamMu           sync.Mutex
	passiveStreams            map[string]int
	streamMessage             streamMessageExecutor
	streamWG                  sync.WaitGroup
	approvalLocks             sync.Map
	sessionMessageAdmissions  sync.Map
	pairing                   *pairingCoordinator
	clientIssuance            *clientIssuanceCoordinator
	clientConnections         *clientConnectionRegistry
	workbenchEvents           *workbenchEventHub
	workbenchEventMonitorOnce sync.Once
	storeRuntime              StoreRuntimeMonitor
	credentialVault           CredentialVaultMonitor
	jingsiRuntime             *jingsiruntime.Provider
	// pptxSealedSweepCursor is the artifact key the next sealed-candidate
	// expiry sweep resumes after; only the retention coordinator goroutine
	// reads or writes it.
	pptxSealedSweepCursor string
}

func (s *Server) addAudit(ctx context.Context, event app.AuditEvent) {
	if principal, ok := ctx.Value(requestPrincipalContextKey{}).(requestPrincipal); ok && principal.LocalAccessID != "" {
		fields := make(map[string]any, len(event.Fields)+3)
		for key, value := range event.Fields {
			fields[key] = value
		}
		fields["auth_method"] = "local"
		fields["local_access_id"] = principal.LocalAccessID
		fields["owner_id"] = principal.OwnerID
		event.Fields = fields
	}
	if err := s.store.AddAudit(context.WithoutCancel(ctx), event); err != nil {
		slog.Warn("gateway audit unavailable", "type", event.Type, "run_id", event.RunID, "code", store.StoreErrorCodeOf(err))
	}
}

type Option func(*Server)

type StoreRuntimeMonitor interface {
	Status() store.RuntimeStatus
	Metrics() []store.OperationMetric
}

type CredentialVaultMonitor interface {
	Ready() error
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

type EmailController interface {
	List(context.Context, string) ([]emailautomation.ProviderStatus, error)
	Update(context.Context, string, string, string, emailautomation.UpdateProviderInput) (emailautomation.ProviderStatus, error)
	OpenLoginBrowser(context.Context, string, string, string) (emailautomation.ProviderStatus, error)
	Check(context.Context, string, string, string) (emailautomation.ProviderStatus, error)
}

type BrowserControlController interface {
	Status(context.Context) browsercontrol.Status
	SaveToken(context.Context, []byte) (browsercontrol.Status, error)
	Check(context.Context) (browsercontrol.Status, error)
	Remove(context.Context) (browsercontrol.Status, error)
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

func WithEmailController(controller EmailController) Option {
	return func(server *Server) {
		server.email = controller
	}
}

func WithEmailManagement(service *emailmanagement.Service) Option {
	return func(server *Server) { server.emailManagement = service }
}

func WithAIPlatformLogin(manager *aichatexport.LoginManager) Option {
	return func(s *Server) { s.aiPlatformLogin = manager }
}

func WithBrowserControlController(controller BrowserControlController) Option {
	return func(server *Server) {
		server.browserControl = controller
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

func WithCredentialVault(vault CredentialVaultMonitor) Option {
	return func(server *Server) {
		server.credentialVault = vault
	}
}

func WithJingSiRuntime(provider *jingsiruntime.Provider) Option {
	return func(server *Server) {
		server.jingsiRuntime = provider
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
		localWorkbenchMux:       http.NewServeMux(),
		started:                 time.Now().UTC(),
		limiter:                 newRateLimiter(cfg.Gateway.RateLimit),
		lifecycleCtx:            context.Background(),
		passiveStreams:          map[string]int{},
		speechRealtimeTickets:   map[string]*speechRealtimeTicket{},
		speechRealtimeTicketIDs: map[string]string{},
		pairing:                 newPairingCoordinator(),
		clientIssuance:          newClientIssuanceCoordinator(),
		clientConnections:       newClientConnectionRegistry(),
		workbenchEvents:         newWorkbenchEventHub(),
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
	if s.r3Mail == nil && s.emailManagement != nil {
		root, _ := filepath.Abs(s.cfg.State.Path + ".r3/mail")
		service, err := r3mail.New(root, r3mail.Repository{OwnerStatus: s.emailManagement.ClientSyncOwnerStatus, Mailbox: s.emailManagement.ClientSyncMailbox, Mailboxes: s.emailManagement.ClientSyncMailboxes}, s.emailManagement)
		if err == nil {
			s.r3Mail = service
		}
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	standard := s.withCORS(s.withRateLimit(s.withAuth(s.mux)))
	if s.jingsiRuntime == nil {
		return standard
	}
	// The runtime carries its own bearer, but it still shares the gateway's
	// request-rate bound: lookup/status/events are cheap to issue and each
	// takes the provider's single store lock.
	runtime := s.withRateLimit(s.jingsiRuntime)
	root := http.NewServeMux()
	root.Handle("POST /v1/executions:submit", runtime)
	root.Handle("POST /v1/executions:lookup", runtime)
	root.Handle("POST /v1/executions:status", runtime)
	root.Handle("POST /v1/executions:cancel", runtime)
	root.Handle("POST /v1/execution-events:list", runtime)
	root.Handle("/", standard)
	return root
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
	s.startWorkbenchEventMonitor(ctx)
	if s.jingsiRuntime != nil {
		s.jingsiRuntime.Start(ctx)
	}
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
		s.r3Mu.Lock()
		executions := s.r3Executions
		s.r3Mu.Unlock()
		if executions != nil {
			executions.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
		if s.jingsiRuntime != nil {
			return s.jingsiRuntime.Wait(ctx)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) routes() {
	s.registerR3ExecutionRoutes()
	s.registerR3MailRoutes()
	s.registerR3ScheduleRoutes()
	s.registerR3HostRoutes()
	s.handleWorkbench("GET /healthz", s.healthz)
	s.handleWorkbench("GET /readyz", s.readyz)
	s.mux.HandleFunc("GET /metrics", s.metrics)
	s.mux.HandleFunc("POST /chat", s.chat)
	s.handleWorkbench("GET /api/config", s.getConfig)
	s.handleWorkbench("GET /api/workbench/identity", s.getWorkbenchIdentity)
	s.handleWorkbench("GET /api/owner", s.getOwnerProfile)
	s.handleWorkbench("POST /api/owner", s.updateOwnerProfile)
	s.handleWorkbench("PUT /api/owner/language", s.updateOwnerLanguage)
	s.handleWorkbench("GET /api/profiles", s.listOwnerProfiles)
	s.handleWorkbench("GET /api/profiles/{owner_id}", s.getOwnerProfileByID)
	s.handleWorkbench("PATCH /api/profiles/{owner_id}", s.patchOwnerProfile)
	s.handleWorkbench("GET /api/clients", s.listClients)
	s.handleWorkbench("POST /api/clients", s.issueClient)
	s.handleWorkbench("POST /api/clients/{id}/revoke", s.revokeClient)
	s.handleWorkbench("GET /api/workbench/events/stream", s.streamWorkbenchEvents)
	s.handleWorkbench("POST /api/tool-policy", s.updateToolPolicy)
	s.mux.HandleFunc("POST /api/pairing/start", s.startPairing)
	s.mux.HandleFunc("POST /api/pairing/claim", s.claimPairing)
	s.handleWorkbench("GET /api/notification-bindings", s.listNotificationBindings)
	s.handleWorkbench("GET /api/notifications", s.listPassiveNotifications)
	s.handleWorkbench("POST /api/notifications/read-all", s.markAllPassiveNotificationsRead)
	s.handleWorkbench("POST /api/notifications/{id}/read", s.markPassiveNotificationRead)
	s.handleWorkbench("GET /api/notifications/events/stream", s.streamPassiveNotifications)
	s.handleWorkbench("GET /api/connectors", s.listConnectors)
	s.handleWorkbench("PATCH /api/connectors/{channel}", s.updateConnector)
	s.handleWorkbench("GET /api/email/providers", s.listEmailProviders)
	s.handleWorkbench("PATCH /api/email/providers/{provider}", s.updateEmailProvider)
	s.handleWorkbench("POST /api/email/providers/{provider}/login-browser", s.openEmailLoginBrowser)
	s.handleWorkbench("POST /api/email/providers/{provider}/check", s.checkEmailProvider)
	s.registerEmailComposeRoutes()
	s.handleWorkbench("GET /api/email/presentations", s.getEmailPresentations)
	s.handleWorkbench("POST /api/email/presentations/ensure", s.ensureEmailPresentations)
	s.handleWorkbench("GET /api/email/notifications", s.listEmailRoutedMessages)
	s.handleWorkbench("GET /api/email/interaction-mails", s.listEmailRoutedMessages)
	s.handleWorkbench("GET /api/email/messages/{mail}/verification", s.revealEmailVerification)
	s.handleWorkbench("GET /api/email/messages/{mail}", s.getEmailSingleMessage)
	s.handleWorkbench("POST /api/email/messages/{mail}/classification", s.changeEmailClassification)
	s.handleWorkbench("POST /api/email/messages/{mail}/assignment", s.changeEmailAssignment)
	s.handleWorkbench("POST /api/email/conversations/{conversation}/rename", s.renameEmailConversation)
	s.handleWorkbench("DELETE /api/email/conversations/{conversation}", s.deleteEmailConversation)
	s.handleWorkbench("GET /api/email/sender-rules", s.emailSenderRules)
	s.handleWorkbench("POST /api/email/sender-rules/{rule}", s.updateEmailSenderRule)
	s.handleWorkbench("GET /api/email/conversations", s.listEmailConversations)
	s.handleWorkbench("GET /api/email/conversations/{conversation}", s.getEmailConversation)
	s.handleWorkbench("GET /api/email/conversations/{conversation}/messages", s.listEmailMessages)
	s.handleWorkbench("GET /api/email/pending", s.listEmailPending)
	s.handleWorkbench("GET /api/email/sync-status", s.getEmailSyncStatus)
	s.handleWorkbench("POST /api/email/sync", s.scheduleEmailSync)
	s.handleWorkbench("GET /api/email/sync-warnings", s.listEmailSyncWarnings)
	s.handleWorkbench("POST /api/email/sync-warnings/{warning}/acknowledge", s.acknowledgeEmailSyncWarning)
	s.handleWorkbench("POST /api/email/messages/viewed", s.markEmailMessagesViewed)
	s.handleWorkbench("POST /api/email/messages/{mail}/reanalyze", s.reanalyzeEmailMessage)
	s.handleWorkbench("GET /api/email/messages/{mail}/file", s.getEmailMessageFile)
	s.handleWorkbench("GET /api/email/messages/{mail}/render-preview", s.getEmailMessageRenderPreview)
	s.handleWorkbench("POST /api/email/source/cleanup", s.cleanupEmailSource)
	s.handleWorkbench("GET /api/browser/extension/ai-platforms", s.getAIPlatformLogin)
	s.handleWorkbench("POST /api/browser/extension/ai-platforms/{provider}/login", s.openAIPlatformLogin)
	s.handleWorkbench("POST /api/browser/extension/ai-platforms/{provider}/check", s.checkAIPlatformLogin)
	s.handleWorkbench("GET /api/browser/extension", s.getBrowserExtension)
	s.handleWorkbench("PUT /api/browser/extension/token", s.putBrowserExtensionToken)
	s.handleWorkbench("POST /api/browser/extension/check", s.checkBrowserExtension)
	s.handleWorkbench("DELETE /api/browser/extension/token", s.deleteBrowserExtensionToken)
	s.handleWorkbench("GET /api/integrations", s.listIntegrations)
	s.handleWorkbench("GET /api/integrations/{id}", s.getIntegration)
	s.handleWorkbench("POST /api/integrations/infinimesh-info/credentials", s.addInfoCredential)
	s.handleWorkbench("POST /api/integrations/localmind/credentials", s.addLocalMindCredential)
	s.handleWorkbench("PUT /api/integrations/{id}/active-credential", s.activateIntegrationCredential)
	s.handleWorkbench("POST /api/integrations/{id}/credentials/{credential_id}/check", s.checkIntegrationCredential)
	s.handleWorkbench("DELETE /api/integrations/{id}/credentials/{credential_id}", s.deleteIntegrationCredential)
	s.handleWorkbench("GET /api/mcp-servers", s.listMCPServers)
	s.handleWorkbench("POST /api/mcp-servers/{name}/refresh", s.refreshMCPServer)
	s.handleWorkbench("POST /api/notification-bindings/{channel}/start", s.startNotificationBinding)
	s.handleWorkbench("GET /api/notification-bindings/{id}", s.getNotificationBinding)
	s.handleWorkbench("POST /api/notification-bindings/{id}/poll", s.pollNotificationBinding)
	s.handleWorkbench("POST /api/notification-bindings/{id}/browser", s.openNotificationBindingBrowser)
	s.handleWorkbench("DELETE /api/notification-bindings/{id}", s.revokeNotificationBinding)
	s.handleWorkbench("GET /api/delivery-endpoints", s.listDeliveryEndpoints)
	s.handleWorkbench("GET /api/deliveries", s.listDeliveries)
	s.handleWorkbench("POST /api/deliveries", s.createDelivery)
	s.handleWorkbench("GET /api/deliveries/{id}", s.getDelivery)
	s.handleWorkbench("POST /api/deliveries/{id}/retry", s.retryDelivery)
	s.handleWorkbench("GET /api/message-history", s.listMessageHistory)
	s.handleWorkbench("GET /api/schedules", s.listCurrentSchedules)
	s.handleWorkbench("POST /api/schedules", s.createSchedule)
	s.handleWorkbench("GET /api/sessions", s.listSessions)
	s.handleWorkbench("POST /api/sessions", s.createSession)
	s.handleWorkbench("GET /api/sessions/{id}", s.getSession)
	s.handleWorkbench("PATCH /api/sessions/{id}", s.updateSession)
	s.handleWorkbench("DELETE /api/sessions/{id}", s.deleteSession)
	s.handleWorkbench("GET /api/sessions/{id}/messages", s.listMessages)
	s.handleWorkbench("POST /api/sessions/{id}/messages/stream", s.postMessageStream)
	s.handleWorkbench("POST /api/sessions/{id}/messages", s.postMessage)
	s.handleWorkbench("GET /api/sessions/{id}/events", s.listEvents)
	s.handleWorkbench("GET /api/sessions/{id}/events/stream", s.streamSessionEvents)
	s.mux.HandleFunc("GET /api/jingsi/v0/readyz", s.jingSiLANGuard(s.readyJingSiLAN))
	s.mux.HandleFunc("POST /api/jingsi/v0/messages/stream", s.jingSiLANGuard(s.postJingSiMessageStream))
	s.mux.HandleFunc("GET /api/jingsi/v0/client-events/head", s.jingSiLANGuard(s.headJingSiEvents))
	s.mux.HandleFunc("GET /api/jingsi/v0/client-events", s.jingSiLANGuard(s.listJingSiEvents))
	s.mux.HandleFunc("GET /api/jingsi/v0/client-events/stream", s.jingSiLANGuard(s.streamJingSiEvents))
	s.mux.HandleFunc("POST /api/bridge/v1/dispatch", s.dispatchBridgeRequest)
	s.mux.HandleFunc("POST /api/bridge/v1/mcp/dispatch", s.dispatchMCPBridgeRequest)
	s.mux.HandleFunc("POST /mcp", s.dispatchLANDirectMCP)
	s.handleWorkbench("PATCH /api/mcp-access/transports", s.updateMCPTransports)
	s.handleWorkbench("GET /api/mcp-access/tickets", s.listMCPAccessTickets)
	s.handleWorkbench("GET /api/mcp-access/catalog", s.listMCPAccessCatalog)
	s.handleWorkbench("POST /api/mcp-access/tickets", s.issueMCPAccessTicket)
	s.handleWorkbench("POST /api/mcp-access/tickets/{id}/revoke", s.revokeMCPAccessTicket)
	s.handleWorkbench("DELETE /api/mcp-access/tickets/{id}", s.deleteMCPAccessTicket)
	s.handleWorkbench("GET /api/mcp-access/bindings", s.listMCPBindings)
	s.handleWorkbench("POST /api/mcp-access/bindings/{id}/revoke", s.revokeMCPBinding)
	s.handleWorkbench("DELETE /api/mcp-access/bindings/{id}", s.deleteMCPBinding)
	s.handleWorkbench("DELETE /api/mcp-access/records", s.deleteMCPAccessRecords)
	s.handleWorkbench("GET /api/iscp-pairing/status", s.getISCPPairingStatus)
	s.handleWorkbench("GET /api/iscp-pairing/onboardings", s.listISCPOnboardings)
	s.handleWorkbench("POST /api/iscp-pairing/start", s.startISCPPairing)
	s.handleWorkbench("GET /api/sessions/{id}/model-calls", s.listSessionModelCalls)
	s.handleWorkbench("GET /api/sessions/{id}/tool-calls", s.listSessionToolCalls)
	s.handleWorkbench("GET /api/sessions/{id}/audit", s.listSessionAudit)
	s.handleWorkbench("GET /api/sessions/{id}/episodes", s.listSessionEpisodes)
	s.handleWorkbench("GET /api/runs/{id}/feedback", s.listRunFeedback)
	s.handleWorkbench("POST /api/runs/{id}/feedback", s.saveRunFeedback)
	s.handleWorkbench("GET /api/tools", s.listTools)
	s.handleWorkbench("POST /api/tools/{name}/invoke", s.invokeTool)
	s.handleWorkbench("GET /api/tool-calls/{id}", s.getToolCall)
	s.handleWorkbench("GET /api/approvals", s.listApprovals)
	s.handleWorkbench("POST /api/approvals/{id}/approve", s.approveApproval)
	s.handleWorkbench("POST /api/approvals/{id}/reject", s.rejectApproval)
	s.handleWorkbench("POST /api/approvals/{id}/modify", s.modifyApproval)
	s.handleWorkbench("GET /api/memories", s.listMemories)
	s.handleWorkbench("GET /api/memories/export", s.getMemoryExport)
	s.handleWorkbench("POST /api/memories/export", s.archiveMemoryExport)
	s.handleWorkbench("POST /api/memories/{id}/update", s.updateMemory)
	s.handleWorkbench("POST /api/memories/{id}/delete", s.deleteMemory)
	s.handleWorkbench("GET /api/memory-candidates", s.listMemoryCandidates)
	s.handleWorkbench("POST /api/memory-candidates/{id}/accept", s.acceptMemoryCandidate)
	s.handleWorkbench("POST /api/memory-candidates/{id}/reject", s.rejectMemoryCandidate)
	s.handleWorkbench("GET /api/traces", s.listTraces)
	s.handleWorkbench("GET /api/traces/{run_id}", s.getTrace)
	s.handleWorkbench("GET /api/artifacts", s.listArtifacts)
	s.handleWorkbench("GET /api/speech/status", s.getSpeechStatus)
	s.handleWorkbench("POST /api/speech/transcriptions", s.postSpeechTranscription)
	s.handleWorkbench("POST /api/speech/realtime-sessions", s.postSpeechRealtimeSession)
	s.handleWorkbench("DELETE /api/speech/realtime-sessions/{id}", s.deleteSpeechRealtimeSession)
	s.handleWorkbench("GET /api/speech/realtime", s.getSpeechRealtime)
	s.handleWorkbench("POST /api/documents/upload", s.uploadDocument)
	s.handleWorkbench("GET /api/documents/available", s.listAvailableDocuments)
	s.handleWorkbench("GET /api/documents/file", s.getUploadedDocument)
	s.handleWorkbench("GET /api/workspace/screenshots/{name}", s.getWorkspaceScreenshot)
	s.handleWorkbench("GET /api/evals", s.listEvals)
	s.handleWorkbench("POST /api/evals/run", s.runEval)
	s.handleWorkbench("GET /api/evals/{id}", s.getEval)
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
	credentialVaultStatus := map[string]any{"ready": true, "state": "ready"}
	if s.credentialVault != nil {
		if err := s.credentialVault.Ready(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"ok": false, "credential_vault": map[string]any{"ready": false, "state": "unavailable"},
			})
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
		"credential_vault":  credentialVaultStatus,
	}
	if storeStatus != nil {
		payload["store"] = storeStatus
	}
	writeJSON(w, http.StatusOK, payload)
}
