package toolhub

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

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
	stats := read.Document.Stats
	structuredStats, _ := structured["stats"].(map[string]any)
	readComplete := boolArg(stats, "read_complete", boolArg(stats, "complete", false))
	coverageStatus := stringArg(stats, "coverage_status", "")
	if coverageStatus == "" {
		coverageStatus = "partial"
		if readComplete {
			coverageStatus = "complete"
		} else if strings.TrimSpace(read.Content) == "" {
			coverageStatus = "unavailable"
		}
	}
	return Result{Output: map[string]any{
		"path":                 path,
		"content":              read.Content,
		"bytes":                len([]byte(read.Content)),
		"truncated":            false,
		"untrusted":            true,
		"read_complete":        readComplete,
		"coverage_status":      coverageStatus,
		"missing_page_indexes": structuredStats["missing_page_indexes"],
		"page_status_counts":   structuredStats["page_status_counts"],
		"scanned_unsupported":  boolArg(read.Document.Stats, "scanned_unsupported", false),
		"document":             structured,
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

func runPDFPython(ctx context.Context, request map[string]any) (map[string]any, error) {
	return runPythonAdapter(ctx, pdfAdapterScript, request)
}

func applyPDFTransform(ctx context.Context, operation string, request document.ApplyRequest) (document.ApplyResult, error) {
	args := request.Edit.Arguments
	out, err := runPDFPython(ctx, map[string]any{
		"operation": operation, "path": request.Metadata.Path, "output_path": request.Edit.OutputPath,
		"pages": args["pages"], "rotation": args["rotation"],
	})
	if err != nil {
		return document.ApplyResult{}, err
	}
	changed := len(request.Matches)
	outputPaths := []string{request.Edit.OutputPath}
	if operation == "split" {
		changed = len(request.Document.Pages)
		outputPaths = outputStringArray(out["outputs"])
	}
	primaryOutput := request.Edit.OutputPath
	if len(outputPaths) > 0 {
		primaryOutput = outputPaths[0]
	}
	return document.ApplyResult{OutputPath: primaryOutput, OutputPaths: outputPaths, Changed: changed, Details: out}, nil
}
