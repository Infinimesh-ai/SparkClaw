package toolhub

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func pdfDocumentFormatProvider() documentFormatProvider {
	provider := documentFormatProvider{
		Format: app.DocumentFormatPDF, ReadToolNames: []string{"pdf.extract_text"},
		OperationOrder: canonicalDocumentOperationOrder(app.DocumentFormatPDF),
		Parser: adapterDocumentParser(func(ctx context.Context, request map[string]any) (map[string]any, error) {
			request["operation"] = "read"
			return runPDFPython(ctx, request)
		}),
		Operations: map[string]documentOperationProvider{},
	}
	for _, operation := range []string{
		app.DocumentOperationExtractPages,
		app.DocumentOperationDeletePages,
		app.DocumentOperationRotatePages,
		app.DocumentOperationSplit,
	} {
		operation := operation
		provider.Operations[operation] = documentOperationProvider{
			ToolName: "pdf.transform", Summary: "Apply a bounded PDF transform and write an output copy.",
			Validate: func(_ context.Context, _ *ToolHub, _ document.Metadata, args map[string]any) error {
				return validatePDFTransformArguments(args)
			},
			BuildTargets: func(args map[string]any) ([]document.LocatorRequest, int, error) {
				target := document.LocatorRequest{Kind: document.LocatorDocument}
				if operation != app.DocumentOperationSplit {
					target = document.LocatorRequest{Kind: document.LocatorPages, PageIndexes: intList(args["pages"]), AllowMultiple: true}
				}
				return []document.LocatorRequest{target}, 0, nil
			},
			Editor: document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
				return applyPDFTransform(ctx, operation, request)
			}),
			SuccessStatus: "pdf_version_written",
		}
	}
	return provider
}
