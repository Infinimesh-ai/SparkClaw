package document

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

func verifyDOCXExpectedMutation(operation string, before, after Representation, edit EditRequest, matches []Match) (bool, error) {
	switch operation {
	case "replace_paragraph":
		if !blockAtAnyMatchHasText(after, matches, stringValue(edit.Arguments["text"])) {
			return true, fmt.Errorf("the replaced paragraph does not contain the expected after-value")
		}
		beforeParagraph, beforeOK := docxParagraphAtMatch(before.Paragraphs, matches, edit)
		afterParagraph, afterOK := docxParagraphAtMatch(after.Paragraphs, matches, edit)
		if !beforeOK || !afterOK || !docxParagraphStructurePreserved(beforeParagraph, afterParagraph) {
			return true, fmt.Errorf("paragraph replacement changed paragraph or run formatting")
		}
	case "insert_paragraph":
		if countBlockText(after.Blocks, stringValue(edit.Arguments["text"])) <= countBlockText(before.Blocks, stringValue(edit.Arguments["text"])) {
			return true, fmt.Errorf("the inserted paragraph was not found in the structured output")
		}
	case "delete_paragraph":
		for _, match := range matches {
			if match.Text != "" && countBlockText(after.Blocks, match.Text) >= countBlockText(before.Blocks, match.Text) {
				return true, fmt.Errorf("the deleted paragraph remains in the structured output")
			}
		}
	case "set_text_style":
		style := mapValue(edit.Arguments["style"])
		if len(style) == 0 {
			return true, fmt.Errorf("the requested style is empty")
		}
		beforeParagraph, ok := docxParagraphAtMatch(before.Paragraphs, matches, edit)
		if !ok {
			return true, fmt.Errorf("the target paragraph is missing from before-edit evidence")
		}
		afterParagraph, ok := docxParagraphAtMatch(after.Paragraphs, matches, edit)
		if !ok {
			return true, fmt.Errorf("the target paragraph is missing from after-edit evidence")
		}
		if builtinStyle, requested := style["builtin_style"]; requested && !strings.EqualFold(strings.TrimSpace(stringValue(afterParagraph["style"])), strings.TrimSpace(stringValue(builtinStyle))) {
			return true, fmt.Errorf("the target paragraph does not have the requested built-in style")
		}
		if bold, requested := style["bold"]; requested && !docxParagraphRunsMatchBool(afterParagraph, "effective_bold", bold) {
			return true, fmt.Errorf("the target paragraph does not have the requested bold value")
		}
		if size, requested := style["font_size_pt"]; requested && !docxParagraphRunsMatchNumber(afterParagraph, "effective_font_size_pt", size) {
			return true, fmt.Errorf("the target paragraph does not have the requested font size")
		}
		if !docxUnrequestedRunFormattingPreserved(beforeParagraph, afterParagraph, style) {
			return true, fmt.Errorf("styling changed unrequested run formatting")
		}
		if !blockAtAnyMatchHasText(after, matches, firstMatchText(matches)) {
			return true, fmt.Errorf("styling unexpectedly changed the target paragraph text")
		}
	default:
		return false, nil
	}
	return true, nil
}

type docxReplacementSpan struct {
	Start       int
	End         int
	Replacement string
}

func verifyDOCXTextReplacementRuns(before, after Representation, edit EditRequest, matches []Match) error {
	beforeByPath := docxParagraphsByPath(before.Paragraphs)
	afterByPath := docxParagraphsByPath(after.Paragraphs)
	checked := map[string]bool{}
	for _, match := range matches {
		path := strings.TrimSpace(stringValue(match.Location["path"]))
		if path == "" || checked[path] {
			continue
		}
		beforeParagraph, beforeOK := beforeByPath[path]
		afterParagraph, afterOK := afterByPath[path]
		if !beforeOK && !afterOK {
			continue
		}
		if !beforeOK || !afterOK {
			return fmt.Errorf("DOCX replacement target paragraph changed identity at %s", path)
		}
		if err := verifyDOCXParagraphTextReplacement(beforeParagraph, afterParagraph, edit); err != nil {
			return fmt.Errorf("DOCX run preservation failed at %s: %w", path, err)
		}
		checked[path] = true
	}
	return nil
}

