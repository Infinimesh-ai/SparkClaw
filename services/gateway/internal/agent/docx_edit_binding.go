package agent

import (
	"errors"
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

func isDOCXReplaceParagraphDefinition(definition app.ToolDefinition, plan toolPlan) bool {
	if plan.WorkflowID != app.WorkflowDocumentEdit {
		return false
	}
	for _, capability := range definition.Capabilities {
		if capability.Name == plan.Capability &&
			capability.Qualifiers[app.CapabilityQualifierFormat] == app.DocumentFormatDOCX &&
			capability.Qualifiers[app.CapabilityQualifierOperation] == "replace_paragraph" {
			return true
		}
	}
	return false
}

func (r Runtime) bindDOCXReplaceParagraphEvidence(run app.AgentRun, args map[string]any) map[string]any {
	evidence, ok := r.currentDOCXParagraphEvidence(run, args)
	if !ok {
		return args
	}
	if cleanOptionalString(args["source_hash"]) == "" {
		args["source_hash"] = evidence.SourceHash
	}
	if _, hasLocation := args["location"]; hasLocation {
		location := map[string]any{
			"part":            "document",
			"block_type":      "paragraph",
			"paragraph_index": evidence.Index,
		}
		if evidence.Path != "" {
			location["path"] = evidence.Path
		}
		args["location"] = location
	}
	return args
}

func (r Runtime) validateDOCXReplaceParagraphEvidence(run app.AgentRun, args map[string]any) error {
	evidence, ok := r.currentDOCXParagraphEvidence(run, args)
	if !ok {
		return errors.New("docx.replace_paragraph target does not match current workflow localization evidence")
	}
	if sourceHash := cleanOptionalString(args["source_hash"]); sourceHash != "" && sourceHash != evidence.SourceHash {
		return errors.New("docx.replace_paragraph source_hash conflicts with current workflow localization evidence")
	}
	if oldText := cleanOptionalString(args["old_text"]); oldText != "" &&
		normalizeDOCXEvidenceText(oldText) != normalizeDOCXEvidenceText(evidence.Text) {
		return errors.New("docx.replace_paragraph old_text conflicts with current workflow localization evidence")
	}
	if cleanOptionalString(args["source_hash"]) == "" {
		return errors.New("docx.replace_paragraph requires current workflow localization evidence")
	}
	return nil
}

func (r Runtime) currentDOCXParagraphEvidence(run app.AgentRun, args map[string]any) (docxParagraphEvidence, bool) {
	if run.Workflow == nil || r.store == nil {
		return docxParagraphEvidence{}, false
	}
	target, ok := docxParagraphTargetFromArguments(args)
	if !ok {
		return docxParagraphEvidence{}, false
	}
	locateState, ok := run.Workflow.Nodes[documentLocateEvidenceNodeID]
	if !ok || locateState.Status != app.WorkflowNodeSucceeded || len(locateState.ToolCallIDs) != 1 {
		return docxParagraphEvidence{}, false
	}
	call, ok := r.store.GetToolCall(locateState.ToolCallIDs[0])
	if !ok || call.RunID != run.ID || call.SessionID != run.SessionID ||
		call.WorkflowID != app.WorkflowDocumentEdit || call.WorkflowNodeID != documentLocateEvidenceNodeID ||
		call.ScopeRevision != locateState.ScopeRevision || call.Tool != "files.read" || !toolCallCompleted(call) {
		return docxParagraphEvidence{}, false
	}
	result, ok := anyMap(call.Result)
	if !ok || !sameDocumentReadPath(strings.TrimSpace(stringValue(args["path"])), call, result) {
		return docxParagraphEvidence{}, false
	}
	document, ok := anyMap(result["document"])
	if !ok || strings.ToLower(strings.TrimSpace(stringValue(document["format"]))) != app.DocumentFormatDOCX {
		return docxParagraphEvidence{}, false
	}
	blocks := documentAnySliceFromAny(document["evidence_blocks"])
	if len(blocks) == 0 {
		blocks = documentAnySliceFromAny(document["blocks"])
	}
	return matchDOCXParagraphEvidence(blocks, target)
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

func matchDOCXParagraphEvidence(blocks []any, target docxParagraphTarget) (docxParagraphEvidence, bool) {
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

func normalizeDOCXEvidenceText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
