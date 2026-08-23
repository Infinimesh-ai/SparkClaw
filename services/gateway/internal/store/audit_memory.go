package store

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) AddAudit(ctx context.Context, event app.AuditEvent) error {
	ctx, cancel := operationContext(ctx, OperationAuditAdd, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditAdd, ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationAuditAdd, ctx); err != nil {
		return err
	}
	prepared, err := prepareAuditEvent(event, time.Now().UTC())
	if err != nil {
		return storeError(ctx, OperationAuditAdd, StoreErrorInvalid, err)
	}
	s.auditEvents = append(s.auditEvents, prepared)
	return nil
}

func (s *MemoryStore) ListAudit(ctx context.Context, sessionID string) ([]app.AuditEvent, error) {
	ctx, cancel := operationContext(ctx, OperationAuditList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationAuditList, ctx); err != nil {
		return nil, err
	}
	out := []app.AuditEvent{}
	for _, event := range s.auditEvents {
		if sessionID == "" || event.SessionID == sessionID {
			cloned, err := cloneAuditEvent(event)
			if err != nil {
				return nil, storeError(ctx, OperationAuditList, StoreErrorCorrupt, err)
			}
			out = append(out, cloned)
		}
	}
	slices.SortFunc(out, func(a, b app.AuditEvent) int {
		if order := b.Time.Compare(a.Time); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) EventsAfter(ctx context.Context, sessionID, after string) ([]app.Event, error) {
	ctx, cancel := operationContext(ctx, OperationAuditEventsAfter, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditEventsAfter, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationAuditEventsAfter, ctx); err != nil {
		return nil, err
	}
	out := []app.Event{}
	started := after == ""
	for _, event := range s.events {
		if !started {
			if event.ID == after {
				started = true
			}
			continue
		}
		if sessionID == "" || event.SessionID == sessionID {
			out = append(out, cloneClientLifecycleEvent(event))
		}
	}
	return out, nil
}
