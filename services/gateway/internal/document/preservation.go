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
	policy, _ := registeredDocumentFormatPolicies.operation(before.Format, edit.Operation)
	appliedDetails := map[string]any{}
	if len(details) > 0 && details[0] != nil {
		appliedDetails = details[0]
	}
	if policy.VerifyExpected != nil {
		if err := policy.VerifyExpected(before, after, edit, matches); err != nil {
			return report, preservationError(before.Format, err.Error())
		}
	}
	if policy.VerifyTargetStructure != nil {
		if err := policy.VerifyTargetStructure(before, after, edit, matches); err != nil {
			return report, preservationError(before.Format, err.Error())
		}
	}
	allowedLayoutShapes := map[string]bool{}
	if policy.VerifyLayoutChanges != nil {
		var err error
		allowedLayoutShapes, err = policy.VerifyLayoutChanges(before, after, edit, appliedDetails)
		if err != nil {
			return report, preservationError(before.Format, err.Error())
		}
	}
	if policy.CheckUnchangedContent {
		if err := verifyUnchangedContent(before, after, edit, matches, allowedLayoutShapes, policy); err != nil {
			return report, preservationError(before.Format, err.Error())
		}
	}
	for _, category := range []string{"assets", "annotations", "layout"} {
		beforeStatus := enrichmentCoverage(before.Enrichment, category)
		afterStatus := enrichmentCoverage(after.Enrichment, category)
		if beforeStatus == "unknown" || afterStatus == "unknown" {
			report.Warnings = append(report.Warnings, category+" preservation is unknown because the parser did not expose that category")
			continue
		}
		beforeValues := evidenceFingerprints(before.Enrichment, category, edit, false, allowedLayoutShapes, policy)
		afterValues := evidenceFingerprints(after.Enrichment, category, edit, true, allowedLayoutShapes, policy)
		allowsEvidenceDelta := policy.AllowsEvidenceDelta
		if allowsEvidenceDelta == nil {
			allowsEvidenceDelta = strictEvidenceDelta
		}
		if !allowsEvidenceDelta(beforeValues, afterValues) {
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

func verifyTextReplacement(before, after Representation, edit EditRequest) error {
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
	return nil
}

func verifyUnchangedContent(before, after Representation, edit EditRequest, matches []Match, allowedLayoutShapes map[string]bool, policy preservationPolicy) error {
	afterByPath := map[string]Block{}
	for _, block := range after.Blocks {
		afterByPath[stringValue(block.Location["path"])] = block
	}
	for _, block := range before.Blocks {
		if mutationAllowsBlock(edit, matches, block, policy) {
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

func mutationAllowsBlock(edit EditRequest, matches []Match, block Block, policy preservationPolicy) bool {
	path := stringValue(block.Location["path"])
	for _, match := range matches {
		if path != "" && path == stringValue(match.Location["path"]) {
			return true
		}
	}
	if policy.AllowsBlock != nil {
		return policy.AllowsBlock(edit, block)
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

func evidenceFingerprints(enrichment map[string]any, category string, edit EditRequest, after bool, allowedLayoutShapes map[string]bool, policy preservationPolicy) []string {
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
				projection := map[string]any{"kind": key, "text": item["text"], "target": item["target"], "author": item["author"]}
				if key == "hyperlinks" {
					if policy.AllowsAnnotationText != nil && policy.AllowsAnnotationText(edit, item) {
						delete(projection, "text")
					} else if !after && strings.EqualFold(strings.TrimSpace(edit.Operation), "replace_text") {
						projection["text"] = replacementExpectedText(stringValue(item["text"]), mapSlice(edit.Arguments["replacements"]))
					}
				}
				if !policy.ChangesEntityIndexes {
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
				if policy.ChangesEntityIndexes {
					for _, field := range []string{"index", "slide_index", "row_index", "path"} {
						delete(projection, field)
					}
					if key == "page_markers" {
						// The reader derives actual_total from physical slide count. A
						// structural edit intentionally changes that value while preserving
						// the marker text so it can be surfaced as a stale-marker warning.
						delete(projection, "actual_total")
					}
				}
				if policy.NormalizeEvidence != nil {
					policy.NormalizeEvidence(category, key, projection, edit, after)
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
