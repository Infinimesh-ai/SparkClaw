package websearch

import (
	"context"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type Adapter interface {
	Search(ctx context.Context, request Request) (Result, error)
}

type Request struct {
	Query      string
	MaxResults int
	Freshness  string
}

type Result struct {
	RequestID   string    `json:"request_id"`
	Query       string    `json:"query"`
	Summary     string    `json:"summary"`
	Answer      string    `json:"answer"`
	Provider    string    `json:"provider"`
	Model       string    `json:"model,omitempty"`
	Count       int       `json:"count"`
	Results     []Item    `json:"results"`
	KeyFacts    []KeyFact `json:"key_facts"`
	Citations   []string  `json:"citations,omitempty"`
	RetrievedAt string    `json:"retrieved_at"`
	TookMS      int64     `json:"took_ms,omitempty"`
	Untrusted   bool      `json:"untrusted"`
}

type Item struct {
	EvidenceIndex int      `json:"evidence_index"`
	ID            string   `json:"id,omitempty"`
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Snippet       string   `json:"snippet"`
	Snippets      []string `json:"snippets"`
	Source        string   `json:"source"`
	PublishedAt   string   `json:"published_at"`
	RetrievedAt   string   `json:"retrieved_at,omitempty"`
}

type KeyFact struct {
	ID         string   `json:"id"`
	Claim      string   `json:"claim"`
	Confidence string   `json:"confidence,omitempty"`
	Sources    []string `json:"sources,omitempty"`
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func NewAdapter(cfg config.Config) Adapter {
	provider := strings.ToLower(strings.TrimSpace(cfg.Tools.Web.Search.Provider))
	switch provider {
	case "", InfoProviderName:
		adapter, err := NewInfinimeshInfoAdapter(cfg.Plugins.Entries.InfinimeshInfo.Config, nil)
		if err != nil {
			return disabledAdapter{reason: err.Error()}
		}
		return adapter
	default:
		return disabledAdapter{reason: "unsupported web search provider: " + provider}
	}
}

type disabledAdapter struct {
	reason string
}

func (a disabledAdapter) Search(context.Context, Request) (Result, error) {
	if strings.TrimSpace(a.reason) == "" {
		return Result{}, errors.New("web search adapter is disabled")
	}
	return Result{}, errors.New(a.reason)
}
