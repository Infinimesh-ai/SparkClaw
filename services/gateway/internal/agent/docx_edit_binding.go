package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const documentLocateEvidenceNodeID app.WorkflowNodeID = "document_locate_evidence"

type docxParagraphTarget struct {
	Index int
	Path  string
}

type docxParagraphEvidence struct {
	Index      int
	Path       string
	Text       string
	SourceHash string
}

type docxReadEvidence struct {
	SourceSHA256        string
	Blocks              []any
	Paragraphs          []any
	SourceToolCallID    string
	SourceNodeID        app.WorkflowNodeID
	SourceScopeRevision int
	SourceSessionID     string
	SourceRunID         string
	SourcePath          string
}

func (r Runtime) bindDOCXMutationEvidence(run app.AgentRun, operation string, args map[string]any) map[string]any {
	evidence, ok := r.currentDOCXReadEvidence(run, args)
	if !ok {
		return args
	}
	if cleanOptionalString(args[app.DocumentSourceSHA256Argument]) == "" {
		args[app.DocumentSourceSHA256Argument] = evidence.SourceSHA256
	}
	if _, supplied := anyMap(args["source_evidence"]); !supplied {
		args["source_evidence"] = docxSourceEvidenceBinding(evidence, operation)
	}
	if operation == app.DocumentOperationReplaceText {
		if len(documentAnySliceFromAny(args["evidence_targets"])) == 0 {
			args["evidence_targets"] = docxReplacementEvidence(evidence.Blocks, args)
		}
		return args
	}
	position := strings.ToLower(cleanOptionalString(args["position"]))
	if operation == app.DocumentOperationInsertParagraph && (position == "start" || position == "end") {
		args["document_boundary"] = position
		return args
	}
	paragraph, ok := matchDOCXParagraphEvidence(evidence.Blocks, mustDOCXParagraphTarget(args))
	if !ok {
		return args
	}
	args["location"] = canonicalDOCXParagraphLocation(paragraph)
	args["paragraph_index"] = paragraph.Index
	if cleanOptionalString(args["source_hash"]) == "" {
		args["source_hash"] = paragraph.SourceHash
	}
	if cleanOptionalString(args["old_text"]) == "" {
		args["old_text"] = paragraph.Text
	}
	if operation == app.DocumentOperationSetTextStyle && cleanOptionalString(args["before_format_sha256"]) == "" {
		args["before_format_sha256"] = docxParagraphFormatSHA256(evidence.Paragraphs, paragraph)
	}
	return args
}

func (r Runtime) validateDOCXMutationEvidence(run app.AgentRun, toolName, operation string, args map[string]any) error {
	evidence, ok := r.currentDOCXReadEvidence(run, args)
	if !ok {
		return fmt.Errorf("%s does not have current workflow localization evidence", toolName)
	}
	return validateDOCXMutationAgainstEvidence(toolName, operation, args, evidence)
}

func validateDOCXMutationAgainstEvidence(toolName, operation string, args map[string]any, evidence docxReadEvidence) error {
	if source := cleanOptionalString(args[app.DocumentSourceSHA256Argument]); source == "" {
		return fmt.Errorf("%s requires current %s evidence", toolName, app.DocumentSourceSHA256Argument)
	} else if source != evidence.SourceSHA256 {
		return fmt.Errorf("%s %s conflicts with current workflow localization evidence", toolName, app.DocumentSourceSHA256Argument)
	}
	if !sameDOCXSourceEvidence(args["source_evidence"], docxSourceEvidenceBinding(evidence, operation)) {
		return fmt.Errorf("%s source_evidence conflicts with current workflow localization evidence", toolName)
	}
	return validateDOCXMutationTargetAgainstEvidence(toolName, operation, args, evidence)
}

