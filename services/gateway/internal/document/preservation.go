package document

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type PreservationReport struct {
	Warnings []string
}

func ValidatePreservation(before, after Representation, edit EditRequest, matches []Match, details ...map[string]any) (PreservationReport, error) {
	report := PreservationReport{}
	appliedDetails := map[string]any{}
	if len(details) > 0 && details[0] != nil {
		appliedDetails = details[0]
	}
	if err := verifyExpectedMutation(before, after, edit, matches); err != nil {
		return report, preservationError(before.Format, err.Error())
	}
	allowedLayoutShapes, err := verifyReportedLayoutChanges(before, after, edit, appliedDetails)
	if err != nil {
		return report, preservationError(before.Format, err.Error())
	}
	if err := verifyUnchangedContent(before, after, edit, matches, allowedLayoutShapes); err != nil {
		return report, preservationError(before.Format, err.Error())
	}
	for _, category := range []string{"assets", "annotations", "layout"} {
		beforeStatus := enrichmentCoverage(before.Enrichment, category)
		afterStatus := enrichmentCoverage(after.Enrichment, category)
		if beforeStatus == "unknown" || afterStatus == "unknown" {
			report.Warnings = append(report.Warnings, category+" preservation is unknown because the parser did not expose that category")
			continue
		}
		beforeValues := evidenceFingerprints(before.Enrichment, category, edit, false, allowedLayoutShapes)
		afterValues := evidenceFingerprints(after.Enrichment, category, edit, true, allowedLayoutShapes)
		if !operationAllowsEvidenceDelta(edit.Operation, beforeValues, afterValues) {
			return report, preservationError(before.Format, fmt.Sprintf("%s evidence changed outside the editable content category", category))
		}
		if beforeStatus != "complete" || afterStatus != "complete" {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s preservation covers only parser-visible evidence (%s -> %s)", category, beforeStatus, afterStatus))
		}
	}
	return report, nil
}

func preservationError(format, detail string) error {
	return &PipelineError{Code: CodePreservationMismatch, Stage: StageApply, Format: format, Detail: detail}
}

func verifyExpectedMutation(before, after Representation, edit EditRequest, matches []Match) error {
	operation := strings.ToLower(strings.TrimSpace(edit.Operation))
	if operation == "replace_text" {
		for _, replacement := range mapSlice(edit.Arguments["replacements"]) {
			find := stringValue(replacement["find"])
			replace := stringValue(replacement["replace"])
			beforeCount := countBlockText(before.Blocks, find)
			afterCount := countBlockText(after.Blocks, find)
			if beforeCount <= afterCount {
				return fmt.Errorf("replacement target %q was not removed from the structured output", find)
			}
			if replace != "" && countBlockText(after.Blocks, replace) == 0 {
				return fmt.Errorf("replacement value %q was not found in the structured output", replace)
			}
		}
		if strings.EqualFold(before.Format, "docx") {
			if err := verifyDOCXTextReplacementRuns(before, after, edit, matches); err != nil {
				return err
			}
		}
		return nil
	}
	if handled, err := verifyDOCXExpectedMutation(operation, before, after, edit, matches); handled {
		return err
	}
	if handled, err := verifyXLSXExpectedMutation(operation, before, after, edit); handled {
		return err
	}
	if handled, err := verifyPPTXExpectedMutation(operation, before, after, edit); handled {
		return err
	}
	if handled, err := verifyPDFExpectedMutation(operation, before, after, edit); handled {
		return err
	}
	return nil
}

func verifyUnchangedContent(before, after Representation, edit EditRequest, matches []Match, allowedLayoutShapes map[string]bool) error {
	operation := strings.ToLower(strings.TrimSpace(edit.Operation))
	switch operation {
	case "replace_text", "replace_paragraph", "set_text_style", "update_cell", "update_row", "update_slide", "rotate_pages":
	default:
		return nil
	}
	afterByPath := map[string]Block{}
	for _, block := range after.Blocks {
		afterByPath[stringValue(block.Location["path"])] = block
	}
	for _, block := range before.Blocks {
		if mutationAllowsBlock(edit, matches, block) {
			continue
		}
		path := stringValue(block.Location["path"])
		other, ok := afterByPath[path]
		layoutOnly := allowedLayoutShapes[layoutShapeKey(block.Location)]
		if !ok || other.Text != block.Text || (!layoutOnly && !sameJSON(other.Format, block.Format)) {
			return fmt.Errorf("unrelated content changed at %s", path)
		}
	}
	return nil
}

func mutationAllowsBlock(edit EditRequest, matches []Match, block Block) bool {
	operation := strings.ToLower(strings.TrimSpace(edit.Operation))
	path := stringValue(block.Location["path"])
	for _, match := range matches {
		if path != "" && path == stringValue(match.Location["path"]) {
			return true
		}
	}
	if allowed, handled := xlsxMutationAllowsBlock(operation, edit, block); handled {
		return allowed
	}
	if allowed, handled := pptxMutationAllowsBlock(operation, edit, block); handled {
		return allowed
	}
	return false
}

