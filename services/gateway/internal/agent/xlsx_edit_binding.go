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

func (r Runtime) bindXLSXEditEvidence(ctx context.Context, run app.AgentRun, operation string, args map[string]any) (map[string]any, error) {
	evidence, ok, err := r.currentXLSXEditEvidence(ctx, run, operation, args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return args, nil
	}
	if cleanOptionalString(args[app.DocumentSourceSHA256Argument]) == "" {
		args[app.DocumentSourceSHA256Argument] = evidence.SourceSHA256
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
	return args, nil
}

func (r Runtime) validateXLSXEditEvidence(ctx context.Context, run app.AgentRun, operation string, args map[string]any) error {
	evidence, ok, err := r.currentXLSXEditEvidence(ctx, run, operation, args)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("XLSX edit target does not match current workflow localization evidence")
	}
	if cleanOptionalString(args[app.DocumentSourceSHA256Argument]) != evidence.SourceSHA256 {
		return errors.New("XLSX " + app.DocumentSourceSHA256Argument + " conflicts with current workflow localization evidence")
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
	if _, err := document.ValidateXLSXPackageForOperation(packagePath, operation, args); err != nil {
		return err
	}
	return nil
}

func (r Runtime) currentXLSXEditEvidence(ctx context.Context, run app.AgentRun, operation string, args map[string]any) (xlsxEditEvidence, bool, error) {
	document, ok, err := r.currentXLSXReadDocument(ctx, run, args)
	if err != nil {
		return xlsxEditEvidence{}, false, err
	}
	if !ok {
		return xlsxEditEvidence{}, false, nil
	}
	metadata, _ := anyMap(document["metadata"])
	evidence := xlsxEditEvidence{
		Operation: operation, SourceSHA256: cleanOptionalString(metadata["sha256"]),
	}
	if evidence.SourceSHA256 == "" {
		return xlsxEditEvidence{}, false, nil
	}
	if operation == app.DocumentOperationReplaceText {
		return evidence, true, nil
	}
	sheet, ok := matchXLSXSheetEvidence(documentAnySliceFromAny(document["sheets"]), cleanOptionalString(args["sheet"]))
	if !ok {
		return xlsxEditEvidence{}, false, nil
	}
	evidence.Sheet = cleanOptionalString(sheet["name"])
	switch operation {
	case app.DocumentOperationUpdateCell:
		cell, found := matchXLSXCellEvidence(sheet, cleanOptionalString(args["cell"]))
		if !found {
			return xlsxEditEvidence{}, false, nil
		}
		evidence.Cell = strings.ToUpper(cleanOptionalString(cell["address"]))
		evidence.TargetHash = cleanOptionalString(cell["source_hash"])
	case app.DocumentOperationInsertRow, app.DocumentOperationDeleteRow, app.DocumentOperationUpdateRow:
		row, found := matchXLSXRowEvidence(sheet, intLikeValue(args["row"]))
		if !found {
			return xlsxEditEvidence{}, false, nil
		}
		evidence.TargetHash = cleanOptionalString(row["source_hash"])
	case app.DocumentOperationAppendRow:
		evidence.TargetHash = cleanOptionalString(sheet["source_hash"])
	default:
		return xlsxEditEvidence{}, false, nil
	}
	return evidence, evidence.TargetHash != "", nil
}

func (r Runtime) currentXLSXReadDocument(ctx context.Context, run app.AgentRun, args map[string]any) (map[string]any, bool, error) {
	if run.Workflow == nil || r.store == nil {
		return nil, false, nil
	}
	locateState, ok := run.Workflow.Nodes[documentLocateEvidenceNodeID]
	if !ok || locateState.Status != app.WorkflowNodeSucceeded || len(locateState.ToolCallIDs) != 1 {
		return nil, false, nil
	}
	call, ok, err := r.store.GetToolCall(ctx, locateState.ToolCallIDs[0])
	if err != nil {
		return nil, false, err
	}
	if !ok || call.RunID != run.ID || call.SessionID != run.SessionID ||
		call.WorkflowID != app.WorkflowDocumentEdit || call.WorkflowNodeID != documentLocateEvidenceNodeID ||
		call.ScopeRevision != locateState.ScopeRevision || call.Tool != "files.read" || !toolCallCompleted(call) {
		return nil, false, nil
	}
	result, ok := anyMap(call.Result)
	if !ok || !sameDocumentReadPath(cleanOptionalString(args["path"]), call, result) {
		return nil, false, nil
	}
	document, ok := anyMap(result["document"])
	if !ok || !strings.EqualFold(cleanOptionalString(document["format"]), app.DocumentFormatXLSX) {
		return nil, false, nil
	}
	return document, true, nil
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
	case app.DocumentOperationUpdateCell:
		return "source_cell_hash"
	case app.DocumentOperationInsertRow, app.DocumentOperationDeleteRow, app.DocumentOperationUpdateRow:
		return "source_row_hash"
	case app.DocumentOperationAppendRow:
		return "source_sheet_hash"
	default:
		return ""
	}
}
