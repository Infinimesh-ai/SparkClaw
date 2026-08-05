package document

import (
	"fmt"
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
		index := intValue(edit.Arguments["paragraph_index"])
		style := stringValue(edit.Arguments["style"])
		if styleObject := mapValue(edit.Arguments["style"]); len(styleObject) > 0 {
			style = firstString(styleObject["builtin_style"], styleObject["style_name"])
		}
		if !paragraphHasStyle(after.Paragraphs, index, style) {
			return true, fmt.Errorf("the target paragraph does not have the requested style")
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

func paragraphHasStyle(paragraphs []map[string]any, index int, style string) bool {
	for _, paragraph := range paragraphs {
		if intValue(paragraph["index"]) == index && strings.EqualFold(strings.TrimSpace(stringValue(paragraph["style"])), strings.TrimSpace(style)) {
			return true
		}
	}
	return false
}
