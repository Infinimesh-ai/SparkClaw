package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const (
	jingsiEventVersion     = "v0"
	jingsiEmptyCursorLabel = "jingsi-lan-v0-empty:"
)

var errJingSiLANDisabled = errors.New("JingSi LAN presentation is disabled")
var errJingSiSessionUnavailable = errors.New("configured JingSi LAN session is unavailable")

type jingSiSendInput struct {
	Content string `json:"content"`
}

type jingSiMessageView struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

type jingSiClientEvent struct {
	Cursor  string            `json:"cursor"`
	Type    string            `json:"type"`
	Message jingSiMessageView `json:"message"`
}

func (s *Server) readyJingSiLAN(w http.ResponseWriter, r *http.Request) {
	session, err := s.jingSiSession(r.Context())
	if errors.Is(err, errJingSiLANDisabled) {
		http.NotFound(w, nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if !queryKeysAllowed(r, nil) {
		writeError(w, http.StatusBadRequest, errors.New("unsupported query parameter"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"event_version": jingsiEventVersion,
		"session_ready": session.ID != "",
	})
}

func (s *Server) headJingSiEvents(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireJingSiSession(r.Context(), w)
	if !ok {
		return
	}
	if !queryKeysAllowed(r, nil) {
		writeError(w, http.StatusBadRequest, errors.New("unsupported query parameter"))
		return
	}
	cursor, err := s.store.MessageEventHead(r.Context(), session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("message event head is unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": jingsiEventVersion,
		"cursor":  publicJingSiCursor(session.ID, cursor),
	})
}

func (s *Server) listJingSiEvents(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireJingSiSession(r.Context(), w)
	if !ok {
		return
	}
	if !queryKeysAllowed(r, map[string]bool{"after": true, "limit": true}) {
		writeError(w, http.StatusBadRequest, errors.New("unsupported query parameter"))
		return
	}
	rawAfter := strings.TrimSpace(r.URL.Query().Get("after"))
	if rawAfter == "" {
		writeError(w, http.StatusBadRequest, errors.New("after cursor is required"))
		return
	}
	limit := store.MessageEventPageLimit
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > store.MessageEventPageLimit {
			writeError(w, http.StatusBadRequest, fmt.Errorf("limit must be between 1 and %d", store.MessageEventPageLimit))
			return
		}
		limit = parsed
	}

	page, err := s.store.MessageEventsAfter(r.Context(), session.ID, internalJingSiCursor(session.ID, rawAfter), limit)
	if errors.Is(err, store.ErrMessageEventCursorInvalid) {
		writeJingSiCursorReset(w)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("message event catch-up is unavailable"))
		return
	}
	events, err := s.projectJingSiEvents(page.Events)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("message event projection failed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":     jingsiEventVersion,
		"events":      events,
		"next_cursor": publicJingSiCursor(session.ID, page.NextCursor),
		"has_more":    page.HasMore,
	})
}

func (s *Server) streamJingSiEvents(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireJingSiSession(r.Context(), w)
	if !ok {
		return
	}
	if !queryKeysAllowed(r, map[string]bool{"after": true}) {
		writeError(w, http.StatusBadRequest, errors.New("unsupported query parameter"))
		return
	}
	rawAfter := strings.TrimSpace(r.URL.Query().Get("after"))
	headerAfter := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if rawAfter != "" && headerAfter != "" && rawAfter != headerAfter {
		writeError(w, http.StatusBadRequest, errors.New("after and Last-Event-ID must match"))
		return
	}
	if rawAfter == "" {
		rawAfter = headerAfter
	}
	if rawAfter == "" {
		head, err := s.store.MessageEventHead(r.Context(), session.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, errors.New("message event head is unavailable"))
			return
		}
		rawAfter = publicJingSiCursor(session.ID, head)
	}
	after := internalJingSiCursor(session.ID, rawAfter)
	if _, err := s.store.MessageEventsAfter(r.Context(), session.ID, after, 1); errors.Is(err, store.ErrMessageEventCursorInvalid) {
		writeJingSiCursorReset(w)
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, errors.New("message event stream is unavailable"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("message event streaming is unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func() bool {
		if _, err := s.jingSiSession(r.Context()); err != nil {
			return false
		}
		for {
			page, err := s.store.MessageEventsAfter(r.Context(), session.ID, after, store.MessageEventPageLimit)
			if err != nil {
				return false
			}
			for _, event := range page.Events {
				projected, visible, err := s.projectJingSiEvent(event)
				if err != nil {
					return false
				}
				after = event.ID
				if !visible {
					continue
				}
				if err := writeJingSiSSEEvent(w, projected); err != nil {
					return false
				}
			}
			if !page.HasMore {
				break
			}
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

func (s *Server) postJingSiMessageStream(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireJingSiSession(r.Context(), w)
	if !ok {
		return
	}
	if !queryKeysAllowed(r, nil) {
		writeError(w, http.StatusBadRequest, errors.New("unsupported query parameter"))
		return
	}
	input, ok := readJingSiSendInput(w, r, s.cfg.JingSiLAN.MaxMessageBytes)
	if !ok {
		return
	}
	ingress, err := s.webMessageIngress(r.Context(), r, session, "", "")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("Web message ingress is unavailable"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("message streaming is unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusCreated)
	if err := writeNamedSSE(w, "message.stream.started", map[string]bool{"accepted": true}); err != nil {
		return
	}
	flusher.Flush()

	type streamResult struct {
		result agent.Result
		err    error
	}
	results := make(chan streamResult, 1)
	executionCtx, finishExecution := s.detachedExecutionContext()
	s.streamWG.Add(1)
	go func() {
		defer s.streamWG.Done()
		defer finishExecution()
		result, err := s.streamMessage(executionCtx, session.ID, input.Content, nil, ingress, func(agent.StreamEvent) error { return nil })
		if err == nil {
			_, err = s.deliverAgentResult(executionCtx, result)
		}
		results <- streamResult{result: result, err: err}
	}()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case result := <-results:
			if result.err != nil {
				_ = writeNamedSSE(w, "error", map[string]string{"error": "message execution failed"})
				flusher.Flush()
				return
			}
			_ = writeNamedSSE(w, "message.stream.final", map[string]any{
				"completed":  true,
				"message_id": result.result.Message.ID,
			})
			flusher.Flush()
			return
		case <-heartbeat.C:
			if err := writeSSEHeartbeat(w); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func readJingSiSendInput(w http.ResponseWriter, r *http.Request, maxBytes int) (jingSiSendInput, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, errors.New("Content-Type must be application/json"))
		return jingSiSendInput{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes+1024))
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input jingSiSendInput
	if err := decoder.Decode(&input); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, errors.New("request body exceeds the JingSi LAN message limit"))
			return jingSiSendInput{}, false
		}
		writeError(w, http.StatusBadRequest, errors.New("invalid text message request"))
		return jingSiSendInput{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, errors.New("invalid text message request"))
		return jingSiSendInput{}, false
	}
	if strings.TrimSpace(input.Content) == "" {
		writeError(w, http.StatusBadRequest, errors.New("content is required"))
		return jingSiSendInput{}, false
	}
	if len([]byte(input.Content)) > maxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, errors.New("content exceeds the JingSi LAN message limit"))
		return jingSiSendInput{}, false
	}
	return input, true
}

