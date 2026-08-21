package gateway

import (
	"context"
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
	schedules := messagecontrol.NewScheduleRegistry(s.store).List(r.Context(), app.ReminderFilter{})
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

func (s *Server) publicScheduleEndpoint(r *http.Request, schedule app.MessageSchedule) publicScheduleEndpoint {
	route := schedule.Spec.ReturnRoute
	if route.Mode == app.ReturnNowhere {
		return publicScheduleEndpoint{Status: "not_applicable"}
	}
	endpointID := route.EndpointID
	if route.Mode == app.ReturnToSource {
		endpointID = route.SourceEndpointID
	}
	endpoint, err := messagecontrol.NewEndpointRegistry(s.store).Get(r.Context(), endpointID)
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
		if session, ok := s.store.GetSession(endpoint.SessionID); ok {
			projection.ConversationLabel = session.Title
		}
	}
	return projection
}

func (s *Server) unavailableScheduleEndpoint(ctx context.Context, endpointID app.EndpointID, sessionID string) publicScheduleEndpoint {
	value := strings.TrimSpace(string(endpointID))
	if strings.HasPrefix(value, "session:") {
		projection := publicScheduleEndpoint{Kind: app.EndpointKindWeb, Channel: "web", SoftwareDisplayName: "WebChat", Status: "unavailable"}
		if session, ok := s.store.GetSession(strings.TrimPrefix(value, "session:")); ok {
			projection.ConversationLabel = session.Title
		}
		return projection
	}
	if chat, ok := s.store.GetExternalChatSession(value); ok {
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
	if session, ok := s.store.GetSession(sessionID); ok {
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
