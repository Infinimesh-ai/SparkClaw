package toolhub

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type scriptedPPTXVisualRepairRunner struct {
	page        pptxVisualQAPageResult
	assessCalls int
	planCalls   int
}

func (runner *scriptedPPTXVisualRepairRunner) Assess(_ context.Context, request pptxVisualQARequest) (pptxVisualQAResult, error) {
	raw, err := os.ReadFile(request.CandidatePath)
	if err != nil {
		return pptxVisualQAResult{}, err
	}
	candidateSHA := pptxBytesSHA256(raw)
	runner.assessCalls++
	page := runner.page
	page.Diagnostics.CandidateSHA256 = candidateSHA
	page.Raster.PNGSHA256 = candidateSHA
	if runner.assessCalls > 1 {
		page.Diagnostics.Facts = []pptxDiagnosticFact{}
		page.Assessment.FactReviews = []pptxVisualFactReview{}
		page.Assessment.SubjectiveIssues = []pptxVisualSubjectiveIssue{}
	}
	return pptxVisualQAResult{
		SchemaVersion: pptxRenderAnalysisSchema, Status: "completed", CandidateSHA256: candidateSHA,
		PDFSHA256: candidateSHA, SlideCount: 1, Pages: []pptxVisualQAPageResult{page},
	}, nil
}

func (runner *scriptedPPTXVisualRepairRunner) PlanRepair(_ context.Context, request pptxVisualRepairRequest) (pptxVisualRepairPlan, modelrouter.ChatResult, error) {
	runner.planCalls++
	return pptxVisualRepairPlan{
		SchemaVersion: pptxVisualRepairPlanSchema, Attempt: request.Attempt, SlideIndex: request.Page.SlideIndex,
		ResolvesDiagnosticIDs: []string{"diag-text-1-2"}, ResolvesVisualIssueIDs: []string{},
		Operations: []pptxVisualRepairOperation{{Op: "set_geometry", ShapeRef: "slide:1:shape:2", RegionMilli: []int{100, 400, 700, 300}}},
	}, modelrouter.ChatResult{}, nil
}

func TestPPTXVisualReportDerivesOnlyQualifiedRuntimeIssues(t *testing.T) {
	cfg := config.Default().Adapters.PPTXVisualQA
	cfg.Phase = "qualified_blocking"
	cfg.RepairQualifiedClasses = []string{"text_clipped"}
	cfg.RepairQualifiedOperations = []string{"set_geometry"}
	cfg.BlockingQualifiedClasses = []string{"text_clipped"}
	result := pptxVisualQAResult{
		SchemaVersion: pptxRenderAnalysisSchema, Status: "completed", CandidateSHA256: strings64("a"), PDFSHA256: strings64("b"), SlideCount: 1,
		Pages: []pptxVisualQAPageResult{{
			SlideIndex: 1,
			Diagnostics: pptxDiagnosticFacts{Facts: []pptxDiagnosticFact{
				{DiagnosticID: "diag-clip", Kind: "text_clipping", Status: "confirmed", ShapeRefs: []string{"slide:1:shape:2"}},
				{DiagnosticID: "diag-overlap", Kind: "geometry_overlap", Status: "observed", ShapeRefs: []string{"slide:1:shape:2", "slide:1:shape:3"}},
			}},
			Assessment: pptxVisualAssessment{
				FactReviews: []pptxVisualFactReview{
					{DiagnosticID: "diag-clip", SemanticEffect: "required_content_lost", ConfidenceMilli: 980},
					{DiagnosticID: "diag-overlap", SemanticEffect: "harmful_obstruction", ConfidenceMilli: 900},
				},
				SubjectiveIssues: []pptxVisualSubjectiveIssue{{VisualIssueID: "visual-1", Type: "weak_hierarchy", ConfidenceMilli: 800, RegionMilli: []int{0, 0, 1000, 1000}}},
			},
		}},
	}
	report := buildPPTXVisualReport(result, cfg)
	if len(report.Issues) != 2 || report.Issues[0].Class != "text_clipped" || !report.Issues[0].RepairQualified || !report.Issues[0].BlockingQualified || report.Issues[1].Class != "weak_hierarchy" || report.Issues[1].RepairQualified || report.Issues[1].BlockingQualified {
		t.Fatalf("unexpected typed PPTX visual issues: %#v", report.Issues)
	}
	decision := applyPPTXVisualPolicy(report, cfg)
	if len(decision.Repairable) != 1 || len(decision.Blocking) != 1 || len(decision.Warnings) != 2 || decision.Failure != "" {
		t.Fatalf("unexpected qualified blocking decision: %#v", decision)
	}
	cfg.Phase = "shadow"
	decision = applyPPTXVisualPolicy(report, cfg)
	if len(decision.Repairable)+len(decision.Blocking)+len(decision.Warnings) != 0 {
		t.Fatalf("shadow policy changed production behavior: %#v", decision)
	}
}

