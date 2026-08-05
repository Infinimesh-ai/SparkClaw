package toolhub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

type documentInvocationValidator func(context.Context, *ToolHub, document.Metadata, map[string]any) error
type documentTargetBuilder func(map[string]any) ([]document.LocatorRequest, int, error)
type documentResultProjector func(map[string]any, document.EditResult)
type documentErrorWrapper func(context.Context, error) error

type documentOperationProvider struct {
	ToolName      string
	ToolAliases   []string
	Summary       string
	WhenToUse     string
	WhenNotToUse  string
	Validate      documentInvocationValidator
	BuildTargets  documentTargetBuilder
	Editor        document.Editor
	SourceSHA256  func(map[string]any) string
	ProjectResult documentResultProjector
	WrapError     documentErrorWrapper
	SuccessStatus string
}

type documentFormatProvider struct {
	Format         string
	Parser         document.Parser
	ReadToolNames  []string
	OperationOrder []string
	Operations     map[string]documentOperationProvider
}

type documentProviderRegistry struct {
	formats map[string]documentFormatProvider
}

func newDocumentProviderRegistry(providers ...documentFormatProvider) documentProviderRegistry {
	registry := documentProviderRegistry{formats: make(map[string]documentFormatProvider, len(providers))}
	for _, provider := range providers {
		format := canonicalDocumentKey(provider.Format)
		if format == "" {
			panic("toolhub: document format provider has an empty format")
		}
		if _, exists := registry.formats[format]; exists {
			panic(fmt.Sprintf("toolhub: duplicate document format provider %q", format))
		}
		provider.Format = format
		normalized := make(map[string]documentOperationProvider, len(provider.Operations))
		for operation, candidate := range provider.Operations {
			operation = canonicalDocumentKey(operation)
			if operation == "" || strings.TrimSpace(candidate.ToolName) == "" || candidate.Editor == nil || candidate.BuildTargets == nil {
				panic(fmt.Sprintf("toolhub: incomplete document operation provider %s:%s", format, operation))
			}
			if _, exists := normalized[operation]; exists {
				panic(fmt.Sprintf("toolhub: duplicate document operation provider %s:%s", format, operation))
			}
			normalized[operation] = candidate
		}
		provider.Operations = normalized
		registry.formats[format] = provider
	}
	return registry
}

func canonicalDocumentKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (r documentProviderRegistry) provider(format string) (documentFormatProvider, bool) {
	provider, ok := r.formats[canonicalDocumentKey(format)]
	return provider, ok
}

func (r documentProviderRegistry) operation(format, operation string) (documentOperationProvider, bool) {
	provider, ok := r.provider(format)
	if !ok {
		return documentOperationProvider{}, false
	}
	operationProvider, ok := provider.Operations[canonicalDocumentKey(operation)]
	return operationProvider, ok
}

func (r documentProviderRegistry) errorWrapper(toolName, operation string) documentErrorWrapper {
	operation = canonicalDocumentKey(operation)
	for _, provider := range r.formats {
		candidate, ok := provider.Operations[operation]
		if ok && candidate.ToolName == toolName && candidate.WrapError != nil {
			return candidate.WrapError
		}
	}
	return nil
}

func (p documentOperationProvider) acceptsTool(toolName string) bool {
	if p.ToolName == toolName {
		return true
	}
	for _, alias := range p.ToolAliases {
		if alias == toolName {
			return true
		}
	}
	return false
}

func (r documentProviderRegistry) parsers() map[string]document.Parser {
	parsers := make(map[string]document.Parser, len(r.formats))
	for format, provider := range r.formats {
		if provider.Parser != nil {
			parsers[format] = provider.Parser
		}
	}
	return parsers
}

func (r documentProviderRegistry) editors() map[string]document.Editor {
	editors := map[string]document.Editor{}
	for format, provider := range r.formats {
		for operation, candidate := range provider.Operations {
			editors[document.EditorKey(format, operation)] = candidate.Editor
		}
	}
	return editors
}

func documentEditExecutor(operation string) toolExecutor {
	return func(h *ToolHub, ctx context.Context, toolName string, args map[string]any, _, _ string) (Result, error) {
		return h.executeDocumentOperation(ctx, toolName, operation, args)
	}
}

