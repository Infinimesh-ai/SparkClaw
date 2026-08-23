package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type publicSchedule struct {
	ID         app.ScheduleID         `json:"id"`
	SessionID  string                 `json:"session_id,omitempty"`
	Title      string                 `json:"title"`
	Text       string                 `json:"text"`
	DueTime    time.Time              `json:"due_time"`
	Timezone   string                 `json:"timezone"`
	Recurrence string                 `json:"recurrence,omitempty"`
	Status     string                 `json:"status"`
	UpdatedAt  time.Time              `json:"updated_at"`
	Editable   bool                   `json:"editable"`
	Cancelable bool                   `json:"cancelable"`
	Endpoint   publicScheduleEndpoint `json:"endpoint"`
}

type publicScheduleEndpoint struct {
	Kind                 app.EndpointKind `json:"kind,omitempty"`
	Channel              string           `json:"channel,omitempty"`
	SoftwareDisplayName  string           `json:"software_display_name,omitempty"`
	AccountDisplayName   string           `json:"account_display_name,omitempty"`
	RecipientDisplayName string           `json:"recipient_display_name,omitempty"`
	ConversationLabel    string           `json:"conversation_label,omitempty"`
	Status               string           `json:"status"`
}

func (s *Server) listCurrentSchedules(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	schedules, err := messagecontrol.NewScheduleRegistry(s.store).List(r.Context(), app.ReminderFilter{})
	if err != nil {
		writeScheduleStoreError(w, err)
		return
	}
	out := make([]publicSchedule, 0, len(schedules))
	for _, schedule := range schedules {
		if schedule.Status != "pending" && schedule.Status != "sending" {
			continue
		}
		if schedule.Spec.OwnerID != principal.OwnerID || schedule.Spec.ActorID != principal.ActorID {
			continue
		}
		endpoint := s.publicScheduleEndpoint(r, schedule)
		text := scheduleText(schedule.Spec.Payload.Content)
		out = append(out, publicSchedule{
			ID: schedule.ID, SessionID: schedule.SessionID, Title: scheduleTitle(schedule.Spec.Payload.Content),
			DueTime: schedule.DueTime, Timezone: schedule.Timezone, Recurrence: schedule.Recurrence,
			Status: schedule.Status, Text: text, UpdatedAt: schedule.UpdatedAt, Endpoint: endpoint,
			Editable:   schedule.Status == "pending" && endpoint.Status == string(app.EndpointActive),
			Cancelable: schedule.Status == "pending",
		})
		if len(out) == 100 {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": out})
}

func writeScheduleStoreError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("schedule request is invalid"))
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("schedule record not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("schedule changed or is no longer available"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, errors.New("schedule request was canceled"))
	case store.StoreErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, errors.New("schedule operation timed out"))
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("schedule service is unavailable"))
	}
}

func (s *Server) publicScheduleEndpoint(r *http.Request, schedule app.MessageSchedule) publicScheduleEndpoint {
	route := schedule.Spec.ReturnRoute
	if route.Mode == app.ReturnNowhere {
		return publicScheduleEndpoint{Status: "not_applicable"}
	}
	endpointID := route.EndpointID
	if route.Mode == app.ReturnToSource {
		endpointID = route.SourceEndpointID
	}
	// Read-only projection of a stored schedule's endpoint for display; the
	// admitted-source form keeps the projection visible after an opt-out.
	endpoint, err := messagecontrol.NewEndpointRegistry(s.store).GetAdmittedSource(r.Context(), endpointID)
	if err != nil {
		return s.unavailableScheduleEndpoint(r.Context(), endpointID, schedule.SessionID)
	}
	projection := publicScheduleEndpoint{
		Kind: endpoint.Kind, Channel: endpoint.ProviderKey, SoftwareDisplayName: endpoint.SoftwareDisplayName,
		AccountDisplayName: endpoint.AccountDisplayName, RecipientDisplayName: endpoint.RecipientDisplayName,
		ConversationLabel: endpoint.ConversationLabel, Status: string(endpoint.Status),
	}
	if endpoint.Kind == app.EndpointKindWeb {
		projection.Channel = "web"
		projection.SoftwareDisplayName = "WebChat"
		session, ok, err := s.store.GetSession(r.Context(), endpoint.SessionID)
		if err != nil {
			slog.Warn("schedule session projection unavailable", "session_id", endpoint.SessionID, "code", store.StoreErrorCodeOf(err))
			projection.Status = "unavailable"
			return projection
		}
		if ok {
			projection.ConversationLabel = session.Title
		}
	}
	return projection
}