func TestPPTXVisualPolicyFailsClosedOnlyWhenQualifiedDiagnosticIsRequired(t *testing.T) {
	report := PPTXVisualReport{
		SchemaVersion: pptxVisualReportSchema, Status: "completed",
		Unavailable: []PPTXVisualUnavailableProof{{SlideIndex: 2, Class: "text_clipped", DiagnosticID: "diag-2"}},
	}
	cfg := config.Default().Adapters.PPTXVisualQA
	cfg.Phase = "warning"
	cfg.RepairQualifiedClasses = []string{"text_clipped"}
	cfg.RepairQualifiedOperations = []string{"set_geometry"}
	cfg.BlockingQualifiedClasses = []string{"text_clipped"}
	if decision := applyPPTXVisualPolicy(report, cfg); decision.Failure != "" {
		t.Fatalf("warning phase failed closed on unavailable evidence: %#v", decision)
	}
	cfg.Phase = "qualified_blocking"
	if decision := applyPPTXVisualPolicy(report, cfg); decision.Failure != app.ToolErrorPPTXRenderDiagnosticUnavailable {
		t.Fatalf("qualified blocking phase did not fail closed: %#v", decision)
	}
	cfg.BlockingQualifiedClasses = []string{}
	if decision := applyPPTXVisualPolicy(report, cfg); decision.Failure != "" {
		t.Fatalf("unqualified diagnostic class affected blocking: %#v", decision)
	}
}

func TestPPTXVisualWarningSummaryAppearsOnlyOutsideShadow(t *testing.T) {
	report := PPTXVisualReport{Issues: []PPTXVisualRuntimeIssue{
		{SlideIndex: 4, Class: "weak_hierarchy"},
		{SlideIndex: 2, Class: "text_clipped"},
		{SlideIndex: 2, Class: "text_clipped"},
	}}
	cfg := config.Default().Adapters.PPTXVisualQA
	cfg.Phase = "shadow"
	if summary := pptxVisualWarningSummary(report, cfg); summary != "" {
		t.Fatalf("shadow phase exposed a warning summary: %q", summary)
	}
	cfg.Phase = "warning"
	summary := pptxVisualWarningSummary(report, cfg)
	if summary != "Final-render review warning on slide(s) 2,4: text_clipped, weak_hierarchy." {
		t.Fatalf("unexpected PPTX warning summary: %q", summary)
	}
}

