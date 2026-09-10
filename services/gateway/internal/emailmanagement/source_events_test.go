package emailmanagement

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type sourceEventRepository struct {
	Repository
	candidates int
}

func (r *sourceEventRepository) GetEmailMail(context.Context, string, string) (app.EmailMail, bool, error) {
	return app.EmailMail{ID: "mail", Subject: "Confirm delivery", RepresentationID: "r", ConversationID: "event", ReplyReferences: []string{"parent"}}, true, nil
}
func (r *sourceEventRepository) GetEmailRepresentation(context.Context, string, string) (app.EmailRepresentation, bool, error) {
	return app.EmailRepresentation{ID: "r", Subject: "Confirm delivery", BodyText: strings.Repeat("Please confirm delivery. ", 200), State: app.EmailParseReady}, true, nil
}
func (r *sourceEventRepository) FindEmailCandidates(context.Context, store.EmailCandidateQuery) (store.EmailCandidateSet, error) {
	r.candidates++
	return store.EmailCandidateSet{Conversations: []app.EmailConversation{{ID: "event", Title: "GENERATED_TITLE", Summary: &app.EmailSummary{Text: "GENERATED_SUMMARY", Current: true}}}}, nil
}

func TestSourceClassificationCannotRetrieveOrMutateContext(t *testing.T) {
	repo := &sourceEventRepository{}
	s := &Service{repository: repo}
	input, refs, epoch, err := s.buildAnalysis(t.Context(), app.EmailJob{OwnerID: "owner", TargetID: "mail", Kind: app.EmailJobClassification})
	if err != nil {
		t.Fatal(err)
	}
	if repo.candidates != 0 || len(refs) != 1 || refs[0] != "source:mail" || epoch != 0 || input.CurrentConversationID != "" || len(input.Candidates) != 0 {
		t.Fatalf("classification coupled to context: %+v %v", input, refs)
	}
	for _, e := range input.Evidence {
		if strings.HasSuffix(e.Ref, ":body") && len(e.Text) > 1200 {
			t.Fatal("body budget exceeded")
		}
	}
}
func TestSourceEventValidationRequiresBothSidesOfAppend(t *testing.T) {
	input := AnalysisInput{PolicyVersion: analysisPromptVersion, Kind: app.EmailJobAssignment, Evidence: []Evidence{{Ref: "representation:current:body", Text: "Shipping order A"}, {Ref: "representation:related:body", Text: "Related source"}}, Candidates: []AnalysisCandidate{{ID: "event", Evidence: []Evidence{{Ref: "representation:member:body", Text: "Order A confirmed"}}}}}
	valid := AnalysisOutput{EventScope: "single_event", Action: "append", TargetConversationID: "event", EvidenceRefs: []string{"representation:current:body", "representation:member:body"}, Reason: "same_event"}
	if err := validateAnalysis(input, valid); err != nil {
		t.Fatal(err)
	}
	for _, refs := range [][]string{{"representation:current:body"}, {"representation:member:body"}, {"representation:related:body", "representation:member:body"}, {"summary:invented"}} {
		out := valid
		out.EvidenceRefs = refs
		if validateAnalysis(input, out) == nil {
			t.Fatalf("admitted unsupported append: %v", refs)
		}
	}
	valid.Summary = "generated summary"
	if validateAnalysis(input, valid) == nil {
		t.Fatal("summary admitted")
	}
}
func TestSourceEventSchemaHasNoCompoundAnalysisFields(t *testing.T) {
	for _, kind := range []string{app.EmailJobClassification, app.EmailJobAssignment} {
		raw, _ := json.Marshal(eventAnalysisSchema(kind))
		for _, forbidden := range []string{"summary", "verification_code", "requested_response", "purpose", "service_label"} {
			if strings.Contains(string(raw), `"`+forbidden+`"`) {
				t.Fatalf("compound field %s", forbidden)
			}
		}
	}
}

