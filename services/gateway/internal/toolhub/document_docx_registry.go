package toolhub

import (
	"context"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func docxDocumentFormatProvider() documentFormatProvider {
	provider := documentFormatProvider{
		Format: app.DocumentFormatDOCX, ReadToolNames: []string{"files.read"},
		OperationOrder: canonicalDocumentOperationOrder(app.DocumentFormatDOCX),
		Parser: adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
			return runPythonAdapter(ctx, docxReadAdapterScript, request)
		}),
		Operations: map[string]documentOperationProvider{},
	}
	provider.Operations[app.DocumentOperationReplaceText] = documentOperationProvider{
		ToolName: "office.replace_text", Summary: "Replace bounded text and write an Office output copy.",
		Validate:     validateDOCXOperationInvocation(app.DocumentOperationReplaceText),
		BuildTargets: exactTextTargets,
		Editor: document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
			return applyOfficeReplacement(ctx, request, func(ctx context.Context, adapterRequest map[string]any) (map[string]any, error) {
				return runPythonAdapter(ctx, docxAdapterScript, adapterRequest)
			})
		}),
		ProjectResult: projectReplacementResult, SuccessStatus: "office_version_written",
	}
	for _, operation := range []string{
		app.DocumentOperationReplaceParagraph,
		app.DocumentOperationInsertParagraph,
		app.DocumentOperationDeleteParagraph,
		app.DocumentOperationSetTextStyle,
	} {
		operation := operation
		provider.Operations[operation] = documentOperationProvider{
			ToolName: "docx." + operation, Summary: docxOperationSummary(operation),
			Validate: validateDOCXOperationInvocation(operation),
			BuildTargets: func(args map[string]any) ([]document.LocatorRequest, int, error) {
				return []document.LocatorRequest{docxEditTarget(operation, args)}, 0, nil
			},
			Editor: document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
				return applyDOCXStructure(ctx, operation, request)
			}),
			SuccessStatus: "docx_version_written",
		}
	}
	provider.Operations[app.DocumentOperationReplaceParagraph] = withDocumentDirectoryBoundary(
		provider.Operations[app.DocumentOperationReplaceParagraph],
		"Use when structured read evidence locates an existing paragraph whose content the owner wants to modify, improve, polish, complete, update, revise, or rewrite.",
		"Do not use when the owner explicitly requests a new paragraph or the target paragraph is absent; use insertion for an additive change.",
	)
	provider.Operations[app.DocumentOperationInsertParagraph] = withDocumentDirectoryBoundary(
		provider.Operations[app.DocumentOperationInsertParagraph],
		"Use only when the owner explicitly requests adding, inserting, or appending a new paragraph, or structured read evidence confirms that no existing target can be replaced.",
		"Do not use to improve, polish, complete, update, revise, or rewrite an existing paragraph; replace that paragraph instead.",
	)
	return provider
}

func docxOperationSummary(operation string) string {
	switch operation {
	case app.DocumentOperationReplaceParagraph:
		return "Replace one existing DOCX paragraph and write a new document."
	case app.DocumentOperationInsertParagraph:
		return "Insert one new DOCX paragraph and write a new document."
	case app.DocumentOperationDeleteParagraph:
		return "Delete one DOCX paragraph and write a new document."
	case app.DocumentOperationSetTextStyle:
		return "Apply a bounded DOCX paragraph style and write a new document."
	default:
		return "Apply a bounded DOCX edit and write a new document."
	}
}

func validateDOCXOperationInvocation(operation string) documentInvocationValidator {
	return func(_ context.Context, _ *ToolHub, _ document.Metadata, args map[string]any) error {
		if operation != app.DocumentOperationReplaceText {
			if err := validateDOCXStructureArguments(operation, args); err != nil {
				return err
			}
		}
		if operation != app.DocumentOperationReplaceText && (operation != app.DocumentOperationInsertParagraph || !isDOCXDocumentBoundaryPosition(args)) &&
			strings.TrimSpace(stringArg(args, "source_hash", "")) == "" {
			return fmt.Errorf("docx.%s requires source_hash preflight evidence", operation)
		}
		return nil
	}
}
