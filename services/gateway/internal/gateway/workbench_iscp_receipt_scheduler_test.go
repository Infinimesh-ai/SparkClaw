package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func receiptSchedulerServer(j *iscpDomainReceipts) *Server {
	return &Server{domainReceipts: j, domainReceiptWake: j.wake}
}

func waitReceiptCondition(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(description)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func runReceiptScheduler(t *testing.T, s *Server, hourly <-chan time.Time) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runDomainReceiptRetention(ctx, hourly)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("receipt scheduler did not drain")
		}
	})
}

func waitReceiptIndexReady(t *testing.T, j *iscpDomainReceipts) {
	t.Helper()
	waitReceiptCondition(t, "startup index never finished", func() bool {
		j.mu.Lock()
		defer j.mu.Unlock()
		return !j.rebuild
	})
}

func waitReceiptRemoved(t *testing.T, j *iscpDomainReceipts, id string) {
	t.Helper()
	waitReceiptCondition(t, "expired result was not collected", func() bool {
		_, err := os.Stat(j.path("scope", id))
		return os.IsNotExist(err)
	})
	j.mu.Lock()
	receipt, found, err := j.load("scope", id)
	j.mu.Unlock()
	if err != nil || !found || receipt.Complete || receipt.Digest == "" {
		t.Fatal("collection lost the original operation fence", err)
	}
}

func TestISCPReceiptSchedulerSkipsEmptyIndexUntilReconciliation(t *testing.T) {
	j := receiptRetentionFixture(t)
	s := receiptSchedulerServer(j)
	if next := s.collectDomainReceipts(t.Context(), true); !next.IsZero() {
		t.Fatal("empty index scheduled an unnecessary alarm", next)
	}
	// Durable data absent from the in-memory index represents a missed update.
	other, err := newISCPDomainReceipts(filepath.Dir(j.root))
	if err != nil {
		t.Fatal(err)
	}
	id := domainTestRequest("id", nil).ID
	if err := other.save("scope", id, completedRetentionReceipt(time.Now().Add(-8*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if next := s.collectDomainReceipts(t.Context(), false); !next.IsZero() {
		t.Fatal("idle collection performed an unsolicited directory scan")
	}
	if _, err := os.Stat(j.path("scope", id)); err != nil {
		t.Fatal("empty index performed filesystem cleanup", err)
	}
	if next := s.collectDomainReceipts(t.Context(), true); !next.IsZero() {
		t.Fatal("reconciliation left work after the only expired record", next)
	}
	waitReceiptRemoved(t, j, id)
}

func TestISCPReceiptSchedulerResetsToEarlierDeadline(t *testing.T) {
	j := receiptRetentionFixture(t)
	s := receiptSchedulerServer(j)
	late, early := domainTestRequest("id", nil).ID, domainTestRequest("id", nil).ID
	if err := j.save("scope", late, completedRetentionReceipt(time.Now())); err != nil {
		t.Fatal(err)
	}
	runReceiptScheduler(t, s, nil)
	waitReceiptIndexReady(t, j)
	expires := time.Now().Add(300 * time.Millisecond)
	j.mu.Lock()
	err := j.save("scope", early, completedRetentionReceipt(expires.Add(-domainReceiptRetention)))
	j.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// There is no hourly channel in this worker: only the new earlier alarm
	// can collect this record while the original future record stays intact.
	waitReceiptRemoved(t, j, early)
	if time.Now().Before(expires) {
		t.Fatal("receipt was collected before its authenticated deadline")
	}
	if _, err := os.Stat(j.path("scope", late)); err != nil {
		t.Fatal("later receipt was removed early", err)
	}
}

func TestISCPReceiptSchedulerRebuildsLargeIndexInBatches(t *testing.T) {
	j := receiptRetentionFixture(t)
	completed := time.Now().UTC()
	for i := 0; i < domainReceiptSweepLimit+20; i++ {
		if err := j.save("scope", domainTestRequest("id", nil).ID, completedRetentionReceipt(completed)); err != nil {
			t.Fatal(err)
		}
	}
	fresh, err := newISCPDomainReceipts(filepath.Dir(j.root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fresh.closeScanLocked() })
	result, err := fresh.sweep(t.Context(), completed)
	if err != nil || !result.More || result.Scanned > domainReceiptSweepLimit || result.Expired != 0 {
		t.Fatal("startup rebuild exceeded its batch or stopped early", result, err)
	}
	for i := 0; i < 20 && fresh.rebuild; i++ {
		if result, err := fresh.sweep(t.Context(), completed); err != nil || result.Scanned > domainReceiptSweepLimit {
			t.Fatal(result, err)
		}
	}
	if fresh.rebuild || len(fresh.expiries) != domainReceiptSweepLimit+20 || fresh.scan != nil {
		t.Fatal("index rebuild did not resume its cursor across batches")
	}
	s := receiptSchedulerServer(fresh)
	if next := s.collectDomainReceipts(t.Context(), false); !next.Equal(completed.Add(domainReceiptRetention)) {
		t.Fatal("finished rebuild did not schedule the earliest expiry", next)
	}
}

func TestISCPReceiptSchedulerRebuildsOnRestartAndLateJournal(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "restart", true: "late journal"}[late], func(t *testing.T) {
			j := receiptRetentionFixture(t)
			id := domainTestRequest("id", nil).ID
			if err := j.save("scope", id, completedRetentionReceipt(time.Now().Add(-8*24*time.Hour))); err != nil {
				t.Fatal(err)
			}
			s := &Server{}
			// Establish the worker's wake channel even before a peer opens a
			// journal; publishing the first journal must wake this same channel.
			s.receiptRetentionSignals()
			if late {
				hourly := make(chan time.Time)
				runReceiptScheduler(t, s, hourly)
				select {
				case hourly <- time.Now():
					// The worker has completed its initial nil-journal pass.
				case <-time.After(5 * time.Second):
					t.Fatal("worker did not start without a journal")
				}
			}
			fresh, err := s.domainReceiptJournal(filepath.Dir(j.root))
			if err != nil {
				t.Fatal(err)
			}
			if !late {
				if len(fresh.expiries) != 0 {
					t.Fatal("restart unexpectedly retained the process-local index")
				}
				runReceiptScheduler(t, s, nil)
			}
			waitReceiptRemoved(t, fresh, id)
		})
	}
}

