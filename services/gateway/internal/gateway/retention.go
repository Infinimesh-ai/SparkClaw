package gateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// retentionSweepInterval paces the background retention coordinator. Both
// retention windows are day-granularity, so hourly sweeps keep expiry latency
// negligible relative to the shortest configurable window.
const retentionSweepInterval = time.Hour

// retentionSweepTimeout is the hard backstop for a single sweep so a stalled
// store call cannot wedge the coordinator or ride the lifecycle context
// indefinitely; the store's own operation timeouts are the graceful stop.
const retentionSweepTimeout = time.Minute

// StartRetentionSweeps runs the retention coordinator that owns the
// destructive pruning of expired memories and passive notifications. Pruning
// lives here on a ticker rather than in request handlers because GET
// endpoints must stay side-effect free (engineering baseline rule 7). The
// first sweep runs immediately so short-lived processes still age data out;
// the goroutine ends with ctx and is awaited by WaitForBackgroundWork.
func (s *Server) StartRetentionSweeps(ctx context.Context) {
	s.streamWG.Add(1)
	go func() {
		defer s.streamWG.Done()
		ticker := time.NewTicker(retentionSweepInterval)
		defer ticker.Stop()
		s.runRetentionSweep(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runRetentionSweep(ctx)
			}
		}
	}()
}

func (s *Server) runRetentionSweep(ctx context.Context) {
	sweepCtx, cancel := context.WithTimeout(ctx, retentionSweepTimeout)
	defer cancel()
	if _, err := s.applyMemoryRetention(sweepCtx); err != nil {
		slog.Warn("memory retention sweep unavailable", "code", store.StoreErrorCodeOf(err))
	}
	if err := s.applyPassiveNotificationRetention(sweepCtx); err != nil {
		slog.Warn("passive notification retention sweep unavailable", "code", store.StoreErrorCodeOf(err))
	}
}
