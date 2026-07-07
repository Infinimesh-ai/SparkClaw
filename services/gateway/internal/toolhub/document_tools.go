package toolhub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type textReplacement struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

type replacementDetail struct {
	Find  string `json:"find"`
	Count int    `json:"count"`
}

func isStructuredDocumentPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx", ".pptx", ".xlsx", ".pdf":
		return true
	default:
		return false
	}
}

func (h *ToolHub) readStructuredDocument(ctx context.Context, path string, maxBytes int) (Result, error) {
	ext := strings.ToLower(filepath.Ext(path))
	var out map[string]any
	var err error
	switch ext {
	case ".docx":
		out, err = runOfficeReadAdapter(ctx, ext, map[string]any{
			"path":      path,
			"max_bytes": maxBytes,
		})
	case ".pptx", ".xlsx":
		out, err = runOfficeReadAdapter(ctx, ext, map[string]any{
			"path":      path,
			"max_bytes": maxBytes,
		})
	case ".pdf":
		out, err = runPDFPython(ctx, map[string]any{
			"operation": "read",
			"path":      path,
			"max_bytes": maxBytes,
		})
	default:
		err = fmt.Errorf("unsupported structured document extension %s", ext)
	}
	if err != nil {
		return Result{}, err
	}
	content := stringArg(out, "content", "")
	truncated := boolArg(out, "truncated", false)
	document, ok := out["document"].(map[string]any)
	if !ok || document == nil {
		document = map[string]any{}
	}
	document["format"] = strings.TrimPrefix(ext, ".")
	if strings.TrimSpace(stringArg(document, "schema_version", "")) == "" {
		document["schema_version"] = "document_read_v1"
	}
	format := strings.TrimPrefix(ext, ".")
	relPath := h.workspaceRelPath(path)
	normalizeDocumentReadEnvelope(document, format, truncated, maxBytes)
	attachEvidenceBlocks(document, relPath, format)
	attachSmallDocumentPipeline(document, relPath, format, content, truncated, maxBytes)
	output := map[string]any{
		"path":         path,
		"rel_path":     relPath,
		"already_read": true,
		"kind":         format,
		"content":      content,
		"bytes":        len([]byte(content)),
		"max_bytes":    maxBytes,
		"truncated":    truncated,
		"untrusted":    true,
		"document":     document,
	}
	return Result{Output: output}, nil
}

func normalizeDocumentReadEnvelope(document map[string]any, format string, truncated bool, maxBytes int) {
	document["format"] = format
	if strings.TrimSpace(stringArg(document, "schema_version", "")) == "" {
		document["schema_version"] = "document_read_v1"
	}
	if _, ok := document["blocks"]; !ok {
		document["blocks"] = []any{}
	}
	if _, ok := document["content_scope"]; !ok {
		document["content_scope"] = map[string]any{
			"kind":     "full_document",
			"complete": !truncated,
		}
	}
	if _, ok := document["strategy"]; !ok {
		mode := "full"
		reason := "adapter_full_read"
		if truncated {
			mode = "byte_limited"
			reason = "max_bytes_exceeded"
		}
		document["strategy"] = map[string]any{
			"mode":       mode,
			"reason":     reason,
			"complete":   !truncated,
			"max_bytes":  maxBytes,
			"extensible": true,
		}
	}
	stats, ok := document["stats"].(map[string]any)
	if !ok || stats == nil {
		stats = map[string]any{}
		document["stats"] = stats
	}
	if _, ok := stats["complete"]; !ok {
		stats["complete"] = !truncated
	}
}

func (h *ToolHub) officeReplaceText(ctx context.Context, args map[string]any) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	if inputPath == outputPath {
		return Result{}, errors.New("output_path must not overwrite the input file")
	}
	replacements, err := replacementArgs(args["replacements"])
	if err != nil {
		return Result{}, err
	}
	ext := strings.ToLower(filepath.Ext(inputPath))
	if ext != ".docx" && ext != ".xlsx" && ext != ".pptx" {
		return Result{}, errors.New("office.replace_text supports only .docx, .xlsx, and .pptx")
	}
	if strings.ToLower(filepath.Ext(outputPath)) != ext {
		return Result{}, fmt.Errorf("output_path must use %s extension", ext)
	}
	out, err := runOfficeAdapter(ctx, ext, map[string]any{
		"path":         inputPath,
		"output_path":  outputPath,
		"replacements": replacements,
	})
	if err != nil {
		return Result{}, err
	}
	total := intArg(out, "replacements", 0)
	expected := intArg(args, "expected_replacements", 0)
	if expected > 0 && expected != total {
		return Result{}, fmt.Errorf("expected %d replacements, got %d", expected, total)
	}
	if total == 0 {
		return Result{}, errors.New("no replacements were made")
	}
	return Result{Output: map[string]any{
		"status":       "office_version_written",
		"path":         inputPath,
		"output_path":  outputPath,
		"replacements": total,
		"bytes":        intArg(out, "bytes", fileSize(outputPath)),
		"details":      out["details"],
		"untrusted":    true,
	}}, nil
}

