package toolhub

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const pptxVisualReportSchema = "sparkclaw.pptx_visual_report.v1"

// PPTXVisualReport is the Runtime-owned, typed projection of render,
// diagnostic, and model evidence used by rollout policy and sealed manifests.
// It intentionally excludes slide text and page pixels.
type PPTXVisualReport struct {
	SchemaVersion   string                       `json:"schema_version"`
	Status          string                       `json:"status"`
	FailureCode     app.ToolErrorCode            `json:"failure_code,omitempty"`
	CandidateSHA256 string                       `json:"candidate_sha256,omitempty"`
	PDFSHA256       string                       `json:"pdf_sha256,omitempty"`
	SlideCount      int                          `json:"slide_count,omitempty"`
	DurationMS      int64                        `json:"duration_ms,omitempty"`
	Infrastructure  PPTXVisualInfrastructure     `json:"infrastructure"`
	Pages           []PPTXVisualReportPage       `json:"pages"`
	Issues          []PPTXVisualRuntimeIssue     `json:"issues"`
	Unavailable     []PPTXVisualUnavailableProof `json:"unavailable_evidence,omitempty"`
}

type PPTXVisualInfrastructure struct {
	Renderer    string `json:"renderer"`
	Diagnostics string `json:"diagnostics"`
	Model       string `json:"model"`
}

type PPTXVisualReportPage struct {
	SlideIndex       int                               `json:"slide_index"`
	PNGSHA256        string                            `json:"png_sha256"`
	PNGWidth         int                               `json:"png_width"`
	PNGHeight        int                               `json:"png_height"`
	UniformWhite     bool                              `json:"uniform_white"`
	Facts            []PPTXVisualReportFact            `json:"facts"`
	FactReviews      []PPTXVisualReportFactReview      `json:"fact_reviews"`
	SubjectiveIssues []PPTXVisualReportSubjectiveIssue `json:"subjective_issues"`
	ModelProfile     string                            `json:"model_profile,omitempty"`
	Model            string                            `json:"model,omitempty"`
}

type PPTXVisualReportFact struct {
	DiagnosticID string   `json:"diagnostic_id"`
	Kind         string   `json:"kind"`
	Status       string   `json:"status"`
	ShapeRefs    []string `json:"shape_refs"`
}

type PPTXVisualReportFactReview struct {
	DiagnosticID    string `json:"diagnostic_id"`
	SemanticEffect  string `json:"semantic_effect"`
	ConfidenceMilli int    `json:"confidence_milli"`
}

type PPTXVisualReportSubjectiveIssue struct {
	VisualIssueID   string   `json:"visual_issue_id"`
	Type            string   `json:"type"`
	ConfidenceMilli int      `json:"confidence_milli"`
	RegionMilli     []int    `json:"region_milli"`
	ShapeRefs       []string `json:"shape_refs"`
}

type PPTXVisualRuntimeIssue struct {
	SlideIndex        int      `json:"slide_index"`
	Class             string   `json:"class"`
	EvidenceSource    string   `json:"evidence_source"`
	EvidenceID        string   `json:"evidence_id"`
	EvidenceStatus    string   `json:"evidence_status,omitempty"`
	SemanticEffect    string   `json:"semantic_effect,omitempty"`
	ConfidenceMilli   int      `json:"confidence_milli"`
	RegionMilli       []int    `json:"region_milli,omitempty"`
	ShapeRefs         []string `json:"shape_refs"`
	RepairQualified   bool     `json:"repair_qualified"`
	BlockingQualified bool     `json:"blocking_qualified"`
}

type PPTXVisualUnavailableProof struct {
	SlideIndex   int      `json:"slide_index"`
	Class        string   `json:"class"`
	DiagnosticID string   `json:"diagnostic_id"`
	ShapeRefs    []string `json:"shape_refs"`
}

type pptxVisualPolicyDecision struct {
	Repairable []PPTXVisualRuntimeIssue
	Warnings   []PPTXVisualRuntimeIssue
	Blocking   []PPTXVisualRuntimeIssue
	Failure    app.ToolErrorCode
}

