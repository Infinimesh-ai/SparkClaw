package emailmanagement

import (
	"context"
	"os"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// These typed reads let client mail synchronization use the same authoritative
// repository as collection without adding mail to Gateway's general Store.
func (s *Service) GetEmailOwnerStatus(ctx context.Context, owner string) (app.EmailOwnerStatus, error) {
	return s.repository.GetEmailOwnerStatus(ctx, owner)
}

func (s *Service) GetEmailMailbox(ctx context.Context, owner, mailbox string) (app.EmailMailbox, bool, error) {
	return s.repository.GetEmailMailbox(ctx, owner, mailbox)
}

func (s *Service) ListEmailMailboxes(ctx context.Context, owner string) ([]app.EmailMailbox, error) {
	return s.repository.ListEmailMailboxes(ctx, owner)
}

func (s *Service) GetEmailMail(ctx context.Context, owner, mail string) (app.EmailMail, bool, error) {
	return s.repository.GetEmailMail(ctx, owner, mail)
}

// ClientSyncMessages is a read-only typed mail projection. Unlike the ordinary
// UI list it includes pending mail and bounded plain body text for offline use.
// It never synchronizes provider cursors, credential/profile state or disk paths.
func (s *Service) ClientSyncMessages(ctx context.Context, q store.EmailQuery) (MessagesView, error) {
	if err := s.validateQuery(ctx, q); err != nil {
		return MessagesView{}, err
	}
	return stableProjection(ctx, s, q.OwnerID, func(version int64) (MessagesView, error) {
		page, err := s.repository.ListEmailMails(ctx, q)
		out := MessagesView{Version: version, Messages: []MessageView{}, NextCursor: page.NextCursor}
		if err != nil {
			return out, err
		}
		root, err := os.OpenRoot(s.opts.WorkspaceRoot)
		if err != nil {
			return out, err
		}
		defer root.Close()
		for _, row := range page.Items {
			view, err := s.messageView(ctx, q.OwnerID, row, version, root)
			if err != nil {
				return out, err
			}
			// One-time verification secrets require the existing explicit reveal
			// endpoint and are never retained in an unattended offline cache.
			if row.Verification != nil {
				code := row.Verification.Code
				if code != "" {
					view.Subject = strings.ReplaceAll(view.Subject, code, "••••••")
					view.BodyText = strings.ReplaceAll(view.BodyText, code, "••••••")
				}
				view.Summary = ""
			} else if row.Classification != nil && row.Classification.NotificationSubtype == "verification" {
				view.Subject, view.BodyText, view.Summary = "", "", ""
			}
			view.Verification = nil
			// Sync manifests carry integrity metadata for an explicitly saved
			// client-local copy. Legacy mail responses remain unchanged.
			if row.RepresentationID != "" {
				representation, found, err := s.repository.GetEmailRepresentation(ctx, q.OwnerID, row.RepresentationID)
				if err != nil {
					return out, err
				}
				if !found {
					return out, ErrNotFound
				}
				for i := range view.Attachments {
					for _, attachment := range representation.Attachments {
						if attachment.ID == view.Attachments[i].ID {
							view.Attachments[i].SHA256 = attachment.SHA256
							break
						}
					}
				}
			}
			out.Messages = append(out.Messages, view)
		}
		return out, nil
	})
}
