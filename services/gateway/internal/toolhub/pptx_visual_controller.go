package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type pptxSealedCandidateAttempt struct {
	Attempt              int               `json:"attempt"`
	InputCandidateSHA256 string            `json:"input_candidate_sha256,omitempty"`
	CandidateSHA256      string            `json:"candidate_sha256"`
	RepairPlanSHA256     []string          `json:"repair_plan_sha256,omitempty"`
	VisualReportSHA256   string            `json:"visual_report_sha256"`
	Accepted             bool              `json:"accepted"`
	FailureCode          app.ToolErrorCode `json:"failure_code,omitempty"`
	StopReason           string            `json:"stop_reason,omitempty"`
}

type pptxVisualCandidatePreparation struct {
	CandidatePath string
	Report        PPTXVisualReport
	Attempts      []pptxSealedCandidateAttempt
}

type pptxVisualRollbackState struct {
	CandidatePath string
	Report        PPTXVisualReport
	Decision      pptxVisualPolicyDecision
	PixelHashes   map[int]string
}

func (h *ToolHub) preparePPTXVisualCandidate(
	ctx context.Context,
	candidatePath, operation, jobDir, sessionID, runID string,
	selectedSlides []int,
	changedShapeIndexes map[int][]int,
	changedAllSlides []int,
) (pptxVisualCandidatePreparation, error) {
	phase := strings.ToLower(strings.TrimSpace(h.cfg.Adapters.PPTXVisualQA.Phase))
	if phase == "disabled" || h.pptxVisualQA == nil {
		candidateSHA, err := pptxVisualFileSHA256(ctx, candidatePath)
		if err != nil {
			return pptxVisualCandidatePreparation{}, &app.CodedToolError{Code: app.ToolErrorPPTXRenderInvalidInput, Err: err}
		}
		report := PPTXVisualReport{
			SchemaVersion: pptxVisualReportSchema, Status: "disabled", CandidateSHA256: candidateSHA,
			Infrastructure: PPTXVisualInfrastructure{Renderer: "not_required", Diagnostics: "not_required", Model: "not_required"},
			Pages:          []PPTXVisualReportPage{}, Issues: []PPTXVisualRuntimeIssue{},
		}
		return pptxVisualCandidatePreparation{CandidatePath: candidatePath, Report: report, Attempts: []pptxSealedCandidateAttempt{pptxVisualAttemptRecord(0, "", nil, report)}}, nil
	}

	authority := h.pptxVisualRepairAuthority(ctx, runID)
	request := pptxVisualQARequest{
		CandidatePath: candidatePath, Operation: operation, SlideIndexes: slices.Clone(selectedSlides),
		ChangedShapeIndexes: clonePPTXChangedShapes(changedShapeIndexes), ChangedAllSlides: slices.Clone(changedAllSlides),
	}
	currentPath := candidatePath
	inputCandidateSHA := ""
	pendingPlanHashes := []string{}
	attempts := []pptxSealedCandidateAttempt{}
	seenCandidates := map[string]bool{}
	seenPlans := map[string]bool{}
	var rollback *pptxVisualRollbackState

	for repairAttempt := 0; ; repairAttempt++ {
		request.CandidatePath = currentPath
		visualResult, visualErr := h.pptxVisualQA.Assess(ctx, request)
		if visualErr != nil {
			kind := pptxVisualQAErrorKindOf(visualErr)
			code := pptxVisualQAErrorCodeOf(visualErr)
			h.auditPPTXVisualQAFailure(ctx, sessionID, runID, operation, request.SlideIndexes, kind, code, "Recorded PPTX final-render visual QA failure")
			if kind == pptxVisualQAIntegrityError || phase == "qualified_blocking" || phase == "default_on" {
				return pptxVisualCandidatePreparation{}, &app.CodedToolError{Code: code, Err: fmt.Errorf("PPTX final-render visual QA failed: %w", visualErr)}
			}
			candidateSHA, hashErr := pptxVisualFileSHA256(ctx, currentPath)
			if hashErr != nil {
				return pptxVisualCandidatePreparation{}, hashErr
			}
			report := unavailablePPTXVisualReport(code, kind, request.SlideIndexes)
			report.CandidateSHA256 = candidateSHA
			attempts = append(attempts, pptxVisualAttemptRecord(repairAttempt, inputCandidateSHA, pendingPlanHashes, report))
			return pptxVisualCandidatePreparation{CandidatePath: currentPath, Report: report, Attempts: attempts}, nil
		}
		actualCandidateSHA, hashErr := pptxVisualFileSHA256(ctx, currentPath)
		if hashErr != nil {
			return pptxVisualCandidatePreparation{}, &app.CodedToolError{Code: app.ToolErrorPPTXRenderInvalidInput, Err: hashErr}
		}
		if visualResult.CandidateSHA256 != actualCandidateSHA {
			return pptxVisualCandidatePreparation{}, &app.CodedToolError{Code: app.ToolErrorPPTXRenderDiagnosticInvalid, Err: errors.New("PPTX visual report does not match the candidate bytes")}
		}

		report := buildPPTXVisualReport(visualResult, h.cfg.Adapters.PPTXVisualQA)
		decision := applyPPTXVisualPolicy(report, h.cfg.Adapters.PPTXVisualQA)
		attemptRecord := pptxVisualAttemptRecord(repairAttempt, inputCandidateSHA, pendingPlanHashes, report)
		attempts = append(attempts, attemptRecord)
		h.auditPPTXVisualReport(ctx, sessionID, runID, operation, request.SlideIndexes, report, repairAttempt)
		if decision.Failure != "" {
			return pptxVisualCandidatePreparation{}, &app.CodedToolError{Code: decision.Failure, Err: errors.New("PPTX final-render evidence required by the active policy is unavailable")}
		}
		if seenCandidates[report.CandidateSHA256] {
			return pptxVisualCandidatePreparation{}, &app.CodedToolError{Code: app.ToolErrorPPTXRenderRepairInvalid, Err: errors.New("PPTX visual repair repeated an earlier candidate")}
		}
		seenCandidates[report.CandidateSHA256] = true

		pixelHashes := pptxVisualPixelHashes(visualResult)
		if rollback != nil {
			if !pptxVisualPixelsChanged(rollback.PixelHashes, pixelHashes, request.SlideIndexes) {
				attempts[len(attempts)-1].FailureCode = app.ToolErrorPPTXRenderRepairInvalid
				attempts[len(attempts)-1].StopReason = "repair_pixels_unchanged"
				if len(rollback.Decision.Blocking) == 0 || phase == "warning" {
					return pptxVisualCandidatePreparation{CandidatePath: rollback.CandidatePath, Report: rollback.Report, Attempts: attempts}, nil
				}
				return pptxVisualCandidatePreparation{}, &app.CodedToolError{Code: app.ToolErrorPPTXRenderRepairInvalid, Err: errors.New("PPTX visual repair did not change rendered pixels")}
			}
			if len(rollback.Decision.Blocking) == 0 && len(decision.Blocking) > 0 {
				attempts[len(attempts)-1].FailureCode = app.ToolErrorPPTXRenderVisualBlocked
				attempts[len(attempts)-1].StopReason = "repair_introduced_blocking_regression"
				return pptxVisualCandidatePreparation{CandidatePath: rollback.CandidatePath, Report: rollback.Report, Attempts: attempts}, nil
			}
		}

		actionableBySlide := pptxActionableIssuesBySlide(decision.Repairable, visualResult, authority)
		if phase == "shadow" || len(actionableBySlide) == 0 || len(h.cfg.Adapters.PPTXVisualQA.RepairQualifiedOperations) == 0 {
			if len(decision.Blocking) > 0 {
				attempts[len(attempts)-1].FailureCode = app.ToolErrorPPTXRenderVisualBlocked
				attempts[len(attempts)-1].StopReason = "qualified_blocking_issue_not_repairable"
				return pptxVisualCandidatePreparation{}, &app.CodedToolError{Code: app.ToolErrorPPTXRenderVisualBlocked, Err: errors.New("PPTX final-render review found a qualified blocking issue")}
			}
			return pptxVisualCandidatePreparation{CandidatePath: currentPath, Report: report, Attempts: attempts}, nil
		}
		if repairAttempt >= h.cfg.Adapters.PPTXVisualQA.MaxRepairAttempts {
			attempts[len(attempts)-1].StopReason = "repair_budget_exhausted"
			if len(decision.Blocking) > 0 {
				attempts[len(attempts)-1].FailureCode = app.ToolErrorPPTXRenderVisualBlocked
				return pptxVisualCandidatePreparation{}, &app.CodedToolError{Code: app.ToolErrorPPTXRenderVisualBlocked, Err: errors.New("qualified PPTX visual issue remains after the repair budget")}
			}
			return pptxVisualCandidatePreparation{CandidatePath: currentPath, Report: report, Attempts: attempts}, nil
		}

		planner, ok := h.pptxVisualQA.(pptxVisualRepairPlanner)
		if !ok {
			return pptxVisualCandidatePreparation{}, &app.CodedToolError{Code: app.ToolErrorPPTXRenderBackendUnavailable, Err: errors.New("PPTX visual repair planner is unavailable")}
		}
		slideIndexes := make([]int, 0, len(actionableBySlide))
		for slideIndex := range actionableBySlide {
			slideIndexes = append(slideIndexes, slideIndex)
		}
		slices.Sort(slideIndexes)
		plans := make([]pptxVisualRepairPlan, 0, len(slideIndexes))
		planPages := make([]pptxVisualQAPageResult, 0, len(slideIndexes))
		planHashes := make([]string, 0, len(slideIndexes))
		planFailed := false
		var planErr error
		for _, slideIndex := range slideIndexes {
			page, found := pptxVisualResultPage(visualResult, slideIndex)
			if !found {
				planFailed, planErr = true, errors.New("PPTX visual repair page evidence is unavailable")
				break
			}
			plan, _, err := planner.PlanRepair(ctx, pptxVisualRepairRequest{
				Attempt: repairAttempt + 1, Operation: operation, Authority: authority, Page: page, Issues: actionableBySlide[slideIndex],
			})
			if err != nil {
				planFailed, planErr = true, err
				break
			}
			if err := validatePPTXVisualRepairPlan(plan, pptxVisualRepairRequest{
				Attempt: repairAttempt + 1, Operation: operation, Authority: authority, Page: page, Issues: actionableBySlide[slideIndex],
			}, h.cfg.Adapters.PPTXVisualQA.RepairQualifiedOperations); err != nil {
				planFailed, planErr = true, &app.CodedToolError{Code: app.ToolErrorPPTXRenderRepairInvalid, Err: err}
				break
			}
			planSHA, err := pptxVisualRepairPlanSHA256(plan)
			if err != nil {
				planFailed, planErr = true, err
				break
			}
			if seenPlans[planSHA] {
				planFailed, planErr = true, &app.CodedToolError{Code: app.ToolErrorPPTXRenderRepairInvalid, Err: errors.New("PPTX visual repair repeated an earlier plan")}
				break
			}
			seenPlans[planSHA] = true
			plans, planPages, planHashes = append(plans, plan), append(planPages, page), append(planHashes, planSHA)
		}
		if planFailed {
			h.auditPPTXVisualRepairFailure(ctx, sessionID, runID, operation, repairAttempt+1, planErr)
			if len(decision.Blocking) == 0 || phase == "warning" {
				attempts[len(attempts)-1].FailureCode = app.ToolErrorCodeFrom(planErr)
				attempts[len(attempts)-1].StopReason = "repair_plan_rejected"
				return pptxVisualCandidatePreparation{CandidatePath: currentPath, Report: report, Attempts: attempts}, nil
			}
			return pptxVisualCandidatePreparation{}, planErr
		}

		rollback = &pptxVisualRollbackState{CandidatePath: currentPath, Report: report, Decision: decision, PixelHashes: pixelHashes}
		nextChangedShapes := map[int][]int{}
		nextPath := currentPath
		currentSHA := report.CandidateSHA256
		for index, plan := range plans {
			outputPath := filepath.Join(jobDir, fmt.Sprintf("candidate-repair-%d-%d.pptx", repairAttempt+1, plan.SlideIndex))
			result, err := applyPPTXVisualRepair(ctx, nextPath, outputPath, currentSHA, planPages[index], plan)
			if err != nil {
				h.auditPPTXVisualRepairFailure(ctx, sessionID, runID, operation, repairAttempt+1, err)
				if len(decision.Blocking) == 0 || phase == "warning" {
					attempts[len(attempts)-1].FailureCode = app.ToolErrorCodeFrom(err)
					attempts[len(attempts)-1].StopReason = "repair_application_rejected"
					return pptxVisualCandidatePreparation{CandidatePath: currentPath, Report: report, Attempts: attempts}, nil
				}
				return pptxVisualCandidatePreparation{}, err
			}
			nextPath, currentSHA = outputPath, result.CandidateSHA256
			nextChangedShapes[plan.SlideIndex] = appendUniquePPTXInts(nextChangedShapes[plan.SlideIndex], result.ChangedShapeIndexes...)
		}
		inputCandidateSHA = report.CandidateSHA256
		pendingPlanHashes = planHashes
		currentPath = nextPath
		request = pptxVisualQARequest{
			CandidatePath: currentPath, Operation: operation, SlideIndexes: slideIndexes,
			ChangedShapeIndexes: nextChangedShapes, ChangedAllSlides: []int{},
		}
	}
}