func validateDOCXMutationTargetAgainstEvidence(toolName, operation string, args map[string]any, evidence docxReadEvidence) error {
	if operation == app.DocumentOperationReplaceText {
		expected := docxReplacementEvidence(evidence.Blocks, args)
		if len(expected) == 0 || !sameDOCXEvidence(expected, documentAnySliceFromAny(args["evidence_targets"])) {
			return fmt.Errorf("%s replacement targets conflict with current workflow localization evidence", toolName)
		}
		return nil
	}
	position := strings.ToLower(cleanOptionalString(args["position"]))
	if operation == app.DocumentOperationInsertParagraph && (position == "start" || position == "end") {
		if cleanOptionalString(args["document_boundary"]) != position {
			return fmt.Errorf("%s document boundary conflicts with current workflow localization evidence", toolName)
		}
		if _, hasTarget := docxParagraphTargetFromArguments(args); hasTarget || cleanOptionalString(args["source_hash"]) != "" {
			return fmt.Errorf("%s start/end insertion must not bind a paragraph target", toolName)
		}
		return nil
	}
	target, ok := docxParagraphTargetFromArguments(args)
	if !ok {
		return fmt.Errorf("%s target does not match current workflow localization evidence", toolName)
	}
	paragraph, ok := matchDOCXParagraphEvidence(evidence.Blocks, target)
	if !ok {
		return fmt.Errorf("%s target does not match current workflow localization evidence", toolName)
	}
	if sourceHash := cleanOptionalString(args["source_hash"]); sourceHash == "" {
		return fmt.Errorf("%s requires current paragraph source_hash evidence", toolName)
	} else if sourceHash != paragraph.SourceHash {
		return fmt.Errorf("%s source_hash conflicts with current workflow localization evidence", toolName)
	}
	if oldText := cleanOptionalString(args["old_text"]); oldText != "" &&
		normalizeDOCXEvidenceText(oldText) != normalizeDOCXEvidenceText(paragraph.Text) {
		return fmt.Errorf("%s old_text conflicts with current workflow localization evidence", toolName)
	}
	if operation == app.DocumentOperationDeleteParagraph && cleanOptionalString(args["old_text"]) == "" {
		return errors.New("docx.delete_paragraph requires current old_text evidence")
	}
	if operation == app.DocumentOperationSetTextStyle {
		style, ok := anyMap(args["style"])
		if !ok || len(style) == 0 {
			return errors.New("docx.set_text_style requires builtin_style, bold, or font_size_pt before approval")
		}
		expectedFormat := docxParagraphFormatSHA256(evidence.Paragraphs, paragraph)
		if expectedFormat == "" || cleanOptionalString(args["before_format_sha256"]) != expectedFormat {
			return errors.New("docx.set_text_style before_format_sha256 conflicts with current workflow localization evidence")
		}
	}
	return nil
}

func (r Runtime) currentDOCXReadEvidence(run app.AgentRun, args map[string]any) (docxReadEvidence, bool) {
	if run.Workflow == nil || r.store == nil {
		return docxReadEvidence{}, false
	}
	locateState, ok := run.Workflow.Nodes[documentLocateEvidenceNodeID]
	if !ok || locateState.Status != app.WorkflowNodeSucceeded || len(locateState.ToolCallIDs) != 1 {
		return docxReadEvidence{}, false
	}
	call, ok := r.store.GetToolCall(locateState.ToolCallIDs[0])
	if !ok || call.RunID != run.ID || call.SessionID != run.SessionID ||
		call.WorkflowID != app.WorkflowDocumentEdit || call.WorkflowNodeID != documentLocateEvidenceNodeID ||
		call.ScopeRevision != locateState.ScopeRevision || call.Tool != "files.read" || !toolCallCompleted(call) {
		return docxReadEvidence{}, false
	}
	result, ok := anyMap(call.Result)
	if !ok || !sameDocumentReadPath(strings.TrimSpace(stringValue(args["path"])), call, result) {
		return docxReadEvidence{}, false
	}
	evidence, ok := docxReadEvidenceFromResult(result)
	if !ok {
		return docxReadEvidence{}, false
	}
	evidence.SourceToolCallID = call.ID
	evidence.SourceNodeID = call.WorkflowNodeID
	evidence.SourceScopeRevision = call.ScopeRevision
	evidence.SourceSessionID = call.SessionID
	evidence.SourceRunID = call.RunID
	evidence.SourcePath = strings.TrimSpace(stringValue(firstNonNil(result["rel_path"], call.Arguments["path"])))
	return evidence, true
}

