package emailmanagement

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type presentationSources struct{ *store.MemoryStore }

func (r *presentationSources) GetEmailMail(context.Context, string, string) (app.EmailMail, bool, error) {
	return app.EmailMail{ID: "m", Subject: "Original subject code 123456", Verification: &app.EmailVerification{Code: "123456"}, RepresentationID: "r", Summary: &app.EmailSummary{Text: "OLD_ATTACHMENT_BASED_SUMMARY"}, Classification: &app.EmailClassification{EffectiveEntry: "interaction", ReasonCode: "confirmation_requested"}}, true, nil
}
func (r *presentationSources) GetEmailRepresentation(context.Context, string, string) (app.EmailRepresentation, bool, error) {
	return app.EmailRepresentation{BodyText: "Please confirm the terms in the attachment. Code: 123456.", Attachments: []app.EmailAttachment{{Name: "PRIVATE_ATTACHMENT_NAME", Text: "PRIVATE_ATTACHMENT_TEXT"}}}, true, nil
}
func TestPresentationUsesBodyOnlyAndExplicitLanguage(t *testing.T) {
	s := &Service{repository: &presentationSources{store.NewMemoryStore()}}
	input, err := s.presentationInput(t.Context(), "owner", app.EmailLocalizedPresentation{TargetKind: "mail", TargetID: "m", Language: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(input)
	for _, forbidden := range []string{"PRIVATE_ATTACHMENT_NAME", "PRIVATE_ATTACHMENT_TEXT", "OLD_ATTACHMENT_BASED_SUMMARY", "123456"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("excluded material leaked: %s", forbidden)
		}
	}
	if input.OutputLanguage != "zh" || !strings.Contains(input.Messages[0].Body, "confirm") || !strings.Contains(string(raw), "attachment_contents_not_analyzed") {
		t.Fatalf("missing language/body coverage: %s", raw)
	}
}
func TestPresentationValidatesConcernIdentity(t *testing.T) {
	input := PresentationInput{OutputLanguage: "zh", Concerns: []PresentationConcern{{ID: "c1", Text: "Duplicate"}, {ID: "c2", Text: "Conflict"}}}
	good := PresentationOutput{Title: "标题", Summary: "摘要", Concerns: []PresentationConcern{{ID: "c2", Text: "冲突"}, {ID: "c1", Text: "疑似重复"}}}
	if err := validatePresentation(input, good); err != nil {
		t.Fatal(err)
	}
	for _, concerns := range [][]PresentationConcern{{{ID: "c1", Text: "重复"}}, {{ID: "c1", Text: "重复"}, {ID: "c1", Text: "重复"}}, {{ID: "c1", Text: "重复"}, {ID: "other", Text: "其他"}}} {
		bad := good
		bad.Concerns = concerns
		if validatePresentation(input, bad) == nil {
			t.Fatal("unbound or omitted concern accepted")
		}
	}
}

func TestPresentationAdapterRejectsMockAndUntrustedOutput(t *testing.T) {
	input := PresentationInput{OutputLanguage: "zh", Concerns: []PresentationConcern{{ID: "concern", Text: "suspected duplicate"}}}
	valid := PresentationOutput{Title: "标题", Summary: "摘要", Explanation: "分类依据", Concerns: []PresentationConcern{{ID: "concern", Text: "疑似重复"}}}
	raw, _ := json.Marshal(valid)
	for _, tc := range []struct {
		content string
		mock    bool
	}{{string(raw), true}, {"{", false}, {string(raw) + " {}", false}, {strings.TrimSuffix(string(raw), "}") + `,"action":"send"}`, false}} {
		client := &modelFixture{result: modelrouter.ChatResult{Content: tc.content, Mock: tc.mock, Model: "fixture"}}
		if _, err := NewModelAnalyzer(client).Present(t.Context(), input); err == nil {
			t.Fatal("untrusted presentation output admitted")
		}
	}
	client := &modelFixture{result: modelrouter.ChatResult{Content: string(raw), Model: "fixture"}}
	if _, err := NewModelAnalyzer(client).Present(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	input.Messages = []PresentationMessage{{Body: strings.Repeat("x", maxAnalysisInputBytes)}}
	if _, err := NewModelAnalyzer(client).Present(t.Context(), input); err == nil || client.calls != 1 {
		t.Fatal("unbounded input reached model")
	}
}

type explicitReplySources struct {
	*store.MemoryStore
	calls []string
}

func (r *explicitReplySources) GetEmailMail(_ context.Context, owner, id string) (app.EmailMail, bool, error) {
	r.calls = append(r.calls, id)
	if owner != "owner" {
		return app.EmailMail{}, false, nil
	}
	switch id {
	case "reply":
		return app.EmailMail{ID: id, ReplyMailID: "original", RepresentationID: "reply-rep"}, true, nil
	case "original":
		return app.EmailMail{ID: id, ReplyMailID: "older", RepresentationID: "original-rep", Classification: &app.EmailClassification{EffectiveEntry: "notification"}}, true, nil
	}
	return app.EmailMail{}, false, nil
}
func (r *explicitReplySources) GetEmailRepresentation(_ context.Context, _, id string) (app.EmailRepresentation, bool, error) {
	return app.EmailRepresentation{ID: id, BodyText: "body:" + id, Attachments: []app.EmailAttachment{{Text: "DO_NOT_ANALYZE_ATTACHMENT"}}}, true, nil
}
func TestExplicitReplyContextRemainsBoundedBodyOnly(t *testing.T) {
	repo := &explicitReplySources{MemoryStore: store.NewMemoryStore()}
	s := &Service{repository: repo}
	input, err := s.presentationInput(t.Context(), "owner", app.EmailLocalizedPresentation{TargetKind: "mail", TargetID: "reply", Language: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Messages) != 2 || !input.Messages[1].ContextOnly || input.Messages[1].Body != "body:original-rep" {
		t.Fatal("explicit reply original missing from presentation")
	}
	for _, id := range repo.calls {
		if id == "older" {
			t.Fatal("recursive reply context crawl")
		}
	}
	analysis := AnalysisInput{Kind: app.EmailJobMessageSummary}
	refs := []string{}
	if err := s.addExplicitReplyEvidence(t.Context(), "owner", app.EmailMail{ID: "reply", ReplyMailID: "original"}, &analysis, &refs); err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != "mail:original" {
		t.Fatal("reply dependency missing")
	}
	raw, _ := json.Marshal(analysis)
	if !strings.Contains(string(raw), "body:original-rep") || strings.Contains(string(raw), "DO_NOT_ANALYZE_ATTACHMENT") {
		t.Fatal("reply context body/attachment boundary violated")
	}
}

func TestPresentationRejectsEnglishGeneratedFieldsForChinese(t *testing.T) {
	input := PresentationInput{OutputLanguage: "zh"}
	good := PresentationOutput{Title: "CloudDesk发货通知", Summary: "包裹已发货。", Explanation: "根据正文判断为通知。", Purpose: "发货状态提醒"}
	if err := validatePresentation(input, good); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"title", "summary", "explanation", "purpose", "requested_response"} {
		bad := good
		switch field {
		case "title":
			bad.Title = "CloudDesk delivery"
		case "summary":
			bad.Summary = "Package shipped"
		case "explanation":
			bad.Explanation = "Notification"
		case "purpose":
			bad.Purpose = "Shipping"
		case "requested_response":
			bad.RequestedResponse = "Please confirm"
		}
		if err := validatePresentation(input, bad); err == nil {
			t.Fatalf("English %s accepted in Chinese projection", field)
		}
	}
}

func TestPresentationChineseEnumGlossCleanup(t *testing.T) {
	original := PresentationOutput{Explanation: "交互（interaction）、通知(notification)、不确定（unknown）；CloudDesk(Delivery) 保留。", ServiceLabel: "CloudDesk(interaction)"}
	got := normalizePresentation(PresentationInput{OutputLanguage: "zh"}, original)
	if got.Explanation != "交互、通知、不确定；CloudDesk(Delivery) 保留。" || got.ServiceLabel != original.ServiceLabel {
		t.Fatalf("cleanup changed unrelated content: %+v", got)
	}
	if got := normalizePresentation(PresentationInput{OutputLanguage: "en"}, original); got.Explanation != original.Explanation {
		t.Fatal("English presentation changed")
	}
}