func pptxVisualAttemptRecord(attempt int, inputSHA string, planHashes []string, report PPTXVisualReport) pptxSealedCandidateAttempt {
	raw, _ := json.Marshal(report)
	return pptxSealedCandidateAttempt{
		Attempt: attempt, InputCandidateSHA256: inputSHA, CandidateSHA256: report.CandidateSHA256,
		RepairPlanSHA256: slices.Clone(planHashes), VisualReportSHA256: pptxBytesSHA256(raw), FailureCode: report.FailureCode,
	}
}

func (h *ToolHub) auditPPTXVisualReport(ctx context.Context, sessionID, runID, operation string, slides []int, report PPTXVisualReport, attempt int) {
	fields, err := pptxVisualReportMap(report)
	if err != nil {
		fields = map[string]any{"schema_version": report.SchemaVersion, "status": "audit_projection_failed"}
	}
	fields["phase"] = h.cfg.Adapters.PPTXVisualQA.Phase
	fields["operation"] = operation
	fields["slide_indexes"] = slices.Clone(slides)
	fields["attempt"] = attempt
	h.addAudit(ctx, app.AuditEvent{
		ID: app.NewID("audit"), Time: time.Now().UTC(), SessionID: sessionID, RunID: runID, Actor: "toolhub",
		Type: "document.pptx.visual_qa", Summary: "Recorded PPTX final-render visual QA evidence", Fields: fields,
	})
}

