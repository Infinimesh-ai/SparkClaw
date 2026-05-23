package personaldata

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestHTTPAdapters(t *testing.T) {
	var gotAuth string
	var gotEmailQuery string
	var gotCalendarFrom string
	var gotEmailSend EmailSendRequest
	var gotCalendarCreate CalendarEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/email/search":
			if r.Method != http.MethodGet {
				t.Fatalf("email search method = %s", r.Method)
			}
			gotEmailQuery = r.URL.Query().Get("query")
			writeJSON(w, map[string]any{"threads": []EmailThread{{
				ID:      "thread_1",
				Subject: "Deployment",
				From:    "alex@example.test",
				Messages: []EmailMessage{{
					Body: "Please review deployment.",
				}},
			}}})
		case "/email/threads/thread_1":
			if r.Method != http.MethodGet {
				t.Fatalf("email thread method = %s", r.Method)
			}
			writeJSON(w, map[string]any{"thread": EmailThread{ID: "thread_1", Subject: "Deployment"}})
		case "/email/send":
			if r.Method != http.MethodPost {
				t.Fatalf("email send method = %s", r.Method)
			}
			requireJSONBody(t, r.Body, &gotEmailSend)
			writeJSON(w, map[string]any{"result": EmailSendResult{ID: "send_1", Status: "sent", Adapter: "http", CreatedAt: "2026-05-22T00:00:00Z"}})
		case "/calendar/events":
			switch r.Method {
			case http.MethodGet:
				gotCalendarFrom = r.URL.Query().Get("from")
				writeJSON(w, map[string]any{"events": []CalendarEvent{{ID: "event_1", Title: "Standup"}}})
			case http.MethodPost:
				requireJSONBody(t, r.Body, &gotCalendarCreate)
				writeJSON(w, map[string]any{"event": CalendarEvent{ID: "event_2", Title: gotCalendarCreate.Title, Start: gotCalendarCreate.Start, End: gotCalendarCreate.End}})
			default:
				t.Fatalf("calendar method = %s", r.Method)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.ServiceAdapterConfig{Backend: "http", BaseURL: server.URL, Token: "adapter-token"}
	email := NewEmailAdapter(cfg, t.TempDir())
	threads, err := email.SearchThreads(t.Context(), "deployment", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 1 || gotEmailQuery != "deployment" || gotAuth != "Bearer adapter-token" {
		t.Fatalf("unexpected email adapter result: threads=%#v query=%q auth=%q", threads, gotEmailQuery, gotAuth)
	}
	thread, err := email.ReadThread(t.Context(), "thread_1")
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID != "thread_1" {
		t.Fatalf("unexpected thread: %#v", thread)
	}
	send, err := email.SendEmail(t.Context(), EmailSendRequest{To: []string{"owner@example.test"}, Subject: "Deployment", Body: "Ready"})
	if err != nil {
		t.Fatal(err)
	}
	if send.ID != "send_1" || gotEmailSend.Subject != "Deployment" || gotEmailSend.To[0] != "owner@example.test" {
		t.Fatalf("unexpected email send result=%#v request=%#v", send, gotEmailSend)
	}

	calendar := NewCalendarAdapter(cfg, t.TempDir())
	events, err := calendar.ReadEvents(t.Context(), "2026-05-22T00:00:00Z", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || gotCalendarFrom != "2026-05-22T00:00:00Z" {
		t.Fatalf("unexpected calendar adapter result: events=%#v from=%q", events, gotCalendarFrom)
	}
	created, err := calendar.CreateEvent(t.Context(), CalendarEvent{Title: "Demo", Start: "2026-05-23T10:00:00Z", End: "2026-05-23T10:30:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "event_2" || gotCalendarCreate.Title != "Demo" {
		t.Fatalf("unexpected calendar create result=%#v request=%#v", created, gotCalendarCreate)
	}
}

func requireJSONBody(t *testing.T, body io.Reader, out any) {
	t.Helper()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(body); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(buf.Bytes(), out); err != nil {
		t.Fatalf("decode request body: %v body=%s", err, buf.String())
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
