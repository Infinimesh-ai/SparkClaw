package toolhub

import (
	"context"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func (h *ToolHub) docxStructureEdit(ctx context.Context, operation string, args map[string]any) (Result, error) {
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, err
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, err
	}
	if operation == "replace_paragraph" && strings.TrimSpace(stringArg(args, "source_hash", "")) == "" {
		return Result{}, errors.New("docx.replace_paragraph requires source_hash preflight evidence")
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

func runDocxStructureAdapter(ctx context.Context, request map[string]any) (map[string]any, error) {
	return runPythonAdapter(ctx, docxStructureAdapterScript, request)
}

func applyDOCXStructure(ctx context.Context, operation string, request document.ApplyRequest) (document.ApplyResult, error) {
	args := request.Edit.Arguments
	out, err := runDocxStructureAdapter(ctx, map[string]any{
		"operation": operation, "path": request.Metadata.Path, "output_path": request.Edit.OutputPath,
		"paragraph_index": intArg(args, "paragraph_index", 0), "position": stringArg(args, "position", ""),
		"old_text": stringArg(args, "old_text", ""), "source_hash": stringArg(args, "source_hash", ""),
		"text": stringArg(args, "text", ""), "style": args["style"], "location": args["location"],
	})
	if err != nil {
		return document.ApplyResult{}, err
	}
	return document.ApplyResult{OutputPath: request.Edit.OutputPath, Changed: 1, Details: out}, nil
}
