package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type conversationRepositoryHarness struct {
	store   Store
	restart func(testing.TB) Store
}

func newConversationRepositoryHarness(t testing.TB, backend string) conversationRepositoryHarness {
	t.Helper()
	switch backend {
	case "memory":
		return conversationRepositoryHarness{store: NewMemoryStore()}
	case "file":
		path := filepath.Join(t.TempDir(), "state.json")
		file, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		return conversationRepositoryHarness{
			store: file,
			restart: func(t testing.TB) Store {
				t.Helper()
				restarted, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				return restarted
			},
		}
	default:
		t.Fatalf("unknown backend %q", backend)
		return conversationRepositoryHarness{}
	}
}

func TestConversationRepositoryMemoryAndFileContract(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			harness := newConversationRepositoryHarness(t, backend)
			session := mustCreateSession(t, harness.store, "New SparkClaw Session")
			other := mustCreateSession(t, harness.store, "other")
			createdAt := time.Date(2026, 8, 21, 9, 30, 0, 123456789, time.FixedZone("test", 8*60*60))
			input := app.Message{
				ID: "message-b", SessionID: session.ID, RunID: "run-1", Role: "user", Content: "first request",
				CreatedAt:      createdAt,
				Attachments:    []app.MessageAttachment{{Name: "input.txt"}},
				RequestedMedia: []app.MessageMediaLocator{{Query: "report"}},
			}
			stored, err := harness.store.AddMessage(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if stored.CreatedAt.Location() != time.UTC || stored.CreatedAt.Nanosecond() != 123456000 {
				t.Fatalf("normalized message time = %s", stored.CreatedAt)
			}
			input.Attachments[0].Name = "mutated input"
			stored.RequestedMedia[0].Query = "mutated output"

			_, err = harness.store.AddMessage(t.Context(), app.Message{
				ID: "message-a", SessionID: session.ID, Role: "assistant", Content: "second",
				CreatedAt: stored.CreatedAt,
			})
			if err != nil {
				t.Fatal(err)
			}
			messages, err := harness.store.ListMessages(t.Context(), session.ID)
			if err != nil || len(messages) != 2 || messages[0].ID != "message-a" || messages[1].ID != "message-b" {
				t.Fatalf("deterministic messages = %#v err=%v", messages, err)
			}
			if messages[1].Attachments[0].Name != "input.txt" || messages[1].RequestedMedia[0].Query != "report" {
				t.Fatalf("message aliases escaped = %#v", messages[1])
			}
			messages[1].Attachments[0].Name = "mutated list"
			if again := mustListMessages(t, harness.store, session.ID); again[1].Attachments[0].Name != "input.txt" {
				t.Fatalf("list result mutated backend state = %#v", again[1])
			}

			updated, found, err := harness.store.GetSession(t.Context(), session.ID)
			if err != nil || !found || updated.Title != "first request" || !updated.UpdatedAt.After(session.UpdatedAt) {
				t.Fatalf("message/session atomic update = %#v found=%v err=%v", updated, found, err)
			}
			page, err := harness.store.MessageEventsAfter(t.Context(), session.ID, "", 10)
			if err != nil || len(page.Events) != 2 || page.NextCursor != page.Events[1].ID || page.HasMore {
				t.Fatalf("message event page = %#v err=%v", page, err)
			}
			payload, ok := page.Events[0].Payload.(app.Message)
			if !ok || payload.ID != "message-b" {
				t.Fatalf("message event payload = %#v", page.Events[0].Payload)
			}
			payload.Attachments[0].Name = "mutated event"
			againPage := mustMessageEventsAfter(t, harness.store, session.ID, "", 10)
			if messageFromEvent(t, againPage.Events[0]).Attachments[0].Name != "input.txt" {
				t.Fatalf("event payload mutated backend state = %#v", againPage.Events[0].Payload)
			}

			reused, err := harness.store.AddMessage(t.Context(), app.Message{
				ID: stored.ID, SessionID: other.ID, Role: "assistant", Content: "replacement",
			})
			if err != nil || reused.SessionID != session.ID || reused.Content != "first request" {
				t.Fatalf("duplicate ID reuse = %#v err=%v", reused, err)
			}
			if otherMessages := mustListMessages(t, harness.store, other.ID); len(otherMessages) != 0 {
				t.Fatalf("duplicate ID created another message = %#v", otherMessages)
			}

			if _, err := harness.store.AddMessage(t.Context(), app.Message{SessionID: "missing", Role: "user"}); StoreErrorCodeOf(err) != StoreErrorNotFound {
				t.Fatalf("missing session error = %v", err)
			}
			if _, err := harness.store.MessageEventsAfter(t.Context(), session.ID, "missing-cursor", 10); !errors.Is(err, ErrMessageEventCursorInvalid) || StoreErrorCodeOf(err) != StoreErrorInvalid {
				t.Fatalf("invalid cursor error = %v code=%q", err, StoreErrorCodeOf(err))
			}
			if missing, err := harness.store.ListMessages(t.Context(), "missing"); err != nil || missing == nil || len(missing) != 0 {
				t.Fatalf("normal absence = %#v err=%v", missing, err)
			}

			if harness.restart != nil {
				restarted := harness.restart(t)
				persisted := mustListMessages(t, restarted, session.ID)
				persistedPage := mustMessageEventsAfter(t, restarted, session.ID, "", 10)
				if len(persisted) != 2 || len(persistedPage.Events) != 2 {
					t.Fatalf("restart messages=%#v events=%#v", persisted, persistedPage)
				}
			}
		})
	}
}

func TestConversationRepositoryHonorsCancellation(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			store := newConversationRepositoryHarness(t, backend).store
			session := mustCreateSession(t, store, "cancel")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := store.AddMessage(ctx, app.Message{SessionID: session.ID}); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("canceled add error = %v", err)
			}
			if _, err := store.ListMessages(ctx, session.ID); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("canceled list error = %v", err)
			}
			if _, err := store.MessageEventHead(ctx, session.ID); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("canceled head error = %v", err)
			}
			if _, err := store.MessageEventsAfter(ctx, session.ID, "", 10); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("canceled events error = %v", err)
			}
			if messages := mustListMessages(t, store, session.ID); len(messages) != 0 {
				t.Fatalf("canceled add mutated state = %#v", messages)
			}
		})
	}
}
