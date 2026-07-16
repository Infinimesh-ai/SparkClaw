package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/gateway"
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
	connectors, err := newConnectorAssembly(cfg, st, runtime, transcriber)
	if err != nil {
		return nil, err
	}

	var reminderScheduler *reminder.Scheduler
	if cfg.Tools.Reminders.Enabled {
		reminderScheduler = reminder.NewScheduler(st, connectors.registry.NotificationRouter())
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
		),
		connectors:        connectors,
		reminderScheduler: reminderScheduler,
	}, nil
}

func (s *gatewayServices) Start(ctx context.Context) {
	if s.reminderScheduler != nil {
		startReminderScheduler(ctx, s.reminderScheduler)
	}
	s.connectors.registry.Start(ctx)
}

func startReminderScheduler(ctx context.Context, scheduler *reminder.Scheduler) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			for _, delivery := range scheduler.Tick(ctx) {
				slog.Info("reminder delivery completed", "reminder_id", delivery.ReminderID, "status", delivery.Status, "channel", delivery.Channel, "retry_state", delivery.RetryState)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
