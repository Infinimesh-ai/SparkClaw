package agent

func documentDOCXEditRoutingExamples() []string {
	return []string{
		"修改这个 Word 文档",
		"Replace every exact occurrence of the old product name in report.docx",
		"Rewrite paragraph 4 in report.docx with the corrected summary",
		"Insert a new paragraph after paragraph 2 in report.docx",
		"Delete paragraph 5 from report.docx without deleting the file",
		"Edit paragraph 3 in report.docx and make its text bold",
		"替换 report.docx 中旧产品名称的每一处准确文本",
		"改写 report.docx 第 4 段的总结内容",
		"在 report.docx 第 2 段后插入一个新段落",
		"删除 report.docx 第 5 段，但保留文档文件",
		"编辑 report.docx 第 3 段并将文字设为粗体",
	}
}

func documentDOCXEditRoutingHardNegatives() []string {
	return []string{
		"Summarize report.docx without changing it",
		"Delete report.docx from the workspace",
		"Create a new Word document",
		"Click the Bold button on the current Word Online page",
		"总结 report.docx，不要修改内容",
		"从工作区删除 report.docx 文件",
		"新建一个 Word 文档",
		"点击当前 Word 在线页面的粗体按钮",
	}
}
