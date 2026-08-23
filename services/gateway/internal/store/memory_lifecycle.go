package store

import (
	"maps"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) appendAuditLocked(typ, sessionID, runID, actor, summary string, fields map[string]any) {
	s.appendAuditLockedAt(time.Now().UTC(), typ, sessionID, runID, actor, summary, fields)
}

func (s *MemoryStore) appendAuditLockedAt(at time.Time, typ, sessionID, runID, actor, summary string, fields map[string]any) {
	clonedFields, err := cloneAuditFields(fields)
	if err != nil {
		clonedFields = maps.Clone(fields)
	}
	s.auditEvents = append(s.auditEvents, app.AuditEvent{
		ID:        app.NewID("audit"),
		Time:      at,
		Type:      typ,
		SessionID: sessionID,
		RunID:     runID,
		Actor:     actor,
		Summary:   summary,
		Fields:    clonedFields,
	})
}

func (s *MemoryStore) appendEventLocked(typ, sessionID, runID string, payload any) {
	s.appendEventLockedAt(time.Now().UTC(), typ, sessionID, runID, payload)
}

func (s *MemoryStore) appendEventLockedAt(at time.Time, typ, sessionID, runID string, payload any) {
	s.events = append(s.events, app.Event{
		ID:        app.NewID("evt"),
		Time:      at,
		Type:      typ,
		SessionID: sessionID,
		RunID:     runID,
		Payload:   payload,
	})
}

func (s *MemoryStore) sessionIDForRunLocked(runID string) string {
	if run, ok := s.runs[runID]; ok {
		return run.SessionID
	}
	return ""
}
