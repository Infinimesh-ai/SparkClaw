package toolhub

import (
	"context"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func (h *ToolHub) xlsxStructureEdit(ctx context.Context, operation string, args map[string]any) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	target := xlsxEditTarget(operation, args)
	result, err := h.editDocumentWorkflow(ctx, document.EditRequest{
		Path: inputPath, OutputPath: outputPath, Operation: operation, Target: target,
		Arguments: args, MaxBytes: document.SmallExtractedMaxBytes,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: documentChangeOutput(result, "xlsx_version_written")}, nil
}

func xlsxEditTarget(operation string, args map[string]any) document.LocatorRequest {
	sheet := stringArg(args, "sheet", "")
	switch operation {
	case "update_cell":
		return document.LocatorRequest{Kind: document.LocatorCell, Sheet: sheet, Cell: stringArg(args, "cell", "")}
	case "append_row":
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
	adapterRequest := map[string]any{
		"operation": operation, "path": request.Metadata.Path, "output_path": request.Edit.OutputPath,
		"sheet": stringArg(args, "sheet", ""), "cell": stringArg(args, "cell", ""), "row": intArg(args, "row", 0),
		"position": stringArg(args, "position", ""), "value": args["value"], "values": args["values"],
	}
	if operation == "append_row" {
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
	return document.ApplyResult{OutputPath: request.Edit.OutputPath, Changed: 1, Details: out}, nil
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
