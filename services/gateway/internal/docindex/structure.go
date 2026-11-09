package docindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func structureHash(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func buildStructure(sourceSHA, format, enrichment string, units []Unit, info SourceInfo) ([]Unit, []document.StructureNode, document.StructureManifest) {
	units = prepareStructureUnits(format, units, info)
	nodes := make([]document.StructureNode, 0, len(units))
	seen := map[string]int{}
	blocks := map[string]string{}
	headings := []string{}
	for i := range units {
		unit := &units[i]
		kind := unit.Kind
		if kind == "" {
			if unit.HeadingLevel > 0 {
				kind = "heading"
			} else {
				kind = "paragraph"
			}
		}
		key := unit.Block + "\x00" + kind
		seen[key]++
		unit.NodeID = "d" + structureHash([]any{sourceSHA, document.LegacyStructureSchemaVersion, document.LegacyStructureParserVersion, key, seen[key]})[:24]
		blocks[unit.Block] = unit.NodeID
		parent := ""
		if unit.HeadingLevel > 0 {
			headings = headings[:min(len(headings), unit.HeadingLevel-1)]
		}
		if len(headings) > 0 {
			parent = headings[len(headings)-1]
		}
		if unit.HeadingLevel > 0 {
			headings = append(headings, unit.NodeID)
		}
		availability := unit.Availability
		if availability == "" {
			availability = document.NodeAvailable
		}
		provenance := unit.Provenance
		if provenance == "" {
			provenance = "native"
		}
		title := unit.Title
		if title == "" && unit.HeadingLevel > 0 {
			title = unit.Text
		}
		nodes = append(nodes, document.StructureNode{ID: unit.NodeID, ParentID: parent, Order: i, Kind: kind, Title: title, Locator: document.StructureLocator{Block: unit.Block, Page: unit.Page, ByteStart: unit.ByteStart, ByteEnd: unit.ByteEnd}, Text: unit.Text, Values: unit.Values, Provenance: provenance, Availability: availability, Reason: unit.Reason})
	}
	for i, unit := range units {
		if nodes[i].ParentID != "" {
			nodes[i].Dependencies = append(nodes[i].Dependencies, nodes[i].ParentID)
		}
		for _, block := range unit.Dependencies {
			if id := blocks[block]; id != "" && id != nodes[i].ID {
				nodes[i].Dependencies = append(nodes[i].Dependencies, id)
			}
		}
	}
	covered := 0
	missing := []string{}
	for _, node := range nodes {
		if node.Availability == document.NodeAvailable {
			covered++
		} else {
			if len(missing) < 8 {
				missing = append(missing, node.ID)
			}
		}
	}
	coverage := document.StructureCoverage{Scope: "source_acquisition", Status: "complete", DenominatorKnown: true, Required: len(nodes), Covered: covered, Missing: len(nodes) - covered, MissingIDsTruncated: len(nodes)-covered > len(missing), MissingNodeIDs: missing, UnrecognizedPageCount: len(info.UnindexedPages), UnrecognizedPages: append([]int{}, info.UnindexedPages[:min(len(info.UnindexedPages), 32)]...), UnrecognizedPagesTruncated: len(info.UnindexedPages) > 32, TaskCoverage: "not_assessed"}
	if len(nodes) > covered {
		coverage.Status = "partial"
	}
	limitations := append([]string{}, info.Limitations...)
	limitations = append(limitations, "Native text and typed values are parser observations; layout, visual objects and reading order are not fully verified.")
	generation := structureHash([]any{document.LegacyStructureParserVersion, enrichment, nodes})
	manifest := document.StructureManifest{SchemaVersion: document.LegacyStructureSchemaVersion, ParserVersion: document.LegacyStructureParserVersion, SourceSHA256: sourceSHA, StructureGeneration: generation, EnrichmentVersion: enrichment, Format: format, Nodes: len(nodes), Capabilities: []string{"outline", "search", "node_read", "traverse"}, Limitations: limitations, Coverage: coverage}
	return units, nodes, manifest
}

func (index *Index) ValidateVersion(sourceSHA, generation string) error {
	if sourceSHA != "" && sourceSHA != index.Manifest.SourceSHA256 {
		return fmt.Errorf("document source version changed; obtain a new outline")
	}
	if generation != "" && generation != index.Manifest.StructureGeneration {
		return fmt.Errorf("document structure generation changed; restart the selected traversal")
	}
	return nil
}

func (index *Index) nodeMap() map[string]int {
	if index.tree != nil {
		return index.tree.byID
	}
	nodes := make(map[string]int, len(index.Nodes))
	for i, node := range index.Nodes {
		nodes[node.ID] = i
	}
	return nodes
}

// SelectedPages returns the native pages of the validated initial scope. It
// never expands a targeted read to neighboring slides or a continuation batch.
func (index *Index) SelectedPages(request RangeRequest) ([]int, error) {
	if request.Cursor != "" {
		return nil, fmt.Errorf("page selection requires an initial range")
	}
	if err := index.ValidateVersion(request.SourceSHA256, request.StructureGeneration); err != nil {
		return nil, err
	}
	selectors := 0
	for _, selected := range []bool{len(request.NodeIDs) > 0, request.All, len(request.ChunkIDs) > 0, request.PageStart > 0, request.Section != ""} {
		if selected {
			selectors++
		}
	}
	if selectors != 1 {
		return nil, fmt.Errorf("select exactly one source range before selecting pages")
	}
	ordinals, err := index.selectedNodes(request)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	pages := []int{}
	for _, ordinal := range ordinals {
		page := index.Nodes[ordinal].Locator.Page
		if page > 0 && !seen[page] {
			seen[page] = true
			pages = append(pages, page)
		}
	}
	sort.Ints(pages)
	return pages, nil
}

func (index *Index) selectedNodes(request RangeRequest) ([]int, error) {
	if index.isTree() {
		return index.treeSelectedNodes(request)
	}
	selected := map[int]bool{}
	byID := index.nodeMap()
	switch {
	case request.All:
		for i := range index.Nodes {
			selected[i] = true
		}
	case len(request.NodeIDs) > 0:
		if len(request.NodeIDs) > MaxRangeChunks {
			return nil, fmt.Errorf("at most %d node_ids can be read at once", MaxRangeChunks)
		}
		for _, id := range request.NodeIDs {
			i, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("node_id %q is not in this source version", id)
			}
			selected[i] = true
		}
		// A heading locates a section, not just its label. Keep the expansion
		// explicit and cursor-bound so legacy exact-node callers are unchanged.
		if request.IncludeDescendants {
			children := map[string][]int{}
			for i, node := range index.Nodes {
				children[node.ParentID] = append(children[node.ParentID], i)
			}
			pending := append([]string(nil), request.NodeIDs...)
			for len(pending) > 0 {
				id := pending[0]
				pending = pending[1:]
				for _, i := range children[id] {
					if !selected[i] {
						selected[i] = true
						pending = append(pending, index.Nodes[i].ID)
					}
				}
			}
		}
	case request.PageStart > 0:
		for i, node := range index.Nodes {
			if node.Locator.Page >= request.PageStart && node.Locator.Page <= max(request.PageStart, request.PageEnd) {
				selected[i] = true
			}
		}
	case request.Section != "":
		section, ok := index.outlineNode(request.Section)
		if !ok {
			return nil, fmt.Errorf("section %q is not in this document", request.Section)
		}
		for ordinal := section.ChunkStart; ordinal < section.ChunkEnd; ordinal++ {
			if i, ok := byID[index.Chunks[ordinal].NodeID]; ok {
				selected[i] = true
			}
		}
		if section.Page > 0 {
			for i, node := range index.Nodes {
				if node.Locator.Page >= section.Page && node.Locator.Page <= max(section.Page, section.PageEnd) {
					selected[i] = true
				}
			}
		}
	default:
		ordinals, err := index.rangeOrdinals(request)
		if err != nil {
			return nil, err
		}
		for _, ordinal := range ordinals {
			if i, ok := byID[index.Chunks[ordinal].NodeID]; ok {
				selected[i] = true
			}
		}
		// A section's pages include unavailable physical pages with no chunks.
		if request.Section != "" {
			if section, ok := index.outlineNode(request.Section); ok && section.Page > 0 {
				for i, node := range index.Nodes {
					if node.Locator.Page >= section.Page && node.Locator.Page <= max(section.Page, section.PageEnd) {
						selected[i] = true
					}
				}
			}
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("the selected scope has no structure nodes")
	}
	ordinals := make([]int, 0, len(selected))
	for i := range selected {
		ordinals = append(ordinals, i)
	}
	sort.Ints(ordinals)
	return ordinals, nil
}

func prepareStructureUnits(format string, units []Unit, info SourceInfo) []Unit {
	units = append([]Unit(nil), units...)
	// Physical PDF pages are known even when native extraction and OCR fail.
	if format == "pdf" {
		present := map[int]bool{}
		for _, unit := range units {
			present[unit.Page] = true
		}
		for page := 1; page <= info.Pages; page++ {
			if !present[page] {
				units = append(units, Unit{Page: page, Kind: "page", Block: fmt.Sprintf("document.page[%d]", page), Availability: document.NodeUnrecognized, Reason: "no_native_text"})
			}
		}
		sort.SliceStable(units, func(i, j int) bool { return units[i].Page < units[j].Page })
	}
	for i := range units {
		unit := &units[i]
		if len(unit.Values) > 0 {
			raw, _ := json.Marshal(unit.Values)
			unit.SearchText += " " + string(raw)
		}
		if unit.Block == "" {
			unit.Block = fmt.Sprintf("unit[%d]", i+1)
		}
	}
	return units
}