func verifyDOCXParagraphTextReplacement(before, after map[string]any, edit EditRequest) error {
	if !sameJSON(docxParagraphProperties(before), docxParagraphProperties(after)) {
		return fmt.Errorf("paragraph properties changed")
	}
	beforeRuns := mapSlice(before["runs"])
	afterRuns := mapSlice(after["runs"])
	if len(beforeRuns) == 0 || len(afterRuns) != len(beforeRuns) {
		return fmt.Errorf("run structure changed")
	}
	beforeText := docxParagraphRawText(before, beforeRuns)
	spans, expectedText, err := docxReplacementSpans(beforeText, mapSlice(edit.Arguments["replacements"]))
	if err != nil {
		return err
	}
	if docxParagraphRawText(after, afterRuns) != expectedText {
		return fmt.Errorf("run text does not match the requested replacement")
	}
	affected := docxAffectedRunIndexes(beforeRuns, spans)
	for index := range beforeRuns {
		if !sameJSON(docxRunFormatting(beforeRuns[index]), docxRunFormatting(afterRuns[index])) {
			return fmt.Errorf("run %d formatting or relationship changed", index+1)
		}
		if !affected[index] && stringValue(beforeRuns[index]["text"]) != stringValue(afterRuns[index]["text"]) {
			return fmt.Errorf("unaffected run %d text changed", index+1)
		}
	}
	return nil
}

func docxReplacementSpans(text string, replacements []map[string]any) ([]docxReplacementSpan, string, error) {
	spans := []docxReplacementSpan{}
	for _, replacement := range replacements {
		find := stringValue(replacement["find"])
		if find == "" {
			continue
		}
		for cursor := 0; cursor <= len(text)-len(find); {
			offset := strings.Index(text[cursor:], find)
			if offset < 0 {
				break
			}
			start := cursor + offset
			spans = append(spans, docxReplacementSpan{Start: start, End: start + len(find), Replacement: stringValue(replacement["replace"])})
			cursor = start + len(find)
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].Start != spans[j].Start {
			return spans[i].Start < spans[j].Start
		}
		return spans[i].End < spans[j].End
	})
	for index := 1; index < len(spans); index++ {
		if spans[index].Start < spans[index-1].End {
			return nil, "", fmt.Errorf("replacement spans overlap")
		}
	}
	var expected strings.Builder
	cursor := 0
	for _, span := range spans {
		expected.WriteString(text[cursor:span.Start])
		expected.WriteString(span.Replacement)
		cursor = span.End
	}
	expected.WriteString(text[cursor:])
	return spans, expected.String(), nil
}

func docxAffectedRunIndexes(runs []map[string]any, spans []docxReplacementSpan) map[int]bool {
	affected := map[int]bool{}
	offset := 0
	for _, span := range spans {
		first, last := -1, -1
		offset = 0
		for index, run := range runs {
			text := stringValue(run["text"])
			start, end := offset, offset+len(text)
			if start < span.End && end > span.Start {
				if first < 0 {
					first = index
				}
				last = index
			}
			offset = end
		}
		for index := first; index >= 0 && index <= last; index++ {
			affected[index] = true
		}
	}
	return affected
}

func docxParagraphRawText(paragraph map[string]any, runs []map[string]any) string {
	if raw, ok := paragraph["raw_text"].(string); ok {
		return raw
	}
	var text strings.Builder
	for _, run := range runs {
		text.WriteString(stringValue(run["text"]))
	}
	return text.String()
}

func docxParagraphsByPath(paragraphs []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, paragraph := range paragraphs {
		path := strings.TrimSpace(stringValue(mapValue(paragraph["location"])["path"]))
		if path != "" {
			out[path] = paragraph
		}
	}
	return out
}

func docxParagraphStructurePreserved(before, after map[string]any) bool {
	if !sameJSON(docxParagraphProperties(before), docxParagraphProperties(after)) {
		return false
	}
	beforeRuns := mapSlice(before["runs"])
	afterRuns := mapSlice(after["runs"])
	if len(beforeRuns) != len(afterRuns) {
		return false
	}
	for index := range beforeRuns {
		if !sameJSON(docxRunFormatting(beforeRuns[index]), docxRunFormatting(afterRuns[index])) {
			return false
		}
	}
	return true
}

