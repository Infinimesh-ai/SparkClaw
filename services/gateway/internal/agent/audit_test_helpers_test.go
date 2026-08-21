package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func mustAgentListAudit(t testing.TB, repository store.AuditRepository, sessionID string) []app.AuditEvent {
	t.Helper()
	events, err := repository.ListAudit(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	return events
}
