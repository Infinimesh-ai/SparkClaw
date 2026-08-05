package toolhub

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func runPptxSlideAdapter(ctx context.Context, request map[string]any) (map[string]any, error) {
	return runPythonAdapter(ctx, pptxSlideAdapterScript, request)
}

func pptxDocumentParser() document.Parser {
	parser := adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
		return runPythonAdapter(ctx, pptxReadAdapterScript, request)
	})
	return document.ParserFunc(func(ctx context.Context, metadata document.Metadata, maxBytes int) (document.AdapterReadResult, error) {
		result, err := parser.Parse(ctx, metadata, maxBytes)
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return document.AdapterReadResult{}, pptxTimeoutPipelineError(document.StageRead, "PPTX reader exceeded the operation deadline")
		}
		return result, err
	})
}

func applyPPTXReplacement(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
	result, err := applyOfficeReplacement(ctx, request, func(ctx context.Context, adapterRequest map[string]any) (map[string]any, error) {
		return runPythonAdapter(ctx, pptxAdapterScript, adapterRequest)
	})
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return document.ApplyResult{}, pptxTimeoutPipelineError(document.StageApply, "PPTX replacement adapter exceeded the operation deadline")
	}
	return result, err
}

func applyPPTXStructure(ctx context.Context, operation string, request document.ApplyRequest) (document.ApplyResult, error) {
	args := request.Edit.Arguments
	out, err := runPptxSlideAdapter(ctx, map[string]any{
		"operation": operation, "path": request.Metadata.Path, "output_path": request.Edit.OutputPath,
		"slide_index": intArg(args, "slide_index", 0), "after_slide_index": intArg(args, "after_slide_index", 0),
		"layout_ref": stringArg(args, "layout_ref", ""), "template_slide_ref": stringArg(args, "template_slide_ref", ""),
		"title": stringArg(args, "title", ""), "body": stringArg(args, "body", ""), "updates": args["updates"],
		"template_updates": args["template_updates"], "slide_updates": args["slide_updates"],
		"layout_policy": stringArg(args, "layout_policy", "coordinated"),
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return document.ApplyResult{}, pptxTimeoutPipelineError(document.StageApply, "PPTX structure adapter exceeded the operation deadline")
		}
		return document.ApplyResult{}, err
	}
	changed := 1
	if operation == "update_slide" || operation == "update_deck" {
		changed = intArg(out, "updated_shapes", 1)
	}
	return document.ApplyResult{OutputPath: request.Edit.OutputPath, Changed: changed, Details: out}, nil
}

func pptxTimeoutPipelineError(stage document.Stage, detail string) error {
	return &document.PipelineError{Code: document.CodeOperationTimeout, Stage: stage, Format: app.DocumentFormatPPTX, Detail: detail}
}

func wrapPPTXToolError(ctx context.Context, err error) error {
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && !document.IsErrorCode(err, document.CodeOperationTimeout) {
		err = pptxTimeoutPipelineError(document.StageRead, "PPTX operation deadline expired during input inspection or read")
	}
	if err == nil || !document.IsErrorCode(err, document.CodeOperationTimeout) {
		return err
	}
	return &app.CodedToolError{Code: app.ToolErrorDocumentOperationTimeout, Err: err}
}

func validatePPTXEditArguments(operation string, args map[string]any) error {
	updates := []any{}
	slides := 0
	replacementBytes := 0
	switch operation {
	case "replace_text":
		replacements := pptxArray(args["replacements"])
		if len(replacements) == 0 {
			return errors.New("replacements must be a non-empty array")
		}
		for _, value := range replacements {
			replacement, ok := value.(map[string]any)
			if !ok {
				return errors.New("each PPTX replacement must be an object")
			}
			replacementBytes += len([]byte(stringArg(replacement, "replace", "")))
		}
	case "update_slide":
		slides = 1
		updates = pptxArray(args["updates"])
	case "add_slide":
		slides = 1
		layoutRef := strings.TrimSpace(stringArg(args, "layout_ref", ""))
		templateRef := strings.TrimSpace(stringArg(args, "template_slide_ref", ""))
		if (layoutRef == "") == (templateRef == "") {
			return errors.New("exactly one of layout_ref or template_slide_ref is required")
		}
		if layoutRef != "" && !strings.HasPrefix(layoutRef, "layout:/ppt/slideLayouts/") {
			return errors.New("layout_ref must come from the current PPTX layout inventory")
		}
		if templateRef != "" && !strings.HasPrefix(templateRef, "slide:") {
			return errors.New("template_slide_ref must come from the current PPTX slide inventory")
		}
		updates = pptxArray(args["template_updates"])
		replacementBytes += len([]byte(stringArg(args, "title", ""))) + len([]byte(stringArg(args, "body", "")))
	case "update_deck":
		slideUpdates := pptxArray(args["slide_updates"])
		slides = len(slideUpdates)
		if len(slideUpdates) == 0 || len(slideUpdates) > document.PPTXWholeDeckMaxSlides {
			return fmt.Errorf("slide_updates must contain between 1 and %d slides", document.PPTXWholeDeckMaxSlides)
		}
		seenSlides := map[int]bool{}
		for _, value := range slideUpdates {
			slideUpdate, ok := value.(map[string]any)
			if !ok {
				return errors.New("each slide_updates item must be an object")
			}
			index := intArg(slideUpdate, "slide_index", 0)
			if index <= 0 || seenSlides[index] {
				return errors.New("slide_updates contains an invalid or duplicate slide_index")
			}
			seenSlides[index] = true
			updates = append(updates, pptxArray(slideUpdate["updates"])...)
		}
	}
	for _, value := range updates {
		update, ok := value.(map[string]any)
		if !ok {
			return errors.New("each PPTX text update must be an object")
		}
		replacementBytes += len([]byte(stringArg(update, "text", "")))
		if strings.EqualFold(strings.TrimSpace(stringArg(update, "mode", "rewrite_shape")), "exact_span") && strings.TrimSpace(stringArg(update, "find", "")) == "" {
			return errors.New("exact_span updates require find")
		}
	}
	return document.ValidatePPTXEditBounds(slides, len(updates), replacementBytes)
}

func pptxArray(value any) []any {
	items, _ := arrayItems(value)
	return items
}