func TestISCPReceiptSchedulerHourlyFallbackAndCapacityPressure(t *testing.T) {
	for _, pressure := range []bool{false, true} {
		t.Run(map[bool]string{false: "hourly", true: "pressure"}[pressure], func(t *testing.T) {
			j := receiptRetentionFixture(t)
			s := receiptSchedulerServer(j)
			hourly := make(chan time.Time, 1)
			runReceiptScheduler(t, s, hourly)
			waitReceiptIndexReady(t, j)
			other, err := newISCPDomainReceipts(filepath.Dir(j.root))
			if err != nil {
				t.Fatal(err)
			}
			expired, fresh := domainTestRequest("id", nil).ID, domainTestRequest("id", nil).ID
			if err := other.save("scope", expired, completedRetentionReceipt(time.Now().Add(-8*24*time.Hour))); err != nil {
				t.Fatal(err)
			}
			if err := other.save("scope", fresh, completedRetentionReceipt(time.Now())); err != nil {
				t.Fatal(err)
			}
			if pressure {
				j.mu.Lock()
				// Active reservations count toward the shared capacity budget.
				j.reserved = domainReceiptTotalBytes * 4 / 5
				accepted := j.hasCapacity(domainReceiptMaxBytes)
				requested, first := j.rebuild, j.lastPressure
				_, statErr := os.Stat(j.path("scope", expired))
				j.hasCapacity(domainReceiptMaxBytes)
				coalesced := j.lastPressure.Equal(first)
				j.reserved = 0
				j.mu.Unlock()
				if !accepted || !requested || first.IsZero() || !coalesced || statErr != nil {
					t.Fatal("pressure did not request bounded asynchronous cleanup", accepted, requested, coalesced, statErr)
				}
			} else {
				hourly <- time.Now()
			}
			waitReceiptRemoved(t, j, expired)
			if _, err := os.Stat(j.path("scope", fresh)); err != nil {
				t.Fatal("fallback/pressure removed a non-expired result", err)
			}
		})
	}
}
