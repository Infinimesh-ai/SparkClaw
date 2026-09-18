package emailmanagement

import (
	"context"
	"errors"
	"fmt"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"os"
	"path"
	"strings"
)

type AssignmentChangeView struct {
	Mail           MessageView `json:"mail"`
	ConversationID string      `json:"conversation_id"`
}
type ConversationDeleteResult struct {
	ConversationID string `json:"conversation_id"`
	DeletedMails   int    `json:"deleted_mails"`
	FreedBytes     int64  `json:"freed_bytes"`
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

func (s *Service) DeleteConversation(ctx context.Context, c store.EmailConversationDelete) (ConversationDeleteResult, error) {
	if c.OwnerID == "" || c.ConversationID == "" || c.CommandKey == "" {
		return ConversationDeleteResult{}, ErrInvalidInput
	}
	conversation, found, err := s.repository.GetEmailConversation(ctx, c.OwnerID, c.ConversationID)
	if err != nil {
		return ConversationDeleteResult{}, err
	}
	if !found {
		// A successful durable delete leaves only its idempotency receipt. Let the
		// store distinguish that replay from a genuinely unknown conversation.
		deleted, deleteErr := s.repository.DeleteEmailConversation(ctx, c)
		if deleteErr != nil {
			return ConversationDeleteResult{}, deleteErr
		}
		if err := s.removeConversationArtifacts(c.OwnerID, deleted.ArtifactPaths); err != nil {
			return ConversationDeleteResult{}, err
		}
		return ConversationDeleteResult{ConversationID: deleted.ConversationID, DeletedMails: deleted.DeletedMails}, nil
	}
	if conversation.InputVersion != c.ExpectedVersion {
		return ConversationDeleteResult{}, ErrConflict
	}
	draftQuery := store.EmailQuery{OwnerID: c.OwnerID, Limit: 100}
	for {
		page, err := s.repository.ListEmailDraftPage(ctx, draftQuery)
		if err != nil {
			return ConversationDeleteResult{}, err
		}
		for _, draft := range page.Items {
			if draft.ConversationID == c.ConversationID && (draft.State == "sending" || draft.State == "unknown") {
				return ConversationDeleteResult{}, ErrConflict
			}
		}
		if page.NextCursor == "" {
			break
		}
		draftQuery.After = page.NextCursor
	}
	cleaned, err := s.CleanupSource(ctx, c.OwnerID, CleanupRequest{
		Scope: store.EmailPurgeScopeConversation, ConversationID: c.ConversationID,
		CommandKey: "delete-conversation-source:" + c.CommandKey,
	})
	if err != nil {
		return ConversationDeleteResult{}, err
	}
	if cleaned.Partial {
		return ConversationDeleteResult{}, fmt.Errorf("conversation source cleanup incomplete")
	}
	deleted, err := s.repository.DeleteEmailConversation(ctx, c)
	if err != nil {
		return ConversationDeleteResult{}, err
	}
	if err := s.removeConversationArtifacts(c.OwnerID, deleted.ArtifactPaths); err != nil {
		return ConversationDeleteResult{}, err
	}
	s.signal()
	return ConversationDeleteResult{ConversationID: deleted.ConversationID, DeletedMails: deleted.DeletedMails, FreedBytes: cleaned.Freed}, nil
}

func (s *Service) removeConversationArtifacts(owner string, artifactPaths []string) error {
	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	analysisPrefix := path.Join("email", ownerScope(owner), "analysis") + "/"
	normalizedPrefix := path.Join("email", ownerScope(owner), "normalized") + "/"
	directories := map[string]bool{}
	for _, artifactPath := range artifactPaths {
		if !safeRelativePath(artifactPath) {
			return ErrInvalidInput
		}
		directory := path.Dir(artifactPath)
		if strings.HasPrefix(artifactPath, analysisPrefix) && path.Dir(directory) == strings.TrimSuffix(analysisPrefix, "/") {
			directories[directory] = true
			continue
		}
		if strings.HasPrefix(artifactPath, normalizedPrefix) {
			relative := strings.TrimPrefix(artifactPath, normalizedPrefix)
			parts := strings.Split(relative, "/")
			if len(parts) == 4 && parts[0] != "" && parts[1] == "render" && parts[2] != "" && (parts[3] == "preview.html" || parts[3] == "content.json") {
				directories[directory] = true
				continue
			}
		}
		if !strings.HasPrefix(artifactPath, analysisPrefix) {
			return ErrInvalidInput
		}
		return ErrInvalidInput
	}
	for directory := range directories {
		if err := root.RemoveAll(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