func (h *ToolHub) docxStructureEdit(ctx context.Context, operation string, args map[string]any) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	if strings.ToLower(filepath.Ext(inputPath)) != ".docx" {
		return Result{}, errors.New(operation + " supports only .docx files")
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	if inputPath == outputPath {
		return Result{}, errors.New("output_path must not overwrite the input file")
	}
	if strings.ToLower(filepath.Ext(outputPath)) != ".docx" {
		return Result{}, errors.New("output_path must use .docx extension")
	}
	if operation == "replace_paragraph" && strings.TrimSpace(stringArg(args, "old_text", "")) == "" && strings.TrimSpace(stringArg(args, "source_hash", "")) == "" {
		return Result{}, errors.New("docx.replace_paragraph requires old_text or source_hash preflight evidence")
	}
	request := map[string]any{
		"operation":       operation,
		"path":            inputPath,
		"output_path":     outputPath,
		"paragraph_index": intArg(args, "paragraph_index", 0),
		"position":        stringArg(args, "position", ""),
		"old_text":        stringArg(args, "old_text", ""),
		"source_hash":     stringArg(args, "source_hash", ""),
		"text":            stringArg(args, "text", ""),
		"style":           args["style"],
		"location":        args["location"],
	}
	out, err := runDocxStructureAdapter(ctx, request)
	if err != nil {
		return Result{}, err
	}
	output := map[string]any{
		"status":          stringArg(out, "status", "docx_version_written"),
		"operation":       operation,
		"path":            inputPath,
		"output_path":     outputPath,
		"bytes":           intArg(out, "bytes", fileSize(outputPath)),
		"paragraph_index": intArg(out, "paragraph_index", intArg(args, "paragraph_index", 0)),
		"position":        stringArg(out, "position", stringArg(args, "position", "")),
		"before":          stringArg(out, "before", ""),
		"source_hash":     stringArg(out, "source_hash", ""),
		"text":            stringArg(out, "text", stringArg(args, "text", "")),
		"style":           objectOrEmpty(out["style"]),
		"untrusted":       true,
	}
	if location, ok := out["location"].(map[string]any); ok {
		output["location"] = location
	} else if location, ok := args["location"].(map[string]any); ok {
		output["location"] = location
	}
	return Result{Output: output}, nil
}

func (h *ToolHub) pptxSlideEdit(ctx context.Context, operation string, args map[string]any) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	if strings.ToLower(filepath.Ext(inputPath)) != ".pptx" {
		return Result{}, errors.New(operation + " supports only .pptx files")
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	if inputPath == outputPath {
		return Result{}, errors.New("output_path must not overwrite the input file")
	}
	if strings.ToLower(filepath.Ext(outputPath)) != ".pptx" {
		return Result{}, errors.New("output_path must use .pptx extension")
	}
	request := map[string]any{
		"operation":    operation,
		"path":         inputPath,
		"output_path":  outputPath,
		"slide_index":  intArg(args, "slide_index", 0),
		"layout_index": intArg(args, "layout_index", 0),
		"title":        stringArg(args, "title", ""),
		"body":         stringArg(args, "body", ""),
	}
	out, err := runPptxSlideAdapter(ctx, request)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"status":               stringArg(out, "status", "pptx_version_written"),
		"operation":            operation,
		"path":                 inputPath,
		"output_path":          outputPath,
		"bytes":                intArg(out, "bytes", fileSize(outputPath)),
		"slides":               intArg(out, "slides", 0),
		"slide_index":          intArg(out, "slide_index", intArg(args, "slide_index", 0)),
		"inserted_slide_index": intArg(out, "inserted_slide_index", 0),
		"layout_index":         intArg(out, "layout_index", intArg(args, "layout_index", 0)),
		"title":                stringArg(out, "title", stringArg(args, "title", "")),
		"body":                 stringArg(out, "body", stringArg(args, "body", "")),
		"untrusted":            true,
	}}, nil
}

func (h *ToolHub) xlsxStructureEdit(ctx context.Context, operation string, args map[string]any) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	if strings.ToLower(filepath.Ext(inputPath)) != ".xlsx" {
		return Result{}, errors.New(operation + " supports only .xlsx files")
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	if inputPath == outputPath {
		return Result{}, errors.New("output_path must not overwrite the input file")
	}
	if strings.ToLower(filepath.Ext(outputPath)) != ".xlsx" {
		return Result{}, errors.New("output_path must use .xlsx extension")
	}
	request := map[string]any{
		"operation":   operation,
		"path":        inputPath,
		"output_path": outputPath,
		"sheet":       stringArg(args, "sheet", ""),
		"cell":        stringArg(args, "cell", ""),
		"row":         intArg(args, "row", 0),
		"position":    stringArg(args, "position", ""),
		"value":       args["value"],
		"values":      args["values"],
	}
	out, err := runXlsxStructureAdapter(ctx, request)
	if err != nil {
		return Result{}, err
	}
	output := map[string]any{
		"status":       stringArg(out, "status", "xlsx_version_written"),
		"operation":    operation,
		"path":         inputPath,
		"output_path":  outputPath,
		"bytes":        intArg(out, "bytes", fileSize(outputPath)),
		"sheet":        stringArg(out, "sheet", stringArg(args, "sheet", "")),
		"cell":         stringArg(out, "cell", stringArg(args, "cell", "")),
		"row":          intArg(out, "row", intArg(args, "row", 0)),
		"inserted_row": intArg(out, "inserted_row", 0),
		"untrusted":    true,
	}
	if value, ok := out["value"]; ok {
		output["value"] = value
	}
	if values, ok := out["values"]; ok {
		output["values"] = values
	}
	return Result{Output: output}, nil
}

func (h *ToolHub) pdfExtractText(ctx context.Context, args map[string]any) (Result, error) {
	path, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	if strings.ToLower(filepath.Ext(path)) != ".pdf" {
		return Result{}, errors.New("pdf.extract_text supports only .pdf files")
	}
	maxBytes := intArg(args, "max_bytes", 20000)
	if maxBytes <= 0 || maxBytes > 200000 {
		maxBytes = 20000
	}
	out, err := runPDFPython(ctx, map[string]any{
		"operation": "extract_text",
		"path":      path,
		"max_bytes": maxBytes,
	})
	if err != nil {
		return Result{}, err
	}
	content := stringArg(out, "content", "")
	truncated := boolArg(out, "truncated", false)
	return Result{Output: map[string]any{
		"path":                path,
		"content":             content,
		"bytes":               len([]byte(content)),
		"truncated":           truncated,
		"untrusted":           true,
		"scanned_unsupported": boolArg(out, "scanned_unsupported", false),
	}}, nil
}

