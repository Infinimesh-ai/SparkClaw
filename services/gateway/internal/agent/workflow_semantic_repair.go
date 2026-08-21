package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const workflowSemanticRepairSchema = "workflow_semantic_repair_request_v1"

type workflowSemanticRepairRequest struct {
	SchemaVersion               string   `json:"schema_version"`
	ProjectionID                string   `json:"projection_id"`
	InvalidOutputDigest         string   `json:"invalid_output_digest"`
	ErrorCodes                  []string `json:"error_codes"`
	InvalidItemIndexes          []int    `json:"invalid_item_indexes,omitempty"`
	InvalidOutput               any      `json:"invalid_output"`
	OriginalOutputSchemaVersion string   `json:"original_output_schema_version"`
	RepairAttempt               int      `json:"repair_attempt"`
}

type workflowSemanticValidationError struct {
	Codes       []string
	ItemIndexes []int
	Digest      string
}

func (e *workflowSemanticValidationError) Error() string {
	return "workflow semantic output failed deterministic validation: " + strings.Join(e.Codes, ",")
}

func (r Runtime) prepareWorkflowSemanticPlan(ctx context.Context, runID string, plan toolPlan) (toolPlan, *workflowSemanticValidationError, error) {
	prepared, err := r.materializeWorkflowBoundArguments(ctx, runID, plan)
	if err != nil {
		return toolPlan{}, nil, err
	}
	definition, ok := r.tools.Definition(prepared.Name)
	if !ok {
		return prepared, nil, nil
	}
	prepared.Args, err = r.bindWorkflowToolArguments(ctx, runID, prepared)
	if err != nil {
		return toolPlan{}, nil, err
	}
	if prepared.WorkflowID != app.WorkflowDocumentEdit {
		return prepared, nil, nil
	}
	run, ok, err := r.store.GetRun(ctx, runID)
	if err != nil {
		return toolPlan{}, nil, err
	}
	if !ok || run.Workflow == nil {
		return prepared, nil, nil
	}
	_, format, operation, registered := agentDocumentOperationForPlan(run, definition, prepared)
	if !registered || format != app.DocumentFormatPPTX {
		return prepared, nil, nil
	}
	inputMutationItems := pptxSemanticMutationItemCount(operation, prepared.Args)
	normalized, validationErr := normalizePPTXSemanticMutation(operation, prepared.Args)
	prepared.Args = normalized
	if validationErr != nil {
		validationErr.Digest = workflowSemanticOutputDigest(plan.Args)
		return prepared, validationErr, nil
	}
	effectiveMutationItems := pptxSemanticMutationItemCount(operation, prepared.Args)
	if inputMutationItems > effectiveMutationItems {
		r.store.AddAudit(app.AuditEvent{
			SessionID: run.SessionID, RunID: run.ID, Actor: "runtime",
			Type: "workflow.semantic_output.normalized", Summary: "Removed non-mutating PPTX items before preflight",
			Fields: map[string]any{
				"operation": operation, "input_item_count": inputMutationItems,
				"effective_item_count": effectiveMutationItems, "dropped_item_count": inputMutationItems - effectiveMutationItems,
			},
		})
	}
	if err := r.tools.Validate(prepared.Name, prepared.Args); err != nil {
		return prepared, nil, fmt.Errorf("PPTX mutation preflight validation failed: %w", err)
	}
	if err := r.validateWorkflowToolPlan(ctx, runID, prepared, definition); err != nil {
		return prepared, nil, fmt.Errorf("PPTX mutation preflight validation failed: %w", err)
	}
	if decision := r.policy.Decide(definition, prepared.Args); !decision.Allowed {
		return prepared, nil, fmt.Errorf("PPTX mutation preflight blocked by Policy: %s", decision.Reason)
	}
	if err := r.tools.PreflightPPTXLayout(ctx, prepared.Name, prepared.Args, run.SessionID); err != nil {
		if app.ToolErrorCodeFrom(err) == app.ToolErrorPPTXLayoutFitConflict {
			return prepared, &workflowSemanticValidationError{
				Codes:  []string{string(app.ToolErrorPPTXLayoutFitConflict)},
				Digest: workflowSemanticOutputDigest(plan.Args),
			}, nil
		}
		return prepared, nil, fmt.Errorf("PPTX mutation preflight failed: %w", err)
	}
	return prepared, nil, nil
}

