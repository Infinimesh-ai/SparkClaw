package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestInfinimeshInfoAdapterMapsSummarySourcesAndCitations(t *testing.T) {
	var freshness string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/info/tokens/issue":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"epoch": time.Now().UTC().Format("2006-01-02"),
				"issued_tokens": []map[string]any{{
					"type": "info.basic", "token_mode": "internal_opaque", "token": "token-1",
					"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
				}},
				"quota_remaining": map[string]int{"info.basic": 9},
			})
		case "/v1/info/query":
			var body struct {
				Requirements struct {
					Freshness string `json:"freshness"`
				} `json:"requirements"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			freshness = body.Requirements.Freshness
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id": r.Header.Get("X-Request-Id"),
				"status":     "ok",
				"answer_context": map[string]any{
					"summary":   "Mapped answer",
					"key_facts": []map[string]any{{"claim": "fact", "sources": []string{"src-2", "src-1"}}},
				},
				"sources": []map[string]any{
					{"id": "bad", "title": "Bad", "url": "file:///private", "snippets": []string{"ignored"}},
					{"id": "src-1", "title": "First", "url": "https://example.test/first", "source_type": "official_documentation", "published_at": "2026-07-13T00:00:00Z", "authority_score": 0.95, "snippets": []string{"first", "evidence"}},
					{"id": "src-2", "title": "Second", "url": "https://example.test/second", "source_type": "news", "snippets": []string{"second evidence"}},
				},
				"usage": map[string]any{"cost_credits": 1, "token_type": "info.basic"},
			})
		}
	}))
	defer server.Close()

	cfg := testInfinimeshConfig(server.URL)
	adapter, err := NewInfinimeshInfoAdapter(cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Search(context.Background(), Request{Query: "最新 SparkClaw 信息", MaxResults: 3})
	if err != nil {
		t.Fatal(err)
	}
	if freshness != "high" || result.RequestID == "" || result.Query != "最新 SparkClaw 信息" || result.Summary != "Mapped answer" || result.Answer != "Mapped answer" || result.Provider != "infinimesh-info" || !result.Untrusted {
		t.Fatalf("unexpected mapped result: %#v", result)
	}
	if result.Count != 2 || len(result.Results) != 2 || result.Results[0].EvidenceIndex != 1 || result.Results[0].ID != "src-1" || result.Results[0].Snippet != "first evidence" || len(result.Results[0].Snippets) != 2 || result.Results[0].Source != "official_documentation" || result.Results[0].AuthorityScore != 0.95 {
		t.Fatalf("unexpected source mapping: %#v", result.Results)
	}
	if len(result.KeyFacts) != 1 || result.KeyFacts[0].ID != "fact:0" || result.KeyFacts[0].Claim != "fact" || result.RetrievedAt == "" {
		t.Fatalf("fixed Info evidence metadata was not preserved: %#v", result)
	}
	if len(result.Citations) != 2 || result.Citations[0] != "https://example.test/first" || result.Citations[1] != "https://example.test/second" {
		t.Fatalf("unexpected citation mapping: %#v", result.Citations)
	}
}

func TestInfinimeshInfoAdapterUsesSourceEvidenceWhenSummaryIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/info/tokens/issue" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"epoch": time.Now().UTC().Format("2006-01-02"),
				"issued_tokens": []map[string]any{{
					"type": "info.basic", "token_mode": "internal_opaque", "token": "token-1",
					"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
				}},
				"quota_remaining": map[string]int{"info.basic": 9},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id": r.Header.Get("X-Request-Id"), "status": "ok", "answer_context": map[string]any{"summary": ""},
			"sources": []map[string]any{{"id": "src-1", "title": "Evidence title", "url": "https://example.test/evidence", "snippets": []string{"Evidence text"}}},
			"usage":   map[string]any{"cost_credits": 1, "token_type": "info.basic"},
		})
	}))
	defer server.Close()

	adapter, err := NewInfinimeshInfoAdapter(testInfinimeshConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Search(context.Background(), Request{Query: "evidence query"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "Evidence text" {
		t.Fatalf("unexpected evidence answer: %q", result.Answer)
	}
	if result.Summary != "" || len(result.Results) != 1 || len(result.Results[0].Snippets) != 1 {
		t.Fatalf("missing fixed summary must stay explicit while source evidence remains available: %#v", result)
	}
}

func TestInfinimeshInfoAdapterUsesKeyFactsWhenSummaryAndSourcesAreEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/info/tokens/issue" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"epoch": time.Now().UTC().Format("2006-01-02"),
				"issued_tokens": []map[string]any{{
					"type": "info.basic", "token_mode": "internal_opaque", "token": "token-1",
					"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
				}},
				"quota_remaining": map[string]int{"info.basic": 9},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id": r.Header.Get("X-Request-Id"), "status": "ok",
			"answer_context": map[string]any{
				"summary": "", "key_facts": []map[string]any{{"claim": "杭州当前气温31°C。"}},
			},
			"usage": map[string]any{"cost_credits": 1, "token_type": "info.basic"},
		})
	}))
	defer server.Close()

	adapter, err := NewInfinimeshInfoAdapter(testInfinimeshConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Search(context.Background(), Request{Query: "杭州天气"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "" || result.Answer != "杭州当前气温31°C。" || len(result.KeyFacts) != 1 {
		t.Fatalf("key facts should remain usable without summary or sources: %#v", result)
	}
}

func testInfinimeshConfig(baseURL string) config.InfinimeshInfoConfig {
	return config.InfinimeshInfoConfig{
		BaseURL:               baseURL,
		TokenBatchSize:        3,
		MaxAttempts:           1,
		RetryBaseDelayMS:      1,
		RequestTimeoutSeconds: 1,
		ResponseBodyMaxBytes:  1 << 20,
		Language:              "zh-CN",
		MaxSources:            8,
		LicenseID:             "lic_test",
		LicenseKey:            "ilk_v1.lic_test.test-key",
	}
}
