package telegram

import (
	"context"
	"log/slog"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func recordAudit(ctx context.Context, repository store.AuditRepository, event app.AuditEvent) {
	if err := repository.AddAudit(context.WithoutCancel(ctx), event); err != nil {
		slog.Warn("Telegram audit unavailable", "type", event.Type, "code", store.StoreErrorCodeOf(err))
	}
}
