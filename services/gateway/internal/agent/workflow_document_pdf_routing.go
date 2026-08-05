package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

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
