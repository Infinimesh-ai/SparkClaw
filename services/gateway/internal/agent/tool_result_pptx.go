package agent

import (
	"fmt"
	"strings"
)

func pptxSlideOperationContext(blocks, layoutShapes []any) string {
	lines := []string{
		"PPTXSlideOperationContext:",
		"- slide_index and shape_index are exact 1-based locations from the structured read.",
		"- For pptx.update_slide, copy old_text exactly and update only listed text shapes; do not merge multiple shapes.",
		"- Use layout_policy=coordinated for slide improvement; use preserve only for exact copy edits that must keep geometry.",
	}
	layoutByShape := map[string]map[string]any{}
	for _, item := range layoutShapes {
		shape, ok := anyMap(item)
		if !ok || intLikeValue(shape["group_child_index"]) > 0 {
			continue
		}
		key := fmt.Sprintf("%d:%d", intLikeValue(shape["slide_index"]), intLikeValue(shape["shape_index"]))
		layoutByShape[key] = shape
	}
	shapeCount := 0
	lastSlide := 0
	for _, item := range blocks {
		block, ok := anyMap(item)
		if !ok {
			continue
		}
		location, _ := anyMap(block["location"])
		blockType := strings.TrimSpace(stringValue(firstNonNil(block["type"], block["block_type"], location["block_type"])))
		if blockType != "shape_text" {
			continue
		}
		slideIndex := intLikeValue(firstNonNil(location["slide_index"], location["slideIndex"]))
		shapeIndex := intLikeValue(firstNonNil(location["shape_index"], location["shapeIndex"]))
		text := strings.TrimSpace(stringValue(block["text"]))
		if slideIndex <= 0 || shapeIndex <= 0 || text == "" || text == "<nil>" {
			continue
		}
		if slideIndex != lastSlide {
			lines = append(lines, fmt.Sprintf("slide_index=%d:", slideIndex))
			lastSlide = slideIndex
		}
		fields := []string{fmt.Sprintf("shape_index=%d", shapeIndex), "old_text=" + quoteInline(text)}
		if shape := layoutByShape[fmt.Sprintf("%d:%d", slideIndex, shapeIndex)]; shape != nil {
			style, _ := anyMap(shape["text_style"])
			for _, field := range []struct {
				name  string
				value any
			}{
				{"font_size_pt", style["font_size_pt"]},
				{"capacity_visual_units", style["single_line_capacity_visual_units"]},
				{"fit_ratio", style["single_line_fit_ratio"]},
				{"companion_group", shape["companion_group_id"]},
				{"companion_role", shape["companion_role"]},
			} {
				if usefulStructuredValue(field.value) {
					fields = append(fields, field.name+"="+strings.TrimSpace(stringValue(field.value)))
				}
			}
		}
		lines = append(lines, "  "+strings.Join(fields, " "))
		shapeCount++
	}
	if shapeCount == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}
