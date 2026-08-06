package toolhub

import (
	"context"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func pptxDocumentFormatProvider() documentFormatProvider {
	provider := documentFormatProvider{
		Format: app.DocumentFormatPPTX, Parser: pptxDocumentParser(), ReadToolNames: []string{"files.read"},
		OperationOrder: []string{"replace_text", "add_slide", "update_slide", "update_deck", "duplicate_slide", "delete_slide"},
		Operations:     map[string]documentOperationProvider{},
	}
	provider.Operations["replace_text"] = documentOperationProvider{
		ToolName: "pptx.replace_text", ToolAliases: []string{"office.replace_text"},
		Summary: "Replace exact PPTX text spans without flattening paragraph and run formatting.", Validate: validatePPTXOperationInvocation("replace_text"),
		BuildTargets: exactTextTargets, Editor: document.EditorFunc(applyPPTXReplacement),
		SourceSHA256:  func(args map[string]any) string { return stringArg(args, "source_document_sha256", "") },
		ProjectResult: projectReplacementResult, WrapError: wrapPPTXToolError, SuccessStatus: "pptx_version_written",
	}
	for _, operation := range []string{"add_slide", "update_slide", "update_deck", "duplicate_slide", "delete_slide"} {
		operation := operation
		provider.Operations[operation] = documentOperationProvider{
			ToolName: "pptx." + operation, Summary: pptxOperationSummary(operation), Validate: validatePPTXOperationInvocation(operation),
			BuildTargets: func(args map[string]any) ([]document.LocatorRequest, int, error) {
				target := document.LocatorRequest{Kind: document.LocatorDocument}
				if operation != "add_slide" && operation != "update_deck" {
					target = document.LocatorRequest{Kind: document.LocatorSlide, SlideIndex: intArg(args, "slide_index", 0), AllowMultiple: true}
				}
				return []document.LocatorRequest{target}, 0, nil
			},
			Editor: document.EditorFunc(func(ctx context.Context, request document.ApplyRequest) (document.ApplyResult, error) {
				return applyPPTXStructure(ctx, operation, request)
			}),
			SourceSHA256: func(args map[string]any) string { return stringArg(args, "source_document_sha256", "") },
			WrapError:    wrapPPTXToolError, SuccessStatus: "pptx_version_written",
		}
	}
	return provider
}

func pptxOperationSummary(operation string) string {
	switch operation {
	case "add_slide":
		return "Add one PPTX slide and write a new presentation."
	case "update_deck":
		return "Atomically improve a bounded whole PPTX deck through evidence-bound slide updates."
	case "update_slide":
		return "Improve one existing PPTX slide through evidence-bound text-shape updates with preserve or coordinated layout policy."
	case "duplicate_slide":
		return "Duplicate one PPTX slide and write a new presentation."
	case "delete_slide":
		return "Delete one PPTX slide and write a new presentation."
	default:
		return "Apply a bounded PPTX edit and write a new presentation."
	}
}

func validatePPTXOperationInvocation(operation string) documentInvocationValidator {
	return func(_ context.Context, _ *ToolHub, metadata document.Metadata, args map[string]any) error {
		expected := strings.TrimSpace(stringArg(args, "source_document_sha256", ""))
		if expected == "" {
			return errors.New("PPTX mutation requires source_document_sha256 preflight evidence")
		}
		if metadata.SHA256 == "" || !strings.EqualFold(metadata.SHA256, expected) {
			return errors.New("PPTX mutation source_document_sha256 does not match the current input file")
		}
		return validatePPTXEditArguments(operation, args)
	}
}
