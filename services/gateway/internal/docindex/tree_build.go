package docindex

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

const physicalGroupLeaves = 24
const physicalGroupBytes = 16 << 10
const leadRunes = 120
const maxNativeTreeDepth = 64

type treePart struct {
	node        document.StructureNode
	children    []*treePart
	ordinal     int
	nativeLevel int
	groupKey    string
}

// Native containers own canonical leaves. Physical groups partition only runs
// of leaves; a native section can never acquire a physical parent.
func buildStructureTree(source, format, enrichment string, units []Unit, info SourceInfo) ([]Unit, []document.StructureNode, document.StructureManifest, error) {
	units = prepareStructureUnits(format, units, info)
	if len(units) == 0 {
		return nil, nil, document.StructureManifest{}, fmt.Errorf("document has no canonical content units")
	}
	if len(info.Outline) > MaxSourceUnits || jsonBytes(info) > MaxExtractedBytes {
		return nil, nil, document.StructureManifest{}, fmt.Errorf("native outline exceeds structure admission")
	}
	for _, entry := range info.Outline {
		if entry.Level > maxNativeTreeDepth {
			return nil, nil, document.StructureManifest{}, fmt.Errorf("native outline depth exceeds structure admission")
		}
	}
	for _, unit := range units {
		if unit.HeadingLevel > maxNativeTreeDepth {
			return nil, nil, document.StructureManifest{}, fmt.Errorf("native heading depth exceeds structure admission")
		}
	}
	root := &treePart{node: document.StructureNode{Kind: "document", Title: "Document"}}
	stack := []*treePart{root}
	seen, blocks := map[string]int{}, map[string]string{}
	leaves := make([]*treePart, 0, len(units))
	bookmarks := append([]OutlineEntry(nil), info.Outline...)
	sort.SliceStable(bookmarks, func(i, j int) bool { return bookmarks[i].Page < bookmarks[j].Page })
	nextBookmark := 0
	addContainer := func(kind, title, source string, level int, locator document.StructureLocator, ordinal int) {
		for len(stack) > 1 && stack[len(stack)-1].nativeLevel >= level {
			stack = stack[:len(stack)-1]
		}
		part := &treePart{node: document.StructureNode{Kind: kind, Title: trimRunes(title, maxHeadingRunes), StructureSource: source, Locator: locator}, nativeLevel: level, ordinal: ordinal}
		parent := stack[len(stack)-1]
		parent.children = append(parent.children, part)
		stack = append(stack, part)
	}
	for i := range units {
		unit := &units[i]
		if format == "pdf" || format == "pptx" {
			for nextBookmark < len(bookmarks) && bookmarks[nextBookmark].Page <= unit.Page {
				entry := bookmarks[nextBookmark]
				nextBookmark++
				if entry.Page < 1 || entry.Page > info.Pages || strings.TrimSpace(entry.Title) == "" {
					continue
				}
				kind, source, block := "bookmark", "pdf_bookmark", fmt.Sprintf("document.bookmark[%d]", nextBookmark)
				if format == "pptx" {
					kind, source, block = "slide_section", "slide_section", fmt.Sprintf("presentation.section[%d]", nextBookmark)
				}
				addContainer(kind, entry.Title, source, max(1, entry.Level), document.StructureLocator{Page: entry.Page, Block: block}, i)
			}
		}
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
		unit.NodeID = "d" + structureHash([]any{source, document.StructureSchemaVersion, document.StructureParserVersion, key, seen[key]})[:24]
		blocks[unit.Block] = unit.NodeID
		locator := document.StructureLocator{Block: unit.Block, Page: unit.Page, ByteStart: unit.ByteStart, ByteEnd: unit.ByteEnd}
		if unit.HeadingLevel > 0 {
			containerKind, provenance := "section", unit.StructureSource
			switch kind {
			case "slide":
				containerKind, provenance = "slide", "slide"
			case "slide_section":
				containerKind, provenance = "slide_section", "slide_section"
			case "sheet":
				containerKind, provenance = "sheet", "sheet"
			}
			if provenance == "" {
				if format == "text" {
					provenance = "markdown_heading"
				} else {
					provenance = "native_style"
				}
			}
			title := unit.Title
			if title == "" {
				title = unit.Text
			}
			level := unit.HeadingLevel
			if format == "pptx" && nextBookmark > 0 {
				level++
			}
			addContainer(containerKind, title, provenance, level, locator, i)
			kind = "heading_label"
		}
		availability := unit.Availability
		if availability == "" {
			availability = document.NodeAvailable
		}
		provenance := unit.Provenance
		if provenance == "" {
			provenance = "native"
		}
		leaf := &treePart{ordinal: i, groupKey: unit.GroupKey, node: document.StructureNode{ID: unit.NodeID, Kind: kind, Locator: locator, Text: unit.Text, Values: unit.Values, Availability: availability, Provenance: provenance}}
		leaf.node.Reason = unit.Reason
		if kind == "heading_label" {
			leaf.node.Title = trimRunes(unit.Title, maxHeadingRunes)
		}
		parent := stack[len(stack)-1]
		parent.children = append(parent.children, leaf)
		leaves = append(leaves, leaf)
	}
	// Native labels remain dependencies of body reads, even after grouping. A
	// navigation preview is never substituted for that source text.
	var bindLabels func(*treePart, []string)
	bindLabels = func(part *treePart, labels []string) {
		if len(part.children) == 0 && part.node.ID != "" {
			part.node.Dependencies = append(part.node.Dependencies, labels...)
			return
		}
		for _, child := range part.children {
			if child.node.Kind == "heading_label" {
				bindLabels(child, labels)
				labels = append(append([]string(nil), labels...), child.node.ID)
			} else {
				bindLabels(child, labels)
			}
		}
	}
	bindLabels(root, nil)
	for i, unit := range units {
		for _, block := range unit.Dependencies {
			id := blocks[block]
			if id == "" {
				return nil, nil, document.StructureManifest{}, fmt.Errorf("missing document dependency %q", block)
			}
			if id != leaves[i].node.ID {
				leaves[i].node.Dependencies = append(leaves[i].node.Dependencies, id)
			}
		}
	}
	// Content before the first native top-level container has its own truthful
	// front-matter range. A wholly unstructured document uses physical windows.
	firstNative := 0
	for firstNative < len(root.children) && root.children[firstNative].node.ID != "" {
		firstNative++
	}
	if firstNative > 0 && firstNative < len(root.children) {
		front := &treePart{node: document.StructureNode{Kind: "front_matter", Title: "Front matter", StructureSource: "physical"}, children: append([]*treePart(nil), root.children[:firstNative]...)}
		root.children = append([]*treePart{front}, root.children[firstNative:]...)
	}
	groupTreeLeaves(root, format)
	nodes := []document.StructureNode{}
	finalizeTree(root, source, "", 0, &nodes)
	if err := validateStructureTree(nodes, units); err != nil {
		return nil, nil, document.StructureManifest{}, err
	}
	covered := 0
	missing := []string{}
	for _, leaf := range leaves {
		if leaf.node.Availability == document.NodeAvailable {
			covered++
		} else if len(missing) < 8 {
			missing = append(missing, leaf.node.ID)
		}
	}
	coverage := document.StructureCoverage{Scope: "source_acquisition", Status: "complete", DenominatorKnown: true, Required: len(leaves), Covered: covered, Missing: len(leaves) - covered, MissingNodeIDs: missing, MissingIDsTruncated: len(leaves)-covered > len(missing), UnrecognizedPageCount: len(info.UnindexedPages), UnrecognizedPages: append([]int{}, info.UnindexedPages[:min(32, len(info.UnindexedPages))]...), UnrecognizedPagesTruncated: len(info.UnindexedPages) > 32, TaskCoverage: "not_assessed"}
	if coverage.Missing > 0 {
		coverage.Status = "partial"
	}
	limitations := append([]string{}, info.Limitations...)
	limitations = append(limitations, "Native structure only; physical groups and extracted previews are navigation, not acquired task evidence. Layout and visual reading order are not fully verified.")
	if format == "pdf" {
		limitations = append(limitations, "Bookmarks locate whole pages; same-page bookmarks may have empty ranges because sub-page boundaries are unavailable.")
	}
	manifest := document.StructureManifest{SchemaVersion: document.StructureSchemaVersion, ParserVersion: document.StructureParserVersion, SourceSHA256: source, StructureGeneration: structureHash([]any{document.StructureParserVersion, enrichment, nodes}), EnrichmentVersion: enrichment, Format: format, Nodes: len(nodes), Capabilities: []string{"outline", "layered_outline", "search", "node_read", "traverse"}, Limitations: limitations, Coverage: coverage}
	return units, nodes, manifest, nil
}

