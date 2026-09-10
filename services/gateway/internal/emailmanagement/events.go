package emailmanagement

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type AssignmentChangeView struct {
	Mail           MessageView `json:"mail"`
	ConversationID string      `json:"conversation_id"`
}

func (s *Service) ChangeAssignment(ctx context.Context, c store.EmailManualAssignment) (AssignmentChangeView, error) {
	if _, err := s.repository.ActivateEmailEventPolicy(ctx, command(c.OwnerID, "activate-source-events-v4")); err != nil {
		return AssignmentChangeView{}, err
	}
	m, err := s.repository.ChangeEmailAssignment(ctx, c)
	if err != nil {
		return AssignmentChangeView{}, err
	}
	view, err := s.Message(ctx, c.OwnerID, m.ID)
	s.signal()
	return AssignmentChangeView{view, m.ConversationID}, err
}
func (s *Service) RenameConversation(ctx context.Context, c store.EmailConversationRename) (ConversationDetail, error) {
	_, err := s.repository.RenameEmailConversation(ctx, c)
	if err != nil {
		return ConversationDetail{}, err
	}
	return s.Conversation(ctx, c.OwnerID, c.ConversationID)
}
