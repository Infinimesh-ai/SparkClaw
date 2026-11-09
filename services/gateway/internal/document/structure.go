package document

// StructureSchemaVersion identifies the canonical, source-bound node contract.
const StructureSchemaVersion = 2
const StructureParserVersion = "structure_tree_v1"

// Persisted read revisions 4–8 retain their original node IDs and generations.
const LegacyStructureSchemaVersion = 1
const LegacyStructureParserVersion = "native_structure_v1"

type NodeAvailability string

const (
	NodeAvailable    NodeAvailability = "available"
	NodeUnread       NodeAvailability = "unread"
	NodeUnrecognized NodeAvailability = "unrecognized"
	NodeUnsupported  NodeAvailability = "unsupported"
	NodeFailed       NodeAvailability = "failed"
)

type StructureManifest struct {
	SchemaVersion       int               `json:"schema_version"`
	ParserVersion       string            `json:"parser_version"`
	SourceSHA256        string            `json:"source_sha256"`
	StructureGeneration string            `json:"structure_generation"`
	EnrichmentVersion   string            `json:"enrichment_version,omitempty"`
	Format              string            `json:"format"`
	Nodes               int               `json:"nodes"`
	Capabilities        []string          `json:"capabilities"`
	Limitations         []string          `json:"limitations"`
	Coverage            StructureCoverage `json:"coverage"`
}

// A native locator identifies parser output, not a promise of editing fidelity.
type StructureLocator struct {
	Block     string `json:"block"`
	Page      int    `json:"page,omitempty"`
	ByteStart int    `json:"byte_start,omitempty"`
	ByteEnd   int    `json:"byte_end,omitempty"`
}

type StructureNode struct {
	ID               string            `json:"id"`
	ParentID         string            `json:"parent_id,omitempty"`
	Order            int               `json:"order"`
	Kind             string            `json:"kind"`
	Title            string            `json:"title,omitempty"`
	Locator          StructureLocator  `json:"locator"`
	Text             string            `json:"text"`
	Values           []map[string]any  `json:"values,omitempty"`
	Dependencies     []string          `json:"dependencies,omitempty"`
	Provenance       string            `json:"provenance"`
	Availability     NodeAvailability  `json:"availability"`
	Reason           string            `json:"reason,omitempty"`
	Level            int               `json:"level,omitempty"`
	StructureSource  string            `json:"structure_source,omitempty"`
	Span             *StructureSpan    `json:"span,omitempty"`
	Stats            *StructureStats   `json:"stats,omitempty"`
	ChildrenComplete *bool             `json:"children_complete,omitempty"`
	Expandable       *bool             `json:"expandable,omitempty"`
	Lead             *StructurePreview `json:"lead,omitempty"`
	Preview          *StructurePreview `json:"preview,omitempty"`
}

func (node StructureNode) IsContainer() bool { return node.Span != nil }

// OrdinalEnd is exclusive; ordinals address canonical leaves, never containers.
type StructureSpan struct {
	FirstBlock   string `json:"first_block"`
	LastBlock    string `json:"last_block"`
	OrdinalStart int    `json:"ordinal_start"`
	OrdinalEnd   int    `json:"ordinal_end"`
	PageStart    int    `json:"page_start,omitempty"`
	PageEnd      int    `json:"page_end,omitempty"`
}

type StructureStats struct {
	ChildContainers int `json:"child_containers"`
	Leaves          int `json:"leaves"`
	TextBytes       int `json:"text_bytes"`
	EstimatedTokens int `json:"estimated_tokens"`
	Tables          int `json:"tables"`
	Unavailable     int `json:"unavailable"`
	Unrecognized    int `json:"unrecognized"`
}

type StructurePreview struct {
	Text      string `json:"text"`
	IsPreview bool   `json:"is_preview"`
}

// Coverage is scoped and factual. Acquired source content and content delivered
// by a read are separate dimensions; neither establishes semantic task success.
type StructureCoverage struct {
	Scope                      string   `json:"scope"`
	Status                     string   `json:"status"`
	DenominatorKnown           bool     `json:"denominator_known"`
	Required                   int      `json:"required"`
	Covered                    int      `json:"covered"`
	Missing                    int      `json:"missing"`
	MissingIDsTruncated        bool     `json:"missing_ids_truncated,omitempty"`
	MissingNodeIDs             []string `json:"missing_node_ids,omitempty"`
	UnrecognizedPageCount      int      `json:"unrecognized_page_count,omitempty"`
	UnrecognizedPages          []int    `json:"unrecognized_pages,omitempty"`
	UnrecognizedPagesTruncated bool     `json:"unrecognized_pages_truncated,omitempty"`
	TaskCoverage               string   `json:"task_coverage"`
}
