package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	documentcontract "github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func pptxSlideOperationContext(blocks, layoutShapes []any) string {
	return pptxScopedOperationContext(blocks, layoutShapes, nil, "", nil, 0)
}

func pptxTargetStructuredEvidence(output map[string]any, scope string, targetSlides []int, maxBytes int) (string, error) {
	document, ok := anyMap(output["document"])
	if !ok || !strings.EqualFold(strings.TrimSpace(stringValue(document["format"])), "pptx") {
		return "", nil
	}
	slides := documentAnySliceFromAny(document["slides"])
	if scope == pptxScopeWholeDeck {
		if len(slides) > documentcontract.PPTXWholeDeckMaxSlides {
			return "", errors.New("pptx_whole_deck_exceeds_batch_bound")
		}
		targetSlides = targetSlides[:0]
		for _, value := range slides {
			slide, ok := anyMap(value)
			if ok && intLikeValue(slide["index"]) > 0 {
				targetSlides = append(targetSlides, intLikeValue(slide["index"]))
			}
		}
	}
	enrichment, _ := anyMap(document["enrichment"])
	layout, _ := anyMap(enrichment["layout"])
	context := pptxScopedOperationContext(
		documentAnySliceFromAny(document["blocks"]),
		documentAnySliceFromAny(layout["shapes"]),
		firstDocumentSlice(layout["layout_inventory"], layout["slide_layouts"]),
		scope,
		targetSlides,
		len(slides),
	)
	if strings.TrimSpace(context) == "" {
		return "", errors.New("pptx_target_evidence_missing")
	}
	metadata := map[string]any{"untrusted": true, "scope": scope, "slide_count": len(slides)}
	for _, key := range []string{"path", "rel_path", "kind", "source_bytes", "bytes"} {
		if value, exists := output[key]; exists && usefulStructuredValue(value) {
			metadata[key] = value
		}
	}
	if documentMetadata, ok := anyMap(document["metadata"]); ok {
		for _, key := range []string{"sha256", "relative_path", "format"} {
			if value, exists := documentMetadata[key]; exists && usefulStructuredValue(value) {
				metadata[key] = value
			}
		}
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	required := []string{string(raw)}
	for _, value := range slides {
		slide, ok := anyMap(value)
		slideIndex := intLikeValue(slide["index"])
		if !ok || slideIndex <= 0 || !containsPPTXSlideIndex(targetSlides, slideIndex) {
			continue
		}
		record := map[string]any{"slide_index": slideIndex}
		for _, key := range []string{"template_ref", "layout_ref", "layout_name", "layout_part"} {
			if value, exists := slide[key]; exists && usefulStructuredValue(value) {
				record[key] = value
			}
		}
		if slideRaw, marshalErr := json.Marshal(record); marshalErr == nil {
			required = append(required, "slide_record="+string(slideRaw))
		}
	}
	optional := []string{}
	for _, line := range strings.Split(context, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "layout_record=") {
			optional = append(optional, line)
		} else {
			required = append(required, line)
		}
	}
	if len([]byte(strings.Join(required, "\n"))) > maxBytes {
		return "", errors.New("pptx_target_evidence_exceeds_budget")
	}
	packed := packWholeEvidenceLines(append(required, optional...), maxBytes)
	if strings.Count(packed, "shape_record=") != strings.Count(context, "shape_record=") ||
		strings.Count(packed, "slide_record=") != len(requiredPPTXSlideIndexes(slides, targetSlides)) {
		return "", errors.New("pptx_target_evidence_exceeds_budget")
	}
	return packed, nil
}

