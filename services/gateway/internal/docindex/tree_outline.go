package docindex

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

const maxOutlineDepth = 8

type OutlineViewNode struct {
	ID               string                     `json:"id"`
	ParentID         string                     `json:"parent_id,omitempty"`
	Kind             string                     `json:"kind"`
	Title            string                     `json:"title,omitempty"`
	Level            int                        `json:"level,omitempty"`
	StructureSource  string                     `json:"structure_source,omitempty"`
	Span             *document.StructureSpan    `json:"span,omitempty"`
	Stats            *document.StructureStats   `json:"stats,omitempty"`
	Expandable       bool                       `json:"expandable"`
	ChildrenComplete bool                       `json:"children_complete"`
	Locator          *document.StructureLocator `json:"locator,omitempty"`
	Availability     document.NodeAvailability  `json:"availability,omitempty"`
	Lead             *document.StructurePreview `json:"lead,omitempty"`
	Preview          *document.StructurePreview `json:"preview,omitempty"`
}

type OutlinePathNode struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}
type StructureBudget struct {
	MaxBytes        int  `json:"max_bytes"`
	SharePercent    int  `json:"share_percent"`
	LeadsOmitted    bool `json:"leads_omitted"`
	PreviewsTrimmed bool `json:"previews_trimmed"`
	Grouped         bool `json:"grouped"`
	DepthLimited    bool `json:"depth_limited"`
}
type TreeOutlineResult struct {
	Node                OutlineViewNode            `json:"node"`
	Path                []OutlinePathNode          `json:"path"`
	Nodes               []OutlineViewNode          `json:"nodes"`
	NextCursor          string                     `json:"next_cursor,omitempty"`
	Coverage            document.StructureCoverage `json:"coverage"`
	SourceSHA256        string                     `json:"source_sha256"`
	StructureGeneration string                     `json:"structure_generation"`
	Budget              StructureBudget            `json:"structure_budget"`
}

func outlineView(node document.StructureNode, lead bool, previewRunes int) OutlineViewNode {
	view := OutlineViewNode{ID: node.ID, ParentID: node.ParentID, Kind: node.Kind, Title: node.Title, Level: node.Level, StructureSource: node.StructureSource, Span: node.Span, Stats: node.Stats, ChildrenComplete: true}
	if node.IsContainer() {
		view.Expandable = node.Stats.Leaves > 0 || node.Stats.ChildContainers > 0
		if lead {
			view.Lead = node.Lead
		}
	} else {
		view.Locator = &node.Locator
		view.Availability = node.Availability
		if previewRunes > 0 && node.Text != "" {
			view.Preview = &document.StructurePreview{Text: trimRunes(node.Text, previewRunes), IsPreview: true}
		}
	}
	return view
}

func (index *Index) treeChildren() map[string][]int {
	return index.treeNavigation().children
}

// Windows are source-bound views of siblings, not canonical parents. This
// reconciles bounded root discovery with the invariant forbidding physical
// parents above native sections. The same opaque ID selects its leaf span.
func (index *Index) makeOutlineWindow(parent, start, end int, children map[string][]int) document.StructureNode {
	ids := children[index.Nodes[parent].ID][start:end]
	span := *index.Nodes[ids[0]].Span
	stats := document.StructureStats{ChildContainers: len(ids)}
	for _, i := range ids {
		s := index.Nodes[i].Stats
		if s.Leaves > 0 {
			if stats.Leaves == 0 {
				span = *index.Nodes[i].Span
			} else {
				span.LastBlock = index.Nodes[i].Span.LastBlock
				span.OrdinalEnd = index.Nodes[i].Span.OrdinalEnd
				span.PageEnd = index.Nodes[i].Span.PageEnd
			}
		}
		stats.Leaves += s.Leaves
		stats.TextBytes += s.TextBytes
		stats.EstimatedTokens += s.EstimatedTokens
		stats.Tables += s.Tables
		stats.Unavailable += s.Unavailable
		stats.Unrecognized += s.Unrecognized
	}
	key := fmt.Sprintf("w.%d.%d.%d", parent, start, end)
	id := key + "." + structureHash([]any{index.Manifest.SourceSHA256, document.StructureParserVersion, key})[:24]
	return document.StructureNode{ID: id, ParentID: index.Nodes[parent].ID, Kind: "group", Title: physicalTitle(span), Level: index.Nodes[parent].Level + 1, StructureSource: "physical", Span: &span, Stats: &stats}
}
func (index *Index) outlineWindow(id string) (document.StructureNode, []int, error) {
	return index.resolveOutlineWindow(id, index.treeChildren())
}
func (index *Index) resolveOutlineWindow(id string, children map[string][]int) (document.StructureNode, []int, error) {
	parts := strings.Split(id, ".")
	if len(parts) != 5 || parts[0] != "w" {
		return document.StructureNode{}, nil, errors.New("invalid outline window")
	}
	p, e1 := strconv.Atoi(parts[1])
	start, e2 := strconv.Atoi(parts[2])
	end, e3 := strconv.Atoi(parts[3])
	if e1 != nil || e2 != nil || e3 != nil || p < 0 || p >= len(index.Nodes) || start < 0 || end <= start {
		return document.StructureNode{}, nil, errors.New("invalid outline window bounds")
	}
	ids := children[index.Nodes[p].ID]
	if end > len(ids) {
		return document.StructureNode{}, nil, errors.New("foreign outline window")
	}
	for _, i := range ids[start:end] {
		if !index.Nodes[i].IsContainer() {
			return document.StructureNode{}, nil, errors.New("window requires containers")
		}
	}
	node := index.makeOutlineWindow(p, start, end, children)
	if node.ID != id {
		return document.StructureNode{}, nil, errors.New("stale or foreign outline window")
	}
	return node, ids[start:end], nil
}

