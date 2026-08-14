package toolhub

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

func (h *ToolHub) webSearchTool(ctx context.Context, args map[string]any) (Result, error) {
	result, err := h.webSearch.Search(ctx, websearch.Request{
		Query:      stringArg(args, "query", ""),
		MaxResults: intArg(args, "max_results", 5),
		Freshness:  stringArg(args, "freshness", ""),
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"schema_version": result.SchemaVersion,
		"request_id":     result.RequestID,
		"status":         result.Status,
		"query":          result.Query,
		"provider":       result.Provider,
		"retrieved_at":   result.RetrievedAt,
		"took_ms":        result.TookMS,
		"aggregate":      result.Aggregate,
		"sources":        result.Sources,
		"usage":          result.Usage,
		"untrusted":      result.Untrusted,
	}}, nil
}
