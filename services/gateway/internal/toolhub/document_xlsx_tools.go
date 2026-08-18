package toolhub

import (
	"context"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func xlsxEditTarget(operation string, args map[string]any) document.LocatorRequest {
	sheet := stringArg(args, "sheet", "")
	switch operation {
	case app.DocumentOperationUpdateCell:
		return document.LocatorRequest{Kind: document.LocatorCell, Sheet: sheet, Cell: stringArg(args, "cell", "")}
	case app.DocumentOperationAppendRow:
		return document.LocatorRequest{Kind: document.LocatorSheet, Sheet: sheet}
	default:
		return document.LocatorRequest{Kind: document.LocatorRow, Sheet: sheet, Row: intArg(args, "row", 0), AllowMultiple: true}
	}
}

func runXlsxStructureAdapter(ctx context.Context, request map[string]any) (map[string]any, error) {
	return runNodeAdapter(ctx, xlsxStructureAdapterScript, request)
}

func applyXLSXStructure(ctx context.Context, operation string, request document.ApplyRequest) (document.ApplyResult, error) {
	args := request.Edit.Arguments
	canonicalSheet, err := validateXLSXEditEvidence(operation, request.Document, args)
	if err != nil {
		return document.ApplyResult{}, err
	}
	adapterRequest := map[string]any{
		"operation": operation, "path": request.Metadata.Path, "output_path": request.Edit.OutputPath,
		"sheet": canonicalSheet, "cell": strings.ToUpper(strings.TrimSpace(stringArg(args, "cell", ""))), "row": intArg(args, "row", 0),
		"position": stringArg(args, "position", ""), "value": args["value"], "values": args["values"],
	}
	if operation == app.DocumentOperationAppendRow {
		appendAfterRow, locateErr := lastStructuredXLSXRow(request.Document, stringArg(args, "sheet", ""))
		if locateErr != nil {
			return document.ApplyResult{}, locateErr
		}
		adapterRequest["append_after_row"] = appendAfterRow
	}
	out, err := runXlsxStructureAdapter(ctx, adapterRequest)
	if err != nil {
		return document.ApplyResult{}, err
	}
	return document.ApplyResult{OutputPath: request.Edit.OutputPath, Changed: intArg(out, "changed", 1), Details: out}, nil
}

func validateXLSXEditEvidence(operation string, representation document.Representation, args map[string]any) (string, error) {
	sheet, ok := xlsxSheetByName(representation, stringArg(args, "sheet", ""))
	if !ok {
		return "", xlsxEvidenceError(representation.Format, "trusted worksheet evidence is missing or ambiguous")
	}
	name := stringArg(sheet, "name", "")
	switch operation {
	case app.DocumentOperationUpdateCell:
		cell := strings.ToUpper(strings.TrimSpace(stringArg(args, "cell", "")))
		if evidence, found := xlsxCellByAddress(sheet, cell); !found || stringArg(args, "source_cell_hash", "") != stringArg(evidence, "source_hash", "") {
			return "", xlsxEvidenceError(representation.Format, "trusted cell evidence is missing or stale")
		}
	case app.DocumentOperationInsertRow, app.DocumentOperationDeleteRow, app.DocumentOperationUpdateRow:
		row := intArg(args, "row", 0)
		if evidence, found := xlsxRowByIndex(sheet, row); !found || stringArg(args, "source_row_hash", "") != stringArg(evidence, "source_hash", "") {
			return "", xlsxEvidenceError(representation.Format, "trusted row evidence is missing or stale")
		}
	case app.DocumentOperationAppendRow:
		if stringArg(args, "source_sheet_hash", "") == "" || stringArg(args, "source_sheet_hash", "") != stringArg(sheet, "source_hash", "") {
			return "", xlsxEvidenceError(representation.Format, "trusted sheet evidence is missing or stale")
		}
	default:
		return "", xlsxEvidenceError(representation.Format, fmt.Sprintf("unsupported XLSX evidence operation %q", operation))
	}
	return name, nil
}

func xlsxSheetByName(representation document.Representation, name string) (map[string]any, bool) {
	var matched map[string]any
	for _, sheet := range representation.Sheets {
		if !strings.EqualFold(strings.TrimSpace(stringArg(sheet, "name", "")), strings.TrimSpace(name)) {
			continue
		}
		if matched != nil {
			return nil, false
		}
		matched = sheet
	}
	return matched, matched != nil
}

func xlsxRowByIndex(sheet map[string]any, index int) (map[string]any, bool) {
	for _, rawRow := range documentAnySlice(sheet["rows"]) {
		if row, ok := documentAnyMap(rawRow); ok && intArg(row, "index", 0) == index {
			return row, true
		}
	}
	return nil, false
}

func xlsxCellByAddress(sheet map[string]any, address string) (map[string]any, bool) {
	for _, rawRow := range documentAnySlice(sheet["rows"]) {
		row, ok := documentAnyMap(rawRow)
		if !ok {
			continue
		}
		for _, rawCell := range documentAnySlice(row["cells"]) {
			if cell, ok := documentAnyMap(rawCell); ok && strings.EqualFold(stringArg(cell, "address", ""), address) {
				return cell, true
			}
		}
	}
	return nil, false
}

func xlsxEvidenceError(format, detail string) error {
	return &document.PipelineError{Code: document.CodeResourceInvalid, Stage: document.StageConstrain, Format: format, Detail: detail}
}

func lastStructuredXLSXRow(representation document.Representation, sheetName string) (int, error) {
	for _, sheet := range representation.Sheets {
		if !strings.EqualFold(strings.TrimSpace(stringArg(sheet, "name", "")), strings.TrimSpace(sheetName)) {
			continue
		}
		lastRow := 0
		for _, rawRow := range documentAnySlice(sheet["rows"]) {
			row, ok := documentAnyMap(rawRow)
			if !ok {
				continue
			}
			if rowIndex := intArg(row, "index", 0); rowIndex > lastRow {
				lastRow = rowIndex
			}
		}
		return lastRow, nil
	}
	return 0, &document.PipelineError{
		Code: document.CodeTargetNotFound, Stage: document.StageLocate, Format: representation.Format,
		Detail: "the requested XLSX sheet was not found in the structured document",
	}
}
