package store

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustAddAudit(t testing.TB, repository AuditRepository, event app.AuditEvent) {
	t.Helper()
	if err := repository.AddAudit(t.Context(), event); err != nil {
		t.Fatalf("add audit: %v", err)
	}
}

func mustListAudit(t testing.TB, repository AuditRepository, sessionID string) []app.AuditEvent {
	t.Helper()
	events, err := repository.ListAudit(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	return events
}

func mustEventsAfter(t testing.TB, repository AuditRepository, sessionID, after string) []app.Event {
	t.Helper()
	events, err := repository.EventsAfter(t.Context(), sessionID, after)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return events
}
