package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	documentcontract "github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

const pptxBusinessProjectionSchema = "pptx_business_projection_v1"

const pptxWholeDeckMaxSlideEvidenceBytes = 6 << 10

func pptxSlideOperationContext(blocks, layoutShapes []any) string {
	return pptxScopedOperationContext(blocks, layoutShapes, nil, "", "", nil, 0)
}

func pptxTargetStructuredEvidence(output map[string]any, scope string, targetSlides []int, maxBytes int) (string, error) {
	return pptxTargetStructuredEvidenceForOperation(output, scope, pptxDefaultOperationForScope(scope), targetSlides, maxBytes)
}

func pptxTargetStructuredEvidenceForOperation(output map[string]any, scope, operation string, targetSlides []int, maxBytes int) (string, error) {
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
		operation,
		targetSlides,
		pptxSourceSlideCount(document, len(slides)),
	)
	if strings.TrimSpace(context) == "" {
		return "", errors.New("pptx_target_evidence_missing")
	}
	if operation == "update_deck" && pptxWholeDeckSlideEvidenceExceedsBound(context, maxBytes) {
		return "", errors.New("pptx_slide_evidence_exceeds_budget")
	}
	metadata := map[string]any{
		"projection_schema": pptxBusinessProjectionSchema,
		"untrusted":         true,
		"scope":             scope,
		"operation":         operation,
		"slide_count":       pptxSourceSlideCount(document, len(slides)),
	}
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
		if !ok || slideIndex <= 0 || len(targetSlides) > 0 && !containsPPTXSlideIndex(targetSlides, slideIndex) {
			continue
		}
		record := map[string]any{"slide_index": slideIndex}
		for _, key := range []string{"template_ref", "layout_ref", "layout_name", "layout_part", "has_notes", "target_hash"} {
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
	expectedSlideRecords := len(requiredPPTXSlideIndexes(slides, targetSlides))
	if len(targetSlides) == 0 {
		expectedSlideRecords = len(slides)
	}
	if strings.Count(packed, "shape_record=") != strings.Count(context, "shape_record=") ||
		strings.Count(packed, "slide_record=") != expectedSlideRecords {
		return "", errors.New("pptx_target_evidence_exceeds_budget")
	}
	return packed, nil
}

func pptxWholeDeckSlideEvidenceExceedsBound(context string, maxBytes int) bool {
	limit := pptxWholeDeckMaxSlideEvidenceBytes
	if maxBytes > 0 && maxBytes < limit {
		limit = maxBytes
	}
	currentBytes := 0
	for _, line := range strings.Split(context, "\n") {
		if strings.HasPrefix(line, "slide_index=") {
			if currentBytes > limit {
				return true
			}
			currentBytes = 0
		}
		if currentBytes > 0 || strings.HasPrefix(line, "slide_index=") || strings.HasPrefix(line, "shape_record=") {
			currentBytes += len([]byte(line)) + 1
		}
	}
	return currentBytes > limit
}

func pptxDefaultOperationForScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case pptxScopeSingleSlide:
		return "update_slide"
	case pptxScopeWholeDeck:
		return "update_deck"
	case pptxScopeExactText:
		return "replace_text"
	case pptxScopeStructural:
		return "structural"
	default:
		return ""
	}
}