func (r *sourceEventRepository) ListEmailMails(context.Context, store.EmailQuery) (store.EmailMailPage, error) {
	return store.EmailMailPage{Items: []app.EmailMail{{ID: "member", RepresentationID: "r"}}}, nil
}
func TestEventCandidatesUseOriginalsWithoutContextPublication(t *testing.T) {
	repo := &sourceEventRepository{}
	s := &Service{repository: repo}
	input, refs, _, err := s.buildAnalysis(t.Context(), app.EmailJob{OwnerID: "owner", TargetID: "mail", Kind: app.EmailJobAssignment})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(input)
	if strings.Contains(string(raw), "GENERATED_") || len(input.Candidates) != 1 || len(input.Candidates[0].Evidence) == 0 {
		t.Fatalf("derived candidate input: %s", raw)
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref, "source:") && !strings.HasPrefix(ref, "mapping:") && !strings.HasPrefix(ref, "members:") {
			t.Fatalf("unstable dependency: %s", ref)
		}
	}
	total := 0
	for _, e := range input.Evidence {
		total += len(e.Text)
	}
	for _, c := range input.Candidates {
		for _, e := range c.Evidence {
			total += len(e.Text)
		}
	}
	if total > 8000 {
		t.Fatalf("source budget %d", total)
	}
}

type stagedSourceAnalyzer struct {
	stages []string
	calls  int
}

func (a *stagedSourceAnalyzer) Analyze(_ context.Context, input AnalysisInput) (AnalysisOutput, error) {
	a.calls++
	out := AnalysisOutput{ModelVersion: "explicit-staged-fixture", Action: "none", Concern: "none", Reason: "insufficient_context"}
	if input.Kind == app.EmailJobClassification {
		a.stages = append(a.stages, input.ClassificationStage)
		if input.ClassificationStage == "subject" {
			out.Category = "unknown"
			out.Uncertainty = true
			return out, nil
		}
		out.Category = "interaction"
		out.Reason = "concrete_request"
	} else {
		out.EventScope = "single_event"
		out.Action = "new"
		out.Title = "Approve equipment purchase"
		out.Reason = "distinct_event"
	}
	for _, e := range input.Evidence {
		if strings.TrimSpace(e.Text) != "" {
			out.EvidenceRefs = append(out.EvidenceRefs, e.Ref)
		}
	}
	return out, nil
}
func TestSourceStagesAndOneHundredRefreshesConverge(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, _ := newFixtureService(t, repo)
	model := &stagedSourceAnalyzer{}
	s.analyzer = model
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.discover(t.Context(), app.EmailJob{OwnerID: "email-owner", MailboxID: box.ID, BindingGeneration: box.BindingGeneration}); err != nil {
		t.Fatal(err)
	}
	kinds := []string{app.EmailJobCapture, app.EmailJobParse, app.EmailJobClassification, app.EmailJobAssignment}
	for range 20 {
		if err = s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err = s.workOne(t.Context(), kinds); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("mail page %v", err)
	}
	mail := page.Items[0]
	if mail.ConversationID == "" || mail.Classification == nil || mail.Summary != nil {
		t.Fatalf("source pipeline incomplete: %+v", mail)
	}
	for relative, expectedHash := range map[string]string{mail.Classification.InputPath: mail.Classification.InputSHA256, mail.Classification.OutputPath: mail.Classification.OutputSHA256} {
		raw, err := os.ReadFile(filepath.Join(s.opts.WorkspaceRoot, relative))
		if err != nil || sourceHash(raw) != expectedHash {
			t.Fatalf("classification artifact missing/mismatched: %s %v", relative, err)
		}
	}
	if mail.Classification.Stage != "body" || mail.Classification.ModelVersion != "explicit-staged-fixture" || mail.Classification.PromptVersion != analysisPromptVersion {
		t.Fatal("classification audit identity missing")
	}
	if len(model.stages) != 2 || model.stages[0] != "subject" || model.stages[1] != "body" {
		t.Fatalf("unbounded or bypassed stages: %v", model.stages)
	}
	beforeCalls := model.calls
	before, _, err := repo.GetEmailAnalysisTarget(t.Context(), "email-owner", app.EmailJobClassification, mail.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if err = s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err = s.workOne(t.Context(), []string{app.EmailJobClassification, app.EmailJobAssignment}); err != nil {
			t.Fatal(err)
		}
	}
	after, _, err := repo.GetEmailAnalysisTarget(t.Context(), "email-owner", app.EmailJobClassification, mail.ID)
	if err != nil || after.Generation != before.Generation || after.InputFingerprint != before.InputFingerprint || len(model.stages) != 2 || model.calls != beforeCalls {
		t.Fatalf("fixed source did not converge: before=%+v after=%+v calls=%v error=%v", before, after, model.stages, err)
	}
}

