package toolhub

import (
	"encoding/json"
	"fmt"

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
	typed := false
	for _, node := range nodes {
		if len(node.Values) > 0 {
			typed = true
			break
		}
	}
	if !typed {
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
			}
		}
		bundle := map[string]any{"node": node, "dependencies": dependencies}
		raw, _ := json.Marshal([]any{bundle})
		if len(raw) > budget {
			missing = append(missing, fmt.Sprintf("%s:node:%s:typed_context_exceeds_budget", unit.ID, node.ID))
			continue
		}
		candidate := append(append([]any(nil), group...), bundle)
		raw, _ = json.Marshal(candidate)
		if len(raw) > budget {
			flush()
		}
		group = append(group, bundle)
	}
	flush()
	return parts, missing
}
