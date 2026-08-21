package store

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustCreateSession(t testing.TB, repository SessionRepository, title string) app.Session {
	t.Helper()
	session, err := repository.CreateSession(t.Context(), title)
	if err != nil {
		t.Fatalf("create session fixture: %v", err)
	}
	return session
}

func mustCreateSessionWithScope(t testing.TB, repository SessionRepository, title, ownerID, workspaceRoot, source string, hidden bool) app.Session {
	t.Helper()
	session, err := repository.CreateSessionWithScope(t.Context(), title, ownerID, workspaceRoot, source, hidden)
	if err != nil {
		t.Fatalf("create scoped session fixture: %v", err)
	}
	return session
}

func mustGetSession(t testing.TB, repository SessionRepository, id string) (app.Session, bool) {
	t.Helper()
	session, found, err := repository.GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("get session fixture: %v", err)
	}
	return session, found
}

func mustListSessions(t testing.TB, repository SessionRepository) []app.Session {
	t.Helper()
	sessions, err := repository.ListSessions(t.Context())
	if err != nil {
		t.Fatalf("list session fixtures: %v", err)
	}
	return sessions
}
