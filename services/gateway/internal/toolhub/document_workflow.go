package toolhub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func newDocumentPipeline(hub *ToolHub) *document.Pipeline {
	parsers := map[string]document.Parser{
		app.DocumentFormatText: document.ParserFunc(parseTextDocument),
		app.DocumentFormatDOCX: adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
			return runPythonAdapter(ctx, docxReadAdapterScript, request)
		}),
		app.DocumentFormatXLSX: adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
			return runNodeAdapter(ctx, xlsxReadAdapterScript, request)
		}),
		app.DocumentFormatPPTX: pptxDocumentParser(),
		app.DocumentFormatPDF: adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
			request["operation"] = "read"
			return runPDFPython(ctx, request)
		}),
	}
	editors := map[string]document.Editor{}
	editors[document.EditorKey(app.DocumentFormatText, "replace_text")] = document.EditorFunc(applyTextReplacement)
	for _, format := range []string{app.DocumentFormatDOCX, app.DocumentFormatXLSX} {
		format := format
		editors[document.EditorKey(format, "replace_text")] = document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
			return applyOfficeReplacement(ctx, format, request)
		})
	}
	editors[document.EditorKey(app.DocumentFormatPPTX, "replace_text")] = document.EditorFunc(applyPPTXReplacement)
	for _, operation := range []string{"replace_paragraph", "insert_paragraph", "delete_paragraph", "set_text_style"} {
		operation := operation
		editors[document.EditorKey(app.DocumentFormatDOCX, operation)] = document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
			return applyDOCXStructure(ctx, operation, request)
		})
	}
	for _, operation := range []string{"add_slide", "update_slide", "update_deck", "duplicate_slide", "delete_slide"} {
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
	return document.NewPipeline(document.InspectorFunc(document.InspectFile), strategy).WithEnrichers(
		&ovisDocumentOCREnricher{hub: hub},
		&fastDocumentImageEnricher{hub: hub},
	)
}

func applyTextReplacement(_ context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
	replacements, err := replacementArgs(request.Edit.Arguments["replacements"])
	if err != nil {
		return document.ApplyResult{}, err
	}
	raw, err := os.ReadFile(request.Metadata.Path)
	if err != nil {
		return document.ApplyResult{}, err
	}
	updated := string(raw)
	changed := 0
	for _, replacement := range replacements {
		count := strings.Count(updated, replacement.Find)
		updated = strings.ReplaceAll(updated, replacement.Find, replacement.Replace)
		changed += count
	}
	if changed != len(request.Matches) {
		return document.ApplyResult{}, &document.PipelineError{
			Code: document.CodeMatchCountMismatch, Stage: document.StageApply, Format: request.Metadata.Format,
			Detail: "text editor change count did not match the constrained target set",
		}
	}
	if err := os.MkdirAll(filepath.Dir(request.Edit.OutputPath), 0o755); err != nil {
		return document.ApplyResult{}, err
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(request.Metadata.Path); statErr == nil {
		mode = info.Mode().Perm()
	}
	file, err := os.OpenFile(request.Edit.OutputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return document.ApplyResult{}, err
	}
	if _, err = file.WriteString(updated); err != nil {
		_ = file.Close()
		_ = os.Remove(request.Edit.OutputPath)
		return document.ApplyResult{}, err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(request.Edit.OutputPath)
		return document.ApplyResult{}, err
	}
	return document.ApplyResult{
		OutputPath: request.Edit.OutputPath, Changed: changed,
		Details: map[string]any{"status": "text_version_written", "operation": "replace_text", "replacements": changed, "bytes": len([]byte(updated))},
	}, nil
}

type adapterReadFunc func(context.Context, map[string]any) (map[string]any, error)

func adapterDocumentParser(run adapterReadFunc) document.Parser {
	return document.ParserFunc(func(ctx context.Context, metadata document.Metadata, maxBytes int) (document.AdapterReadResult, error) {
		out, err := run(ctx, map[string]any{"path": metadata.Path, "max_bytes": maxBytes})
		if err != nil {
			return document.AdapterReadResult{}, err
		}
		structured, _ := out["document"].(map[string]any)
		resources, err := decodeDocumentResources(out["resources"], structured)
		if err != nil {
			return document.AdapterReadResult{}, err
		}
		return document.AdapterReadResult{
			Content: stringArg(out, "content", ""), ExtractedBytes: intArg(out, "extracted_bytes", 0),
			Truncated: boolArg(out, "truncated", false), Document: structured, Resources: resources,
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

func (h *ToolHub) readDocumentWorkflow(ctx context.Context, path string, maxBytes int, enrichment ...document.EnrichmentOptions) (document.ReadResult, error) {
	options := document.EnrichmentOptions{ImageAnalysis: "none"}
	if len(enrichment) > 0 {
		options = enrichment[0]
	}
	return h.documents.Read(ctx, document.ReadRequest{Root: h.cfg.Workspaces.DefaultRoot, Path: path, MaxBytes: maxBytes, Enrichment: options})
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
	for _, key := range []string{"paragraph_index", "slide_index", "inserted_slide_index", "layout_index", "slides", "updated_shapes", "fitted_shapes", "wrapped_shapes", "layout_adjusted_shapes", "companion_groups_used", "row", "inserted_row", "pages", "replacements"} {
		if _, exists := result.Details[key]; exists {
			out[key] = intArg(result.Details, key, 0)
		}
	}
	for _, key := range []string{"wrapped_shape_indexes", "layout_adjusted_shape_indexes"} {
		if _, exists := result.Details[key]; exists {
			values := documentAnySlice(result.Details[key])
			indexes := make([]int, 0, len(values))
			for _, value := range values {
				indexes = append(indexes, documentIntValue(value))
			}
			out[key] = indexes
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
