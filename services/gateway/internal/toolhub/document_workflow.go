package toolhub

import (
	"context"
	"errors"
	"os"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func newDocumentPipeline() *document.Pipeline {
	parsers := map[string]document.Parser{
		app.DocumentFormatText: document.ParserFunc(parseTextDocument),
		app.DocumentFormatDOCX: adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
			return runPythonAdapter(ctx, docxReadAdapterScript, request)
		}),
		app.DocumentFormatXLSX: adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
			return runNodeAdapter(ctx, xlsxReadAdapterScript, request)
		}),
		app.DocumentFormatPPTX: adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
			return runPythonAdapter(ctx, pptxReadAdapterScript, request)
		}),
		app.DocumentFormatPDF: adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
			request["operation"] = "read"
			return runPDFPython(ctx, request)
		}),
	}
	editors := map[string]document.Editor{}
	for _, format := range []string{app.DocumentFormatDOCX, app.DocumentFormatXLSX, app.DocumentFormatPPTX} {
		format := format
		editors[document.EditorKey(format, "replace_text")] = document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
			return applyOfficeReplacement(ctx, format, request)
		})
	}
	for _, operation := range []string{"replace_paragraph", "insert_paragraph", "delete_paragraph", "set_text_style"} {
		operation := operation
		editors[document.EditorKey(app.DocumentFormatDOCX, operation)] = document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
			return applyDOCXStructure(ctx, operation, request)
		})
	}
	for _, operation := range []string{"add_slide", "duplicate_slide", "delete_slide"} {
		operation := operation
		editors[document.EditorKey(app.DocumentFormatPPTX, operation)] = document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
			return applyPPTXStructure(ctx, operation, request)
		})
	}
	for _, operation := range []string{"update_cell", "insert_row", "delete_row", "update_row", "append_row"} {
		operation := operation
		editors[document.EditorKey(app.DocumentFormatXLSX, operation)] = document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
			return applyXLSXStructure(ctx, operation, request)
		})
	}
	for _, operation := range []string{"extract_pages", "delete_pages", "rotate_pages", "split"} {
		operation := operation
		editors[document.EditorKey(app.DocumentFormatPDF, operation)] = document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
			return applyPDFTransform(ctx, operation, request)
		})
	}
	strategy := document.NewSmallFileStrategy(parsers, editors)
	return document.NewPipeline(document.InspectorFunc(document.InspectFile), strategy)
}

type adapterReadFunc func(context.Context, map[string]any) (map[string]any, error)

func adapterDocumentParser(run adapterReadFunc) document.Parser {
	return document.ParserFunc(func(ctx context.Context, metadata document.Metadata, maxBytes int) (document.AdapterReadResult, error) {
		out, err := run(ctx, map[string]any{"path": metadata.Path, "max_bytes": maxBytes})
		if err != nil {
			return document.AdapterReadResult{}, err
		}
		structured, _ := out["document"].(map[string]any)
		return document.AdapterReadResult{
			Content: stringArg(out, "content", ""), ExtractedBytes: intArg(out, "extracted_bytes", 0),
			Truncated: boolArg(out, "truncated", false), Document: structured,
		}, nil
	})
}

func parseTextDocument(ctx context.Context, metadata document.Metadata, maxBytes int) (document.AdapterReadResult, error) {
	select {
	case <-ctx.Done():
		return document.AdapterReadResult{}, ctx.Err()
	default:
	}
	raw, err := os.ReadFile(metadata.Path)
	if err != nil {
		return document.AdapterReadResult{}, err
	}
	if len(raw) > maxBytes {
		return document.AdapterReadResult{Content: string(raw[:maxBytes]), ExtractedBytes: len(raw), Truncated: true}, nil
	}
	return document.AdapterReadResult{Content: string(raw), ExtractedBytes: len(raw), Document: textDocumentReadEnvelope(string(raw), false, maxBytes)}, nil
}

