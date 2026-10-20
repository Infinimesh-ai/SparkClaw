package gateway

import (
	"container/heap"
	"time"
)

// The expiry heap is a derived index, rebuilt from authenticated receipt
// timestamps on startup and reconciled hourly. It never authorizes deletion:
// collection re-reads the receipt and persists its original dedupe fence.
type receiptExpiry struct {
	key       string
	expiresAt time.Time
	retryAt   time.Time
	position  int
}

func (e *receiptExpiry) dueAt() time.Time {
	if e.retryAt.After(e.expiresAt) {
		return e.retryAt
	}
	return e.expiresAt
}

type receiptExpiryHeap []*receiptExpiry

func (h receiptExpiryHeap) Len() int { return len(h) }
func (h receiptExpiryHeap) Less(i, k int) bool {
	if h[i].dueAt().Equal(h[k].dueAt()) {
		return h[i].key < h[k].key
	}
	return h[i].dueAt().Before(h[k].dueAt())
}
func (h receiptExpiryHeap) Swap(i, k int) {
	h[i], h[k] = h[k], h[i]
	h[i].position, h[k].position = i, k
}
func (h *receiptExpiryHeap) Push(value any) {
	entry := value.(*receiptExpiry)
	entry.position = len(*h)
	*h = append(*h, entry)
}
func (h *receiptExpiryHeap) Pop() any {
	last := len(*h) - 1
	entry := (*h)[last]
	(*h)[last] = nil
	*h = (*h)[:last]
	entry.position = -1
	return entry
}

func receiptExpiryTime(receipt iscpDomainReceipt) time.Time {
	if !receipt.Complete || receipt.CompletedAt.IsZero() || receipt.Digest == "" || !domainReceiptFence(receipt.Operation) {
		return time.Time{}
	}
	return receipt.CompletedAt.Add(domainReceiptRetention)
}

// Index updates and notifications run under the same lock as receipt commits.
// A coalesced notification resets an existing timer if an earlier key appears.
func (j *iscpDomainReceipts) indexReceipt(key string, receipt iscpDomainReceipt) {
	before := j.nextExpiryLocked()
	expiresAt := receiptExpiryTime(receipt)
	entry := j.expiryByKey[key]
	if expiresAt.IsZero() {
		j.removeExpiry(key)
	} else if entry == nil {
		entry = &receiptExpiry{key: key, expiresAt: expiresAt}
		j.expiryByKey[key] = entry
		heap.Push(&j.expiries, entry)
	} else if !entry.expiresAt.Equal(expiresAt) {
		entry.expiresAt, entry.retryAt = expiresAt, time.Time{}
		heap.Fix(&j.expiries, entry.position)
	}
	if !before.Equal(j.nextExpiryLocked()) {
		j.notifyRetention()
	}
}

func (j *iscpDomainReceipts) removeExpiry(key string) {
	if entry := j.expiryByKey[key]; entry != nil {
		heap.Remove(&j.expiries, entry.position)
		delete(j.expiryByKey, key)
	}
}

func (j *iscpDomainReceipts) nextExpiryLocked() time.Time {
	if len(j.expiries) == 0 {
		return time.Time{}
	}
	return j.expiries[0].dueAt()
}

func (j *iscpDomainReceipts) notifyRetention() {
	select {
	case j.wake <- struct{}{}:
	default:
	}
}

// Pressure notifications never run filesystem cleanup in a request. Coalesce
// sustained pressure to at most one reconciliation request per minute.
func (j *iscpDomainReceipts) pressureCleanup(now time.Time) {
	if !j.lastPressure.IsZero() && now.Sub(j.lastPressure) < time.Minute {
		return
	}
	j.lastPressure = now
	j.rebuild = true
	j.notifyRetention()
}