func pptxSelectedOperation(run app.AgentRun) string {
	if run.Workflow == nil {
		return ""
	}
	state, ok := run.Workflow.Nodes["select_edit_operation"]
	if !ok || state.Status != app.WorkflowNodeSucceeded {
		return ""
	}
	for _, ref := range state.OutcomeRefs {
		if ref.Kind == "tool_directory_entry" {
			return strings.TrimSpace(ref.Attributes[app.CapabilityQualifierOperation])
		}
	}
	return ""
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

func pptxScopedOperationContext(blocks, layoutShapes, slideLayouts []any, scope, operation string, targetSlides []int, slideCount int) string {
	lines := []string{
		"PPTXSlideOperationContext:",
		"- slide_index and shape_index are exact 1-based locations from the structured read.",
		"- For PPTX text updates, return only shape_index and replacement text (plus an optional mode/find/break_mode); Runtime binds old_text from current evidence.",
		"- Return at most 16 selected text updates; coordinated layout may retain current_text when a selected shape needs layout-only adjustment.",
		"- Update only listed text shapes; do not merge multiple shapes or return Runtime-owned path, output_path, source hash, or single-slide index fields.",
		"- Use layout_policy=coordinated for slide improvement; use preserve only for exact copy edits that must keep geometry.",
		"- Blocks marked editable=false or group_child_index>0 are read-only and must never enter editor arguments.",
	}
	if scope != "" {
		lines = append(lines, fmt.Sprintf("projection_schema=%s scope=%s operation=%s slide_count=%d target_slides=%v processing_unit=slide", pptxBusinessProjectionSchema, scope, operation, slideCount, targetSlides))
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
	includeShapeSlide := includeSlide
	if operation == "add_slide" && len(targetSlides) > 1 {
		templateSlide := targetSlides[len(targetSlides)-1]
		includeShapeSlide = func(index int) bool { return index == templateSlide }
	}
	includeLayouts := operation == "add_slide" || operation == "structural" || operation == ""
	for _, item := range slideLayouts {
		if !includeLayouts {
			break
		}
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
		if slideIndex <= 0 || shapeIndex <= 0 || text == "" || text == "<nil>" || !includeShapeSlide(slideIndex) ||
			intLikeValue(location["group_child_index"]) > 0 || (format["editable"] != nil && !boolValue(format["editable"])) {
			continue
		}
		if operation == "duplicate_slide" || operation == "delete_slide" {
			continue
		}
		record := map[string]any{"slide_index": slideIndex, "shape_index": shapeIndex, "current_text": text, "editable": true}
		if usefulStructuredValue(block["target_hash"]) {
			record["target_hash"] = block["target_hash"]
		}
		includeShapeLayout := operation == "update_slide" || operation == "update_deck" || operation == "add_slide" || operation == "structural" || operation == ""
		if shape := layoutByShape[fmt.Sprintf("%d:%d", slideIndex, shapeIndex)]; includeShapeLayout && shape != nil {
			style, _ := anyMap(shape["text_style"])
			for key, value := range map[string]any{
				"font_size_pt": style["font_size_pt"], "capacity_visual_units": style["single_line_capacity_visual_units"],
				"fit_ratio": style["single_line_fit_ratio"], "companion_group": shape["companion_group_id"],
				"companion_role": shape["companion_role"],
			} {
				if usefulStructuredValue(value) {
					record[key] = value
				}
			}
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

func (r Runtime) projectPPTXLocalizationPersistence(runID string, call app.ToolCall, output any) any {
	if call.WorkflowID != app.WorkflowDocumentEdit || call.WorkflowNodeID != documentLocateEvidenceNodeID || call.Tool != "files.read" || r.store == nil {
		return output
	}
	run, ok := r.store.GetRun(runID)
	if !ok || run.Workflow == nil || !strings.EqualFold(firstNonEmptyString(run.Workflow.Route.Facts["document_format"], run.Workflow.Route.Slots.Format), app.DocumentFormatPPTX) {
		return output
	}
	outputMap, ok := outputAsMap(output)
	if !ok {
		return output
	}
	projected, ok := pptxBusinessProjectionResult(
		outputMap,
		strings.TrimSpace(run.Workflow.Route.Facts[pptxScopeFact]),
		decodePPTXSlideIndexes(run.Workflow.Route.Facts[pptxSlideIndexesFact]),
	)
	if !ok {
		return output
	}
	return projected
}

func pptxBusinessProjectionResult(output map[string]any, scope string, targetSlides []int) (map[string]any, bool) {
	document, ok := anyMap(output["document"])
	if !ok || !strings.EqualFold(strings.TrimSpace(stringValue(document["format"])), app.DocumentFormatPPTX) {
		return nil, false
	}
	targetSet := map[int]bool{}
	for _, index := range targetSlides {
		if index > 0 {
			targetSet[index] = true
		}
	}
	includeSlide := func(index int) bool {
		if scope == pptxScopeWholeDeck || scope == pptxScopeExactText || scope == pptxScopeStructural && len(targetSet) == 0 {
			return true
		}
		return targetSet[index]
	}
	notesSlides := map[int]bool{}
	enrichment, _ := anyMap(document["enrichment"])
	annotations, _ := anyMap(enrichment["annotations"])
	for _, value := range documentAnySliceFromAny(annotations["notes"]) {
		note, ok := anyMap(value)
		location, _ := anyMap(note["location"])
		if ok && strings.TrimSpace(stringValue(note["text"])) != "" {
			notesSlides[intLikeValue(location["slide_index"])] = true
		}
	}
	projectedSlides := []any{}
	for _, value := range documentAnySliceFromAny(document["slides"]) {
		slide, ok := anyMap(value)
		index := intLikeValue(slide["index"])
		if !ok || index <= 0 || !includeSlide(index) {
			continue
		}
		record := map[string]any{"index": index, "has_notes": notesSlides[index]}
		for _, key := range []string{"template_ref", "layout_ref", "layout_name", "layout_part"} {
			if usefulStructuredValue(slide[key]) {
				record[key] = slide[key]
			}
		}
		record["target_hash"] = pptxTargetHash(index, 0, stringValue(record["template_ref"]), stringValue(record["layout_ref"]), fmt.Sprint(notesSlides[index]))
		projectedSlides = append(projectedSlides, record)
	}
	projectedBlocks := []any{}
	projectedShapeKeys := map[string]bool{}
	for _, value := range documentAnySliceFromAny(document["blocks"]) {
		block, ok := anyMap(value)
		if !ok {
			continue
		}
		location, _ := anyMap(block["location"])
		blockType := strings.TrimSpace(stringValue(firstNonNil(block["kind"], block["type"], location["block_type"])))
		slideIndex := intLikeValue(location["slide_index"])
		shapeIndex := intLikeValue(location["shape_index"])
		if blockType != "shape_text" || slideIndex <= 0 || shapeIndex <= 0 || !includeSlide(slideIndex) {
			continue
		}
		if scope == pptxScopeStructural && len(targetSet) == 0 {
			continue
		}
		format, _ := anyMap(firstNonNil(block["format_metadata"], block["format"]))
		editable := intLikeValue(location["group_child_index"]) == 0 && (format["editable"] == nil || boolValue(format["editable"]))
		if !editable && scope != pptxScopeExactText {
			continue
		}
		text := stringValue(block["text"])
		if strings.TrimSpace(text) == "" || text == "<nil>" {
			continue
		}
		projectedLocation := map[string]any{"slide_index": slideIndex, "shape_index": shapeIndex, "block_type": "shape_text"}
		for _, key := range []string{"path", "group_child_index"} {
			if usefulStructuredValue(location[key]) {
				projectedLocation[key] = location[key]
			}
		}
		record := map[string]any{
			"kind": "shape_text", "text": text, "location": projectedLocation,
			"format_metadata": map[string]any{"editable": editable},
			"target_hash":     pptxTargetHash(slideIndex, shapeIndex, text, fmt.Sprint(editable)),
		}
		projectedBlocks = append(projectedBlocks, record)
		if editable {
			projectedShapeKeys[fmt.Sprintf("%d:%d", slideIndex, shapeIndex)] = true
		}
	}
	layout, _ := anyMap(enrichment["layout"])
	projectedShapes := []any{}
	includeShapeLayout := scope == pptxScopeSingleSlide || scope == pptxScopeWholeDeck || scope == pptxScopeStructural
	for _, value := range documentAnySliceFromAny(layout["shapes"]) {
		if !includeShapeLayout {
			break
		}
		shape, ok := anyMap(value)
		if !ok || !projectedShapeKeys[fmt.Sprintf("%d:%d", intLikeValue(shape["slide_index"]), intLikeValue(shape["shape_index"]))] {
			continue
		}
		record := map[string]any{
			"slide_index": shape["slide_index"], "shape_index": shape["shape_index"],
			"editable": true,
		}
		for _, key := range []string{"companion_group_id", "companion_role"} {
			if usefulStructuredValue(shape[key]) {
				record[key] = shape[key]
			}
		}
		style, _ := anyMap(shape["text_style"])
		styleRecord := map[string]any{}
		for _, key := range []string{"font_size_pt", "single_line_capacity_visual_units", "single_line_fit_ratio"} {
			if usefulStructuredValue(style[key]) {
				styleRecord[key] = style[key]
			}
		}
		if len(styleRecord) > 0 {
			record["text_style"] = styleRecord
		}
		projectedShapes = append(projectedShapes, record)
	}
	projectedLayouts := []any{}
	if scope == pptxScopeStructural {
		for _, value := range firstDocumentSlice(layout["layout_inventory"], layout["slide_layouts"]) {
			entry, ok := anyMap(value)
			if !ok {
				continue
			}
			record := map[string]any{}
			for _, key := range []string{"layout_ref", "name", "part_name", "placeholder_roles"} {
				if usefulStructuredValue(entry[key]) {
					record[key] = entry[key]
				}
			}
			if len(record) > 0 {
				projectedLayouts = append(projectedLayouts, record)
			}
		}
	}
	metadata, _ := anyMap(document["metadata"])
	projectedMetadata := map[string]any{}
	for _, key := range []string{"sha256", "relative_path", "format"} {
		if usefulStructuredValue(metadata[key]) {
			projectedMetadata[key] = metadata[key]
		}
	}
	slideCount := len(documentAnySliceFromAny(document["slides"]))
	projectedDocument := map[string]any{
		"format":   app.DocumentFormatPPTX,
		"metadata": projectedMetadata,
		"slides":   projectedSlides,
		"blocks":   projectedBlocks,
		"projection": map[string]any{
			"schema": pptxBusinessProjectionSchema, "scope": scope,
			"operations": pptxScopeOperations(scope), "slide_count": slideCount,
			"projected_slide_count": len(projectedSlides), "projected_shape_count": len(projectedBlocks),
		},
		"enrichment": map[string]any{"layout": map[string]any{"shapes": projectedShapes, "layout_inventory": projectedLayouts}},
	}
	projectedOutput := map[string]any{"projection_schema": pptxBusinessProjectionSchema, "document": projectedDocument}
	for _, key := range []string{"status", "path", "rel_path", "kind", "source_bytes", "bytes", "untrusted"} {
		if usefulStructuredValue(output[key]) {
			projectedOutput[key] = output[key]
		}
	}
	return projectedOutput, true
}

func pptxScopeOperations(scope string) []string {
	switch scope {
	case pptxScopeSingleSlide:
		return []string{"update_slide"}
	case pptxScopeWholeDeck:
		return []string{"update_deck"}
	case pptxScopeExactText:
		return []string{"replace_text"}
	case pptxScopeStructural:
		return []string{"add_slide", "duplicate_slide", "delete_slide"}
	default:
		return nil
	}
}

func pptxSourceSlideCount(document map[string]any, fallback int) int {
	projection, _ := anyMap(document["projection"])
	if count := intLikeValue(projection["slide_count"]); count > 0 {
		return count
	}
	return fallback
}

func pptxTargetHash(slideIndex, shapeIndex int, values ...string) string {
	payload := fmt.Sprintf("%d\x00%d\x00%s", slideIndex, shapeIndex, strings.Join(values, "\x00"))
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}
