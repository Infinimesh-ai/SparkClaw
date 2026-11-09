package docindex

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

type treeNavigation struct {
	byID     map[string]int
	children map[string][]int
	leaves   []int
	// viewPrefix[i] is the model-view byte size of canonical leaves before
	// ordinal i. Estimates use the same rendering that delivers content.
	viewPrefix []int
}

// Built/loaded indexes are immutable and shared by readers. Compute navigation
// indexes once before publication, so local expansion is proportional to the
// requested level rather than to the entire source.
func (index *Index) initTreeNavigation() {
	if index.isTree() {
		index.tree = index.treeNavigation()
	}
}
func (index *Index) treeNavigation() *treeNavigation {
	if index.tree != nil {
		return index.tree
	}
	navigation := &treeNavigation{byID: map[string]int{}, children: map[string][]int{}, leaves: []int{}}
	for i, node := range index.Nodes {
		navigation.byID[node.ID] = i
		if node.ParentID != "" {
			navigation.children[node.ParentID] = append(navigation.children[node.ParentID], i)
		}
		if !node.IsContainer() {
			navigation.leaves = append(navigation.leaves, i)
		}
	}
	navigation.viewPrefix = make([]int, len(navigation.leaves)+1)
	depths := map[string]int{}
	var headingDepth func(position int, seen map[int]bool) int
	// Same rule as document.ModelView: one more than the deepest heading
	// dependency. A full subtree read carries those headings as dependencies.
	headingDepth = func(position int, seen map[int]bool) int {
		node := index.Nodes[position]
		if depth, ok := depths[node.ID]; ok {
			return depth
		}
		if seen[position] {
			return 1
		}
		seen[position] = true
		depth := 1
		for _, id := range node.Dependencies {
			if dependency, ok := navigation.byID[id]; ok && index.Nodes[dependency].Kind == "heading_label" {
				depth = max(depth, headingDepth(dependency, seen)+1)
			}
		}
		depths[node.ID] = depth
		return depth
	}
	for ordinal, position := range navigation.leaves {
		node := index.Nodes[position]
		navigation.viewPrefix[ordinal+1] = navigation.viewPrefix[ordinal] + document.ModelViewNodeBytes(node, headingDepth(position, map[int]bool{}))
	}
	return navigation
}

// ModelViewBytes is the size of a container's canonical leaves as rendered by
// document.ModelView: what a full read of that subtree delivers to a model,
// excluding ancestor headings carried as context.
func (index *Index) ModelViewBytes(node document.StructureNode) (int, bool) {
	if node.Span == nil {
		return 0, false
	}
	navigation := index.treeNavigation()
	start, end := node.Span.OrdinalStart, node.Span.OrdinalEnd
	if start < 0 || end < start || end >= len(navigation.viewPrefix) {
		return 0, false
	}
	return navigation.viewPrefix[end] - navigation.viewPrefix[start], true
}

// Reject malformed trees before publishing a generation. The preorder and
// half-open leaf spans make every acquisition denominator independent of the
// number of navigation containers.
func validateStructureTree(nodes []document.StructureNode, units []Unit) error {
	if len(nodes) == 0 || nodes[0].Kind != "document" || nodes[0].ParentID != "" {
		return fmt.Errorf("structure tree requires one root")
	}
	byID := map[string]int{}
	children := map[string][]int{}
	leaves := []int{}
	for i, node := range nodes {
		if node.ID == "" {
			return fmt.Errorf("empty structure ID")
		}
		if _, ok := byID[node.ID]; ok {
			return fmt.Errorf("duplicate structure ID")
		}
		if i > 0 {
			p, ok := byID[node.ParentID]
			if !ok || !nodes[p].IsContainer() {
				return fmt.Errorf("invalid structure parent")
			}
			if node.IsContainer() && node.StructureSource != "physical" && nodes[p].StructureSource == "physical" {
				return fmt.Errorf("physical container cannot parent native structure")
			}
			children[node.ParentID] = append(children[node.ParentID], i)
		}
		byID[node.ID] = i
		if !node.IsContainer() {
			leaves = append(leaves, i)
		}
	}
	if len(leaves) != len(units) {
		return fmt.Errorf("tree leaf count differs from source")
	}
	for i, ordinal := range leaves {
		if nodes[ordinal].ID != units[i].NodeID {
			return fmt.Errorf("tree leaves are not in source order")
		}
	}
	walk := []int{}
	var visit func(int) error
	visit = func(i int) error {
		walk = append(walk, i)
		node := nodes[i]
		if !node.IsContainer() {
			return nil
		}
		if node.Stats == nil || node.ChildrenComplete == nil || !*node.ChildrenComplete {
			return fmt.Errorf("incomplete tree container")
		}
		start, end := -1, -1
		count := 0
		for _, child := range children[node.ID] {
			if err := visit(child); err != nil {
				return err
			}
			c := nodes[child]
			a, b := sort.SearchInts(leaves, child), sort.SearchInts(leaves, child)+1
			if c.IsContainer() {
				a, b = c.Span.OrdinalStart, c.Span.OrdinalEnd
			}
			if a == b {
				continue
			}
			if start < 0 {
				start = a
			} else if end != a {
				return fmt.Errorf("container has a leaf gap or overlap")
			}
			end = b
			count += b - a
		}
		if count == 0 {
			if node.Span.OrdinalStart != node.Span.OrdinalEnd || node.Stats.Leaves != 0 {
				return fmt.Errorf("invalid empty span")
			}
			return nil
		}
		first, last := nodes[leaves[start]], nodes[leaves[end-1]]
		expected := document.StructureSpan{FirstBlock: first.Locator.Block, LastBlock: last.Locator.Block, OrdinalStart: start, OrdinalEnd: end, PageStart: first.Locator.Page, PageEnd: last.Locator.Page}
		if !reflect.DeepEqual(*node.Span, expected) || node.Stats.Leaves != count {
			return fmt.Errorf("container span differs from children: %s", node.ID)
		}
		return nil
	}
	if err := visit(0); err != nil {
		return err
	}
	for i, ordinal := range walk {
		if i != ordinal {
			return fmt.Errorf("tree is not in preorder")
		}
	}
	return nil
}

