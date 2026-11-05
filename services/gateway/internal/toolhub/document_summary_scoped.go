package toolhub

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

// Version 2 keeps the previous summarizer available to persisted workflows.
// Every section is visited; per-section bounds include the late part and expose
// every omitted part rather than silently claiming whole-source coverage.
func (h *ToolHub) documentSummarizeScoped(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	ctx = withDocumentOCRExecution(ctx, sessionID, runID)
	path, err := h.resolveReadPath(ctx, stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	index, metadata, err := h.ensureDocumentIndex(ctx, path)
	if err != nil {
		return Result{}, err
	}
	if err := index.ValidateVersion(stringArg(args, "source_sha256", ""), stringArg(args, "structure_generation", "")); err != nil {
		return Result{}, err
	}
	previousGeneration := index.Manifest.StructureGeneration
	if len(index.Manifest.UnindexedPages) > 0 && index.Manifest.SourceBytes <= documentIndexFullOCRMaxSourceBytes {
		index, _, err = h.recognizeDocumentPages(ctx, metadata, index, index.Manifest.UnindexedPages)
		if err != nil {
			return Result{}, err
		}
	}
	units := index.SummaryUnits(documentSummaryMaxUnits)
	if len(units) == 0 {
		return Result{}, errors.New("the document has no recognized text to summarize")
	}
	sections := make([]documentSummarizeSection, 0, len(units))
	missing := []string{}
	required, covered := 0, 0
	for position, unit := range units {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		reportToolProgress(ctx, fmt.Sprintf("正在总结文档章节（%d/%d）", position, len(units)))
		section, total, omitted, err := h.summarizeScopedUnit(ctx, sessionID, runID, metadata, index, unit)
		if err != nil {
			return Result{}, err
		}
		sections = append(sections, section)
		required, covered = required+total, covered+total-len(omitted)
		missing = append(missing, omitted...)
	}
	reportToolProgress(ctx, fmt.Sprintf("正在总结文档章节（%d/%d）", len(units), len(units)))
	return scopedSummaryResult(metadata, index, previousGeneration, sections, required, covered, missing), nil
}

func (h *ToolHub) summarizeScopedUnit(ctx context.Context, sessionID, runID string, metadata document.Metadata, index *docindex.Index, unit docindex.SummaryUnit) (documentSummarizeSection, int, []string, error) {
	section := documentSummarizeSection{ID: unit.ID, Title: unit.Title, Page: unit.Page, PageEnd: unit.PageEnd, Unrecognized: index.UnindexedPagesInUnit(unit)}
	parts, missing := scopedSummaryParts(index, unit, h.cfg.FinalEvidenceBytes(h.cfg.Model.Fast))
	total := len(parts) + len(missing)
	if len(parts) == 0 {
		section.Summary, section.Sampled = "[No complete source unit fits the bounded summary context.]", true
		if len(missing) == 0 {
			missing = []string{unit.ID + ":unrecognized"}
			total = 1
		}
		return section, total, missing, nil
	}
	selected := summarySelectedParts(len(parts), documentSummaryMaxParts)
	summaries := []string{}
	for position, part := range parts {
		if !selected[position] {
			missing = append(missing, fmt.Sprintf("%s:part:%d", unit.ID, position+1))
			continue
		}
		title := unit.Title
		if len(parts) > 1 {
			title = fmt.Sprintf("%s (part %d/%d)", unit.Title, position+1, len(parts))
		}
		summary, err := h.cachedScopedSectionSummary(ctx, sessionID, runID, metadata, index, title, part)
		if err != nil {
			return section, total, missing, err
		}
		summaries = append(summaries, summary)
	}
	section.Sampled = len(missing) > 0
	section.Summary = strings.Join(summaries, "\n\n")
	if len(summaries) > 1 {
		prefix := "Summaries of covered parts of this section. Preserve qualifications and conflicts; missing parts are not evidence:\n\n"
		merged, err := h.cachedScopedSectionSummary(ctx, sessionID, runID, metadata, index, unit.Title, prefix+section.Summary)
		if err != nil {
			return section, total, missing, err
		}
		section.Summary = merged
	}
	return section, total, missing, nil
}

func summarySelectedParts(total, limit int) map[int]bool {
	selected := map[int]bool{}
	if total <= limit {
		for i := 0; i < total; i++ {
			selected[i] = true
		}
		return selected
	}
	for i := 0; i < limit; i++ {
		selected[i*(total-1)/(limit-1)] = true
	}
	return selected
}

func scopedSummaryResult(metadata document.Metadata, index *docindex.Index, previousGeneration string, sections []documentSummarizeSection, required, covered int, missing []string) Result {
	complete := len(missing) == 0 && len(index.Manifest.UnindexedPages) == 0 && index.Structure.Coverage.Status == "complete"
	status := "partial"
	if complete {
		status = "complete"
	}
	coverage := map[string]any{"scope": "whole_document", "status": status, "denominator_known": index.Structure.Coverage.Status == "complete", "required": required, "covered": covered, "missing": required - covered, "missing_parts": missing, "missing_source_nodes": index.Structure.Coverage.Missing, "missing_node_ids": index.Structure.Coverage.MissingNodeIDs, "unrecognized_pages": index.Manifest.UnindexedPages, "source_acquisition": documentJSONValue(index.Structure.Coverage), "task_coverage": "not_assessed"}
	var content strings.Builder
	fmt.Fprintf(&content, "Whole-document summary by section (%d sections; coverage=%s):\n", len(sections), status)
	for _, section := range sections {
		fmt.Fprintf(&content, "\n## %s\n%s\n", section.Title, section.Summary)
		if section.Sampled {
			content.WriteString("[Partial section: the per-section part budget was exhausted; bounded sampling includes the final part and lists every omitted part.]\n")
		}
		if len(section.Unrecognized) > 0 {
			fmt.Fprintf(&content, "[Scanned pages not recognized: %s]\n", pageList(section.Unrecognized))
		}
	}
	if !complete {
		fmt.Fprintf(&content, "\n[Incomplete whole-document coverage. Missing parts: %v. Unrecognized pages: %v. Source limitations: %v.]\n", missing, index.Manifest.UnindexedPages, index.Structure.Limitations)
	}
	text := content.String()
	return Result{Output: map[string]any{
		"path": metadata.Relative, "rel_path": metadata.Relative, "kind": metadata.Format, "contract_version": 2,
		"content": text, "bytes": len(text), "sections": documentJSONValue(sections), "count": len(sections),
		"structure": documentJSONValue(index.Structure), "source_sha256": index.Manifest.SourceSHA256, "structure_generation": index.Manifest.StructureGeneration,
		"generation_rebased": previousGeneration != index.Manifest.StructureGeneration, "previous_structure_generation": previousGeneration,
		"coverage": coverage, "missing_parts": missing, "read_complete": complete, "missing_page_indexes": append([]int{}, index.Manifest.UnindexedPages...), "untrusted": true,
	}}
}

// Version 2 treats each supplied part as an atomic source bundle. A capacity
// rejection must not split formulas from cached-result/provenance metadata.
func (h *ToolHub) cachedScopedSectionSummary(ctx context.Context, sessionID, runID string, metadata document.Metadata, index *docindex.Index, title, text string) (string, error) {
	return h.cachedSectionSummaryVersion(ctx, sessionID, runID, metadata, index, title, text, "document_section_summary_v2", documentSummarySystem+" Preserve source limitations, units, conflicts, formula definitions, and whether cached results were recalculated or missing. Never present an unrecalculated cached result as current. Missing units are not evidence.", false)
}
