package toolhub

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

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

func runPptxSlideAdapter(ctx context.Context, request map[string]any) (map[string]any, error) {
	return runPythonAdapter(ctx, pptxSlideAdapterScript, request)
}

func applyPPTXStructure(ctx context.Context, operation string, request document.ApplyRequest) (document.ApplyResult, error) {
	args := request.Edit.Arguments
	out, err := runPptxSlideAdapter(ctx, map[string]any{
		"operation": operation, "path": request.Metadata.Path, "output_path": request.Edit.OutputPath,
		"slide_index": intArg(args, "slide_index", 0), "layout_index": intArg(args, "layout_index", 0),
		"title": stringArg(args, "title", ""), "body": stringArg(args, "body", ""), "updates": args["updates"],
		"layout_policy": stringArg(args, "layout_policy", "coordinated"),
	})
	if err != nil {
		return document.ApplyResult{}, err
	}
	return document.ApplyResult{OutputPath: request.Edit.OutputPath, Changed: 1, Details: out}, nil
}
