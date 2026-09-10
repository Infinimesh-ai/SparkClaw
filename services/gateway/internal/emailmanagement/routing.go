package emailmanagement

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"time"
)

func (s *Service) Notifications(ctx context.Context, q store.EmailQuery) (MessagesView, error) {
	q.Entry = "notification"
	q.ConversationID = ""
	q.PendingOnly = false
	out, err := s.Messages(ctx, q)
	for i := range out.Messages {
		m := &out.Messages[i]
		if m.Classification != nil && m.Classification.NotificationSubtype == "verification" {
			m.BodyText = ""
			m.Summary = ""
			m.Subject = ""
		}
	}
	return out, err
}
func (s *Service) InteractionMails(ctx context.Context, q store.EmailQuery) (MessagesView, error) {
	q.Entry = "interaction"
	q.UnassignedOnly = true
	q.ConversationID = ""
	q.PendingOnly = false
	return s.Messages(ctx, q)
}
func (s *Service) Message(ctx context.Context, owner, id string) (MessageView, error) {
	return stableProjection(ctx, s, owner, func(version int64) (MessageView, error) {
		m, ok, err := s.repository.GetEmailMail(ctx, owner, id)
		if err != nil {
			return MessageView{}, err
		}
		if !ok {
			return MessageView{}, ErrNotFound
		}
		return s.messageView(ctx, owner, m, version)
	})
}
func (s *Service) ChangeClassification(ctx context.Context, c store.EmailClassificationOverride) (MessageView, error) {
	_, err := s.repository.OverrideEmailClassification(ctx, c)
	if err != nil {
		return MessageView{}, err
	}
	return s.Message(ctx, c.OwnerID, c.MailID)
}
func (s *Service) SenderRules(ctx context.Context, q store.EmailQuery) ([]app.EmailSenderRule, error) {
	return s.repository.ListEmailSenderRules(ctx, q)
}
func (s *Service) ChangeSenderRule(ctx context.Context, c store.EmailSenderRuleCommand) (app.EmailSenderRule, error) {
	return s.repository.UpdateEmailSenderRule(ctx, c)
}

type VerificationView struct {
	TimingEvidence string `json:"timing_evidence,omitempty"`
	SourceTime     string `json:"source_time,omitempty"`
	ReceivedAt     string `json:"received_at,omitempty"`
	Purpose        string `json:"purpose"`
	State          string `json:"state"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	ServerNow      string `json:"server_now"`
	CanReveal      bool   `json:"can_reveal"`
	Code           string `json:"code,omitempty"`
}

func emailTimePtr(v *time.Time) string {
	if v == nil {
		return ""
	}
	return emailTime(*v)
}
func (s *Service) Verification(ctx context.Context, owner, id string) (VerificationView, error) {
	m, ok, err := s.repository.GetEmailMail(ctx, owner, id)
	if err != nil {
		return VerificationView{}, err
	}
	if !ok || m.Verification == nil {
		return VerificationView{}, ErrNotFound
	}
	view, err := s.messageView(ctx, owner, m, 0)
	if err != nil {
		return VerificationView{}, err
	}
	out := *view.Verification
	out.Code = m.Verification.Code
	return out, nil
}
