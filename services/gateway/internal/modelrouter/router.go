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
	profile, err := r.Profile(profileName)
	if err != nil {
		return ChatResult{}, err
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
	if injected := mockInjectedResponse(user, "MOCK_DIRECTORY_SELECTION_RESPONSE:"); injected != "" {
		return injected
	}
	if strings.Contains(user, "Deterministic route and authority-safe delivery fallback:") {
		if injected := mockInjectedResponse(user, "MOCK_INTENT_RESPONSE:"); injected != "" {
			return injected
		}
	}
	if injected := mockInjectedResponse(user, "MOCK_TASK_HINT_RESPONSE:"); injected != "" {
		return injected
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