func normalizePPTXSemanticMutation(operation string, args map[string]any) (map[string]any, *workflowSemanticValidationError) {
	switch operation {
	case "update_slide":
		updates, validationErr := normalizePPTXTextUpdates(anySlice(args["updates"]), 0)
		args["updates"] = updates
		return args, validationErr
	case "update_deck":
		codes := []string{}
		indexes := []int{}
		effective := 0
		ordinal := 0
		for _, raw := range anySlice(args["slide_updates"]) {
			slide, ok := anyMap(raw)
			if !ok {
				codes = appendUniqueString(codes, "invalid_mutation_item")
				indexes = append(indexes, ordinal)
				ordinal++
				continue
			}
			rawUpdates := anySlice(slide["updates"])
			updates, validationErr := normalizePPTXTextUpdates(rawUpdates, ordinal)
			slide["updates"] = updates
			effective += len(updates)
			if validationErr != nil {
				for _, code := range validationErr.Codes {
					codes = appendUniqueString(codes, code)
				}
				indexes = append(indexes, validationErr.ItemIndexes...)
			}
			ordinal += len(rawUpdates)
		}
		if effective == 0 && len(codes) == 0 {
			codes = appendUniqueString(codes, "no_effective_mutation")
		}
		if len(codes) > 0 {
			return args, &workflowSemanticValidationError{Codes: codes, ItemIndexes: indexes}
		}
	case "replace_text":
		kept := []any{}
		codes := []string{}
		indexes := []int{}
		for index, raw := range anySlice(args["replacements"]) {
			replacement, ok := anyMap(raw)
			if !ok {
				codes = appendUniqueString(codes, "invalid_mutation_item")
				indexes = append(indexes, index)
				continue
			}
			find := strings.TrimSpace(stringValue(replacement["find"]))
			replace := strings.TrimSpace(stringValue(replacement["replace"]))
			if replace == "" {
				codes = appendUniqueString(codes, "replacement_text_empty")
				indexes = append(indexes, index)
				continue
			}
			if normalizePPTXEvidenceText(find) == normalizePPTXEvidenceText(replace) {
				continue
			}
			kept = append(kept, replacement)
		}
		args["replacements"] = kept
		if len(kept) == 0 && !containsString(codes, "replacement_text_empty") {
			codes = appendUniqueString(codes, "no_effective_mutation")
		}
		if len(codes) > 0 {
			return args, &workflowSemanticValidationError{Codes: codes, ItemIndexes: indexes}
		}
	}
	return args, nil
}

func normalizePPTXTextUpdates(updates []any, offset int) ([]any, *workflowSemanticValidationError) {
	kept := make([]any, 0, len(updates))
	codes := []string{}
	indexes := []int{}
	cosmeticIndexes := []int{}
	seenShapes := map[int]bool{}
	for index, raw := range updates {
		update, ok := anyMap(raw)
		if !ok {
			codes = appendUniqueString(codes, "invalid_mutation_item")
			indexes = append(indexes, offset+index)
			continue
		}
		shapeIndex := intLikeValue(update["shape_index"])
		if shapeIndex <= 0 || seenShapes[shapeIndex] {
			codes = appendUniqueString(codes, "invalid_or_duplicate_target")
			indexes = append(indexes, offset+index)
			continue
		}
		seenShapes[shapeIndex] = true
		if alias, exists := update["replacement_text"]; exists {
			aliasText := strings.TrimSpace(stringValue(alias))
			canonicalText := strings.TrimSpace(stringValue(update["text"]))
			delete(update, "replacement_text")
			switch {
			case (canonicalText == "" || canonicalText == "<nil>") && aliasText != "" && aliasText != "<nil>":
				update["text"] = alias
			case aliasText != "" && aliasText != "<nil>" && normalizePPTXEvidenceText(canonicalText) != normalizePPTXEvidenceText(aliasText):
				codes = appendUniqueString(codes, "conflicting_replacement_fields")
				indexes = append(indexes, offset+index)
				continue
			}
		}
		text := strings.TrimSpace(stringValue(update["text"]))
		if text == "" || text == "<nil>" {
			codes = appendUniqueString(codes, "replacement_text_empty")
			indexes = append(indexes, offset+index)
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(stringValue(update["mode"])))
		oldText := strings.TrimSpace(stringValue(update["old_text"]))
		comparisonText := oldText
		if mode == "exact_span" {
			comparisonText = stringValue(update["find"])
			if normalizePPTXEvidenceText(comparisonText) == normalizePPTXEvidenceText(text) {
				continue
			}
		} else if normalizePPTXEvidenceText(oldText) == normalizePPTXEvidenceText(text) {
			continue
		}
		if pptxSemanticContentKey(comparisonText) == pptxSemanticContentKey(text) {
			cosmeticIndexes = append(cosmeticIndexes, offset+index)
			continue
		}
		kept = append(kept, update)
	}
	if len(cosmeticIndexes) > 0 && (len(kept) == 0 || len(codes) > 0) {
		codes = appendUniqueString(codes, "cosmetic_only_change")
		indexes = append(indexes, cosmeticIndexes...)
	}
	if len(kept) == 0 && len(codes) == 0 {
		codes = appendUniqueString(codes, "no_effective_mutation")
	}
	if len(codes) > 0 {
		sort.Ints(indexes)
		return kept, &workflowSemanticValidationError{Codes: codes, ItemIndexes: indexes}
	}
	return kept, nil
}

