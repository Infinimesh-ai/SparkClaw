package agent

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type documentPreflight struct {
	InputRef  string
	OutputRef string
	Format    string
}

func recognizeDocumentRoute(input workflowRecognitionContext, edit bool) (workflowRecognition, bool) {
	content := semanticRoutingContent(input.Content)
	lower := strings.ToLower(content)
	wantsEdit := workspaceMutationRequested(lower) || containsEnglishSemanticTerm(lower, "replace", "update", "change", "insert", "append", "transform", "rotate", "split") ||
		containsAny(lower, "替换", "更新", "改为", "插入", "追加", "转换", "旋转", "拆分")
	wantsRead := documentInformationRequested(content, lower) && !wantsEdit
	if edit && !wantsEdit || !edit && !wantsRead {
		return workflowRecognition{}, false
	}
	if edit && unsupportedDocumentEditIntent(lower) {
		return workflowRecognition{}, false
	}
	paths := extractPaths(content)
	if len(paths) == 0 && edit {
		if recentPath := recentDocumentContextPath(input.Snapshot); recentPath != "" {
			paths = []string{recentPath}
		}
	}
	if len(paths) > 1 || len(paths) == 0 && (!edit || !documentReadIntent(lower)) {
		return workflowRecognition{}, false
	}
	if len(paths) == 0 {
		return workflowRecognition{
			Status: app.RouteClarify, Confidence: 0.75,
			Reason: "The document workflow requires one explicit governed path.",
		}, true
	}
	preflight, err := preflightDocumentPath(input.WorkspaceRoot, paths[0], edit)
	if err != nil {
		if edit && strings.Contains(err.Error(), "read-only in revision 1") {
			return workflowRecognition{}, false
		}
		return workflowRecognition{
			Status: app.RouteBlocked, Confidence: 0.95,
			Reason: "Document preflight failed: " + err.Error(),
		}, true
	}
	operation := app.RouteOperationRead
	facts := map[string]string{"path": preflight.InputRef, "document_format": preflight.Format}
	if edit {
		operation = app.RouteOperationEdit
		documentOperation := documentOperationForContent(preflight.Format, lower)
		if documentOperation == "" {
			return workflowRecognition{
				Status: app.RouteClarify, Confidence: 0.8,
				Reason: "The requested document edit operation is not specific enough for revision 1.",
			}, true
		}
		if preflight.Format == app.DocumentFormatPDF {
			operation = app.RouteOperationTransform
		}
		facts["document_operation"] = documentOperation
		facts["output_path"] = preflight.OutputRef
	}
	return workflowRecognition{
		Slots: app.RouteSlots{
			Operation: operation, Query: content, TargetKind: "workspace_path", TargetRef: preflight.InputRef,
			OutputRef: preflight.OutputRef, Format: preflight.Format,
		},
		Facts: facts, Confidence: 0.95,
		Reason: "The request targets one preflighted governed document.",
	}, true
}

func unsupportedDocumentEditIntent(lower string) bool {
	createOrWrite := containsEnglishSemanticTerm(lower, "create", "write", "new") || containsAny(lower, "创建", "新建", "写入")
	if createOrWrite && !containsEnglishSemanticTerm(lower, "slide") && !containsAny(lower, "幻灯片") {
		return true
	}
	deleteOrRemove := containsEnglishSemanticTerm(lower, "delete", "remove") || containsAny(lower, "删除", "移除")
	return deleteOrRemove && !containsEnglishSemanticTerm(lower, "paragraph", "row", "slide", "page") &&
		!containsAny(lower, "段落", "行", "幻灯片", "页面")
}

func documentReadIntent(lower string) bool {
	return containsEnglishSemanticTerm(lower, "read", "summarize", "inspect", "explain") ||
		containsAny(lower, "读取", "阅读", "查看", "总结", "概括")
}

