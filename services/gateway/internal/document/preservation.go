package document

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
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
	switch operation {
	case "replace_text":
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
	case "replace_paragraph":
		if !blockAtAnyMatchHasText(after, matches, stringValue(edit.Arguments["text"])) {
			return fmt.Errorf("the replaced paragraph does not contain the expected after-value")
		}
	case "insert_paragraph":
		if countBlockText(after.Blocks, stringValue(edit.Arguments["text"])) <= countBlockText(before.Blocks, stringValue(edit.Arguments["text"])) {
			return fmt.Errorf("the inserted paragraph was not found in the structured output")
		}
	case "delete_paragraph":
		for _, match := range matches {
			if match.Text != "" && countBlockText(after.Blocks, match.Text) >= countBlockText(before.Blocks, match.Text) {
				return fmt.Errorf("the deleted paragraph remains in the structured output")
			}
		}
	case "set_text_style":
		index := intValue(edit.Arguments["paragraph_index"])
		style := stringValue(edit.Arguments["style"])
		if styleObject := mapValue(edit.Arguments["style"]); len(styleObject) > 0 {
			style = firstString(styleObject["builtin_style"], styleObject["style_name"])
		}
		if !paragraphHasStyle(after.Paragraphs, index, style) {
			return fmt.Errorf("the target paragraph does not have the requested style")
		}
		if !blockAtAnyMatchHasText(after, matches, firstMatchText(matches)) {
			return fmt.Errorf("styling unexpectedly changed the target paragraph text")
		}
	case "update_cell":
		if !cellHasValue(after, stringValue(edit.Arguments["sheet"]), stringValue(edit.Arguments["cell"]), stringValue(edit.Arguments["value"])) {
			return fmt.Errorf("the target cell does not contain the expected after-value")
		}
	case "insert_row", "append_row":
		if sheetRowCount(after, stringValue(edit.Arguments["sheet"])) != sheetRowCount(before, stringValue(edit.Arguments["sheet"]))+1 ||
			!sheetContainsValues(after, stringValue(edit.Arguments["sheet"]), anySlice(edit.Arguments["values"])) {
			return fmt.Errorf("the inserted row was not found at the expected structural boundary")
		}
	case "delete_row":
		if sheetRowCount(after, stringValue(edit.Arguments["sheet"])) != sheetRowCount(before, stringValue(edit.Arguments["sheet"]))-1 {
			return fmt.Errorf("the deleted row count was not reflected in the structured output")
		}
	case "update_row":
		if !sheetRowAtIndexHasValues(after, stringValue(edit.Arguments["sheet"]), intValue(edit.Arguments["row"]), anySlice(edit.Arguments["values"])) {
			return fmt.Errorf("the target row does not contain the expected after-values")
		}
	case "update_slide":
		for _, update := range mapSlice(edit.Arguments["updates"]) {
			if !slideShapeHasText(after, intValue(edit.Arguments["slide_index"]), intValue(update["shape_index"]), stringValue(update["text"])) {
				return fmt.Errorf("updated slide shape %d does not contain the expected after-value", intValue(update["shape_index"]))
			}
		}
	case "add_slide", "duplicate_slide":
		if len(after.Slides) != len(before.Slides)+1 {
			return fmt.Errorf("the structured slide count did not increase by one")
		}
	case "delete_slide":
		if len(after.Slides) != len(before.Slides)-1 {
			return fmt.Errorf("the structured slide count did not decrease by one")
		}
	case "extract_pages":
		if len(after.Pages) != len(intSlice(edit.Arguments["pages"])) {
			return fmt.Errorf("the extracted PDF page count does not match the request")
		}
	case "delete_pages":
		if len(after.Pages) != len(before.Pages)-len(intSlice(edit.Arguments["pages"])) {
			return fmt.Errorf("the deleted PDF page count does not match the request")
		}
	case "split":
		if len(after.Pages) != 1 {
			return fmt.Errorf("each split PDF output must contain exactly one page")
		}
	case "rotate_pages":
		if len(after.Pages) != len(before.Pages) {
			return fmt.Errorf("rotating pages unexpectedly changed the PDF page count")
		}
		rotation := intValue(edit.Arguments["rotation"])
		for _, pageIndex := range intSlice(edit.Arguments["pages"]) {
			beforeRotation, beforeOK := pageRotation(before.Pages, pageIndex)
			afterRotation, afterOK := pageRotation(after.Pages, pageIndex)
			if !beforeOK || !afterOK || ((beforeRotation+rotation)%360+360)%360 != ((afterRotation%360)+360)%360 {
				return fmt.Errorf("page %d does not have the requested rotation", pageIndex)
			}
		}
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
	switch operation {
	case "update_row":
		return equalFoldValue(block.Location["sheet"], stringValue(edit.Arguments["sheet"])) && intValue(block.Location["row_index"]) == intValue(edit.Arguments["row"])
	case "update_slide":
		if intValue(block.Location["slide_index"]) != intValue(edit.Arguments["slide_index"]) {
			return false
		}
		for _, update := range mapSlice(edit.Arguments["updates"]) {
			if intValue(block.Location["shape_index"]) == intValue(update["shape_index"]) {
				return true
			}
		}
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
				projection := map[string]any{"kind": key, "text": item["text"], "target": item["target"], "author": item["author"]}
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
					projection["range"] = mergedRangeBeforeCoordinates(stringValue(projection["range"]), edit)
				}
				values = append(values, fingerprint(projection))
			}
		}
	}
	slices.Sort(values)
	return values
}

