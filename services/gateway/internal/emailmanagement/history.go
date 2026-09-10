package emailmanagement

import (
	"context"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// mailHistory describes only proved source relationships. It never infers
// missing messages from quoted prose or performs browser work while reading.
func (s *Service) mailHistory(ctx context.Context, owner string, mail app.EmailMail) (string, string, error) {
	state, reason := "complete", ""
	box, found, err := s.repository.GetEmailMailbox(ctx, owner, mail.MailboxID)
	if err != nil {
		return "", "", err
	}
	setGap := func(next, why string) {
		if state != "failed" && (state == "complete" || next == "partial" || next == "failed") {
			state, reason = next, why
		}
	}
	seen := map[string]bool{}
	for _, ref := range mail.ReplyReferences {
		if ref == "" || ref == mail.MessageID || seen[ref] {
			continue
		}
		seen[ref] = true
		if len(seen) > 100 {
			setGap("partial", "reply_reference_check_limit")
			break
		}
		page, err := s.repository.ListEmailMails(ctx, store.EmailQuery{OwnerID: owner, MailboxID: mail.MailboxID, MailMessageID: ref, Limit: 100})
		if err != nil {
			return "", "", err
		}
		if len(page.Items) == 0 {
			setGap("partial", "reply_source_unavailable")
			continue
		}
		ready := false
		for _, previous := range page.Items {
			if previous.ParseState == app.EmailParseFailed {
				setGap("failed", "reply_source_failed")
			}
			if previous.CaptureState != app.EmailCaptureComplete && previous.LocalSendID == "" {
				continue
			}
			ready = true
			if previous.ConversationID != "" && mail.ConversationID != "" && previous.ConversationID != mail.ConversationID {
				// A source thread can legitimately span events. Keep the relation visible
				// without silently merging events or claiming it was shown in this event.
				setGap("partial", "reply_in_other_event")
			} else if previous.ConversationID == "" {
				setGap("pending", "reply_assignment_pending")
			}
		}
		if !ready {
			for _, previous := range page.Items {
				if previous.CaptureState == app.EmailCaptureFailed || previous.ParseState == app.EmailParseFailed {
					setGap("failed", "reply_source_failed")
				}
			}
			setGap("pending", "reply_capture_pending")
		}
	}
	if mail.ProviderThreadID != "" {
		thread, ok, err := s.repository.GetEmailThread(ctx, owner, store.EmailProviderThreadID(mail.MailboxID, mail.ProviderThreadID))
		if err != nil {
			return "", "", err
		}
		if ok && thread.ErrorCode != "" {
			state, reason = "failed", thread.ErrorCode
		} else if !ok || thread.Coverage == "pending" {
			setGap("pending", "thread_history_pending")
		} else if thread.Coverage != "complete_for_observation" || len(thread.Gaps) > 0 || thread.Cursor != "" {
			why := strings.Join(thread.Gaps, ",")
			if why == "" {
				why = "thread_history_partial"
			}
			next := "partial"
			if thread.Cursor != "" {
				next = "pending"
			}
			setGap(next, why)
		}
		page, err := s.repository.ListEmailMails(ctx, store.EmailQuery{OwnerID: owner, MailboxID: mail.MailboxID, MailThreadID: mail.ProviderThreadID, Limit: 100})
		if err != nil {
			return "", "", err
		}
		if page.NextCursor != "" {
			setGap("partial", "thread_member_check_limit")
		}
		for _, member := range page.Items {
			if member.ID == mail.ID {
				continue
			}
			if member.CaptureState == app.EmailCaptureFailed || member.ParseState == app.EmailParseFailed {
				setGap("failed", "thread_source_failed")
			} else if member.CaptureState != app.EmailCaptureComplete && member.LocalSendID == "" {
				setGap("pending", "thread_capture_pending")
			} else if member.ConversationID == "" {
				setGap("pending", "thread_assignment_pending")
			}
		}
	}
	if state != "complete" && (!found || !box.Active || !box.IntakeEnabled) {
		return "paused", "receiving_disabled", nil
	}
	return state, reason, nil
}
