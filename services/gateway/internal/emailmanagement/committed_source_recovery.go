package emailmanagement

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Store pointers, not a date-tree walk, define the recovery set. Each batch is
// bounded; sources created after this pass began belong to the running intake.
func (s *Service) reconcileCommittedSources(ctx context.Context) error {
	started := s.now()
	owners, err := s.repository.ListOwnerProfiles(ctx)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if root != nil {
		defer root.Close()
	}
	for _, owner := range owners {
		after := ""
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			captures, err := s.repository.ScanEmailCaptures(ctx, store.EmailCaptureScan{OwnerID: owner.ID, After: after, Limit: 100})
			if err != nil {
				return err
			}
			if len(captures) == 0 {
				break
			}
			for _, capture := range captures {
				after = capture.ID
				if capture.PurgedAt != nil || capture.CreatedAt.After(started) {
					continue
				}
				mail, found, err := s.repository.GetEmailMail(ctx, owner.ID, capture.MailID)
				if err != nil {
					return err
				}
				if !found || mail.CaptureID != capture.ID || mail.SyncState == app.EmailMailSyncSuppressed {
					continue
				}
				box, found, err := s.repository.GetEmailMailbox(ctx, owner.ID, mail.MailboxID)
				if err != nil {
					return err
				}
				if !found {
					continue
				}
				checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				_, files, verifyErr := loadManifest(checkCtx, s.opts.WorkspaceRoot, owner.ID, capture)
				if verifyErr == nil && root == nil {
					root, verifyErr = os.OpenRoot(s.opts.WorkspaceRoot)
					if root != nil {
						defer root.Close()
					}
				}
				if verifyErr == nil {
					for _, source := range files {
						file, err := openVerifiedFile(checkCtx, root, source, maxSourceBytes)
						if err != nil {
							verifyErr = err
							break
						}
						file.Close()
					}
				}
				cancel()
				if verifyErr == nil {
					continue
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				_, err = s.repository.ReportEmailSourceFailure(ctx, store.EmailSourceFailureCommand{
					EmailCommand: command(owner.ID, "source-recovery:"+capture.ID+":"+capture.OriginalSHA256+":"+started.UTC().Format(time.RFC3339Nano)),
					MailboxID:    box.ID, BindingGeneration: box.BindingGeneration, MailID: mail.ID,
					ExpectedCaptureID: capture.ID, ExpectedOriginalSHA256: capture.OriginalSHA256, ErrorCode: "email_source_corrupt",
				})
				if err != nil && store.StoreErrorCodeOf(err) != store.StoreErrorConflict {
					return err
				}
			}
			if len(captures) < 100 {
				break
			}
		}
	}
	return nil
}