func groupTreeLeaves(parent *treePart, format string) {
	result := []*treePart{}
	hasContainers := false
	for _, child := range parent.children {
		if child.node.ID == "" {
			hasContainers = true
			break
		}
	}
	for start := 0; start < len(parent.children); {
		if parent.children[start].node.ID == "" {
			child := parent.children[start]
			groupTreeLeaves(child, format)
			result = append(result, child)
			start++
			continue
		}
		end, bytes := start, 0
		for end < len(parent.children) && parent.children[end].node.ID != "" {
			bytes += len(parent.children[end].node.Text)
			end++
		}
		if !hasContainers && end-start <= physicalGroupLeaves && (format == "pdf" || bytes <= physicalGroupBytes) && parent.node.Kind != "document" {
			result = append(result, parent.children[start:end]...)
			start = end
			continue
		}
		for at := start; at < end; {
			last, size := at, 0
			for last < end {
				leaf := parent.children[last]
				// An adapter's atomic key keeps a table or worksheet row intact.
				same := last > at && leaf.groupKey != "" && leaf.groupKey == parent.children[last-1].groupKey
				if last > at && !same && (last-at >= physicalGroupLeaves || format != "pdf" && size+len(leaf.node.Text) > physicalGroupBytes) {
					break
				}
				size += len(leaf.node.Text)
				last++
			}
			kind := "group"
			if format == "pdf" {
				kind = "page_group"
			}
			group := &treePart{node: document.StructureNode{Kind: kind, StructureSource: "physical"}, children: append([]*treePart(nil), parent.children[at:last]...)}
			result = append(result, group)
			at = last
		}
		start = end
	}
	parent.children = result
}

