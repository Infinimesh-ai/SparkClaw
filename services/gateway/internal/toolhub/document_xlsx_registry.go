package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func xlsxToolRegistrations() map[string]toolRegistration {
	return map[string]toolRegistration{
		"xlsx.update_cell": documentEditRegistration(
			structureOp((*ToolHub).xlsxStructureEdit, "update_cell"),
			app.DocumentFormatXLSX,
			"update_cell",
			"Update one XLSX cell and write a new workbook.",
		),
		"xlsx.insert_row": documentEditRegistration(
			structureOp((*ToolHub).xlsxStructureEdit, "insert_row"),
			app.DocumentFormatXLSX,
			"insert_row",
			"Insert one XLSX row and write a new workbook.",
		),
		"xlsx.delete_row": documentEditRegistration(
			structureOp((*ToolHub).xlsxStructureEdit, "delete_row"),
			app.DocumentFormatXLSX,
			"delete_row",
			"Delete one XLSX row and write a new workbook.",
		),
		"xlsx.update_row": documentEditRegistration(
			structureOp((*ToolHub).xlsxStructureEdit, "update_row"),
			app.DocumentFormatXLSX,
			"update_row",
			"Update one XLSX row and write a new workbook.",
		),
		"xlsx.append_row": documentEditRegistration(
			structureOp((*ToolHub).xlsxStructureEdit, "append_row"),
			app.DocumentFormatXLSX,
			"append_row",
			"Append one XLSX row and write a new workbook.",
		),
	}
}
