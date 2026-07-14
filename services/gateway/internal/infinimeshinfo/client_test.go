package infinimeshinfo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testEntitlement = "entitlement-sentinel"
	testAttestation = "attestation-sentinel"
	testLicense     = "license-sentinel"
	testQuery       = "public-query-sentinel"
)

func testClientConfig(baseURL string) Config {
	return Config{
		BaseURL:              baseURL,
		EntitlementProof:     testEntitlement,
		DeviceAttestation:    testAttestation,
		LicenseProof:         testLicense,
		TokenBatchSize:       3,
		MaxAttempts:          3,
		RetryBaseDelay:       time.Millisecond,
		RequestTimeout:       time.Second,
		ResponseBodyMaxBytes: 1 << 20,
	}
}

func writeIssuedTokens(t *testing.T, w http.ResponseWriter, batch string, count int) {
	t.Helper()
	issued := make([]map[string]any, 0, count)
	for index := 0; index < count; index++ {
		issued = append(issued, map[string]any{
			"type":       "info.basic",
			"token_mode": "internal_opaque",
			"token":      batch + "-token-" + string(rune('a'+index)),
			"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"epoch":           time.Now().UTC().Format("2006-01-02"),
		"issued_tokens":   issued,
		"quota_remaining": map[string]int{"info.basic": 90},
	})
}

func TestClientIssueQueryRetryUsesFreshTokenAndRandomRequestID(t *testing.T) {
	var mu sync.Mutex
	issueCount := 0
	queryCount := 0
	authorizations := []string{}
	requestIDs := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case issueTokensPath:
			mu.Lock()
			issueCount++
			mu.Unlock()
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+testEntitlement {
				t.Error("token issue request contract mismatch")
			}
			var body issueTokensRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error("token issue body did not decode")
				return
			}
			if body.DeviceAttestation != testAttestation || body.LicenseProof != testLicense || body.TokenMode != "internal_opaque" || len(body.BlindedTokenRequests) != 0 {
				t.Error("token issue body contract mismatch")
			}
			if len(body.RequestedTokens) != 1 || body.RequestedTokens[0].Type != TokenTypeBasic || body.RequestedTokens[0].Count != 3 {
				t.Error("token issue batch contract mismatch")
			}
			writeIssuedTokens(t, w, "batch-1", 3)
		case queryPath:
			var body infoQueryRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error("query body did not decode")
				return
			}
			raw, _ := json.Marshal(body)
			text := string(raw)
			if strings.Contains(text, testEntitlement) || strings.Contains(text, testAttestation) || strings.Contains(text, testLicense) {
				t.Error("query request leaked issuance credentials")
			}
			for _, forbidden := range []string{"session_id", "user_id", "device_id", "account_id", "license_id"} {
				if strings.Contains(text, forbidden) {
					t.Error("query request leaked a stable identity field")
				}
			}
			if body.Query != testQuery || body.Product != "sparkclaw" || body.TaskType != "general_research" || body.ContextPolicy.IncludePrivateContext || body.ContextPolicy.LocalContextSummary != nil {
				t.Error("query request contract mismatch")
			}
			if !body.Requirements.CitationRequired || body.Requirements.ResponseMode != "agent_context" || body.Requirements.MaxSources != 5 || body.Requirements.Freshness != "high" {
				t.Error("query requirements contract mismatch")
			}
			mu.Lock()
			queryCount++
			current := queryCount
			authorizations = append(authorizations, r.Header.Get("Authorization"))
			requestIDs = append(requestIDs, body.RequestID)
			mu.Unlock()
			if body.RequestID == "" || r.Header.Get("X-Request-Id") != body.RequestID {
				t.Error("query request ID contract mismatch")
			}
			w.Header().Set("Content-Type", "application/json")
			if current == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"request_id": body.RequestID,
					"error": map[string]any{
						"code":      "SERVICE_DEGRADED",
						"message":   "temporary failure " + testQuery + " " + testEntitlement,
						"retryable": true,
						"details":   map[string]any{},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id": body.RequestID,
				"status":     "ok",
				"answer_context": map[string]any{
					"summary": "contract summary",
					"key_facts": []map[string]any{{
						"claim":   "contract claim",
						"sources": []string{"src-1"},
					}},
				},
				"sources": []map[string]any{{
					"id":          "src-1",
					"title":       "Contract source",
					"url":         "https://example.test/source",
					"source_type": "official_documentation",
					"snippets":    []string{"source evidence"},
				}},
				"usage": map[string]any{"cost_credits": 1, "token_type": "info.basic"},
			})
		default:
			t.Error("unexpected Info API path")
		}
	}))
	defer server.Close()

	client, err := NewClient(testClientConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.retryJitter = func(delay time.Duration) time.Duration { return delay }
	response, err := client.Query(context.Background(), QueryRequest{
		Query:      testQuery,
		Freshness:  "high",
		MaxSources: 5,
		Language:   "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.AnswerContext.Summary != "contract summary" || len(response.Sources) != 1 {
		t.Fatal("query response contract mismatch")
	}
	mu.Lock()
	defer mu.Unlock()
	if issueCount != 1 || queryCount != 2 {
		t.Fatalf("unexpected request counts: issue=%d query=%d", issueCount, queryCount)
	}
	if authorizations[0] == authorizations[1] || !strings.HasPrefix(authorizations[0], "PrivateToken ") || !strings.HasPrefix(authorizations[1], "PrivateToken ") {
		t.Fatal("query retry reused an anonymous token")
	}
	if requestIDs[0] == requestIDs[1] {
		t.Fatal("query retry reused a request ID")
	}
}

func TestClientNonRetryableErrorIsSanitized(t *testing.T) {
	queryCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == issueTokensPath {
			writeIssuedTokens(t, w, "batch-1", 3)
			return
		}
		queryCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":      "POLICY_DENIED",
				"message":   "denied " + testQuery + " " + testLicense,
				"retryable": false,
				"details":   map[string]any{},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(testClientConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Query(context.Background(), QueryRequest{Query: testQuery})
	if err == nil {
		t.Fatal("expected policy error")
	}
	if queryCount != 1 {
		t.Fatalf("non-retryable query attempts = %d, want 1", queryCount)
	}
	if strings.Contains(err.Error(), testQuery) || strings.Contains(err.Error(), testLicense) || strings.Contains(err.Error(), testEntitlement) {
		t.Fatal("sanitized error leaked request data")
	}
}

func TestClientTokenExpiredDiscardsRemainingBatch(t *testing.T) {
	issueCount := 0
	queryCount := 0
	authorizations := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case issueTokensPath:
			issueCount++
			writeIssuedTokens(t, w, "batch-"+string(rune('0'+issueCount)), 3)
		case queryPath:
			queryCount++
			authorizations = append(authorizations, r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			if queryCount == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"code": "TOKEN_EXPIRED", "message": "expired", "retryable": false, "details": map[string]any{},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id": r.Header.Get("X-Request-Id"), "status": "ok", "answer_context": map[string]any{"summary": "ok"}, "sources": []any{},
				"usage": map[string]any{"cost_credits": 1, "token_type": "info.basic"},
			})
		}
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.RetryBaseDelay = time.Millisecond
	client, err := NewClient(cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.retryJitter = func(delay time.Duration) time.Duration { return delay }
	if _, err := client.Query(context.Background(), QueryRequest{Query: testQuery}); err != nil {
		t.Fatal(err)
	}
	if issueCount != 2 || queryCount != 2 {
		t.Fatalf("unexpected request counts: issue=%d query=%d", issueCount, queryCount)
	}
	if authorizations[0] == authorizations[1] || !strings.Contains(authorizations[1], "batch-2") {
		t.Fatal("TOKEN_EXPIRED retry did not use a newly issued batch")
	}
}

func TestClientDoesNotRetryAmbiguousTokenIssueTimeout(t *testing.T) {
	var issueCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != issueTokensPath {
			t.Error("query should not run when token issuance times out")
			return
		}
		issueCount.Add(1)
		time.Sleep(40 * time.Millisecond)
		writeIssuedTokens(t, w, "late-batch", 3)
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.RequestTimeout = 10 * time.Millisecond
	client, err := NewClient(cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Query(context.Background(), QueryRequest{Query: testQuery})
	if err == nil {
		t.Fatal("expected token issue timeout")
	}
	if issueCount.Load() != 1 {
		t.Fatalf("ambiguous token issue attempts = %d, want 1", issueCount.Load())
	}
}
