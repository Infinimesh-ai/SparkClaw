package document

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	SmallFileMaxBytes      int64 = 8 << 20
	SmallExtractedMaxBytes       = 200_000
)

type Stage string

const (
	StageInspect   Stage = "inspect"
	StageRead      Stage = "read"
	StageStructure Stage = "structure"
	StageLocate    Stage = "locate"
	StageConstrain Stage = "constrain"
	StageApply     Stage = "apply"
)

type ErrorCode string

const (
	CodeResourceInvalid     ErrorCode = "resource_invalid"
	CodeFormatUnsupported   ErrorCode = "format_unsupported"
	CodeStrategyDeferred    ErrorCode = "strategy_deferred"
	CodeParseFailed         ErrorCode = "parse_failed"
	CodeTargetNotFound      ErrorCode = "target_not_found"
	CodeTargetAmbiguous     ErrorCode = "target_ambiguous"
	CodeMatchCountMismatch  ErrorCode = "match_count_mismatch"
	CodeMutationUnsupported ErrorCode = "mutation_unsupported"
	CodeOutputConflict      ErrorCode = "output_conflict"
)

type PipelineError struct {
	Code   ErrorCode `json:"code"`
	Stage  Stage     `json:"stage"`
	Format string    `json:"format,omitempty"`
	Size   int64     `json:"size,omitempty"`
	Limit  int64     `json:"limit,omitempty"`
	Detail string    `json:"detail"`
}

func (e *PipelineError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{"document", string(e.Stage), string(e.Code) + ":", e.Detail}
	if e.Size > 0 || e.Limit > 0 {
		parts = append(parts, fmt.Sprintf("(size=%d limit=%d)", e.Size, e.Limit))
	}
	return strings.Join(parts, " ")
}

func IsErrorCode(err error, code ErrorCode) bool {
	var pipelineErr *PipelineError
	return errors.As(err, &pipelineErr) && pipelineErr.Code == code
}

type Metadata struct {
	Path        string    `json:"path"`
	Relative    string    `json:"relative_path"`
	Format      string    `json:"format"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256,omitempty"`
	ModifiedAt  time.Time `json:"modified_at"`
}

type StrategyMetadata struct {
	Name              string `json:"name"`
	Mode              string `json:"mode"`
	Reason            string `json:"reason"`
	Complete          bool   `json:"complete"`
	Extensible        bool   `json:"extensible"`
	MaxSourceBytes    int64  `json:"max_source_bytes"`
	MaxExtractedBytes int    `json:"max_extracted_bytes"`
}

type Block struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"`
	Text     string         `json:"text"`
	Location map[string]any `json:"location"`
	Format   map[string]any `json:"format_metadata,omitempty"`
}

type Representation struct {
	SchemaVersion         string           `json:"schema_version"`
	RepresentationVersion string           `json:"representation_version"`
	ID                    string           `json:"id"`
	Format                string           `json:"format"`
	Source                string           `json:"source"`
	Metadata              Metadata         `json:"metadata"`
	Strategy              StrategyMetadata `json:"strategy"`
	ContentScope          map[string]any   `json:"content_scope"`
	Blocks                []Block          `json:"blocks"`
	Paragraphs            []map[string]any `json:"paragraphs,omitempty"`
	Tables                []map[string]any `json:"tables,omitempty"`
	Sheets                []map[string]any `json:"sheets,omitempty"`
	Slides                []map[string]any `json:"slides,omitempty"`
	Sections              []map[string]any `json:"sections,omitempty"`
	Pages                 []map[string]any `json:"pages,omitempty"`
	Stats                 map[string]any   `json:"stats"`
}