func (index *Index) OutlineTree(request OutlineRequest) (TreeOutlineResult, error) {
	if !index.isTree() {
		return TreeOutlineResult{}, errors.New("layered outline requires structure version 2")
	}
	if err := index.ValidateVersion(request.SourceSHA256, request.StructureGeneration); err != nil {
		return TreeOutlineResult{}, err
	}
	children, byID := index.treeChildren(), index.nodeMap()
	id := request.NodeID
	if id == "" {
		id = index.Nodes[0].ID
	}
	var node document.StructureNode
	var ids []int
	if i, ok := byID[id]; ok {
		node = index.Nodes[i]
		ids = children[id]
	} else {
		var err error
		node, ids, err = index.resolveOutlineWindow(id, children)
		if err != nil {
			return TreeOutlineResult{}, err
		}
	}
	if !node.IsContainer() {
		return TreeOutlineResult{}, errors.New("node_id must name a container")
	}
	depth := request.Depth
	if depth <= 0 {
		depth = maxOutlineDepth
	}
	depth = min(depth, maxOutlineDepth)
	cursor, err := index.cursor(request.Cursor, "tree-outline:"+id)
	if err != nil {
		return TreeOutlineResult{}, err
	}
	if cursor.Position > len(ids) || cursor.Offset != 0 {
		return TreeOutlineResult{}, errors.New("invalid tree outline cursor position")
	}
	result := TreeOutlineResult{Node: outlineView(node, false, 0), Path: []OutlinePathNode{}, Nodes: []OutlineViewNode{}, SourceSHA256: index.Manifest.SourceSHA256, StructureGeneration: index.Manifest.StructureGeneration, Budget: StructureBudget{MaxBytes: readBudget(request.MaxBytes), SharePercent: request.SharePercent}, Coverage: document.StructureCoverage{Scope: "structure_discovery", Status: "complete", DenominatorKnown: true, Required: len(ids), Covered: len(ids), TaskCoverage: "not_assessed"}}
	for parent := node.ParentID; parent != ""; {
		p := index.Nodes[byID[parent]]
		result.Path = append([]OutlinePathNode{{ID: p.ID, Title: p.Title, Kind: p.Kind}}, result.Path...)
		parent = p.ParentID
	}
	if jsonBytes(result) > result.Budget.MaxBytes {
		return TreeOutlineResult{}, errors.New("structure budget is too small for the container and ancestor path")
	}
	if len(ids) == 0 {
		return result, nil
	}
	if !index.Nodes[ids[0]].IsContainer() {
		return index.outlineLeaves(result, ids, cursor, request.Limit)
	}
	if request.Cursor != "" {
		return TreeOutlineResult{}, errors.New("container levels are complete; only leaf lists have continuation cursors")
	}
	level := ids
	for d := 0; d < depth && len(level) > 0; d++ {
		var views []OutlineViewNode
		candidate, fits := result, false
		// Uniform preview reduction applies to auto-deepened levels too. Never
		// hide a later leaf simply because earlier previews consumed the budget.
		if len(level) <= MaxOutlineNodes {
			for _, preview := range []int{leadRunes, 60, 30, 0} {
				views = make([]OutlineViewNode, 0, len(level))
				for _, i := range level {
					views = append(views, outlineView(index.Nodes[i], false, preview))
				}
				candidate.Nodes = append(append([]OutlineViewNode(nil), result.Nodes...), views...)
				if jsonBytes(candidate) <= result.Budget.MaxBytes {
					fits = true
					if preview < leadRunes {
						result.Budget.PreviewsTrimmed = true
					}
					break
				}
			}
		}
		if !fits {
			result.Budget.DepthLimited = true
			if d == 0 {
				return index.groupOutlineLevel(result, node, ids, children, byID)
			}
			break
		}
		// Either every lead at this level fits, or none is included.
		withLeads := append([]OutlineViewNode(nil), views...)
		for i, ordinal := range level {
			if index.Nodes[ordinal].IsContainer() {
				withLeads[i].Lead = index.Nodes[ordinal].Lead
			}
		}
		candidate = result
		candidate.Nodes = append(append([]OutlineViewNode(nil), result.Nodes...), withLeads...)
		if jsonBytes(candidate) <= result.Budget.MaxBytes {
			result = candidate
		} else {
			result.Nodes = append(result.Nodes, views...)
			result.Budget.LeadsOmitted = true
		}
		next := []int{}
		for _, i := range level {
			next = append(next, children[index.Nodes[i].ID]...)
		}
		level = next
	}
	if len(level) > 0 {
		result.Budget.DepthLimited = true
	}
	return result, nil
}