func (h *ToolHub) pdfTransform(ctx context.Context, args map[string]any) (Result, error) {
	operation := stringArg(args, "operation", "")
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	if strings.ToLower(filepath.Ext(outputPath)) != ".pdf" {
		return Result{}, errors.New("output_path must use .pdf extension")
	}
	request := map[string]any{
		"operation":   operation,
		"output_path": outputPath,
		"pages":       args["pages"],
		"rotation":    args["rotation"],
		"text":        args["text"],
	}
	if operation == "merge" {
		inputs, err := resolveStringPaths(h, args["inputs"])
		if err != nil {
			return Result{}, err
		}
		if len(inputs) < 2 {
			return Result{}, errors.New("merge requires at least two inputs")
		}
		request["inputs"] = inputs
	} else {
		path, err := h.resolvePath(stringArg(args, "path", ""))
		if err != nil {
			return Result{}, err
		}
		if strings.ToLower(filepath.Ext(path)) != ".pdf" {
			return Result{}, errors.New("pdf.transform path must be a .pdf file")
		}
		if path == outputPath {
			return Result{}, errors.New("output_path must not overwrite the input file")
		}
		request["path"] = path
	}
	out, err := runPDFPython(ctx, request)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"status":      stringArg(out, "status", "pdf_version_written"),
		"operation":   operation,
		"path":        stringArg(out, "path", ""),
		"inputs":      outputStringArray(out["inputs"]),
		"output_path": outputPath,
		"outputs":     outputStringArray(out["outputs"]),
		"bytes":       intArg(out, "bytes", fileSize(outputPath)),
		"pages":       intArg(out, "pages", 0),
	}}, nil
}

func (h *ToolHub) resolveNewOutputPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("output_path is required")
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) && strings.HasPrefix(clean, "..") {
		return "", errors.New("output_path cannot escape workspace")
	}
	abs, err := h.resolvePath(clean)
	if err != nil {
		return "", err
	}
	if strings.Contains(abs, string(os.PathSeparator)+".sparkclaw"+string(os.PathSeparator)+"state") {
		return "", errors.New("output_path cannot target SparkClaw control files")
	}
	return abs, nil
}

func replacementArgs(value any) ([]textReplacement, error) {
	items, ok := arrayItems(value)
	if !ok || len(items) == 0 {
		return nil, errors.New("replacements must be a non-empty array")
	}
	out := make([]textReplacement, 0, len(items))
	for i, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("replacements[%d] must be object", i)
		}
		find := strings.TrimSpace(stringArg(object, "find", ""))
		replace := stringArg(object, "replace", "")
		if find == "" {
			return nil, fmt.Errorf("replacements[%d].find cannot be empty", i)
		}
		out = append(out, textReplacement{Find: find, Replace: replace})
	}
	return out, nil
}

func resolveStringPaths(h *ToolHub, value any) ([]string, error) {
	items, ok := arrayItems(value)
	if !ok {
		return nil, errors.New("inputs must be an array")
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, errors.New("inputs must contain only strings")
		}
		path, err := h.resolvePath(text)
		if err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, nil
}

func outputStringArray(value any) []string {
	items, ok := arrayItems(value)
	if !ok {
		return []string{}
	}
	out := []string{}
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func objectOrEmpty(value any) map[string]any {
	if object, ok := value.(map[string]any); ok && object != nil {
		return object
	}
	return map[string]any{}
}

func runOfficeAdapter(ctx context.Context, ext string, request map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	switch ext {
	case ".docx":
		cmd = exec.CommandContext(ctx, documentPythonBinary(), "-c", docxAdapterScript)
	case ".pptx":
		cmd = exec.CommandContext(ctx, documentPythonBinary(), "-c", pptxAdapterScript)
	case ".xlsx":
		cmd = exec.CommandContext(ctx, documentNodeBinary(), "-e", xlsxAdapterScript)
		cmd.Env = append(os.Environ(), "NODE_PATH="+documentNodeModulesPath())
	default:
		return nil, fmt.Errorf("unsupported office extension %s", ext)
	}
	cmd.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, err
	}
	if errText := stringArg(out, "error", ""); errText != "" {
		return nil, errors.New(errText)
	}
	return out, nil
}

func runOfficeReadAdapter(ctx context.Context, ext string, request map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	switch ext {
	case ".docx":
		cmd = exec.CommandContext(ctx, documentPythonBinary(), "-c", docxReadAdapterScript)
	case ".pptx":
		cmd = exec.CommandContext(ctx, documentPythonBinary(), "-c", pptxReadAdapterScript)
	case ".xlsx":
		cmd = exec.CommandContext(ctx, documentNodeBinary(), "-e", xlsxReadAdapterScript)
		cmd.Env = append(os.Environ(), "NODE_PATH="+documentNodeModulesPath())
	default:
		return nil, fmt.Errorf("unsupported office extension %s", ext)
	}
	cmd.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, err
	}
	if errText := stringArg(out, "error", ""); errText != "" {
		return nil, errors.New(errText)
	}
	return out, nil
}

func runDocxStructureAdapter(ctx context.Context, request map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, documentPythonBinary(), "-c", docxStructureAdapterScript)
	cmd.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, err
	}
	if errText := stringArg(out, "error", ""); errText != "" {
		return nil, errors.New(errText)
	}
	return out, nil
}

func runPptxSlideAdapter(ctx context.Context, request map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, documentPythonBinary(), "-c", pptxSlideAdapterScript)
	cmd.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, err
	}
	if errText := stringArg(out, "error", ""); errText != "" {
		return nil, errors.New(errText)
	}
	return out, nil
}

func runXlsxStructureAdapter(ctx context.Context, request map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, documentNodeBinary(), "-e", xlsxStructureAdapterScript)
	cmd.Env = append(os.Environ(), "NODE_PATH="+documentNodeModulesPath())
	cmd.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, err
	}
	if errText := stringArg(out, "error", ""); errText != "" {
		return nil, errors.New(errText)
	}
	return out, nil
}

func runPDFPython(ctx context.Context, request map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, documentPythonBinary(), "-c", pdfAdapterScript)
	cmd.Stdin = bytes.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, err
	}
	if errText := stringArg(out, "error", ""); errText != "" {
		return nil, errors.New(errText)
	}
	return out, nil
}

func documentPythonBinary() string {
	path := findLocalToolPath(filepath.Join(".tools", "document-python", "bin", "python"))
	if path != "" {
		return path
	}
	return "python3"
}

func documentNodeBinary() string {
	path := findLocalToolPath(filepath.Join(".tools", "node-v24.14.0-darwin-arm64", "bin", "node"))
	if path != "" {
		return path
	}
	return "node"
}

