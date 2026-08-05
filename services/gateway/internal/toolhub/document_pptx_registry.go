package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func pptxToolRegistrations() map[string]toolRegistration {
	return map[string]toolRegistration{
		"pptx.add_slide": documentEditRegistration(
			structureOp((*ToolHub).pptxSlideEdit, "add_slide"),
			app.DocumentFormatPPTX,
			"add_slide",
			"Add one PPTX slide and write a new presentation.",
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
