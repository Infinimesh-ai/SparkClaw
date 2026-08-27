package toolhub

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

func (h *ToolHub) webSearchTool(ctx context.Context, args map[string]any, _, runID string) (Result, error) {
	call, err := h.beginInfoCall(ctx, runID, true)
	if err != nil {
		return Result{}, err
	}
	defer call.finish()
	result, err := call.search.Search(call.ctx, websearch.Request{
		Query:      stringArg(args, "query", ""),
		MaxResults: intArg(args, "max_results", 5),
		Freshness:  stringArg(args, "freshness", ""),
	})
	if err != nil {
		return Result{}, mapInfoCallError(call.ctx, err)
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
