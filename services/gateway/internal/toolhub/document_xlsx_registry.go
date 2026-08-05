package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

type xlsxOperationDirectoryBoundary struct {
	whenToUse    string
	whenNotToUse string
}

var xlsxOperationDirectoryBoundaries = map[string]xlsxOperationDirectoryBoundary{
	"replace_text": {
		whenToUse:    "Use when the owner supplies explicit old and new text and intends matching structured text blocks or text-valued cells to change.",
		whenNotToUse: "Do not use when a cell or row location is explicit, values are typed, the requested change inserts, appends, or deletes a row, or the request requires whole-slide rewriting.",
	},
	"update_cell": {
		whenToUse:    "Use when exactly one evidence-located cell or one field in an existing row changes.",
		whenNotToUse: "Do not use when multiple cells in one row change or when a row is inserted, appended, or deleted.",
	},
	"update_row": {
		whenToUse:    "Use when multiple leading cells of one existing evidence-bound row change while omitted trailing cells remain unchanged.",
		whenNotToUse: "Do not use to create or remove a row, change arbitrary workbook text, or update only one explicit cell.",
	},
	"insert_row": {
		whenToUse:    "Use when a new row is required before or after one explicit evidence-bound existing row.",
		whenNotToUse: "Do not use for an end-of-sheet append without a positional anchor or to modify an existing row.",
	},
	"append_row": {
		whenToUse:    "Use when a new row belongs after the final structured row of one evidence-bound sheet.",
		whenNotToUse: "Do not use when a before or after row anchor is explicit or when an existing row changes.",
	},
	"delete_row": {
		whenToUse:    "Use only when the owner explicitly removes one complete evidence-bound row.",
		whenNotToUse: "Do not use to clear one cell, remove matching text, delete a column, or delete the workbook file.",
	},
}

func xlsxToolRegistrations() map[string]toolRegistration {
	return map[string]toolRegistration{
		"xlsx.update_cell": xlsxOperationRegistration(
			structureOp((*ToolHub).xlsxStructureEdit, "update_cell"),
			"update_cell",
			"Update one XLSX cell and write a new workbook.",
		),
		"xlsx.insert_row": xlsxOperationRegistration(
			structureOp((*ToolHub).xlsxStructureEdit, "insert_row"),
			"insert_row",
			"Insert one XLSX row and write a new workbook.",
		),
		"xlsx.delete_row": xlsxOperationRegistration(
			structureOp((*ToolHub).xlsxStructureEdit, "delete_row"),
			"delete_row",
			"Delete one XLSX row and write a new workbook.",
		),
		"xlsx.update_row": xlsxOperationRegistration(
			structureOp((*ToolHub).xlsxStructureEdit, "update_row"),
			"update_row",
			"Update one XLSX row and write a new workbook.",
		),
		"xlsx.append_row": xlsxOperationRegistration(
			structureOp((*ToolHub).xlsxStructureEdit, "append_row"),
			"append_row",
			"Append one XLSX row and write a new workbook.",
		),
	}
}

func xlsxOperationRegistration(run toolExecutor, operation, summary string) toolRegistration {
	registration := documentEditRegistration(run, app.DocumentFormatXLSX, operation, summary)
	applyXLSXOperationDirectoryBoundary(&registration, operation)
	return registration
}

func applyXLSXOperationDirectoryBoundary(registration *toolRegistration, operation string) {
	if registration == nil {
		return
	}
	boundary, ok := xlsxOperationDirectoryBoundaries[operation]
	if !ok {
		return
	}
	registration.directory.WhenToUse = boundary.whenToUse
	registration.directory.WhenNotToUse = boundary.whenNotToUse
}
