package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func materializePDFTransformSchemas(definitions []app.ToolDefinition, view app.DirectoryView, entryIDs []app.ToolDirectoryEntryID) []app.ToolDefinition {
	if len(entryIDs) != 1 {
		return definitions
	}
	operation := ""
	for _, entry := range view.Entries {
		if entry.ID == entryIDs[0] && entry.Capability.Name == app.ToolCapabilityDocumentEdit &&
			entry.Capability.Qualifiers[app.CapabilityQualifierFormat] == app.DocumentFormatPDF {
			operation = entry.Capability.Qualifiers[app.CapabilityQualifierOperation]
			break
		}
	}
	schema := pdfTransformOperationSchema(operation)
	if schema == nil {
		return definitions
	}
	projected := append([]app.ToolDefinition(nil), definitions...)
	for index := range projected {
		if projected[index].Name == "pdf.transform" {
			projected[index].InputSchema = schema
		}
	}
	return projected
}

func pdfTransformOperationSchema(operation string) map[string]any {
	operationSchema := map[string]any{"enum": []any{operation}}
	pathSchema := map[string]any{"type": "string", "minLength": float64(1)}
	pageSchema := map[string]any{
		"type": "array", "minItems": float64(1), "uniqueItems": true,
		"items": map[string]any{"type": "integer", "minimum": float64(1)},
	}
	rotationSchema := map[string]any{"enum": []any{-270, -180, -90, 90, 180, 270}}
	properties := map[string]any{
		"operation": operationSchema, "path": pathSchema, "output_path": pathSchema,
	}
	required := []any{"operation", "path", "output_path"}
	switch operation {
	case "extract_pages", "delete_pages":
		properties["pages"] = pageSchema
		required = append(required, "pages")
	case "rotate_pages":
		properties["pages"] = pageSchema
		properties["rotation"] = rotationSchema
		required = append(required, "pages", "rotation")
	case "split":
	default:
		return nil
	}
	return map[string]any{
		"type": "object", "required": required, "properties": properties, "additionalProperties": false,
	}
}

func documentPDFReadRoutingExamples() []string {
	return []string{"总结 report.pdf"}
}

func documentPDFTransformRoutingVariant() workflowRoutingVariant {
	return workflowRoutingVariant{
		Key: "transform", Route: workflowRouteTemplate{Operation: app.RouteOperationTransform},
		EmbedTexts:      []string{"旋转这个 PDF", "拆分附件里的 PDF", "Transform the PDF into an edited copy", "调整 PDF 页面"},
		TreeDescription: "Transform a governed PDF into a modified output copy, including page-oriented PDF operations. Do not use for reading or deleting the source file.",
		HardNegatives:   []string{"总结 PDF", "编辑 Word 文档", "删除 PDF 文件", "创建 PDF"},
	}
}
