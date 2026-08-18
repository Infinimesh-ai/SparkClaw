package toolhub

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func xlsxDocumentFormatProvider() documentFormatProvider {
	provider := documentFormatProvider{
		Format: app.DocumentFormatXLSX, ReadToolNames: []string{"files.read"},
		OperationOrder: canonicalDocumentOperationOrder(app.DocumentFormatXLSX),
		Parser: adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
			return runNodeAdapter(ctx, xlsxReadAdapterScript, request)
		}),
		Operations: map[string]documentOperationProvider{},
	}
	provider.Operations[app.DocumentOperationReplaceText] = documentOperationProvider{
		ToolName: "office.replace_text", Summary: "Replace bounded text and write an Office output copy.",
		BuildTargets: exactTextTargets,
		Editor: document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
			return applyOfficeReplacement(ctx, request, func(ctx context.Context, adapterRequest map[string]any) (map[string]any, error) {
				return runNodeAdapter(ctx, xlsxAdapterScript, adapterRequest)
			})
		}),
		ProjectResult: projectReplacementResult, SuccessStatus: "office_version_written",
	}
	for _, operation := range []string{
		app.DocumentOperationUpdateCell,
		app.DocumentOperationInsertRow,
		app.DocumentOperationDeleteRow,
		app.DocumentOperationUpdateRow,
		app.DocumentOperationAppendRow,
	} {
		operation := operation
		provider.Operations[operation] = documentOperationProvider{
			ToolName: "xlsx." + operation, Summary: xlsxOperationSummary(operation),
			BuildTargets: func(args map[string]any) ([]document.LocatorRequest, int, error) {
				return []document.LocatorRequest{xlsxEditTarget(operation, args)}, 0, nil
			},
			Editor: document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
				return applyXLSXStructure(ctx, operation, request)
			}),
			SuccessStatus: "xlsx_version_written",
		}
	}
	for operation, boundary := range xlsxOperationDirectoryBoundaries {
		candidate, ok := provider.Operations[operation]
		if !ok {
			continue
		}
		provider.Operations[operation] = withDocumentDirectoryBoundary(candidate, boundary.whenToUse, boundary.whenNotToUse)
	}
	return provider
}

func xlsxOperationSummary(operation string) string {
	switch operation {
	case app.DocumentOperationUpdateCell:
		return "Update one XLSX cell and write a new workbook."
	case app.DocumentOperationInsertRow:
		return "Insert one XLSX row and write a new workbook."
	case app.DocumentOperationDeleteRow:
		return "Delete one XLSX row and write a new workbook."
	case app.DocumentOperationUpdateRow:
		return "Update one XLSX row and write a new workbook."
	case app.DocumentOperationAppendRow:
		return "Append one XLSX row and write a new workbook."
	default:
		return "Apply a bounded XLSX edit and write a new workbook."
	}
}

type xlsxOperationDirectoryBoundary struct {
	whenToUse    string
	whenNotToUse string
}

var xlsxOperationDirectoryBoundaries = map[string]xlsxOperationDirectoryBoundary{
	app.DocumentOperationReplaceText: {
		whenToUse:    "Use when the owner supplies explicit old and new text and intends all matching structured text blocks or text-valued cells to change; a named sheet may narrow the replacement scope without turning it into a single-cell update.",
		whenNotToUse: "Do not use when the owner identifies one unique cell or record field as the target, values are typed, the requested change inserts, appends, or deletes a row, or the request requires whole-slide rewriting.",
	},
	app.DocumentOperationUpdateCell: {
		whenToUse:    "Use only when the owner supplies a new value and identifies exactly one evidence-located cell by address or exactly one existing record plus field; evidence may verify that owner-specified target but cannot supply a missing target or value.",
		whenNotToUse: "Do not use for old/new replacement across matching text-valued cells, even within one named sheet; do not infer an omitted unique target or new value from evidence, update multiple cells in one row, or insert, append, or delete a row.",
	},
	app.DocumentOperationUpdateRow: {
		whenToUse:    "Use when multiple leading cells of one existing evidence-bound row change while omitted trailing cells remain unchanged.",
		whenNotToUse: "Do not use to create or remove a row, change arbitrary workbook text, or update only one explicit cell.",
	},
	app.DocumentOperationInsertRow: {
		whenToUse:    "Use when a new row is required before or after one explicit evidence-bound existing row.",
		whenNotToUse: "Do not use for an end-of-sheet append without a positional anchor or to modify an existing row.",
	},
	app.DocumentOperationAppendRow: {
		whenToUse:    "Use when a new row belongs after the final structured row of one evidence-bound sheet.",
		whenNotToUse: "Do not use when a before or after row anchor is explicit or when an existing row changes.",
	},
	app.DocumentOperationDeleteRow: {
		whenToUse:    "Use only when the owner explicitly removes one complete evidence-bound row.",
		whenNotToUse: "Do not use to clear one cell, remove matching text, delete a column, or delete the workbook file.",
	},
}