func (h *ToolHub) executeDocumentOperation(ctx context.Context, toolName, requestedOperation string, args map[string]any) (Result, error) {
	operation := canonicalDocumentKey(requestedOperation)
	if operation == "" {
		operation = canonicalDocumentKey(stringArg(args, "operation", ""))
	}
	if operation == "" {
		return Result{}, errors.New("document operation is required")
	}
	registry := toolhubDocumentProviderRegistry()
	earlyWrapper := registry.errorWrapper(toolName, operation)
	inputPath, err := h.resolvePath(stringArg(args, "path", ""))
	if err != nil {
		return Result{}, wrapEarlyDocumentProviderError(ctx, earlyWrapper, err)
	}
	outputPath, err := h.resolveNewOutputPath(stringArg(args, "output_path", ""))
	if err != nil {
		return Result{}, wrapEarlyDocumentProviderError(ctx, earlyWrapper, err)
	}
	metadata, err := document.InspectFile(ctx, h.cfg.Workspaces.DefaultRoot, inputPath)
	if err != nil {
		return Result{}, wrapEarlyDocumentProviderError(ctx, earlyWrapper, err)
	}
	provider, ok := registry.operation(metadata.Format, operation)
	if !ok || !provider.acceptsTool(toolName) {
		return Result{}, &document.PipelineError{
			Code: document.CodeMutationUnsupported, Stage: document.StageConstrain, Format: metadata.Format,
			Detail: fmt.Sprintf("operation %q is not registered for tool %q", operation, toolName),
		}
	}
	if provider.Validate != nil {
		if err := provider.Validate(ctx, h, metadata, args); err != nil {
			return Result{}, wrapDocumentProviderError(ctx, provider, err)
		}
	}
	targets, expectedMatches, err := provider.BuildTargets(args)
	if err != nil {
		return Result{}, wrapDocumentProviderError(ctx, provider, err)
	}
	request := document.EditRequest{
		Path: inputPath, OutputPath: outputPath, Operation: operation,
		Targets: targets, ExpectedMatches: expectedMatches,
		Arguments: args, MaxBytes: document.SmallExtractedMaxBytes,
	}
	if len(targets) > 0 {
		request.Target = targets[0]
	}
	if provider.SourceSHA256 != nil {
		request.SourceSHA256 = strings.TrimSpace(provider.SourceSHA256(args))
	}
	result, err := h.editDocumentWorkflow(ctx, request)
	if err != nil {
		return Result{}, wrapDocumentProviderError(ctx, provider, err)
	}
	output := documentChangeOutput(result, provider.SuccessStatus)
	if provider.ProjectResult != nil {
		provider.ProjectResult(output, result)
	}
	return Result{Output: output}, nil
}

func wrapEarlyDocumentProviderError(ctx context.Context, wrapper documentErrorWrapper, err error) error {
	if wrapper != nil {
		return wrapper(ctx, err)
	}
	return err
}

func wrapDocumentProviderError(ctx context.Context, provider documentOperationProvider, err error) error {
	if provider.WrapError != nil {
		return provider.WrapError(ctx, err)
	}
	return err
}

func exactTextTargets(args map[string]any) ([]document.LocatorRequest, int, error) {
	replacements, err := replacementArgs(args["replacements"])
	if err != nil {
		return nil, 0, err
	}
	expected := intArg(args, "expected_replacements", 0)
	targets := make([]document.LocatorRequest, 0, len(replacements))
	for _, replacement := range replacements {
		targets = append(targets, document.LocatorRequest{
			Kind: document.LocatorExactText, Text: replacement.Find, AllowMultiple: expected > 0,
		})
	}
	return targets, expected, nil
}

func projectReplacementResult(output map[string]any, result document.EditResult) {
	output["replacements"] = result.Changed
}

func withDocumentDirectoryBoundary(provider documentOperationProvider, whenToUse, whenNotToUse string) documentOperationProvider {
	provider.WhenToUse = whenToUse
	provider.WhenNotToUse = whenNotToUse
	return provider
}

func textDocumentFormatProvider() documentFormatProvider {
	return documentFormatProvider{
		Format: "text", Parser: document.ParserFunc(parseTextDocument), ReadToolNames: []string{"files.read"},
		OperationOrder: []string{"replace_text"},
		Operations: map[string]documentOperationProvider{
			"replace_text": {
				ToolName: "text.replace_text", Summary: "Replace bounded text and write a new plain-text output copy.",
				BuildTargets:  exactTextTargets,
				Editor:        document.EditorFunc(applyTextReplacement),
				ProjectResult: projectReplacementResult, SuccessStatus: "text_version_written",
			},
		},
	}
}

var (
	documentProvidersOnce sync.Once
	documentProviders     documentProviderRegistry
)

func toolhubDocumentProviderRegistry() documentProviderRegistry {
	documentProvidersOnce.Do(func() {
		documentProviders = newDocumentProviderRegistry(documentFormatProvidersFromCatalog()...)
	})
	return documentProviders
}