func (s *Server) jingSiSession(ctx context.Context) (app.Session, error) {
	if !s.cfg.JingSiLAN.Enabled {
		return app.Session{}, errJingSiLANDisabled
	}
	session, ok, err := s.store.GetSession(ctx, strings.TrimSpace(s.cfg.JingSiLAN.SessionID))
	if err != nil {
		return app.Session{}, errJingSiSessionUnavailable
	}
	if !ok || session.Hidden || sessionOwnerID(session) != app.DefaultOwnerID || strings.TrimSpace(session.Source) != "webchat" {
		return app.Session{}, errJingSiSessionUnavailable
	}
	return session, nil
}

func (s *Server) requireJingSiSession(ctx context.Context, w http.ResponseWriter) (app.Session, bool) {
	session, err := s.jingSiSession(ctx)
	if errors.Is(err, errJingSiLANDisabled) {
		http.NotFound(w, nil)
		return app.Session{}, false
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return app.Session{}, false
	}
	return session, true
}

func (s *Server) projectJingSiEvents(events []app.Event) ([]jingSiClientEvent, error) {
	projected := make([]jingSiClientEvent, 0, len(events))
	for _, event := range events {
		view, visible, err := s.projectJingSiEvent(event)
		if err != nil {
			return nil, err
		}
		if visible {
			projected = append(projected, view)
		}
	}
	return projected, nil
}

func (s *Server) projectJingSiEvent(event app.Event) (jingSiClientEvent, bool, error) {
	if event.Type != "message.created" {
		return jingSiClientEvent{}, false, nil
	}
	var message app.Message
	switch payload := event.Payload.(type) {
	case app.Message:
		message = payload
	case *app.Message:
		if payload == nil {
			return jingSiClientEvent{}, false, errors.New("message event payload is empty")
		}
		message = *payload
	default:
		raw, err := json.Marshal(payload)
		if err != nil {
			return jingSiClientEvent{}, false, err
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			return jingSiClientEvent{}, false, err
		}
	}
	if message.ID == "" || message.SessionID != event.SessionID {
		return jingSiClientEvent{}, false, errors.New("message event payload does not match the configured session")
	}
	role := strings.ToLower(strings.TrimSpace(message.Role))
	if role != "user" && role != "assistant" {
		return jingSiClientEvent{}, false, nil
	}
	text := message.Content
	if strings.TrimSpace(text) == "" {
		text = "[Unsupported non-text message]"
	}
	if len([]byte(text)) > s.cfg.JingSiLAN.MaxMessageBytes {
		text = "[Message exceeds the JingSi display limit]"
	}
	return jingSiClientEvent{
		Cursor: event.ID,
		Type:   "message.created",
		Message: jingSiMessageView{
			ID: message.ID, Role: role, Text: text, CreatedAt: message.CreatedAt,
		},
	}, true, nil
}

func writeJingSiSSEEvent(w io.Writer, event jingSiClientEvent) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %s\n", event.Cursor); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "event: message.created\n"); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", raw)
	return err
}

func writeJingSiCursorReset(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": "message event cursor is not valid for the configured session",
		"code":  "cursor_reset_required",
	})
}

func publicJingSiCursor(sessionID, cursor string) string {
	if cursor == "" {
		digest := sha256.Sum256([]byte(jingsiEmptyCursorLabel + sessionID))
		return fmt.Sprintf("ce_%x", digest[:16])
	}
	return cursor
}

func internalJingSiCursor(sessionID, cursor string) string {
	if cursor == publicJingSiCursor(sessionID, "") {
		return ""
	}
	return cursor
}

func queryKeysAllowed(r *http.Request, allowed map[string]bool) bool {
	for key, values := range r.URL.Query() {
		if allowed == nil || !allowed[key] || len(values) != 1 {
			return false
		}
	}
	return true
}
