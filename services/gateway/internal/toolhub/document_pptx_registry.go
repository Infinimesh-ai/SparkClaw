package toolhub

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func pptxDocumentFormatProvider() documentFormatProvider {
	provider := documentFormatProvider{
		Format: app.DocumentFormatPPTX, Parser: pptxDocumentParser(), ReadToolNames: []string{"files.read"},
		OperationOrder: canonicalDocumentOperationOrder(app.DocumentFormatPPTX),
		Operations:     map[string]documentOperationProvider{},
	}
	provider.Operations[app.DocumentOperationReplaceText] = documentOperationProvider{
		ToolName: "pptx.replace_text", ToolAliases: []string{"office.replace_text"},
		Summary: "Replace exact PPTX text spans without flattening paragraph and run formatting.", Validate: validatePPTXOperationInvocation(app.DocumentOperationReplaceText),
		BuildTargets: exactTextTargets, Editor: document.EditorFunc(applyPPTXReplacement),
		ProjectResult: projectReplacementResult, WrapError: wrapPPTXToolError, SuccessStatus: "pptx_version_written",
	}
	for _, operation := range []string{
		app.DocumentOperationAddSlide,
		app.DocumentOperationUpdateSlide,
		app.DocumentOperationUpdateDeck,
		app.DocumentOperationDuplicateSlide,
		app.DocumentOperationDeleteSlide,
	} {
		operation := operation
		provider.Operations[operation] = documentOperationProvider{
			ToolName: "pptx." + operation, Summary: pptxOperationSummary(operation), Validate: validatePPTXOperationInvocation(operation),
			BuildTargets: func(args map[string]any) ([]document.LocatorRequest, int, error) {
				target := document.LocatorRequest{Kind: document.LocatorDocument}
				if operation != app.DocumentOperationAddSlide && operation != app.DocumentOperationUpdateDeck {
					target = document.LocatorRequest{Kind: document.LocatorSlide, SlideIndex: intArg(args, "slide_index", 0), AllowMultiple: true}
				}
				return []document.LocatorRequest{target}, 0, nil
			},
			Editor: document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
				return applyPPTXStructure(ctx, operation, request)
			}),
			WrapError: wrapPPTXToolError, SuccessStatus: "pptx_version_written",
		}
	}
	return provider
}

func pptxOperationSummary(operation string) string {
	switch operation {
	case app.DocumentOperationAddSlide:
		return "Add one PPTX slide and write a new presentation."
	case app.DocumentOperationUpdateDeck:
		return "Atomically improve a bounded whole PPTX deck through evidence-bound slide updates."
	case app.DocumentOperationUpdateSlide:
		return "Improve one existing PPTX slide through evidence-bound text-shape updates with preserve or coordinated layout policy."
	case app.DocumentOperationDuplicateSlide:
		return "Duplicate one PPTX slide and write a new presentation."
	case app.DocumentOperationDeleteSlide:
		return "Delete one PPTX slide and write a new presentation."
	default:
		return "Apply a bounded PPTX edit and write a new presentation."
	}
}

func validatePPTXOperationInvocation(operation string) documentInvocationValidator {
	return func(_ context.Context, _ *ToolHub, _ document.Metadata, args map[string]any) error {
		return validatePPTXEditArguments(operation, args)
	}
}
