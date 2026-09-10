package emailmanagement

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

var (
	ErrNotFound       = errors.New("email record not found")
	ErrInvalidInput   = errors.New("invalid email request")
	ErrConflict       = errors.New("email state changed")
	ErrProjectionBusy = errors.New("email projection changed; retry")
	ErrNotEnabled     = errors.New("email receiving is not enabled for this mailbox")
)

type ConcernView struct {
	ID                     string   `json:"id"`
	Kind                   string   `json:"kind"`
	Evidence               string   `json:"evidence"`
	RelatedConversationIDs []string `json:"related_conversation_ids"`
	Version                int64    `json:"version"`
}
type ConversationView struct {
	HistoricalMixed bool          `json:"historical_mixed"`
	ID              string        `json:"id"`
	Version         int64         `json:"version"`
	Title           string        `json:"title"`
	Participants    []string      `json:"participants"`
	Summary         string        `json:"summary,omitempty"`
	SummaryState    string        `json:"summary_state"`
	UnseenCount     int           `json:"unseen_count"`
	LastActivityAt  string        `json:"last_activity_at,omitempty"`
	Concerns        []ConcernView `json:"concerns"`
	ProcessingState string        `json:"processing_state"`
}
type AttachmentView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Available bool   `json:"available"`
}
type MessageView struct {
	ConfirmationSource        string                   `json:"confirmation_source,omitempty"`
	LocalSendID               string                   `json:"local_send_id,omitempty"`
	ReplyMailID               string                   `json:"reply_mail_id,omitempty"`
	SourceState               string                   `json:"source_state,omitempty"`
	CurrentSenderAddress      string                   `json:"current_sender_address"`
	CurrentSenderRuleRevision int64                    `json:"current_sender_rule_revision"`
	Verification              *VerificationView        `json:"verification,omitempty"`
	Classification            *app.EmailClassification `json:"classification,omitempty"`
	ConversationID            string                   `json:"conversation_id,omitempty"`
	ID                        string                   `json:"id"`
	Version                   int64                    `json:"version"`
	MailboxID                 string                   `json:"mailbox_id"`
	ReceivingAddress          string                   `json:"receiving_address"`
	Direction                 string                   `json:"direction"`
	From                      string                   `json:"from"`
	To                        []string                 `json:"to"`
	CC                        []string                 `json:"cc"`
	Subject                   string                   `json:"subject"`
	SentAt                    string                   `json:"sent_at,omitempty"`
	ArrivedAt                 string                   `json:"arrived_at"`
	Summary                   string                   `json:"summary,omitempty"`
	SummaryState              string                   `json:"summary_state"`
	BodyText                  string                   `json:"body_text,omitempty"`
	Viewed                    bool                     `json:"viewed"`
	OriginalAvailable         bool                     `json:"original_available"`
	Attachments               []AttachmentView         `json:"attachments"`
	ProcessingState           string                   `json:"processing_state"`
}
type MailboxView struct {
	ID            string `json:"id"`
	Version       int64  `json:"version"`
	Provider      string `json:"provider"`
	Address       string `json:"address"`
	IntakeEnabled bool   `json:"intake_enabled"`
	ActiveBinding bool   `json:"active_binding"`
	State         string `json:"state"`
	LastSyncAt    string `json:"last_sync_at,omitempty"`
	CoverageStart string `json:"coverage_start,omitempty"`
	CoverageEnd   string `json:"coverage_end,omitempty"`
	Gap           string `json:"gap,omitempty"`
	Error         string `json:"error,omitempty"`
}
type ConversationsView struct {
	Counts        *store.EmailScopeCounts `json:"counts,omitempty"`
	Version       int64                   `json:"version"`
	Conversations []ConversationView      `json:"conversations"`
	NextCursor    string                  `json:"next_cursor,omitempty"`
}
type ConversationDetail struct {
	Version      int64            `json:"version"`
	Conversation ConversationView `json:"conversation"`
}
type MessagesView struct {
	Counts     *store.EmailScopeCounts `json:"counts,omitempty"`
	ServerNow  string                  `json:"server_now"`
	Version    int64                   `json:"version"`
	Messages   []MessageView           `json:"messages"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}
type StatusView struct {
	Version      int64         `json:"version"`
	Mailboxes    []MailboxView `json:"mailboxes"`
	Backlog      int           `json:"backlog"`
	PendingCount int           `json:"pending_count"`
}
type ViewedResult struct {
	Version int64    `json:"version"`
	MailIDs []string `json:"mail_ids"`
}
type ScheduleResult struct {
	Scheduled bool `json:"scheduled"`
}

// Repository queries are individually consistent. A bounded revision check
// prevents a projection assembled across writes from claiming a newer version.
func stableProjection[T any](ctx context.Context, s *Service, owner string, read func(int64) (T, error)) (T, error) {
	var zero T
	if strings.TrimSpace(owner) == "" {
		return zero, ErrInvalidInput
	}
	for attempt := 0; attempt < 3; attempt++ {
		before, err := s.repository.GetEmailOwnerStatus(ctx, owner)
		if err != nil {
			return zero, err
		}
		result, err := read(before.Revision)
		if err != nil {
			return zero, err
		}
		after, err := s.repository.GetEmailOwnerStatus(ctx, owner)
		if err != nil {
			return zero, err
		}
		if before.Revision == after.Revision {
			return result, nil
		}
	}
	return zero, ErrProjectionBusy
}
func emailTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func nonNilStrings(values []string) []string { return append([]string{}, values...) }
func (s *Service) validateQuery(ctx context.Context, q store.EmailQuery) error {
	if strings.TrimSpace(q.OwnerID) == "" || q.Limit < 0 || q.Limit > 100 || len(q.Search) > 500 || len(q.After) > 2048 {
		return ErrInvalidInput
	}
	if q.MailboxID != "" {
		if _, found, err := s.repository.GetEmailMailbox(ctx, q.OwnerID, q.MailboxID); err != nil {
			return err
		} else if !found {
			return ErrNotFound
		}
	}
	return nil
}
func (s *Service) QueryConversations(ctx context.Context, q store.EmailQuery) (ConversationsView, error) {
	if err := s.validateQuery(ctx, q); err != nil {
		return ConversationsView{}, err
	}
	return stableProjection(ctx, s, q.OwnerID, func(version int64) (ConversationsView, error) {
		page, err := s.repository.ListEmailConversations(ctx, q)
		out := ConversationsView{Counts: page.Counts, Version: version, Conversations: []ConversationView{}, NextCursor: page.NextCursor}
		if err != nil {
			return out, err
		}
		for _, row := range page.Items {
			view, err := s.conversationView(ctx, q.OwnerID, row, version)
			if err != nil {
				return out, err
			}
			out.Conversations = append(out.Conversations, view)
		}
		return out, nil
	})
}
func (s *Service) Conversation(ctx context.Context, owner, id string) (ConversationDetail, error) {
	return stableProjection(ctx, s, owner, func(version int64) (ConversationDetail, error) {
		row, found, err := s.repository.GetEmailConversation(ctx, owner, id)
		if err != nil {
			return ConversationDetail{}, err
		}
		if !found {
			return ConversationDetail{}, ErrNotFound
		}
		view, err := s.conversationView(ctx, owner, row, version)
		return ConversationDetail{Version: version, Conversation: view}, err
	})
}
func (s *Service) conversationView(ctx context.Context, owner string, row app.EmailConversation, version int64) (ConversationView, error) {
	out := ConversationView{HistoricalMixed: row.HistoricalMixed, ID: row.ID, Version: version, Title: row.Title, Participants: nonNilStrings(row.Participants), UnseenCount: row.UnseenCount, LastActivityAt: emailTime(row.UpdatedAt), Concerns: []ConcernView{}, ProcessingState: "ready"}
	target, found, err := s.repository.GetEmailAnalysisTarget(ctx, owner, app.EmailJobConversationSummary, row.ID)
	if err != nil {
		return out, err
	}
	out.Summary, out.SummaryState = projectSummary(row.Summary, target, found)
	rows, err := s.repository.ListEmailConcerns(ctx, store.EmailQuery{OwnerID: owner, ConversationID: row.ID, Limit: 100})
	if err != nil {
		return out, err
	}
	for _, concern := range rows {
		related := []string{}
		for _, id := range concern.ConversationIDs {
			if id != row.ID {
				related = append(related, id)
			}
		}
		out.Concerns = append(out.Concerns, ConcernView{ID: concern.ID, Kind: concern.Kind, Evidence: concern.Reason, RelatedConversationIDs: related, Version: concern.Version})
	}
	out.ProcessingState = out.SummaryState
	return out, nil
}
func projectSummary(summary *app.EmailSummary, target app.EmailAnalysisTarget, found bool) (string, string) {
	text, state := "", app.EmailSummaryPending
	if found {
		state = target.State
	}
	if summary != nil {
		text = summary.Text
		if summary.Current && (!found || target.State == app.EmailSummaryCurrent) {
			state = app.EmailSummaryCurrent
		} else if !summary.Current && state != app.EmailSummaryFailed {
			state = app.EmailSummaryStale
		}
	}
	return text, state
}
func (s *Service) Messages(ctx context.Context, q store.EmailQuery) (MessagesView, error) {
	if err := s.validateQuery(ctx, q); err != nil {
		return MessagesView{}, err
	}
	if q.ConversationID != "" {
		if _, found, err := s.repository.GetEmailConversation(ctx, q.OwnerID, q.ConversationID); err != nil {
			return MessagesView{}, err
		} else if !found {
			return MessagesView{}, ErrNotFound
		}
	}
	q.CapturedOnly = !q.PendingOnly
	return stableProjection(ctx, s, q.OwnerID, func(version int64) (MessagesView, error) {
		page, err := s.repository.ListEmailMails(ctx, q)
		out := MessagesView{Counts: page.Counts, ServerNow: emailTime(page.ServerNow), Version: version, Messages: []MessageView{}, NextCursor: page.NextCursor}
		if err != nil {
			return out, err
		}
		for _, row := range page.Items {
			view, err := s.messageView(ctx, q.OwnerID, row, version)
			if err != nil {
				return out, err
			}
			if row.Verification != nil || (row.Classification != nil && row.Classification.NotificationSubtype == "verification") {
				if row.Verification != nil {
					code := row.Verification.Code
					view.Subject = strings.ReplaceAll(view.Subject, code, "••••••")
					view.BodyText = strings.ReplaceAll(view.BodyText, code, "••••••")
					view.Summary = ""
				} else {
					view.Subject = ""
					view.BodyText = ""
					view.Summary = ""
				}
			}
			out.Messages = append(out.Messages, view)
		}
		return out, nil
	})
}
func (s *Service) Pending(ctx context.Context, q store.EmailQuery) (MessagesView, error) {
	q.PendingOnly = true
	q.ConversationID = ""
	return s.Messages(ctx, q)
}
func (s *Service) messageView(ctx context.Context, owner string, row app.EmailMail, version int64) (MessageView, error) {
	out := MessageView{ConfirmationSource: row.SendConfirmationSource, LocalSendID: row.LocalSendID, ReplyMailID: row.ReplyMailID, SourceState: row.CaptureState, Classification: row.Classification, ConversationID: row.ConversationID, ID: row.ID, Version: version, MailboxID: row.MailboxID, Direction: row.Direction, Subject: row.Subject, SentAt: emailTime(row.SourceTime), ArrivedAt: emailTime(row.DiscoveredAt), Viewed: row.ViewedAt != nil, To: []string{}, CC: []string{}, Attachments: []AttachmentView{}, ProcessingState: row.AssignmentState}
	mailbox, found, err := s.repository.GetEmailMailbox(ctx, owner, row.MailboxID)
	if err != nil {
		return out, err
	}
	if !found {
		return out, ErrNotFound
	}
	if row.Verification != nil {
		v := row.Verification
		out.Verification = &VerificationView{TimingEvidence: v.ExpiryEvidence, SourceTime: emailTime(row.SourceTime), ReceivedAt: emailTime(row.DiscoveredAt), Purpose: v.Purpose, State: "validity_unknown", ExpiresAt: emailTimePtr(v.ExpiresAt), ServerNow: emailTime(s.now()), CanReveal: true}
		if v.Code != "" {
			out.Verification.TimingEvidence = strings.ReplaceAll(out.Verification.TimingEvidence, v.Code, "••••••")
			out.Verification.Purpose = strings.ReplaceAll(out.Verification.Purpose, v.Code, "••••••")
			if out.Classification != nil {
				classification := *out.Classification
				classification.RequestedResponse = strings.ReplaceAll(classification.RequestedResponse, v.Code, "••••••")
				classification.Purpose = strings.ReplaceAll(classification.Purpose, v.Code, "••••••")
				classification.ServiceLabel = strings.ReplaceAll(classification.ServiceLabel, v.Code, "••••••")
				classification.Reason = strings.ReplaceAll(classification.Reason, v.Code, "••••••")
				classification.Evidence = append([]app.EmailClassificationEvidence(nil), classification.Evidence...)
				for i := range classification.Evidence {
					classification.Evidence[i].Text = strings.ReplaceAll(classification.Evidence[i].Text, v.Code, "••••••")
				}
				out.Classification = &classification
			}
		}
		if v.ExpiresAt != nil {
			out.Verification.State = "not_expired"
			if !s.now().Before(*v.ExpiresAt) {
				out.Verification.State = "expired"
			}
		}
	}
	out.ReceivingAddress = mailbox.Address
	if row.CaptureID != "" {
		capture, found, err := s.repository.GetEmailCapture(ctx, owner, row.CaptureID)
		if err != nil {
			return out, err
		}
		out.OriginalAvailable = found && capture.OriginalPath != ""
	}
	if row.RepresentationID != "" {
		representation, found, err := s.repository.GetEmailRepresentation(ctx, owner, row.RepresentationID)
		if err != nil {
			return out, err
		}
		if !found {
			return out, ErrNotFound
		}
		out.Subject, out.From, out.To, out.CC, out.BodyText = representation.Subject, strings.Join(representation.From, ", "), nonNilStrings(representation.To), nonNilStrings(representation.CC), representation.BodyText
		out.SentAt = emailTime(representation.SourceTime)
		if len(representation.From) == 1 {
			if addr, err := mail.ParseAddress(representation.From[0]); err == nil {
				i := strings.LastIndex(addr.Address, "@")
				if i > 0 {
					address := addr.Address[:i] + "@" + strings.ToLower(addr.Address[i+1:])
					out.CurrentSenderAddress = address
					rules, err := s.repository.ListEmailSenderRules(ctx, store.EmailQuery{OwnerID: owner, Search: address, Limit: 1})
					if err != nil {
						return out, err
					}
					if len(rules) > 0 {
						out.CurrentSenderRuleRevision = rules[0].Revision
					}
				}
			}
		}

		for _, part := range representation.Attachments {
			out.Attachments = append(out.Attachments, AttachmentView{ID: part.ID, Name: part.Name, Size: part.SizeBytes, Available: part.Path != ""})
		}
	}
	target, found, err := s.repository.GetEmailAnalysisTarget(ctx, owner, app.EmailJobMessageSummary, row.ID)
	if err != nil {
		return out, err
	}
	out.Summary, out.SummaryState = projectSummary(row.Summary, target, found)
	switch {
	case row.ParseState == app.EmailParseFailed || row.CaptureState == app.EmailCaptureFailed:
		out.ProcessingState = "failed"
	case row.ParseState == app.EmailParsePartial || row.CaptureState == app.EmailCapturePartial:
		out.ProcessingState = "partial"
	case row.ConversationID != "":
		out.ProcessingState = out.SummaryState
	}
	return out, nil
}
func ProjectMailbox(mailbox app.EmailMailbox) MailboxView {
	out := MailboxView{ID: mailbox.ID, Version: mailbox.Version, Provider: mailbox.Provider, Address: mailbox.Address, IntakeEnabled: mailbox.IntakeEnabled, ActiveBinding: mailbox.Active, State: "active", LastSyncAt: emailTime(mailbox.LastCheckedAt), CoverageStart: emailTime(mailbox.ActivatedAt), CoverageEnd: emailTime(mailbox.Boundary)}
	if !mailbox.Active || !mailbox.IntakeEnabled {
		out.State = "paused"
	}
	if mailbox.ErrorCode != "" {
		out.State = "needs_attention"
		out.Error = "Email receiving needs attention. Check the signed-in account and retry."
	}
	if mailbox.Cursor != "" || (mailbox.Coverage != "" && mailbox.Coverage != "complete_for_observation" && mailbox.Coverage != "complete") {
		out.Gap = "The admitted interval has not been fully scanned."
	}
	return out
}
func (s *Service) Status(ctx context.Context, owner string) (StatusView, error) {
	return stableProjection(ctx, s, owner, func(version int64) (StatusView, error) {
		status, err := s.repository.GetEmailOwnerStatus(ctx, owner)
		out := StatusView{Version: version, Mailboxes: []MailboxView{}, Backlog: status.BacklogCount, PendingCount: status.PendingCount}
		if err != nil {
			return out, err
		}
		mailboxes, err := s.repository.ListEmailMailboxes(ctx, owner)
		if err != nil {
			return out, err
		}
		for _, mailbox := range mailboxes {
			out.Mailboxes = append(out.Mailboxes, ProjectMailbox(mailbox))
		}
		return out, nil
	})
}
func (s *Service) View(ctx context.Context, owner string, ids []string) (ViewedResult, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return ViewedResult{}, ErrInvalidInput
	}
	receipts, err := s.repository.MarkEmailMailsViewed(ctx, store.EmailViewedCommand{EmailCommand: command(owner, app.NewID("email_view")), MailIDs: ids, ViewedAt: s.now()})
	if err != nil {
		return ViewedResult{}, err
	}
	status, err := s.repository.GetEmailOwnerStatus(ctx, owner)
	if err != nil {
		return ViewedResult{}, err
	}
	out := ViewedResult{Version: status.Revision, MailIDs: []string{}}
	for _, receipt := range receipts {
		out.MailIDs = append(out.MailIDs, receipt.MailID)
	}
	return out, nil
}
func (s *Service) Sync(ctx context.Context, owner, mailboxID string) (ScheduleResult, error) {
	mailboxes, err := s.repository.ListEmailMailboxes(ctx, owner)
	if err != nil {
		return ScheduleResult{}, err
	}
	scheduled := false
	found := mailboxID == ""
	for _, mailbox := range mailboxes {
		if mailboxID != "" && mailbox.ID != mailboxID {
			continue
		}
		found = true
		if !mailbox.Active || !mailbox.IntakeEnabled {
			continue
		}
		_, err = s.repository.RequestEmailJob(ctx, store.EmailJobRequest{EmailCommand: command(owner, app.NewID("email_sync")), Kind: app.EmailJobDiscover, TargetID: mailbox.ID, MailboxID: mailbox.ID, BindingGeneration: mailbox.BindingGeneration, Rearm: true})
		if err != nil {
			return ScheduleResult{}, err
		}
		scheduled = true
	}
	if !found {
		return ScheduleResult{}, ErrNotFound
	}
	if !scheduled {
		return ScheduleResult{}, ErrNotEnabled
	}
	s.signal()
	return ScheduleResult{Scheduled: true}, nil
}
func (s *Service) Reanalyze(ctx context.Context, owner, id string) (ScheduleResult, error) {
	mail, found, err := s.repository.GetEmailMail(ctx, owner, id)
	if err != nil {
		return ScheduleResult{}, err
	}
	if !found {
		return ScheduleResult{}, ErrNotFound
	}
	if mail.CaptureID == "" {
		return s.Sync(ctx, owner, mail.MailboxID)
	}
	kinds := []string{app.EmailJobClassification}
	if mail.Classification != nil && mail.Classification.EffectiveEntry == "interaction" {
		kinds = append(kinds, app.EmailJobMessageSummary)
		if mail.ConversationID != "" {
			kinds = append(kinds, app.EmailJobRelationshipCheck)
		} else {
			kinds = append(kinds, app.EmailJobAssignment)
		}
	}
	if mail.RepresentationID == "" || mail.ParseState == app.EmailParseFailed || mail.ParseState == app.EmailParsePartial || mail.ParseState == app.EmailParseUnsupported {
		kinds = []string{app.EmailJobParse}
	}
	for _, kind := range kinds {
		_, err = s.repository.RequestEmailJob(ctx, store.EmailJobRequest{EmailCommand: command(owner, app.NewID("email_reanalyze")), Kind: kind, TargetID: id, Rearm: true, ForceAnalysis: kind != app.EmailJobParse})
		if err != nil {
			return ScheduleResult{}, err
		}
	}
	if mail.ConversationID != "" {
		_, err = s.repository.RequestEmailJob(ctx, store.EmailJobRequest{EmailCommand: command(owner, app.NewID("email_reanalyze")), Kind: app.EmailJobConversationSummary, TargetID: mail.ConversationID, Rearm: true, ForceAnalysis: true})
		if err != nil {
			return ScheduleResult{}, err
		}
	}
	s.signal()
	return ScheduleResult{Scheduled: true}, nil
}
