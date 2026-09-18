package emailmanagement

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// cleanupBatch bounds one store transaction; cleanupBatches bounds one request
// so an owner-wide cleanup cannot hold a worker indefinitely. Unprocessed rows
// are reported as partial; startup finishes only already-persisted tombstones.
const cleanupBatch = 100
const cleanupBatches = 20

type CleanupRequest struct {
	Scope          string
	MailID         string
	MailboxID      string
	ConversationID string
	Date           string // "YYYY-MM-DD"
	CommandKey     string
}

type CleanupResult struct {
	Scope   string `json:"scope"`
	Purged  int    `json:"purged"`
	Freed   int64  `json:"freed_bytes"`
	Partial bool   `json:"partial"`
}

// CleanupSource removes locally stored originals for one of four scopes. The
// database is tombstoned first and the bytes are unlinked second, so there is
// never a window where a pointer advertises bytes that are already gone. The
// reverse window is safe: OpenFile refuses a purged capture before it touches
// the filesystem, and startup cleanup finishes any interrupted unlink.
func (s *Service) CleanupSource(ctx context.Context, owner string, req CleanupRequest) (CleanupResult, error) {
	out := CleanupResult{Scope: req.Scope}
	if owner == "" || req.CommandKey == "" {
		return out, ErrInvalidInput
	}
	command := store.EmailCapturePurgeCommand{Scope: req.Scope, MailID: req.MailID, MailboxID: req.MailboxID, ConversationID: req.ConversationID,
		Reason: app.EmailPurgeManual, Limit: cleanupBatch}
	switch req.Scope {
	case store.EmailPurgeScopeMail:
		if req.MailID == "" {
			return out, ErrInvalidInput
		}
	case store.EmailPurgeScopeMailbox:
		if req.MailboxID == "" {
			return out, ErrInvalidInput
		}
	case store.EmailPurgeScopeConversation:
		if req.ConversationID == "" {
			return out, ErrInvalidInput
		}
	case store.EmailPurgeScopeDate:
		day, err := time.Parse("2006-01-02", req.Date)
		if err != nil {
			return out, ErrInvalidInput
		}
		command.DatePath = day.Format("2006/01/02")
	case store.EmailPurgeScopeAll:
	default:
		return out, ErrInvalidInput
	}

	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if err != nil {
		return out, err
	}
	defer root.Close()
	for batch := 0; batch < cleanupBatches; batch++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		command.EmailCommand = store.EmailCommand{OwnerID: owner, CommandKey: fmt.Sprintf("%s:%s:%d", req.CommandKey, req.Scope, batch)}
		command.At = s.now().UTC()
		result, err := s.repository.PurgeEmailCaptures(ctx, command)
		if err != nil {
			return out, err
		}
		for _, capture := range result.Captures {
			freed, err := s.unlinkCapture(root, owner, capture)
			if err != nil {
				return out, err
			}
			out.Purged++
			out.Freed += freed
		}
		if !result.Remaining {
			return out, nil
		}
		command.After = result.NextCursor
	}
	out.Partial = true
	return out, nil
}

// unlinkCapture removes one capture directory. Resolution goes through os.Root,
// which confines every operation to the workspace and refuses to follow symlinks
// out of it, and the path is re-derived from this owner's scope digest first.
func (s *Service) unlinkCapture(root *os.Root, owner string, capture app.EmailCaptureVersion) (int64, error) {
	if _, _, _, ok := captureDirScope(capture.ManifestPath, owner); !ok {
		// Legacy pointers predate the date layout. The tombstone still stands;
		// the cutover sweep removes whatever tree they referred to.
		return 0, nil
	}
	directory := path.Dir(capture.ManifestPath)
	freed := captureBytes(root, directory)
	if err := root.RemoveAll(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	pruneCaptureAncestors(root, directory)
	return freed, nil
}

func captureBytes(root *os.Root, directory string) int64 {
	var total int64
	handle, err := root.Open(directory)
	if err != nil {
		return 0
	}
	entries, err := handle.ReadDir(-1)
	handle.Close()
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if entry.IsDir() {
			total += captureBytes(root, path.Join(directory, entry.Name()))
			continue
		}
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

// pruneCaptureAncestors walks back up source → mail → mailbox → scope → DD → MM
// → YYYY with non-recursive removes, so a directory a concurrent capture just
// populated fails with ENOTEMPTY and is left alone. "email/" is never removed.
func pruneCaptureAncestors(root *os.Root, directory string) {
	for parent := path.Dir(directory); parent != "." && parent != "/" && parent != "email"; parent = path.Dir(parent) {
		if !strings.HasPrefix(parent, "email/") || root.Remove(parent) != nil {
			return
		}
	}
}
