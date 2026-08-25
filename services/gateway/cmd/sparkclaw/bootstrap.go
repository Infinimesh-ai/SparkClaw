package main

import (
	"context"
	"net/url"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/happyapproval"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpintegration"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/reminder"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

type gatewayServices struct {
	server            *gateway.Server
	connectors        *connectorAssembly
	reminderScheduler *reminder.Scheduler
	mcpManager        *mcpintegration.Manager
	happyApprovals    *happyapproval.Service
}

func newGatewayServices(
	cfg config.Config,
	st backend,
	tools *toolhub.ToolHub,
	runtime agent.Runtime,
	traces *trace.Writer,
	transcriber speech.Transcriber,
	storeRuntime *store.Runtime,
) (*gatewayServices, error) {
	endpoints := messagecontrol.NewEndpointRegistry(st)
	runtime = runtime.WithMessageControlRouter(endpointMessageControlRouter{endpoints: endpoints})
	transcriber = speech.WithModelCallRecording(transcriber, st, cfg.Speech)
	connectors, err := newConnectorAssembly(cfg, st, runtime, transcriber, endpoints)
	if err != nil {
		return nil, err
	}
	providers, err := connectors.registry.ProviderRegistry()
	if err != nil {
		return nil, err
	}
	// Schedule admission through reminder tools must honor the owner's
	// connector opt-out; without this gate third-party routes fail closed.
	tools.WithConnectorGate(connectors.registry.Enabled)

	var reminderScheduler *reminder.Scheduler
	if cfg.Tools.Reminders.Enabled {
		schedules := messagecontrol.NewScheduleRegistry(st).WithEndpoints(connectors.endpoints)
		routes := messagecontrol.NewReturnRouteResolver(connectors.endpoints)
		reminderScheduler = reminder.NewMessageScheduler(st, schedules, newScheduledRequestPublisher(runtime, routes, connectors.delivery), cfg.Tools.Reminders.MaxDeliveryAttempts)
	}
	mcpManager := mcpintegration.New(withoutLocalMindMCPServer(cfg.MCPServers), tools, nil)
	iscpPairing, err := newISCPPairingService(cfg, st)
	if err != nil {
		return nil, err
	}
	var happyApprovals *happyapproval.Service
	if _, configured := cfg.MCPServers[happyapproval.ServerName]; configured {
		happyApprovals = happyapproval.New(st, mcpManager, 0)
	}

	return &gatewayServices{
		server: gateway.NewWithTrace(
			cfg,
			st,
			tools,
			runtime,
			traces,
			gateway.WithSpeechTranscriber(transcriber),
			gateway.WithConnectorController(connectors.registry),
			gateway.WithMCPController(mcpManager),
			gateway.WithISCPPairing(iscpPairing),
			gateway.WithExternalApprovalResolver(happyApprovals),
			gateway.WithManagedBrowserWindows(tools),
			gateway.WithMessageDelivery(connectors.endpoints, providers, connectors.delivery),
			gateway.WithStoreRuntime(storeRuntime),
		),
		connectors:        connectors,
		reminderScheduler: reminderScheduler,
		mcpManager:        mcpManager,
		happyApprovals:    happyApprovals,
	}, nil
}

func newISCPPairingService(cfg config.Config, st iscppairing.Repository) (*iscppairing.Service, error) {
	options := iscppairing.Options{
		Enabled: cfg.ISCPPairing.Enabled, DomainID: cfg.ISCPPairing.DomainID,
		ExpectedTicketType: cfg.ISCPPairing.ExpectedTicketType,
		DefaultTTL:         time.Duration(cfg.ISCPPairing.TicketTTLSeconds) * time.Second,
	}
	if !cfg.ISCPPairing.Enabled {
		return iscppairing.New(st, options), nil
	}
	endpoint, _ := url.Parse(cfg.ISCPPairing.AuthorityURL)
	options.AuthorityHost = endpoint.Host
	authority, err := iscppairing.NewHTTPAuthority(iscppairing.HTTPAuthorityOptions{
		Endpoint: cfg.ISCPPairing.AuthorityURL, TokenEnv: cfg.ISCPPairing.TokenEnv, TokenFile: cfg.ISCPPairing.TokenFile,
		Timeout: time.Duration(cfg.ISCPPairing.RequestTimeoutSeconds) * time.Second, ResponseMaxBytes: cfg.ISCPPairing.ResponseBodyMaxBytes,
	})
	if err != nil {
		return nil, err
	}
	options.Authority = authority
	return iscppairing.New(st, options), nil
}

func (s *gatewayServices) Start(ctx context.Context) error {
	s.server.BindLifecycleContext(ctx)
	s.server.StartRetentionSweeps(ctx)
	s.connectors.credentials.BindLifecycle(ctx)
	if err := s.connectors.registry.Start(ctx); err != nil {
		return err
	}
	if s.reminderScheduler != nil {
		go s.reminderScheduler.Run(ctx)
	}
	if s.mcpManager != nil {
		s.mcpManager.Run(ctx)
	}
	if s.happyApprovals != nil {
		s.happyApprovals.Run(ctx)
	}
	return nil
}
