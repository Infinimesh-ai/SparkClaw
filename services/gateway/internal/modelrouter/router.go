package modelrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	return r.chatCompletionsWithTemperature(ctx, profile, system, user, 0.2)
}

func (r Router) chatCompletionsWithTemperature(ctx context.Context, profile config.ModelProfile, system, user string, temperature float64) (string, tokenUsage, error) {
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
		"temperature": temperature,
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
	rawContent, usage, err := r.chatCompletionsWithTemperature(ctx, profile, system, content, 0)
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

// GuardVerdictUnknown marks a guard reply that produced no recognizable
// verdict (unparseable, empty, or truncated). It is deliberately distinct
// from "review": review is an explicit model verdict and stops the run,
// while unknown is a classifier infrastructure failure and must not brick
// the gateway — the caller audits it and lets the run proceed, matching the
// fail-open posture of the transport-failure fallback.
const GuardVerdictUnknown = "unknown"

func parseGuardContent(content string) GuardResult {
	content = strings.TrimSpace(content)
	result := GuardResult{
		Verdict: GuardVerdictUnknown,
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
	if verdict, categories, ok := parseNativeGuardContent(content); ok {
		result.Verdict = verdict
		result.Categories = categories
		return result
	}
	// A reply that matches neither the JSON nor the native format stays at
	// the "unknown" default. Never scan prose for verdict words: a reply
	// like "unsafe, do not allow" must not weaken the verdict to allow.
	return result
}

func parseNativeGuardContent(content string) (string, []string, bool) {
	verdict := ""
	categories := []string{}
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "safety":
			verdict = normalizeGuardVerdict(value)
		case "categories":
			for category := range strings.FieldsFuncSeq(value, func(r rune) bool {
				return r == ',' || r == ';'
			}) {
				category = strings.TrimSpace(category)
				if category != "" && !strings.EqualFold(category, "none") {
					categories = append(categories, category)
				}
			}
		}
	}
	if verdict == "" {
		return "", nil, false
	}
	return verdict, uniqueStrings(categories), true
}

func normalizeGuardVerdict(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "allow", "allowed", "safe":
		return "allow"
	case "review", "needs_review", "needs-review", "warn", "controversial":
		return "review"
	case "block", "blocked", "deny", "unsafe":
		return "block"
	default:
		return ""
	}
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

func getenv(key string) string {
	// Wrapped for tests and to keep all external model access in this package.
	return os.Getenv(key)
}
