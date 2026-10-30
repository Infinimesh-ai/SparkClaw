package app

import "time"

const ClientActionScheduleShow = "schedule.show"

// ClientPresentation is an advertised, read-only presentation capability. It
// confers no backend tool/data authority; its scope comes from the admitted task.
type ClientPresentation struct {
	SchemaVersion int       `json:"schema_version"`
	ScheduleShow  bool      `json:"schedule_show"`
	Timezone      string    `json:"timezone"`
	RequestedAt   time.Time `json:"requested_at"`
}

func (p ClientPresentation) Valid() bool {
	if p.SchemaVersion != 1 || !p.ScheduleShow || p.Timezone == "" || p.Timezone == "Local" || len(p.Timezone) > 80 || p.RequestedAt.IsZero() {
		return false
	}
	_, err := time.LoadLocation(p.Timezone)
	return err == nil
}

// ClientAction contains no schedule content. Dates are civil dates in Timezone,
// with an exclusive end; two empty dates mean all locally visible schedules.
type ClientAction struct {
	SchemaVersion  int       `json:"schema_version"`
	Type           string    `json:"type"`
	ActionID       string    `json:"action_id"`
	RequestID      string    `json:"request_id"`
	ConversationID string    `json:"local_conversation_id"`
	Timezone       string    `json:"timezone"`
	DateStart      string    `json:"date_start"`
	DateEnd        string    `json:"date_end"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func (a ClientAction) Valid() bool {
	if a.SchemaVersion != 1 || a.Type != ClientActionScheduleShow || a.ActionID == "" || a.ActionID != a.RequestID || a.ConversationID == "" || a.ExpiresAt.IsZero() || a.Timezone == "" || a.Timezone == "Local" || len(a.Timezone) > 80 {
		return false
	}
	if _, err := time.LoadLocation(a.Timezone); err != nil {
		return false
	}
	return ValidClientScheduleDates(a.DateStart, a.DateEnd)
}

func ValidClientScheduleDates(start, end string) bool {
	if start == "" || end == "" {
		return start == "" && end == ""
	}
	a, err := time.Parse(time.DateOnly, start)
	if err != nil || a.Format(time.DateOnly) != start {
		return false
	}
	b, err := time.Parse(time.DateOnly, end)
	return err == nil && b.Format(time.DateOnly) == end && b.After(a) && b.Sub(a) <= 366*24*time.Hour
}

type ClientActionReceipt struct {
	ActionID string `json:"action_id"`
	Status   string `json:"status"`
}

func (r ClientActionReceipt) Valid(requestID string) bool {
	return r.ActionID == requestID && (r.Status == "shown" || r.Status == "failed" || r.Status == "skipped")
}
