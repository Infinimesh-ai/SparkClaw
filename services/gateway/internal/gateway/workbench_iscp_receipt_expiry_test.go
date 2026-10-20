package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestISCPReceiptExpiryIndexTracksCommittedReplacements(t *testing.T) {
	j := receiptRetentionFixture(t)
	now := time.Now().UTC()
	late := domainTestRequest("id", nil).ID
	early := domainTestRequest("id", nil).ID
	save := func(id string, expires time.Time) {
		t.Helper()
		if err := j.save("scope", id, completedRetentionReceipt(expires.Add(-domainReceiptRetention))); err != nil {
			t.Fatal(err)
		}
	}
	save(late, now.Add(time.Hour))
	save(early, now.Add(time.Minute))
	if !j.nextExpiryLocked().Equal(now.Add(time.Minute)) {
		t.Fatal("index did not select the earliest expiry")
	}
	// Replacing one receipt must update its existing heap entry, not leave a
	// stale early deadline or consume an additional index slot.
	save(early, now.Add(2*time.Hour))
	if len(j.expiries) != 2 || !j.nextExpiryLocked().Equal(now.Add(time.Hour)) {
		t.Fatal("replacement left a stale deadline")
	}
	if err := j.save("scope", late, iscpDomainReceipt{Version: 1, Digest: "intent"}); err != nil {
		t.Fatal(err)
	}
	if len(j.expiries) != 1 || !j.nextExpiryLocked().Equal(now.Add(2*time.Hour)) {
		t.Fatal("incomplete replacement retained an expiry")
	}
	failed := domainTestRequest("id", nil).ID
	if err := os.Mkdir(j.path("scope", failed), 0700); err != nil {
		t.Fatal(err)
	}
	if err := j.save("scope", failed, completedRetentionReceipt(now.Add(-8*24*time.Hour))); err == nil {
		t.Fatal("expected an atomic commit failure")
	}
	if len(j.expiries) != 1 {
		t.Fatal("failed commit entered the expiry index")
	}
}

func TestISCPReceiptExpiryIndexRechecksDiskBeforeRemoval(t *testing.T) {
	j := receiptRetentionFixture(t)
	now := time.Now().UTC()
	id := domainTestRequest("id", nil).ID
	if err := j.save("scope", id, completedRetentionReceipt(now.Add(-8*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	// Simulate a newer durable replacement missed by this process's index.
	other, err := newISCPDomainReceipts(filepath.Dir(j.root))
	if err != nil {
		t.Fatal(err)
	}
	if err := other.save("scope", id, completedRetentionReceipt(now)); err != nil {
		t.Fatal(err)
	}
	result, err := j.sweep(t.Context(), now)
	if err != nil || result.Expired != 0 || !j.nextExpiryLocked().Equal(now.Add(domainReceiptRetention)) {
		t.Fatal("stale index deleted a fresh result", result, err)
	}
	if _, err := os.Stat(j.path("scope", id)); err != nil {
		t.Fatal(err)
	}
}

func TestISCPReceiptSweepYieldsToActiveRequest(t *testing.T) {
	j := receiptRetentionFixture(t)
	j.mu.Lock()
	done := make(chan iscpReceiptSweep, 1)
	go func() {
		result, _ := j.sweep(t.Context(), time.Now())
		done <- result
	}()
	select {
	case result := <-done:
		j.mu.Unlock()
		if result.Scanned != 0 || !result.More {
			t.Fatal("contended sweep did not yield", result)
		}
	case <-time.After(time.Second):
		j.mu.Unlock()
		t.Fatal("collector waited behind a normal request")
	}
}

func TestISCPReceiptSweepDefersCorruptEarliestWithoutStarvingOtherKeys(t *testing.T) {
	j := receiptRetentionFixture(t)
	now := time.Now().UTC()
	broken, healthy := domainTestRequest("id", nil).ID, domainTestRequest("id", nil).ID
	for i, id := range []string{broken, healthy} {
		if err := j.save("scope", id, completedRetentionReceipt(now.Add(-8*24*time.Hour+time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(j.path("scope", broken), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	j.rebuild = false
	if result, err := j.sweep(t.Context(), now); err == nil || !result.More {
		t.Fatal("corrupt earliest result was not deferred", result, err)
	}
	if result, err := j.sweep(t.Context(), now); err != nil || result.Expired != 1 || result.More {
		t.Fatal("corrupt earliest result starved another due key", result, err)
	}
	if !j.nextExpiryLocked().Equal(now.Add(time.Minute)) {
		t.Fatal("failed expiry did not back off")
	}
	if _, err := os.Stat(j.path("scope", broken)); err != nil {
		t.Fatal("corrupt evidence was removed", err)
	}
}
