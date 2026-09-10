package emailmanagement

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"strings"
	"testing"
)

type bodyOnlyRepository struct{ *store.MemoryStore }

func (r *bodyOnlyRepository) GetEmailRepresentation(context.Context, string, string) (app.EmailRepresentation, bool, error) {
	return app.EmailRepresentation{ID: "r", MailID: "m", BodyText: "Body request", State: app.EmailParseReady, Attachments: []app.EmailAttachment{{ID: "a", Text: "SECRET_ATTACHMENT_CONTENT", Name: "secret", State: app.EmailParseReady}}}, true, nil
}
func (r *bodyOnlyRepository) GetEmailConversation(context.Context, string, string) (app.EmailConversation, bool, error) {
	return app.EmailConversation{ID: "c", Title: "Matter"}, true, nil
}
func (r *bodyOnlyRepository) ListEmailMails(context.Context, store.EmailQuery) (store.EmailMailPage, error) {
	return store.EmailMailPage{Items: []app.EmailMail{{ID: "m", RepresentationID: "r", Summary: &app.EmailSummary{ID: "s", Text: "SECRET_OLD_ATTACHMENT_SUMMARY", Current: true, PromptVersion: "email-management-v1"}}}}, nil
}
func TestAllAnalysisExcludesAttachmentsAndLegacyDerivedSummaries(t *testing.T) {
	s := &Service{repository: &bodyOnlyRepository{store.NewMemoryStore()}}
	input, _, _, err := s.buildAnalysis(t.Context(), app.EmailJob{OwnerID: "owner", TargetID: "c", Kind: app.EmailJobConversationSummary})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, e := range input.Evidence {
		text += e.Text
	}
	if !strings.Contains(text, "Body request") || strings.Contains(text, "SECRET_") {
		t.Fatal("body-only context leaked attachment-derived input")
	}
	found := false
	for _, gap := range input.MissingContext {
		if strings.HasPrefix(gap, "attachment_not_analyzed:") {
			found = true
		}
	}
	if !found {
		t.Fatal("attachment gap not declared")
	}
}
