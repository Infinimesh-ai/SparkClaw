package toolhub

import (
	"context"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func isDOCXDocumentBoundaryPosition(args map[string]any) bool {
	position := strings.ToLower(strings.TrimSpace(stringArg(args, "position", "")))
	return position == "start" || position == "end"
}

func validateDOCXStructureArguments(operation string, args map[string]any) error {
	position := strings.ToLower(strings.TrimSpace(stringArg(args, "position", "")))
	index, locationIndex, hasTarget, err := docxArgumentTarget(args)
	if err != nil {
		return err
	}
	if operation == app.DocumentOperationInsertParagraph {
		switch position {
		case "start", "end":
			if hasTarget {
				return errors.New("docx.insert_paragraph start/end must not include a paragraph target")
			}
			if boundary := strings.ToLower(strings.TrimSpace(stringArg(args, "document_boundary", ""))); boundary != position {
				return errors.New("docx.insert_paragraph start/end requires the matching document_boundary evidence")
			}
			if strings.TrimSpace(stringArg(args, "source_hash", "")) != "" || strings.TrimSpace(stringArg(args, "old_text", "")) != "" {
				return errors.New("docx.insert_paragraph start/end must not include paragraph evidence")
			}
		case "before", "after":
			if !hasTarget {
				return errors.New("docx.insert_paragraph before/after requires paragraph_index or location")
			}
			if strings.TrimSpace(stringArg(args, "source_hash", "")) == "" {
				return errors.New("docx.insert_paragraph before/after requires source_hash preflight evidence")
			}
			if strings.TrimSpace(stringArg(args, "document_boundary", "")) != "" {
				return errors.New("docx.insert_paragraph before/after must not include document_boundary evidence")
			}
		default:
			return errors.New("docx.insert_paragraph position must be start, end, before, or after")
		}
	}
	if operation == app.DocumentOperationReplaceParagraph || operation == app.DocumentOperationDeleteParagraph || operation == app.DocumentOperationSetTextStyle {
		if !hasTarget {
			return errors.New("docx paragraph edit requires paragraph_index or location")
		}
	}
	if index > 0 && locationIndex > 0 && index != locationIndex {
		return errors.New("docx paragraph_index conflicts with location.paragraph_index")
	}
	if operation == app.DocumentOperationSetTextStyle {
		style, ok := args["style"].(map[string]any)
		if !ok || len(style) == 0 {
			return errors.New("docx.set_text_style style must contain builtin_style, bold, or font_size_pt")
		}
		if value, exists := style["builtin_style"]; exists && strings.TrimSpace(documentStringValue(value)) == "" {
			return errors.New("docx.set_text_style builtin_style must not be empty")
		}
		if value, exists := style["font_size_pt"]; exists {
			size := documentIntValue(value)
			if size < 1 || size > 200 {
				return errors.New("docx.set_text_style font_size_pt must be between 1 and 200")
			}
		}
		if strings.TrimSpace(stringArg(args, "before_format_sha256", "")) == "" {
			return errors.New("docx.set_text_style requires before_format_sha256 preflight evidence")
		}
	}
	return nil
}

func docxArgumentTarget(args map[string]any) (int, int, bool, error) {
	index := intArg(args, "paragraph_index", 0)
	if index < 0 {
		return 0, 0, false, errors.New("docx paragraph_index must be a positive 1-based integer")
	}
	locationIndex := 0
	if value, exists := args["location"]; exists && value != nil {
		location, ok := value.(map[string]any)
		if !ok {
			return 0, 0, false, errors.New("docx location must be an object")
		}
		if part := strings.TrimSpace(documentStringValue(location["part"])); part != "" && part != "document" {
			return 0, 0, false, errors.New("docx location must identify the document part")
		}
		if blockType := strings.TrimSpace(documentStringValue(location["block_type"])); blockType != "" && blockType != "paragraph" {
			return 0, 0, false, errors.New("only top-level paragraph locations are currently editable")
		}
		locationIndex = intArg(location, "paragraph_index", 0)
		if locationIndex <= 0 {
			return 0, 0, false, errors.New("docx location.paragraph_index must be a positive 1-based integer")
		}
	}
	return index, locationIndex, index > 0 || locationIndex > 0, nil
}

func docxEditTarget(operation string, args map[string]any) document.LocatorRequest {
	if position := stringArg(args, "position", ""); operation == app.DocumentOperationInsertParagraph && (position == "start" || position == "end") {
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
