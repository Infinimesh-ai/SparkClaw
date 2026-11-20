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

// pptxSealedSweepLimit bounds the artifact keys one sealed-candidate expiry
// sweep examines. Each candidate is two objects, so this drains a hundred
// abandoned approvals per hour while keeping a single sweep's listing and
// deletes well inside retentionSweepTimeout.
const pptxSealedSweepLimit = 200

// StartRetentionSweeps runs the retention coordinator that owns the
// destructive pruning of expired memories, passive notifications and sealed
// PPTX candidates that were never published within their approval TTL. Pruning
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
	s.applyPPTXSealedCandidateRetention(sweepCtx)
}

// applyPPTXSealedCandidateRetention runs one bounded sweep over the sealed
// PPTX namespace and remembers where to resume. A short page resets the
// cursor so the next sweep restarts from the beginning of the namespace; a
// listing failure yields an empty cursor for the same reason.
func (s *Server) applyPPTXSealedCandidateRetention(ctx context.Context) {
	result, err := s.tools.SweepExpiredPPTXSealedCandidates(ctx, s.pptxSealedSweepCursor, pptxSealedSweepLimit)
	s.pptxSealedSweepCursor = result.NextCursor
	if err != nil {
		slog.Warn("sealed PPTX candidate retention sweep incomplete", "scanned", result.Scanned, "deleted", result.Deleted, "error", err)
	}
}
