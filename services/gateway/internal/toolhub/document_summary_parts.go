package toolhub

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func scopedSummaryParts(index *docindex.Index, unit docindex.SummaryUnit, budget int) ([]string, []string) {
	positions := map[string]int{}
	for position, node := range index.Nodes {
		positions[node.ID] = position
	}
	start, end := 0, len(index.Nodes)
	if unit.ChunkStart > 0 && unit.ChunkStart < len(index.Chunks) {
		start = positions[index.Chunks[unit.ChunkStart].NodeID]
	}
	if unit.ChunkEnd < len(index.Chunks) {
		end = positions[index.Chunks[unit.ChunkEnd].NodeID]
	}
	if end <= start {
		end = min(start+1, len(index.Nodes))
	}
	nodes := index.Nodes[start:end]
	if span, ok := index.SummarySpan(unit); ok {
		nodes = nil
		ordinal := 0
		for _, node := range index.Nodes {
			if node.IsContainer() {
				continue
			}
			if ordinal >= span.OrdinalStart && ordinal < span.OrdinalEnd {
				nodes = append(nodes, node)
			}
			ordinal++
		}
	}
	structured := false
	for _, node := range nodes {
		if len(node.Values) > 0 || len(node.Dependencies) > 0 {
			structured = true
			break
		}
	}
	if !structured {
		return splitSummaryText(index.UnitText(unit), budget), nil
	}
	parts, missing := []string{}, []string{}
	group := []any{}
	flush := func() {
		if len(group) > 0 {
			raw, _ := json.Marshal(group)
			parts = append(parts, string(raw))
			group = nil
		}
	}
	for _, node := range nodes {
		if node.IsContainer() {
			continue
		}
		dependencies := []document.StructureNode{}
		seen := map[string]bool{node.ID: true}
		pending := append([]string(nil), node.Dependencies...)
		dependencyMissing := false
		for len(pending) > 0 {
			id := pending[0]
			pending = pending[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			if position, ok := positions[id]; ok {
				dependency := index.Nodes[position]
				dependencies = append(dependencies, dependency)
				pending = append(pending, dependency.Dependencies...)
			} else {
				dependencyMissing = true
			}
		}
		if dependencyMissing {
			missing = append(missing, fmt.Sprintf("%s:node:%s:required_dependency_missing", unit.ID, node.ID))
			continue
		}
		bundles, fits := scopedSummaryNodeBundles(node, dependencies, budget)
		if !fits {
			missing = append(missing, fmt.Sprintf("%s:node:%s:required_context_exceeds_budget", unit.ID, node.ID))
			continue
		}
		for _, bundle := range bundles {
			candidate := append(append([]any(nil), group...), bundle)
			raw, _ := json.Marshal(candidate)
			if len(raw) > budget {
				flush()
			}
			group = append(group, bundle)
		}
	}
	flush()
	return parts, missing
}

// Text can be continued across parts, but each part retains the full required
// dependency closure. Typed values stay atomic with their cached-state flags.
func scopedSummaryNodeBundles(node document.StructureNode, dependencies []document.StructureNode, budget int) ([]any, bool) {
	bundle := func(start, end int) map[string]any {
		fragment := node
		fragment.Text = node.Text[start:end]
		return map[string]any{"node": fragment, "dependencies": dependencies, "text_byte_start": start, "text_byte_end": end, "whole_node": start == 0 && end == len(node.Text)}
	}
	fits := func(value any) bool { raw, err := json.Marshal([]any{value}); return err == nil && len(raw) <= budget }
	full := bundle(0, len(node.Text))
	if fits(full) {
		return []any{full}, true
	}
	if len(node.Values) > 0 || node.Text == "" {
		return nil, false
	}
	parts := []any{}
	for start := 0; start < len(node.Text); {
		low, high := start+1, min(len(node.Text), start+max(0, budget))
		best := start
		for low <= high {
			mid := low + (high-low)/2
			if fits(bundle(start, mid)) {
				best = mid
				low = mid + 1
			} else {
				high = mid - 1
			}
		}
		for best > start && best < len(node.Text) && !utf8.RuneStart(node.Text[best]) {
			best--
		}
		if best == start {
			return nil, false
		}
		parts = append(parts, bundle(start, best))
		start = best
	}
	return parts, true
}

const documentSummaryModelViewHeader = "Source content (# lines are native headings; every other line is [native locator] source text; (context) marks required context outside this section):"

// scopedSummaryViewParts is contract 5. It selects the same canonical leaves
// as contract 4, but each part renders its dependency closure once instead of
// repeating complete node JSON for every leaf. A long text node may continue
// across parts; typed values stay atomic with their context.
func scopedSummaryViewParts(index *docindex.Index, unit docindex.SummaryUnit, budget int) ([]string, []string) {
	positions := map[string]int{}
	for position, node := range index.Nodes {
		positions[node.ID] = position
	}
	parts, missing := []string{}, []string{}
	current := []document.ModelViewFragment{}
	live := document.NewModelView()
	selected := 0
	build := func(fragments []document.ModelViewFragment) *document.ModelView {
		view := document.NewModelView()
		for _, fragment := range fragments {
			view.Add(fragment)
		}
		return view
	}
	flush := func() {
		if selected > 0 {
			parts = append(parts, build(current).String())
		}
		current, selected, live = nil, 0, document.NewModelView()
	}
	for _, node := range scopedSummaryUnitLeaves(index, unit) {
		context, ok := scopedSummaryClosure(index, positions, node)
		if !ok {
			missing = append(missing, fmt.Sprintf("%s:node:%s:required_dependency_missing", unit.ID, node.ID))
			continue
		}
		bundle := append([]document.ModelViewFragment{{Node: node}}, context...)
		// Grow the live view; rebuild only when a part must be closed.
		for _, fragment := range bundle {
			live.Add(fragment)
		}
		if live.Len() <= budget {
			current, selected = append(current, bundle...), selected+1
			continue
		}
		if scopedSummaryHeadingsCarried(current, context) {
			// Headings alone need no section call: the next part carries
			// them as this node's required context.
			current, selected = nil, 0
		}
		flush()
		if candidate := build(bundle); candidate.Len() <= budget {
			current, selected, live = bundle, 1, candidate
			continue
		}
		pieces, fits := scopedSummaryViewSplit(node, context, budget)
		if !fits {
			missing = append(missing, fmt.Sprintf("%s:node:%s:required_context_exceeds_budget", unit.ID, node.ID))
			continue
		}
		for _, piece := range pieces {
			parts = append(parts, build(piece).String())
		}
	}
	flush()
	return parts, missing
}

func scopedSummaryUnitLeaves(index *docindex.Index, unit docindex.SummaryUnit) []document.StructureNode {
	positions := map[string]int{}
	for position, node := range index.Nodes {
		positions[node.ID] = position
	}
	start, end := 0, len(index.Nodes)
	if unit.ChunkStart > 0 && unit.ChunkStart < len(index.Chunks) {
		start = positions[index.Chunks[unit.ChunkStart].NodeID]
	}
	if unit.ChunkEnd < len(index.Chunks) {
		end = positions[index.Chunks[unit.ChunkEnd].NodeID]
	}
	if end <= start {
		end = min(start+1, len(index.Nodes))
	}
	nodes := index.Nodes[start:end]
	if span, ok := index.SummarySpan(unit); ok {
		nodes = nil
		ordinal := 0
		for _, node := range index.Nodes {
			if node.IsContainer() {
				continue
			}
			if ordinal >= span.OrdinalStart && ordinal < span.OrdinalEnd {
				nodes = append(nodes, node)
			}
			ordinal++
		}
	}
	leaves := []document.StructureNode{}
	for _, node := range nodes {
		if !node.IsContainer() {
			leaves = append(leaves, node)
		}
	}
	return leaves
}

func scopedSummaryClosure(index *docindex.Index, positions map[string]int, node document.StructureNode) ([]document.ModelViewFragment, bool) {
	context := []document.ModelViewFragment{}
	seen := map[string]bool{node.ID: true}
	pending := append([]string(nil), node.Dependencies...)
	for len(pending) > 0 {
		id := pending[0]
		pending = pending[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		position, ok := positions[id]
		if !ok {
			return nil, false
		}
		dependency := index.Nodes[position]
		context = append(context, document.ModelViewFragment{Node: dependency, Context: true})
		pending = append(pending, dependency.Dependencies...)
	}
	return context, true
}

func scopedSummaryViewSplit(node document.StructureNode, context []document.ModelViewFragment, budget int) ([][]document.ModelViewFragment, bool) {
	if len(node.Values) > 0 || node.Text == "" {
		return nil, false
	}
	piece := func(start, end int) []document.ModelViewFragment {
		fragment := node
		fragment.Text = node.Text[start:end]
		return append([]document.ModelViewFragment{{Node: fragment, Partial: true, ByteStart: start, ByteEnd: end}}, context...)
	}
	fits := func(fragments []document.ModelViewFragment) bool {
		view := document.NewModelView()
		for _, fragment := range fragments {
			view.Add(fragment)
		}
		return view.Len() <= budget
	}
	pieces := [][]document.ModelViewFragment{}
	for start := 0; start < len(node.Text); {
		low, high, best := start+1, len(node.Text), start
		for low <= high {
			mid := low + (high-low)/2
			if fits(piece(start, mid)) {
				best, low = mid, mid+1
			} else {
				high = mid - 1
			}
		}
		for best > start && best < len(node.Text) && !utf8.RuneStart(node.Text[best]) {
			best--
		}
		if best == start {
			return nil, false
		}
		pieces = append(pieces, piece(start, best))
		start = best
	}
	return pieces, true
}

func scopedSummaryHeadingsCarried(current, context []document.ModelViewFragment) bool {
	carried := map[string]bool{}
	for _, fragment := range context {
		carried[fragment.Node.ID] = true
	}
	for _, fragment := range current {
		if fragment.Context {
			continue
		}
		if fragment.Node.Kind != "heading_label" || !carried[fragment.Node.ID] {
			return false
		}
	}
	return len(current) > 0
}
