package store

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

// EventWindow is a bounded audit projection. Tail captures the current boundary
// without reading history. CursorFound distinguishes a retention gap from idle.
type EventWindow struct {
	Events      []app.Event
	Cursor      string
	CursorFound bool
	More        bool
}
type EventWindowReader interface {
	ReadEventWindow(context.Context, string, int, bool) (EventWindow, error)
}

const EventWindowLimit = 512

func (s *MemoryStore) ReadEventWindow(ctx context.Context, after string, limit int, tail bool) (EventWindow, error) {
	ctx, cancel := operationContext(ctx, OperationAuditEventWindow, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditEventWindow, ctx); err != nil {
		return EventWindow{}, err
	}
	if limit < 1 || limit > EventWindowLimit {
		return EventWindow{}, errors.New("invalid event window limit")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := EventWindow{Events: []app.Event{}, Cursor: after, CursorFound: after == ""}
	if tail {
		out.CursorFound = true
		if len(s.events) > 0 {
			out.Cursor = s.events[len(s.events)-1].ID
		}
		return out, nil
	}
	for _, event := range s.events {
		if !out.CursorFound {
			if event.ID == after {
				out.CursorFound = true
			}
			continue
		}
		if len(out.Events) == limit {
			out.More = true
			break
		}
		out.Events = append(out.Events, cloneClientLifecycleEvent(event))
		out.Cursor = event.ID
	}
	return out, operationContextError(OperationAuditEventWindow, ctx)
}
func (s *FileStore) ReadEventWindow(ctx context.Context, after string, limit int, tail bool) (EventWindow, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationAuditEventWindow, 1)
	if err != nil {
		return EventWindow{}, err
	}
	defer release()
	return s.inner.ReadEventWindow(ctx, after, limit, tail)
}
func (s *PostgresStore) ReadEventWindow(ctx context.Context, after string, limit int, tail bool) (EventWindow, error) {
	ctx, cancel := operationContext(ctx, OperationAuditEventWindow, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditEventWindow, ctx); err != nil {
		return EventWindow{}, err
	}
	if limit < 1 || limit > EventWindowLimit {
		return EventWindow{}, errors.New("invalid event window limit")
	}
	out := EventWindow{Events: []app.Event{}, Cursor: after, CursorFound: after == ""}
	if tail {
		event, err := scanEvent(s.auditPostgres.QueryRow(ctx, `SELECT id,happened_at,type,coalesce(session_id,''),coalesce(run_id,''),payload FROM events ORDER BY seq DESC LIMIT 1`))
		if errors.Is(err, pgx.ErrNoRows) {
			out.CursorFound = true
			return out, nil
		}
		if err != nil {
			return out, classifyAuditPostgresError(OperationAuditEventWindow, ctx, err)
		}
		out.CursorFound = true
		out.Cursor = event.ID
		return out, nil
	}
	var seq int64
	if after != "" {
		err := s.auditPostgres.QueryRow(ctx, `SELECT seq FROM events WHERE id=$1`, after).Scan(&seq)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, nil
		}
		if err != nil {
			return out, classifyAuditPostgresError(OperationAuditEventWindow, ctx, err)
		}
		out.CursorFound = true
	}
	rows, err := s.auditPostgres.Query(ctx, `SELECT id,happened_at,type,coalesce(session_id,''),coalesce(run_id,''),payload FROM events WHERE seq>$1 ORDER BY seq ASC LIMIT $2`, seq, limit+1)
	if err != nil {
		return out, classifyAuditPostgresError(OperationAuditEventWindow, ctx, err)
	}
	defer rows.Close()
	for rows.Next() {
		if len(out.Events) == limit {
			out.More = true
			break
		}
		event, err := scanEvent(rows)
		if err != nil {
			return out, classifyAuditPostgresError(OperationAuditEventWindow, ctx, err)
		}
		out.Events = append(out.Events, event)
		out.Cursor = event.ID
	}
	if err := rows.Err(); err != nil {
		return out, classifyAuditPostgresError(OperationAuditEventWindow, ctx, err)
	}
	return out, nil
}

var _ EventWindowReader = (*MemoryStore)(nil)
var _ EventWindowReader = (*FileStore)(nil)
var _ EventWindowReader = (*PostgresStore)(nil)