func enrichmentCoverage(enrichment map[string]any, category string) string {
	if enrichment == nil {
		return "unknown"
	}
	status := strings.ToLower(strings.TrimSpace(stringValue(mapValue(enrichment["coverage"])[category])))
	if status == "" {
		return "unknown"
	}
	return status
}

func evidenceFingerprints(enrichment map[string]any, category string, edit EditRequest, after bool, allowedLayoutShapes map[string]bool) []string {
	if enrichment == nil {
		return nil
	}
	values := []string{}
	switch category {
	case "assets":
		assets := mapValue(enrichment["assets"])
		for _, key := range []string{"images", "charts", "embedded_objects"} {
			for _, item := range mapSlice(assets[key]) {
				source := mapValue(item["source"])
				projection := map[string]any{
					"kind": item["kind"], "sha256": item["sha256"], "content_type": item["content_type"],
					"relationship_id": source["relationship_id"], "part_name": source["part_name"],
				}
				values = append(values, fingerprint(projection))
			}
		}
	case "annotations":
		annotations := mapValue(enrichment["annotations"])
		for _, key := range []string{"comments", "notes", "hyperlinks"} {
			for _, item := range mapSlice(annotations[key]) {
				text := item["text"]
				if key == "hyperlinks" && !after && strings.EqualFold(strings.TrimSpace(edit.Operation), "replace_text") {
					text = replacementExpectedText(stringValue(text), mapSlice(edit.Arguments["replacements"]))
				}
				projection := map[string]any{"kind": key, "text": text, "target": item["target"], "author": item["author"]}
				if !operationChangesEntityIndexes(edit.Operation) {
					projection["anchor"] = mapValue(item["location"])["path"]
				}
				values = append(values, fingerprint(projection))
			}
		}
	case "layout":
		layout := mapValue(enrichment["layout"])
		for _, key := range []string{"sections", "page_settings", "slide_layouts", "merged_ranges", "shapes", "companion_groups", "page_markers"} {
			for _, item := range mapSlice(layout[key]) {
				if key == "shapes" && allowedLayoutShapes[layoutShapeKey(item)] {
					continue
				}
				projection := cloneMap(item)
				if key == "shapes" {
					delete(projection, "text")
					style := cloneMap(mapValue(projection["text_style"]))
					delete(style, "visual_units")
					delete(style, "single_line_fit_ratio")
					projection["text_style"] = style
				}
				if strings.EqualFold(strings.TrimSpace(edit.Operation), "rotate_pages") {
					delete(projection, "rotation")
				}
				if operationChangesEntityIndexes(edit.Operation) {
					for _, field := range []string{"index", "slide_index", "row_index", "path"} {
						delete(projection, field)
					}
				}
				if key == "merged_ranges" && after {
					projection["range"] = xlsxMergedRangeBeforeCoordinates(projection, edit)
				}
				values = append(values, fingerprint(projection))
			}
		}
	}
	slices.Sort(values)
	return values
}

func replacementExpectedText(text string, replacements []map[string]any) string {
	_, expected, err := docxReplacementSpans(text, replacements)
	if err != nil {
		return text
	}
	return expected
}

func operationAllowsEvidenceDelta(operation string, before, after []string) bool {
	operation = strings.ToLower(strings.TrimSpace(operation))
	if allowed, handled := pptxOperationAllowsEvidenceDelta(operation, before, after); handled {
		return allowed
	}
	if allowed, handled := pdfOperationAllowsEvidenceDelta(operation, before, after); handled {
		return allowed
	}
	return slices.Equal(before, after)
}

func operationChangesEntityIndexes(operation string) bool {
	operation = strings.ToLower(strings.TrimSpace(operation))
	return docxOperationChangesEntityIndexes(operation) ||
		xlsxOperationChangesEntityIndexes(operation) ||
		pptxOperationChangesEntityIndexes(operation) ||
		pdfOperationChangesEntityIndexes(operation)
}

func multisetContains(superset, subset []string) bool {
	counts := map[string]int{}
	for _, item := range superset {
		counts[item]++
	}
	for _, item := range subset {
		counts[item]--
		if counts[item] < 0 {
			return false
		}
	}
	return true
}

func fingerprint(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sameJSON(left, right any) bool {
	leftRaw, _ := json.Marshal(left)
	rightRaw, _ := json.Marshal(right)
	return string(leftRaw) == string(rightRaw)
}

func countBlockText(blocks []Block, text string) int {
	if text == "" {
		return 0
	}
	count := 0
	for _, block := range blocks {
		count += strings.Count(block.Text, text)
	}
	return count
}

func blockAtAnyMatchHasText(document Representation, matches []Match, expected string) bool {
	for _, match := range matches {
		path := stringValue(match.Location["path"])
		for _, block := range document.Blocks {
			if stringValue(block.Location["path"]) == path && block.Text == expected {
				return true
			}
		}
	}
	return false
}

func firstMatchText(matches []Match) string {
	for _, match := range matches {
		if match.Text != "" {
			return match.Text
		}
	}
	return ""
}

func intSlice(value any) []int {
	out := []int{}
	for _, item := range anySlice(value) {
		if current := intValue(item); current > 0 {
			out = append(out, current)
		}
	}
	return out
}
