package modelrouter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

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
	LaneHint       string
	TaskType       string
	EvidenceNeed   string
	ToolMode       string
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

type ImageInput struct {
	Path        string
	Content     []byte
	ContentType string
}

type ModelStreamEvent struct {
	Type           string `json:"type"`
	SessionID      string `json:"session_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	SpanID         string `json:"span_id,omitempty"`
	Text           string `json:"text,omitempty"`
	ToolCallID     string `json:"tool_call_id,omitempty"`
	ToolName       string `json:"tool_name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
	Arguments      any    `json:"arguments,omitempty"`
	Error          string `json:"error,omitempty"`
}

type StreamHandler func(ModelStreamEvent) error

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
	switch strings.ToLower(strings.TrimSpace(task.LaneHint)) {
	case "deep":
		return r.cfg.Model.Deep
	case "fast":
		return r.cfg.Model.Fast
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

func (r Router) ChatWithImage(ctx context.Context, profileName, system, user string, image ImageInput) (ChatResult, error) {
	return r.ChatWithImageMaxTokens(ctx, profileName, system, user, image, 0)
}

func (r Router) ChatWithImageMaxTokens(ctx context.Context, profileName, system, user string, image ImageInput, maxTokens int) (ChatResult, error) {
	profile, err := r.Profile(profileName)
	if err != nil {
		return ChatResult{}, err
	}
	if maxTokens > 0 && (profile.MaxTokens <= 0 || maxTokens < profile.MaxTokens) {
		profile.MaxTokens = maxTokens
	}
	return r.chatWithImageProfile(ctx, profile, system, user, image)
}

func (r Router) ChatStreamWithProfile(ctx context.Context, profileName, system, user string, emit StreamHandler) (ChatResult, error) {
	profile, err := r.Profile(profileName)
	if err != nil {
		return ChatResult{}, err
	}
	return r.chatStreamWithProfile(ctx, profile, system, user, emit)
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

func (r Router) chatWithImageProfile(ctx context.Context, profile config.ModelProfile, system, user string, image ImageInput) (ChatResult, error) {
	lane := r.LaneFor(profile)
	if len(image.Content) == 0 {
		return ChatResult{}, errors.New("image content cannot be empty")
	}
	if strings.TrimSpace(image.ContentType) == "" {
		image.ContentType = "application/octet-stream"
	}
	if r.cfg.Model.Mock {
		content := "Mock image inspection: image content was received and can be described by the multimodal model."
		promptTokens := estimateTokens(system) + estimateTokens(user) + len(image.Content)/768
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
	content, usage, err := r.chatCompletionsWithImage(ctx, profile, system, user, image)
	if err != nil {
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

func (r Router) chatStreamWithProfile(ctx context.Context, profile config.ModelProfile, system, user string, emit StreamHandler) (ChatResult, error) {
	lane := r.LaneFor(profile)
	if emit == nil {
		emit = func(ModelStreamEvent) error { return nil }
	}
	if r.cfg.Model.Mock {
		content := mockResponse(lane, user)
		for _, chunk := range streamChunks(content, 12) {
			if err := emit(ModelStreamEvent{Type: "text_delta", Text: chunk}); err != nil {
				return ChatResult{}, err
			}
		}
		_ = emit(ModelStreamEvent{Type: "done"})
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
	content, usage, err := r.chatCompletionsStream(ctx, profile, system, user, emit)
	if err != nil {
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
		return "", tokenUsage{}, modelHTTPError(resp, profile, endpoint, raw, system, user)
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

func (r Router) chatCompletionsWithImage(ctx context.Context, profile config.ModelProfile, system, user string, image ImageInput) (string, tokenUsage, error) {
	if profile.BaseURL == "" {
		return "", tokenUsage{}, errors.New("model base_url is empty")
	}
	endpoint := strings.TrimRight(profile.BaseURL, "/") + "/chat/completions"
	dataURL := "data:" + image.ContentType + ";base64," + base64.StdEncoding.EncodeToString(image.Content)
	body := map[string]any{
		"model": modelID(profile),
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": user},
					{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
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
		return "", tokenUsage{}, modelHTTPError(resp, profile, endpoint, raw, system, user)
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
		usage.PromptTokens = estimateTokens(system) + estimateTokens(user) + len(image.Content)/768
	}
	if usage.ResponseTokens == 0 {
		usage.ResponseTokens = estimateTokens(content)
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.ResponseTokens
	}
	return content, usage, nil
}

func (r Router) chatCompletionsStream(ctx context.Context, profile config.ModelProfile, system, user string, emit StreamHandler) (string, tokenUsage, error) {
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
		"stream":      true,
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
	req.Header.Set("Accept", "text/event-stream")
	if key := strings.TrimSpace(getenv("OPENAI_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "", tokenUsage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", tokenUsage{}, modelHTTPError(resp, profile, endpoint, raw, system, user)
	}
	var content strings.Builder
	usage := tokenUsage{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if err := emit(ModelStreamEvent{Type: "done"}); err != nil {
				return "", tokenUsage{}, err
			}
			break
		}
		delta, deltaUsage, err := parseOpenAIStreamChunk(data)
		if err != nil {
			_ = emit(ModelStreamEvent{Type: "error", Error: err.Error()})
			return "", tokenUsage{}, err
		}
		if deltaUsage.TotalTokens > 0 || deltaUsage.PromptTokens > 0 || deltaUsage.ResponseTokens > 0 {
			usage = deltaUsage
		}
		for _, event := range delta {
			if event.Type == "text_delta" {
				content.WriteString(event.Text)
			}
			if err := emit(event); err != nil {
				return "", tokenUsage{}, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		_ = emit(ModelStreamEvent{Type: "error", Error: err.Error()})
		return "", tokenUsage{}, err
	}
	text := strings.TrimSpace(content.String())
	if text == "" {
		return "", tokenUsage{}, errors.New("model stream had empty assistant content")
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = estimateTokens(system) + estimateTokens(user)
	}
	if usage.ResponseTokens == 0 {
		usage.ResponseTokens = estimateTokens(text)
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.ResponseTokens
	}
	return text, usage, nil
}

func parseOpenAIStreamChunk(data string) ([]ModelStreamEvent, tokenUsage, error) {
	var decoded struct {
		Choices []struct {
			Delta struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &decoded); err != nil {
		return nil, tokenUsage{}, err
	}
	events := []ModelStreamEvent{}
	for _, choice := range decoded.Choices {
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			events = append(events, ModelStreamEvent{Type: "text_delta", Text: *choice.Delta.Content})
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			events = append(events, ModelStreamEvent{
				Type:           "tool_call_delta",
				ToolCallID:     toolCall.ID,
				ToolName:       toolCall.Function.Name,
				ArgumentsDelta: toolCall.Function.Arguments,
			})
		}
	}
	return events, tokenUsage{
		PromptTokens:   decoded.Usage.PromptTokens,
		ResponseTokens: decoded.Usage.CompletionTokens,
		TotalTokens:    decoded.Usage.TotalTokens,
	}, nil
}

func streamChunks(value string, size int) []string {
	if size <= 0 {
		size = 12
	}
	runes := []rune(value)
	chunks := []string{}
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
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
	if strings.Contains(user, "REQUEST_NORMALIZATION_INPUT") {
		if injected := mockInjectedResponse(user, "MOCK_NORMALIZATION_RESPONSE:"); injected != "" {
			return injected
		}
		if marker := strings.Index(user, "Original request JSON:\n"); marker >= 0 {
			rest := user[marker+len("Original request JSON:\n"):]
			if end := strings.IndexByte(rest, '\n'); end >= 0 {
				rest = rest[:end]
			}
			var input map[string]string
			if json.Unmarshal([]byte(rest), &input) == nil {
				encoded, _ := json.Marshal(map[string]string{"canonical_request": input["request"]})
				return string(encoded)
			}
		}
		return `{"canonical_request":""}`
	}
	if injected := mockInjectedResponse(user, "MOCK_DIRECTORY_SELECTION_RESPONSE:"); injected != "" {
		return injected
	}
	if strings.Contains(user, "WORKFLOW_FINAL_ANSWER_REQUEST") {
		if injected := mockInjectedResponse(user, "MOCK_WORKFLOW_FINAL_RESPONSE:"); injected != "" {
			return injected
		}
		if strings.Contains(user, "images.inspect") {
			return "Mock image inspection completed from the workflow evidence."
		}
		return "Mock workflow answer grounded in the completed document evidence."
	}
	if strings.Contains(user, "WORKFLOW_MODEL_ANSWER_REQUEST") {
		if injected := mockInjectedResponse(user, "MOCK_CONVERSATION_RESPONSE:"); injected != "" {
			return injected
		}
		return "I can answer this directly from the current conversation."
	}
	if strings.Contains(user, "INTENT_FUSION_TREE_REPAIR_REQUEST") {
		if injected := mockInjectedResponse(user, "MOCK_INTENT_TREE_REPAIR_RESPONSE:"); injected != "" {
			return injected
		}
		return mockIntentFusionResponse(user)
	}
	if strings.Contains(user, "INTENT_FUSION_TREE_REQUEST") {
		if injected := mockInjectedResponse(user, "MOCK_INTENT_TREE_RESPONSE:"); injected != "" {
			return injected
		}
		return mockIntentFusionResponse(user)
	}
	if injected := mockInjectedResponse(user, "MOCK_TASK_HINT_RESPONSE:"); injected != "" {
		return injected
	}
	if strings.Contains(user, "REACT_OUTPUT_REQUEST") {
		lower := strings.ToLower(user)
		for _, stage := range []struct {
			tool   string
			marker string
		}{
			{"info.query", "MOCK_INFO_QUERY_RESPONSE:"},
			{"weather.structure_payload", "MOCK_WEATHER_STRUCTURE_RESPONSE:"},
			{"media.render_weather_card", "MOCK_WEATHER_RENDER_RESPONSE:"},
		} {
			if strings.Contains(lower, "model-visible tools this workflow stage: "+stage.tool) {
				if injected := mockInjectedResponse(user, stage.marker); injected != "" {
					return injected
				}
			}
		}
	}
	if injected := mockInjectedResponse(user, "MOCK_REACT_RESPONSE:"); injected != "" {
		return injected
	}
	if strings.Contains(user, "REACT_OUTPUT_REQUEST") {
		return mockReActResponse(user)
	}
	if strings.Contains(user, "DIRECTORY_SELECTION_REQUEST") {
		for _, line := range strings.Split(user, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "- entry_id=") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			entryID := strings.TrimPrefix(fields[1], "entry_id=")
			if entryID != "" {
				return `{"entry_id":"` + entryID + `"}`
			}
		}
	}
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

func mockReActResponse(user string) string {
	goal := mockReActGoal(user)
	lowerGoal := strings.ToLower(goal)
	lowerPrompt := strings.ToLower(user)
	if stage := mockBrowserInteractionStage(lowerPrompt); stage != "" {
		return mockBrowserInteractionAction(user, goal, stage)
	}
	switch {
	case strings.Contains(lowerPrompt, "model-visible tools this workflow stage: browser.list_tabs") && !strings.Contains(lowerPrompt, "browser.list_tabs observation"):
		return mockReActAction("browser.list_tabs", map[string]any{})
	case strings.Contains(lowerPrompt, "model-visible tools this workflow stage: browser.focus") && !strings.Contains(lowerPrompt, "browser.focus observation"):
		return mockReActAction("browser.focus", map[string]any{"page_id": mockWorkflowPageID(user)})
	case strings.Contains(lowerPrompt, "model-visible tools this workflow stage: browser.open") && !strings.Contains(lowerPrompt, "browser.open observation"):
		urls := mockURLs(goal)
		if len(urls) > 0 {
			return mockReActAction("browser.open", map[string]any{"url": urls[0]})
		}
	}
	if strings.Contains(lowerPrompt, "previous observation summaries") {
		if strings.Contains(lowerPrompt, "workflow_requirement: source_page_required") && !strings.Contains(lowerPrompt, "browser.read observation") {
			urls := mockURLs(user)
			if len(urls) > 0 {
				return mockReActAction("browser.read", map[string]any{"url": urls[0]})
			}
		}
		if strings.Contains(lowerGoal, "failing test") || strings.Contains(lowerGoal, "failed test") {
			if strings.Contains(lowerPrompt, "files.search observation") {
				return mockReActAction("shell.exec_sandboxed", map[string]any{"command": "npm test"})
			}
			return mockReActAction("files.search", map[string]any{"query": "test"})
		}
		if strings.Contains(lowerGoal, "compare") {
			paths := mockPaths(goal)
			readCount := strings.Count(lowerPrompt, "files.read observation")
			if readCount < len(paths) {
				return mockReActAction("files.read", map[string]any{"path": paths[readCount]})
			}
		}
		if strings.Contains(lowerPrompt, "browser.read observation") && strings.Contains(lowerGoal, "compare") && strings.Count(lowerPrompt, "browser.read observation") < 2 {
			urls := mockURLs(goal)
			if len(urls) > 1 {
				return mockReActAction("browser.read", map[string]any{"url": urls[1]})
			}
		}
		if strings.Contains(lowerPrompt, "browser.type") && (strings.Contains(lowerGoal, "截图") || strings.Contains(lowerGoal, "screenshot")) && !strings.Contains(lowerPrompt, "browser.screenshot") {
			return mockReActAction("browser.screenshot", map[string]any{})
		}
		if strings.Contains(lowerGoal, "detail") && strings.Contains(lowerPrompt, "web.search observation") {
			return `{"type":"final","answer":"I reviewed the observed web search evidence and prepared the bounded answer."}`
		}
		return `{"type":"final","answer":"I reviewed the observed evidence and prepared the bounded answer."}`
	}
	switch {
	case (strings.Contains(lowerGoal, "输入") || strings.Contains(lowerGoal, "type")) && (strings.Contains(lowerGoal, "截图") || strings.Contains(lowerGoal, "screenshot")):
		return mockReActAction("browser.type", map[string]any{"text": "苹果"})
	case strings.Contains(lowerGoal, "apply patch"):
		return mockReActAction("code.apply_patch", map[string]any{"patch": mockPatch(goal)})
	case strings.Contains(lowerGoal, "inspect repo"):
		if strings.Contains(lowerGoal, "failing test") || strings.Contains(lowerGoal, "failed test") {
			return mockReActAction("files.search", map[string]any{"query": "test"})
		}
		return mockReActAction("files.search", map[string]any{"query": "repo"})
	case strings.Contains(lowerGoal, "shell command") || strings.Contains(lowerGoal, "run tests"):
		return mockReActAction("shell.exec_sandboxed", map[string]any{"command": mockShellCommand(goal)})
	case strings.Contains(lowerGoal, "remember"):
		return mockReActAction("memory.write_candidate", map[string]any{
			"content":     goal,
			"kind":        "note",
			"sensitivity": "normal",
			"reason":      "User asked SparkClaw to remember this.",
		})
	case len(mockURLs(goal)) > 0:
		return mockReActAction("browser.read", map[string]any{"url": mockURLs(goal)[0]})
	case strings.Contains(lowerGoal, "web") || strings.Contains(lowerGoal, "internet") || strings.Contains(lowerGoal, "news") || strings.Contains(lowerGoal, "latest") || strings.Contains(lowerGoal, "today") || strings.Contains(lowerGoal, "search online") || strings.Contains(lowerGoal, "网上") || strings.Contains(lowerGoal, "联网") || strings.Contains(lowerGoal, "查一下") || strings.Contains(lowerGoal, "最新"):
		return mockReActAction("web.search", map[string]any{"query": mockSearchQuery(goal)})
	case strings.Contains(lowerGoal, "compare"):
		paths := mockPaths(goal)
		if len(paths) > 0 {
			return mockReActAction("files.read", map[string]any{"path": paths[0]})
		}
		return mockReActAction("files.search", map[string]any{"query": mockSearchQuery(goal)})
	case strings.Contains(lowerGoal, "search") || strings.Contains(lowerGoal, "find"):
		return mockReActAction("files.search", map[string]any{"query": mockSearchQuery(goal)})
	case strings.Contains(lowerGoal, "read") || strings.Contains(lowerGoal, "summarize"):
		return mockReActAction("files.read", map[string]any{"path": mockPath(goal)})
	default:
		return `{"type":"final","answer":"I can answer this directly from the current conversation."}`
	}
}

func mockBrowserInteractionStage(prompt string) string {
	marker := "workflow_stage:"
	index := strings.LastIndex(prompt, marker)
	if index < 0 {
		return ""
	}
	value := strings.TrimSpace(prompt[index+len(marker):])
	if end := strings.IndexAny(value, " .\t\r\n,;}"); end >= 0 {
		value = value[:end]
	}
	switch value {
	case "health_check", "scan_tabs", "focus_existing", "navigate_blank", "open_new", "snapshot_before_action", "choose_and_click", "snapshot_after_action", "verify_action":
		return value
	default:
		return ""
	}
}

func mockBrowserInteractionAction(prompt, goal, stage string) string {
	switch stage {
	case "health_check":
		return mockReActAction("browser.status", map[string]any{})
	case "scan_tabs":
		return mockReActAction("browser.list_tabs", map[string]any{})
	case "focus_existing":
		return mockReActAction("browser.focus", map[string]any{"page_id": mockWorkflowPageID(prompt)})
	case "navigate_blank":
		urls := mockURLs(goal)
		if len(urls) > 0 {
			return mockReActAction("browser.navigate", map[string]any{"page_id": mockWorkflowPageID(prompt), "url": urls[0]})
		}
	case "open_new":
		urls := mockURLs(goal)
		if len(urls) > 0 {
			return mockReActAction("browser.open", map[string]any{"url": urls[0]})
		}
	case "snapshot_before_action", "snapshot_after_action":
		return mockReActAction("browser.snapshot", map[string]any{})
	case "choose_and_click":
		snapshotID, pageID, elementRef := mockLatestBrowserSnapshot(prompt)
		return mockReActAction("browser.click", map[string]any{
			"page_id": pageID, "snapshot_id": snapshotID, "uid": elementRef,
			"expected_effect": "Advance the frozen browser interaction goal.",
		})
	case "verify_action":
		snapshotIDs := mockBrowserFieldValues(prompt, "snapshot_id")
		beforeID, afterID := "snapshot_before", "snapshot_after"
		if len(snapshotIDs) >= 2 {
			beforeID, afterID = snapshotIDs[len(snapshotIDs)-2], snapshotIDs[len(snapshotIDs)-1]
		}
		clicked := mockLastBrowserFieldValue(prompt, "clicked")
		if clicked == "" {
			_, _, clicked = mockLatestBrowserSnapshot(prompt)
		}
		return mockReActAction("browser.verify", map[string]any{
			"before_snapshot_id": beforeID, "after_snapshot_id": afterID,
			"element_ref": clicked, "verdict": "success", "reason": "The requested click produced a verified page-state change.",
		})
	}
	return `{"type":"final","answer":"The browser interaction workflow could not select its required next action."}`
}

func mockLatestBrowserSnapshot(prompt string) (string, string, string) {
	normalized := strings.ReplaceAll(prompt, `\"`, `"`)
	if !strings.Contains(normalized, `"schema_version":"browser_interaction_snapshot_v1"`) {
		return "snapshot_missing", mockWorkflowPageID(prompt), "element_missing"
	}
	return mockLastBrowserFieldValue(normalized, "snapshot_id"), mockLastBrowserFieldValue(normalized, "page_id"), mockLastBrowserFieldValue(normalized, "ref")
}

func mockBrowserFieldValues(prompt, key string) []string {
	normalized := strings.ReplaceAll(prompt, `\"`, `"`)
	marker := `"` + key + `":"`
	values := []string{}
	seen := map[string]bool{}
	for offset := 0; offset < len(normalized); {
		index := strings.Index(normalized[offset:], marker)
		if index < 0 {
			break
		}
		index += offset + len(marker)
		end := strings.IndexByte(normalized[index:], '"')
		if end < 0 {
			break
		}
		value := normalized[index : index+end]
		if value != "" && !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
		offset = index + end + 1
	}
	return values
}

func mockLastBrowserFieldValue(prompt, key string) string {
	values := mockBrowserFieldValues(prompt, key)
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func mockFieldAfter(value, key string) string {
	marker := `"` + key + `":"`
	index := strings.Index(value, marker)
	if index < 0 {
		return ""
	}
	value = value[index+len(marker):]
	if end := strings.IndexByte(value, '"'); end >= 0 {
		return value[:end]
	}
	return ""
}

func mockWorkflowPageID(prompt string) string {
	marker := "page_id="
	index := strings.LastIndex(prompt, marker)
	if index < 0 {
		return "1"
	}
	value := prompt[index+len(marker):]
	if end := strings.IndexAny(value, " \t\r\n,;}"); end >= 0 {
		value = value[:end]
	}
	return strings.TrimSpace(value)
}

func mockReActAction(tool string, args map[string]any) string {
	raw, _ := json.Marshal(map[string]any{
		"type":      "action",
		"tool":      tool,
		"arguments": args,
		"reason":    "mock ReAct action for test coverage",
	})
	return string(raw)
}

func mockReActGoal(user string) string {
	marker := "User goal:"
	idx := strings.Index(user, marker)
	if idx < 0 {
		return strings.TrimSpace(user)
	}
	rest := strings.TrimSpace(user[idx+len(marker):])
	if next := strings.Index(rest, "\n\n"); next >= 0 {
		rest = rest[:next]
	}
	return strings.TrimSpace(rest)
}

func mockURLs(content string) []string {
	fields := strings.Fields(content)
	urls := []string{}
	for _, field := range fields {
		cleaned := strings.Trim(field, ".,;:()[]{}<>\"'`")
		if strings.HasPrefix(cleaned, "http://") || strings.HasPrefix(cleaned, "https://") {
			urls = append(urls, cleaned)
		}
	}
	return urls
}

func mockPath(content string) string {
	paths := mockPaths(content)
	if len(paths) > 0 {
		return paths[0]
	}
	return "missing.txt"
}

func mockPaths(content string) []string {
	paths := []string{}
	for _, field := range strings.Fields(content) {
		cleaned := strings.Trim(field, ".,;:()[]{}<>\"'`")
		if strings.Contains(cleaned, ".") && !strings.HasPrefix(cleaned, "http") {
			paths = append(paths, cleaned)
		}
	}
	return paths
}

func mockSearchQuery(content string) string {
	lower := strings.ToLower(content)
	if idx := strings.Index(lower, "search email for "); idx >= 0 {
		return strings.TrimSpace(content[idx+len("search email for "):])
	}
	for _, prefix := range []string{"search for ", "find ", "search "} {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			query := strings.TrimSpace(content[idx+len(prefix):])
			query = strings.TrimSuffix(query, " in the workspace")
			if query != "" {
				return query
			}
		}
	}
	return strings.TrimSpace(content)
}

func mockShellCommand(content string) string {
	if start := strings.Index(content, "`"); start >= 0 {
		if end := strings.Index(content[start+1:], "`"); end >= 0 {
			return content[start+1 : start+1+end]
		}
	}
	if strings.Contains(strings.ToLower(content), "run tests") {
		return "npm test"
	}
	return "ls -la"
}

func mockPatch(content string) string {
	start := strings.Index(content, "```diff")
	if start < 0 {
		return content
	}
	patch := strings.TrimSpace(content[start+len("```diff"):])
	if end := strings.LastIndex(patch, "```"); end >= 0 {
		patch = patch[:end]
	}
	return strings.TrimSpace(patch)
}

func mockInjectedResponse(user, marker string) string {
	idx := strings.Index(user, marker)
	if idx < 0 {
		return ""
	}
	value := strings.TrimSpace(user[idx+len(marker):])
	if newline := strings.Index(value, "\n"); newline >= 0 {
		value = strings.TrimSpace(value[:newline])
	}
	return value
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
		terms := mockSemanticTerms(input)
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
	results := make([]RerankScored, 0, len(documents))
	for index, document := range documents {
		score := mockSemanticSimilarity(query, document)
		if candidateID := mockPromptValue(document, "candidate_id="); candidateID != "" {
			if prior := mockIntentCandidatePrior(query, candidateID); prior > 0 {
				score = prior
			} else {
				score = min(score, 0.25)
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

type mockIntentGraphCandidate struct {
	CandidateID       string   `json:"candidate_id"`
	SemanticBoundary  string   `json:"semantic_boundary"`
	PositiveSemantics []string `json:"positive_semantics"`
	HardNegatives     []string `json:"hard_negatives"`
}

func mockIntentFusionResponse(user string) string {
	revision := mockPromptValue(user, "Graph revision: ")
	query := mockPromptSection(user, "Owner semantic query:\n", "\n\nReturn the scored registered candidates now.")
	graphJSON := mockPromptSection(user, "Semantic graph:\n", "\n\nOwner semantic query:")
	var graph []mockIntentGraphCandidate
	if json.Unmarshal([]byte(graphJSON), &graph) != nil || len(graph) == 0 {
		encoded, _ := json.Marshal(map[string]any{"graph_revision": revision, "candidates": []any{}})
		return string(encoded)
	}
	type scoredCandidate struct {
		ID    string
		Score float64
	}
	scored := make([]scoredCandidate, 0, len(graph))
	for _, candidate := range graph {
		positive := mockSemanticSimilarity(query, candidate.SemanticBoundary)
		for _, example := range candidate.PositiveSemantics {
			positive = max(positive, mockSemanticSimilarity(query, example))
		}
		negative := 0.0
		for _, example := range candidate.HardNegatives {
			negative = max(negative, mockSemanticSimilarity(query, example))
		}
		score := min(0.99, max(0.01, 0.08+1.25*positive-0.55*negative))
		if prior := mockIntentCandidatePrior(query, candidate.CandidateID); prior > 0 {
			score = prior
		} else {
			score = min(score, 0.25)
		}
		scored = append(scored, scoredCandidate{ID: candidate.CandidateID, Score: score})
	}
	slices.SortFunc(scored, func(left, right scoredCandidate) int {
		if left.Score > right.Score {
			return -1
		}
		if left.Score < right.Score {
			return 1
		}
		return strings.Compare(left.ID, right.ID)
	})
	if len(scored) > 5 {
		scored = scored[:5]
	}
	candidates := make([]map[string]any, 0, len(scored))
	for _, candidate := range scored {
		candidates = append(candidates, map[string]any{
			"candidate_id": candidate.ID, "tree_score": candidate.Score,
		})
	}
	encoded, _ := json.Marshal(map[string]any{"graph_revision": revision, "candidates": candidates})
	return string(encoded)
}

func mockPromptValue(prompt, prefix string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), prefix))
		}
	}
	return ""
}

func mockPromptSection(prompt, start, end string) string {
	index := strings.Index(prompt, start)
	if index < 0 {
		return ""
	}
	section := prompt[index+len(start):]
	if endIndex := strings.Index(section, end); endIndex >= 0 {
		section = section[:endIndex]
	}
	return strings.TrimSpace(section)
}

func mockSemanticSimilarity(left, right string) float64 {
	leftTerms := mockSemanticTermSet(left)
	rightTerms := mockSemanticTermSet(right)
	if len(leftTerms) == 0 || len(rightTerms) == 0 {
		return 0
	}
	intersection := 0
	for term := range leftTerms {
		if rightTerms[term] {
			intersection++
		}
	}
	return float64(intersection) / math.Sqrt(float64(len(leftTerms)*len(rightTerms)))
}

func mockSemanticTermSet(value string) map[string]bool {
	terms := mockSemanticTerms(value)
	set := make(map[string]bool, len(terms))
	for _, term := range terms {
		set[term] = true
	}
	return set
}

func mockSemanticTerms(value string) []string {
	lower := strings.ToLower(value)
	terms := strings.FieldsFunc(lower, func(char rune) bool {
		return !(unicode.IsLetter(char) || unicode.IsDigit(char))
	})
	for _, run := range strings.FieldsFunc(lower, func(char rune) bool { return !unicode.In(char, unicode.Han) }) {
		runes := []rune(run)
		for size := 1; size <= 3; size++ {
			for start := 0; start+size <= len(runes); start++ {
				terms = append(terms, string(runes[start:start+size]))
			}
		}
	}
	if len(terms) == 0 {
		return []string{lower}
	}
	return terms
}

func mockIntentCandidatePrior(query, candidateID string) float64 {
	lower := strings.ToLower(strings.TrimSpace(query))
	contains := func(terms ...string) bool { return containsAnyTerm(lower, terms...) }
	temporal := contains("秒后", "分钟后", "小时后", "天后", "明天", "后天", "稍后", "到时候", "每天", "每周", "tomorrow", "later", "every ")
	scheduleDiscussion := contains("为什么", "失败", "没有触发", "没触发", "why", "failed", "failure")
	scheduleStatement := contains("我会", "我将", "我参加", "i will ", "i am going")
	switch candidateID {
	case "schedule.manage#create":
		if temporal && !scheduleDiscussion && !scheduleStatement && !contains("查看", "列出", "有哪些", "show", "list") && contains("提醒", "告知", "叫我", "跟我说", "通知", "查一下", "查询", "remind", "tell me", "notify", "search") {
			return 0.97
		}
	case "schedule.manage#read":
		scheduleTarget := contains("提醒", "定时任务", "计划任务", "schedule", "scheduled", "reminder")
		if !scheduleDiscussion && scheduleTarget && contains("查看", "列出", "有哪些", "show", "list", "view") {
			return 0.96
		}
	case "schedule.manage#edit":
		scheduleTarget := contains("提醒", "定时任务", "计划任务", "schedule", "reminder")
		if !scheduleDiscussion && scheduleTarget && contains("修改", "改到", "改为", "推迟", "提前", "reschedule", "edit reminder") {
			return 0.97
		}
	case "schedule.manage#delete":
		if !scheduleDiscussion && contains("取消", "删除", "不要再", "cancel", "delete reminder") && contains("提醒", "定时", "任务", "reminder", "schedule") {
			return 0.97
		}
	case "browser.weather#read":
		if contains("天气", "气温", "温度", "下雨", "下雪", "weather", "forecast") && !contains("预警", "新闻", "空气质量", "对比", "比较", "alert", "news", "compare", "air quality") {
			return 0.97
		}
	case "browser.internet_search#search":
		ordinaryWeather := contains("天气", "气温", "温度", "下雨", "下雪", "weather", "forecast") && !contains("预警", "新闻", "空气质量", "对比", "比较", "alert", "news", "air quality", "compare")
		localDocument := contains(".pdf", ".docx", ".xlsx", ".pptx", ".txt", ".md", "本地文件", "工作区", "workspace", "local file")
		browserAction := contains("点击", "点开", "按钮", "当前页面", "当前标签", "页面结构", "chrome 页面", "勾选", "输入", "click", "tap", "button", "current page", "current tab", "page structure", "check", "type")
		conceptual := contains("概念", "是什么意思", "是什么概念", "解释", "what is", "explain")
		if !ordinaryWeather && !localDocument && !browserAction && !conceptual && contains("查一下", "查询一下", "搜索", "联网", "浏览器查询", "最新", "今天", "今日", "现在", "当前", "实时", "最近", "新闻", "价格", "售价", "汇率", "指数", "比分", "在售", "上架", "预警", "空气质量", "对比", "比较", "search", "look up", "online", "current", "latest", "today", "news", "price", "pricing", "exchange rate", "score", "available", "compare") {
			return 0.96
		}
	case "browser.automation#open":
		if contains("打开", "访问", "切换到", "open", "visit", "focus") && !contains("点击", "点开", "输入", "填写", "选择", "勾选", "登录", "认证", "草稿箱", "收件箱", "click", "type", "select", "check", "login", "sign in", "authenticate", "drafts", "inbox") {
			return 0.96
		}
	case "browser.interaction#interact":
		if contains("点击", "点开", "按钮", "勾选", "选择", "输入", "草稿箱", "收件箱", "click", "tap", "check", "select", "type", "drafts", "inbox") {
			return 0.97
		}
	case "document.read#read":
		documentTarget := contains("附件", "文档", "文件", "图片", "图像", ".pdf", ".docx", ".xlsx", ".pptx", ".txt", ".md", ".png", ".jpg", ".jpeg", "document", "file", "image", "attached")
		mutation := contains("修改", "编辑", "替换", "润色", "完善", "改写", "填入", "填写", "新增", "添加", "插入", "追加", "删除", "移除", "更新", "调整", "edit", "modify", "replace", "polish", "improve", "fill", "add", "insert", "append", "delete", "remove", "update")
		if documentTarget && contains("读取", "阅读", "查看", "总结", "概括", "解释", "什么内容", "什么文字", "分析", "read", "summarize", "inspect", "explain", "analyze") && !mutation {
			return 0.96
		}
	case "document.edit#edit":
		mutation := contains("修改", "编辑", "替换", "改为", "润色", "完善", "改写", "填入", "填写", "新增", "添加", "增加", "插入", "追加", "删除", "移除", "更新", "调整", "edit", "modify", "replace", "polish", "improve", "fill", "add", "insert", "append", "delete", "remove", "update")
		browserContext := contains("按钮", "页面", "账户", "网页", "button", "page", "account", "browser")
		fileLifecycle := contains("删除", "移除", "delete", "remove") && !contains("内容", "文字", "文本", "段落", "行", "单元格", "幻灯片", "页面内容", "content", "text", "paragraph", "row", "cell", "slide")
		if mutation && !browserContext && !fileLifecycle && !contains("pdf") {
			return 0.97
		}
	case "document.edit#transform":
		if contains("pdf") && contains("修改", "旋转", "拆分", "调整", "transform", "rotate", "split", "edit") {
			return 0.97
		}
	case "conversation.answer#answer":
		reserved := contains(
			"打开", "点击", "登录", "提醒", "定时", "文件", "文档", "附件", "图片", "图像", "照片", "天气", "气温", "温度", "下雨", "下雪", "预报", "空气质量",
			"新闻", "价格", "金价", "售价", "汇率", "指数", "比分", "现在", "当前", "实时", "最新", "运行", "测试", "代码", "仓库", "项目", "记住", "完善", "修改", "编辑",
			"open", "click", "login", "remind", "schedule", "file", "document", "image", "photo", "weather", "forecast", "air quality", "news", "price", "exchange rate", "current", "latest", "run test", "code", "repo", "repository", "project", "remember", "edit", "improve",
		)
		if !reserved && (contains("你好", "您好", "谢谢", "解释", "概括", "是什么", "为什么", "区别", "hello", "thanks", "explain", "what is", "why") || len([]rune(lower)) <= 12) {
			return 0.95
		}
	}
	return 0
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

func modelHTTPError(resp *http.Response, profile config.ModelProfile, endpoint string, raw []byte, system, user string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyText := strings.TrimSpace(string(body))
	if bodyText == "" {
		bodyText = "<empty response body>"
	}
	return fmt.Errorf(
		"model router received HTTP %d from %s model=%q request_bytes=%d system_bytes=%d user_bytes=%d response_body=%q",
		resp.StatusCode,
		endpoint,
		modelID(profile),
		len(raw),
		len([]byte(system)),
		len([]byte(user)),
		bodyText,
	)
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
