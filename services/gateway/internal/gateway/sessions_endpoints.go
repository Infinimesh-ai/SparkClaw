package gateway

import (
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Message string `json:"message"`
		Content string `json:"content"`
		System  string `json:"system"`
		Profile string `json:"profile"`
		Model   string `json:"model"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	content := strings.TrimSpace(input.Message)
	if content == "" {
		content = strings.TrimSpace(input.Content)
	}
	if content == "" {
		writeError(w, http.StatusBadRequest, errors.New("message is required"))
		return
	}
	profile := strings.TrimSpace(input.Profile)
	if profile == "" {
		profile = strings.TrimSpace(input.Model)
	}
	if profile == "" {
		profile = "fast"
	}
	system := strings.TrimSpace(input.System)
	if system == "" {
		system = "You are SparkClaw chat, a local-first model router endpoint. Answer directly and do not claim that tools were executed."
	}
	started := time.Now().UTC()
	result, err := s.models.ChatWithProfile(r.Context(), modelcapacity.OperationDirectChat, profile, system, content)
	completed := time.Now().UTC()
	if _, saveErr := s.store.SaveModelCall(r.Context(), modelCallFromChat("", "", "direct_chat", result, err, started, completed)); saveErr != nil {
		slog.Warn("direct chat model call persistence unavailable", "code", store.StoreErrorCodeOf(saveErr))
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": result.Content,
		"model":   result,
	})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	ownerID := queryOwnerID(r)
	sessions := []app.Session{}
	listed, err := s.store.ListSessions(r.Context())
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	for _, session := range listed {
		if sessionOwnerID(session) == ownerID {
			sessions = append(sessions, session)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title   string `json:"title"`
		OwnerID string `json:"owner_id"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ownerID := strings.TrimSpace(input.OwnerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	profile, ok, err := s.store.GetOwnerProfileByID(r.Context(), ownerID)
	if err != nil {
		writeOwnerStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusBadRequest, errors.New("profile not found"))
		return
	}
	session, err := s.store.CreateSessionWithScope(r.Context(), input.Title, profile.ID, profile.WorkspaceRoot, "webchat", false)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, ok, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) updateSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title string `json:"title"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	current, ok, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if ok && current.Source == "mcp" {
		writeError(w, http.StatusConflict, errors.New("MCP conversation titles are managed by the MCP binding"))
		return
	}
	session, err := s.store.UpdateSessionTitle(r.Context(), r.PathValue("id"), input.Title)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	current, ok, err := s.store.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if ok && current.Source == "mcp" {
		writeError(w, http.StatusConflict, errors.New("MCP conversations are managed by the MCP binding"))
		return
	}
	session, err := s.store.DeleteSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	messages, err := s.store.ListMessages(r.Context(), r.PathValue("id"))
	if err != nil {
		writeConversationError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	session, ok, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	if session.Source == "mcp" {
		writeError(w, http.StatusConflict, errors.New("MCP conversations receive requirements through their managed binding"))
		return
	}
	var input webMessageInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(input.Content) == "" && len(input.Attachments) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("content or an attachment is required"))
		return
	}
	var result agent.Result
	if input.Schedule != nil {
		ingress, ingressErr := s.webMessageIngress(r.Context(), r, session, "", input.ClientTimezone)
		if ingressErr != nil {
			writeError(w, http.StatusBadRequest, ingressErr)
			return
		}
		result, err = s.runtime.HandleScheduleActionWithIngress(r.Context(), sessionID, input.Content, input.Schedule.agentAction(), ingress)
	} else {
		ingress, ingressErr := s.webMessageIngress(r.Context(), r, session, input.TargetEndpointID, input.ClientTimezone)
		if ingressErr != nil {
			status := deliveryHTTPStatus(errorCode(ingressErr))
			if input.TargetEndpointID != "" && errorCode(ingressErr) == "" {
				status = http.StatusServiceUnavailable
			}
			writeError(w, status, ingressErr)
			return
		}
		result, err = s.runtime.HandleMessageWithIngress(r.Context(), sessionID, "", "", input.Content, sanitizeMessageAttachments(input.Attachments), ingress)
	}
	if err != nil {
		writeConversationError(w, http.StatusInternalServerError, err)
		return
	}
	if _, err := s.deliverAgentResult(r.Context(), result); err != nil {
		writeConversationError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

type scheduleActionInput struct {
	Operation         app.RouteOperation `json:"operation"`
	ScheduleID        string             `json:"schedule_id"`
	ExpectedUpdatedAt string             `json:"expected_updated_at"`
	Text              *string            `json:"text,omitempty"`
	DueTime           *string            `json:"due_time,omitempty"`
	Timezone          *string            `json:"timezone,omitempty"`
	Recurrence        *string            `json:"recurrence,omitempty"`
}

func (input scheduleActionInput) agentAction() agent.ScheduleAction {
	return agent.ScheduleAction{
		Operation: input.Operation, ScheduleID: input.ScheduleID, ExpectedUpdatedAt: input.ExpectedUpdatedAt,
		Text: input.Text, DueTime: input.DueTime, Timezone: input.Timezone, Recurrence: input.Recurrence,
	}
}

func (s *Server) postMessageStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	session, ok, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	if session.Source == "mcp" {
		writeError(w, http.StatusConflict, errors.New("MCP conversations receive requirements through their managed binding"))
		return
	}
	var input webMessageInput
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(input.Content) == "" && len(input.Attachments) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("content or an attachment is required"))
		return
	}
	ingress, err := s.webMessageIngress(r.Context(), r, session, input.TargetEndpointID, input.ClientTimezone)
	if err != nil {
		status := deliveryHTTPStatus(errorCode(err))
		if input.TargetEndpointID != "" && errorCode(err) == "" {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, err)
		return
	}
	initialEvents, err := s.store.EventsAfter(r.Context(), sessionID, "")
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	after := lastEventID(initialEvents)
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

	send := func(name string, value any) error {
		if err := writeNamedSSE(w, name, value); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	// Event name must stay in sync with MESSAGE_STREAM_STARTED_EVENT in
	// apps/webchat/src/lib/messageStream.ts: the webchat client keys its
	// accepted/not-accepted failure disposition on this exact string.
	if err := send("message.stream.started", map[string]string{"session_id": sessionID}); err != nil {
		return
	}
	attachments := sanitizeMessageAttachments(input.Attachments)

	type streamResult struct {
		result agent.Result
		err    error
		// deliveryErr reports a post-run delivery failure: the run itself
		// finished and its result is persisted, only handing it to the
		// selected external endpoint failed. It must stay separate from err
		// so the client can tell a real delivery failure apart from a benign
		// dropped stream on an accepted run.
		deliveryErr error
	}
	modelEvents := make(chan agent.StreamEvent, 16)
	results := make(chan streamResult, 1)
	executionCtx, finishExecution := s.detachedExecutionContext()
	s.streamWG.Add(1)
	go func() {
		defer s.streamWG.Done()
		defer finishExecution()
		result, err := s.streamMessage(executionCtx, sessionID, input.Content, attachments, ingress, func(event agent.StreamEvent) error {
			select {
			case <-r.Context().Done():
				return nil
			case modelEvents <- event:
				return nil
			}
		})
		var deliveryErr error
		if err == nil {
			_, deliveryErr = s.deliverAgentResult(executionCtx, result)
		}
		results <- streamResult{result: result, err: err, deliveryErr: deliveryErr}
		close(modelEvents)
	}()

	sendRuntimeEvents := func() bool {
		events, err := s.store.EventsAfter(r.Context(), sessionID, after)
		if err != nil {
			slog.Warn("message stream events unavailable", "session_id", sessionID, "code", store.StoreErrorCodeOf(err))
			return false
		}
		for _, event := range events {
			if event.ID == after {
				continue
			}
			after = event.ID
			if !streamVisibleEvent(event.Type) {
				continue
			}
			if err := send(event.Type, event); err != nil {
				return false
			}
		}
		return true
	}

	poll := time.NewTicker(200 * time.Millisecond)
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-modelEvents:
			if !ok {
				modelEvents = nil
				continue
			}
			name := strings.TrimSpace(event.Type)
			if name == "" {
				name = "model.event"
			}
			if err := send(name, event); err != nil {
				return
			}
		case <-poll.C:
			if !sendRuntimeEvents() {
				return
			}
		case <-heartbeat.C:
			if err := writeSSEHeartbeat(w); err != nil {
				return
			}
			flusher.Flush()
		case result := <-results:
			if !sendRuntimeEvents() {
				return
			}
			if result.err != nil {
				_ = send("error", map[string]string{"error": publicConversationError(result.err).Error(), "session_id": sessionID})
				return
			}
			if result.deliveryErr != nil {
				// Event name must stay in sync with
				// MESSAGE_STREAM_DELIVERY_FAILED_EVENT in
				// apps/webchat/src/lib/messageStream.ts: a plain "error"
				// event on an accepted stream would be presented as a benign
				// stream detach, hiding the delivery failure.
				_ = send("message.stream.delivery_failed", map[string]string{"error": publicConversationError(result.deliveryErr).Error(), "session_id": sessionID})
				return
			}
			_ = send("message.stream.final", result.result)
			return
		}
	}
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.EventsAfter(r.Context(), r.PathValue("id"), r.URL.Query().Get("after"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) streamSessionEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	_, ok, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("event streaming is unavailable"))
		return
	}
	after := r.URL.Query().Get("after")
	if after == "" {
		after = r.Header.Get("Last-Event-ID")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func() bool {
		events, err := s.store.EventsAfter(r.Context(), sessionID, after)
		if err != nil {
			slog.Warn("session events unavailable", "session_id", sessionID, "code", store.StoreErrorCodeOf(err))
			return false
		}
		for _, event := range events {
			if event.ID == after {
				continue
			}
			if err := writeSSEEvent(w, event); err != nil {
				return false
			}
			after = event.ID
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

func (s *Server) listSessionToolCalls(w http.ResponseWriter, r *http.Request) {
	toolCalls, err := s.store.ListToolCalls(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool_calls": toolCalls})
}

func (s *Server) listSessionAudit(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListAudit(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_events": events})
}

func (s *Server) listSessionEpisodes(w http.ResponseWriter, r *http.Request) {
	episodes, err := s.store.ListEpisodeSummaries(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": episodes})
}

func (s *Server) listSessionModelCalls(w http.ResponseWriter, r *http.Request) {
	modelCalls, err := s.store.ListModelCalls(r.Context(), r.PathValue("id"), r.URL.Query().Get("run_id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model_calls": modelCalls})
}

func (s *Server) listRunFeedback(w http.ResponseWriter, r *http.Request) {
	feedback, err := s.store.ListRunFeedback(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"feedback": feedback})
}

func (s *Server) saveRunFeedback(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	run, ok, err := s.store.GetRun(r.Context(), runID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("run not found"))
		return
	}
	var input struct {
		MessageID  string `json:"message_id"`
		Rating     string `json:"rating"`
		Note       string `json:"note"`
		Correction string `json:"correction"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rating := strings.TrimSpace(strings.ToLower(input.Rating))
	if rating != "up" && rating != "down" && rating != "corrected" {
		writeError(w, http.StatusBadRequest, errors.New("feedback rating must be up, down, or corrected"))
		return
	}
	feedback, err := s.store.SaveRunFeedback(r.Context(), app.RunFeedback{
		SessionID:  run.SessionID,
		RunID:      run.ID,
		MessageID:  strings.TrimSpace(input.MessageID),
		Rating:     rating,
		Note:       input.Note,
		Correction: input.Correction,
	})
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	s.refreshTrace(r.Context(), run.ID)
	writeJSON(w, http.StatusOK, feedback)
}

