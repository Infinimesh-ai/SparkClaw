package modelrouter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type Router struct {
	cfg    config.Config
	client *http.Client
}

type Task struct {
	Message        string
	Risk           app.RiskLevel
	NeedsCode      bool
	NeedsTerminal  bool
	ToolFailures   int
	RequestedDeep  bool
	NeedsSummarize bool
}

type ChatResult struct {
	Lane           string `json:"lane"`
	Profile        string `json:"profile"`
	Model          string `json:"model"`
	Content        string `json:"content"`
	Mock           bool   `json:"mock"`
	Fallback       bool   `json:"fallback,omitempty"`
	ErrorNote      string `json:"error_note,omitempty"`
	PromptTokens   int    `json:"prompt_tokens,omitempty"`
	ResponseTokens int    `json:"response_tokens,omitempty"`
	TotalTokens    int    `json:"total_tokens,omitempty"`
}

type EmbeddingResult struct {
	Lane         string      `json:"lane"`
	Profile      string      `json:"profile"`
	Model        string      `json:"model"`
	Vectors      [][]float32 `json:"vectors"`
	Mock         bool        `json:"mock"`
	PromptTokens int         `json:"prompt_tokens,omitempty"`
	TotalTokens  int         `json:"total_tokens,omitempty"`
}

type RerankResult struct {
	Lane         string         `json:"lane"`
	Profile      string         `json:"profile"`
	Model        string         `json:"model"`
	Results      []RerankScored `json:"results"`
	Mock         bool           `json:"mock"`
	PromptTokens int            `json:"prompt_tokens,omitempty"`
	TotalTokens  int            `json:"total_tokens,omitempty"`
}

