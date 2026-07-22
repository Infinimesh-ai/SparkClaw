package gateway

import (
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
)

type publicSchedule struct {
	ID          app.ScheduleID          `json:"id"`
	SessionID   string                  `json:"session_id,omitempty"`
	Title       string                  `json:"title"`
	DueTime     time.Time               `json:"due_time"`
	Timezone    string                  `json:"timezone"`
	Recurrence  string                  `json:"recurrence,omitempty"`
	Status      string                  `json:"status"`
	PayloadMode app.SchedulePayloadMode `json:"payload_mode"`
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
		out = append(out, publicSchedule{
			ID: schedule.ID, SessionID: schedule.SessionID, Title: scheduleTitle(schedule.Spec.Payload.Content),
			DueTime: schedule.DueTime, Timezone: schedule.Timezone, Recurrence: schedule.Recurrence,
			Status: schedule.Status, PayloadMode: schedule.Spec.Payload.Mode,
		})
		if len(out) == 100 {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": out})
}

func scheduleTitle(content app.MessageContent) string {
	for _, part := range content.Parts {
		if part.Kind != app.MessagePartText || strings.TrimSpace(part.Text) == "" {
			continue
		}
		runes := []rune(strings.TrimSpace(part.Text))
		if len(runes) > 120 {
			return string(runes[:120]) + "..."
		}
		return string(runes)
	}
	return "Scheduled task"
}
