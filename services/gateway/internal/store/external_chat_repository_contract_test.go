package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func TestExternalChatRepositoryMemoryAndFileContract(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repository testBackend
			var restart func() testBackend
			switch backend {
			case "memory":
				repository = NewMemoryStore()
			case "file":
				path := filepath.Join(t.TempDir(), "state.json")
				file, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				repository = file
				restart = func() testBackend {
					reloaded, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return reloaded
				}
			}
			exerciseExternalChatRepositoryContract(t, repository, restart)
		})
	}
}

func TestPostgresExternalChatRepositoryConfiguredContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	repository, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	truncatePostgresStore(t, repository)
	exerciseExternalChatRepositoryContract(t, repository, nil)
}

func exerciseExternalChatRepositoryContract(t *testing.T, repository testBackend, restart func() testBackend) {
	t.Helper()
	if sessions, err := repository.ListExternalChatSessions(t.Context(), "", ""); err != nil || sessions == nil || len(sessions) != 0 {
		t.Fatalf("initial sessions = %#v err=%v", sessions, err)
	}
	if messages, err := repository.ListExternalChatMessages(t.Context(), "", 0); err != nil || messages == nil || len(messages) != 0 {
		t.Fatalf("initial messages = %#v err=%v", messages, err)
	}
	assertCanceledExternalChatOperations(t, repository)

	linked, err := repository.CreateSessionWithScope(t.Context(), "New SparkClaw Session", "owner-chat", "/workspace/chat", "web", false)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 22, 9, 30, 0, 123456789, time.FixedZone("contract", 8*60*60))
	session, err := repository.SaveExternalChatSession(t.Context(), app.ExternalChatSession{
		ID: "external-chat-a", OwnerID: "owner-chat", WorkspaceRoot: "/workspace/chat",
		BindingID: "binding-chat", Channel: "TeLeGrAm", Provider: "telegram-bot-api",
		ExternalUserID: "user-a", ExternalChatID: "chat-a", ExternalThreadID: "thread-a",
		LinkedSessionID: linked.ID, CreatedAt: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Channel != "telegram" || session.Status != "active" || session.AuthorizedOwnerID != "owner-chat" || session.AuthorizedActorID != "owner-chat" {
		t.Fatalf("session defaults = %#v", session)
	}
	if session.CreatedAt.Location() != time.UTC || session.CreatedAt.Nanosecond() != 123456000 || session.UpdatedAt.Location() != time.UTC || session.UpdatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("session time normalization = %#v", session)
	}
	linkedAfter, found, err := repository.GetSession(t.Context(), linked.ID)
	if err != nil || !found || !linkedAfter.Hidden || linkedAfter.Source != "telegram" || linkedAfter.Title != "Telegram 会话" {
		t.Fatalf("linked session = %#v found=%t err=%v", linkedAfter, found, err)
	}
	originalSessionCreatedAt := session.CreatedAt
	session.DisplayName = "Updated recipient"
	session.CreatedAt = base.Add(24 * time.Hour)
	session, err = repository.SaveExternalChatSession(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	if session.CreatedAt != originalSessionCreatedAt || session.DisplayName != "Updated recipient" {
		t.Fatalf("updated session = %#v", session)
	}
	if foundSession, ok, err := repository.FindExternalChatSession(t.Context(), session.BindingID, session.ExternalChatID, session.ExternalThreadID); err != nil || !ok || foundSession.ID != session.ID {
		t.Fatalf("find session = %#v found=%t err=%v", foundSession, ok, err)
	}
	if foundSession, ok, err := repository.FindExternalChatSessionByLinkedSessionID(t.Context(), linked.ID); err != nil || !ok || foundSession.ID != session.ID {
		t.Fatalf("find linked session = %#v found=%t err=%v", foundSession, ok, err)
	}
	if sessions, err := repository.ListExternalChatSessions(t.Context(), "TELEGRAM", "active"); err != nil || len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("filtered sessions = %#v err=%v", sessions, err)
	}

	message, err := repository.SaveExternalChatMessage(t.Context(), app.ExternalChatMessage{
		ID: "external-message-a", ChatSessionID: session.ID, BindingID: session.BindingID,
		Direction: "inbound", Role: "user", ExternalMessageID: "native-a", Content: "hello",
		Status: "received", CreatedAt: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.Channel != "telegram" || message.CreatedAt.Location() != time.UTC || message.CreatedAt.Nanosecond() != 123456000 {
		t.Fatalf("message normalization = %#v", message)
	}
	originalMessageCreatedAt := message.CreatedAt
	message.Status = "processed"
	message.CreatedAt = base.Add(24 * time.Hour)
	message, err = repository.SaveExternalChatMessage(t.Context(), message)
	if err != nil {
		t.Fatal(err)
	}
	if message.CreatedAt != originalMessageCreatedAt || message.Status != "processed" {
		t.Fatalf("updated message = %#v", message)
	}
	second, err := repository.SaveExternalChatMessage(t.Context(), app.ExternalChatMessage{
		ID: "external-message-b", ChatSessionID: session.ID, BindingID: session.BindingID,
		Direction: "outbound", Role: "assistant", ExternalMessageID: "native-b", Content: "world",
		Status: "sent", CreatedAt: base.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if foundMessage, ok, err := repository.FindExternalChatMessageByExternalID(t.Context(), session.ID, message.ExternalMessageID); err != nil || !ok || foundMessage.ID != message.ID {
		t.Fatalf("find message = %#v found=%t err=%v", foundMessage, ok, err)
	}
	if _, ok, err := repository.FindExternalChatMessageByExternalID(t.Context(), session.ID, ""); err != nil || ok {
		t.Fatalf("blank external message ID found=%t err=%v", ok, err)
	}
	if messages, err := repository.ListExternalChatMessages(t.Context(), session.ID, 1); err != nil || len(messages) != 1 || messages[0].ID != second.ID {
		t.Fatalf("limited messages = %#v err=%v", messages, err)
	}
	if _, ok, err := repository.GetExternalChatSession(t.Context(), "missing-session"); err != nil || ok {
		t.Fatalf("missing session found=%t err=%v", ok, err)
	}
	if _, ok, err := repository.GetExternalChatMessage(t.Context(), "missing-message"); err != nil || ok {
		t.Fatalf("missing message found=%t err=%v", ok, err)
	}

	if restart != nil {
		reloaded := restart()
		persistedSession, ok, err := reloaded.GetExternalChatSession(t.Context(), session.ID)
		if err != nil || !ok || persistedSession.CreatedAt != originalSessionCreatedAt || persistedSession.DisplayName != session.DisplayName {
			t.Fatalf("restarted session = %#v found=%t err=%v", persistedSession, ok, err)
		}
		persistedMessage, ok, err := reloaded.GetExternalChatMessage(t.Context(), message.ID)
		if err != nil || !ok || persistedMessage.CreatedAt != originalMessageCreatedAt || persistedMessage.Status != "processed" {
			t.Fatalf("restarted message = %#v found=%t err=%v", persistedMessage, ok, err)
		}
	}
}

func assertCanceledExternalChatOperations(t *testing.T, repository ExternalChatRepository) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checks := []struct {
		name string
		call func() error
	}{
		{name: "save session", call: func() error { _, err := repository.SaveExternalChatSession(ctx, app.ExternalChatSession{}); return err }},
		{name: "get session", call: func() error { _, _, err := repository.GetExternalChatSession(ctx, "id"); return err }},
		{name: "list sessions", call: func() error { _, err := repository.ListExternalChatSessions(ctx, "", ""); return err }},
		{name: "find session", call: func() error { _, _, err := repository.FindExternalChatSession(ctx, "", "", ""); return err }},
		{name: "find linked session", call: func() error { _, _, err := repository.FindExternalChatSessionByLinkedSessionID(ctx, "id"); return err }},
		{name: "save message", call: func() error { _, err := repository.SaveExternalChatMessage(ctx, app.ExternalChatMessage{}); return err }},
		{name: "get message", call: func() error { _, _, err := repository.GetExternalChatMessage(ctx, "id"); return err }},
		{name: "find message", call: func() error {
			_, _, err := repository.FindExternalChatMessageByExternalID(ctx, "id", "external")
			return err
		}},
		{name: "list messages", call: func() error { _, err := repository.ListExternalChatMessages(ctx, "id", 1); return err }},
	}
	for _, check := range checks {
		t.Run("canceled "+check.name, func(t *testing.T) {
			if err := check.call(); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("error = %v code=%q", err, StoreErrorCodeOf(err))
			}
		})
	}
}

func TestPostgresExternalChatWritesUseTransactions(t *testing.T) {
	createdAt := normalizeExternalChatTime(time.Now())
	sessionTransaction := &fakeDeliveryPostgresTx{rowQueue: []onboardingPostgresRow{fakeDeliveryPostgresRow{values: []any{createdAt}}}}
	sessionDatabase := &fakeDeliveryPostgresSession{transaction: sessionTransaction}
	sessionOps := &fakeDeliveryPostgresOps{session: sessionDatabase}
	repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, externalChatPostgres: sessionOps}
	saved, err := repository.SaveExternalChatSession(t.Context(), app.ExternalChatSession{
		ID: "chat-transaction", LinkedSessionID: "linked-session", Channel: "telegram", Status: "active",
	})
	if err != nil || saved.CreatedAt != createdAt || sessionTransaction.commits != 1 || sessionTransaction.rollbacks != 0 || len(sessionTransaction.execSQL) != 3 {
		t.Fatalf("session transaction saved=%#v err=%v commits=%d rollbacks=%d statements=%d", saved, err, sessionTransaction.commits, sessionTransaction.rollbacks, len(sessionTransaction.execSQL))
	}
	for _, expected := range []string{"UPDATE sessions", "INSERT INTO audit_events", "INSERT INTO events"} {
		if !strings.Contains(sessionTransaction.execSQL[len(sessionTransaction.execSQL)-3], expected) &&
			!strings.Contains(sessionTransaction.execSQL[len(sessionTransaction.execSQL)-2], expected) &&
			!strings.Contains(sessionTransaction.execSQL[len(sessionTransaction.execSQL)-1], expected) {
			t.Errorf("session transaction is missing %q: %#v", expected, sessionTransaction.execSQL)
		}
	}

	messageTransaction := &fakeDeliveryPostgresTx{rowQueue: []onboardingPostgresRow{fakeDeliveryPostgresRow{values: []any{createdAt}}}}
	messageDatabase := &fakeDeliveryPostgresSession{transaction: messageTransaction}
	messageOps := &fakeDeliveryPostgresOps{session: messageDatabase}
	repository = &PostgresStore{operationTimeouts: defaultOperationTimeouts, externalChatPostgres: messageOps}
	message, err := repository.SaveExternalChatMessage(t.Context(), app.ExternalChatMessage{
		ID: "message-transaction", ChatSessionID: "chat-transaction", Channel: "telegram", Status: "sent",
	})
	if err != nil || message.CreatedAt != createdAt || messageTransaction.commits != 1 || messageTransaction.rollbacks != 0 || len(messageTransaction.execSQL) != 2 {
		t.Fatalf("message transaction saved=%#v err=%v commits=%d rollbacks=%d statements=%d", message, err, messageTransaction.commits, messageTransaction.rollbacks, len(messageTransaction.execSQL))
	}
	if !strings.Contains(messageTransaction.execSQL[0], "INSERT INTO audit_events") || !strings.Contains(messageTransaction.execSQL[1], "INSERT INTO events") {
		t.Fatalf("message lifecycle statements = %#v", messageTransaction.execSQL)
	}
}

func TestPostgresExternalChatReadFailuresAreExplicit(t *testing.T) {
	backendErr := errors.New("postgres unavailable")
	operations := &fakeDeliveryPostgresOps{row: fakeDeliveryPostgresRow{err: backendErr}}
	repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, externalChatPostgres: operations}
	if _, _, err := repository.GetExternalChatSession(t.Context(), "chat"); StoreErrorCodeOf(err) != StoreErrorUnavailable {
		t.Fatalf("get error = %v code=%q", err, StoreErrorCodeOf(err))
	}
	operations.row = fakeDeliveryPostgresRow{err: pgx.ErrNoRows}
	if _, found, err := repository.GetExternalChatSession(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing session found=%t err=%v", found, err)
	}
	rows := &fakeDeliveryPostgresRows{err: backendErr}
	operations.rows = rows
	if _, err := repository.ListExternalChatMessages(t.Context(), "chat", 10); StoreErrorCodeOf(err) != StoreErrorUnavailable || !rows.closed {
		t.Fatalf("list error = %v code=%q closed=%t", err, StoreErrorCodeOf(err), rows.closed)
	}
}