func docxReadEvidenceFromResult(result map[string]any) (docxReadEvidence, bool) {
	document, ok := anyMap(result["document"])
	if !ok || strings.ToLower(strings.TrimSpace(stringValue(document["format"]))) != app.DocumentFormatDOCX {
		return docxReadEvidence{}, false
	}
	metadata, _ := anyMap(document["metadata"])
	sourceSHA := cleanOptionalString(firstNonNil(metadata["sha256"], result[app.DocumentSourceSHA256Argument]))
	blocks := documentAnySliceFromAny(document["evidence_blocks"])
	if len(blocks) == 0 {
		blocks = documentAnySliceFromAny(document["blocks"])
	}
	if sourceSHA == "" || len(blocks) == 0 {
		return docxReadEvidence{}, false
	}
	return docxReadEvidence{
		SourceSHA256: sourceSHA,
		Blocks:       blocks,
		Paragraphs:   documentAnySliceFromAny(document["paragraphs"]),
	}, true
}

func docxSourceEvidenceBinding(evidence docxReadEvidence, operation string) map[string]any {
	return map[string]any{
		"tool_call_id":   evidence.SourceToolCallID,
		"node_id":        string(evidence.SourceNodeID),
		"scope_revision": evidence.SourceScopeRevision,
		"session_id":     evidence.SourceSessionID,
		"run_id":         evidence.SourceRunID,
		"path":           evidence.SourcePath,
		"operation":      operation,
	}
}

func sameDOCXSourceEvidence(actual any, expected map[string]any) bool {
	value, ok := anyMap(actual)
	if !ok {
		return false
	}
	actualJSON, actualErr := json.Marshal(value)
	expectedJSON, expectedErr := json.Marshal(expected)
	return actualErr == nil && expectedErr == nil && string(actualJSON) == string(expectedJSON)
}

func docxParagraphTargetFromArguments(args map[string]any) (docxParagraphTarget, bool) {
	target := docxParagraphTarget{Index: intLikeValue(args["paragraph_index"])}
	if location, ok := anyMap(args["location"]); ok {
		locationIndex := intLikeValue(firstNonNil(location["paragraph_index"], location["paragraphIndex"]))
		if target.Index > 0 && locationIndex > 0 && target.Index != locationIndex {
			return docxParagraphTarget{}, false
		}
		if target.Index <= 0 {
			target.Index = locationIndex
		}
		target.Path = cleanOptionalString(location["path"])
	}
	return target, target.Index > 0 || target.Path != ""
}

func mustDOCXParagraphTarget(args map[string]any) docxParagraphTarget {
	target, _ := docxParagraphTargetFromArguments(args)
	return target
}

func matchDOCXParagraphEvidence(blocks []any, target docxParagraphTarget) (docxParagraphEvidence, bool) {
	if target.Index <= 0 && target.Path == "" {
		return docxParagraphEvidence{}, false
	}
	var matched docxParagraphEvidence
	found := false
	for _, value := range blocks {
		block, ok := anyMap(value)
		if !ok {
			continue
		}
		location, _ := anyMap(block["location"])
		evidence := docxParagraphEvidence{
			Index:      intLikeValue(firstNonNil(location["paragraphIndex"], location["paragraph_index"])),
			Path:       cleanOptionalString(firstNonNil(location["path"], block["blockId"], block["block_id"])),
			Text:       cleanOptionalString(block["text"]),
			SourceHash: cleanOptionalString(firstNonNil(block["sourceHash"], block["source_hash"])),
		}
		if evidence.Index <= 0 ||
			target.Index > 0 && target.Index != evidence.Index ||
			target.Path != "" && target.Path != evidence.Path ||
			evidence.SourceHash == "" {
			continue
		}
		if found {
			return docxParagraphEvidence{}, false
		}
		matched = evidence
		found = true
	}
	return matched, found
}

