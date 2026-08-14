package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestMemoryMessageEventsAreBoundedAndSessionScoped(t *testing.T) {
	st := NewMemoryStore()
	firstSession := st.CreateSession("first")
	secondSession := st.CreateSession("second")
	first := st.AddMessage(app.Message{SessionID: firstSession.ID, Role: "user", Content: "one"})
	st.AddMessage(app.Message{SessionID: secondSession.ID, Role: "assistant", Content: "other"})
	second := st.AddMessage(app.Message{SessionID: firstSession.ID, Role: "assistant", Content: "two"})

	page, err := st.MessageEventsAfter(firstSession.ID, "", 1)
	if err != nil || len(page.Events) != 1 || !page.HasMore || page.NextCursor != page.Events[0].ID {
		t.Fatalf("unexpected first page: %#v err=%v", page, err)
	}
	if message := messageFromEvent(t, page.Events[0]); message.ID != first.ID {
		t.Fatalf("first page returned message %q, want %q", message.ID, first.ID)
	}
	page, err = st.MessageEventsAfter(firstSession.ID, page.NextCursor, 1)
	if err != nil || len(page.Events) != 1 || page.HasMore {
		t.Fatalf("unexpected second page: %#v err=%v", page, err)
	}
	if message := messageFromEvent(t, page.Events[0]); message.ID != second.ID {
		t.Fatalf("second page returned message %q, want %q", message.ID, second.ID)
	}
	head, err := st.MessageEventHead(firstSession.ID)
	if err != nil || head != page.Events[0].ID {
		t.Fatalf("message head = %q, want %q, err=%v", head, page.Events[0].ID, err)
	}
	otherHead, err := st.MessageEventHead(secondSession.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MessageEventsAfter(firstSession.ID, otherHead, 10); !errors.Is(err, ErrMessageEventCursorInvalid) {
		t.Fatalf("wrong-session cursor error = %v", err)
	}
	if _, err := st.MessageEventsAfter(firstSession.ID, "evt_missing", 10); !errors.Is(err, ErrMessageEventCursorInvalid) {
		t.Fatalf("unknown cursor error = %v", err)
	}

	for index := 0; index < MessageEventPageLimit; index++ {
		st.AddMessage(app.Message{SessionID: firstSession.ID, Role: "assistant", Content: "bounded"})
	}
	page, err = st.MessageEventsAfter(firstSession.ID, "", MessageEventPageLimit+1)
	if err != nil || len(page.Events) != MessageEventPageLimit || !page.HasMore {
		t.Fatalf("oversized limit was not clamped: len=%d has_more=%v err=%v", len(page.Events), page.HasMore, err)
	}
}

func TestFileMessageEventsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := st.CreateSession("file events")
	message := st.AddMessage(app.Message{SessionID: session.ID, Role: "assistant", Content: "persisted"})
	head, err := st.MessageEventHead(session.ID)
	if err != nil || head == "" {
		t.Fatalf("head = %q err=%v", head, err)
	}

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reloaded.MessageEventsAfter(session.ID, "", 100)
	if err != nil || len(page.Events) != 1 || page.NextCursor != head {
		t.Fatalf("reloaded page = %#v err=%v", page, err)
	}
	if got := messageFromEvent(t, page.Events[0]); got.ID != message.ID || got.Content != message.Content {
		t.Fatalf("reloaded message = %#v", got)
	}
}

func messageFromEvent(t *testing.T, event app.Event) app.Message {
	t.Helper()
	if message, ok := event.Payload.(app.Message); ok {
		return message
	}
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var message app.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("decode event payload %T: %v", event.Payload, err)
	}
	return message
}
