package document

import (
	"encoding/json"
	"fmt"
	"strings"
)

func verifyPPTXExpectedMutation(operation string, before, after Representation, edit EditRequest) (bool, error) {
	switch operation {
	case "update_slide":
		if err := verifyPPTXSlideUpdates(after, intValue(edit.Arguments["slide_index"]), mapSlice(edit.Arguments["updates"])); err != nil {
			return true, err
		}
	case "update_deck":
		for _, slideUpdate := range mapSlice(edit.Arguments["slide_updates"]) {
			if err := verifyPPTXSlideUpdates(after, intValue(slideUpdate["slide_index"]), mapSlice(slideUpdate["updates"])); err != nil {
				return true, err
			}
		}
	case "add_slide":
		if len(after.Slides) != len(before.Slides)+1 {
			return true, fmt.Errorf("the structured slide count did not increase by one")
		}
		insertedIndex := intValue(edit.Arguments["after_slide_index"]) + 1
		if intValue(edit.Arguments["after_slide_index"]) == 0 {
			insertedIndex = len(after.Slides)
		}
		if err := verifyPPTXSlideUpdates(after, insertedIndex, mapSlice(edit.Arguments["template_updates"])); err != nil {
			return true, err
		}
	case "duplicate_slide":
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

func verifyPPTXSlideUpdates(after Representation, slideIndex int, updates []map[string]any) error {
	for _, update := range updates {
		expected := stringValue(update["text"])
		if strings.EqualFold(strings.TrimSpace(stringValue(update["mode"])), "exact_span") {
			beforeText := stringValue(update["old_text"])
			find := stringValue(update["find"])
			expected = strings.Replace(beforeText, find, expected, 1)
		}
		if !slideShapeHasText(after, slideIndex, intValue(update["shape_index"]), expected) {
			return fmt.Errorf("updated slide %d shape %d does not contain the expected after-value", slideIndex, intValue(update["shape_index"]))
		}
	}
	return nil
}

func pptxMutationAllowsBlock(operation string, edit EditRequest, block Block) (bool, bool) {
	if operation != "update_slide" && operation != "update_deck" {
		return false, false
	}
	slideUpdates := []map[string]any{{"slide_index": edit.Arguments["slide_index"], "updates": edit.Arguments["updates"]}}
	if operation == "update_deck" {
		slideUpdates = mapSlice(edit.Arguments["slide_updates"])
	}
	for _, slideUpdate := range slideUpdates {
		if intValue(block.Location["slide_index"]) != intValue(slideUpdate["slide_index"]) {
			continue
		}
		for _, update := range mapSlice(slideUpdate["updates"]) {
			if intValue(block.Location["shape_index"]) == intValue(update["shape_index"]) {
				return true, true
			}
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

func pptxMutationAllowsAnnotationText(edit EditRequest, annotation map[string]any) bool {
	location := mapValue(annotation["location"])
	operation := strings.ToLower(strings.TrimSpace(edit.Operation))
	switch operation {
	case "replace_text":
		// Replacement targets are constrained by structured block matches, and
		// unchanged-content validation still protects every unrelated shape. The
		// hyperlink display text may span only part of a matched run range.
		return strings.HasPrefix(stringValue(location["path"]), "presentation.slide[")
	case "update_slide", "update_deck":
		block := Block{Kind: "shape_text", Location: location}
		allowed, handled := pptxMutationAllowsBlock(operation, edit, block)
		return handled && allowed
	}
	return false
}

func verifyPPTXRichTextPreservation(before, after Representation, edit EditRequest, matches []Match) error {
	if !strings.EqualFold(before.Format, "pptx") {
		return nil
	}
	operation := strings.ToLower(strings.TrimSpace(edit.Operation))
	targets := map[string]bool{}
	switch operation {
	case "replace_text":
		for _, match := range matches {
			targets[stringValue(match.Location["path"])] = true
		}
	case "update_slide", "update_deck":
		for _, block := range before.Blocks {
			if allowed, handled := pptxMutationAllowsBlock(operation, edit, block); handled && allowed {
				targets[stringValue(block.Location["path"])] = true
			}
		}
	default:
		return nil
	}
	afterByPath := map[string]Block{}
	for _, block := range after.Blocks {
		afterByPath[stringValue(block.Location["path"])] = block
	}
	for _, block := range before.Blocks {
		path := stringValue(block.Location["path"])
		if !targets[path] || block.Kind != "shape_text" {
			continue
		}
		other, ok := afterByPath[path]
		if !ok {
			return fmt.Errorf("target PPTX text structure disappeared at %s", path)
		}
		beforeStructure := mapValue(block.Format["text_structure"])
		if len(beforeStructure) == 0 {
			// Representations persisted before PPTX rich-text evidence shipped do not
			// have enough information to prove or disprove run-level preservation.
			continue
		}
		if err := comparePPTXTextStructure(beforeStructure, mapValue(other.Format["text_structure"])); err != nil {
			return fmt.Errorf("target PPTX rich text changed at %s: %w", path, err)
		}
	}
	return nil
}

func comparePPTXTextStructure(before, after map[string]any) error {
	if len(before) == 0 || len(after) == 0 {
		return fmt.Errorf("paragraph and run evidence is unavailable")
	}
	if len(anySlice(before["unsupported"])) > 0 || len(anySlice(after["unsupported"])) > 0 {
		return fmt.Errorf("unsupported text properties are present")
	}
	beforeParagraphs := mapSlice(before["paragraphs"])
	afterParagraphs := mapSlice(after["paragraphs"])
	if len(beforeParagraphs) != len(afterParagraphs) {
		return fmt.Errorf("paragraph count changed")
	}
	for index := range beforeParagraphs {
		beforeParagraph := cloneMap(beforeParagraphs[index])
		afterParagraph := cloneMap(afterParagraphs[index])
		beforeRuns := mapSlice(beforeParagraph["runs"])
		afterRuns := mapSlice(afterParagraph["runs"])
		for _, field := range []string{"path", "text", "runs", "soft_breaks"} {
			delete(beforeParagraph, field)
			delete(afterParagraph, field)
		}
		if !sameJSON(beforeParagraph, afterParagraph) {
			return fmt.Errorf("paragraph properties changed at paragraph %d", index+1)
		}
		beforeStyles := pptxRunStyles(beforeRuns)
		afterStyles := pptxRunStyles(afterRuns)
		if len(afterStyles) < len(beforeStyles) {
			return fmt.Errorf("run count decreased at paragraph %d", index+1)
		}
		for runIndex := range beforeStyles {
			if !sameJSON(beforeStyles[runIndex], afterStyles[runIndex]) {
				return fmt.Errorf("run style changed at paragraph %d run %d", index+1, runIndex+1)
			}
		}
		if len(afterStyles) > len(beforeStyles) {
			if len(beforeStyles) == 0 {
				return fmt.Errorf("new runs appeared without an evidence style at paragraph %d", index+1)
			}
			last := beforeStyles[len(beforeStyles)-1]
			for runIndex := len(beforeStyles); runIndex < len(afterStyles); runIndex++ {
				if !sameJSON(last, afterStyles[runIndex]) {
					return fmt.Errorf("new soft-break run changed style at paragraph %d", index+1)
				}
			}
		}
	}
	return nil
}

func pptxRunStyles(runs []map[string]any) []map[string]any {
	styles := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		style := cloneMap(mapValue(run["style"]))
		raw, _ := json.Marshal(style)
		var normalized map[string]any
		_ = json.Unmarshal(raw, &normalized)
		styles = append(styles, normalized)
	}
	return styles
}

func verifyReportedLayoutChanges(before, after Representation, edit EditRequest, details map[string]any) (map[string]bool, error) {
	allowed := map[string]bool{}
	operation := strings.ToLower(strings.TrimSpace(edit.Operation))
	if operation != "update_slide" && operation != "update_deck" {
		return allowed, nil
	}
	changes := mapSlice(details["layout_changes"])
	indexes := intSlice(details["layout_adjusted_shape_indexes"])
	if len(changes) == 0 && len(indexes) == 0 {
		return allowed, nil
	}
	if operation == "update_slide" && !strings.EqualFold(strings.TrimSpace(stringValue(details["layout_policy"])), "coordinated") {
		return nil, fmt.Errorf("layout changes were reported without coordinated layout_policy")
	}
	slideIndex := intValue(edit.Arguments["slide_index"])
	declared := map[int]bool{}
	declaredTargets := map[string]bool{}
	if operation == "update_deck" {
		for _, target := range mapSlice(details["layout_adjusted_targets"]) {
			key := fmt.Sprintf("%d:%d", intValue(target["slide_index"]), intValue(target["shape_index"]))
			if key == "0:0" || declaredTargets[key] {
				return nil, fmt.Errorf("layout_adjusted_targets contains an invalid or duplicate target")
			}
			declaredTargets[key] = true
		}
	} else {
		for _, index := range indexes {
			if index <= 0 || declared[index] {
				return nil, fmt.Errorf("layout_adjusted_shape_indexes contains an invalid or duplicate shape index")
			}
			declared[index] = true
		}
	}
	for _, change := range changes {
		shapeIndex := intValue(change["shape_index"])
		currentSlide := slideIndex
		if operation == "update_deck" {
			currentSlide = intValue(change["slide_index"])
		}
		if operation == "update_deck" && !declaredTargets[fmt.Sprintf("%d:%d", currentSlide, shapeIndex)] || operation == "update_slide" && !declared[shapeIndex] {
			return nil, fmt.Errorf("layout change for shape %d was not declared in the adjustment allowlist", shapeIndex)
		}
		beforeShape, beforeOK := layoutShape(before.Enrichment, currentSlide, shapeIndex)
		afterShape, afterOK := layoutShape(after.Enrichment, currentSlide, shapeIndex)
		if !beforeOK || !afterOK {
			return nil, fmt.Errorf("layout change shape %d was not present in both structured reads", shapeIndex)
		}
		if !sameJSON(layoutShapeState(beforeShape), normalizedLayoutChangeState(mapValue(change["before"]))) ||
			!sameJSON(layoutShapeState(afterShape), normalizedLayoutChangeState(mapValue(change["after"]))) {
			return nil, fmt.Errorf("layout change for shape %d did not match the re-read geometry and style", shapeIndex)
		}
		allowed[layoutShapeKey(beforeShape)] = true
	}
	declaredCount := len(declared)
	if operation == "update_deck" {
		declaredCount = len(declaredTargets)
	}
	if len(allowed) != declaredCount {
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
