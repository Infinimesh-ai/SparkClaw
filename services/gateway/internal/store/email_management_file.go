package store

import (
	"context"
	"time"
)

// Evaluate the bounded aggregate against an isolated row overlay. Idle claims
// and exhausted refresh scans avoid copying/encoding/fsyncing the full File
// snapshot. Changed aggregates still use the one authoritative replacement,
// rollback and unknown-outcome fence protocol.
func emailFileRun[T any](s *FileStore, ctx context.Context, op StoreOperation, owner, key string, input any, _ bool, fn func(*emailEngine) (T, error)) (out T, err error) {
	if err = operationContextError(op, ctx); err != nil {
		return
	}
	s.inner.mu.RLock()
	db := &emailMemoryRecords{owner: normalizeConnectorOwner(owner), records: s.inner.emailRecords, writes: map[string]EmailRecord{}}
	e := &emailEngine{db: db, owner: db.owner, now: postgresTime(time.Now())}
	out, err = emailRun(e, op, key, input, fn)
	s.inner.mu.RUnlock()
	if err != nil {
		return out, emailClassify(ctx, op, err)
	}
	if err = ctx.Err(); err != nil {
		return out, emailClassify(ctx, op, err)
	}
	if !db.changed() {
		return out, nil
	}
	return runFileCommand(s, ctx, op, func(ctx context.Context) (T, error) {
		if err := ctx.Err(); err != nil {
			var zero T
			return zero, emailClassify(ctx, op, err)
		}
		s.inner.mu.Lock()
		defer s.inner.mu.Unlock()
		for key, r := range db.writes {
			s.inner.emailRecords[key] = r
		}
		return out, nil
	})
}