func (d Representation) Map() (map[string]any, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type AdapterReadResult struct {
	Content        string
	ExtractedBytes int
	Truncated      bool
	Document       map[string]any
}

type Parser interface {
	Parse(context.Context, Metadata, int) (AdapterReadResult, error)
}

type ParserFunc func(context.Context, Metadata, int) (AdapterReadResult, error)

func (f ParserFunc) Parse(ctx context.Context, metadata Metadata, maxBytes int) (AdapterReadResult, error) {
	return f(ctx, metadata, maxBytes)
}

type LocatorRequest struct {
	Kind            string `json:"kind"`
	Text            string `json:"text,omitempty"`
	BlockID         string `json:"block_id,omitempty"`
	LocationPath    string `json:"location_path,omitempty"`
	ParagraphIndex  int    `json:"paragraph_index,omitempty"`
	Sheet           string `json:"sheet,omitempty"`
	Cell            string `json:"cell,omitempty"`
	Row             int    `json:"row,omitempty"`
	SlideIndex      int    `json:"slide_index,omitempty"`
	PageIndexes     []int  `json:"page_indexes,omitempty"`
	ExpectedMatches int    `json:"expected_matches,omitempty"`
	AllowMultiple   bool   `json:"allow_multiple,omitempty"`
}

type Match struct {
	BlockID    string         `json:"block_id"`
	Kind       string         `json:"kind"`
	Text       string         `json:"text,omitempty"`
	Location   map[string]any `json:"location"`
	Occurrence int            `json:"occurrence,omitempty"`
}

type EditRequest struct {
	Root            string
	Path            string
	OutputPath      string
	Operation       string
	Target          LocatorRequest
	Targets         []LocatorRequest
	ExpectedMatches int
	Arguments       map[string]any
	MaxBytes        int
}

type ApplyRequest struct {
	Metadata Metadata
	Document Representation
	Matches  []Match
	Edit     EditRequest
}

type ApplyResult struct {
	OutputPath string
	Changed    int
	Details    map[string]any
}

type Editor interface {
	Apply(context.Context, ApplyRequest) (ApplyResult, error)
}

type EditorFunc func(context.Context, ApplyRequest) (ApplyResult, error)

func (f EditorFunc) Apply(ctx context.Context, request ApplyRequest) (ApplyResult, error) {
	return f(ctx, request)
}

type ReadRequest struct {
	Root     string
	Path     string
	MaxBytes int
}

type ReadResult struct {
	Metadata Metadata
	Content  string
	Document Representation
}

type ChangeSummary struct {
	DocumentID        string  `json:"document_id"`
	Operation         string  `json:"operation"`
	InputPath         string  `json:"input_path"`
	OutputPath        string  `json:"output_path"`
	OriginalUnchanged bool    `json:"original_unchanged"`
	Matched           int     `json:"matched"`
	Changed           int     `json:"changed"`
	Targets           []Match `json:"targets"`
}

func (s ChangeSummary) Map() (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type EditResult struct {
	Metadata      Metadata
	Document      Representation
	OutputPath    string
	Changed       int
	Details       map[string]any
	ChangeSummary ChangeSummary
}

type Strategy interface {
	Name() string
	Supports(Metadata) bool
	Read(context.Context, Metadata, int) (ReadResult, error)
	Apply(context.Context, ApplyRequest) (ApplyResult, error)
}

type Inspector interface {
	Inspect(context.Context, string, string) (Metadata, error)
}

type InspectorFunc func(context.Context, string, string) (Metadata, error)

func (f InspectorFunc) Inspect(ctx context.Context, root, path string) (Metadata, error) {
	return f(ctx, root, path)
}

type Pipeline struct {
	inspector  Inspector
	strategies []Strategy
}

func NewPipeline(inspector Inspector, strategies ...Strategy) *Pipeline {
	return &Pipeline{inspector: inspector, strategies: append([]Strategy(nil), strategies...)}
}

func (p *Pipeline) Read(ctx context.Context, request ReadRequest) (ReadResult, error) {
	metadata, strategy, err := p.inspectAndSelect(ctx, request.Root, request.Path)
	if err != nil {
		return ReadResult{}, err
	}
	return strategy.Read(ctx, metadata, request.MaxBytes)
}

func (p *Pipeline) Edit(ctx context.Context, request EditRequest) (EditResult, error) {
	metadata, strategy, err := p.inspectAndSelect(ctx, request.Root, request.Path)
	if err != nil {
		return EditResult{}, err
	}
	read, err := strategy.Read(ctx, metadata, request.MaxBytes)
	if err != nil {
		return EditResult{}, err
	}
	targets := append([]LocatorRequest(nil), request.Targets...)
	if len(targets) == 0 {
		targets = []LocatorRequest{request.Target}
	}
	matches := []Match{}
	for _, target := range targets {
		located, locateErr := Locate(read.Document, target)
		if locateErr != nil {
			return EditResult{}, locateErr
		}
		matches = append(matches, located...)
	}
	if request.ExpectedMatches > 0 && len(matches) != request.ExpectedMatches {
		return EditResult{}, &PipelineError{
			Code: CodeMatchCountMismatch, Stage: StageConstrain, Format: metadata.Format,
			Detail: fmt.Sprintf("expected %d target matches, found %d", request.ExpectedMatches, len(matches)),
		}
	}
	if err := validateOutputPath(request.Root, metadata, request.OutputPath); err != nil {
		return EditResult{}, err
	}
	current, err := p.inspector.Inspect(ctx, request.Root, request.Path)
	if err != nil {
		return EditResult{}, err
	}
	if current.Size != metadata.Size || current.SHA256 != metadata.SHA256 || !current.ModifiedAt.Equal(metadata.ModifiedAt) {
		return EditResult{}, &PipelineError{Code: CodeResourceInvalid, Stage: StageConstrain, Format: metadata.Format, Detail: "input document changed after it was structured"}
	}
	applied, err := strategy.Apply(ctx, ApplyRequest{Metadata: metadata, Document: read.Document, Matches: matches, Edit: request})
	if err != nil {
		_ = os.Remove(request.OutputPath)
		return EditResult{}, err
	}
	if applied.Changed <= 0 {
		return EditResult{}, &PipelineError{Code: CodeParseFailed, Stage: StageApply, Format: metadata.Format, Detail: "editor reported no applied changes"}
	}
	return EditResult{
		Metadata: read.Metadata, Document: read.Document, OutputPath: applied.OutputPath, Changed: applied.Changed, Details: applied.Details,
		ChangeSummary: ChangeSummary{
			DocumentID: read.Document.ID, Operation: request.Operation, InputPath: metadata.Path, OutputPath: applied.OutputPath,
			OriginalUnchanged: true, Matched: len(matches), Changed: applied.Changed, Targets: matches,
		},
	}, nil
}

func (p *Pipeline) inspectAndSelect(ctx context.Context, root, path string) (Metadata, Strategy, error) {
	if p == nil || p.inspector == nil {
		return Metadata{}, nil, &PipelineError{Code: CodeResourceInvalid, Stage: StageInspect, Detail: "document inspector is unavailable"}
	}
	metadata, err := p.inspector.Inspect(ctx, root, path)
	if err != nil {
		return Metadata{}, nil, err
	}
	for _, strategy := range p.strategies {
		if strategy != nil && strategy.Supports(metadata) {
			return metadata, strategy, nil
		}
	}
	if metadata.Size > SmallFileMaxBytes {
		return Metadata{}, nil, &PipelineError{
			Code: CodeStrategyDeferred, Stage: StageInspect, Format: metadata.Format, Size: metadata.Size, Limit: SmallFileMaxBytes,
			Detail: "no registered large-document strategy can process this resource",
		}
	}
	return Metadata{}, nil, &PipelineError{Code: CodeFormatUnsupported, Stage: StageRead, Format: metadata.Format, Detail: "no parser is registered for the detected format"}
}

func validateOutputPath(root string, metadata Metadata, outputPath string) error {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		return &PipelineError{Code: CodeOutputConflict, Stage: StageConstrain, Format: metadata.Format, Detail: "output path is required"}
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return &PipelineError{Code: CodeResourceInvalid, Stage: StageConstrain, Format: metadata.Format, Detail: "workspace root is invalid"}
	}
	outputAbs, err := filepath.Abs(outputPath)
	if err != nil || outputAbs == rootAbs || !strings.HasPrefix(outputAbs, rootAbs+string(os.PathSeparator)) {
		return &PipelineError{Code: CodeOutputConflict, Stage: StageConstrain, Format: metadata.Format, Detail: "output path escapes the workspace"}
	}
	if outputAbs == metadata.Path {
		return &PipelineError{Code: CodeOutputConflict, Stage: StageConstrain, Format: metadata.Format, Detail: "output path must not overwrite the input file"}
	}
	wantedExtension := ExtensionForFormat(metadata.Format)
	if wantedExtension == "" || strings.ToLower(filepath.Ext(outputAbs)) != wantedExtension {
		return &PipelineError{Code: CodeOutputConflict, Stage: StageConstrain, Format: metadata.Format, Detail: "output path does not match the detected format"}
	}
	if _, err := os.Lstat(outputAbs); err == nil {
		return &PipelineError{Code: CodeOutputConflict, Stage: StageConstrain, Format: metadata.Format, Detail: "output path already exists"}
	} else if !errors.Is(err, os.ErrNotExist) {
		return &PipelineError{Code: CodeOutputConflict, Stage: StageConstrain, Format: metadata.Format, Detail: "output path is unavailable"}
	}
	relative, _ := filepath.Rel(rootAbs, outputAbs)
	current := rootAbs
	for _, component := range strings.Split(filepath.Dir(relative), string(os.PathSeparator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return &PipelineError{Code: CodeOutputConflict, Stage: StageConstrain, Format: metadata.Format, Detail: "output path must not traverse symlinks or non-directory parents"}
		}
	}
	return nil
}