func sanitizeMessageAttachments(attachments []agent.MessageAttachment) []agent.MessageAttachment {
	out := []agent.MessageAttachment{}
	for _, attachment := range attachments {
		clean, ok := cleanWorkspaceRelativePath(attachment.RelPath)
		if !ok {
			continue
		}
		attachment.RelPath = filepath.ToSlash(clean)
		attachment.Name = strings.TrimSpace(attachment.Name)
		if attachment.Name == "" {
			attachment.Name = filepath.Base(clean)
		}
		attachment.ArtifactID = strings.TrimSpace(attachment.ArtifactID)
		attachment.URI = strings.TrimSpace(attachment.URI)
		attachment.ContentType = strings.TrimSpace(attachment.ContentType)
		out = append(out, attachment)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func writeSessionStoreError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("session request is invalid"))
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("session not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("session cannot be changed"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, errors.New("session request was canceled"))
	case store.StoreErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, errors.New("session operation timed out"))
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("session service is unavailable"))
	}
}

func writeConversationError(w http.ResponseWriter, fallbackStatus int, err error) {
	status, publicErr, ok := conversationErrorProjection(err)
	if !ok {
		writeError(w, fallbackStatus, err)
		return
	}
	writeError(w, status, publicErr)
}

func publicConversationError(err error) error {
	_, publicErr, ok := conversationErrorProjection(err)
	if ok {
		return publicErr
	}
	return err
}

func conversationErrorProjection(err error) (int, error, bool) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		return http.StatusBadRequest, errors.New("conversation request is invalid"), true
	case store.StoreErrorNotFound:
		return http.StatusNotFound, errors.New("conversation not found"), true
	case store.StoreErrorConflict:
		return http.StatusConflict, errors.New("conversation cannot be changed"), true
	case store.StoreErrorCanceled:
		return http.StatusRequestTimeout, errors.New("conversation request was canceled"), true
	case store.StoreErrorTimeout:
		return http.StatusGatewayTimeout, errors.New("conversation operation timed out"), true
	case store.StoreErrorUnavailable, store.StoreErrorDurability, store.StoreErrorUnknownOutcome,
		store.StoreErrorCorrupt, store.StoreErrorInternal:
		return http.StatusServiceUnavailable, errors.New("conversation service is unavailable"), true
	default:
		return 0, nil, false
	}
}
