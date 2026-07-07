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
		"query":     result.Query,
		"answer":    result.Answer,
		"provider":  result.Provider,
		"model":     result.Model,
		"count":     result.Count,
		"results":   result.Results,
		"citations": result.Citations,
		"took_ms":   int(result.TookMS),
		"untrusted": result.Untrusted,
	}}, nil
}
