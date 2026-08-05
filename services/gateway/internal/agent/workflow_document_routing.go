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
		TreeDescription: "Read, inspect, summarize, explain, or extract verbatim in-image text from exactly one governed workspace document or attachment without modifying it. Optional OCR is evidence inside this workflow, not a separate route.",
		HardNegatives:   []string{"修改这个文档", "打开网页", "搜索整个代码仓库", "创建一个新文件"},
	}}}
}

func documentEditRoutingSemantics() workflowRoutingSemantics {
	embedTexts := []string{}
	embedTexts = append(embedTexts, documentDOCXEditRoutingExamples()...)
	embedTexts = append(embedTexts, documentXLSXEditRoutingExamples()...)
	embedTexts = append(embedTexts, documentPPTXEditRoutingExamples()...)
	embedTexts = append(embedTexts, "Edit notes.md and replace the heading")
	hardNegatives := []string{"读取并总结文档", "删除整个文件", "创建新文档", "新建一个演示文稿", "发送演示文稿", "修改网页内容"}
	hardNegatives = append(hardNegatives, documentDOCXEditRoutingHardNegatives()...)
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{
		{
			Key: "edit", Route: workflowRouteTemplate{Operation: app.RouteOperationEdit},
			EmbedTexts:      embedTexts,
			TreeDescription: "Edit a copy of one governed text, Word, spreadsheet, or presentation document. The request changes document content rather than deleting the file itself.",
			HardNegatives:   hardNegatives,
		},
		documentPDFTransformRoutingVariant(),
	}}
}
