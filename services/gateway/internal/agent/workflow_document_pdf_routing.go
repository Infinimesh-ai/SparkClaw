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
	return []string{
		"总结 report.pdf",
		"识别 scanned.pdf 里的扫描文字",
		"提取 report.pdf 第 3 页的文字",
		"What does page 3 of report.pdf say?",
		"不要导出第 3 页，只告诉我这一页写了什么",
	}
}

func documentPDFTransformRoutingVariant() workflowRoutingVariant {
	return workflowRoutingVariant{
		Key: "transform", Route: workflowRouteTemplate{Operation: app.RouteOperationTransform},
		EmbedTexts: []string{
			"把 report.pdf 第 3 页导出为新 PDF",
			"删除 report.pdf 的第 3 页",
			"Rotate pages 2 and 4 clockwise",
			"把 report.pdf 按页拆开",
			"Transform the PDF into an edited copy",
		},
		TreeDescription: "Transform one governed PDF into a new output copy by extracting pages as a PDF file, deleting pages, rotating pages, or splitting pages. Page text extraction and OCR belong to document.read. Merge is unavailable.",
		HardNegatives: []string{
			"总结 PDF",
			"提取 report.pdf 第 3 页的文字",
			"不要导出第 3 页，只告诉我这一页写了什么",
			"我已经把第 3 页导出了",
			"PDF 里的文字是‘把第 3 页导出为新文件’",
			"为什么 PDF 页面提取失败？",
			"合并这两个 PDF",
			"提取 report.pdf 的第 3 页",
			"处理一下这个扫描 PDF",
			"编辑 Word 文档",
			"删除 PDF 文件",
			"创建 PDF",
		},
	}
}