func (index *Index) groupOutlineLevel(result TreeOutlineResult, node document.StructureNode, ids []int, children map[string][]int, byID map[string]int) (TreeOutlineResult, error) {
	parent, offset := byID[node.ID], 0
	if _, ok := byID[node.ID]; !ok {
		parent = byID[node.ParentID]
		all := children[node.ParentID]
		for all[offset] != ids[0] {
			offset++
		}
	}
	result.Budget.Grouped = true
	result.Budget.LeadsOmitted = true
	for count := min(len(ids), MaxOutlineNodes); count >= 1; count /= 2 {
		views := []OutlineViewNode{}
		for i := 0; i < count; i++ {
			start, end := offset+i*len(ids)/count, offset+(i+1)*len(ids)/count
			views = append(views, outlineView(index.makeOutlineWindow(parent, start, end, children), false, 0))
		}
		result.Nodes = views
		if jsonBytes(result) <= result.Budget.MaxBytes {
			if len(views) == 1 && views[0].ID == node.ID {
				return TreeOutlineResult{}, errors.New("structure budget cannot make progress inside this navigation window; increase max_bytes or the configured structure share")
			}
			return result, nil
		}
	}
	return TreeOutlineResult{}, errors.New("structure budget is too small for a whole-level navigation window")
}

func (index *Index) outlineLeaves(result TreeOutlineResult, ids []int, cursor traversalCursor, limit int) (TreeOutlineResult, error) {
	if limit <= 0 {
		limit = MaxOutlineNodes
	}
	limit = min(limit, MaxOutlineNodes)
	start := cursor.Position
	end := min(len(ids), start+limit)
	build := func(end, preview int) TreeOutlineResult {
		out := result
		out.Nodes = []OutlineViewNode{}
		for _, i := range ids[start:end] {
			out.Nodes = append(out.Nodes, outlineView(index.Nodes[i], false, preview))
		}
		out.Coverage.Covered = end
		out.Coverage.Missing = len(ids) - end
		if end < len(ids) {
			cursor.Position = end
			out.NextCursor = index.encodeCursor(cursor)
			out.Coverage.Status = "partial"
		}
		return out
	}
	for _, preview := range []int{leadRunes, 60, 30, 0} {
		candidate := build(end, preview)
		candidate.Budget.PreviewsTrimmed = preview < leadRunes
		if jsonBytes(candidate) <= result.Budget.MaxBytes {
			return candidate, nil
		}
	}
	result.Budget.PreviewsTrimmed = true
	// At zero preview, binary search the largest prefix. No UTF-8 truncation
	// or dropped leaf can silently masquerade as a complete child list.
	lo, hi := start+1, end
	for lo <= hi {
		mid := (lo + hi) / 2
		if jsonBytes(build(mid, 0)) <= result.Budget.MaxBytes {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if hi <= start {
		return TreeOutlineResult{}, errors.New("structure budget is too small for a leaf and continuation")
	}
	return build(hi, 0), nil
}