func buildPPTXVisualReport(result pptxVisualQAResult, cfg config.PPTXVisualQAAdapterConfig) PPTXVisualReport {
	report := PPTXVisualReport{
		SchemaVersion:   pptxVisualReportSchema,
		Status:          result.Status,
		CandidateSHA256: result.CandidateSHA256,
		PDFSHA256:       result.PDFSHA256,
		SlideCount:      result.SlideCount,
		DurationMS:      result.DurationMS,
		Infrastructure:  PPTXVisualInfrastructure{Renderer: "completed", Diagnostics: "completed", Model: "not_required"},
		Pages:           make([]PPTXVisualReportPage, 0, len(result.Pages)),
		Issues:          []PPTXVisualRuntimeIssue{},
	}
	for _, page := range result.Pages {
		report.Infrastructure.Model = "completed"
		projected := PPTXVisualReportPage{
			SlideIndex: page.SlideIndex, PNGSHA256: page.Raster.PNGSHA256,
			PNGWidth: page.Raster.Width, PNGHeight: page.Raster.Height, UniformWhite: page.Raster.UniformWhite,
			Facts:            make([]PPTXVisualReportFact, 0, len(page.Diagnostics.Facts)),
			FactReviews:      make([]PPTXVisualReportFactReview, 0, len(page.Assessment.FactReviews)),
			SubjectiveIssues: make([]PPTXVisualReportSubjectiveIssue, 0, len(page.Assessment.SubjectiveIssues)),
			ModelProfile:     page.Model.Profile, Model: page.Model.Model,
		}
		facts := make(map[string]pptxDiagnosticFact, len(page.Diagnostics.Facts))
		for _, fact := range page.Diagnostics.Facts {
			facts[fact.DiagnosticID] = fact
			projected.Facts = append(projected.Facts, PPTXVisualReportFact{
				DiagnosticID: fact.DiagnosticID, Kind: fact.Kind, Status: fact.Status, ShapeRefs: slices.Clone(fact.ShapeRefs),
			})
			if fact.Status == "unavailable" {
				if class := pptxRuntimeClassForDiagnostic(fact.Kind); class != "" {
					report.Unavailable = append(report.Unavailable, PPTXVisualUnavailableProof{
						SlideIndex: page.SlideIndex, Class: class, DiagnosticID: fact.DiagnosticID, ShapeRefs: slices.Clone(fact.ShapeRefs),
					})
				}
			}
		}
		for _, review := range page.Assessment.FactReviews {
			projected.FactReviews = append(projected.FactReviews, PPTXVisualReportFactReview{
				DiagnosticID: review.DiagnosticID, SemanticEffect: review.SemanticEffect, ConfidenceMilli: review.ConfidenceMilli,
			})
			fact, ok := facts[review.DiagnosticID]
			if !ok || fact.Status != "confirmed" {
				continue
			}
			class := pptxRuntimeClassForFactReview(fact.Kind, review.SemanticEffect)
			if class == "" {
				continue
			}
			report.Issues = append(report.Issues, qualifiedPPTXRuntimeIssue(cfg, PPTXVisualRuntimeIssue{
				SlideIndex: page.SlideIndex, Class: class, EvidenceSource: "objective", EvidenceID: fact.DiagnosticID,
				EvidenceStatus: fact.Status, SemanticEffect: review.SemanticEffect, ConfidenceMilli: review.ConfidenceMilli,
				ShapeRefs: slices.Clone(fact.ShapeRefs),
			}))
		}
		for _, issue := range page.Assessment.SubjectiveIssues {
			projected.SubjectiveIssues = append(projected.SubjectiveIssues, PPTXVisualReportSubjectiveIssue{
				VisualIssueID: issue.VisualIssueID, Type: issue.Type, ConfidenceMilli: issue.ConfidenceMilli,
				RegionMilli: slices.Clone(issue.RegionMilli), ShapeRefs: slices.Clone(issue.ShapeRefs),
			})
			report.Issues = append(report.Issues, qualifiedPPTXRuntimeIssue(cfg, PPTXVisualRuntimeIssue{
				SlideIndex: page.SlideIndex, Class: issue.Type, EvidenceSource: "subjective", EvidenceID: issue.VisualIssueID,
				ConfidenceMilli: issue.ConfidenceMilli, RegionMilli: slices.Clone(issue.RegionMilli), ShapeRefs: slices.Clone(issue.ShapeRefs),
			}))
		}
		report.Pages = append(report.Pages, projected)
	}
	return report
}

func unavailablePPTXVisualReport(code app.ToolErrorCode, kind pptxVisualQAErrorKind, selected []int) PPTXVisualReport {
	report := PPTXVisualReport{
		SchemaVersion: pptxVisualReportSchema, Status: "unavailable", FailureCode: code,
		Infrastructure: PPTXVisualInfrastructure{Renderer: "unavailable", Diagnostics: "unavailable", Model: "unavailable"},
		Pages:          []PPTXVisualReportPage{}, Issues: []PPTXVisualRuntimeIssue{},
	}
	if kind == pptxVisualQAModelError {
		report.Infrastructure.Renderer = "completed"
		report.Infrastructure.Diagnostics = "completed"
	}
	if len(selected) == 0 {
		report.Infrastructure.Model = "not_required"
	}
	return report
}