func projectTreeOutline(nodes []document.StructureNode, chunks []Chunk) []OutlineNode {
	paths := map[string][]string{}
	for _, node := range nodes {
		path := paths[node.ParentID]
		if node.IsContainer() && node.Kind != "document" && node.StructureSource != "physical" {
			path = append(append([]string(nil), path...), node.Title)
		}
		paths[node.ID] = path
	}
	chunkStarts := map[string]int{}
	for i, chunk := range chunks {
		chunks[i].Anchor.HeadingPath = append([]string(nil), paths[chunk.NodeID]...)
		if _, ok := chunkStarts[chunk.NodeID]; !ok {
			chunkStarts[chunk.NodeID] = i
		}
	}
	leaves := []document.StructureNode{}
	for _, node := range nodes {
		if !node.IsContainer() {
			leaves = append(leaves, node)
		}
	}
	boundaries := make([]int, len(leaves)+1)
	next := len(chunks)
	boundaries[len(leaves)] = next
	for i := len(leaves) - 1; i >= 0; i-- {
		if at, ok := chunkStarts[leaves[i].ID]; ok {
			next = at
		}
		boundaries[i] = next
	}
	outline := []OutlineNode{}
	for _, node := range nodes {
		if node.IsContainer() && node.Kind != "document" {
			span := node.Span
			outline = append(outline, OutlineNode{ID: node.ID, Title: node.Title, Level: node.Level, Page: span.PageStart, PageEnd: span.PageEnd, ChunkStart: boundaries[span.OrdinalStart], ChunkEnd: boundaries[span.OrdinalEnd]})
		}
	}
	return outline
}

func (index *Index) isTree() bool {
	return index.Structure.SchemaVersion == document.StructureSchemaVersion
}

// SummarySpan resolves summary units (including merged consecutive units) back
// to the canonical leaf interval. Chunk boundaries cannot locate containers or
// unavailable leaves that have no searchable text.
func (index *Index) SummarySpan(unit SummaryUnit) (document.StructureSpan, bool) {
	if !index.isTree() {
		return document.StructureSpan{}, false
	}
	ids := strings.Split(unit.ID, "..")
	byID := index.nodeMap()
	first, ok := byID[ids[0]]
	if !ok || !index.Nodes[first].IsContainer() {
		return document.StructureSpan{}, false
	}
	last, ok := byID[ids[len(ids)-1]]
	if !ok || !index.Nodes[last].IsContainer() {
		return document.StructureSpan{}, false
	}
	span := *index.Nodes[first].Span
	span.OrdinalEnd = index.Nodes[last].Span.OrdinalEnd
	span.LastBlock = index.Nodes[last].Span.LastBlock
	span.PageEnd = index.Nodes[last].Span.PageEnd
	return span, true
}

// treeSelectedNodes returns only canonical leaves. Containers and previews
// never enter read coverage; required native labels/headers remain dependencies.
func (index *Index) treeSelectedNodes(request RangeRequest) ([]int, error) {
	selected := map[int]bool{}
	byID := index.nodeMap()
	if request.Section != "" {
		request.NodeIDs = []string{request.Section}
		request.IncludeDescendants = true
	}
	if len(request.NodeIDs) > MaxRangeChunks {
		return nil, fmt.Errorf("at most %d node_ids can be read at once", MaxRangeChunks)
	}
	selectSpan := func(span document.StructureSpan) {
		for _, i := range index.treeNavigation().leaves[span.OrdinalStart:span.OrdinalEnd] {
			selected[i] = true
		}
	}
	switch {
	case len(request.NodeIDs) > 0:
		for _, id := range request.NodeIDs {
			if i, ok := byID[id]; ok {
				node := index.Nodes[i]
				if !node.IsContainer() {
					selected[i] = true
				} else if request.IncludeDescendants {
					selectSpan(*node.Span)
				} else {
					return nil, fmt.Errorf("container reads require include_descendants=true")
				}
			} else if window, _, err := index.outlineWindow(id); err == nil && request.IncludeDescendants {
				selectSpan(*window.Span)
			} else {
				return nil, fmt.Errorf("node_id %q is not in this source version", id)
			}
		}
	case request.All:
		for i, node := range index.Nodes {
			if !node.IsContainer() {
				selected[i] = true
			}
		}
	case request.PageStart > 0:
		for i, node := range index.Nodes {
			if !node.IsContainer() && node.Locator.Page >= request.PageStart && node.Locator.Page <= max(request.PageStart, request.PageEnd) {
				selected[i] = true
			}
		}
	default:
		ordinals, err := index.rangeOrdinals(request)
		if err != nil {
			return nil, err
		}
		for _, i := range ordinals {
			selected[byID[index.Chunks[i].NodeID]] = true
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("the selected scope has no canonical leaves")
	}
	ordinals := make([]int, 0, len(selected))
	for i := range selected {
		ordinals = append(ordinals, i)
	}
	sort.Ints(ordinals)
	return ordinals, nil
}
