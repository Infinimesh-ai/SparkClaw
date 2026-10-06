package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	workbenchEventPollInterval = 250 * time.Millisecond
	workbenchEventQueueSize    = 64
	workbenchEventMaxPerOwner  = 8
)

type workbenchInvalidation struct {
	SchemaVersion int    `json:"schema_version"`
	Epoch         string `json:"epoch"`
	Sequence      uint64 `json:"sequence"`
	Category      string `json:"category"`
	ResourceID    string `json:"resource_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type workbenchEventSubscriber struct {
	ownerID string
	events  chan workbenchInvalidation
}

type workbenchEventHub struct {
	mu          sync.Mutex
	epoch       string
	sequence    uint64
	nextID      uint64
	subscribers map[uint64]workbenchEventSubscriber
}

func newWorkbenchEventHub() *workbenchEventHub {
	return &workbenchEventHub{epoch: app.NewID("workbench_epoch"), subscribers: map[uint64]workbenchEventSubscriber{}}
}

func (h *workbenchEventHub) subscribe(ownerID string) (uint64, <-chan workbenchInvalidation, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, subscriber := range h.subscribers {
		if subscriber.ownerID == ownerID {
			count++
		}
	}
	if count >= workbenchEventMaxPerOwner {
		return 0, nil, false
	}
	h.nextID++
	id := h.nextID
	channel := make(chan workbenchInvalidation, workbenchEventQueueSize)
	h.subscribers[id] = workbenchEventSubscriber{ownerID: ownerID, events: channel}
	channel <- workbenchInvalidation{
		SchemaVersion: 1, Epoch: h.epoch, Sequence: h.sequence, Category: "all", Reason: "resync",
	}
	return id, channel, true
}

func (h *workbenchEventHub) unsubscribe(id uint64) {
	h.mu.Lock()
	delete(h.subscribers, id)
	h.mu.Unlock()
}

func (h *workbenchEventHub) publish(ownerID, category, resourceID string) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sequence++
	event := workbenchInvalidation{
		SchemaVersion: 1, Epoch: h.epoch, Sequence: h.sequence,
		Category: strings.TrimSpace(category), ResourceID: strings.TrimSpace(resourceID),
	}
	for _, subscriber := range h.subscribers {
		if subscriber.ownerID != ownerID {
			continue
		}
		select {
		case subscriber.events <- event:
		default:
			for len(subscriber.events) > 0 {
				<-subscriber.events
			}
			subscriber.events <- workbenchInvalidation{
				SchemaVersion: 1, Epoch: h.epoch, Sequence: h.sequence, Category: "all", Reason: "resync",
			}
		}
	}
}

func (h *workbenchEventHub) resyncAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sequence++
	for _, subscriber := range h.subscribers {
		for len(subscriber.events) > 0 {
			<-subscriber.events
		}
		subscriber.events <- workbenchInvalidation{
			SchemaVersion: 1, Epoch: h.epoch, Sequence: h.sequence, Category: "all", Reason: "resync",
		}
	}
}

func (h *workbenchEventHub) ownerIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]bool{}
	owners := make([]string, 0, len(h.subscribers))
	for _, subscriber := range h.subscribers {
		if !seen[subscriber.ownerID] {
			seen[subscriber.ownerID] = true
			owners = append(owners, subscriber.ownerID)
		}
	}
	return owners
}

func (s *Server) streamWorkbenchEvents(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	if !principal.Authenticated || (strings.TrimSpace(principal.ClientID) == "" && principal.LocalAccessID == "") {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("a Client bearer token is required"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming is unavailable"))
		return
	}
	id, events, admitted := s.workbenchEvents.subscribe(principal.OwnerID)
	if !admitted {
		writeError(w, http.StatusTooManyRequests, fmt.Errorf("too many concurrent workbench event streams for this Owner"))
		return
	}
	defer s.workbenchEvents.unsubscribe(id)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			if r.Context().Err() != nil {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "event: invalidation\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) startWorkbenchEventMonitor(ctx context.Context) {
	s.workbenchEventMonitorOnce.Do(func() {
		go s.runWorkbenchEventMonitor(ctx)
	})
}

func (s *Server) runWorkbenchEventMonitor(ctx context.Context) {
	cursor := ""
	notificationRevisions := map[string]uint64{}
	initial, err := s.store.EventsAfter(ctx, "", "")
	initialized := err == nil
	if err == nil && len(initial) > 0 {
		cursor = initial[len(initial)-1].ID
	}
	readFailed := false
	ticker := time.NewTicker(workbenchEventPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, ownerID := range s.workbenchEvents.ownerIDs() {
				revision, revisionErr := s.store.PassiveNotificationRevision(ctx, ownerID)
				if revisionErr != nil {
					continue
				}
				previous := notificationRevisions[ownerID]
				notificationRevisions[ownerID] = revision
				if revision != previous {
					s.workbenchEvents.publish(ownerID, "notifications", "")
				}
			}
			events, readErr := s.store.EventsAfter(ctx, "", cursor)
			if readErr != nil {
				if !readFailed {
					slog.Warn("workbench invalidation monitor could not read events", "error", readErr)
					s.workbenchEvents.resyncAll()
				}
				readFailed = true
				continue
			}
			readFailed = false
			if !initialized {
				if len(events) > 0 {
					cursor = events[len(events)-1].ID
				}
				initialized = true
				s.workbenchEvents.resyncAll()
				continue
			}
			for _, event := range events {
				if event.Type == "client.revoked" {
					s.cancelClientConnections(workbenchEventResourceID(event))
				}
				ownerID := workbenchEventOwner(event)
				if ownerID == "" && event.SessionID != "" {
					session, found, sessionErr := s.store.GetSession(ctx, event.SessionID)
					if sessionErr == nil && found {
						ownerID = session.OwnerID
					}
				}
				if ownerID == "" {
					if delivery, ok := event.Payload.(app.ReminderDelivery); ok {
						reminder, found, reminderErr := s.store.GetReminder(ctx, delivery.ReminderID)
						if reminderErr == nil && found {
							session, sessionFound, sessionErr := s.store.GetSession(ctx, reminder.SessionID)
							if sessionErr == nil && sessionFound {
								ownerID = sessionOwnerID(session)
							}
						}
					}
				}
				if ownerID != "" {
					s.workbenchEvents.publish(ownerID, workbenchEventCategory(event.Type), workbenchEventResourceID(event))
				}
				cursor = event.ID
			}
		}
	}
}

func workbenchEventOwner(event app.Event) string {
	if profile, ok := event.Payload.(app.OwnerProfile); ok {
		return strings.TrimSpace(profile.ID)
	}
	value := reflect.ValueOf(event.Payload)
	if value.IsValid() {
		if value.Kind() == reflect.Pointer && !value.IsNil() {
			value = value.Elem()
		}
		if value.Kind() == reflect.Struct {
			field := value.FieldByName("OwnerID")
			if field.IsValid() && field.Kind() == reflect.String {
				return strings.TrimSpace(field.String())
			}
		}
	}
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		return ""
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	ownerID, _ := object["owner_id"].(string)
	return strings.TrimSpace(ownerID)
}

func workbenchEventCategory(eventType string) string {
	prefix := strings.SplitN(strings.TrimSpace(eventType), ".", 2)[0]
	switch prefix {
	case "session":
		return "sessions"
	case "message", "episode_summary":
		return "conversation"
	case "run", "run_feedback", "model_call", "tool_call":
		return "tasks"
	case "approval":
		return "approvals"
	case "memory", "memory_candidate":
		return "memories"
	case "reminder", "reminder_delivery":
		return "schedules"
	case "notification", "passive_notification":
		return "notifications"
	case "email", "email_mail", "email_conversation", "email_draft", "email_sync":
		return "email"
	case "client":
		return "clients"
	case "owner_profile", "connector", "notification_binding":
		return "settings"
	case "artifact", "document":
		return "files"
	case "eval":
		return "evaluations"
	default:
		return "shared"
	}
}

func workbenchEventResourceID(event app.Event) string {
	if event.SessionID != "" {
		return event.SessionID
	}
	value := reflect.ValueOf(event.Payload)
	if value.IsValid() {
		if value.Kind() == reflect.Pointer && !value.IsNil() {
			value = value.Elem()
		}
		if value.Kind() == reflect.Struct {
			field := value.FieldByName("ID")
			if field.IsValid() && field.Kind() == reflect.String {
				return strings.TrimSpace(field.String())
			}
		}
	}
	if object, ok := event.Payload.(map[string]any); ok {
		if id, ok := object["id"].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}
