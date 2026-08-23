package storetest

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func MustCreateSession(t testing.TB, repository store.SessionRepository, title string) app.Session {
	t.Helper()
	session, err := repository.CreateSession(t.Context(), title)
	if err != nil {
		t.Fatalf("create session fixture: %v", err)
	}
	return session
}

func MustCreateSessionWithScope(t testing.TB, repository store.SessionRepository, title, ownerID, workspaceRoot, source string, hidden bool) app.Session {
	t.Helper()
	session, err := repository.CreateSessionWithScope(t.Context(), title, ownerID, workspaceRoot, source, hidden)
	if err != nil {
		t.Fatalf("create scoped session fixture: %v", err)
	}
	return session
}

func MustListSessions(t testing.TB, repository store.SessionRepository) []app.Session {
	t.Helper()
	sessions, err := repository.ListSessions(t.Context())
	if err != nil {
		t.Fatalf("list session fixtures: %v", err)
	}
	return sessions
}

func MustGetSession(t testing.TB, repository store.SessionRepository, id string) (app.Session, bool) {
	t.Helper()
	session, found, err := repository.GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("get session fixture: %v", err)
	}
	return session, found
}

func MustUpdateSessionTitle(t testing.TB, repository store.SessionRepository, id, title string) app.Session {
	t.Helper()
	session, err := repository.UpdateSessionTitle(t.Context(), id, title)
	if err != nil {
		t.Fatalf("update session fixture: %v", err)
	}
	return session
}

func MustDeleteSession(t testing.TB, repository store.SessionRepository, id string) app.Session {
	t.Helper()
	session, err := repository.DeleteSession(t.Context(), id)
	if err != nil {
		t.Fatalf("delete session fixture: %v", err)
	}
	return session
}