func documentNodeModulesPath() string {
	path := findLocalToolPath(filepath.Join(".tools", "document-node", "node_modules"))
	if path != "" {
		return path
	}
	path, err := filepath.Abs(filepath.Join(".tools", "document-node", "node_modules"))
	if err != nil {
		return filepath.Join(".tools", "document-node", "node_modules")
	}
	return path
}

func findLocalToolPath(rel string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(cwd, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		next := filepath.Dir(cwd)
		if next == cwd {
			return ""
		}
		cwd = next
	}
}

const docxReadAdapterScript = `
import json, sys
try:
    import docx
except Exception:
    print(json.dumps({"error":"DOCX reader requires python-docx"}))
    sys.exit(0)

req = json.load(sys.stdin)
max_bytes = int(req.get("max_bytes") or 20000)

def trim(text):
    return " ".join(str(text or "").split())

try:
    document = docx.Document(req["path"])
    paragraphs = []
    blocks = []
    tables = []
    lines = []
    block_index = 0

    for index, paragraph in enumerate(document.paragraphs, start=1):
        text = trim(paragraph.text)
        if not text:
            continue
        block_index += 1
        location = {
            "part": "document",
            "block_type": "paragraph",
            "block_index": block_index,
            "paragraph_index": index,
            "table_index": 0,
            "row_index": 0,
            "cell_index": 0,
            "cell_paragraph_index": 0,
            "path": "document.p[%d]" % index,
        }
        style = paragraph.style.name if paragraph.style is not None else ""
        item = {"index": index, "text": text, "style": style, "location": location}
        paragraphs.append(item)
        blocks.append({"text": text, "style": style, "location": location})
        lines.append(text)

    for table_index, table in enumerate(document.tables, start=1):
        rows = []
        for row_index, row in enumerate(table.rows, start=1):
            row_values = []
            for cell_index, cell in enumerate(row.cells, start=1):
                cell_texts = []
                for cell_paragraph_index, paragraph in enumerate(cell.paragraphs, start=1):
                    text = trim(paragraph.text)
                    if not text:
                        continue
                    cell_texts.append(text)
                    block_index += 1
                    location = {
                        "part": "document",
                        "block_type": "table_cell",
                        "block_index": block_index,
                        "paragraph_index": 0,
                        "table_index": table_index,
                        "row_index": row_index,
                        "cell_index": cell_index,
                        "cell_paragraph_index": cell_paragraph_index,
                        "path": "document.table[%d].row[%d].cell[%d].p[%d]" % (table_index, row_index, cell_index, cell_paragraph_index),
                    }
                    blocks.append({"text": text, "style": "", "location": location})
                    lines.append(text)
                row_values.append("\n".join(cell_texts))
            rows.append(row_values)
        tables.append({"index": table_index, "rows": rows})

    content = "\n".join(lines).strip()
    raw = content.encode("utf-8")
    truncated = len(raw) > max_bytes
    if truncated:
        content = raw[:max_bytes].decode("utf-8", errors="ignore")
    print(json.dumps({
        "content": content,
        "truncated": truncated,
        "document": {
            "schema_version": "document_read_v1",
            "format": "docx",
            "source": "python_docx",
            "blocks": blocks,
            "paragraphs": paragraphs,
            "tables": tables,
            "stats": {
                "blocks": len(blocks),
                "paragraphs": len(paragraphs),
                "tables": len(tables),
                "complete": not truncated,
            }
        }
    }, ensure_ascii=False))
except Exception as e:
    print(json.dumps({"error":str(e)}))
`

