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