func recentDocumentContextPath(snapshot agentContextSnapshot) string {
	for index := len(snapshot.ToolResults) - 1; index >= 0; index-- {
		call := snapshot.ToolResults[index]
		for _, value := range []string{stringValue(call.Arguments["output_path"]), stringValue(call.Arguments["path"])} {
			value = strings.TrimSpace(value)
			if value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return ""
}

func preflightDocumentPath(workspaceRoot, requestedPath string, edit bool) (documentPreflight, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return documentPreflight{}, errors.New("workspace root is unavailable")
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return documentPreflight{}, errors.New("workspace root is invalid")
	}
	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" {
		return documentPreflight{}, errors.New("document path is required")
	}
	candidate := filepath.Clean(filepath.FromSlash(requestedPath))
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil || candidate == root || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return documentPreflight{}, errors.New("document path escapes the workspace")
	}
	relativeCandidate, err := filepath.Rel(root, candidate)
	if err != nil || relativeCandidate == "." || strings.HasPrefix(relativeCandidate, ".."+string(os.PathSeparator)) {
		return documentPreflight{}, errors.New("document path escapes the workspace")
	}
	current := root
	for _, component := range strings.Split(relativeCandidate, string(os.PathSeparator)) {
		current = filepath.Join(current, component)
		componentInfo, componentErr := os.Lstat(current)
		if componentErr != nil {
			return documentPreflight{}, errors.New("document path is unavailable")
		}
		if componentInfo.Mode()&os.ModeSymlink != 0 {
			return documentPreflight{}, errors.New("document path must not traverse symlinks")
		}
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		return documentPreflight{}, errors.New("document path is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return documentPreflight{}, errors.New("document path must be a regular non-symlink file")
	}
	format, err := detectDocumentFormat(candidate)
	if err != nil {
		return documentPreflight{}, err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return documentPreflight{}, errors.New("document path escapes the workspace")
	}
	result := documentPreflight{InputRef: filepath.ToSlash(relative), Format: format}
	if edit {
		if format != app.DocumentFormatDOCX && format != app.DocumentFormatXLSX && format != app.DocumentFormatPPTX && format != app.DocumentFormatPDF {
			return documentPreflight{}, fmt.Errorf("document format %q is read-only in revision 1", format)
		}
		extension := filepath.Ext(relative)
		base := strings.TrimSuffix(filepath.Base(relative), extension) + "-sparkclaw-edit" + extension
		result.OutputRef = filepath.ToSlash(filepath.Join(filepath.Dir(relative), base))
		outputPath := filepath.Join(root, filepath.FromSlash(result.OutputRef))
		if _, err := os.Lstat(outputPath); err == nil {
			return documentPreflight{}, errors.New("document output copy already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return documentPreflight{}, errors.New("document output path is unavailable")
		}
	}
	return result, nil
}

func detectDocumentFormat(path string) (string, error) {
	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".pdf":
		raw, err := readDocumentPrefix(path, 8)
		if err != nil || len(raw) < 5 || string(raw[:5]) != "%PDF-" {
			return "", errors.New("PDF extension and signature do not match")
		}
		return app.DocumentFormatPDF, nil
	case ".docx", ".xlsx", ".pptx":
		archive, err := zip.OpenReader(path)
		if err != nil {
			return "", errors.New("Office extension and ZIP signature do not match")
		}
		defer archive.Close()
		required := map[string]string{".docx": "word/document.xml", ".xlsx": "xl/workbook.xml", ".pptx": "ppt/presentation.xml"}[extension]
		for _, file := range archive.File {
			if file.Name == required {
				return strings.TrimPrefix(extension, "."), nil
			}
		}
		return "", errors.New("Office extension and package type do not match")
	default:
		raw, err := readDocumentPrefix(path, 4096)
		if err != nil {
			return "", errors.New("document cannot be read for type inspection")
		}
		if strings.IndexByte(string(raw), 0) >= 0 {
			return "", fmt.Errorf("unsupported document extension %q", extension)
		}
		return app.DocumentFormatText, nil
	}
}

func readDocumentPrefix(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit))
}

func documentOperationForContent(format, lower string) string {
	switch format {
	case app.DocumentFormatDOCX:
		switch {
		case containsEnglishSemanticTerm(lower, "insert") || containsAny(lower, "插入", "新增段落"):
			return "insert_paragraph"
		case containsEnglishSemanticTerm(lower, "delete", "remove") || containsAny(lower, "删除段落"):
			return "delete_paragraph"
		case containsEnglishSemanticTerm(lower, "style", "bold") || containsAny(lower, "样式", "加粗"):
			return "set_text_style"
		case containsEnglishSemanticTerm(lower, "replace") || containsAny(lower, "替换"):
			return "replace_paragraph"
		}
	case app.DocumentFormatXLSX:
		switch {
		case containsEnglishSemanticTerm(lower, "append") || containsAny(lower, "追加"):
			return "append_row"
		case containsEnglishSemanticTerm(lower, "insert") || containsAny(lower, "插入行"):
			return "insert_row"
		case containsEnglishSemanticTerm(lower, "delete", "remove") || containsAny(lower, "删除行"):
			return "delete_row"
		case containsEnglishSemanticTerm(lower, "row") || containsAny(lower, "整行", "这一行"):
			return "update_row"
		case containsEnglishSemanticTerm(lower, "cell", "update", "change", "edit", "modify") || containsAny(lower, "单元格", "修改", "改为", "更新"):
			return "update_cell"
		}
	case app.DocumentFormatPPTX:
		switch {
		case containsEnglishSemanticTerm(lower, "add", "insert") || containsAny(lower, "新增幻灯片", "添加幻灯片"):
			return "add_slide"
		case containsEnglishSemanticTerm(lower, "duplicate", "copy") || containsAny(lower, "复制幻灯片"):
			return "duplicate_slide"
		case containsEnglishSemanticTerm(lower, "delete", "remove") || containsAny(lower, "删除幻灯片"):
			return "delete_slide"
		case containsEnglishSemanticTerm(lower, "replace") || containsAny(lower, "替换"):
			return "replace_text"
		}
	case app.DocumentFormatPDF:
		switch {
		case containsEnglishSemanticTerm(lower, "rotate") || containsAny(lower, "旋转"):
			return "rotate_pages"
		case containsEnglishSemanticTerm(lower, "extract") || containsAny(lower, "提取页面"):
			return "extract_pages"
		case containsEnglishSemanticTerm(lower, "delete", "remove") || containsAny(lower, "删除页面"):
			return "delete_pages"
		case containsEnglishSemanticTerm(lower, "split") || containsAny(lower, "拆分"):
			return "split"
		}
	}
	return ""
}

func normalizeBrowserURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimSpace(raw)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}
