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

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

type textReplacement struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

func (h *ToolHub) officeReplaceText(ctx context.Context, args map[string]any) (Result, error) {
	return h.replaceDocumentText(ctx, args, "office_version_written")
}

func (h *ToolHub) textReplaceText(ctx context.Context, args map[string]any) (Result, error) {
	return h.replaceDocumentText(ctx, args, "text_version_written")
}

func (h *ToolHub) replaceDocumentText(ctx context.Context, args map[string]any, status string) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	replacements, err := replacementArgs(args["replacements"])
	if err != nil {
		return Result{}, err
	}
	expected := intArg(args, "expected_replacements", 0)
	targets := make([]document.LocatorRequest, 0, len(replacements))
	for _, replacement := range replacements {
		targets = append(targets, document.LocatorRequest{
			Kind: document.LocatorExactText, Text: replacement.Find, AllowMultiple: expected > 0,
		})
	}
	result, err := h.editDocumentWorkflow(ctx, document.EditRequest{
		Path: inputPath, OutputPath: outputPath, Operation: "replace_text", Targets: targets, ExpectedMatches: expected,
		Arguments: map[string]any{"replacements": args["replacements"]}, MaxBytes: document.SmallExtractedMaxBytes,
	})
	if err != nil {
		return Result{}, err
	}
	output := documentChangeOutput(result, status)
	output["replacements"] = result.Changed
	return Result{Output: output}, nil
}

func (h *ToolHub) docxStructureEdit(ctx context.Context, operation string, args map[string]any) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	if operation == "replace_paragraph" && strings.TrimSpace(stringArg(args, "old_text", "")) == "" && strings.TrimSpace(stringArg(args, "source_hash", "")) == "" {
		return Result{}, errors.New("docx.replace_paragraph requires old_text or source_hash preflight evidence")
	}
	target := docxEditTarget(operation, args)
	result, err := h.editDocumentWorkflow(ctx, document.EditRequest{
		Path: inputPath, OutputPath: outputPath, Operation: operation, Target: target,
		Arguments: args, MaxBytes: document.SmallExtractedMaxBytes,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: documentChangeOutput(result, "docx_version_written")}, nil
}

func (h *ToolHub) pptxSlideEdit(ctx context.Context, operation string, args map[string]any) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	target := document.LocatorRequest{Kind: document.LocatorDocument}
	if operation != "add_slide" {
		target = document.LocatorRequest{Kind: document.LocatorSlide, SlideIndex: intArg(args, "slide_index", 0), AllowMultiple: true}
	}
	result, err := h.editDocumentWorkflow(ctx, document.EditRequest{
		Path: inputPath, OutputPath: outputPath, Operation: operation, Target: target,
		Arguments: args, MaxBytes: document.SmallExtractedMaxBytes,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: documentChangeOutput(result, "pptx_version_written")}, nil
}

func (h *ToolHub) xlsxStructureEdit(ctx context.Context, operation string, args map[string]any) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	target := xlsxEditTarget(operation, args)
	result, err := h.editDocumentWorkflow(ctx, document.EditRequest{
		Path: inputPath, OutputPath: outputPath, Operation: operation, Target: target,
		Arguments: args, MaxBytes: document.SmallExtractedMaxBytes,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: documentChangeOutput(result, "xlsx_version_written")}, nil
}

func (h *ToolHub) pdfExtractText(ctx context.Context, args map[string]any) (Result, error) {
	path, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	maxBytes := intArg(args, "max_bytes", document.SmallExtractedMaxBytes)
	if maxBytes <= 0 || maxBytes > document.SmallExtractedMaxBytes {
		maxBytes = document.SmallExtractedMaxBytes
	}
	read, err := h.readDocumentWorkflow(ctx, path, maxBytes)
	if err != nil {
		return Result{}, err
	}
	structured, err := read.Document.Map()
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"path":                path,
		"content":             read.Content,
		"bytes":               len([]byte(read.Content)),
		"truncated":           false,
		"untrusted":           true,
		"scanned_unsupported": boolArg(read.Document.Stats, "scanned_unsupported", false),
		"document":            structured,
	}}, nil
}

func (h *ToolHub) pdfTransform(ctx context.Context, args map[string]any) (Result, error) {
	operation := stringArg(args, "operation", "")
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	if operation == "merge" {
		inputs, err := resolveStringPaths(h, args["inputs"])
		if err != nil {
			return Result{}, err
		}
		if len(inputs) < 2 {
			return Result{}, errors.New("merge requires at least two inputs")
		}
		for _, input := range inputs {
			if input == outputPath {
				return Result{}, errors.New("output_path must not overwrite an input file")
			}
		}
		if _, err := os.Lstat(outputPath); err == nil {
			return Result{}, errors.New("output_path already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return Result{}, errors.New("output_path is unavailable")
		}
		out, err := runPDFPython(ctx, map[string]any{"operation": operation, "output_path": outputPath, "inputs": inputs})
		if err != nil {
			return Result{}, err
		}
		return Result{Output: map[string]any{
			"status": stringArg(out, "status", "pdf_version_written"), "operation": operation, "inputs": inputs,
			"output_path": outputPath, "bytes": intArg(out, "bytes", fileSize(outputPath)), "pages": intArg(out, "pages", 0),
		}}, nil
	}
	path, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	target := document.LocatorRequest{Kind: document.LocatorDocument}
	if operation != "split" {
		target = document.LocatorRequest{Kind: document.LocatorPages, PageIndexes: intList(args["pages"]), AllowMultiple: true}
	}
	result, err := h.editDocumentWorkflow(ctx, document.EditRequest{
		Path: path, OutputPath: outputPath, Operation: operation, Target: target,
		Arguments: args, MaxBytes: document.SmallExtractedMaxBytes,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: documentChangeOutput(result, "pdf_version_written")}, nil
}

func docxEditTarget(operation string, args map[string]any) document.LocatorRequest {
	if position := stringArg(args, "position", ""); operation == "insert_paragraph" && (position == "start" || position == "end") {
		return document.LocatorRequest{Kind: document.LocatorDocument}
	}
	if location, ok := args["location"].(map[string]any); ok {
		if path := stringArg(location, "path", ""); path != "" {
			return document.LocatorRequest{Kind: document.LocatorBlock, LocationPath: path}
		}
	}
	if index := intArg(args, "paragraph_index", 0); index > 0 {
		return document.LocatorRequest{Kind: document.LocatorParagraph, ParagraphIndex: index}
	}
	return document.LocatorRequest{Kind: document.LocatorExactText, Text: stringArg(args, "old_text", "")}
}

func xlsxEditTarget(operation string, args map[string]any) document.LocatorRequest {
	sheet := stringArg(args, "sheet", "")
	switch operation {
	case "update_cell":
		return document.LocatorRequest{Kind: document.LocatorCell, Sheet: sheet, Cell: stringArg(args, "cell", "")}
	case "append_row":
		return document.LocatorRequest{Kind: document.LocatorSheet, Sheet: sheet}
	default:
		return document.LocatorRequest{Kind: document.LocatorRow, Sheet: sheet, Row: intArg(args, "row", 0), AllowMultiple: true}
	}
}

func intList(value any) []int {
	items, ok := arrayItems(value)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(items))
	for _, item := range items {
		if number, ok := numberValue(item); ok {
			out = append(out, int(number))
		}
	}
	return out
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
