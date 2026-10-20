package gateway

import (
	"container/heap"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// A batch handles only due heap entries or a requested reconciliation pass.
// Release the journal lock after every record. If a request already owns it,
// yield the batch instead of waiting or holding it across the whole scan.
func (j *iscpDomainReceipts) sweep(ctx context.Context, now time.Time) (iscpReceiptSweep, error) {
	var result iscpReceiptSweep
	deadline := time.Now().Add(domainReceiptSweepBudget)
	for result.Scanned < domainReceiptSweepLimit && time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !j.mu.TryLock() {
			result.More = true
			return result, nil
		}
		worked, expired, err := j.sweepOneLocked(now)
		result.More = j.rebuild || (!j.nextExpiryLocked().IsZero() && !j.nextExpiryLocked().After(now))
		j.mu.Unlock()
		if worked {
			result.Scanned++
		}
		if expired {
			result.Expired++
		}
		if err != nil || !worked {
			return result, err
		}
	}
	return result, nil
}

func (j *iscpDomainReceipts) sweepOneLocked(now time.Time) (worked, expired bool, err error) {
	if next := j.nextExpiryLocked(); !next.IsZero() && !next.After(now) {
		entry := j.expiries[0]
		receipt, found, err := j.readReceipt(filepath.Join(j.root, entry.key), nil)
		if err == nil {
			expiresAt := receiptExpiryTime(receipt)
			switch {
			case !found || expiresAt.IsZero():
				j.removeExpiry(entry.key)
			case expiresAt.After(now):
				// The index is advisory; a replacement cannot be deleted early.
				j.indexReceipt(entry.key, receipt)
			default:
				err = j.retireReceipt(entry.key, receipt)
				if err == nil {
					j.removeExpiry(entry.key)
					return true, true, nil
				}
			}
		}
		if err != nil {
			// A broken earliest record must not hot-loop or starve other keys.
			entry.retryAt = now.Add(time.Minute)
			heap.Fix(&j.expiries, entry.position)
		}
		return true, false, err
	}
	if !j.rebuild {
		return false, false, nil
	}
	if j.scan == nil {
		j.scan, err = os.Open(j.root)
		if err != nil {
			// A later hourly/pressure request can retry an inaccessible root.
			j.rebuild = false
			return false, false, err
		}
	}
	names, err := j.scan.Readdirnames(1)
	if err != nil {
		j.closeScanLocked()
		j.rebuild = false
		if errors.Is(err, io.EOF) {
			return false, false, nil
		}
		return false, false, err
	}
	key := names[0]
	if !receiptKeyValid(key) {
		return true, false, nil
	}
	receipt, found, err := j.readReceipt(filepath.Join(j.root, key), nil)
	if err == nil && found {
		j.indexReceipt(key, receipt)
	}
	return true, false, err
}
