package emailmanagement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

var ErrReplyPolishUnavailable = errors.New("email_reply_polish_unavailable")

type ReplyPolishRequest struct {
	DraftID        string
	MailID         string
	Instruction    string
	OutputLanguage string
}

func (s *Service) PolishReply(ctx context.Context, owner string, request ReplyPolishRequest) (store.EmailDraft, error) {
	instruction := strings.TrimSpace(request.Instruction)
	if s.analyzer == nil {
		return store.EmailDraft{}, ErrReplyPolishUnavailable
	}
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(request.MailID) == "" || strings.TrimSpace(request.DraftID) == "" ||
		(request.OutputLanguage != "zh" && request.OutputLanguage != "en") || instruction == "" || len(instruction) > 4000 || !utf8.ValidString(instruction) || strings.ContainsRune(instruction, 0) {
		return store.EmailDraft{}, ErrInvalidInput
	}
	target, found, err := s.repository.GetEmailMail(ctx, owner, request.MailID)
	if err != nil {
		return store.EmailDraft{}, err
	}
	if !found {
		return store.EmailDraft{}, ErrNotFound
	}
	if target.Direction != "inbound" && target.Direction != "received" {
		return store.EmailDraft{}, ErrInvalidInput
	}
	input := AnalysisInput{
		PolicyVersion:    replyPolishPromptVersion,
		OutputLanguage:   request.OutputLanguage,
		Kind:             replyPolishKind,
		TargetID:         target.ID,
		Subject:          target.Subject,
		ReplyInstruction: instruction,
		Participants:     append([]string{}, target.Participants...),
		Evidence:         []Evidence{},
		Candidates:       []AnalysisCandidate{},
		MissingContext:   []string{},
	}
	rows := []app.EmailMail{target}
	if target.ConversationID != "" {
		page, listErr := s.repository.ListEmailMails(ctx, store.EmailQuery{OwnerID: owner, ConversationID: target.ConversationID, Limit: 12})
		if listErr != nil {
			return store.EmailDraft{}, listErr
		}
		rows = page.Items
		if page.NextCursor != "" {
			input.MissingContext = append(input.MissingContext, "conversation_window_limited")
		}
	}
	seenTarget := false
	for _, mail := range rows {
		if mail.ID == target.ID {
			seenTarget = true
		}
		if err := s.addMailEvidence(ctx, owner, mail, &input, 2400); err != nil {
			return store.EmailDraft{}, err
		}
		input.addEvidence("mail:"+mail.ID+":role", fmt.Sprintf("direction=%s; participants=%s; sent_at=%s", mail.Direction, strings.Join(mail.Participants, ", "), mail.SourceTime.UTC().Format("2006-01-02T15:04:05Z")), 600)
	}
	if !seenTarget {
		if err := s.addMailEvidence(ctx, owner, target, &input, 2400); err != nil {
			return store.EmailDraft{}, err
		}
	}
	input, _, _, err = finalizeInput(input, nil, 0)
	if err != nil {
		return store.EmailDraft{}, err
	}
	output, err := s.analyzer.Analyze(ctx, input)
	if err != nil {
		return store.EmailDraft{}, err
	}
	if err := validateAnalysis(input, output); err != nil {
		return store.EmailDraft{}, err
	}
	return s.SaveDraft(ctx, owner, store.EmailDraft{
		ID: request.DraftID, MailboxID: target.MailboxID, Mode: "reply", ReplyMailID: target.ID, Body: strings.TrimSpace(output.Body),
	}, 0)
}
