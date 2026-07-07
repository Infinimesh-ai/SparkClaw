package app

import "time"

type DocumentTaskType string

const (
	DocumentTaskSummary DocumentTaskType = "summary"
	DocumentTaskQA      DocumentTaskType = "qa"
	DocumentTaskExtract DocumentTaskType = "extract"
	DocumentTaskCompare DocumentTaskType = "compare"
	DocumentTaskEdit    DocumentTaskType = "edit"
)

type DocumentJobStatus string

const (
	DocumentJobPending   DocumentJobStatus = "pending"
	DocumentJobRunning   DocumentJobStatus = "running"
	DocumentJobSucceeded DocumentJobStatus = "succeeded"
	DocumentJobPartial   DocumentJobStatus = "partial"
	DocumentJobFailed    DocumentJobStatus = "failed"
)

type DocumentStrategyMode string

const (
	DocumentStrategySmallDirect    DocumentStrategyMode = "small_direct"
	DocumentStrategyMediumHybrid   DocumentStrategyMode = "medium_hybrid"
	DocumentStrategyLargeRetrieval DocumentStrategyMode = "large_retrieval"
)

type DocumentContextMode string

const (
	DocumentContextFullText  DocumentContextMode = "full_text"
	DocumentContextSummary   DocumentContextMode = "summary"
	DocumentContextRetrieval DocumentContextMode = "retrieval"
	DocumentContextHybrid    DocumentContextMode = "hybrid"
)

type DocumentProcessingStatus string

const (
	DocumentProcessingSucceeded DocumentProcessingStatus = "succeeded"
	DocumentProcessingPartial   DocumentProcessingStatus = "partial"
	DocumentProcessingFailed    DocumentProcessingStatus = "failed"
)

type DocumentIndexStatus string

const (
	DocumentIndexReady   DocumentIndexStatus = "ready"
	DocumentIndexSkipped DocumentIndexStatus = "skipped"
	DocumentIndexFailed  DocumentIndexStatus = "failed"
)

type DocumentJob struct {
	ID         string             `json:"id"`
	DocumentID string             `json:"document_id"`
	UserID     string             `json:"user_id"`
	TaskType   DocumentTaskType   `json:"task_type"`
	Status     DocumentJobStatus  `json:"status"`
	Options    DocumentJobOptions `json:"options,omitempty"`
	Error      *ProcessingError   `json:"error,omitempty"`
	Telemetry  *DocumentTelemetry `json:"telemetry,omitempty"`
	CreatedAt  time.Time          `json:"created_at,omitempty"`
	UpdatedAt  time.Time          `json:"updated_at,omitempty"`
}

type DocumentJobOptions struct {
	Language     string `json:"language,omitempty"`
	OutputStyle  string `json:"output_style,omitempty"`
	NeedCitation bool   `json:"need_citation,omitempty"`
}

type ParsedDocument struct {
	DocumentID string                 `json:"document_id"`
	Text       string                 `json:"text"`
	Pages      []ParsedDocumentPage   `json:"pages,omitempty"`
	Metadata   ParsedDocumentMetadata `json:"metadata,omitempty"`
}

type ParsedDocumentPage struct {
	PageNumber int             `json:"page_number"`
	Text       string          `json:"text"`
	Blocks     []DocumentBlock `json:"blocks,omitempty"`
}

type ParsedDocumentMetadata struct {
	Title     string `json:"title,omitempty"`
	Author    string `json:"author,omitempty"`
	FileType  string `json:"file_type,omitempty"`
	PageCount int    `json:"page_count,omitempty"`
}

type DocumentProfile struct {
	PageCount        int    `json:"page_count"`
	CharCount        int    `json:"char_count"`
	TokenEstimate    int    `json:"token_estimate"`
	Language         string `json:"language,omitempty"`
	HasTables        bool   `json:"has_tables"`
	HasImages        bool   `json:"has_images"`
	IsScanned        bool   `json:"is_scanned"`
	StructureQuality string `json:"structure_quality,omitempty"`
	Complexity       string `json:"complexity,omitempty"`
}

type DocumentStrategy struct {
	Strategy    DocumentStrategyMode   `json:"strategy"`
	ContextMode DocumentContextMode    `json:"context_mode"`
	Reason      string                 `json:"reason,omitempty"`
	Limits      DocumentStrategyLimits `json:"limits,omitempty"`
}

type DocumentStrategyLimits struct {
	MaxInputTokens int `json:"max_input_tokens,omitempty"`
	MaxChunks      int `json:"max_chunks,omitempty"`
	MaxLatencyMS   int `json:"max_latency_ms,omitempty"`
}

type NormalizedDocument struct {
	DocumentID string            `json:"document_id"`
	Sections   []DocumentSection `json:"sections,omitempty"`
	Blocks     []DocumentBlock   `json:"blocks,omitempty"`
}

type DocumentSection struct {
	SectionID string `json:"section_id"`
	Title     string `json:"title,omitempty"`
	Level     int    `json:"level,omitempty"`
	Text      string `json:"text,omitempty"`
	PageRange []int  `json:"page_range,omitempty"`
}

type DocumentBlock struct {
	BlockID    string         `json:"block_id,omitempty"`
	DocumentID string         `json:"document_id,omitempty"`
	FileType   string         `json:"file_type,omitempty"`
	Type       string         `json:"type"`
	Text       string         `json:"text,omitempty"`
	PageNumber int            `json:"page_number,omitempty"`
	Location   map[string]any `json:"location,omitempty"`
	SourceHash string         `json:"source_hash,omitempty"`
}