const docxStructureAdapterScript = `
import hashlib, json, sys, os
try:
    from docx import Document
    from docx.shared import Pt
except Exception:
    print(json.dumps({"error":"DOCX structure adapter requires python-docx"}))
    sys.exit(0)

req = json.load(sys.stdin)
op = req.get("operation")

def trim(text):
    return " ".join(str(text or "").split())

def source_hash(text):
    return "sha1:" + hashlib.sha1(trim(text).encode("utf-8")).hexdigest()

def requested_location():
    loc = req.get("location")
    if loc in (None, ""):
        return None
    if not isinstance(loc, dict):
        raise ValueError("location must be an object")
    part = str(loc.get("part") or "document")
    block_type = str(loc.get("block_type") or "")
    if part != "document":
        raise ValueError("only document part locations are currently editable")
    if block_type != "paragraph":
        raise ValueError("only top-level paragraph locations are currently editable")
    idx = int(loc.get("paragraph_index") or 0)
    if idx <= 0:
        raise ValueError("location.paragraph_index must be a positive 1-based integer")
    return loc

def paragraph_index():
    loc = requested_location()
    if loc is not None:
        return int(loc.get("paragraph_index") or 0)
    idx = int(req.get("paragraph_index") or 0)
    if idx <= 0:
        raise ValueError("paragraph_index or location must identify a positive 1-based paragraph")
    return idx

def paragraph_at(doc, idx):
    if idx < 1 or idx > len(doc.paragraphs):
        raise ValueError("paragraph_index out of range: %s" % idx)
    return doc.paragraphs[idx - 1]

def preflight_paragraph(paragraph):
    before = trim(paragraph.text)
    expected = trim(req.get("old_text") or "")
    if expected and before != expected:
        raise ValueError("old_text mismatch at target paragraph")
    expected_hash = str(req.get("source_hash") or "").strip()
    actual_hash = source_hash(before)
    if expected_hash and actual_hash != expected_hash:
        raise ValueError("source_hash mismatch at target paragraph")
    return before, actual_hash

def clear_paragraph(paragraph):
    for run in paragraph.runs:
        run.text = ""
    if not paragraph.runs:
        paragraph.add_run("")

def set_paragraph_text(paragraph, text):
    clear_paragraph(paragraph)
    paragraph.runs[0].text = text

def insert_paragraph(doc, position, idx, text):
    position = (position or "").strip().lower()
    if position == "start":
        if doc.paragraphs:
            return doc.paragraphs[0].insert_paragraph_before(text)
        return doc.add_paragraph(text)
    if position == "end":
        return doc.add_paragraph(text)
    if position in ("before", "after"):
        paragraph = paragraph_at(doc, idx)
        if position == "before":
            return paragraph.insert_paragraph_before(text)
        inserted = paragraph.insert_paragraph_before(text)
        paragraph._p.addnext(inserted._p)
        return inserted
    raise ValueError("position must be one of start, end, before, after")

def delete_paragraph(paragraph):
    element = paragraph._element
    parent = element.getparent()
    parent.remove(element)
    paragraph._p = paragraph._element = None

def apply_style(paragraph, style_req):
    if not isinstance(style_req, dict):
        raise ValueError("style must be an object")
    applied = {}
    builtin_style = str(style_req.get("builtin_style") or "").strip()
    if builtin_style:
        paragraph.style = builtin_style
        applied["builtin_style"] = builtin_style
    bold = style_req.get("bold")
    font_size = style_req.get("font_size_pt")
    if bold is not None or font_size is not None:
        if not paragraph.runs:
            paragraph.add_run("")
        for run in paragraph.runs:
            if bold is not None:
                run.bold = bool(bold)
            if font_size is not None:
                size = int(font_size)
                if size <= 0 or size > 200:
                    raise ValueError("font_size_pt must be between 1 and 200")
                run.font.size = Pt(size)
        if bold is not None:
            applied["bold"] = bool(bold)
        if font_size is not None:
            applied["font_size_pt"] = int(font_size)
    if not applied:
        raise ValueError("style must contain builtin_style, bold, or font_size_pt")
    return applied

try:
    doc = Document(req["path"])
    text = str(req.get("text") or "")
    result = {
        "status": "docx_version_written",
        "operation": op,
        "path": req["path"],
        "output_path": req["output_path"]
    }
    loc = requested_location()
    if loc is not None:
        result["location"] = loc
    if op == "replace_paragraph":
        idx = paragraph_index()
        paragraph = paragraph_at(doc, idx)
        before, before_hash = preflight_paragraph(paragraph)
        set_paragraph_text(paragraph, text)
        result["paragraph_index"] = idx
        result["before"] = before
        result["source_hash"] = before_hash
        result["text"] = text
    elif op == "insert_paragraph":
        position = str(req.get("position") or "").strip().lower()
        idx = paragraph_index() if loc is not None else int(req.get("paragraph_index") or 0)
        if position in ("before", "after") and idx <= 0:
            raise ValueError("paragraph_index or location is required for before/after insertion")
        insert_paragraph(doc, position, idx, text)
        result["position"] = position
        result["text"] = text
        if position == "start":
            result["paragraph_index"] = 1
        elif position == "end":
            result["paragraph_index"] = len(doc.paragraphs)
        elif position == "before":
            result["paragraph_index"] = idx
        else:
            result["paragraph_index"] = idx + 1
    elif op == "delete_paragraph":
        idx = paragraph_index()
        paragraph = paragraph_at(doc, idx)
        result["paragraph_index"] = idx
        result["text"] = paragraph.text
        delete_paragraph(paragraph)
    elif op == "set_text_style":
        idx = paragraph_index()
        paragraph = paragraph_at(doc, idx)
        applied = apply_style(paragraph, req.get("style"))
        result["paragraph_index"] = idx
        result["style"] = applied
    else:
        raise ValueError("unsupported docx operation: %s" % op)
    os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
    doc.save(req["output_path"])
    result["bytes"] = os.path.getsize(req["output_path"])
    print(json.dumps(result, ensure_ascii=False))
except Exception as e:
    print(json.dumps({"error":str(e)}, ensure_ascii=False))
`

const pptxReadAdapterScript = `
import json, sys
try:
    from pptx import Presentation
except Exception:
    print(json.dumps({"error":"PPTX reader requires python-pptx"}))
    sys.exit(0)

req = json.load(sys.stdin)
max_bytes = int(req.get("max_bytes") or 20000)

def trim(text):
    return " ".join(str(text or "").split())

try:
    prs = Presentation(req["path"])
    slides = []
    lines = []
    for s_index, slide in enumerate(prs.slides, start=1):
        items = []
        for shape_index, shape in enumerate(slide.shapes, start=1):
            if hasattr(shape, "text") and trim(shape.text):
                items.append({
                    "shape_index": shape_index,
                    "type": "text",
                    "text": trim(shape.text)
                })
            if hasattr(shape, "table"):
                rows = []
                for r_index, row in enumerate(shape.table.rows, start=1):
                    rows.append({"index": r_index, "cells": [trim(cell.text) for cell in row.cells]})
                items.append({"shape_index": shape_index, "type": "table", "rows": rows})
        slides.append({"index": s_index, "items": items})
        if items:
            lines.append("Slide %d:" % s_index)
            for item in items:
                if item["type"] == "text":
                    lines.append(item["text"])
                elif item["type"] == "table":
                    for row in item["rows"]:
                        lines.append("\t".join(row["cells"]))
    content = "\n".join(lines).strip()
    raw = content.encode("utf-8")
    truncated = len(raw) > max_bytes
    if truncated:
        content = raw[:max_bytes].decode("utf-8", errors="ignore")
    print(json.dumps({
        "content": content,
        "truncated": truncated,
        "document": {
            "schema_version": "document_read_v1",
            "format": "pptx",
            "slides": slides,
            "stats": {"slides": len(slides)}
        }
    }, ensure_ascii=False))
except Exception as e:
    print(json.dumps({"error":str(e)}))
`

