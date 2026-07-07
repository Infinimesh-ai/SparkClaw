package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const (
	defaultParallelFreeEndpoint = "https://search.parallel.ai/mcp"
	mcpProtocolVersion          = "2025-06-18"
	parallelFreeMaxQueries      = 5
	parallelFreeMaxQueryChars   = 200
	parallelFreeMaxResults      = 40
)

type ParallelFreeAdapter struct {
	cfg    config.ParallelWebSearchConfig
	client *http.Client
}

func NewParallelFreeAdapter(cfg config.ParallelWebSearchConfig, client *http.Client) ParallelFreeAdapter {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return ParallelFreeAdapter{cfg: cfg, client: client}
}

func (a ParallelFreeAdapter) Search(ctx context.Context, request Request) (Result, error) {
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return Result{}, errors.New("web.search query cannot be empty")
	}
	maxResults := request.MaxResults
	if maxResults <= 0 {
		maxResults = a.cfg.MaxResults
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	maxResults = clampInt(maxResults, 1, parallelFreeMaxResults)
	endpoint := strings.TrimSpace(a.cfg.BaseURL)
	if endpoint == "" {
		endpoint = defaultParallelFreeEndpoint
	}
	start := time.Now()
	payload, err := a.runSearch(ctx, parallelFreeSearchRequest{
		Endpoint:      endpoint,
		Objective:     query,
		SearchQueries: normalizeSearchQueries([]string{query}),
		MaxResults:    maxResults,
	})
	if err != nil {
		return Result{}, err
	}
	items := parallelPayloadToItems(payload, maxResults)
	citations := citationsFromItems(items)
	answer := parallelAnswer(query, items)
	if len(items) == 0 && strings.TrimSpace(payload.Error) != "" {
		answer = strings.TrimSpace(payload.Message)
		if answer == "" {
			answer = payload.Error
		}
	}
	return Result{
		Query:     query,
		Answer:    answer,
		Provider:  "parallel-free",
		Count:     len(items),
		Results:   items,
		Citations: citations,
		TookMS:    time.Since(start).Milliseconds(),
		Untrusted: true,
	}, nil
}

type parallelFreeSearchRequest struct {
	Endpoint      string
	Objective     string
	SearchQueries []string
	MaxResults    int
}

func (a ParallelFreeAdapter) runSearch(ctx context.Context, request parallelFreeSearchRequest) (parallelMCPPayload, error) {
	if len(request.SearchQueries) == 0 {
		return parallelMCPPayload{}, errors.New("invalid_search_queries")
	}
	initID := newRequestID()
	initEnvelope, sessionID, protocolVersion, err := a.postMCP(ctx, request.Endpoint, "", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      initID,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "sparkclaw",
				"version": "0.1",
			},
		},
	}, initID)
	if err != nil {
		return parallelMCPPayload{}, err
	}
	if version := stringFromMap(initEnvelope.Result, "protocolVersion"); version != "" {
		protocolVersion = version
	}
	if protocolVersion == "" {
		protocolVersion = mcpProtocolVersion
	}
	_, _, _, _ = a.postMCP(ctx, request.Endpoint, sessionID, protocolVersion, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}, "")

	callID := newRequestID()
	callEnvelope, _, _, err := a.postMCP(ctx, request.Endpoint, sessionID, protocolVersion, map[string]any{
		"jsonrpc": "2.0",
		"id":      callID,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "web_search",
			"arguments": map[string]any{
				"objective":      request.Objective,
				"search_queries": request.SearchQueries,
				"session_id":     sessionIDOrFallback(sessionID),
			},
		},
	}, callID)
	if err != nil {
		return parallelMCPPayload{}, err
	}
	return extractParallelPayload(callEnvelope, request.MaxResults)
}

func (a ParallelFreeAdapter) postMCP(ctx context.Context, endpoint, sessionID, protocolVersion string, body map[string]any, requestID string) (mcpEnvelope, string, string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return mcpEnvelope{}, "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return mcpEnvelope{}, "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "SparkClaw/0.1 parallel-free-web-search")
	if strings.TrimSpace(sessionID) != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if strings.TrimSpace(protocolVersion) != "" {
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return mcpEnvelope{}, "", "", err
	}
	defer resp.Body.Close()
	rawBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcpEnvelope{}, "", "", fmt.Errorf("parallel-free MCP error: HTTP %d", resp.StatusCode)
	}
	if readErr != nil {
		return mcpEnvelope{}, "", "", readErr
	}
	return selectMCPEnvelope(string(rawBody), requestID), resp.Header.Get("Mcp-Session-Id"), protocolVersion, nil
}

type mcpEnvelope struct {
	ID     any            `json:"id,omitempty"`
	Result map[string]any `json:"result,omitempty"`
	Error  any            `json:"error,omitempty"`
}