type EvidenceBlock struct {
	BlockID    string                `json:"block_id"`
	DocumentID string                `json:"document_id"`
	FileType   string                `json:"file_type"`
	Type       string                `json:"type"`
	Text       string                `json:"text"`
	Location   EvidenceBlockLocation `json:"location,omitempty"`
	SourceHash string                `json:"source_hash"`
}

type EvidenceBlockLocation struct {
	PageNumber     int            `json:"pageNumber,omitempty"`
	ParagraphIndex int            `json:"paragraphIndex,omitempty"`
	SectionPath    []string       `json:"sectionPath,omitempty"`
	HeadingPath    []string       `json:"headingPath,omitempty"`
	TableID        string         `json:"tableId,omitempty"`
	RowIndex       int            `json:"rowIndex,omitempty"`
	ColumnIndex    int            `json:"columnIndex,omitempty"`
	SheetName      string         `json:"sheetName,omitempty"`
	SlideNumber    int            `json:"slideNumber,omitempty"`
	BBox           map[string]any `json:"bbox,omitempty"`
	CharStart      int            `json:"charStart,omitempty"`
	CharEnd        int            `json:"charEnd,omitempty"`
}

type DocumentArtifact struct {
	DocumentID string                   `json:"document_id"`
	ArtifactID string                   `json:"artifact_id"`
	TaskType   DocumentTaskType         `json:"task_type"`
	Status     DocumentProcessingStatus `json:"status"`
	Output     DocumentArtifactOutput   `json:"output,omitempty"`
	Evidence   []DocumentEvidence       `json:"evidence,omitempty"`
	Warnings   []string                 `json:"warnings,omitempty"`
}

type DocumentArtifactOutput struct {
	Summary         string         `json:"summary,omitempty"`
	Outline         []string       `json:"outline,omitempty"`
	KeyPoints       []string       `json:"key_points,omitempty"`
	ExtractedFields map[string]any `json:"extracted_fields,omitempty"`
}

type DocumentEvidence struct {
	Text       string         `json:"text"`
	PageNumber int            `json:"page_number,omitempty"`
	BlockID    string         `json:"block_id,omitempty"`
	Quote      string         `json:"quote,omitempty"`
	Location   map[string]any `json:"location,omitempty"`
}

type DocumentIndexBuildResult struct {
	DocumentID  string              `json:"document_id"`
	IndexStatus DocumentIndexStatus `json:"index_status"`
	Indexes     DocumentIndexes     `json:"indexes,omitempty"`
	Reason      string              `json:"reason,omitempty"`
}

type DocumentIndexes struct {
	VectorIndexID  string `json:"vector_index_id,omitempty"`
	KeywordIndexID string `json:"keyword_index_id,omitempty"`
	SummaryIndexID string `json:"summary_index_id,omitempty"`
}

type ContextBundle struct {
	Mode          DocumentContextMode `json:"mode"`
	Items         []ContextBundleItem `json:"items,omitempty"`
	Citations     []DocumentCitation  `json:"citations,omitempty"`
	TokenEstimate int                 `json:"token_estimate,omitempty"`
	Warnings      []string            `json:"warnings,omitempty"`
}

type ContextBundleItem struct {
	Type      string  `json:"type"`
	Text      string  `json:"text"`
	PageRange []int   `json:"page_range,omitempty"`
	Score     float64 `json:"score,omitempty"`
}

type DocumentCitation struct {
	PageNumber int    `json:"page_number,omitempty"`
	BlockID    string `json:"block_id,omitempty"`
	Quote      string `json:"quote"`
}

type AgentAnswer struct {
	Status    DocumentProcessingStatus `json:"status"`
	Answer    string                   `json:"answer,omitempty"`
	Document  AgentAnswerDocument      `json:"document,omitempty"`
	Citations []DocumentCitation       `json:"citations,omitempty"`
	Warnings  []string                 `json:"warnings,omitempty"`
	Debug     *AgentAnswerDebug        `json:"debug,omitempty"`
}

type AgentAnswerDocument struct {
	DocumentID string `json:"document_id,omitempty"`
	Filename   string `json:"filename,omitempty"`
}

type AgentAnswerDebug struct {
	Strategy      DocumentStrategyMode `json:"strategy,omitempty"`
	ContextMode   DocumentContextMode  `json:"context_mode,omitempty"`
	TokenEstimate int                  `json:"token_estimate,omitempty"`
}

type ProcessingError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type DocumentTelemetry struct {
	JobID         string               `json:"job_id,omitempty"`
	DocumentID    string               `json:"document_id,omitempty"`
	Strategy      DocumentStrategyMode `json:"strategy,omitempty"`
	FileType      string               `json:"file_type,omitempty"`
	PageCount     int                  `json:"page_count,omitempty"`
	TokenEstimate int                  `json:"token_estimate,omitempty"`
	ParseLatency  int                  `json:"parse_latency,omitempty"`
	ModelLatency  int                  `json:"model_latency,omitempty"`
	TotalLatency  int                  `json:"total_latency,omitempty"`
	ErrorCode     string               `json:"error_code,omitempty"`
	FallbackUsed  bool                 `json:"fallback_used"`
}
