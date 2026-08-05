package document

import (
	"fmt"
	"math"
	"strings"
)

func verifyDOCXExpectedMutation(operation string, before, after Representation, edit EditRequest, matches []Match) (bool, error) {
	switch operation {
	case "replace_paragraph":
		if !blockAtAnyMatchHasText(after, matches, stringValue(edit.Arguments["text"])) {
			return true, fmt.Errorf("the replaced paragraph does not contain the expected after-value")
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
