package app

// Document pipeline vocabulary shared between the document tools and any
// future pipeline consumers. Keep these constants as the single source of
// truth for the wire values emitted in the files.read "pipeline" envelope.

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