func (s *Server) unavailableScheduleEndpoint(ctx context.Context, endpointID app.EndpointID, sessionID string) publicScheduleEndpoint {
	value := strings.TrimSpace(string(endpointID))
	if strings.HasPrefix(value, "session:") {
		projection := publicScheduleEndpoint{Kind: app.EndpointKindWeb, Channel: "web", SoftwareDisplayName: "WebChat", Status: "unavailable"}
		session, ok, err := s.store.GetSession(ctx, strings.TrimPrefix(value, "session:"))
		if err != nil {
			slog.Warn("schedule session projection unavailable", "session_id", sessionID, "code", store.StoreErrorCodeOf(err))
			return projection
		}
		if ok {
			projection.ConversationLabel = session.Title
		}
		return projection
	}
	chat, ok, chatErr := s.store.GetExternalChatSession(ctx, value)
	if chatErr != nil {
		slog.Warn("schedule chat projection unavailable", "chat_session_id", value, "code", store.StoreErrorCodeOf(chatErr))
		return publicScheduleEndpoint{Kind: app.EndpointKindThirdPartyDevice, Status: "unavailable"}
	}
	if ok {
		binding, _, err := s.store.GetNotificationBinding(ctx, chat.BindingID)
		if err != nil {
			slog.Warn("schedule binding projection unavailable", "binding_id", chat.BindingID, "code", store.StoreErrorCodeOf(err))
		}
		return publicScheduleEndpoint{
			Kind: app.EndpointKindThirdPartyDevice, Channel: chat.Channel, SoftwareDisplayName: chat.Channel,
			AccountDisplayName: binding.DisplayName, RecipientDisplayName: chat.DisplayName,
			ConversationLabel: binding.DisplayName, Status: "unavailable",
		}
	}
	if binding, ok, err := s.store.GetNotificationBinding(ctx, value); err != nil {
		slog.Warn("schedule binding projection unavailable", "binding_id", value, "code", store.StoreErrorCodeOf(err))
	} else if ok {
		return publicScheduleEndpoint{
			Kind: app.EndpointKindThirdPartyDevice, Channel: binding.Channel, SoftwareDisplayName: binding.Channel,
			AccountDisplayName: binding.DisplayName, Status: "unavailable",
		}
	}
	session, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		slog.Warn("schedule session projection unavailable", "session_id", sessionID, "code", store.StoreErrorCodeOf(err))
		return publicScheduleEndpoint{Status: "unavailable"}
	}
	if ok {
		return publicScheduleEndpoint{Kind: app.EndpointKindWeb, Channel: "web", SoftwareDisplayName: "WebChat", ConversationLabel: session.Title, Status: "unavailable"}
	}
	return publicScheduleEndpoint{Status: "unavailable"}
}

func scheduleTitle(content app.MessageContent) string {
	text := scheduleText(content)
	if text == "" {
		return "Scheduled task"
	}
	runes := []rune(text)
	if len(runes) > 120 {
		return string(runes[:120]) + "..."
	}
	return text
}

func scheduleText(content app.MessageContent) string {
	for _, part := range content.Parts {
		if part.Kind != app.MessagePartText || strings.TrimSpace(part.Text) == "" {
			continue
		}
		return strings.TrimSpace(part.Text)
	}
	return ""
}