func TestSourceVerificationMaskDoesNotNeedExtraction(t *testing.T) {
	input := AnalysisInput{PolicyVersion: analysisPromptVersion, Kind: app.EmailJobAssignment, Evidence: []Evidence{{Ref: "representation:r:body", Text: "Your sign-in code is A7B3C9. The previous verification code was 173620."}}}
	for _, token := range []string{"A7B3C9", "173620"} {
		out := AnalysisOutput{EventScope: "single_event", Action: "new", Title: "Receive verification code " + token, EvidenceRefs: []string{"representation:r:body"}}
		if validateAnalysis(input, out) == nil {
			t.Fatal("unmasked title accepted")
		}
		masked := redactVerificationTokens(input, out.Title)
		if strings.Contains(masked, token) {
			t.Fatal("title token not masked")
		}
	}
	input.Kind = app.EmailJobClassification
	evidence := classificationEvidence(input, AnalysisOutput{EvidenceRefs: []string{"representation:r:body"}})
	if len(evidence) != 1 || strings.Contains(evidence[0].Text, "A7B3C9") || strings.Contains(evidence[0].Text, "173620") {
		t.Fatalf("classification leaked code without extraction: %+v", evidence)
	}
}

func TestSourceEventNamingFailureDoesNotDiscardValidNewEvent(t *testing.T) {
	input := AnalysisInput{PolicyVersion: analysisPromptVersion, Kind: app.EmailJobAssignment, Evidence: []Evidence{{Ref: "representation:r:body", Text: "Please organize the annual compliance workshop."}}}
	out := AnalysisOutput{EventScope: "single_event", Action: "new", EvidenceRefs: []string{"representation:r:body"}, Reason: "distinct_event"}
	if err := validateAnalysis(input, out); err != nil {
		t.Fatalf("missing name discarded event: %v", err)
	}
	out.EvidenceRefs = nil
	if validateAnalysis(input, out) == nil {
		t.Fatal("naming fallback admitted unsupported event")
	}
}

func TestVerificationAppendRequiresSharedOccurrenceIdentity(t *testing.T) {
	input := AnalysisInput{PolicyVersion: analysisPromptVersion, Kind: app.EmailJobAssignment, Evidence: []Evidence{{Ref: "representation:r:body", Text: "Your verification code is 592714."}}, Candidates: []AnalysisCandidate{{ID: "prior", Evidence: []Evidence{{Ref: "representation:p:body", Text: "Your verification code was 418239."}}}}}
	out := AnalysisOutput{EventScope: "single_event", Action: "append", TargetConversationID: "prior", EvidenceRefs: []string{"representation:r:body", "representation:p:body"}}
	if validateAnalysis(input, out) == nil {
		t.Fatal("separate code issuances merged without occurrence proof")
	}
	input.Evidence[0].Text += " New attempt: your requested sign-in."
	input.Candidates[0].Evidence[0].Text += " Old attempt: your requested sign-in."
	if sharedVerificationOccurrence(input, "prior") {
		t.Fatal("ordinary prose mistaken for occurrence ID")
	}
	input.Evidence[0].Text += " Challenge ID: request-512"
	input.Candidates[0].Evidence[0].Text += " Challenge ID: request-512"
	if err := validateAnalysis(input, out); err != nil {
		t.Fatalf("proved same occurrence rejected: %v", err)
	}
}