const xlsxReadAdapterScript = `
let ExcelJS;
try {
  ExcelJS = require("exceljs");
} catch (error) {
  console.log(JSON.stringify({ error: "XLSX reader requires exceljs" }));
  process.exit(0);
}

let raw = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", chunk => raw += chunk);
process.stdin.on("end", async () => {
  try {
    const req = JSON.parse(raw);
    const maxBytes = Number(req.max_bytes || 20000);
    const workbook = new ExcelJS.Workbook();
    await workbook.xlsx.readFile(req.path);
    const sheets = [];
    const lines = [];
    workbook.eachSheet((sheet) => {
      const rows = [];
      sheet.eachRow({ includeEmpty: false }, (row, rowNumber) => {
        const cells = [];
        row.eachCell({ includeEmpty: true }, (cell, colNumber) => {
          let value = cell.text;
          if (value === undefined || value === null) value = "";
          cells.push({ address: cell.address, row: rowNumber, column: colNumber, value: String(value) });
        });
        rows.push({ index: rowNumber, cells });
      });
      sheets.push({ name: sheet.name, index: sheet.id, rows });
      lines.push("Sheet: " + sheet.name);
      for (const row of rows) {
        lines.push(row.cells.map(cell => cell.value).join("\\t"));
      }
    });
    let content = lines.join("\\n").trim();
    const bytes = Buffer.byteLength(content, "utf8");
    const truncated = bytes > maxBytes;
    if (truncated) {
      content = Buffer.from(content, "utf8").subarray(0, maxBytes).toString("utf8");
    }
    console.log(JSON.stringify({
      content,
      truncated,
      document: {
        schema_version: "document_read_v1",
        format: "xlsx",
        sheets,
        stats: { sheets: sheets.length, rows: sheets.reduce((sum, sheet) => sum + sheet.rows.length, 0) }
      }
    }));
  } catch (error) {
    console.log(JSON.stringify({ error: String(error && error.message || error) }));
  }
});
`

func fileSize(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return int(info.Size())
}

const docxAdapterScript = `
import json, sys, os
try:
    from docx import Document
except Exception:
    print(json.dumps({"error":"DOCX adapter requires python-docx"}))
    sys.exit(0)

req = json.load(sys.stdin)
replacements = req.get("replacements") or []
counts = {item["Find"] if "Find" in item else item.get("find"): 0 for item in replacements}

def find_value(item):
    return item["Find"] if "Find" in item else item.get("find", "")

def replace_value(item):
    return item["Replace"] if "Replace" in item else item.get("replace", "")

def replace_in_paragraph(paragraph):
    total = 0
    for item in replacements:
        find = find_value(item)
        repl = replace_value(item)
        if not find:
            continue
        full = "".join(run.text for run in paragraph.runs)
        count = full.count(find)
        if count <= 0:
            continue
        next_text = full.replace(find, repl)
        for run in paragraph.runs:
            run.text = ""
        if paragraph.runs:
            paragraph.runs[0].text = next_text
        else:
            paragraph.add_run(next_text)
        counts[find] = counts.get(find, 0) + count
        total += count
    return total

try:
    doc = Document(req["path"])
    total = 0
    for paragraph in doc.paragraphs:
        total += replace_in_paragraph(paragraph)
    for table in doc.tables:
        for row in table.rows:
            for cell in row.cells:
                for paragraph in cell.paragraphs:
                    total += replace_in_paragraph(paragraph)
    missing = [find for find, count in counts.items() if count == 0]
    if missing:
        print(json.dumps({"error":"find text was not matched: " + ", ".join(repr(x) for x in missing)}))
        sys.exit(0)
    os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
    doc.save(req["output_path"])
    print(json.dumps({"replacements":total,"bytes":os.path.getsize(req["output_path"]),"details":[{"find":k,"count":v} for k,v in counts.items()]}))
except Exception as e:
    print(json.dumps({"error":str(e)}))
`

const pptxAdapterScript = `
import json, sys, os
try:
    from pptx import Presentation
except Exception:
    print(json.dumps({"error":"PPTX adapter requires python-pptx"}))
    sys.exit(0)

req = json.load(sys.stdin)
replacements = req.get("replacements") or []
counts = {item["Find"] if "Find" in item else item.get("find"): 0 for item in replacements}

def find_value(item):
    return item["Find"] if "Find" in item else item.get("find", "")

def replace_value(item):
    return item["Replace"] if "Replace" in item else item.get("replace", "")

def replace_in_text_frame(tf):
    total = 0
    for paragraph in tf.paragraphs:
        for item in replacements:
            find = find_value(item)
            repl = replace_value(item)
            if not find:
                continue
            full = "".join(run.text for run in paragraph.runs)
            count = full.count(find)
            if count <= 0:
                continue
            next_text = full.replace(find, repl)
            for run in paragraph.runs:
                run.text = ""
            if paragraph.runs:
                paragraph.runs[0].text = next_text
            else:
                paragraph.add_run().text = next_text
            counts[find] = counts.get(find, 0) + count
            total += count
    return total

try:
    prs = Presentation(req["path"])
    total = 0
    for slide in prs.slides:
        for shape in slide.shapes:
            if hasattr(shape, "text_frame") and shape.has_text_frame:
                total += replace_in_text_frame(shape.text_frame)
            if hasattr(shape, "table"):
                for row in shape.table.rows:
                    for cell in row.cells:
                        total += replace_in_text_frame(cell.text_frame)
    missing = [find for find, count in counts.items() if count == 0]
    if missing:
        print(json.dumps({"error":"find text was not matched: " + ", ".join(repr(x) for x in missing)}))
        sys.exit(0)
    os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
    prs.save(req["output_path"])
    print(json.dumps({"replacements":total,"bytes":os.path.getsize(req["output_path"]),"details":[{"find":k,"count":v} for k,v in counts.items()]}))
except Exception as e:
    print(json.dumps({"error":str(e)}))
`

