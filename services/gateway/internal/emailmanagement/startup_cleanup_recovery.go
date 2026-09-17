package emailmanagement

import (
	"context"
	"errors"
	"os"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Only an explicit cleanup tombstone authorizes unlinking here. This startup
// pass does not adopt orphan sources or sweep staging: timeline journals may
// still own those bytes, and normal polling must never rescan their history.
func (s *Service) reconcilePurgedSources(ctx context.Context) error {
	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	owners, err := s.repository.ListOwnerProfiles(ctx)
	if err != nil {
		return err
	}
	for _, owner := range owners {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			pending, err := s.repository.ScanEmailCaptures(ctx, store.EmailCaptureScan{OwnerID: owner.ID, State: "purged", Limit: 1})
			if err != nil {
				return err
			}
			if len(pending) == 0 {
				break
			}
			if err := s.reapPurged(ctx, root, owner.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