type GuardResult struct {
	Lane           string   `json:"lane"`
	Profile        string   `json:"profile"`
	Model          string   `json:"model"`
	Verdict        string   `json:"verdict"`
	Categories     []string `json:"categories,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	Mock           bool     `json:"mock"`
	PromptTokens   int      `json:"prompt_tokens,omitempty"`
	ResponseTokens int      `json:"response_tokens,omitempty"`
	TotalTokens    int      `json:"total_tokens,omitempty"`
}

type RerankScored struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

type tokenUsage struct {
	PromptTokens   int
	ResponseTokens int
	TotalTokens    int
}

func New(cfg config.Config) Router {
	timeout := time.Duration(cfg.Model.HTTPTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	return Router{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

func (r Router) ChooseModel(task Task) config.ModelProfile {
	if task.Risk == app.RiskDangerous || task.NeedsCode || task.NeedsTerminal || task.ToolFailures > 0 || task.RequestedDeep {
		return r.cfg.Model.Deep
	}
	return r.cfg.Model.Fast
}

func (r Router) LaneFor(profile config.ModelProfile) string {
	if profile.Name == r.cfg.Model.Deep.Name {
		return "deep"
	}
	if profile.Name == r.cfg.Model.Embedding.Name {
		return "embedding"
	}
	if profile.Name == r.cfg.Model.Reranker.Name {
		return "reranker"
	}
	if profile.Name == r.cfg.Model.Guard.Name {
		return "guard"
	}
	return "fast"
}

func (r Router) Chat(ctx context.Context, task Task, system, user string) (ChatResult, error) {
	profile := r.ChooseModel(task)
	return r.chatWithProfile(ctx, profile, system, user, true)
}

func (r Router) ChatWithProfile(ctx context.Context, profileName, system, user string) (ChatResult, error) {
	profile, err := r.Profile(profileName)
	if err != nil {
		return ChatResult{}, err
	}
	return r.chatWithProfile(ctx, profile, system, user, false)
}

func (r Router) Profile(name string) (config.ModelProfile, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "auto", "fast", strings.ToLower(r.cfg.Model.Fast.Name), strings.ToLower(modelID(r.cfg.Model.Fast)):
		return r.cfg.Model.Fast, nil
	case "deep", strings.ToLower(r.cfg.Model.Deep.Name), strings.ToLower(modelID(r.cfg.Model.Deep)):
		return r.cfg.Model.Deep, nil
	default:
		return config.ModelProfile{}, fmt.Errorf("unknown chat profile %q", name)
	}
}

func (r Router) chatWithProfile(ctx context.Context, profile config.ModelProfile, system, user string, allowFallback bool) (ChatResult, error) {
	lane := r.LaneFor(profile)
	if r.cfg.Model.Mock {
		content := mockResponse(lane, user)
		promptTokens := estimateTokens(system) + estimateTokens(user)
		responseTokens := estimateTokens(content)
		return ChatResult{
			Lane:           lane,
			Profile:        profile.Name,
			Model:          modelID(profile),
			Content:        content,
			Mock:           true,
			PromptTokens:   promptTokens,
			ResponseTokens: responseTokens,
			TotalTokens:    promptTokens + responseTokens,
		}, nil
	}
	content, usage, err := r.chatCompletions(ctx, profile, system, user)
	if err != nil {
		if allowFallback && lane == "fast" && r.cfg.Model.Deep.BaseURL != "" {
			deep := r.cfg.Model.Deep
			deepContent, deepUsage, deepErr := r.chatCompletions(ctx, deep, system, user)
			if deepErr == nil {
				return ChatResult{
					Lane:           "deep",
					Profile:        deep.Name,
					Model:          modelID(deep),
					Content:        deepContent,
					Mock:           false,
					Fallback:       true,
					ErrorNote:      err.Error(),
					PromptTokens:   deepUsage.PromptTokens,
					ResponseTokens: deepUsage.ResponseTokens,
					TotalTokens:    deepUsage.TotalTokens,
				}, nil
			}
		}
		return ChatResult{}, err
	}
	return ChatResult{
		Lane:           lane,
		Profile:        profile.Name,
		Model:          modelID(profile),
		Content:        content,
		Mock:           false,
		PromptTokens:   usage.PromptTokens,
		ResponseTokens: usage.ResponseTokens,
		TotalTokens:    usage.TotalTokens,
	}, nil
}

func (r Router) Embed(ctx context.Context, inputs []string) (EmbeddingResult, error) {
	if len(inputs) == 0 {
		return EmbeddingResult{}, errors.New("embedding inputs cannot be empty")
	}
	profile := r.cfg.Model.Embedding
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = "sparkclaw-embedding"
	}
	if r.cfg.Model.Mock {
		promptTokens := estimateTokenList(inputs)
		return EmbeddingResult{
			Lane:         "embedding",
			Profile:      profile.Name,
			Model:        modelID(profile),
			Vectors:      mockEmbeddings(inputs),
			Mock:         true,
			PromptTokens: promptTokens,
			TotalTokens:  promptTokens,
		}, nil
	}
	vectors, usage, err := r.embeddings(ctx, profile, inputs)
	if err != nil {
		return EmbeddingResult{}, err
	}
	return EmbeddingResult{
		Lane:         "embedding",
		Profile:      profile.Name,
		Model:        modelID(profile),
		Vectors:      vectors,
		Mock:         false,
		PromptTokens: usage.PromptTokens,
		TotalTokens:  usage.TotalTokens,
	}, nil
}

func (r Router) Rerank(ctx context.Context, query string, documents []string, topN int) (RerankResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return RerankResult{}, errors.New("rerank query cannot be empty")
	}
	if len(documents) == 0 {
		return RerankResult{}, errors.New("rerank documents cannot be empty")
	}
	if topN <= 0 || topN > len(documents) {
		topN = len(documents)
	}
	profile := r.cfg.Model.Reranker
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = "sparkclaw-reranker"
	}
	if r.cfg.Model.Mock {
		promptTokens := estimateTokens(query) + estimateTokenList(documents)
		return RerankResult{
			Lane:         "reranker",
			Profile:      profile.Name,
			Model:        modelID(profile),
			Results:      mockRerank(query, documents, topN),
			Mock:         true,
			PromptTokens: promptTokens,
			TotalTokens:  promptTokens,
		}, nil
	}
	results, usage, err := r.rerank(ctx, profile, query, documents, topN)
	if err != nil {
		return RerankResult{}, err
	}
	return RerankResult{
		Lane:         "reranker",
		Profile:      profile.Name,
		Model:        modelID(profile),
		Results:      results,
		Mock:         false,
		PromptTokens: usage.PromptTokens,
		TotalTokens:  usage.TotalTokens,
	}, nil
}

func (r Router) Guard(ctx context.Context, content string) (GuardResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return GuardResult{}, errors.New("guard content cannot be empty")
	}
	profile := r.cfg.Model.Guard
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = "sparkclaw-guard"
	}
	if r.cfg.Model.Mock {
		promptTokens := estimateTokens(content)
		result := mockGuard(content)
		result.Lane = "guard"
		result.Profile = profile.Name
		result.Model = modelID(profile)
		result.Mock = true
		result.PromptTokens = promptTokens
		result.ResponseTokens = estimateTokens(result.Verdict + " " + result.Reason + " " + strings.Join(result.Categories, " "))
		result.TotalTokens = result.PromptTokens + result.ResponseTokens
		return result, nil
	}
	result, usage, err := r.guard(ctx, profile, content)
	if err != nil {
		result := mockGuard(content)
		result.Lane = "guard"
		result.Profile = profile.Name
		result.Model = modelID(profile)
		result.Mock = true
		result.PromptTokens = estimateTokens(content)
		result.ResponseTokens = estimateTokens(result.Verdict + " " + result.Reason + " " + strings.Join(result.Categories, " "))
		result.TotalTokens = result.PromptTokens + result.ResponseTokens
		if strings.TrimSpace(result.Reason) == "" {
			result.Reason = "External guard unavailable; used local heuristic fallback."
		} else {
			result.Reason = result.Reason + " External guard unavailable; used local heuristic fallback."
		}
		return result, nil
	}
	result.Lane = "guard"
	result.Profile = profile.Name
	result.Model = modelID(profile)
	result.Mock = false
	result.PromptTokens = usage.PromptTokens
	result.ResponseTokens = usage.ResponseTokens
	result.TotalTokens = usage.TotalTokens
	return result, nil
}

func (r Router) chatCompletions(ctx context.Context, profile config.ModelProfile, system, user string) (string, tokenUsage, error) {
	if profile.BaseURL == "" {
		return "", tokenUsage{}, errors.New("model base_url is empty")
	}
	endpoint := strings.TrimRight(profile.BaseURL, "/") + "/chat/completions"
	body := map[string]any{
		"model": modelID(profile),
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.2,
	}
	if r.cfg.Model.DisableThinking {
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	}
	if profile.MaxTokens > 0 {
		body["max_tokens"] = profile.MaxTokens
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", tokenUsage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", tokenUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(getenv("OPENAI_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "", tokenUsage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", tokenUsage{}, fmt.Errorf("model router received HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content   *string `json:"content"`
				Reasoning string  `json:"reasoning"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", tokenUsage{}, err
	}
	if len(decoded.Choices) == 0 {
		return "", tokenUsage{}, errors.New("model response had no choices")
	}
	content := ""
	if decoded.Choices[0].Message.Content != nil {
		content = strings.TrimSpace(*decoded.Choices[0].Message.Content)
	}
	if content == "" && strings.TrimSpace(decoded.Choices[0].Message.Reasoning) != "" {
		return "", tokenUsage{}, fmt.Errorf("model response contained reasoning but no assistant content (finish_reason=%s)", decoded.Choices[0].FinishReason)
	}
	if content == "" {
		return "", tokenUsage{}, fmt.Errorf("model response had empty assistant content (finish_reason=%s)", decoded.Choices[0].FinishReason)
	}
	usage := tokenUsage{
		PromptTokens:   decoded.Usage.PromptTokens,
		ResponseTokens: decoded.Usage.CompletionTokens,
		TotalTokens:    decoded.Usage.TotalTokens,
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = estimateTokens(system) + estimateTokens(user)
	}
	if usage.ResponseTokens == 0 {
		usage.ResponseTokens = estimateTokens(content)
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.ResponseTokens
	}
	return content, usage, nil
}