func docxParagraphFormatSHA256(paragraphs []any, evidence docxParagraphEvidence) string {
	var projection map[string]any
	for _, value := range paragraphs {
		paragraph, ok := anyMap(value)
		if !ok {
			continue
		}
		location, _ := anyMap(paragraph["location"])
		index := intLikeValue(firstNonNil(paragraph["index"], location["paragraph_index"], location["paragraphIndex"]))
		path := cleanOptionalString(location["path"])
		if index != evidence.Index || evidence.Path != "" && path != evidence.Path ||
			!strings.EqualFold(cleanOptionalString(paragraph["part_kind"]), "body") {
			continue
		}
		if projection != nil {
			return ""
		}
		runs := []any{}
		for _, runValue := range documentAnySliceFromAny(paragraph["runs"]) {
			run, ok := anyMap(runValue)
			if !ok {
				return ""
			}
			runs = append(runs, map[string]any{
				"bold": run["bold"], "italic": run["italic"], "underline": run["underline"],
				"font_name": run["font_name"], "font_size_pt": run["font_size_pt"], "font_color": run["font_color"],
				"effective_bold": run["effective_bold"], "effective_font_size_pt": run["effective_font_size_pt"],
				"relationship_id": run["relationship_id"], "boundaries": run["boundaries"],
			})
		}
		projection = map[string]any{
			"style": paragraph["style"], "outline_level": paragraph["outline_level"], "list_id": paragraph["list_id"],
			"format": paragraph["format"], "unsupported_boundaries": paragraph["unsupported_boundaries"], "runs": runs,
		}
	}
	if projection == nil {
		return ""
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func canonicalDOCXParagraphLocation(evidence docxParagraphEvidence) map[string]any {
	location := map[string]any{
		"part": "document", "part_kind": "body", "block_type": "paragraph", "paragraph_index": evidence.Index,
	}
	if evidence.Path != "" {
		location["path"] = evidence.Path
	}
	return location
}

func docxReplacementEvidence(blocks []any, args map[string]any) []any {
	items := documentAnySliceFromAny(args["replacements"])
	if len(items) == 0 {
		return nil
	}
	targets := []any{}
	total := 0
	for _, itemValue := range items {
		item, ok := anyMap(itemValue)
		if !ok {
			return nil
		}
		find := cleanOptionalString(item["find"])
		if find == "" {
			return nil
		}
		for _, blockValue := range blocks {
			block, ok := anyMap(blockValue)
			if !ok {
				continue
			}
			text := stringValue(block["text"])
			occurrences := strings.Count(text, find)
			if occurrences == 0 {
				continue
			}
			location, _ := anyMap(block["location"])
			sourceHash := cleanOptionalString(firstNonNil(block["sourceHash"], block["source_hash"]))
			if sourceHash == "" {
				return nil
			}
			targets = append(targets, map[string]any{
				"find": find, "occurrences": occurrences, "source_hash": sourceHash, "location": location,
			})
			total += occurrences
		}
	}
	if expected := intLikeValue(args["expected_replacements"]); expected > 0 && total != expected {
		return nil
	}
	return targets
}

func sameDOCXEvidence(expected, actual []any) bool {
	expectedJSON, expectedErr := json.Marshal(expected)
	actualJSON, actualErr := json.Marshal(actual)
	return expectedErr == nil && actualErr == nil && string(expectedJSON) == string(actualJSON)
}

func normalizeDOCXEvidenceText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (r Runtime) revalidateApprovedDOCXMutation(ctx context.Context, call app.ToolCall, operation string) error {
	run, ok := r.store.GetRun(call.RunID)
	if !ok {
		return errors.New("approved DOCX mutation run is unavailable")
	}
	initial, ok := r.currentDOCXReadEvidence(run, call.Arguments)
	if !ok {
		return errors.New("approved DOCX mutation lost its workflow localization evidence")
	}
	if err := validateDOCXMutationAgainstEvidence(call.Tool, operation, call.Arguments, initial); err != nil {
		return err
	}
	read, err := r.tools.Execute(ctx, "files.read", map[string]any{"path": call.Arguments["path"]}, call.SessionID, call.RunID)
	if err != nil {
		return fmt.Errorf("approved DOCX mutation source could not be reread: %w", err)
	}
	result, ok := anyMap(read.Output)
	if !ok {
		return errors.New("approved DOCX mutation source reread is invalid")
	}
	fresh, ok := docxReadEvidenceFromResult(result)
	if !ok {
		return errors.New("approved DOCX mutation source reread lacks structured evidence")
	}
	if err := validateDOCXMutationTargetAgainstEvidence(call.Tool, operation, call.Arguments, fresh); err != nil {
		return fmt.Errorf("approved DOCX mutation is stale: %w", err)
	}
	return nil
}
