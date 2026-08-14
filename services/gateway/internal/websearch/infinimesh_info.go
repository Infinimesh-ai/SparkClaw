package websearch

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
)

const (
	maxInfinimeshInfoResults = 40
	InfoProviderName         = infinimeshinfo.ProviderName
)

type InfinimeshInfoAdapter struct {
	cfg    config.InfinimeshInfoConfig
	client *infinimeshinfo.Client
}

func NewInfinimeshInfoAdapter(cfg config.InfinimeshInfoConfig, httpClient *http.Client) (InfinimeshInfoAdapter, error) {
	client, err := infinimeshinfo.NewClient(infinimeshinfo.Config{
		BaseURL:              cfg.BaseURL,
		LicenseID:            cfg.LicenseID,
		LicenseKey:           cfg.LicenseKey,
		TokenBatchSize:       cfg.TokenBatchSize,
		MaxAttempts:          cfg.MaxAttempts,
		RetryBaseDelay:       time.Duration(cfg.RetryBaseDelayMS) * time.Millisecond,
		RequestTimeout:       time.Duration(cfg.RequestTimeoutSeconds) * time.Second,
		ResponseBodyMaxBytes: cfg.ResponseBodyMaxBytes,
	}, httpClient)
	if err != nil {
		return InfinimeshInfoAdapter{}, err
	}
	return InfinimeshInfoAdapter{cfg: cfg, client: client}, nil
}

func (a InfinimeshInfoAdapter) Search(ctx context.Context, request Request) (Result, error) {
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return Result{}, errors.New("web.search query cannot be empty")
	}
	maxResults := request.MaxResults
	if maxResults <= 0 {
		maxResults = a.cfg.MaxSources
	}
	if maxResults <= 0 {
		maxResults = 8
	}
	if a.cfg.MaxSources > 0 && maxResults > a.cfg.MaxSources {
		maxResults = a.cfg.MaxSources
	}
	maxResults = clampInt(maxResults, 1, maxInfinimeshInfoResults)

	start := time.Now()
	response, err := a.client.Query(ctx, infinimeshinfo.QueryRequest{
		Query:      query,
		TaskType:   "general_research",
		Freshness:  infinimeshFreshness(query, request.Freshness),
		MaxSources: maxResults,
		Language:   a.cfg.Language,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{
		SchemaVersion: InfoResultSchemaVersion,
		RequestID:     response.RequestID,
		Status:        response.Status,
		Query:         query,
		Provider:      InfoProviderName,
		RetrievedAt:   time.Now().UTC().Format(time.RFC3339),
		TookMS:        time.Since(start).Milliseconds(),
		Aggregate:     mapAggregate(response.AnswerContext),
		Sources:       mapSources(response.Sources),
		Usage: Usage{
			CostCredits: response.Usage.CostCredits,
			TokenType:   response.Usage.TokenType,
			CacheHit:    response.Usage.CacheHit,
		},
		Untrusted: true,
	}, nil
}

func mapAggregate(answer infinimeshinfo.AnswerContext) Aggregate {
	aggregate := Aggregate{
		Summary:                strings.TrimSpace(answer.Summary),
		Freshness:              mapFreshness(answer.Freshness),
		Uncertainty:            append([]string(nil), answer.Uncertainty...),
		RecommendedNextActions: append([]string(nil), answer.RecommendedNextActions...),
	}
	for _, fact := range answer.KeyFacts {
		aggregate.Facts = append(aggregate.Facts, Fact{
			Claim: strings.TrimSpace(fact.Claim), Confidence: strings.TrimSpace(fact.Confidence),
			Sources: append([]string(nil), fact.Sources...),
		})
	}
	for _, conflict := range answer.Conflicts {
		mapped := Conflict{Topic: strings.TrimSpace(conflict.Topic)}
		for _, viewpoint := range conflict.Viewpoints {
			mapped.Viewpoints = append(mapped.Viewpoints, Viewpoint{
				Claim: strings.TrimSpace(viewpoint.Claim), Sources: append([]string(nil), viewpoint.Sources...),
			})
		}
		aggregate.Conflicts = append(aggregate.Conflicts, mapped)
	}
	return aggregate
}

func mapFreshness(freshness infinimeshinfo.FreshnessStatus) Freshness {
	return Freshness{
		Status: strings.TrimSpace(freshness.Status), LatestSourceDate: copyOptionalString(freshness.LatestSourceDate),
		StalenessRisk: strings.TrimSpace(freshness.StalenessRisk),
	}
}

func mapSources(sources []infinimeshinfo.Source) []Source {
	out := make([]Source, 0, len(sources))
	for index, source := range sources {
		out = append(out, Source{
			ID: strings.TrimSpace(source.ID), Title: strings.TrimSpace(source.Title), URL: strings.TrimSpace(source.URL),
			SourceType: strings.TrimSpace(source.SourceType), PublishedAt: copyOptionalString(source.PublishedAt),
			RetrievedAt: strings.TrimSpace(source.RetrievedAt), AuthorityScore: source.AuthorityScore,
			Snippets: append([]string(nil), source.Snippets...), evidenceIndex: index, evidenceIndexSet: true,
		})
	}
	return out
}

func copyOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := strings.TrimSpace(*value)
	return &copy
}

func infinimeshFreshness(query, requested string) string {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "high", "latest", "recent", "current", "today", "now", "real-time", "realtime":
		return "high"
	case "low":
		return "low"
	case "medium":
		return "medium"
	}
	lower := strings.ToLower(query)
	for _, term := range []string{
		"latest", "recent", "current", "today", "now", "real-time", "realtime",
		"最新", "最近", "当前", "今天", "今日", "实时", "现在",
	} {
		if strings.Contains(lower, term) {
			return "high"
		}
	}
	return "medium"
}