func qualifiedPPTXRuntimeIssue(cfg config.PPTXVisualQAAdapterConfig, issue PPTXVisualRuntimeIssue) PPTXVisualRuntimeIssue {
	issue.RepairQualified = slices.Contains(cfg.RepairQualifiedClasses, issue.Class)
	issue.BlockingQualified = slices.Contains(cfg.BlockingQualifiedClasses, issue.Class)
	return issue
}

func pptxRuntimeClassForFactReview(kind, semanticEffect string) string {
	switch {
	case kind == "text_clipping" && semanticEffect == "required_content_lost":
		return "text_clipped"
	case kind == "geometry_overlap" && semanticEffect == "harmful_obstruction":
		return "content_obscured"
	case kind == "off_canvas" && semanticEffect == "harmful_overflow":
		return "element_off_canvas"
	default:
		return ""
	}
}

func pptxRuntimeClassForDiagnostic(kind string) string {
	switch kind {
	case "text_clipping":
		return "text_clipped"
	case "geometry_overlap":
		return "content_obscured"
	case "off_canvas":
		return "element_off_canvas"
	default:
		return ""
	}
}

func applyPPTXVisualPolicy(report PPTXVisualReport, cfg config.PPTXVisualQAAdapterConfig) pptxVisualPolicyDecision {
	phase := strings.ToLower(strings.TrimSpace(cfg.Phase))
	decision := pptxVisualPolicyDecision{}
	if phase == "disabled" || phase == "shadow" {
		return decision
	}
	decision.Warnings = slices.Clone(report.Issues)
	for _, issue := range report.Issues {
		if issue.RepairQualified && cfg.MaxRepairAttempts > 0 {
			decision.Repairable = append(decision.Repairable, issue)
		}
		if (phase == "qualified_blocking" || phase == "default_on") && issue.BlockingQualified {
			decision.Blocking = append(decision.Blocking, issue)
		}
	}
	if phase == "qualified_blocking" || phase == "default_on" {
		for _, unavailable := range report.Unavailable {
			if slices.Contains(cfg.BlockingQualifiedClasses, unavailable.Class) {
				decision.Failure = app.ToolErrorPPTXRenderDiagnosticUnavailable
				break
			}
		}
	}
	return decision
}

func pptxVisualReportMap(report PPTXVisualReport) (map[string]any, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func pptxVisualWarningSummary(report PPTXVisualReport, cfg config.PPTXVisualQAAdapterConfig) string {
	decision := applyPPTXVisualPolicy(report, cfg)
	if len(decision.Warnings) == 0 {
		return ""
	}
	classes := make([]string, 0, len(decision.Warnings))
	slides := make([]int, 0, len(decision.Warnings))
	for _, issue := range decision.Warnings {
		if !slices.Contains(classes, issue.Class) {
			classes = append(classes, issue.Class)
		}
		if !slices.Contains(slides, issue.SlideIndex) {
			slides = append(slides, issue.SlideIndex)
		}
	}
	slices.Sort(classes)
	slices.Sort(slides)
	return fmt.Sprintf("Final-render review warning on slide(s) %s: %s.", joinPPTXInts(slides), strings.Join(classes, ", "))
}

func joinPPTXInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ",")
}

func pptxVisualPolicyConfigSHA256(cfg config.PPTXVisualQAAdapterConfig) (string, error) {
	policy := struct {
		Phase                     string   `json:"phase"`
		RepairQualifiedClasses    []string `json:"repair_qualified_classes"`
		RepairQualifiedOperations []string `json:"repair_qualified_operations"`
		BlockingQualifiedClasses  []string `json:"blocking_qualified_classes"`
		MaxRepairAttempts         int      `json:"max_repair_attempts"`
		DiagnosticToleranceMilli  int      `json:"diagnostic_tolerance_milli"`
	}{
		Phase:                     strings.ToLower(strings.TrimSpace(cfg.Phase)),
		RepairQualifiedClasses:    slices.Clone(cfg.RepairQualifiedClasses),
		RepairQualifiedOperations: slices.Clone(cfg.RepairQualifiedOperations),
		BlockingQualifiedClasses:  slices.Clone(cfg.BlockingQualifiedClasses),
		MaxRepairAttempts:         cfg.MaxRepairAttempts,
		DiagnosticToleranceMilli:  cfg.DiagnosticToleranceMilli,
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}
	return pptxBytesSHA256(raw), nil
}
