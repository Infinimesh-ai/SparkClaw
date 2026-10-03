package emailmanagement

import (
	"context"
	"os"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

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
			out.Messages = append(out.Messages, view)
		}
		return out, nil
	})
}
