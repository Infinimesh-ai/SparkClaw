package main

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
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
}

func newGatewayServices(
	cfg config.Config,
	st store.Store,
	tools *toolhub.ToolHub,
	runtime agent.Runtime,
	traces *trace.Writer,
	transcriber speech.Transcriber,
) (*gatewayServices, error) {
	endpoints := messagecontrol.NewEndpointRegistry(st)
	runtime = runtime.WithMessageControlRouter(endpointMessageControlRouter{endpoints: endpoints})
	connectors, err := newConnectorAssembly(cfg, st, runtime, transcriber, endpoints)
	if err != nil {
		return nil, err
	}
	providers, err := connectors.registry.ProviderRegistry()
	if err != nil {
		return nil, err
	}

	var reminderScheduler *reminder.Scheduler
	if cfg.Tools.Reminders.Enabled {
		schedules := messagecontrol.NewScheduleRegistry(st)
		routes := messagecontrol.NewReturnRouteResolver(connectors.endpoints)
		reminderScheduler = reminder.NewMessageScheduler(st, schedules, routes, connectors.delivery, newScheduledRequestPublisher(runtime, routes, connectors.delivery))
	}

	return &gatewayServices{
		server: gateway.NewWithTrace(
			cfg,
			st,
			tools,
			runtime,
			traces,
			gateway.WithSpeechTranscriber(transcriber),
			gateway.WithCredentialVault(connectors.credentials),
			gateway.WithBindingRouter(connectors.registry.BindingRouter()),
			gateway.WithNotificationBindingCancellation(connectors.registry.CancelBinding),
			gateway.WithMessageDelivery(connectors.endpoints, providers, connectors.delivery),
		),
		connectors:        connectors,
		reminderScheduler: reminderScheduler,
	}, nil
}

func (s *gatewayServices) Start(ctx context.Context) {
	if s.reminderScheduler != nil {
		go s.reminderScheduler.Run(ctx)
	}
	s.connectors.registry.Start(ctx)
}
