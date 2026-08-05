package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

type pptxWorkflowEditEvidence struct {
	document     map[string]any
	slides       map[int]map[string]any
	shapes       map[int]map[int]string
	layoutRefs   map[string]bool
	notesSlides  map[int]bool
	readOnlyText []string
}

func (r Runtime) validatePPTXEditEvidence(run app.AgentRun, operation string, args map[string]any) error {
	evidence, err := r.currentPPTXWorkflowEditEvidence(run, args)
	if err != nil {
		return err
	}
	if err := validatePPTXWorkflowEditBounds(operation, args); err != nil {
		return err
	}
	scope := strings.TrimSpace(run.Workflow.Route.Facts[pptxScopeFact])
	switch operation {
	case "replace_text":
		if scope != pptxScopeExactText {
			return errors.New("pptx.replace_text is outside the frozen PPTX scope")
		}
		for _, value := range anySlice(args["replacements"]) {
			replacement, ok := anyMap(value)
			find := strings.TrimSpace(stringValue(replacement["find"]))
			if !ok || find == "" {
				return errors.New("pptx.replace_text requires non-empty evidence-bound replacement targets")
			}
			for _, text := range evidence.readOnlyText {
				if strings.Contains(text, find) {
					return errors.New("pptx.replace_text target is grouped or non-editable in current workflow evidence")
				}
			}
			found := false
			for _, shapes := range evidence.shapes {
				for _, text := range shapes {
					if strings.Contains(text, find) {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				return errors.New("pptx.replace_text target is stale or absent from current workflow evidence")
			}
		}
	case "update_slide":
		if scope != pptxScopeSingleSlide {
			return errors.New("pptx.update_slide is outside the frozen PPTX scope")
		}
		slideIndex := intLikeValue(args["slide_index"])
		if !containsPPTXSlideIndex(decodePPTXSlideIndexes(run.Workflow.Route.Facts[pptxSlideIndexesFact]), slideIndex) {
			return errors.New("pptx.update_slide conflicts with the frozen owner slide scope")
		}
		return validatePPTXEvidenceUpdates(evidence, slideIndex, anySlice(args["updates"]))
	case "update_deck":
		if scope != pptxScopeWholeDeck {
			return errors.New("pptx.update_deck is outside the frozen PPTX scope")
		}
		if len(evidence.slides) > document.PPTXWholeDeckMaxSlides {
			return errors.New("pptx.update_deck exceeds the frozen whole-deck batch bound")
		}
		seen := map[int]bool{}
		for _, value := range anySlice(args["slide_updates"]) {
			slideUpdate, ok := anyMap(value)
			if !ok {
				return errors.New("pptx.update_deck contains an invalid slide update")
			}
			slideIndex := intLikeValue(slideUpdate["slide_index"])
			if seen[slideIndex] {
				return errors.New("pptx.update_deck contains a duplicate slide index")
			}
			seen[slideIndex] = true
			if err := validatePPTXEvidenceUpdates(evidence, slideIndex, anySlice(slideUpdate["updates"])); err != nil {
				return err
			}
		}
	case "add_slide":
		if scope != pptxScopeStructural {
			return errors.New("pptx.add_slide is outside the frozen PPTX scope")
		}
		layoutRef := strings.TrimSpace(stringValue(args["layout_ref"]))
		templateRef := strings.TrimSpace(stringValue(args["template_slide_ref"]))
		if (layoutRef == "") == (templateRef == "") {
			return errors.New("pptx.add_slide requires exactly one current-read layout or template reference")
		}
		if layoutRef != "" && !evidence.layoutRefs[layoutRef] {
			return errors.New("pptx.add_slide layout_ref is stale or absent from current workflow evidence")
		}
		if after := intLikeValue(args["after_slide_index"]); after > 0 {
			if _, ok := evidence.slides[after]; !ok {
				return errors.New("pptx.add_slide after_slide_index is stale or absent from current workflow evidence")
			}
		}
		if templateRef != "" {
			templateIndex, ok := pptxTemplateSlideIndex(evidence, templateRef)
			if !ok {
				return errors.New("pptx.add_slide template_slide_ref is stale or absent from current workflow evidence")
			}
			if evidence.notesSlides[templateIndex] {
				return errors.New("pptx.add_slide cannot clone a template slide with speaker notes without loss")
			}
			return validatePPTXEvidenceUpdates(evidence, templateIndex, anySlice(args["template_updates"]))
		}
	case "duplicate_slide", "delete_slide":
		if scope != pptxScopeStructural {
			return fmt.Errorf("pptx.%s is outside the frozen PPTX scope", operation)
		}
		slideIndex := intLikeValue(args["slide_index"])
		if _, ok := evidence.slides[slideIndex]; !ok {
			return fmt.Errorf("pptx.%s slide_index is stale or absent from current workflow evidence", operation)
		}
		if operation == "duplicate_slide" && evidence.notesSlides[slideIndex] {
			return errors.New("pptx.duplicate_slide cannot clone a slide with speaker notes without loss")
		}
	}
	return nil
}

func validatePPTXWorkflowEditBounds(operation string, args map[string]any) error {
	slides := 0
	updates := []any{}
	replacementBytes := 0
	switch operation {
	case "replace_text":
		replacements := anySlice(args["replacements"])
		if len(replacements) == 0 {
			return errors.New("pptx.replace_text requires at least one evidence-bound replacement")
		}
		for _, value := range replacements {
			replacement, _ := anyMap(value)
			replacementBytes += len([]byte(stringValue(replacement["replace"])))
		}
	case "update_slide":
		slides = 1
		updates = anySlice(args["updates"])
	case "update_deck":
		slideUpdates := anySlice(args["slide_updates"])
		slides = len(slideUpdates)
		for _, value := range slideUpdates {
			slideUpdate, _ := anyMap(value)
			updates = append(updates, anySlice(slideUpdate["updates"])...)
		}
	case "add_slide":
		slides = 1
		updates = anySlice(args["template_updates"])
		replacementBytes += len([]byte(stringValue(args["title"]))) + len([]byte(stringValue(args["body"])))
	}
	for _, value := range updates {
		update, _ := anyMap(value)
		replacementBytes += len([]byte(stringValue(update["text"])))
	}
	return document.ValidatePPTXEditBounds(slides, len(updates), replacementBytes)
}

func (r Runtime) currentPPTXWorkflowEditEvidence(run app.AgentRun, args map[string]any) (pptxWorkflowEditEvidence, error) {
	if run.Workflow == nil || r.store == nil {
		return pptxWorkflowEditEvidence{}, errors.New("PPTX edit requires current workflow localization evidence")
	}
	state, ok := run.Workflow.Nodes[documentLocateEvidenceNodeID]
	if !ok || state.Status != app.WorkflowNodeSucceeded || len(state.ToolCallIDs) != 1 {
		return pptxWorkflowEditEvidence{}, errors.New("PPTX edit requires one completed current workflow localization read")
	}
	call, ok := r.store.GetToolCall(state.ToolCallIDs[0])
	if !ok || call.RunID != run.ID || call.SessionID != run.SessionID || call.WorkflowID != app.WorkflowDocumentEdit ||
		call.WorkflowNodeID != documentLocateEvidenceNodeID || call.ScopeRevision != state.ScopeRevision ||
		call.Tool != "files.read" || !toolCallCompleted(call) {
		return pptxWorkflowEditEvidence{}, errors.New("PPTX edit localization evidence does not belong to the active workflow run")
	}
	result, ok := anyMap(call.Result)
	if !ok || !sameDocumentReadPath(strings.TrimSpace(stringValue(args["path"])), call, result) {
		return pptxWorkflowEditEvidence{}, errors.New("PPTX edit path does not match current workflow localization evidence")
	}
	document, ok := anyMap(result["document"])
	if !ok || !strings.EqualFold(strings.TrimSpace(stringValue(document["format"])), app.DocumentFormatPPTX) {
		return pptxWorkflowEditEvidence{}, errors.New("PPTX edit requires a completed structured PPTX read")
	}
	evidence := pptxWorkflowEditEvidence{
		document: document, slides: map[int]map[string]any{}, shapes: map[int]map[int]string{},
		layoutRefs: map[string]bool{}, notesSlides: map[int]bool{}, readOnlyText: []string{},
	}
	for _, value := range documentAnySliceFromAny(document["slides"]) {
		slide, ok := anyMap(value)
		index := intLikeValue(slide["index"])
		if ok && index > 0 {
			evidence.slides[index] = slide
		}
	}
	for _, value := range documentAnySliceFromAny(document["blocks"]) {
		block, ok := anyMap(value)
		if !ok {
			continue
		}
		location, _ := anyMap(block["location"])
		format, _ := anyMap(firstNonNil(block["format_metadata"], block["format"]))
		if strings.TrimSpace(stringValue(firstNonNil(block["kind"], block["type"], location["block_type"]))) != "shape_text" {
			continue
		}
		if intLikeValue(location["group_child_index"]) > 0 || (format["editable"] != nil && !boolValue(format["editable"])) {
			if text := stringValue(block["text"]); strings.TrimSpace(text) != "" {
				evidence.readOnlyText = append(evidence.readOnlyText, text)
			}
			continue
		}
		slideIndex := intLikeValue(location["slide_index"])
		shapeIndex := intLikeValue(location["shape_index"])
		text := stringValue(block["text"])
		if slideIndex <= 0 || shapeIndex <= 0 || strings.TrimSpace(text) == "" {
			continue
		}
		if evidence.shapes[slideIndex] == nil {
			evidence.shapes[slideIndex] = map[int]string{}
		}
		evidence.shapes[slideIndex][shapeIndex] = text
	}
	enrichment, _ := anyMap(document["enrichment"])
	layout, _ := anyMap(enrichment["layout"])
	for _, value := range documentAnySliceFromAny(layout["layout_inventory"]) {
		entry, ok := anyMap(value)
		if ref := strings.TrimSpace(stringValue(entry["layout_ref"])); ok && ref != "" {
			evidence.layoutRefs[ref] = true
		}
	}
	annotations, _ := anyMap(enrichment["annotations"])
	for _, value := range documentAnySliceFromAny(annotations["notes"]) {
		note, ok := anyMap(value)
		location, _ := anyMap(note["location"])
		if ok && strings.TrimSpace(stringValue(note["text"])) != "" {
			evidence.notesSlides[intLikeValue(location["slide_index"])] = true
		}
	}
	return evidence, nil
}

func validatePPTXEvidenceUpdates(evidence pptxWorkflowEditEvidence, slideIndex int, updates []any) error {
	if _, ok := evidence.slides[slideIndex]; !ok {
		return fmt.Errorf("PPTX slide %d is stale or absent from current workflow evidence", slideIndex)
	}
	if len(updates) == 0 {
		return fmt.Errorf("PPTX slide %d requires at least one evidence-bound text update", slideIndex)
	}
	seen := map[int]bool{}
	for _, value := range updates {
		update, ok := anyMap(value)
		if !ok {
			return fmt.Errorf("PPTX slide %d contains an invalid text update", slideIndex)
		}
		shapeIndex := intLikeValue(update["shape_index"])
		if shapeIndex <= 0 || seen[shapeIndex] {
			return fmt.Errorf("PPTX slide %d contains an invalid or duplicate shape index", slideIndex)
		}
		seen[shapeIndex] = true
		current, ok := evidence.shapes[slideIndex][shapeIndex]
		if !ok {
			return fmt.Errorf("PPTX slide %d shape %d is grouped, non-editable, stale, or absent from current workflow evidence", slideIndex, shapeIndex)
		}
		if normalizePPTXEvidenceText(stringValue(update["old_text"])) != normalizePPTXEvidenceText(current) {
			return fmt.Errorf("PPTX slide %d shape %d old_text conflicts with current workflow evidence", slideIndex, shapeIndex)
		}
		if strings.TrimSpace(stringValue(update["text"])) == "" {
			return fmt.Errorf("PPTX slide %d shape %d replacement text is empty", slideIndex, shapeIndex)
		}
		if strings.EqualFold(strings.TrimSpace(stringValue(update["mode"])), "exact_span") {
			find := strings.TrimSpace(stringValue(update["find"]))
			if find == "" || strings.Count(current, find) != 1 {
				return fmt.Errorf("PPTX slide %d shape %d exact_span find is absent or ambiguous in current workflow evidence", slideIndex, shapeIndex)
			}
		}
	}
	return nil
}

func pptxTemplateSlideIndex(evidence pptxWorkflowEditEvidence, ref string) (int, bool) {
	for index, slide := range evidence.slides {
		if strings.TrimSpace(stringValue(slide["template_ref"])) == ref {
			return index, true
		}
	}
	return 0, false
}

func containsPPTXSlideIndex(indexes []int, expected int) bool {
	for _, index := range indexes {
		if index == expected {
			return true
		}
	}
	return false
}

func normalizePPTXEvidenceText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func pptxApprovalSummary(name string, args map[string]any) string {
	path := strings.TrimSpace(stringValue(args["path"]))
	switch name {
	case "pptx.replace_text":
		return "替换演示文稿中的明确文本：" + path
	case "pptx.update_slide":
		return fmt.Sprintf("修改演示文稿第 %d 页：%s", intLikeValue(args["slide_index"]), path)
	case "pptx.update_deck":
		indexes := []string{}
		for _, value := range anySlice(args["slide_updates"]) {
			update, ok := anyMap(value)
			if ok && intLikeValue(update["slide_index"]) > 0 {
				indexes = append(indexes, fmt.Sprint(intLikeValue(update["slide_index"])))
			}
		}
		return "修改演示文稿第 " + strings.Join(indexes, "、") + " 页：" + path
	case "pptx.add_slide":
		return fmt.Sprintf("在演示文稿第 %d 页后新增一页：%s", intLikeValue(args["after_slide_index"]), path)
	case "pptx.duplicate_slide":
		return fmt.Sprintf("复制演示文稿第 %d 页：%s", intLikeValue(args["slide_index"]), path)
	case "pptx.delete_slide":
		return fmt.Sprintf("删除演示文稿第 %d 页：%s", intLikeValue(args["slide_index"]), path)
	default:
		return "批准演示文稿修改：" + path
	}
}
