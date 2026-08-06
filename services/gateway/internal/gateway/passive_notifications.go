package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type passiveNotificationView struct {
	ID             string     `json:"id"`
	NotificationID string     `json:"notification_id"`
	Source         string     `json:"source"`
	Kind           string     `json:"kind"`
	DeepLink       string     `json:"deep_link"`
	OccurredAt     time.Time  `json:"occurred_at"`
	ReadAt         *time.Time `json:"read_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (s *Server) listPassiveNotifications(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	records := s.store.ListPassiveNotifications(principal.OwnerID, "", queryInt(r, "limit", 100))
	views := make([]passiveNotificationView, 0, len(records))
	for _, record := range records {
		views = append(views, publicPassiveNotification(record))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": views,
		"unread_count":  s.store.CountUnreadPassiveNotifications(principal.OwnerID),
	})
}

func (s *Server) markPassiveNotificationRead(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	notification, err := s.store.MarkPassiveNotificationRead(principal.OwnerID, r.PathValue("id"), time.Now().UTC())
	if errors.Is(err, store.ErrPassiveNotificationNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("notification read state could not be persisted"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notification": publicPassiveNotification(notification),
		"unread_count": s.store.CountUnreadPassiveNotifications(principal.OwnerID),
	})
}

func (s *Server) markAllPassiveNotificationsRead(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	count, err := s.store.MarkAllPassiveNotificationsRead(principal.OwnerID, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("notification read state could not be persisted"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": count, "unread_count": 0})
}

func (s *Server) streamPassiveNotifications(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("notification streaming is unavailable"))
		return
	}
	ownerID := principalForRequest(r).OwnerID
	after := r.URL.Query().Get("after")
	if after == "" {
		after = r.Header.Get("Last-Event-ID")
	}
	if after == "" {
		if current := s.store.ListPassiveNotifications(ownerID, "", 1); len(current) > 0 {
			after = current[0].ID
		}
	} else if _, ok := s.store.GetPassiveNotification(ownerID, after); !ok {
		writeError(w, http.StatusConflict, errors.New("notification cursor is not valid for this owner"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func() bool {
		notifications := s.store.ListPassiveNotifications(ownerID, after, 100)
		if after == "" {
			for left, right := 0, len(notifications)-1; left < right; left, right = left+1, right-1 {
				notifications[left], notifications[right] = notifications[right], notifications[left]
			}
		}
		for _, notification := range notifications {
			if err := writePassiveNotificationSSE(w, notification); err != nil {
				return false
			}
			after = notification.ID
		}
		flusher.Flush()
		return true
	}
	if !send() {
		return
	}
	poll := time.NewTicker(750 * time.Millisecond)
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			if !send() {
				return
			}
		case <-heartbeat.C:
			if err := writeSSEHeartbeat(w); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func publicPassiveNotification(notification app.PassiveNotification) passiveNotificationView {
	return passiveNotificationView{
		ID: notification.ID, NotificationID: notification.NotificationID,
		Source: notification.Source, Kind: notification.Kind, DeepLink: notification.DeepLink,
		OccurredAt: notification.OccurredAt, ReadAt: notification.ReadAt,
		CreatedAt: notification.CreatedAt, UpdatedAt: notification.UpdatedAt,
	}
}

func writePassiveNotificationSSE(w http.ResponseWriter, notification app.PassiveNotification) error {
	raw, err := json.Marshal(publicPassiveNotification(notification))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %s\n", notification.ID); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "event: notification.created"); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", raw)
	return err
}
