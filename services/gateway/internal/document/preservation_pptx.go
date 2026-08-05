package document

import (
	"fmt"
	"strings"
)

func verifyPPTXExpectedMutation(operation string, before, after Representation, edit EditRequest) (bool, error) {
	switch operation {
	case "update_slide":
		for _, update := range mapSlice(edit.Arguments["updates"]) {
			if !slideShapeHasText(after, intValue(edit.Arguments["slide_index"]), intValue(update["shape_index"]), stringValue(update["text"])) {
				return true, fmt.Errorf("updated slide shape %d does not contain the expected after-value", intValue(update["shape_index"]))
			}
		}
	case "add_slide", "duplicate_slide":
		if len(after.Slides) != len(before.Slides)+1 {
			return true, fmt.Errorf("the structured slide count did not increase by one")
		}
	case "delete_slide":
		if len(after.Slides) != len(before.Slides)-1 {
			return true, fmt.Errorf("the structured slide count did not decrease by one")
		}
	default:
		return false, nil
	}
	return true, nil
}

func pptxMutationAllowsBlock(operation string, edit EditRequest, block Block) (bool, bool) {
	if operation != "update_slide" {
		return false, false
	}
	if intValue(block.Location["slide_index"]) != intValue(edit.Arguments["slide_index"]) {
		return false, true
	}
	for _, update := range mapSlice(edit.Arguments["updates"]) {
		if intValue(block.Location["shape_index"]) == intValue(update["shape_index"]) {
			return true, true
		}
	}
	return false, true
}

func pptxOperationAllowsEvidenceDelta(operation string, before, after []string) (bool, bool) {
	switch operation {
	case "add_slide", "duplicate_slide":
		return multisetContains(after, before), true
	case "delete_slide":
		return multisetContains(before, after), true
	default:
		return false, false
	}
}

func pptxOperationChangesEntityIndexes(operation string) bool {
	switch operation {
	case "add_slide", "duplicate_slide", "delete_slide":
		return true
	default:
		return false
	}
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