func verifyReportedLayoutChanges(before, after Representation, edit EditRequest, details map[string]any) (map[string]bool, error) {
	allowed := map[string]bool{}
	if !strings.EqualFold(strings.TrimSpace(edit.Operation), "update_slide") {
		return allowed, nil
	}
	changes := mapSlice(details["layout_changes"])
	indexes := intSlice(details["layout_adjusted_shape_indexes"])
	if len(changes) == 0 && len(indexes) == 0 {
		return allowed, nil
	}
	if !strings.EqualFold(strings.TrimSpace(stringValue(details["layout_policy"])), "coordinated") {
		return nil, fmt.Errorf("layout changes were reported without coordinated layout_policy")
	}
	slideIndex := intValue(edit.Arguments["slide_index"])
	declared := map[int]bool{}
	for _, index := range indexes {
		if index <= 0 || declared[index] {
			return nil, fmt.Errorf("layout_adjusted_shape_indexes contains an invalid or duplicate shape index")
		}
		declared[index] = true
	}
	for _, change := range changes {
		shapeIndex := intValue(change["shape_index"])
		if !declared[shapeIndex] {
			return nil, fmt.Errorf("layout change for shape %d was not declared in the adjustment allowlist", shapeIndex)
		}
		beforeShape, beforeOK := layoutShape(before.Enrichment, slideIndex, shapeIndex)
		afterShape, afterOK := layoutShape(after.Enrichment, slideIndex, shapeIndex)
		if !beforeOK || !afterOK {
			return nil, fmt.Errorf("layout change shape %d was not present in both structured reads", shapeIndex)
		}
		if !sameJSON(layoutShapeState(beforeShape), normalizedLayoutChangeState(mapValue(change["before"]))) ||
			!sameJSON(layoutShapeState(afterShape), normalizedLayoutChangeState(mapValue(change["after"]))) {
			return nil, fmt.Errorf("layout change for shape %d did not match the re-read geometry and style", shapeIndex)
		}
		allowed[layoutShapeKey(beforeShape)] = true
	}
	if len(allowed) != len(declared) {
		return nil, fmt.Errorf("layout adjustment allowlist did not match the reported layout changes")
	}
	return allowed, nil
}

func layoutShape(enrichment map[string]any, slideIndex, shapeIndex int) (map[string]any, bool) {
	layout := mapValue(enrichment["layout"])
	for _, shape := range mapSlice(layout["shapes"]) {
		if intValue(shape["slide_index"]) == slideIndex && intValue(shape["shape_index"]) == shapeIndex && intValue(shape["group_child_index"]) == 0 {
			return shape, true
		}
	}
	return nil, false
}

func layoutShapeKey(shape map[string]any) string {
	return fmt.Sprintf("%d:%d:%d", intValue(shape["slide_index"]), intValue(shape["shape_index"]), intValue(shape["group_child_index"]))
}

func layoutShapeState(shape map[string]any) map[string]any {
	style := mapValue(shape["text_style"])
	return normalizedLayoutChangeState(map[string]any{
		"x": shape["x"], "y": shape["y"], "width": shape["width"], "height": shape["height"],
		"font_size_pt": style["font_size_pt"], "word_wrap": style["word_wrap"],
	})
}

func normalizedLayoutChangeState(value map[string]any) map[string]any {
	state := map[string]any{
		"x": intValue(value["x"]), "y": intValue(value["y"]), "width": intValue(value["width"]), "height": intValue(value["height"]),
		"font_size_pt": value["font_size_pt"], "word_wrap": value["word_wrap"],
	}
	if state["font_size_pt"] == nil {
		state["font_size_pt"] = nil
	}
	if state["word_wrap"] == nil {
		state["word_wrap"] = nil
	}
	return state
}

