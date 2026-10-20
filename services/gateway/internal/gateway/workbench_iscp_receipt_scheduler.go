package gateway

import (
	"context"
	"log/slog"
	"time"
)

const domainReceiptBatchPause = 10 * time.Millisecond

// This worker has its own timer, so slower memory/artifact retention does not
// delay receipt deadlines. The hourly ticker is only a reconciliation fallback.
func (s *Server) startDomainReceiptRetention(ctx context.Context) {
	s.streamWG.Add(1)
	go func() {
		defer s.streamWG.Done()
		hourly := time.NewTicker(time.Hour)
		defer hourly.Stop()
		s.runDomainReceiptRetention(ctx, hourly.C)
	}()
}

func (s *Server) receiptRetentionSignals() <-chan struct{} {
	s.domainReceiptsMu.Lock()
	defer s.domainReceiptsMu.Unlock()
	if s.domainReceiptWake == nil {
		s.domainReceiptWake = make(chan struct{}, 1)
	}
	return s.domainReceiptWake
}

func (s *Server) runDomainReceiptRetention(ctx context.Context, hourly <-chan time.Time) {
	wake := s.receiptRetentionSignals()
	defer s.closeDomainReceiptScan()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	next := s.collectDomainReceipts(ctx, true)
	for {
		var alarm <-chan time.Time
		if !next.IsZero() {
			timer.Reset(max(time.Until(next), time.Millisecond))
			alarm = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-hourly:
			next = s.collectDomainReceipts(ctx, true)
		case <-wake:
			next = s.collectDomainReceipts(ctx, false)
		case <-alarm:
			next = s.collectDomainReceipts(ctx, false)
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
}

func (s *Server) collectDomainReceipts(ctx context.Context, reconcile bool) time.Time {
	s.domainReceiptsMu.Lock()
	journal := s.domainReceipts
	s.domainReceiptsMu.Unlock()
	if journal == nil || ctx.Err() != nil {
		return time.Time{}
	}
	if reconcile {
		journal.mu.Lock()
		journal.rebuild = true
		journal.mu.Unlock()
	}
	result, err := journal.sweep(ctx, domainNow())
	if err != nil && ctx.Err() == nil {
		// Never log receipt keys, decrypted content or identity scopes.
		slog.Warn("ISCP receipt retention sweep incomplete", "scanned", result.Scanned, "expired", result.Expired)
	} else if result.Expired > 0 {
		slog.Info("ISCP receipt results expired", "scanned", result.Scanned, "expired", result.Expired)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	// All changes before this snapshot are represented by the heap/rebuild
	// flag. A writer after unlock queues a new notification, so an earlier
	// deadline cannot be lost while stopping/resetting the timer.
	select {
	case <-journal.wake:
	default:
	}
	now := domainNow()
	next := journal.nextExpiryLocked()
	if result.More || journal.rebuild || (!next.IsZero() && !next.After(now)) {
		return now.Add(domainReceiptBatchPause)
	}
	// No due/indexed receipts and no reconciliation work: disable the alarm.
	// Only a new completion, pressure notification or hourly tick wakes it.
	return next
}

func (s *Server) closeDomainReceiptScan() {
	s.domainReceiptsMu.Lock()
	defer s.domainReceiptsMu.Unlock()
	if s.domainReceipts != nil {
		s.domainReceipts.mu.Lock()
		defer s.domainReceipts.mu.Unlock()
		s.domainReceipts.closeScanLocked()
	}
}