func requiredPPTXSlideIndexes(slides []any, targetSlides []int) []int {
	wanted := map[int]bool{}
	for _, value := range targetSlides {
		wanted[value] = true
	}
	indexes := []int{}
	for _, value := range slides {
		slide, ok := anyMap(value)
		index := intLikeValue(slide["index"])
		if ok && index > 0 && wanted[index] {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func firstDocumentSlice(values ...any) []any {
	for _, value := range values {
		if items := documentAnySliceFromAny(value); len(items) > 0 {
			return items
		}
	}
	return nil
}

func pptxScopedOperationContext(blocks, layoutShapes, slideLayouts []any, scope string, targetSlides []int, slideCount int) string {
	lines := []string{
		"PPTXSlideOperationContext:",
		"- slide_index and shape_index are exact 1-based locations from the structured read.",
		"- For pptx.update_slide, copy old_text exactly and update only listed text shapes; do not merge multiple shapes.",
		"- Use layout_policy=coordinated for slide improvement; use preserve only for exact copy edits that must keep geometry.",
		"- Blocks marked editable=false or group_child_index>0 are read-only and must never enter editor arguments.",
	}
	if scope != "" {
		lines = append(lines, fmt.Sprintf("scope=%s slide_count=%d target_slides=%v", scope, slideCount, targetSlides))
	}
	targetSet := map[int]bool{}
	for _, index := range targetSlides {
		if index > 0 {
			targetSet[index] = true
		}
	}
	includeSlide := func(index int) bool {
		return len(targetSet) == 0 || targetSet[index]
	}
	for _, item := range slideLayouts {
		layout, ok := anyMap(item)
		if !ok {
			continue
		}
		record := map[string]any{}
		for _, key := range []string{"layout_ref", "name", "part_name", "placeholder_roles", "representative_slide_refs"} {
			if value, exists := layout[key]; exists && usefulStructuredValue(value) {
				record[key] = value
			}
		}
		if raw, err := json.Marshal(record); err == nil && len(record) > 0 {
			lines = append(lines, "layout_record="+string(raw))
		}
	}
	layoutByShape := map[string]map[string]any{}
	for _, item := range layoutShapes {
		shape, ok := anyMap(item)
		if !ok || intLikeValue(shape["group_child_index"]) > 0 || (shape["editable"] != nil && !boolValue(shape["editable"])) {
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
		format, _ := anyMap(firstNonNil(block["format_metadata"], block["format"]))
		if slideIndex <= 0 || shapeIndex <= 0 || text == "" || text == "<nil>" || !includeSlide(slideIndex) ||
			intLikeValue(location["group_child_index"]) > 0 || (format["editable"] != nil && !boolValue(format["editable"])) {
			continue
		}
		record := map[string]any{"slide_index": slideIndex, "shape_index": shapeIndex, "old_text": text, "editable": true}
		if shape := layoutByShape[fmt.Sprintf("%d:%d", slideIndex, shapeIndex)]; shape != nil {
			style, _ := anyMap(shape["text_style"])
			for key, value := range map[string]any{
				"font_size_pt": style["font_size_pt"], "capacity_visual_units": style["single_line_capacity_visual_units"],
				"fit_ratio": style["single_line_fit_ratio"], "companion_group": shape["companion_group_id"],
				"companion_role": shape["companion_role"], "text_structure": shape["text_structure"],
			} {
				if usefulStructuredValue(value) {
					record[key] = value
				}
			}
		}
		if len(format) > 0 && record["text_structure"] == nil && usefulStructuredValue(format["text_structure"]) {
			record["text_structure"] = format["text_structure"]
		}
		if slideIndex != lastSlide {
			lines = append(lines, fmt.Sprintf("slide_index=%d:", slideIndex))
			lastSlide = slideIndex
		}
		if scope == "" {
			fields := []string{fmt.Sprintf("shape_index=%d", shapeIndex), "old_text=" + quoteInline(text)}
			for _, key := range []string{"font_size_pt", "capacity_visual_units", "fit_ratio", "companion_group", "companion_role"} {
				if usefulStructuredValue(record[key]) {
					fields = append(fields, key+"="+strings.TrimSpace(stringValue(record[key])))
				}
			}
			lines = append(lines, "  "+strings.Join(fields, " "))
		} else if raw, err := json.Marshal(record); err == nil {
			lines = append(lines, "shape_record="+string(raw))
		}
		shapeCount++
	}
	if shapeCount == 0 && scope != pptxScopeStructural {
		return ""
	}
	return strings.Join(lines, "\n")
}
