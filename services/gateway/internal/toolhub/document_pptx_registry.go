package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func pptxToolRegistrations() map[string]toolRegistration {
	return map[string]toolRegistration{
		"pptx.replace_text": documentEditRegistration(
			ctxArgs((*ToolHub).pptxReplaceText),
			app.DocumentFormatPPTX,
			"replace_text",
			"Replace exact PPTX text spans without flattening paragraph and run formatting.",
		),
		"pptx.add_slide": documentEditRegistration(
			structureOp((*ToolHub).pptxSlideEdit, "add_slide"),
			app.DocumentFormatPPTX,
			"add_slide",
			"Add one PPTX slide and write a new presentation.",
		),
		"pptx.update_deck": documentEditRegistration(
			structureOp((*ToolHub).pptxSlideEdit, "update_deck"),
			app.DocumentFormatPPTX,
			"update_deck",
			"Atomically improve a bounded whole PPTX deck through evidence-bound slide updates.",
		),
		"pptx.update_slide": documentEditRegistration(
			structureOp((*ToolHub).pptxSlideEdit, "update_slide"),
			app.DocumentFormatPPTX,
			"update_slide",
			"Improve one existing PPTX slide through evidence-bound text-shape updates with preserve or coordinated layout policy.",
		),
		"pptx.duplicate_slide": documentEditRegistration(
			structureOp((*ToolHub).pptxSlideEdit, "duplicate_slide"),
			app.DocumentFormatPPTX,
			"duplicate_slide",
			"Duplicate one PPTX slide and write a new presentation.",
		),
		"pptx.delete_slide": documentEditRegistration(
			structureOp((*ToolHub).pptxSlideEdit, "delete_slide"),
			app.DocumentFormatPPTX,
			"delete_slide",
			"Delete one PPTX slide and write a new presentation.",
		),
	}
}