func (r Router) rerank(ctx context.Context, profile config.ModelProfile, query string, documents []string, topN int) ([]RerankScored, tokenUsage, error) {
	if profile.BaseURL == "" {
		return nil, tokenUsage{}, errors.New("reranker base_url is empty")
	}
	endpoint := strings.TrimRight(profile.BaseURL, "/") + "/rerank"
	body := map[string]any{
		"model":     modelID(profile),
		"query":     query,
		"documents": documents,
		"top_n":     topN,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, tokenUsage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, tokenUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(getenv("OPENAI_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, tokenUsage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusNotFound {
			return r.generativeScoreRerank(ctx, profile, query, documents, topN)
		}
		return nil, tokenUsage{}, fmt.Errorf("reranker received HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Results []struct {
			Index          int      `json:"index"`
			RelevanceScore *float64 `json:"relevance_score"`
			Score          *float64 `json:"score"`
		} `json:"results"`
		Data []struct {
			Index          int      `json:"index"`
			RelevanceScore *float64 `json:"relevance_score"`
			Score          *float64 `json:"score"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, tokenUsage{}, err
	}
	rawResults := decoded.Results
	if len(rawResults) == 0 {
		rawResults = decoded.Data
	}
	if len(rawResults) == 0 {
		return nil, tokenUsage{}, errors.New("reranker response had no results")
	}
	results := make([]RerankScored, 0, len(rawResults))
	for _, item := range rawResults {
		if item.Index < 0 || item.Index >= len(documents) {
			continue
		}
		score := 0.0
		if item.RelevanceScore != nil {
			score = *item.RelevanceScore
		} else if item.Score != nil {
			score = *item.Score
		}
		results = append(results, RerankScored{Index: item.Index, Score: score})
	}
	if len(results) == 0 {
		return nil, tokenUsage{}, errors.New("reranker response did not reference provided documents")
	}
	usage := tokenUsage{PromptTokens: decoded.Usage.PromptTokens, TotalTokens: decoded.Usage.TotalTokens}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = estimateTokens(query) + estimateTokenList(documents)
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens
	}
	return results, usage, nil
}

func (r Router) generativeScoreRerank(ctx context.Context, profile config.ModelProfile, query string, documents []string, topN int) ([]RerankScored, tokenUsage, error) {
	endpoint := strings.TrimSuffix(strings.TrimRight(profile.BaseURL, "/"), "/v1") + "/generative_scoring"
	items := make([]string, 0, len(documents))
	for _, document := range documents {
		items = append(items, "Document: "+document+"\nAnswer:")
	}
	body := map[string]any{
		"model":           modelID(profile),
		"query":           "Is this document relevant to the query? Answer Yes or No.\nQuery: " + query,
		"items":           items,
		"label_token_ids": []int{7414, 2308},
		"apply_softmax":   true,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, tokenUsage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, tokenUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(getenv("OPENAI_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, tokenUsage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, tokenUsage{}, fmt.Errorf("reranker generative scoring received HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Data []struct {
			Index int     `json:"index"`
			Score float64 `json:"score"`
		} `json:"data"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, tokenUsage{}, err
	}
	results := make([]RerankScored, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		if item.Index < 0 || item.Index >= len(documents) {
			continue
		}
		results = append(results, RerankScored{Index: item.Index, Score: item.Score})
	}
	if len(results) == 0 {
		return nil, tokenUsage{}, errors.New("reranker generative scoring response had no usable scores")
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if topN > 0 && topN < len(results) {
		results = results[:topN]
	}
	usage := tokenUsage{
		PromptTokens:   decoded.Usage.PromptTokens,
		ResponseTokens: decoded.Usage.CompletionTokens,
		TotalTokens:    decoded.Usage.TotalTokens,
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = estimateTokens(query) + estimateTokenList(documents)
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.ResponseTokens
	}
	return results, usage, nil
}

func (r Router) embeddings(ctx context.Context, profile config.ModelProfile, inputs []string) ([][]float32, tokenUsage, error) {
	if profile.BaseURL == "" {
		return nil, tokenUsage{}, errors.New("embedding base_url is empty")
	}
	endpoint := strings.TrimRight(profile.BaseURL, "/") + "/embeddings"
	body := map[string]any{
		"model": modelID(profile),
		"input": inputs,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, tokenUsage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, tokenUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(getenv("OPENAI_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, tokenUsage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, tokenUsage{}, fmt.Errorf("embedding router received HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, tokenUsage{}, err
	}
	if len(decoded.Data) == 0 {
		return nil, tokenUsage{}, errors.New("embedding response had no data")
	}
	vectors := make([][]float32, len(inputs))
	for _, item := range decoded.Data {
		if item.Index < 0 || item.Index >= len(inputs) {
			continue
		}
		vectors[item.Index] = item.Embedding
	}
	for i, vector := range vectors {
		if len(vector) == 0 {
			return nil, tokenUsage{}, fmt.Errorf("embedding response missing vector for input %d", i)
		}
	}
	usage := tokenUsage{PromptTokens: decoded.Usage.PromptTokens, TotalTokens: decoded.Usage.TotalTokens}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = estimateTokenList(inputs)
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens
	}
	return vectors, usage, nil
}

func (r Router) guard(ctx context.Context, profile config.ModelProfile, content string) (GuardResult, tokenUsage, error) {
	if profile.BaseURL == "" {
		return GuardResult{}, tokenUsage{}, errors.New("guard base_url is empty")
	}
	system := "Classify the user content for SparkClaw safety. Return compact JSON with verdict allow, review, or block; categories array; and reason."
	rawContent, usage, err := r.chatCompletions(ctx, profile, system, content)
	if err != nil {
		return GuardResult{}, tokenUsage{}, err
	}
	return parseGuardContent(rawContent), usage, nil
}

func modelID(profile config.ModelProfile) string {
	if strings.TrimSpace(profile.Model) != "" {
		return profile.Model
	}
	return profile.Name
}

func mockResponse(lane, user string) string {
	lower := strings.ToLower(user)
	switch {
	case strings.Contains(lower, "approval") || strings.Contains(lower, "shell") || strings.Contains(lower, "delete"):
		return "I will keep this behind SparkClaw approval policy and stage the action instead of executing it."
	case strings.Contains(lower, "remember") || strings.Contains(lower, "记住"):
		return "I will create a memory candidate for owner review."
	case strings.Contains(lower, "search") || strings.Contains(lower, "找"):
		return "I will search the allowed workspace and report only observed results."
	case lane == "deep":
		return "I will use the deep lane because this task has higher risk or complexity."
	default:
		return "I will use the fast lane for a bounded local-first response."
	}
}

func mockGuard(content string) GuardResult {
	lower := strings.ToLower(content)
	categories := []string{}
	verdict := "allow"
	reason := "No guard trigger matched."
	if containsAnyTerm(lower, "ignore previous instructions", "ignore all previous instructions", "developer message", "system prompt", "jailbreak", "bypass policy") {
		verdict = "review"
		categories = append(categories, "prompt_injection")
		reason = "Content appears to request instruction override or policy bypass."
	}
	if containsAnyTerm(lower, "api_key", "password", "ssh_key", "secret", "token") && containsAnyTerm(lower, "send", "exfiltrate", "leak", "print", "reveal") {
		verdict = "block"
		categories = append(categories, "secret_exfiltration")
		reason = "Content appears to request secret disclosure or exfiltration."
	}
	if containsAnyTerm(lower, "rm -rf /", "delete everything", "format disk") {
		if verdict != "block" {
			verdict = "review"
		}
		categories = append(categories, "destructive_action")
		reason = "Content references destructive host or file operations."
	}
	return GuardResult{
		Verdict:    verdict,
		Categories: uniqueStrings(categories),
		Reason:     reason,
	}
}

func parseGuardContent(content string) GuardResult {
	content = strings.TrimSpace(content)
	result := GuardResult{
		Verdict: "review",
		Reason:  content,
	}
	var decoded struct {
		Verdict    string   `json:"verdict"`
		Categories []string `json:"categories"`
		Reason     string   `json:"reason"`
	}
	if err := json.Unmarshal([]byte(content), &decoded); err == nil {
		if verdict := normalizeGuardVerdict(decoded.Verdict); verdict != "" {
			result.Verdict = verdict
		}
		result.Categories = uniqueStrings(decoded.Categories)
		result.Reason = strings.TrimSpace(decoded.Reason)
		if result.Reason == "" {
			result.Reason = content
		}
		return result
	}
	lower := strings.ToLower(content)
	for _, verdict := range []string{"allow", "review", "block"} {
		if strings.Contains(lower, verdict) {
			result.Verdict = verdict
			break
		}
	}
	return result
}

func normalizeGuardVerdict(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "allow", "allowed", "safe":
		return "allow"
	case "review", "needs_review", "needs-review", "warn":
		return "review"
	case "block", "blocked", "deny", "unsafe":
		return "block"
	default:
		return ""
	}
}

func mockEmbeddings(inputs []string) [][]float32 {
	vectors := make([][]float32, 0, len(inputs))
	for _, input := range inputs {
		vector := make([]float32, 64)
		terms := strings.Fields(strings.ToLower(input))
		if len(terms) == 0 {
			terms = []string{input}
		}
		for _, term := range terms {
			sum := sha256.Sum256([]byte(term))
			idx := int(binary.BigEndian.Uint16(sum[:2]) % uint16(len(vector)))
			sign := float32(1)
			if sum[2]%2 == 1 {
				sign = -1
			}
			vector[idx] += sign
		}
		normalize(vector)
		vectors = append(vectors, vector)
	}
	return vectors
}

func mockRerank(query string, documents []string, topN int) []RerankScored {
	queryTerms := strings.Fields(strings.ToLower(query))
	results := make([]RerankScored, 0, len(documents))
	for index, document := range documents {
		lower := strings.ToLower(document)
		score := float64(len(queryTerms)) * 0.01
		for _, term := range queryTerms {
			if strings.Contains(lower, term) {
				score += 1
			}
		}
		results = append(results, RerankScored{Index: index, Score: score})
	}
	slices.SortFunc(results, func(a, b RerankScored) int {
		if a.Score != b.Score {
			if a.Score > b.Score {
				return -1
			}
			return 1
		}
		return a.Index - b.Index
	})
	if len(results) > topN {
		results = results[:topN]
	}
	return results
}

func containsAnyTerm(value string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(value, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
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

func estimateTokenList(values []string) int {
	total := 0
	for _, value := range values {
		total += estimateTokens(value)
	}
	return total
}

func estimateTokens(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	words := len(strings.Fields(value))
	runes := len([]rune(value))
	charEstimate := (runes + 3) / 4
	if charEstimate > words {
		return charEstimate
	}
	if words == 0 {
		return 1
	}
	return words
}

func normalize(vector []float32) {
	var sum float32
	for _, value := range vector {
		sum += value * value
	}
	if sum == 0 {
		return
	}
	scale := float32(math.Sqrt(float64(sum)))
	for i := range vector {
		vector[i] /= scale
	}
}

func getenv(key string) string {
	// Wrapped for tests and to keep all external model access in this package.
	return os.Getenv(key)
}
