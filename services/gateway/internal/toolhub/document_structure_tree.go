package toolhub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

func documentStructureContext(ctx context.Context, args map[string]any) context.Context {
	version := intArg(args, "structure_version", document.LegacyStructureSchemaVersion)
	if _, ok := args["node_id"]; ok {
		version = document.StructureSchemaVersion
	}
	if _, ok := args["depth"]; ok {
		version = document.StructureSchemaVersion
	}
	return docindex.WithStructureVersion(ctx, version)
}

func (h *ToolHub) documentTreeOutline(index *docindex.Index, metadata document.Metadata, args map[string]any) (Result, error) {
	budget := h.cfg.DocumentStructureBytes()
	if requested := intArg(args, "max_bytes", 0); requested > 0 {
		budget = min(budget, requested)
	}
	page, err := index.OutlineTree(docindex.OutlineRequest{NodeID: stringArg(args, "node_id", ""), Depth: intArg(args, "depth", 0), Cursor: stringArg(args, "cursor", ""), SourceSHA256: stringArg(args, "source_sha256", ""), StructureGeneration: stringArg(args, "structure_generation", ""), MaxBytes: budget, Limit: intArg(args, "limit", 0), SharePercent: h.cfg.Runtime.DocumentStructureSharePercent})
	if err != nil {
		return Result{}, err
	}
	raw, _ := json.Marshal(page)
	content := string(raw)
	if boolArg(args, "model_view", false) {
		content = documentOutlineModelView(index, page)
	}
	return Result{Output: map[string]any{
		"path": metadata.Relative, "pages": index.Manifest.Pages, "chunks": index.Manifest.Chunks, "outline": documentJSONValue(page.Nodes),
		"node": documentJSONValue(page.Node), "path_nodes": documentJSONValue(page.Path), "structure_budget": documentJSONValue(page.Budget),
		"structure": documentJSONValue(index.Structure), "source_sha256": page.SourceSHA256, "structure_generation": page.StructureGeneration,
		"expanded_node_id": page.Node.ID, "next_cursor": page.NextCursor, "truncated": page.NextCursor != "", "coverage": documentJSONValue(page.Coverage),
		"version_pinned": stringArg(args, "source_sha256", "") != "" && stringArg(args, "structure_generation", "") != "", "source_version_pinned": stringArg(args, "source_sha256", "") != "", "generation_pinned": stringArg(args, "structure_generation", "") != "",
		"content": content, "untrusted": true,
	}}, nil
}

// documentOutlineModelView renders one tree level for a model. Node IDs stay
// because they are the handles for expansion and selection; parent IDs,
// ordinals, provenance and zero counters remain in the structured output.
// read_tokens uses the same model view that delivers content, so navigation
// estimates and delivered evidence share one measure.
func documentOutlineModelView(index *docindex.Index, page docindex.TreeOutlineResult) string {
	var out strings.Builder
	out.WriteString("Expanded " + documentOutlineModelViewNode(index, page.Node) + "\n")
	if len(page.Path) > 0 {
		titles := []string{}
		for _, node := range page.Path {
			titles = append(titles, node.Title)
		}
		out.WriteString("Path: " + strings.Join(titles, " > ") + "\n")
	}
	if len(page.Nodes) > 0 {
		out.WriteString("Children (indentation is nesting; id is the handle for document.outline node_id or document.read_range node_ids; leads and previews are not acquired evidence):\n")
	}
	depth := map[string]int{page.Node.ID: -1}
	for _, node := range page.Nodes {
		level := 0
		if parent, ok := depth[node.ParentID]; ok {
			level = parent + 1
		}
		depth[node.ID] = level
		out.WriteString(strings.Repeat("  ", level) + "- " + documentOutlineModelViewNode(index, node) + "\n")
	}
	if page.NextCursor != "" {
		out.WriteString("More children remain: call document.outline with the same node_id to continue this level.\n")
	}
	flags := []string{}
	for name, set := range map[string]bool{"leads omitted": page.Budget.LeadsOmitted, "previews trimmed": page.Budget.PreviewsTrimmed, "grouped into navigation windows": page.Budget.Grouped, "depth limited": page.Budget.DepthLimited} {
		if set {
			flags = append(flags, name)
		}
	}
	if len(flags) > 0 {
		sort.Strings(flags)
		out.WriteString("Outline budget: " + strings.Join(flags, "; ") + ".\n")
	}
	fmt.Fprintf(&out, "Discovery coverage: %s %d/%d", page.Coverage.Status, page.Coverage.Covered, page.Coverage.Required)
	return out.String()
}

func documentOutlineModelViewNode(index *docindex.Index, node docindex.OutlineViewNode) string {
	parts := []string{"id=" + node.ID}
	if node.Span != nil {
		parts = append(parts, node.Kind, strconv.Quote(node.Title))
		if node.Span.FirstBlock != "" {
			span := node.Span.FirstBlock
			if node.Span.LastBlock != "" && node.Span.LastBlock != node.Span.FirstBlock {
				span += ".." + node.Span.LastBlock
			}
			parts = append(parts, "range="+span)
		}
		facts := []string{}
		if node.Stats != nil {
			facts = append(facts, fmt.Sprintf("%d leaves", node.Stats.Leaves))
			if bytes, ok := index.ModelViewBytes(document.StructureNode{Span: node.Span}); ok {
				facts = append(facts, fmt.Sprintf("read_tokens~%d", document.ModelViewTokens(bytes)))
			}
			if node.Stats.ChildContainers > 0 {
				facts = append(facts, fmt.Sprintf("%d child sections", node.Stats.ChildContainers))
			}
			if node.Stats.Tables > 0 {
				facts = append(facts, fmt.Sprintf("%d tables", node.Stats.Tables))
			}
			if node.Stats.Unavailable > 0 {
				facts = append(facts, fmt.Sprintf("%d unavailable", node.Stats.Unavailable))
			}
			if node.Stats.Unrecognized > 0 {
				facts = append(facts, fmt.Sprintf("%d unrecognized", node.Stats.Unrecognized))
			}
		}
		if node.Expandable {
			facts = append(facts, "expandable")
		}
		line := strings.Join(parts, " ")
		if len(facts) > 0 {
			line += "; " + strings.Join(facts, ", ")
		}
		if node.Lead != nil && node.Lead.Text != "" {
			line += "; lead preview: " + strconv.Quote(node.Lead.Text)
		}
		return line
	}
	if node.Locator != nil {
		parts = append(parts, "["+document.ModelViewLocator(*node.Locator)+"]")
	}
	if node.Kind == "heading_label" && node.Title != "" {
		parts = append(parts, "heading "+strconv.Quote(node.Title))
	}
	if node.Availability != "" && node.Availability != document.NodeAvailable {
		parts = append(parts, "("+string(node.Availability)+")")
	}
	if node.Preview != nil && node.Preview.Text != "" {
		parts = append(parts, "preview: "+strconv.Quote(node.Preview.Text))
	}
	return strings.Join(parts, " ")
}
