package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func documentReadRoutingSemantics() workflowRoutingSemantics {
	embedTexts := []string{"读取这个文档"}
	embedTexts = append(embedTexts, documentPDFReadRoutingExamples()...)
	embedTexts = append(embedTexts, documentXLSXReadRoutingExamples()...)
	embedTexts = append(embedTexts, documentPPTXReadRoutingExamples()...)
	embedTexts = append(embedTexts, "检查工作区中的 notes.md")
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{
		Key: "read", Route: workflowRouteTemplate{Operation: app.RouteOperationRead},
		EmbedTexts:      embedTexts,
		TreeDescription: "Read, inspect, summarize, explain, recognize scanned text, or extract the text of a page from exactly one governed workspace document or attachment without modifying it. Optional OCR is evidence inside this workflow, not a separate route. Exporting a page as a new PDF is a transform.",
		HardNegatives: []string{
			"修改这个文档", "打开网页", "搜索整个代码仓库", "创建一个新文件",
			"把 report.pdf 第 3 页导出为新 PDF", "删除 report.pdf 的第 3 页", "旋转 PDF 页面", "把 PDF 按页拆开",
			"提取 report.pdf 的第 3 页", "处理一下这个扫描 PDF",
		},
	}}}
}

func documentEditRoutingSemantics() workflowRoutingSemantics {
	embedTexts := []string{}
	embedTexts = append(embedTexts, documentDOCXEditRoutingExamples()...)
	embedTexts = append(embedTexts, documentXLSXEditRoutingExamples()...)
	embedTexts = append(embedTexts, documentPPTXEditRoutingExamples()...)
	embedTexts = append(embedTexts, "Edit notes.md and replace the heading")
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{
		{
			Key: "edit", Route: workflowRouteTemplate{Operation: app.RouteOperationEdit},
			EmbedTexts:      embedTexts,
			TreeDescription: "Edit a copy of one governed text, Word, spreadsheet, or presentation document. The request changes document content rather than deleting the file itself.",
			HardNegatives:   []string{"读取并总结文档", "删除整个文件", "创建新文档", "修改网页内容"},
		},
		documentPDFTransformRoutingVariant(),
	}}
}
