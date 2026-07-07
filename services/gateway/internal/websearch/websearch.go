package websearch

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

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
	Query     string   `json:"query"`
	Answer    string   `json:"answer"`
	Provider  string   `json:"provider"`
	Model     string   `json:"model,omitempty"`
	Count     int      `json:"count"`
	Results   []Item   `json:"results"`
	Citations []string `json:"citations,omitempty"`
	TookMS    int64    `json:"took_ms,omitempty"`
	Untrusted bool     `json:"untrusted"`
}

type Item struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	Source      string `json:"source"`
	PublishedAt string `json:"published_at"`
}

func NewAdapter(cfg config.Config) Adapter {
	provider := strings.ToLower(strings.TrimSpace(cfg.Tools.Web.Search.Provider))
	if provider == "" || provider == "parallel-free" {
		return NewParallelFreeAdapter(cfg.Plugins.Entries.Parallel.Config.WebSearch, &http.Client{Timeout: webSearchHTTPTimeout(cfg)})
	}
	return disabledAdapter{reason: "unsupported web search provider: " + provider}
}

func webSearchHTTPTimeout(cfg config.Config) time.Duration {
	seconds := cfg.Model.HTTPTimeoutSeconds
	if seconds <= 0 || seconds > 120 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
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
