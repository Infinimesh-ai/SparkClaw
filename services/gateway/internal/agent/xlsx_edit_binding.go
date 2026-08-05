package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

type xlsxEditEvidence struct {
	Operation    string
	SourceSHA256 string
	Sheet        string
	Cell         string
	TargetHash   string
}

func (r Runtime) bindXLSXEditEvidence(run app.AgentRun, operation string, args map[string]any) map[string]any {
	evidence, ok := r.currentXLSXEditEvidence(run, operation, args)
	if !ok {
		return args
	}
	if cleanOptionalString(args["source_sha256"]) == "" {
		args["source_sha256"] = evidence.SourceSHA256
	}
	if evidence.Sheet != "" {
		args["sheet"] = evidence.Sheet
	}
	if evidence.Cell != "" {
		args["cell"] = evidence.Cell
	}
	field := xlsxTargetHashArgument(operation)
	if field != "" && cleanOptionalString(args[field]) == "" {
		args[field] = evidence.TargetHash
	}
	return args
}

func (r Runtime) validateXLSXEditEvidence(ctx context.Context, run app.AgentRun, operation string, args map[string]any) error {
	evidence, ok := r.currentXLSXEditEvidence(run, operation, args)
	if !ok {
		return errors.New("XLSX edit target does not match current workflow localization evidence")
	}
	if cleanOptionalString(args["source_sha256"]) != evidence.SourceSHA256 {
		return errors.New("XLSX source_sha256 conflicts with current workflow localization evidence")
	}
	if field := xlsxTargetHashArgument(operation); field != "" && cleanOptionalString(args[field]) != evidence.TargetHash {
		return errors.New("XLSX " + field + " conflicts with current workflow localization evidence")
	}
	root := r.tools.Config().Workspaces.DefaultRoot
	preflight, err := preflightDocumentPath(root, cleanOptionalString(args["path"]), false)
	if err != nil || preflight.Format != app.DocumentFormatXLSX {
		return errors.New("XLSX package preflight could not resolve the evidence-bound workbook")
	}
	packagePath := filepath.Join(root, filepath.FromSlash(preflight.InputRef))
	metadata, err := document.InspectFile(ctx, root, packagePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(metadata.SHA256, evidence.SourceSHA256) {
		return errors.New("XLSX workbook changed after current workflow localization evidence was read")
	}
	if _, err := document.ValidateXLSXPackageForOperation(packagePath, operation, args); err != nil {
		return err
	}
	return nil
}

func (r Runtime) currentXLSXEditEvidence(run app.AgentRun, operation string, args map[string]any) (xlsxEditEvidence, bool) {
	document, ok := r.currentXLSXReadDocument(run, args)
	if !ok {
		return xlsxEditEvidence{}, false
	}
	metadata, _ := anyMap(document["metadata"])
	evidence := xlsxEditEvidence{
		Operation: operation, SourceSHA256: cleanOptionalString(metadata["sha256"]),
	}
	if evidence.SourceSHA256 == "" {
		return xlsxEditEvidence{}, false
	}
	if operation == "replace_text" {
		return evidence, true
	}
	sheet, ok := matchXLSXSheetEvidence(documentAnySliceFromAny(document["sheets"]), cleanOptionalString(args["sheet"]))
	if !ok {
		return xlsxEditEvidence{}, false
	}
	evidence.Sheet = cleanOptionalString(sheet["name"])
	switch operation {
	case "update_cell":
		cell, found := matchXLSXCellEvidence(sheet, cleanOptionalString(args["cell"]))
		if !found {
			return xlsxEditEvidence{}, false
		}
		evidence.Cell = strings.ToUpper(cleanOptionalString(cell["address"]))
		evidence.TargetHash = cleanOptionalString(cell["source_hash"])
	case "insert_row", "delete_row", "update_row":
		row, found := matchXLSXRowEvidence(sheet, intLikeValue(args["row"]))
		if !found {
			return xlsxEditEvidence{}, false
		}
		evidence.TargetHash = cleanOptionalString(row["source_hash"])
	case "append_row":
		evidence.TargetHash = cleanOptionalString(sheet["source_hash"])
	default:
		return xlsxEditEvidence{}, false
	}
	return evidence, evidence.TargetHash != ""
}

func (r Runtime) currentXLSXReadDocument(run app.AgentRun, args map[string]any) (map[string]any, bool) {
	if run.Workflow == nil || r.store == nil {
		return nil, false
	}
	locateState, ok := run.Workflow.Nodes[documentLocateEvidenceNodeID]
	if !ok || locateState.Status != app.WorkflowNodeSucceeded || len(locateState.ToolCallIDs) != 1 {
		return nil, false
	}
	call, ok := r.store.GetToolCall(locateState.ToolCallIDs[0])
	if !ok || call.RunID != run.ID || call.SessionID != run.SessionID ||
		call.WorkflowID != app.WorkflowDocumentEdit || call.WorkflowNodeID != documentLocateEvidenceNodeID ||
		call.ScopeRevision != locateState.ScopeRevision || call.Tool != "files.read" || !toolCallCompleted(call) {
		return nil, false
	}
	result, ok := anyMap(call.Result)
	if !ok || !sameDocumentReadPath(cleanOptionalString(args["path"]), call, result) {
		return nil, false
	}
	document, ok := anyMap(result["document"])
	if !ok || !strings.EqualFold(cleanOptionalString(document["format"]), app.DocumentFormatXLSX) {
		return nil, false
	}
	return document, true
}

func matchXLSXSheetEvidence(sheets []any, name string) (map[string]any, bool) {
	var matched map[string]any
	for _, rawSheet := range sheets {
		sheet, ok := anyMap(rawSheet)
		if !ok || !strings.EqualFold(cleanOptionalString(sheet["name"]), name) {
			continue
		}
		if matched != nil {
			return nil, false
		}
		matched = sheet
	}
	return matched, matched != nil
}

func matchXLSXRowEvidence(sheet map[string]any, index int) (map[string]any, bool) {
	var matched map[string]any
	for _, rawRow := range documentAnySliceFromAny(sheet["rows"]) {
		row, ok := anyMap(rawRow)
		if !ok || intLikeValue(row["index"]) != index || cleanOptionalString(row["source_hash"]) == "" {
			continue
		}
		if matched != nil {
			return nil, false
		}
		matched = row
	}
	return matched, matched != nil
}

func matchXLSXCellEvidence(sheet map[string]any, address string) (map[string]any, bool) {
	address = strings.ToUpper(strings.TrimSpace(address))
	var matched map[string]any
	for _, rawRow := range documentAnySliceFromAny(sheet["rows"]) {
		row, ok := anyMap(rawRow)
		if !ok {
			continue
		}
		for _, rawCell := range documentAnySliceFromAny(row["cells"]) {
			cell, ok := anyMap(rawCell)
			if !ok || !strings.EqualFold(cleanOptionalString(cell["address"]), address) || cleanOptionalString(cell["source_hash"]) == "" {
				continue
			}
			if matched != nil {
				return nil, false
			}
			matched = cell
		}
	}
	return matched, matched != nil
}

func xlsxTargetHashArgument(operation string) string {
	switch operation {
	case "update_cell":
		return "source_cell_hash"
	case "insert_row", "delete_row", "update_row":
		return "source_row_hash"
	case "append_row":
		return "source_sheet_hash"
	default:
		return ""
	}
}
