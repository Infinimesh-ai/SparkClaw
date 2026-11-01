package store

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
)

func persistedClarification(t *testing.T) *clarification.Record {
	t.Helper()
	record, err := clarification.Sign(bytes.Repeat([]byte{42}, 32), clarification.Binding{
		OwnerID: "owner", ClientID: "client", InstallationID: "installation", ConversationID: "conversation",
	}, clarification.Record{
		SchemaVersion: 2, ID: "clarification-original", RequestID: "request-original", SourceMessageID: "message-original",
		SourceTextSHA256: strings.Repeat("a", 64), ContextSHA256: strings.Repeat("b", 64), PromptSHA256: strings.Repeat("c", 64),
		IssuedAt: "2026-10-30T00:00:00Z", ExpiresAt: "2026-10-31T00:00:00Z",
		GraphRevision: "graph.1", CatalogRevision: "catalog.1", CalibrationRevision: "calibration.1",
		Options: []clarification.Option{{Number: 1, Kind: "file", FileID: "file-original", BindingSHA256: strings.Repeat("d", 64)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &record
}

func TestConversationContextBackendParity(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var harness conversationRepositoryHarness
			if backend == "postgres" {
				dsn := newPostgresMigrationTestSchema(t)
				open := func(tb testing.TB) testBackend {
					tb.Helper()
					st, err := NewPostgresStore(tb.Context(), dsn)
					if err != nil {
						tb.Fatal(err)
					}
					tb.Cleanup(st.Close)
					return st
				}
				harness = conversationRepositoryHarness{store: open(t), restart: open}
			} else {
				harness = newConversationRepositoryHarness(t, backend)
			}
			session := mustCreateSession(t, harness.store, "context persistence")
			input := app.Message{ID: "question-original", SessionID: session.ID, Role: "assistant", Content: "1. Read the original file",
				FilesUnavailable: true, Clarification: persistedClarification(t)}
			stored := mustAddMessage(t, harness.store, input)
			want := cloneMessage(stored)
			input.Clarification.Options[0].FileID = "input-alias"
			stored.Clarification.ContextSHA256 = strings.Repeat("0", 64)
			stored.Clarification.Options[0].FileID = "output-alias"
			assertState := func(repository testBackend) {
				t.Helper()
				messages := mustListMessages(t, repository, session.ID)
				if len(messages) != 1 || !reflect.DeepEqual(messages[0], want) {
					t.Fatalf("context changed: %#v", messages)
				}
				messages[0].Clarification.Options[0].FileID = "list-alias"
				recent, err := repository.ListRecentMessages(t.Context(), session.ID, time.Now().Add(time.Hour), "", 32)
				if err != nil || len(recent) != 1 || !reflect.DeepEqual(recent[0], want) {
					t.Fatalf("recent context: %#v %v", recent, err)
				}
				recent[0].Clarification.Options[0].FileID = "recent-alias"
				page := mustMessageEventsAfter(t, repository, session.ID, "", 10)
				if len(page.Events) != 1 || !reflect.DeepEqual(messageFromEvent(t, page.Events[0]), want) {
					t.Fatalf("event context changed: %#v", page.Events)
				}
				payload := messageFromEvent(t, page.Events[0])
				payload.Clarification.Options[0].FileID = "event-alias"
				duplicate := mustAddMessage(t, repository, app.Message{ID: want.ID, SessionID: session.ID, Content: "replacement"})
				if !reflect.DeepEqual(duplicate, want) {
					t.Fatalf("duplicate changed binding: %#v", duplicate)
				}
				duplicate.Clarification.Options[0].FileID = "duplicate-alias"
			}
			assertState(harness.store)
			assertState(harness.store)
			if harness.restart != nil {
				assertState(harness.restart(t))
			}
		})
	}
}

func TestConversationContextSnapshotIsolation(t *testing.T) {
	st := NewMemoryStore()
	session := mustCreateSession(t, st, "snapshot context")
	want := mustAddMessage(t, st, app.Message{ID: "snapshot-message", SessionID: session.ID, Role: "assistant", Content: "question", Clarification: persistedClarification(t), FilesUnavailable: true})
	snapshot := st.snapshot()
	snapshot.Messages[session.ID][0].Clarification.Options[0].FileID = "snapshot-alias"
	if got := mustListMessages(t, st, session.ID)[0]; !reflect.DeepEqual(got, want) {
		t.Fatal("snapshot aliases original store")
	}
	snapshot = st.snapshot()
	restored := NewMemoryStore()
	restored.loadSnapshot(snapshot)
	snapshot.Messages[session.ID][0].Clarification.Options[0].FileID = "load-alias"
	if got := mustListMessages(t, restored, session.ID)[0]; !reflect.DeepEqual(got, want) {
		t.Fatal("loaded snapshot aliases caller")
	}
}

func TestPostgresConversationContextMigrationPreservesOriginalAndRollsBack(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	pool := openPostgresMigrationTestPool(t, dsn)
	defer pool.Close()
	migrations, err := loadPostgresMigrations()
	if err != nil {
		t.Fatal(err)
	}
	prior := migrations[:19]
	if err := runPostgresMigrationsWith(t.Context(), pool, prior, postgresMigrationHooks{}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO sessions (id,title) VALUES ('original-session','original title');
		INSERT INTO messages (id,session_id,role,content,attachments) VALUES ('original-message','original-session','user','original text','[{"name":"original.txt"}]');
		ALTER TABLE messages ADD COLUMN files_unavailable TEXT NOT NULL DEFAULT 'conflicting-existing-data'`); err != nil {
		t.Fatal(err)
	}
	if err := runPostgresMigrations(t.Context(), pool); err == nil {
		t.Fatal("conflicting migration succeeded")
	}
	var version, newColumns int
	var text, attachment, conflict string
	if err := pool.QueryRow(t.Context(), `SELECT max(version) FROM sparkclaw_schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='messages' AND column_name='clarification'`).Scan(&newColumns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT content, attachments->0->>'name', files_unavailable FROM messages WHERE id='original-message'`).Scan(&text, &attachment, &conflict); err != nil {
		t.Fatal(err)
	}
	if version != 19 || newColumns != 0 || text != "original text" || attachment != "original.txt" || conflict != "conflicting-existing-data" {
		t.Fatal("failed migration changed original data or version")
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE messages DROP COLUMN files_unavailable`); err != nil {
		t.Fatal(err)
	}
	if err := runPostgresMigrations(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if err := runPostgresMigrationsWith(t.Context(), pool, prior, postgresMigrationHooks{}); err == nil {
		t.Fatal("older migration set accepted a newer database")
	}
	st, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got := mustListMessages(t, st, "original-session")
	if len(got) != 1 || got[0].Content != "original text" || got[0].Attachments[0].Name != "original.txt" || got[0].Clarification != nil || got[0].FilesUnavailable {
		t.Fatalf("migrated message changed: %#v", got)
	}
}