const pptxSlideAdapterScript = `
import copy
import json
import os
import sys
try:
    from pptx import Presentation
except Exception:
    print(json.dumps({"error":"PPTX slide adapter requires python-pptx"}))
    sys.exit(0)

req = json.load(sys.stdin)
op = req.get("operation")

def positive_index(value, name):
    idx = int(value or 0)
    if idx <= 0:
        raise ValueError("%s must be a positive 1-based integer" % name)
    return idx

def slide_at(prs, idx):
    if idx < 1 or idx > len(prs.slides):
        raise ValueError("slide_index out of range: %s" % idx)
    return prs.slides[idx - 1]

def delete_slide(prs, idx):
    slide = slide_at(prs, idx)
    slide_id_list = prs.slides._sldIdLst
    slide_id = slide_id_list[idx - 1]
    rel_id = slide_id.rId
    slide_id_list.remove(slide_id)
    prs.part.drop_rel(rel_id)

def fill_text_placeholders(slide, title, body):
    title = str(title or "")
    body = str(body or "")
    if title and slide.shapes.title is not None:
        slide.shapes.title.text = title
    if body:
        for shape in slide.placeholders:
            if shape == slide.shapes.title:
                continue
            if hasattr(shape, "text_frame"):
                shape.text = body
                return
        left = top = width = height = None
        for shape in slide.shapes:
            if hasattr(shape, "text_frame") and shape != slide.shapes.title:
                shape.text = body
                return

def duplicate_slide(prs, idx):
    source = slide_at(prs, idx)
    blank_layout = prs.slide_layouts[6] if len(prs.slide_layouts) > 6 else prs.slide_layouts[0]
    dest = prs.slides.add_slide(blank_layout)
    for shape in source.shapes:
        dest.shapes._spTree.insert_element_before(copy.deepcopy(shape.element), 'p:extLst')
    for rel in source.part.rels.values():
        if "notesSlide" in rel.reltype:
            continue
        if rel.is_external:
            dest.part.rels.get_or_add_ext_rel(rel.reltype, rel.target_ref)
        else:
            dest.part.rels.get_or_add(rel.reltype, rel._target)
    slide_id_list = prs.slides._sldIdLst
    new_slide_id = slide_id_list[-1]
    slide_id_list.remove(new_slide_id)
    slide_id_list.insert(idx, new_slide_id)
    return dest

try:
    prs = Presentation(req["path"])
    result = {
        "status": "pptx_version_written",
        "operation": op,
        "path": req["path"],
        "output_path": req["output_path"]
    }
    if op == "add_slide":
        layout_index = int(req.get("layout_index") or 0)
        if layout_index < 0 or layout_index >= len(prs.slide_layouts):
            raise ValueError("layout_index out of range: %s" % layout_index)
        slide = prs.slides.add_slide(prs.slide_layouts[layout_index])
        fill_text_placeholders(slide, req.get("title"), req.get("body"))
        result["slide_index"] = len(prs.slides)
        result["layout_index"] = layout_index
        result["title"] = str(req.get("title") or "")
        result["body"] = str(req.get("body") or "")
    elif op == "duplicate_slide":
        idx = positive_index(req.get("slide_index"), "slide_index")
        duplicate_slide(prs, idx)
        result["slide_index"] = idx
        result["inserted_slide_index"] = idx + 1
    elif op == "delete_slide":
        idx = positive_index(req.get("slide_index"), "slide_index")
        if len(prs.slides) <= 1:
            raise ValueError("cannot delete the only slide")
        delete_slide(prs, idx)
        result["slide_index"] = idx
    else:
        raise ValueError("unsupported pptx operation: %s" % op)
    os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
    prs.save(req["output_path"])
    result["slides"] = len(prs.slides)
    result["bytes"] = os.path.getsize(req["output_path"])
    print(json.dumps(result, ensure_ascii=False))
except Exception as e:
    print(json.dumps({"error":str(e)}, ensure_ascii=False))
`

const xlsxAdapterScript = `
let ExcelJS;
try {
  ExcelJS = require("exceljs");
} catch (error) {
  console.log(JSON.stringify({ error: "XLSX adapter requires exceljs" }));
  process.exit(0);
}

let raw = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", chunk => raw += chunk);
process.stdin.on("end", async () => {
  try {
    const req = JSON.parse(raw);
    const replacements = req.replacements || [];
    const counts = new Map(replacements.map(item => [item.Find || item.find, 0]));
    const workbook = new ExcelJS.Workbook();
    await workbook.xlsx.readFile(req.path);
    let total = 0;
    workbook.eachSheet(sheet => {
      sheet.eachRow(row => {
        row.eachCell(cell => {
          if (typeof cell.value !== "string") return;
          let text = cell.value;
          for (const item of replacements) {
            const find = item.Find || item.find || "";
            const repl = item.Replace || item.replace || "";
            if (!find) continue;
            const count = text.split(find).length - 1;
            if (count > 0) {
              text = text.split(find).join(repl);
              counts.set(find, (counts.get(find) || 0) + count);
              total += count;
            }
          }
          cell.value = text;
        });
      });
    });
    const missing = [...counts.entries()].filter(([, count]) => count === 0).map(([find]) => find);
    if (missing.length) {
      console.log(JSON.stringify({ error: "find text was not matched: " + missing.map(x => JSON.stringify(x)).join(", ") }));
      return;
    }
    const fs = require("fs");
    const path = require("path");
    fs.mkdirSync(path.dirname(req.output_path), { recursive: true });
    await workbook.xlsx.writeFile(req.output_path);
    console.log(JSON.stringify({
      replacements: total,
      bytes: fs.statSync(req.output_path).size,
      details: [...counts.entries()].map(([find, count]) => ({ find, count }))
    }));
  } catch (error) {
    console.log(JSON.stringify({ error: String(error && error.message || error) }));
  }
});
`

