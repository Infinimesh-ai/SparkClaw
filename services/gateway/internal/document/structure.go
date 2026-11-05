package document

// StructureSchemaVersion identifies the canonical, source-bound node contract.
const StructureSchemaVersion = 1
const StructureParserVersion = "native_structure_v1"

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
	ID           string           `json:"id"`
	ParentID     string           `json:"parent_id,omitempty"`
	Order        int              `json:"order"`
	Kind         string           `json:"kind"`
	Title        string           `json:"title,omitempty"`
	Locator      StructureLocator `json:"locator"`
	Text         string           `json:"text"`
	Values       []map[string]any `json:"values,omitempty"`
	Dependencies []string         `json:"dependencies,omitempty"`
	Provenance   string           `json:"provenance"`
	Availability NodeAvailability `json:"availability"`
	Reason       string           `json:"reason,omitempty"`
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