func applyOfficeReplacement(ctx context.Context, format string, request document.ApplyRequest) (document.ApplyResult, error) {
	adapterRequest := map[string]any{
		"path": request.Metadata.Path, "output_path": request.Edit.OutputPath, "replacements": request.Edit.Arguments["replacements"],
	}
	var out map[string]any
	var err error
	switch format {
	case app.DocumentFormatDOCX:
		out, err = runPythonAdapter(ctx, docxAdapterScript, adapterRequest)
	case app.DocumentFormatXLSX:
		out, err = runNodeAdapter(ctx, xlsxAdapterScript, adapterRequest)
	case app.DocumentFormatPPTX:
		out, err = runPythonAdapter(ctx, pptxAdapterScript, adapterRequest)
	default:
		err = errors.New("unsupported Office replacement format")
	}
	if err != nil {
		return document.ApplyResult{}, err
	}
	changed := intArg(out, "replacements", 0)
	if changed != len(request.Matches) {
		_ = os.Remove(request.Edit.OutputPath)
		return document.ApplyResult{}, &document.PipelineError{
			Code: document.CodeMatchCountMismatch, Stage: document.StageApply, Format: format,
			Detail: "editor change count did not match the constrained target set",
		}
	}
	return document.ApplyResult{OutputPath: request.Edit.OutputPath, Changed: changed, Details: out}, nil
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

func applyPPTXStructure(ctx context.Context, operation string, request document.ApplyRequest) (document.ApplyResult, error) {
	args := request.Edit.Arguments
	out, err := runPptxSlideAdapter(ctx, map[string]any{
		"operation": operation, "path": request.Metadata.Path, "output_path": request.Edit.OutputPath,
		"slide_index": intArg(args, "slide_index", 0), "layout_index": intArg(args, "layout_index", 0),
		"title": stringArg(args, "title", ""), "body": stringArg(args, "body", ""),
	})
	if err != nil {
		return document.ApplyResult{}, err
	}
	return document.ApplyResult{OutputPath: request.Edit.OutputPath, Changed: 1, Details: out}, nil
}

func applyXLSXStructure(ctx context.Context, operation string, request document.ApplyRequest) (document.ApplyResult, error) {
	args := request.Edit.Arguments
	out, err := runXlsxStructureAdapter(ctx, map[string]any{
		"operation": operation, "path": request.Metadata.Path, "output_path": request.Edit.OutputPath,
		"sheet": stringArg(args, "sheet", ""), "cell": stringArg(args, "cell", ""), "row": intArg(args, "row", 0),
		"position": stringArg(args, "position", ""), "value": args["value"], "values": args["values"],
	})
	if err != nil {
		return document.ApplyResult{}, err
	}
	return document.ApplyResult{OutputPath: request.Edit.OutputPath, Changed: 1, Details: out}, nil
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

func (h *ToolHub) readDocumentWorkflow(ctx context.Context, path string, maxBytes int) (document.ReadResult, error) {
	return h.documents.Read(ctx, document.ReadRequest{Root: h.cfg.Workspaces.DefaultRoot, Path: path, MaxBytes: maxBytes})
}

func (h *ToolHub) editDocumentWorkflow(ctx context.Context, request document.EditRequest) (document.EditResult, error) {
	request.Root = h.cfg.Workspaces.DefaultRoot
	return h.documents.Edit(ctx, request)
}

func documentReadOutput(read document.ReadResult, maxBytes int) (map[string]any, error) {
	structured, err := read.Document.Map()
	if err != nil {
		return nil, err
	}
	attachEvidenceBlocks(structured, read.Metadata.Relative, read.Metadata.Format)
	attachSmallDocumentPipeline(structured, read.Metadata.Relative, read.Metadata.Format, read.Content, maxBytes)
	return map[string]any{
		"path": read.Metadata.Path, "rel_path": read.Metadata.Relative, "already_read": true,
		"kind": read.Metadata.Format, "content": read.Content, "bytes": len([]byte(read.Content)),
		"source_bytes": read.Metadata.Size, "max_bytes": maxBytes, "truncated": false, "untrusted": true, "document": structured,
	}, nil
}

func documentChangeOutput(result document.EditResult, status string) map[string]any {
	changeSummary, _ := result.ChangeSummary.Map()
	out := map[string]any{
		"status": status, "operation": result.ChangeSummary.Operation, "path": result.Metadata.Path,
		"output_path": result.OutputPath, "bytes": fileSize(result.OutputPath), "changes": result.Changed,
		"change_summary": changeSummary, "untrusted": true,
	}
	if len(result.OutputPaths) > 0 {
		out["outputs"] = append([]string(nil), result.OutputPaths...)
	}
	for key, value := range result.Details {
		if _, exists := out[key]; !exists {
			out[key] = value
		}
	}
	out["bytes"] = intArg(result.Details, "bytes", fileSize(result.OutputPath))
	for _, key := range []string{"paragraph_index", "slide_index", "inserted_slide_index", "layout_index", "slides", "row", "inserted_row", "pages", "replacements"} {
		if _, exists := result.Details[key]; exists {
			out[key] = intArg(result.Details, key, 0)
		}
	}
	if _, exists := result.Details["outputs"]; exists && len(result.OutputPaths) == 0 {
		out["outputs"] = outputStringArray(result.Details["outputs"])
	}
	if _, exists := result.Details["inputs"]; exists {
		out["inputs"] = outputStringArray(result.Details["inputs"])
	}
	return out
}
