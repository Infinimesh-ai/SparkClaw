package toolhub

import (
	"context"
	"encoding/json"

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
	content, _ := json.Marshal(page)
	return Result{Output: map[string]any{
		"path": metadata.Relative, "pages": index.Manifest.Pages, "chunks": index.Manifest.Chunks, "outline": documentJSONValue(page.Nodes),
		"node": documentJSONValue(page.Node), "path_nodes": documentJSONValue(page.Path), "structure_budget": documentJSONValue(page.Budget),
		"structure": documentJSONValue(index.Structure), "source_sha256": page.SourceSHA256, "structure_generation": page.StructureGeneration,
		"expanded_node_id": page.Node.ID, "next_cursor": page.NextCursor, "truncated": page.NextCursor != "", "coverage": documentJSONValue(page.Coverage),
		"version_pinned": stringArg(args, "source_sha256", "") != "" && stringArg(args, "structure_generation", "") != "", "source_version_pinned": stringArg(args, "source_sha256", "") != "", "generation_pinned": stringArg(args, "structure_generation", "") != "",
		"content": string(content), "untrusted": true,
	}}, nil
}
