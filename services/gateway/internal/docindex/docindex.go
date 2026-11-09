// Package docindex builds and queries the structure-first retrieval index of
// documents of every size: an outline, anchored text chunks
// and a BM25 keyword index, persisted per source SHA-256 under the owning
// workspace's control directory. Format parsing is supplied by the caller
// through Extractor; this package never reads document formats itself.
package docindex

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

const (
	// SchemaVersion changes when the persisted layout changes.
	SchemaVersion = 2
	// BuilderVersion changes when extraction guarantees, chunking or tokenization
	// change, so older indexes are rebuilt instead of mixed with new ones.
	BuilderVersion = "docindex_structure_bm25_v3"
)

// State is the lifecycle state recorded in an index manifest.
type State string

const (
	StateBuilding State = "building"
	StateReady    State = "ready"
	// StatePartial is a ready index with pages the extractor could not read,
	// such as PDF pages without a text layer.
	StatePartial State = "partial"
	StateFailed  State = "failed"
)

// Usable reports whether queries may run against an index in this state.
func (s State) Usable() bool { return s == StateReady || s == StatePartial }

// Manifest describes one persisted index.
type Manifest struct {
	StructureGeneration string   `json:"structure_generation"`
	EnrichmentVersion   string   `json:"enrichment_version,omitempty"`
	SchemaVersion       int      `json:"schema_version"`
	BuilderVersion      string   `json:"builder_version"`
	SourceSHA256        string   `json:"source_sha256"`
	SourceBytes         int64    `json:"source_bytes"`
	Format              string   `json:"format"`
	SourcePaths         []string `json:"source_paths"`
	State               State    `json:"state"`
	Attempts            int      `json:"attempts"`
	Error               string   `json:"error,omitempty"`
	Pages               int      `json:"pages,omitempty"`
	Chunks              int      `json:"chunks"`
	TextBytes           int      `json:"text_bytes"`
	UnindexedPages      []int    `json:"unindexed_pages,omitempty"`
	// OCRPages are pages whose text came from analysis-phase OCR.
	OCRPages   []int     `json:"ocr_pages,omitempty"`
	IndexBytes int64     `json:"index_bytes"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// Anchor locates a chunk in the source document with the existing citation
// vocabulary: a 1-based page, the heading path, and a stable block location.
type Anchor struct {
	Page        int      `json:"page,omitempty"`
	PageEnd     int      `json:"page_end,omitempty"`
	HeadingPath []string `json:"heading_path,omitempty"`
	Block       string   `json:"block,omitempty"`
}

// Chunk is one retrievable span of document text.
type Chunk struct {
	NodeID     string `json:"node_id,omitempty"`
	ByteStart  int    `json:"byte_start,omitempty"`
	ByteEnd    int    `json:"byte_end,omitempty"`
	SearchText string `json:"search_text,omitempty"`
	ID         string `json:"id"`
	Text       string `json:"text"`
	Anchor     Anchor `json:"anchor"`
}

// OutlineNode is one heading of the document structure. Chunks
// [ChunkStart, ChunkEnd) and pages [Page, PageEnd] belong to the node and
// its descendants.
type OutlineNode struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Level      int    `json:"level"`
	Page       int    `json:"page,omitempty"`
	PageEnd    int    `json:"page_end,omitempty"`
	ChunkStart int    `json:"chunk_start"`
	ChunkEnd   int    `json:"chunk_end"`
}

// Unit is one piece of extracted text in document order: a paragraph, a
// heading, a table row or a page. HeadingLevel is positive for headings.
type Unit struct {
	StructureSource string                    `json:"structure_source,omitempty"`
	GroupKey        string                    `json:"group_key,omitempty"`
	NodeID          string                    `json:"node_id,omitempty"`
	Kind            string                    `json:"kind,omitempty"`
	Title           string                    `json:"title,omitempty"`
	SearchText      string                    `json:"search_text,omitempty"`
	ByteStart       int                       `json:"byte_start,omitempty"`
	ByteEnd         int                       `json:"byte_end,omitempty"`
	Values          []map[string]any          `json:"values,omitempty"`
	Dependencies    []string                  `json:"dependencies,omitempty"`
	Availability    document.NodeAvailability `json:"availability,omitempty"`
	Provenance      string                    `json:"provenance,omitempty"`
	Reason          string                    `json:"reason,omitempty"`
	Text            string                    `json:"text"`
	Page            int                       `json:"page,omitempty"`
	HeadingLevel    int                       `json:"heading_level,omitempty"`
	Block           string                    `json:"block,omitempty"`
}

// OutlineEntry is a heading reported by the format itself, such as a PDF
// bookmark, independent of the text units.
type OutlineEntry struct {
	Title string `json:"title"`
	Level int    `json:"level"`
	Page  int    `json:"page"`
}

// SourceInfo is what an extractor knows about the whole document.
type SourceInfo struct {
	Limitations    []string       `json:"limitations,omitempty"`
	Pages          int            `json:"pages,omitempty"`
	Outline        []OutlineEntry `json:"outline,omitempty"`
	UnindexedPages []int          `json:"unindexed_pages,omitempty"`
}

// Extractor streams the text units of one document in order. Implementations
// bound their own subprocess time per batch and never run OCR.
type Extractor interface {
	Extract(ctx context.Context, path, format string, emit func(Unit) error) (SourceInfo, error)
}

// Acquisition describes this call only; it is not part of canonical source
// identity, persisted parser output or cursor generation.
const (
	AcquisitionMemoryCache = "memory_cache"
	AcquisitionDiskCache   = "disk_cache"
	AcquisitionBuilt       = "built"
	AcquisitionSharedBuild = "shared_build"
	AcquisitionRebuilt     = "rebuilt"
)