func docxParagraphProperties(paragraph map[string]any) map[string]any {
	return map[string]any{
		"style": paragraph["style"], "outline_level": paragraph["outline_level"], "list_id": paragraph["list_id"],
		"format": paragraph["format"], "unsupported_boundaries": paragraph["unsupported_boundaries"],
	}
}

func docxRunFormatting(run map[string]any) map[string]any {
	return map[string]any{
		"bold": run["bold"], "italic": run["italic"], "underline": run["underline"],
		"font_name": run["font_name"], "font_size_pt": run["font_size_pt"], "font_color": run["font_color"],
		"effective_bold": run["effective_bold"], "effective_font_size_pt": run["effective_font_size_pt"],
		"relationship_id": run["relationship_id"], "boundaries": run["boundaries"],
	}
}

func docxOperationChangesEntityIndexes(operation string) bool {
	return operation == "insert_paragraph" || operation == "delete_paragraph"
}

func docxParagraphAtMatch(paragraphs []map[string]any, matches []Match, edit EditRequest) (map[string]any, bool) {
	paths := map[string]bool{}
	for _, match := range matches {
		if path := strings.TrimSpace(stringValue(match.Location["path"])); path != "" {
			paths[path] = true
		}
	}
	index := intValue(edit.Arguments["paragraph_index"])
	if location := mapValue(edit.Arguments["location"]); index <= 0 {
		index = intValue(location["paragraph_index"])
	}
	for _, paragraph := range paragraphs {
		location := mapValue(paragraph["location"])
		if paths[strings.TrimSpace(stringValue(location["path"]))] ||
			index > 0 && intValue(paragraph["index"]) == index && strings.EqualFold(stringValue(paragraph["part_kind"]), "body") {
			return paragraph, true
		}
	}
	return nil, false
}

func docxParagraphRunsMatchBool(paragraph map[string]any, field string, expected any) bool {
	wanted, ok := expected.(bool)
	if !ok {
		return false
	}
	matched := false
	for _, run := range mapSlice(paragraph["runs"]) {
		if stringValue(run["text"]) == "" {
			continue
		}
		value, ok := run[field].(bool)
		if !ok || value != wanted {
			return false
		}
		matched = true
	}
	return matched
}

func docxParagraphRunsMatchNumber(paragraph map[string]any, field string, expected any) bool {
	wanted, ok := docxNumber(expected)
	if !ok {
		return false
	}
	matched := false
	for _, run := range mapSlice(paragraph["runs"]) {
		if stringValue(run["text"]) == "" {
			continue
		}
		value, ok := docxNumber(run[field])
		if !ok || math.Abs(value-wanted) > 0.01 {
			return false
		}
		matched = true
	}
	return matched
}

func docxNumber(value any) (float64, bool) {
	switch current := value.(type) {
	case float64:
		return current, true
	case float32:
		return float64(current), true
	case int:
		return float64(current), true
	case int64:
		return float64(current), true
	default:
		return 0, false
	}
}

func docxUnrequestedRunFormattingPreserved(before, after, requested map[string]any) bool {
	beforeRuns := mapSlice(before["runs"])
	afterRuns := mapSlice(after["runs"])
	if len(beforeRuns) != len(afterRuns) {
		return false
	}
	_, boldRequested := requested["bold"]
	_, sizeRequested := requested["font_size_pt"]
	_, builtinRequested := requested["builtin_style"]
	for index := range beforeRuns {
		beforeFormat := docxRunStyleProjection(beforeRuns[index], boldRequested, sizeRequested, builtinRequested)
		afterFormat := docxRunStyleProjection(afterRuns[index], boldRequested, sizeRequested, builtinRequested)
		if !sameJSON(beforeFormat, afterFormat) {
			return false
		}
	}
	return true
}

func docxRunStyleProjection(run map[string]any, boldRequested, sizeRequested, builtinRequested bool) map[string]any {
	projection := map[string]any{}
	for _, field := range []string{"text", "start", "end", "italic", "underline", "font_name", "font_color", "relationship_id", "boundaries"} {
		projection[field] = run[field]
	}
	if !boldRequested {
		projection["bold"] = run["bold"]
		if !builtinRequested {
			projection["effective_bold"] = run["effective_bold"]
		}
	}
	if !sizeRequested {
		projection["font_size_pt"] = run["font_size_pt"]
		if !builtinRequested {
			projection["effective_font_size_pt"] = run["effective_font_size_pt"]
		}
	}
	return projection
}