func (h *ToolHub) auditPPTXVisualRepairFailure(ctx context.Context, sessionID, runID, operation string, attempt int, err error) {
	h.addAudit(ctx, app.AuditEvent{
		ID: app.NewID("audit"), Time: time.Now().UTC(), SessionID: sessionID, RunID: runID, Actor: "toolhub",
		Type: "document.pptx.visual_repair", Summary: "Stopped bounded PPTX visual repair",
		Fields: map[string]any{"phase": h.cfg.Adapters.PPTXVisualQA.Phase, "operation": operation, "attempt": attempt, "error_code": string(app.ToolErrorCodeFrom(err))},
	})
}

func pptxActionableIssuesBySlide(issues []PPTXVisualRuntimeIssue, result pptxVisualQAResult, authority pptxVisualRepairAuthority) map[int][]PPTXVisualRuntimeIssue {
	out := map[int][]PPTXVisualRuntimeIssue{}
	for _, page := range result.Pages {
		filtered := filterPPTXRepairIssues(issues, page, authority)
		if len(filtered) > 0 {
			out[page.SlideIndex] = filtered
		}
	}
	return out
}

func pptxVisualResultPage(result pptxVisualQAResult, slideIndex int) (pptxVisualQAPageResult, bool) {
	for _, page := range result.Pages {
		if page.SlideIndex == slideIndex {
			return page, true
		}
	}
	return pptxVisualQAPageResult{}, false
}

func pptxVisualPixelHashes(result pptxVisualQAResult) map[int]string {
	out := make(map[int]string, len(result.Pages))
	for _, page := range result.Pages {
		out[page.SlideIndex] = page.Raster.PNGSHA256
	}
	return out
}

func pptxVisualPixelsChanged(before, after map[int]string, slides []int) bool {
	for _, slideIndex := range slides {
		if before[slideIndex] == "" || after[slideIndex] == "" || before[slideIndex] == after[slideIndex] {
			return false
		}
	}
	return true
}

func clonePPTXChangedShapes(values map[int][]int) map[int][]int {
	out := make(map[int][]int, len(values))
	for slideIndex, indexes := range values {
		out[slideIndex] = slices.Clone(indexes)
	}
	return out
}

func appendUniquePPTXInts(values []int, additions ...int) []int {
	for _, value := range additions {
		if value > 0 && !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	slices.Sort(values)
	return values
}