type parallelMCPPayload struct {
	SearchID string                 `json:"search_id"`
	Results  []parallelResult       `json:"results"`
	Warnings []any                  `json:"warnings,omitempty"`
	Usage    []any                  `json:"usage,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Message  string                 `json:"message,omitempty"`
	Raw      map[string]interface{} `json:"-"`
}

type parallelResult struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Excerpts    []string `json:"excerpts"`
	Description string   `json:"description"`
	PublishDate string   `json:"publish_date"`
}

func selectMCPEnvelope(text, requestID string) mcpEnvelope {
	messages := parseMCPMessages(text)
	fallback := mcpEnvelope{}
	for _, msg := range messages {
		if msg.Result == nil && msg.Error == nil {
			continue
		}
		if requestID != "" && fmt.Sprint(msg.ID) == requestID {
			return msg
		}
		fallback = msg
	}
	return fallback
}

func parseMCPMessages(text string) []mcpEnvelope {
	body := strings.TrimSpace(text)
	if body == "" {
		return nil
	}
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
		return decodeMCPJSON(body)
	}
	out := []mcpEnvelope{}
	dataLines := []string{}
	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		out = append(out, decodeMCPJSON(strings.Join(dataLines, "\n"))...)
		dataLines = nil
	}
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
		}
	}
	flush()
	return out
}

func decodeMCPJSON(text string) []mcpEnvelope {
	var one mcpEnvelope
	if err := json.Unmarshal([]byte(text), &one); err == nil && (one.Result != nil || one.Error != nil || one.ID != nil) {
		return []mcpEnvelope{one}
	}
	var many []mcpEnvelope
	if err := json.Unmarshal([]byte(text), &many); err == nil {
		return many
	}
	return nil
}

func extractParallelPayload(envelope mcpEnvelope, maxResults int) (parallelMCPPayload, error) {
	if envelope.Error != nil {
		return parallelMCPPayload{}, fmt.Errorf("parallel-free MCP error: %v", envelope.Error)
	}
	result := envelope.Result
	if result == nil {
		return parallelMCPPayload{}, errors.New("parallel-free MCP returned no result")
	}
	if isErr, _ := result["isError"].(bool); isErr {
		return parallelMCPPayload{}, fmt.Errorf("parallel-free MCP tool error: %v", result)
	}
	if structured, ok := result["structuredContent"].(map[string]any); ok {
		return decodeParallelPayload(structured, maxResults)
	}
	if content, ok := result["content"].([]any); ok {
		for _, block := range content {
			obj, ok := block.(map[string]any)
			if !ok || stringFromMap(obj, "type") != "text" {
				continue
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(stringFromMap(obj, "text")), &parsed); err == nil {
				return decodeParallelPayload(parsed, maxResults)
			}
		}
	}
	return parallelMCPPayload{}, errors.New("parallel-free MCP returned no parseable content")
}

func decodeParallelPayload(raw map[string]any, maxResults int) (parallelMCPPayload, error) {
	bytes, err := json.Marshal(raw)
	if err != nil {
		return parallelMCPPayload{}, err
	}
	var payload parallelMCPPayload
	if err := json.Unmarshal(bytes, &payload); err != nil {
		return parallelMCPPayload{}, err
	}
	if maxResults > 0 && len(payload.Results) > maxResults {
		payload.Results = payload.Results[:maxResults]
	}
	payload.Raw = raw
	return payload, nil
}

func parallelPayloadToItems(payload parallelMCPPayload, maxResults int) []Item {
	items := []Item{}
	for _, result := range payload.Results {
		url := normalizeHTTPURL(result.URL)
		if url == "" {
			continue
		}
		snippet := strings.TrimSpace(result.Description)
		if snippet == "" && len(result.Excerpts) > 0 {
			snippet = strings.Join(result.Excerpts, "\n\n")
		}
		items = append(items, Item{
			Title:       strings.TrimSpace(result.Title),
			URL:         url,
			Snippet:     strings.TrimSpace(snippet),
			Source:      hostFromURL(url),
			PublishedAt: strings.TrimSpace(result.PublishDate),
		})
		if maxResults > 0 && len(items) >= maxResults {
			break
		}
	}
	return items
}

func parallelAnswer(query string, items []Item) string {
	if len(items) == 0 {
		return "没有找到可靠的联网搜索结果。"
	}
	lines := []string{"找到以下相关网页结果："}
	for i, item := range items {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = item.URL
		}
		line := fmt.Sprintf("%d. %s\n%s", i+1, title, item.URL)
		if strings.TrimSpace(item.Snippet) != "" {
			line += "\n" + trimText(item.Snippet, 360)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n\n")
}

func citationsFromItems(items []Item) []string {
	citations := []string{}
	for _, item := range items {
		citations = uniqueAppend(citations, item.URL)
	}
	return citations
}

func normalizeSearchQueries(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len([]rune(value)) > parallelFreeMaxQueryChars {
			value = string([]rune(value)[:parallelFreeMaxQueryChars])
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if len(out) >= parallelFreeMaxQueries {
			break
		}
	}
	return out
}

func newRequestID() string {
	return fmt.Sprintf("sparkclaw-%d", time.Now().UnixNano())
}

func sessionIDOrFallback(value string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return newRequestID()
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func normalizeHTTPURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	if parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func hostFromURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func uniqueAppend(base []string, values ...string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range base {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func trimText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "..."
}