func TestPPTXVisualControllerAppliesOneQualifiedRepairAndRechecksPixels(t *testing.T) {
	root := t.TempDir()
	writeSingleSlidePptxFixture(t, root, "candidate.pptx")
	candidatePath := filepath.Join(root, "candidate.pptx")
	pdfPath := filepath.Join(root, "candidate.pdf")
	writePPTXVisualQAPDFFixture(t, pdfPath, 960, 720)
	candidateSHA, err := pptxVisualFileSHA256(t.Context(), candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	analysisDir := t.TempDir()
	service := newPPTXVisualQAService(testPPTXVisualQAConfig("http://127.0.0.1"), nil)
	analysis, err := service.analyzeRender(t.Context(), pptxVisualQARequest{
		CandidatePath: candidatePath, Operation: "update_slide", SlideIndexes: []int{1}, ChangedShapeIndexes: map[int][]int{1: {2}},
	}, candidateSHA, pdfPath, analysisDir)
	if err != nil {
		t.Fatal(err)
	}
	page := analysis.Pages[0]
	runner := &scriptedPPTXVisualRepairRunner{page: pptxVisualQAPageResult{
		SlideIndex: 1, Raster: page.Raster, Structure: page.Structure, Targets: page.Targets,
		Diagnostics: pptxDiagnosticFacts{
			SchemaVersion: pptxDiagnosticFactsSchema, CandidateSHA256: candidateSHA, SlideIndex: 1, CoordinateSpace: "region_milli",
			Facts: []pptxDiagnosticFact{{DiagnosticID: "diag-text-1-2", Kind: "text_clipping", Status: "confirmed", ShapeRefs: []string{"slide:1:shape:2"}}},
		},
		Assessment: pptxVisualAssessment{
			SchemaVersion: pptxVisualAssessmentSchema, SlideIndex: 1,
			FactReviews:      []pptxVisualFactReview{{DiagnosticID: "diag-text-1-2", SemanticEffect: "required_content_lost", ConfidenceMilli: 990}},
			SubjectiveIssues: []pptxVisualSubjectiveIssue{},
		},
	}}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Adapters.PPTXVisualQA.Phase = "qualified_blocking"
	cfg.Adapters.PPTXVisualQA.RepairQualifiedClasses = []string{"text_clipped"}
	cfg.Adapters.PPTXVisualQA.RepairQualifiedOperations = []string{"set_geometry"}
	cfg.Adapters.PPTXVisualQA.BlockingQualifiedClasses = []string{"text_clipped"}
	cfg.Adapters.PPTXVisualQA.MaxRepairAttempts = 2
	hub := New(cfg, store.NewMemoryStore())
	hub.pptxVisualQA = runner
	prepared, err := hub.preparePPTXVisualCandidate(t.Context(), candidatePath, "update_slide", root, "session", "run", []int{1}, map[int][]int{1: {2}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.CandidatePath == candidatePath || len(prepared.Attempts) != 2 || runner.assessCalls != 2 || runner.planCalls != 1 || len(prepared.Report.Issues) != 0 {
		t.Fatalf("qualified repair did not converge: path=%q attempts=%#v assess=%d plan=%d report=%#v", prepared.CandidatePath, prepared.Attempts, runner.assessCalls, runner.planCalls, prepared.Report)
	}
	before, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(prepared.CandidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Equal(before, after) {
		t.Fatal("qualified visual repair did not change the candidate bytes")
	}
}

func TestClassifyPPTXVisualRepairAuthorityIsConservative(t *testing.T) {
	for _, test := range []struct {
		request string
		want    string
	}{
		{request: "Replace slide 2 title with Q3 results", want: "exact"},
		{request: "完善第二页 PPT", want: "outcome"},
		{request: "Improve slide 2 but keep the image and brand colors", want: "mixed"},
		{request: "修改第二页", want: "exact"},
	} {
		if got := classifyPPTXVisualRepairAuthority(test.request); got != test.want {
			t.Fatalf("classifyPPTXVisualRepairAuthority(%q) = %q, want %q", test.request, got, test.want)
		}
	}
}

func TestPPTXVisualPolicyQualificationCorpusEnforcesClassAndOperationCeilings(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "pptx_visual_policy_qualification_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		SchemaVersion string `json:"schema_version"`
		Cases         []struct {
			ID                  string   `json:"id"`
			EvidenceSource      string   `json:"evidence_source"`
			DiagnosticKind      string   `json:"diagnostic_kind"`
			SemanticEffect      string   `json:"semantic_effect"`
			SubjectiveType      string   `json:"subjective_type"`
			ExpectedClass       string   `json:"expected_class"`
			BlockingCeiling     bool     `json:"blocking_ceiling"`
			QualifiedOperations []string `json:"qualified_operations"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.SchemaVersion != "sparkclaw.pptx_visual_policy_qualification.v1" || len(corpus.Cases) != 13 {
		t.Fatalf("invalid PPTX visual qualification corpus: %#v", corpus)
	}
	seenClasses := []string{}
	blockingClasses := []string{}
	for _, testCase := range corpus.Cases {
		class := testCase.SubjectiveType
		if testCase.EvidenceSource == "objective" {
			class = pptxRuntimeClassForFactReview(testCase.DiagnosticKind, testCase.SemanticEffect)
		}
		if class != testCase.ExpectedClass || class == "" || slices.Contains(seenClasses, class) || len(testCase.QualifiedOperations) == 0 {
			t.Fatalf("invalid qualification case %q: class=%q case=%#v", testCase.ID, class, testCase)
		}
		seenClasses = append(seenClasses, class)
		if testCase.BlockingCeiling {
			blockingClasses = append(blockingClasses, class)
		}
		issue := PPTXVisualRuntimeIssue{Class: class}
		for _, operation := range testCase.QualifiedOperations {
			if !pptxVisualOperationAllowedForIssues(operation, []PPTXVisualRuntimeIssue{issue}, "outcome") {
				t.Fatalf("qualification case %q lists unsupported operation %q", testCase.ID, operation)
			}
		}
	}
	slices.Sort(seenClasses)
	slices.Sort(blockingClasses)
	if !slices.Equal(seenClasses, []string{"broken_layout", "content_obscured", "element_off_canvas", "inconsistent_style", "low_contrast", "misaligned", "missing_glyph", "overcrowded", "poor_whitespace", "text_clipped", "text_too_small", "unclear_focus", "weak_hierarchy"}) {
		t.Fatalf("qualification corpus class set drifted: %#v", seenClasses)
	}
	if !slices.Equal(blockingClasses, []string{"content_obscured", "element_off_canvas", "missing_glyph", "text_clipped"}) {
		t.Fatalf("qualification corpus exceeded the blocking ceiling: %#v", blockingClasses)
	}
}

func strings64(value string) string {
	return value + value + value + value + value + value + value + value +
		value + value + value + value + value + value + value + value +
		value + value + value + value + value + value + value + value +
		value + value + value + value + value + value + value + value +
		value + value + value + value + value + value + value + value +
		value + value + value + value + value + value + value + value +
		value + value + value + value + value + value + value + value +
		value + value + value + value + value + value + value + value
}

// scriptedMultiSlideVisualRunner serves per-slide pages for exactly the slides
// each Assess request selects, clears the repaired slide's findings after the
// first pass, and records every selection so the controller's re-review scope
// can be asserted.
type scriptedMultiSlideVisualRunner struct {
	pages       map[int]pptxVisualQAPageResult
	selections  [][]int
	assessCalls int
}

func (runner *scriptedMultiSlideVisualRunner) Assess(_ context.Context, request pptxVisualQARequest) (pptxVisualQAResult, error) {
	raw, err := os.ReadFile(request.CandidatePath)
	if err != nil {
		return pptxVisualQAResult{}, err
	}
	candidateSHA := pptxBytesSHA256(raw)
	runner.assessCalls++
	runner.selections = append(runner.selections, slices.Clone(request.SlideIndexes))
	pages := make([]pptxVisualQAPageResult, 0, len(request.SlideIndexes))
	for _, slideIndex := range request.SlideIndexes {
		page := runner.pages[slideIndex]
		page.Diagnostics.CandidateSHA256 = candidateSHA
		page.Raster.PNGSHA256 = candidateSHA
		if runner.assessCalls > 1 && slideIndex == 1 {
			page.Diagnostics.Facts = []pptxDiagnosticFact{}
			page.Assessment.FactReviews = []pptxVisualFactReview{}
		}
		pages = append(pages, page)
	}
	return pptxVisualQAResult{
		SchemaVersion: pptxRenderAnalysisSchema, Status: "completed", CandidateSHA256: candidateSHA,
		PDFSHA256: candidateSHA, SlideCount: len(runner.pages), Pages: pages,
	}, nil
}

func (runner *scriptedMultiSlideVisualRunner) PlanRepair(_ context.Context, request pptxVisualRepairRequest) (pptxVisualRepairPlan, modelrouter.ChatResult, error) {
	return pptxVisualRepairPlan{
		SchemaVersion: pptxVisualRepairPlanSchema, Attempt: request.Attempt, SlideIndex: request.Page.SlideIndex,
		ResolvesDiagnosticIDs: []string{"diag-text-1-2"}, ResolvesVisualIssueIDs: []string{},
		Operations: []pptxVisualRepairOperation{{Op: "set_geometry", ShapeRef: "slide:1:shape:2", RegionMilli: []int{100, 400, 700, 300}}},
	}, modelrouter.ChatResult{}, nil
}

// unchangedSecondSlidePage derives a slide-2 page from a slide-1 page whose
// shapes were all left untouched by the current run, so any issue on it is
// blocking-qualified but never actionable under exact authority.
func unchangedSecondSlidePage(t *testing.T, page pptxVisualQAPageResult) pptxVisualQAPageResult {
	t.Helper()
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.ReplaceAll(strings.ReplaceAll(string(raw), "slide:1:", "slide:2:"), "diag-text-1-", "diag-text-2-"))
	var second pptxVisualQAPageResult
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatal(err)
	}
	second.SlideIndex, second.Structure.SlideIndex, second.Diagnostics.SlideIndex, second.Assessment.SlideIndex = 2, 2, 2, 2
	for _, shape := range second.Structure.Shapes {
		shape["changed"] = false
	}
	return second
}

func TestPPTXVisualControllerKeepsBlockingIssuesOnUnrepairedSlidesInScope(t *testing.T) {
	root := t.TempDir()
	writeSingleSlidePptxFixture(t, root, "candidate.pptx")
	candidatePath := filepath.Join(root, "candidate.pptx")
	pdfPath := filepath.Join(root, "candidate.pdf")
	writePPTXVisualQAPDFFixture(t, pdfPath, 960, 720)
	candidateSHA, err := pptxVisualFileSHA256(t.Context(), candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	service := newPPTXVisualQAService(testPPTXVisualQAConfig("http://127.0.0.1"), nil)
	analysis, err := service.analyzeRender(t.Context(), pptxVisualQARequest{
		CandidatePath: candidatePath, Operation: "update_slide", SlideIndexes: []int{1}, ChangedShapeIndexes: map[int][]int{1: {2}},
	}, candidateSHA, pdfPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	analyzed := analysis.Pages[0]
	first := pptxVisualQAPageResult{
		SlideIndex: 1, Raster: analyzed.Raster, Structure: analyzed.Structure, Targets: analyzed.Targets,
		Diagnostics: pptxDiagnosticFacts{
			SchemaVersion: pptxDiagnosticFactsSchema, CandidateSHA256: candidateSHA, SlideIndex: 1, CoordinateSpace: "region_milli",
			Facts: []pptxDiagnosticFact{{DiagnosticID: "diag-text-1-2", Kind: "text_clipping", Status: "confirmed", ShapeRefs: []string{"slide:1:shape:2"}}},
		},
		Assessment: pptxVisualAssessment{
			SchemaVersion: pptxVisualAssessmentSchema, SlideIndex: 1,
			FactReviews:      []pptxVisualFactReview{{DiagnosticID: "diag-text-1-2", SemanticEffect: "required_content_lost", ConfidenceMilli: 990}},
			SubjectiveIssues: []pptxVisualSubjectiveIssue{},
		},
	}
	runner := &scriptedMultiSlideVisualRunner{pages: map[int]pptxVisualQAPageResult{1: first, 2: unchangedSecondSlidePage(t, first)}}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Adapters.PPTXVisualQA.Phase = "qualified_blocking"
	cfg.Adapters.PPTXVisualQA.RepairQualifiedClasses = []string{"text_clipped"}
	cfg.Adapters.PPTXVisualQA.RepairQualifiedOperations = []string{"set_geometry"}
	cfg.Adapters.PPTXVisualQA.BlockingQualifiedClasses = []string{"text_clipped"}
	cfg.Adapters.PPTXVisualQA.MaxRepairAttempts = 2
	hub := New(cfg, store.NewMemoryStore())
	hub.pptxVisualQA = runner
	_, err = hub.preparePPTXVisualCandidate(t.Context(), candidatePath, "update_slide", root, "session", "run", []int{1, 2}, map[int][]int{1: {2}}, nil)
	if app.ToolErrorCodeFrom(err) != app.ToolErrorPPTXRenderVisualBlocked {
		t.Fatalf("blocking issue on the unrepaired slide was sealed away: err=%v selections=%v", err, runner.selections)
	}
	if runner.assessCalls != 2 || !slices.Equal(runner.selections[1], []int{1, 2}) {
		t.Fatalf("re-review narrowed the authorized selection: assess=%d selections=%v", runner.assessCalls, runner.selections)
	}
}
