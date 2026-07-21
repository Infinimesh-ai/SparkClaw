package toolhub

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

func infoQueryDefinition() app.ToolDefinition {
	return app.ToolDefinition{
		Name:        "info.query",
		Description: "Submit the frozen owner question directly to Infinimesh Info and return bounded untrusted summary, fact, source, and citation evidence.",
		InputSchema: strictObjectSchema([]string{"query"}, map[string]any{
			"query": stringSchema(),
		}),
		OutputSchema: objectSchema([]string{"request_id", "query", "summary", "provider", "key_facts", "sources", "citations", "retrieved_at", "untrusted"}, map[string]any{
			"request_id":   stringSchema(),
			"query":        stringSchema(),
			"summary":      stringSchema(),
			"provider":     stringSchema(),
			"key_facts":    arraySchema(objectValueSchema()),
			"sources":      arraySchema(objectValueSchema()),
			"citations":    stringArraySchema(),
			"retrieved_at": stringSchema(),
			"took_ms":      integerSchema(),
			"untrusted":    booleanSchema(),
		}),
		Risk:             app.RiskRead,
		RequiresApproval: false,
		Idempotent:       true,
		TimeoutMS:        30000,
		Sandbox:          "forbidden",
		Audit:            "always",
	}
}

func (h *ToolHub) infoQuery(ctx context.Context, args map[string]any) (Result, error) {
	result, err := h.webSearch.Search(ctx, websearch.Request{
		Query:      stringArg(args, "query", ""),
		MaxResults: h.cfg.Plugins.Entries.InfinimeshInfo.Config.MaxSources,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{
		"request_id":   result.RequestID,
		"query":        result.Query,
		"summary":      result.Summary,
		"provider":     result.Provider,
		"key_facts":    result.KeyFacts,
		"sources":      result.Results,
		"citations":    result.Citations,
		"retrieved_at": result.RetrievedAt,
		"took_ms":      int(result.TookMS),
		"untrusted":    true,
	}}, nil
}