var mergedRangePattern = regexp.MustCompile(`^([A-Za-z]+)([0-9]+):([A-Za-z]+)([0-9]+)$`)

func mergedRangeBeforeCoordinates(value string, edit EditRequest) string {
	matches := mergedRangePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 5 {
		return value
	}
	startRow := intValue(matches[2])
	endRow := intValue(matches[4])
	row := intValue(edit.Arguments["row"])
	switch strings.ToLower(strings.TrimSpace(edit.Operation)) {
	case "insert_row":
		insertAt := row
		if strings.EqualFold(strings.TrimSpace(stringValue(edit.Arguments["position"])), "after") {
			insertAt++
		}
		if startRow >= insertAt {
			startRow--
			endRow--
		} else if endRow >= insertAt {
			endRow--
		}
	case "delete_row":
		if startRow >= row {
			startRow++
			endRow++
		} else if endRow >= row {
			endRow++
		}
	}
	return fmt.Sprintf("%s%d:%s%d", strings.ToUpper(matches[1]), startRow, strings.ToUpper(matches[3]), endRow)
}

func operationAllowsEvidenceDelta(operation string, before, after []string) bool {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "add_slide", "duplicate_slide":
		return multisetContains(after, before)
	case "delete_slide", "extract_pages", "delete_pages", "split":
		return multisetContains(before, after)
	default:
		return slices.Equal(before, after)
	}
}

func operationChangesEntityIndexes(operation string) bool {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "insert_paragraph", "delete_paragraph", "insert_row", "delete_row", "append_row", "add_slide", "duplicate_slide", "delete_slide", "extract_pages", "delete_pages", "split":
		return true
	default:
		return false
	}
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

func paragraphHasStyle(paragraphs []map[string]any, index int, style string) bool {
	for _, paragraph := range paragraphs {
		if intValue(paragraph["index"]) == index && strings.EqualFold(strings.TrimSpace(stringValue(paragraph["style"])), strings.TrimSpace(style)) {
			return true
		}
	}
	return false
}

func cellHasValue(document Representation, sheetName, address, expected string) bool {
	for _, sheet := range document.Sheets {
		if !strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			continue
		}
		for _, row := range mapSlice(sheet["rows"]) {
			for _, cell := range mapSlice(row["cells"]) {
				if strings.EqualFold(stringValue(cell["address"]), address) && stringValue(cell["value"]) == expected {
					return true
				}
			}
		}
	}
	return false
}

func sheetRowCount(document Representation, sheetName string) int {
	for _, sheet := range document.Sheets {
		if strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			return len(mapSlice(sheet["rows"]))
		}
	}
	return 0
}

func sheetContainsValues(document Representation, sheetName string, values []any) bool {
	for _, sheet := range document.Sheets {
		if !strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			continue
		}
		for _, row := range mapSlice(sheet["rows"]) {
			if rowHasValues(row, values) {
				return true
			}
		}
	}
	return false
}

func sheetRowAtIndexHasValues(document Representation, sheetName string, index int, values []any) bool {
	for _, sheet := range document.Sheets {
		if !strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			continue
		}
		for _, row := range mapSlice(sheet["rows"]) {
			if intValue(row["index"]) == index && rowHasValues(row, values) {
				return true
			}
		}
	}
	return false
}

func rowHasValues(row map[string]any, values []any) bool {
	cells := mapSlice(row["cells"])
	if len(cells) < len(values) {
		return false
	}
	for index, value := range values {
		if stringValue(cells[index]["value"]) != stringValue(value) {
			return false
		}
	}
	return true
}

func slideShapeHasText(document Representation, slideIndex, shapeIndex int, expected string) bool {
	expected = strings.Join(strings.Fields(expected), " ")
	for _, slide := range document.Slides {
		if intValue(slide["index"]) != slideIndex {
			continue
		}
		for _, item := range mapSlice(slide["items"]) {
			actual := strings.Join(strings.Fields(stringValue(item["text"])), " ")
			if intValue(item["shape_index"]) == shapeIndex && actual == expected {
				return true
			}
		}
	}
	return false
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

func pageRotation(pages []map[string]any, index int) (int, bool) {
	for _, page := range pages {
		if intValue(page["index"]) == index {
			return intValue(page["rotation"]), true
		}
	}
	return 0, false
}
