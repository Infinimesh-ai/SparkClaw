package toolhub

import (
	"fmt"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
)

// Resolve ordering locally instead of asking a model to perform ordering over
// a long outline. Relations refer only to native siblings and include at most
// eight neighbors on each side. They are navigation facts, not body coverage.
func selectedSourceLocationRelations(index *docindex.Index, ids []string) []string {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	locations := map[string]string{}
	for _, node := range index.Nodes {
		if node.Title != "" {
			locations[node.ID] = fmt.Sprintf("%q (%s)", node.Title, node.Locator.Block)
		}
	}
	facts := []string{}
	for position, node := range index.Nodes {
		if !wanted[node.ID] || node.Title == "" {
			continue
		}
		subject := locations[node.ID]
		if parent := locations[node.ParentID]; parent != "" {
			facts = append(facts, subject+" belongs to parent heading "+parent+".")
		}
		before, after := []string{}, []string{}
		for i, sibling := range index.Nodes {
			if sibling.ID == node.ID || sibling.Title == "" || sibling.ParentID != node.ParentID {
				continue
			}
			if i < position {
				before = append(before, locations[sibling.ID])
			} else {
				after = append(after, locations[sibling.ID])
			}
		}
		for _, other := range before[max(0, len(before)-8):] {
			facts = append(facts, subject+" comes AFTER "+other+" in the original source.")
		}
		for _, other := range after[:min(8, len(after))] {
			facts = append(facts, subject+" comes BEFORE "+other+" in the original source.")
		}
	}
	return facts
}

// This map is copied from native structure, never from model prose or summary
// unit ordinals. Duplicate titles remain distinct through their native blocks.
func summarySourceLocations(index *docindex.Index, unit *docindex.SummaryUnit) []map[string]any {
	byID := map[string]string{}
	for _, node := range index.Nodes {
		byID[node.ID] = node.Locator.Block
	}
	start, end := 0, len(index.Nodes)
	if unit != nil {
		for i, node := range index.Nodes {
			if unit.ChunkStart < len(index.Chunks) && node.ID == index.Chunks[unit.ChunkStart].NodeID {
				start = i
			}
			if unit.ChunkEnd < len(index.Chunks) && node.ID == index.Chunks[unit.ChunkEnd].NodeID {
				end = i
			}
		}
	}
	locations := []map[string]any{}
	for i, node := range index.Nodes {
		if i < start || i >= end || node.Title == "" {
			continue
		}
		locations = append(locations, map[string]any{"title": node.Title, "block": node.Locator.Block, "parent_block": byID[node.ParentID], "page": node.Locator.Page})
	}
	return locations
}
