package agent

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

type documentPreflight struct {
	InputRef  string
	OutputRef string
	Format    string
}

func recognizeDocumentRoute(input workflowRecognitionContext, edit bool) (workflowRecognition, bool) {
	content := semanticRoutingContent(input.Content)
	lower := strings.ToLower(content)
	wantsEdit := documentContentMutationRequested(lower)
	wantsRead := documentInformationRequested(content, lower) && !wantsEdit
	if edit && !wantsEdit || !edit && !wantsRead {
		return workflowRecognition{}, false
	}
	if edit && unsupportedDocumentEditIntent(lower) {
		return workflowRecognition{}, false
	}
	paths := documentRoutePaths(content)
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
		if edit && strings.Contains(err.Error(), "read-only") {
			return workflowRecognition{}, false
		}
		return workflowRecognition{
			Status: app.RouteBlocked, Confidence: 0.95,
			Reason: "Document preflight failed: " + err.Error(),
		}, true
	}
	if preflight.Format == app.DocumentFormatImage && (hasExplicitExternalSendSignal(content) || !imageInspectCanFinalize(content)) {
		return workflowRecognition{}, false
	}
	operation := app.RouteOperationRead
	facts := map[string]string{"path": preflight.InputRef, "document_format": preflight.Format}
	if edit {
		operation = app.RouteOperationEdit
		if preflight.Format == app.DocumentFormatPDF {
			operation = app.RouteOperationTransform
		}
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

func documentContentMutationRequested(lower string) bool {
	return containsEnglishSemanticTerm(lower,
		"edit", "modify", "replace", "update", "change", "insert", "append", "add", "delete", "remove",
		"revise", "improve", "polish", "rewrite", "fill", "correct", "adjust", "transform", "rotate", "split",
	) || containsAny(lower,
		"编辑", "修改", "替换", "更新", "改为", "插入", "追加", "新增", "添加", "增加", "删除", "移除",
		"完善", "润色", "改写", "补充", "填写", "填入", "调整", "修订", "修正", "转换", "旋转", "拆分",
	)
}

func unsupportedDocumentEditIntent(lower string) bool {
	createOrWrite := containsEnglishSemanticTerm(lower, "create", "write", "new") || containsAny(lower, "创建", "新建", "写入")
	explicitEdit := containsEnglishSemanticTerm(lower, "edit", "modify", "replace", "update", "change", "revise", "improve", "polish") ||
		containsAny(lower, "编辑", "修改", "替换", "更新", "改为", "完善", "润色")
	if createOrWrite && !explicitEdit && !containsEnglishSemanticTerm(lower, "slide") && !containsAny(lower, "幻灯片") {
		return true
	}
	deleteOrRemove := containsEnglishSemanticTerm(lower, "delete", "remove") || containsAny(lower, "删除", "移除")
	contentTarget := containsEnglishSemanticTerm(lower, "content", "text", "paragraph", "row", "cell", "slide", "page", "chart", "image") ||
		containsAny(lower, "内容", "正文", "文字", "文本", "段落", "行", "单元格", "幻灯片", "页面", "图表", "图片")
	return deleteOrRemove && !contentTarget
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

func documentRoutePaths(content string) []string {
	if paths := attachmentPathsFromRoutingProjection(content); len(paths) > 0 {
		return paths
	}
	return extractPaths(content)
}

func attachmentPathsFromRoutingProjection(content string) []string {
	const header = "Attached files for this user turn:"
	index := strings.LastIndex(content, header)
	if index < 0 {
		return nil
	}
	seen := map[string]bool{}
	paths := []string{}
	for _, line := range strings.Split(content[index+len(header):], "\n") {
		pathIndex := strings.Index(line, " path=")
		if pathIndex < 0 {
			continue
		}
		value := line[pathIndex+len(" path="):]
		for _, suffix := range []string{" content_type=", " bytes=", " size=", " sha256=", " media_kind="} {
			if suffixIndex := strings.Index(value, suffix); suffixIndex >= 0 {
				value = value[:suffixIndex]
			}
		}
		value = filepath.Clean(strings.TrimSpace(value))
		if value == "" || value == "." || seen[value] {
			continue
		}
		seen[value] = true
		paths = append(paths, value)
	}
	return paths
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
	format, err := document.DetectFormat(candidate)
	if err != nil {
		return documentPreflight{}, err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return documentPreflight{}, errors.New("document path escapes the workspace")
	}
	result := documentPreflight{InputRef: filepath.ToSlash(relative), Format: format}
	if edit {
		if format != app.DocumentFormatText && format != app.DocumentFormatDOCX && format != app.DocumentFormatXLSX && format != app.DocumentFormatPPTX && format != app.DocumentFormatPDF {
			return documentPreflight{}, fmt.Errorf("document format %q is read-only", format)
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
