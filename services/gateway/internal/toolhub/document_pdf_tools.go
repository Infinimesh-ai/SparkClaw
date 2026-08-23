package toolhub

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func (h *ToolHub) pdfExtractText(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	path, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	maxBytes := intArg(args, "max_bytes", document.SmallExtractedMaxBytes)
	if maxBytes <= 0 || maxBytes > document.SmallExtractedMaxBytes {
		maxBytes = document.SmallExtractedMaxBytes
	}
	read, err := h.readDocumentWorkflow(withDocumentOCRExecution(ctx, sessionID, runID), path, maxBytes)
	if err != nil {
		h.recordPDFReadMetrics(ctx, sessionID, runID, "unavailable", nil, nil)
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
	h.recordPDFReadMetrics(ctx, sessionID, runID, coverageStatus, documentAnySlice(structured["pages"]), documentAnySlice(structuredStats["missing_page_indexes"]))
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

func validatePDFTransformArguments(args map[string]any) error {
	operation := strings.TrimSpace(stringArg(args, "operation", ""))
	if strings.TrimSpace(stringArg(args, "path", "")) == "" {
		return errors.New("pdf.transform path is required")
	}
	if strings.TrimSpace(stringArg(args, "output_path", "")) == "" {
		return errors.New("pdf.transform output_path is required")
	}
	switch operation {
	case app.DocumentOperationExtractPages, app.DocumentOperationDeletePages, app.DocumentOperationRotatePages:
		pages, err := validatedPDFPageIndexes(args["pages"])
		if err != nil {
			return err
		}
		if len(pages) == 0 {
			return errors.New("pdf.transform pages must not be empty")
		}
		if operation == app.DocumentOperationRotatePages {
			rotation, ok := integerArgument(args["rotation"])
			if !ok || !validPDFRotation(rotation) {
				return errors.New("pdf.transform rotation must be one of -270, -180, -90, 90, 180, or 270")
			}
		} else if _, supplied := args["rotation"]; supplied {
			return fmt.Errorf("pdf.transform %s does not accept rotation", operation)
		}
	case app.DocumentOperationSplit:
		for _, key := range []string{"pages", "rotation", "inputs"} {
			if _, supplied := args[key]; supplied {
				return fmt.Errorf("pdf.transform split does not accept %s", key)
			}
		}
	default:
		return fmt.Errorf("unsupported pdf operation: %s", operation)
	}
	return nil
}

func validatedPDFPageIndexes(value any) ([]int, error) {
	items, ok := arrayItems(value)
	if !ok || len(items) == 0 {
		return nil, errors.New("pdf.transform pages must be a non-empty array")
	}
	pages := make([]int, 0, len(items))
	seen := map[int]bool{}
	for _, item := range items {
		page, ok := integerArgument(item)
		if !ok || page < 1 {
			return nil, errors.New("pdf.transform pages must contain only positive integers")
		}
		if seen[page] {
			return nil, fmt.Errorf("pdf.transform pages contains duplicate page %d", page)
		}
		seen[page] = true
		pages = append(pages, page)
	}
	return pages, nil
}

func integerArgument(value any) (int, bool) {
	number, ok := numberValue(value)
	if !ok || math.Trunc(number) != number {
		return 0, false
	}
	return int(number), true
}

func validPDFRotation(rotation int) bool {
	switch rotation {
	case -270, -180, -90, 90, 180, 270:
		return true
	default:
		return false
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
	if operation == app.DocumentOperationSplit {
		changed = len(request.Document.Pages)
		outputPaths = outputStringArray(out["outputs"])
	}
	primaryOutput := request.Edit.OutputPath
	if len(outputPaths) > 0 {
		primaryOutput = outputPaths[0]
	}
	return document.ApplyResult{OutputPath: primaryOutput, OutputPaths: outputPaths, Changed: changed, Details: out}, nil
}
