package emailmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// One bounded cleanup batch. Startup drains the explicit tombstone index only.
const recoverTombstones = 200

// reapPurged finishes cleanups that were interrupted between the tombstone and
// the unlink. The "purged" index makes this a lookup rather than a walk, and
// finalizing each page is what stops it growing without bound.
func (s *Service) reapPurged(ctx context.Context, root *os.Root, owner string) error {
	for done := 0; done < recoverTombstones; {
		captures, err := s.repository.ScanEmailCaptures(ctx, store.EmailCaptureScan{OwnerID: owner, State: "purged", Limit: 100})
		if err != nil || len(captures) == 0 {
			return err
		}
		ids := make([]string, 0, len(captures))
		for _, capture := range captures {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := s.unlinkCapture(root, owner, capture); err != nil {
				return err
			}
			ids = append(ids, capture.ID)
		}
		// The key must be derived from the exact batch. Keying on the first ID
		// and a count collides whenever a later batch happens to start with the
		// same capture, and the store then rejects it as a conflicting replay.
		if _, err := s.repository.PurgeEmailCaptures(ctx, store.EmailCapturePurgeCommand{
			EmailCommand: command(owner, "reap-purged:"+batchKey(ids)), Scope: store.EmailPurgeScopeCaptureIDs,
			CaptureIDs: ids, Reason: app.EmailPurgeManual, At: s.now().UTC(), Finalize: true}); err != nil {
			return err
		}
		done += len(ids)
	}
	return nil
}

// batchKey derives a stable idempotency key from the exact set of IDs, so a
// replay of the same batch is a no-op while a different batch never collides.
func batchKey(ids []string) string {
	digest := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	return hex.EncodeToString(digest[:16])
}
