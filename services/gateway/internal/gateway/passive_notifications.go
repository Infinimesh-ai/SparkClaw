package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// maxPassiveNotificationStreamsPerOwner bounds concurrent SSE inbox streams
// per owner; each stream costs a poll loop, and a single owner has no
// legitimate need for more than a handful of live tabs.
const maxPassiveNotificationStreamsPerOwner = 4

// passiveNotificationPollInterval is a variable so tests can shrink it.
var passiveNotificationPollInterval = 750 * time.Millisecond

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

// applyPassiveNotificationRetention enforces the configured retention window
// and per-owner cap. The prune is destructive, so it runs only from the
// background retention coordinator (StartRetentionSweeps), never from
// request handlers.
func (s *Server) applyPassiveNotificationRetention(ctx context.Context) error {
	maxPerOwner := s.cfg.PassiveNotifications.MaxPerOwner
	cutoff := time.Time{}
	if days := s.cfg.PassiveNotifications.RetentionDays; days > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -days)
	}
	if cutoff.IsZero() && maxPerOwner <= 0 {
		return nil
	}
	_, err := s.store.PrunePassiveNotifications(ctx, cutoff, maxPerOwner)
	return err
}

func (s *Server) listPassiveNotifications(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	records, err := s.store.ListPassiveNotifications(r.Context(), principal.OwnerID, "", queryInt(r, "limit", 100))
	if err != nil {
		writePassiveNotificationStoreError(w, err)
		return
	}
	unread, err := s.store.CountUnreadPassiveNotifications(r.Context(), principal.OwnerID)
	if err != nil {
		writePassiveNotificationStoreError(w, err)
		return
	}
	views := make([]passiveNotificationView, 0, len(records))
	for _, record := range records {
		views = append(views, publicPassiveNotification(record))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": views,
		"unread_count":  unread,
	})
}

func (s *Server) markPassiveNotificationRead(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	notification, err := s.store.MarkPassiveNotificationRead(r.Context(), principal.OwnerID, r.PathValue("id"), time.Now().UTC())
	if errors.Is(err, store.ErrPassiveNotificationNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writePassiveNotificationStoreError(w, err)
		return
	}
	unread, err := s.store.CountUnreadPassiveNotifications(r.Context(), principal.OwnerID)
	if err != nil {
		writePassiveNotificationStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"notification": publicPassiveNotification(notification),
		"unread_count": unread,
	})
}

func (s *Server) markAllPassiveNotificationsRead(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	count, err := s.store.MarkAllPassiveNotificationsRead(r.Context(), principal.OwnerID, time.Now().UTC())
	if err != nil {
		writePassiveNotificationStoreError(w, err)
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
	if !s.acquirePassiveNotificationStream(ownerID) {
		writeError(w, http.StatusTooManyRequests, errors.New("too many concurrent notification streams for this owner"))
		return
	}
	defer s.releasePassiveNotificationStream(ownerID)
	after := r.URL.Query().Get("after")
	if after == "" {
		after = r.Header.Get("Last-Event-ID")
	}
	if after == "" {
		current, err := s.store.ListPassiveNotifications(r.Context(), ownerID, "", 1)
		if err != nil {
			writePassiveNotificationStoreError(w, err)
			return
		}
		if len(current) > 0 {
			after = current[0].ID
		}
	} else {
		_, ok, err := s.store.GetPassiveNotification(r.Context(), ownerID, after)
		if err != nil {
			writePassiveNotificationStoreError(w, err)
			return
		}
		if !ok {
			writeError(w, http.StatusConflict, errors.New("notification cursor is not valid for this owner"))
			return
		}
	}
	lastRevision, err := s.store.PassiveNotificationRevision(r.Context(), ownerID)
	if err != nil {
		writePassiveNotificationStoreError(w, err)
		return
	}
	initial, err := s.store.ListPassiveNotifications(r.Context(), ownerID, after, 100)
	if err != nil {
		writePassiveNotificationStoreError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func(notifications []app.PassiveNotification) bool {
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
	// Reading the revision before listing means a change racing the send is
	// caught on the next tick instead of lost; the cursor keeps re-sends
	// duplicate-free.
	if !send(initial) {
		return
	}
	poll := time.NewTicker(passiveNotificationPollInterval)
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			revision, err := s.store.PassiveNotificationRevision(r.Context(), ownerID)
			if err != nil {
				return
			}
			if revision == lastRevision {
				continue
			}
			lastRevision = revision
			notifications, err := s.store.ListPassiveNotifications(r.Context(), ownerID, after, 100)
			if err != nil || !send(notifications) {
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

func writePassiveNotificationStoreError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("notification request is invalid"))
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("notification was not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("notification conflicts with existing state"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, errors.New("notification request was canceled"))
	case store.StoreErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, errors.New("notification operation timed out"))
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("notification service is unavailable"))
	}
}

func (s *Server) acquirePassiveNotificationStream(ownerID string) bool {
	s.passiveStreamMu.Lock()
	defer s.passiveStreamMu.Unlock()
	if s.passiveStreams[ownerID] >= maxPassiveNotificationStreamsPerOwner {
		return false
	}
	s.passiveStreams[ownerID]++
	return true
}

func (s *Server) releasePassiveNotificationStream(ownerID string) {
	s.passiveStreamMu.Lock()
	defer s.passiveStreamMu.Unlock()
	if s.passiveStreams[ownerID] <= 1 {
		delete(s.passiveStreams, ownerID)
	} else {
		s.passiveStreams[ownerID]--
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
