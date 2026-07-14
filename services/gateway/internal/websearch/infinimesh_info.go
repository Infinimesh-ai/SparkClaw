package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
)

const (
	maxInfinimeshInfoResults = 40
	maxSourceSnippetBytes    = 1200
)

type InfinimeshInfoAdapter struct {
	cfg    config.InfinimeshInfoConfig
	client *infinimeshinfo.Client
}

func NewInfinimeshInfoAdapter(cfg config.InfinimeshInfoConfig, httpClient *http.Client) (InfinimeshInfoAdapter, error) {
	client, err := infinimeshinfo.NewClient(infinimeshinfo.Config{
		BaseURL:              cfg.BaseURL,
		EntitlementProof:     cfg.EntitlementProof,
		DeviceAttestation:    cfg.DeviceAttestation,
		LicenseProof:         cfg.LicenseProof,
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
	items, sourceURLs := infinimeshSources(response.Sources, maxResults)
	answer := strings.TrimSpace(response.AnswerContext.Summary)
	if answer == "" {
		answer = evidenceAnswer(items)
	}
	if answer == "" {
		return Result{}, errors.New("infinimesh info returned no answer or usable sources")
	}
	return Result{
		Query:     query,
		Answer:    answer,
		Provider:  infinimeshinfo.ProviderName,
		Count:     len(items),
		Results:   items,
		Citations: infinimeshCitations(response.AnswerContext, response.Sources, sourceURLs),
		TookMS:    time.Since(start).Milliseconds(),
		Untrusted: true,
	}, nil
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

func infinimeshSources(sources []infinimeshinfo.Source, limit int) ([]Item, map[string]string) {
	items := make([]Item, 0, minInt(limit, len(sources)))
	urlsByID := map[string]string{}
	for _, source := range sources {
		if len(items) >= limit {
			break
		}
		if !publicHTTPURL(source.URL) {
			continue
		}
		item := Item{
			Title:       strings.TrimSpace(source.Title),
			URL:         strings.TrimSpace(source.URL),
			Snippet:     boundedSnippet(source.Snippets, maxSourceSnippetBytes),
			Source:      strings.TrimSpace(source.SourceType),
			PublishedAt: strings.TrimSpace(source.PublishedAt),
		}
		if item.Title == "" {
			item.Title = item.URL
		}
		items = append(items, item)
		if id := strings.TrimSpace(source.ID); id != "" {
			urlsByID[id] = item.URL
		}
	}
	return items, urlsByID
}

func infinimeshCitations(answer infinimeshinfo.AnswerContext, sources []infinimeshinfo.Source, urlsByID map[string]string) []string {
	referenced := map[string]bool{}
	directURLs := map[string]bool{}
	directOrder := []string{}
	for _, citation := range answer.Citations {
		citation = strings.TrimSpace(citation)
		if citation == "" {
			continue
		}
		if publicHTTPURL(citation) {
			if !directURLs[citation] {
				directURLs[citation] = true
				directOrder = append(directOrder, citation)
			}
		} else {
			referenced[citation] = true
		}
	}
	for _, fact := range answer.KeyFacts {
		for _, id := range fact.Sources {
			if id = strings.TrimSpace(id); id != "" {
				referenced[id] = true
			}
		}
	}
	result := []string{}
	seen := map[string]bool{}
	for _, source := range sources {
		id := strings.TrimSpace(source.ID)
		url := urlsByID[id]
		if url == "" || seen[url] || (!referenced[id] && !directURLs[url]) {
			continue
		}
		seen[url] = true
		result = append(result, url)
	}
	for _, directURL := range directOrder {
		if !seen[directURL] {
			seen[directURL] = true
			result = append(result, directURL)
		}
	}
	if len(result) > 0 {
		return result
	}
	for _, source := range sources {
		url := urlsByID[strings.TrimSpace(source.ID)]
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		result = append(result, url)
	}
	return result
}

func boundedSnippet(snippets []string, maxBytes int) string {
	parts := make([]string, 0, len(snippets))
	length := 0
	for _, snippet := range snippets {
		snippet = strings.TrimSpace(snippet)
		if snippet == "" {
			continue
		}
		separator := 0
		if len(parts) > 0 {
			separator = 1
		}
		remaining := maxBytes - length - separator
		if remaining <= 0 {
			break
		}
		if len(snippet) > remaining {
			snippet = truncateUTF8(snippet, remaining)
		}
		parts = append(parts, snippet)
		length += len(snippet) + separator
	}
	return strings.Join(parts, " ")
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		_, size := utf8.DecodeLastRuneInString(value)
		if size <= 0 || size > len(value) {
			return ""
		}
		value = value[:len(value)-size]
	}
	return value
}

func evidenceAnswer(items []Item) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(item.Snippet)
		if text == "" {
			text = strings.TrimSpace(item.Title)
		}
		if text != "" {
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n")
}

func publicHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
