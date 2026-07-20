package app

// Document pipeline vocabulary shared between the document tools and any
// future pipeline consumers. Keep these constants as the single source of
// truth for the wire values emitted in the files.read "pipeline" envelope.

type DocumentStrategyMode string

const (
	DocumentStrategySmallDirect DocumentStrategyMode = "small_direct"
)

type DocumentContextMode string

const (
	DocumentContextFullText DocumentContextMode = "full_text"
)

type DocumentProcessingStatus string

const (
	DocumentProcessingSucceeded DocumentProcessingStatus = "succeeded"
)

type DocumentIndexStatus string

const (
	DocumentIndexSkipped DocumentIndexStatus = "skipped"
)