func finalizeTree(part *treePart, source, parent string, depth int, nodes *[]document.StructureNode) {
	if part.node.ID != "" {
		part.node.ParentID = parent
		part.node.Order = len(*nodes)
		*nodes = append(*nodes, part.node)
		return
	}
	first := part
	for len(first.children) > 0 {
		first = first.children[0]
	}
	identity := first.node.ID
	if identity == "" {
		identity = part.node.Locator.Block
	}
	part.node.ID = "d" + structureHash([]any{source, document.StructureSchemaVersion, part.node.Kind, identity, depth})[:24]
	part.node.ParentID, parent = parent, part.node.ID
	part.node.Level = depth
	part.node.Order = len(*nodes)
	part.node.Availability = document.NodeAvailable
	part.node.Provenance = "structure"
	complete := true
	part.node.ChildrenComplete = &complete
	span := document.StructureSpan{OrdinalStart: part.ordinal, OrdinalEnd: part.ordinal}
	stats := document.StructureStats{}
	position := len(*nodes)
	*nodes = append(*nodes, part.node)
	for _, child := range part.children {
		childPosition := len(*nodes)
		finalizeTree(child, source, parent, depth+1, nodes)
		node := (*nodes)[childPosition]
		childSpan := document.StructureSpan{FirstBlock: node.Locator.Block, LastBlock: node.Locator.Block, OrdinalStart: child.ordinal, OrdinalEnd: child.ordinal + 1, PageStart: node.Locator.Page, PageEnd: node.Locator.Page}
		childStats := document.StructureStats{Leaves: 1, TextBytes: len(node.Text), EstimatedTokens: (len(node.Text) + 2) / 3}
		if node.Availability != document.NodeAvailable {
			childStats.Unavailable = 1
		}
		if node.Availability == document.NodeUnrecognized {
			childStats.Unrecognized = 1
		}
		if node.Kind == "table" || node.Kind == "table_row" && strings.HasSuffix(node.Locator.Block, ".row[1]") {
			childStats.Tables = 1
		}
		if node.IsContainer() {
			childSpan = *node.Span
			childStats = *node.Stats
			stats.ChildContainers++
		}
		if part.node.Lead == nil {
			if node.IsContainer() {
				part.node.Lead = node.Lead
			} else if node.Kind != "heading_label" && strings.TrimSpace(node.Text) != "" {
				part.node.Lead = &document.StructurePreview{Text: firstSentence(node.Text), IsPreview: true}
			}
		}
		if childStats.Leaves > 0 {
			if stats.Leaves == 0 {
				span = childSpan
			} else {
				span.LastBlock = childSpan.LastBlock
				span.OrdinalEnd = childSpan.OrdinalEnd
				span.PageEnd = max(span.PageEnd, childSpan.PageEnd)
			}
		}
		stats.Leaves += childStats.Leaves
		stats.TextBytes += childStats.TextBytes
		stats.EstimatedTokens += childStats.EstimatedTokens
		stats.Tables += childStats.Tables
		stats.Unavailable += childStats.Unavailable
		stats.Unrecognized += childStats.Unrecognized
	}
	part.node.Span, part.node.Stats = &span, &stats
	if part.node.Locator.Block == "" {
		part.node.Locator = document.StructureLocator{Block: span.FirstBlock, Page: span.PageStart}
	}
	if part.node.Title == "" {
		part.node.Title = physicalTitle(span)
	}
	(*nodes)[position] = part.node
}

func physicalTitle(span document.StructureSpan) string {
	if span.PageStart > 0 {
		return fmt.Sprintf("p.%d–%d", span.PageStart, span.PageEnd)
	}
	return trimRunes(span.FirstBlock+"–"+span.LastBlock, maxHeadingRunes)
}
func firstSentence(text string) string {
	text = trimRunes(strings.TrimSpace(text), leadRunes)
	for i, r := range text {
		if strings.ContainsRune("。！？.!?\n", r) {
			return text[:i+len(string(r))]
		}
	}
	return text
}
