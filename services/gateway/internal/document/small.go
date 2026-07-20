package document

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type SmallFileStrategy struct {
	MaxSourceBytes    int64
	MaxExtractedBytes int
	parsers           map[string]Parser
	editors           map[string]Editor
}

func NewSmallFileStrategy(parsers map[string]Parser, editors map[string]Editor) *SmallFileStrategy {
	return &SmallFileStrategy{
		MaxSourceBytes: SmallFileMaxBytes, MaxExtractedBytes: SmallExtractedMaxBytes,
		parsers: copyParsers(parsers), editors: copyEditors(editors),
	}
}

func (s *SmallFileStrategy) Name() string { return "small_file_v1" }

func (s *SmallFileStrategy) Supports(metadata Metadata) bool {
	return metadata.Size <= s.sourceLimit() && s.parsers[metadata.Format] != nil
}

func (s *SmallFileStrategy) Read(ctx context.Context, metadata Metadata, requestedMax int) (ReadResult, error) {
	if metadata.Size > s.sourceLimit() {
		return ReadResult{}, &PipelineError{
			Code: CodeStrategyDeferred, Stage: StageInspect, Format: metadata.Format, Size: metadata.Size, Limit: s.sourceLimit(),
			Detail: "source exceeds the small-document strategy",
		}
	}
	parser := s.parsers[metadata.Format]
	if parser == nil {
		return ReadResult{}, &PipelineError{Code: CodeFormatUnsupported, Stage: StageRead, Format: metadata.Format, Detail: "no parser is registered for the detected format"}
	}
	maxExtracted := s.extractedLimit()
	if requestedMax > 0 && requestedMax < maxExtracted {
		maxExtracted = requestedMax
	}
	if metadata.Format == "text" && metadata.Size > int64(maxExtracted) {
		return ReadResult{}, deferredExtractedError(metadata, metadata.Size, maxExtracted)
	}
	parsed, err := parser.Parse(ctx, metadata, maxExtracted)
	if err != nil {
		var pipelineErr *PipelineError
		if errors.As(err, &pipelineErr) {
			return ReadResult{}, err
		}
		return ReadResult{}, &PipelineError{Code: CodeParseFailed, Stage: StageRead, Format: metadata.Format, Detail: err.Error()}
	}
	extractedBytes := len([]byte(parsed.Content))
	if parsed.ExtractedBytes > extractedBytes {
		extractedBytes = parsed.ExtractedBytes
	}
	if parsed.Truncated || extractedBytes > maxExtracted {
		return ReadResult{}, deferredExtractedError(metadata, int64(extractedBytes), maxExtracted)
	}
	representation, err := Normalize(metadata, s.Name(), parsed.Content, parsed.Document)
	if err != nil {
		return ReadResult{}, &PipelineError{Code: CodeParseFailed, Stage: StageStructure, Format: metadata.Format, Detail: err.Error()}
	}
	return ReadResult{Metadata: metadata, Content: parsed.Content, Document: representation}, nil
}

func (s *SmallFileStrategy) Apply(ctx context.Context, request ApplyRequest) (ApplyResult, error) {
	editor := s.editors[editorKey(request.Metadata.Format, request.Edit.Operation)]
	if editor == nil {
		return ApplyResult{}, &PipelineError{
			Code: CodeMutationUnsupported, Stage: StageApply, Format: request.Metadata.Format,
			Detail: fmt.Sprintf("operation %q has no registered editor", request.Edit.Operation),
		}
	}
	return editor.Apply(ctx, request)
}

func EditorKey(format, operation string) string { return editorKey(format, operation) }

func editorKey(format, operation string) string {
	return strings.ToLower(strings.TrimSpace(format)) + ":" + strings.ToLower(strings.TrimSpace(operation))
}

func (s *SmallFileStrategy) sourceLimit() int64 {
	if s.MaxSourceBytes <= 0 {
		return SmallFileMaxBytes
	}
	return s.MaxSourceBytes
}

func (s *SmallFileStrategy) extractedLimit() int {
	if s.MaxExtractedBytes <= 0 {
		return SmallExtractedMaxBytes
	}
	return s.MaxExtractedBytes
}

func deferredExtractedError(metadata Metadata, size int64, limit int) error {
	return &PipelineError{
		Code: CodeStrategyDeferred, Stage: StageRead, Format: metadata.Format, Size: size, Limit: int64(limit),
		Detail: "complete extracted content exceeds the small-document strategy; chunked or streaming processing is not registered",
	}
}

func copyParsers(source map[string]Parser) map[string]Parser {
	out := make(map[string]Parser, len(source))
	for format, parser := range source {
		out[strings.ToLower(strings.TrimSpace(format))] = parser
	}
	return out
}

func copyEditors(source map[string]Editor) map[string]Editor {
	out := make(map[string]Editor, len(source))
	for key, editor := range source {
		out[strings.ToLower(strings.TrimSpace(key))] = editor
	}
	return out
}
