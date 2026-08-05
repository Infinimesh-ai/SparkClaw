package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func docxToolRegistrations() map[string]toolRegistration {
	return map[string]toolRegistration{
		"docx.replace_paragraph": docxReplaceParagraphRegistration(),
		"docx.insert_paragraph":  docxInsertParagraphRegistration(),
		"docx.delete_paragraph": documentEditRegistration(
			structureOp((*ToolHub).docxStructureEdit, "delete_paragraph"),
			app.DocumentFormatDOCX,
			"delete_paragraph",
			"Delete one DOCX paragraph and write a new document.",
		),
		"docx.set_text_style": documentEditRegistration(
			structureOp((*ToolHub).docxStructureEdit, "set_text_style"),
			app.DocumentFormatDOCX,
			"set_text_style",
			"Apply a bounded DOCX paragraph style and write a new document.",
		),
	}
}

func docxReplaceParagraphRegistration() toolRegistration {
	registration := documentEditRegistration(
		structureOp((*ToolHub).docxStructureEdit, "replace_paragraph"),
		app.DocumentFormatDOCX,
		"replace_paragraph",
		"Replace one existing DOCX paragraph and write a new document.",
	)
	registration.directory.WhenToUse = "Use when structured read evidence locates an existing paragraph whose content the owner wants to modify, improve, polish, complete, update, revise, or rewrite."
	registration.directory.WhenNotToUse = "Do not use when the owner explicitly requests a new paragraph or the target paragraph is absent; use insertion for an additive change."
	return registration
}

func docxInsertParagraphRegistration() toolRegistration {
	registration := documentEditRegistration(
		structureOp((*ToolHub).docxStructureEdit, "insert_paragraph"),
		app.DocumentFormatDOCX,
		"insert_paragraph",
		"Insert one new DOCX paragraph and write a new document.",
	)
	registration.directory.WhenToUse = "Use only when the owner explicitly requests adding, inserting, or appending a new paragraph, or structured read evidence confirms that no existing target can be replaced."
	registration.directory.WhenNotToUse = "Do not use to improve, polish, complete, update, revise, or rewrite an existing paragraph; replace that paragraph instead."
	return registration
}