const xlsxStructureAdapterScript = `
let ExcelJS;
try {
  ExcelJS = require("exceljs");
} catch (error) {
  console.log(JSON.stringify({ error: "XLSX structure adapter requires exceljs" }));
  process.exit(0);
}

let raw = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", chunk => raw += chunk);
process.stdin.on("end", async () => {
  try {
    const req = JSON.parse(raw);
    const operation = String(req.operation || "");
    const workbook = new ExcelJS.Workbook();
    await workbook.xlsx.readFile(req.path);
    const sheetName = String(req.sheet || "").trim();
    if (!sheetName) throw new Error("sheet is required");
    const sheet = workbook.getWorksheet(sheetName);
    if (!sheet) throw new Error("sheet not found: " + sheetName);

    const result = {
      status: "xlsx_version_written",
      operation,
      path: req.path,
      output_path: req.output_path,
      sheet: sheetName
    };

    function positiveRow(value) {
      const row = Number(value || 0);
      if (!Number.isInteger(row) || row <= 0) throw new Error("row must be a positive 1-based integer");
      return row;
    }

    function existingRow(value) {
      const row = positiveRow(value);
      if (row > sheet.rowCount) throw new Error("row out of range: " + row);
      return row;
    }

    function valuesArray(value) {
      if (!Array.isArray(value)) throw new Error("values must be an array");
      return value;
    }

    function writeRow(rowNumber, values) {
      const row = sheet.getRow(rowNumber);
      row.values = [];
      values.forEach((value, index) => {
        row.getCell(index + 1).value = value;
      });
      row.commit();
    }

    function assertCell(address) {
      const cell = String(address || "").trim().toUpperCase();
      if (!/^[A-Z]+[1-9][0-9]*$/.test(cell)) throw new Error("cell must be a valid A1 address");
      return cell;
    }

    if (operation === "update_cell") {
      const cellAddress = assertCell(req.cell);
      sheet.getCell(cellAddress).value = req.value;
      result.cell = cellAddress;
      result.value = req.value;
    } else if (operation === "insert_row") {
      const row = existingRow(req.row);
      const position = String(req.position || "").trim().toLowerCase();
      if (position !== "before" && position !== "after") throw new Error("position must be before or after");
      const insertAt = position === "before" ? row : row + 1;
      const values = valuesArray(req.values);
      sheet.spliceRows(insertAt, 0, values);
      result.row = row;
      result.inserted_row = insertAt;
      result.values = values;
    } else if (operation === "delete_row") {
      const row = existingRow(req.row);
      sheet.spliceRows(row, 1);
      result.row = row;
    } else if (operation === "update_row") {
      const row = existingRow(req.row);
      const values = valuesArray(req.values);
      writeRow(row, values);
      result.row = row;
      result.values = values;
    } else if (operation === "append_row") {
      const values = valuesArray(req.values);
      const newRow = sheet.rowCount + 1;
      sheet.addRow(values);
      result.row = newRow;
      result.values = values;
    } else {
      throw new Error("unsupported xlsx operation: " + operation);
    }

    const fs = require("fs");
    const path = require("path");
    fs.mkdirSync(path.dirname(req.output_path), { recursive: true });
    await workbook.xlsx.writeFile(req.output_path);
    result.bytes = fs.statSync(req.output_path).size;
    console.log(JSON.stringify(result));
  } catch (error) {
    console.log(JSON.stringify({ error: String(error && error.message || error) }));
  }
});
`

const pdfAdapterScript = `
import json, sys, os
try:
    from pypdf import PdfReader, PdfWriter
except Exception:
    print(json.dumps({"error":"PDF adapter requires pypdf"}))
    sys.exit(0)

req = json.load(sys.stdin)
op = req.get("operation")

def page_indexes(pages, count):
    if not pages:
        raise ValueError("pages are required")
    out = []
    for page in pages:
        idx = int(page) - 1
        if idx < 0 or idx >= count:
            raise ValueError("page out of range: %s" % page)
        out.append(idx)
    return out

try:
    if op in ("extract_text", "read"):
        reader = PdfReader(req["path"])
        pages = []
        chunks = []
        for index, page in enumerate(reader.pages, start=1):
            text = page.extract_text() or ""
            pages.append({"index": index, "text": text.strip()})
            chunks.append(text)
        text = "\n\n".join(chunks).strip()
        if not text:
            out = {"content":"","truncated":False,"scanned_unsupported":True}
            if op == "read":
                out["document"] = {"schema_version":"document_read_v1","format":"pdf","pages":pages,"stats":{"pages":len(pages)}}
            print(json.dumps(out))
            sys.exit(0)
        max_bytes = int(req.get("max_bytes") or 20000)
        raw = text.encode("utf-8")
        truncated = len(raw) > max_bytes
        if truncated:
            text = raw[:max_bytes].decode("utf-8", errors="ignore")
        out = {"content":text,"truncated":truncated,"scanned_unsupported":False}
        if op == "read":
            out["document"] = {"schema_version":"document_read_v1","format":"pdf","pages":pages,"stats":{"pages":len(pages)}}
        print(json.dumps(out))
    elif op == "merge":
        writer = PdfWriter()
        for path in req["inputs"]:
            reader = PdfReader(path)
            for page in reader.pages:
                writer.add_page(page)
        os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
        with open(req["output_path"], "wb") as f:
            writer.write(f)
        print(json.dumps({"status":"pdf_version_written","operation":op,"output_path":req["output_path"],"bytes":os.path.getsize(req["output_path"]),"pages":len(writer.pages),"inputs":req["inputs"]}))
    elif op in ("extract_pages", "delete_pages", "rotate_pages"):
        reader = PdfReader(req["path"])
        writer = PdfWriter()
        selected = set(page_indexes(req.get("pages"), len(reader.pages)))
        if op == "rotate_pages":
            rotation = int(req.get("rotation") or 90)
            if rotation % 90 != 0:
                raise ValueError("rotation must be a multiple of 90")
        for i, page in enumerate(reader.pages):
            if op == "extract_pages" and i not in selected:
                continue
            if op == "delete_pages" and i in selected:
                continue
            if op == "rotate_pages" and i in selected:
                page.rotate(rotation)
            writer.add_page(page)
        if len(writer.pages) == 0:
            raise ValueError("operation would produce an empty PDF")
        os.makedirs(os.path.dirname(req["output_path"]), exist_ok=True)
        with open(req["output_path"], "wb") as f:
            writer.write(f)
        print(json.dumps({"status":"pdf_version_written","operation":op,"path":req["path"],"output_path":req["output_path"],"bytes":os.path.getsize(req["output_path"]),"pages":len(writer.pages)}))
    elif op == "split":
        reader = PdfReader(req["path"])
        if len(reader.pages) == 0:
            raise ValueError("cannot split an empty PDF")
        base, ext = os.path.splitext(req["output_path"])
        outputs = []
        for i, page in enumerate(reader.pages, start=1):
            writer = PdfWriter()
            writer.add_page(page)
            if len(reader.pages) == 1:
                part_path = req["output_path"]
            else:
                part_path = "%s-page-%d%s" % (base, i, ext or ".pdf")
            os.makedirs(os.path.dirname(part_path), exist_ok=True)
            with open(part_path, "wb") as f:
                writer.write(f)
            outputs.append(part_path)
        print(json.dumps({"status":"pdf_version_written","operation":op,"path":req["path"],"output_path":req["output_path"],"outputs":outputs,"bytes":sum(os.path.getsize(p) for p in outputs),"pages":len(reader.pages)}))
    else:
        print(json.dumps({"error":"unsupported pdf operation: %s" % op}))
except Exception as e:
    print(json.dumps({"error":str(e)}))
`
