package document

import "fmt"

func verifyPDFExpectedMutation(operation string, before, after Representation, edit EditRequest) (bool, error) {
	switch operation {
	case "extract_pages":
		if len(after.Pages) != len(intSlice(edit.Arguments["pages"])) {
			return true, fmt.Errorf("the extracted PDF page count does not match the request")
		}
	case "delete_pages":
		if len(after.Pages) != len(before.Pages)-len(intSlice(edit.Arguments["pages"])) {
			return true, fmt.Errorf("the deleted PDF page count does not match the request")
		}
	case "split":
		if len(after.Pages) != 1 {
			return true, fmt.Errorf("each split PDF output must contain exactly one page")
		}
	case "rotate_pages":
		if len(after.Pages) != len(before.Pages) {
			return true, fmt.Errorf("rotating pages unexpectedly changed the PDF page count")
		}
		rotation := intValue(edit.Arguments["rotation"])
		for _, pageIndex := range intSlice(edit.Arguments["pages"]) {
			beforeRotation, beforeOK := pageRotation(before.Pages, pageIndex)
			afterRotation, afterOK := pageRotation(after.Pages, pageIndex)
			if !beforeOK || !afterOK || ((beforeRotation+rotation)%360+360)%360 != ((afterRotation%360)+360)%360 {
				return true, fmt.Errorf("page %d does not have the requested rotation", pageIndex)
			}
		}
	default:
		return false, nil
	}
	return true, nil
}

func pdfOperationAllowsEvidenceDelta(operation string, before, after []string) (bool, bool) {
	switch operation {
	case "extract_pages", "delete_pages", "split":
		return multisetContains(before, after), true
	default:
		return false, false
	}
}

func pdfOperationChangesEntityIndexes(operation string) bool {
	switch operation {
	case "extract_pages", "delete_pages", "split":
		return true
	default:
		return false
	}
}

func pageRotation(pages []map[string]any, index int) (int, bool) {
	for _, page := range pages {
		if intValue(page["index"]) == index {
			return intValue(page["rotation"]), true
		}
	}
	return 0, false
}