func pptxSemanticMutationItemCount(operation string, args map[string]any) int {
	switch operation {
	case "update_slide":
		return len(anySlice(args["updates"]))
	case "update_deck":
		count := 0
		for _, raw := range anySlice(args["slide_updates"]) {
			if slide, ok := anyMap(raw); ok {
				count += len(anySlice(slide["updates"]))
			}
		}
		return count
	case "replace_text":
		return len(anySlice(args["replacements"]))
	default:
		return 0
	}
}

func pptxSemanticContentKey(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func newWorkflowSemanticRepairRequest(projectionID string, invalidOutput any, validationErr *workflowSemanticValidationError) workflowSemanticRepairRequest {
	return workflowSemanticRepairRequest{
		SchemaVersion: workflowSemanticRepairSchema, ProjectionID: projectionID,
		InvalidOutputDigest: validationErr.Digest, ErrorCodes: validationErr.Codes,
		InvalidItemIndexes: validationErr.ItemIndexes, InvalidOutput: invalidOutput,
		OriginalOutputSchemaVersion: "workflow_stage_semantic_output_v1", RepairAttempt: 1,
	}
}

func workflowSemanticRepairObservation(request workflowSemanticRepairRequest) string {
	raw, err := json.Marshal(request)
	if err != nil {
		return "workflow_semantic_repair_required"
	}
	instruction := "Preserve every valid effective mutation from invalid_output, repair or omit only invalid_item_indexes, and return one corrected action against the same projection. Return only actual changes; never copy unchanged current_text or use empty replacement text. Do not reread the source or widen the target scope."
	switch {
	case containsString(request.ErrorCodes, string(app.ToolErrorPPTXLayoutFitConflict)):
		instruction = "The generated PPTX text failed deterministic layout preflight. Preserve every other valid effective mutation and shorten the overflowing replacement. If that optional update cannot fit, omit the entire update object; never return empty text or unchanged current_text. Keep the same slide and operation, and return one corrected action against the same projection. Do not reread, widen scope, reduce font size, or bypass layout validation."
	case containsString(request.ErrorCodes, "cosmetic_only_change"):
		instruction = "The generated PPTX action contains cosmetic-only replacements at invalid_item_indexes; changes limited to punctuation, symbols, spacing, or letter case do not satisfy this slide/deck improvement request. Preserve every other valid substantive mutation from invalid_output. For each listed item, supply meaningful non-empty wording whose letters or numbers differ from current_text, or omit the entire update object. The corrected action must contain at least one substantive clarity, accuracy, concision, or hierarchy improvement. Never copy unchanged current_text or preserve a cosmetic-only replacement. Keep the same slide and operation, and return one corrected action against the same projection. Do not reread or widen the target scope."
	case containsString(request.ErrorCodes, "replacement_text_empty"):
		instruction = "The generated PPTX action contains empty replacement text at invalid_item_indexes. Preserve every other valid effective mutation from invalid_output. For each listed item, supply meaningful non-empty text that differs from current_text or omit the entire update object. For a broad improve or polish request, the corrected action must contain at least one substantive clarity, accuracy, concision, or hierarchy improvement; a Unicode punctuation or glyph substitution alone is valid only when the owner explicitly requested it. Return only actual changes; never fill the update list by copying unchanged current_text. Keep the same slide and operation, and return one corrected action against the same projection. Do not reread or widen the target scope."
	case containsString(request.ErrorCodes, "no_effective_mutation"):
		instruction = "Every proposed PPTX replacement was unchanged from current_text. Return at least one meaningful replacement that differs from current_text and include only shapes that actually change. Keep the same slide and operation, and return one corrected action against the same projection. Do not reread or widen the target scope."
	}
	return "WORKFLOW_SEMANTIC_REPAIR_REQUEST\n" + string(raw) + "\n" + instruction
}

func workflowSemanticOutputDigest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte(fmt.Sprint(value))
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
