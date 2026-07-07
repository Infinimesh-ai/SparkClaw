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
	"time"
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

// documentAdapterTimeout bounds a single document subprocess so a hung
// node/python process cannot pin the request forever when the caller's
// context carries no deadline of its own.
const documentAdapterTimeout = 60 * time.Second

func runPythonAdapter(ctx context.Context, script string, request map[string]any) (map[string]any, error) {
	return runSubprocessAdapter(ctx, request, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, documentPythonBinary(), "-c", script)
	})
}

func runNodeAdapter(ctx context.Context, script string, request map[string]any) (map[string]any, error) {
	return runSubprocessAdapter(ctx, request, func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, documentNodeBinary(), "-e", script)
		cmd.Env = append(os.Environ(), "NODE_PATH="+documentNodeModulesPath())
		return cmd
	})
}

func runSubprocessAdapter(ctx context.Context, request map[string]any, makeCmd func(context.Context) *exec.Cmd) (map[string]any, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, documentAdapterTimeout)
		defer cancel()
	}
	cmd := makeCmd(ctx)
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

func runOfficeAdapter(ctx context.Context, ext string, request map[string]any) (map[string]any, error) {
	switch ext {
	case ".docx":
		return runPythonAdapter(ctx, docxAdapterScript, request)
	case ".pptx":
		return runPythonAdapter(ctx, pptxAdapterScript, request)
	case ".xlsx":
		return runNodeAdapter(ctx, xlsxAdapterScript, request)
	default:
		return nil, fmt.Errorf("unsupported office extension %s", ext)
	}
}

func runOfficeReadAdapter(ctx context.Context, ext string, request map[string]any) (map[string]any, error) {
	switch ext {
	case ".docx":
		return runPythonAdapter(ctx, docxReadAdapterScript, request)
	case ".pptx":
		return runPythonAdapter(ctx, pptxReadAdapterScript, request)
	case ".xlsx":
		return runNodeAdapter(ctx, xlsxReadAdapterScript, request)
	default:
		return nil, fmt.Errorf("unsupported office extension %s", ext)
	}
}

func runDocxStructureAdapter(ctx context.Context, request map[string]any) (map[string]any, error) {
	return runPythonAdapter(ctx, docxStructureAdapterScript, request)
}

func runPptxSlideAdapter(ctx context.Context, request map[string]any) (map[string]any, error) {
	return runPythonAdapter(ctx, pptxSlideAdapterScript, request)
}

func runXlsxStructureAdapter(ctx context.Context, request map[string]any) (map[string]any, error) {
	return runNodeAdapter(ctx, xlsxStructureAdapterScript, request)
}

func runPDFPython(ctx context.Context, request map[string]any) (map[string]any, error) {
	return runPythonAdapter(ctx, pdfAdapterScript, request)
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

func fileSize(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return int(info.Size())
}
