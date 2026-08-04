package documentocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const (
	ovisOCR2Prompt    = "Extract all readable content from the image in natural human reading order and output the result as a single Markdown document. For charts or images, represent them using an HTML image tag: <img src=\"images/bbox_{left}_{top}_{right}_{bottom}.jpg\" />, where left, top, right, bottom are bounding box coordinates scaled to [0, 1000). Format formulas as LaTeX. Format tables as HTML: <table>...</table>. Transcribe all other text as standard Markdown. Preserve the original text without translation or paraphrasing."
	ovisOCR2MinPixels = 448 * 448
	ovisOCR2MaxPixels = 2880 * 2880
)

type OpenAIHTTP struct {
	cfg      config.DocumentOCRAdapterConfig
	client   *http.Client
	admitted chan struct{}
	workers  chan struct{}
}

func NewOpenAIHTTP(cfg config.DocumentOCRAdapterConfig) (*OpenAIHTTP, error) {
	if !cfg.Enabled || cfg.Provider != "openai-http" {
		return nil, errors.New("OpenAI-compatible document OCR provider is not enabled")
	}
	if err := validateEndpoint(cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Model) == "" || cfg.TimeoutSeconds <= 0 || cfg.MaxUploadBytes <= 0 || cfg.MaxOutputBytes <= 0 || cfg.MaxTokens <= 0 || cfg.MaxConcurrency <= 0 || cfg.MaxPending < 0 {
		return nil, errors.New("OpenAI-compatible document OCR configuration is invalid")
	}
	return &OpenAIHTTP{
		cfg: cfg,
		client: &http.Client{
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		admitted: make(chan struct{}, cfg.MaxConcurrency+cfg.MaxPending),
		workers:  make(chan struct{}, cfg.MaxConcurrency),
	}, nil
}

func validateEndpoint(cfg config.DocumentOCRAdapterConfig) error {
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("OpenAI-compatible document OCR base URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("OpenAI-compatible document OCR base URL must use http or https")
	}
	allowed := false
	for _, host := range cfg.AllowedHosts {
		if strings.EqualFold(strings.TrimSpace(host), parsed.Hostname()) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("document OCR base URL host %q is not allowlisted", parsed.Hostname())
	}
	return nil
}

func (a *OpenAIHTTP) Enabled() bool { return true }

func (a *OpenAIHTTP) Parse(ctx context.Context, input Request) (Result, error) {
	if len(input.Content) == 0 {
		return Result{}, errors.New("document OCR image is empty")
	}
	if int64(len(input.Content)) > a.cfg.MaxUploadBytes {
		return Result{}, errors.New("document OCR image exceeds the upload limit")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(input.ContentType, ";")[0]))
	switch contentType {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp":
	default:
		return Result{}, fmt.Errorf("document OCR does not support content type %q", contentType)
	}
	if err := a.acquire(ctx); err != nil {
		return Result{}, err
	}
	defer func() { <-a.admitted }()
	select {
	case a.workers <- struct{}{}:
		defer func() { <-a.workers }()
	case <-ctx.Done():
		return Result{}, contextError(ctx.Err())
	}

	payload := map[string]any{
		"model": a.cfg.Model,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(input.Content)}},
				map[string]any{"type": "text", "text": ovisOCR2Prompt},
			},
		}},
		"max_tokens":           a.cfg.MaxTokens,
		"temperature":          0,
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
		"mm_processor_kwargs": map[string]any{
			"images_kwargs": map[string]any{"min_pixels": ovisOCR2MinPixels, "max_pixels": ovisOCR2MaxPixels},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Result{}, fmt.Errorf("encode document OCR request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint("/chat/completions"), bytes.NewReader(raw))
	if err != nil {
		return Result{}, fmt.Errorf("create document OCR request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	started := time.Now()
	resp, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, contextError(ctx.Err())
		}
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return Result{}, errors.New("document OCR inference timed out")
		}
		return Result{}, errors.New("document OCR service is unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = readBounded(resp.Body, 64<<10)
		return Result{}, fmt.Errorf("document OCR service returned HTTP %d", resp.StatusCode)
	}
	responseLimit := int64(a.cfg.MaxOutputBytes)*2 + 64<<10
	responseRaw, err := readBounded(resp.Body, responseLimit)
	if err != nil {
		return Result{}, err
	}
	var decoded struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseRaw, &decoded); err != nil {
		return Result{}, errors.New("document OCR service returned invalid JSON")
	}
	if len(decoded.Choices) == 0 {
		return Result{}, errors.New("document OCR service returned no completion")
	}
	markdown := cleanOvisOCR2Output(decoded.Choices[0].Message.Content)
	if strings.TrimSpace(markdown) == "" {
		return Result{}, errors.New("document OCR service returned no readable content")
	}
	if len([]byte(markdown)) > a.cfg.MaxOutputBytes {
		return Result{}, errors.New("document OCR output exceeds the configured limit")
	}
	if strings.TrimSpace(decoded.Model) == "" {
		decoded.Model = a.cfg.Model
	}
	return Result{Markdown: markdown, Model: decoded.Model, InferenceMS: time.Since(started).Milliseconds()}, nil
}

func (a *OpenAIHTTP) acquire(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return contextError(ctx.Err())
	default:
	}
	select {
	case a.admitted <- struct{}{}:
		return nil
	default:
		return errors.New("document OCR service is busy")
	}
}

func (a *OpenAIHTTP) Close() error {
	a.client.CloseIdleConnections()
	return nil
}

func (a *OpenAIHTTP) endpoint(path string) string {
	return strings.TrimRight(a.cfg.BaseURL, "/") + path
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("document OCR service response exceeds the configured limit")
	}
	return raw, nil
}

func contextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("document OCR inference timed out")
	}
	return errors.New("document OCR inference was cancelled")
}

func cleanOvisOCR2Output(value string) string {
	blocks := strings.Split(strings.TrimSpace(value), "\n\n")
	kept := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if strings.HasPrefix(strings.TrimSpace(block), `<img src="images/bbox_`) {
			continue
		}
		kept = append(kept, block)
	}
	return cleanTruncatedRepeats(strings.TrimSpace(strings.Join(kept, "\n\n")))
}

func cleanTruncatedRepeats(value string) string {
	characters := []rune(value)
	n := len(characters)
	if n < 8000 {
		return value
	}
	maxPeriod := min(200, n-1)
	for unitLength := 1; unitLength <= maxPeriod; unitLength++ {
		if characters[n-1] != characters[n-1-unitLength] {
			continue
		}
		matchLength := 1
		for index := n - 2; index >= unitLength && characters[index] == characters[index-unitLength]; index-- {
			matchLength++
		}
		totalLength := matchLength + unitLength
		repeatTimes := totalLength / unitLength
		tailLength := totalLength % unitLength
		if repeatTimes >= 5 && totalLength >= 100 {
			cleaned := append([]rune(nil), characters[:n-totalLength+unitLength]...)
			if tailLength > 0 {
				cleaned = append(cleaned, characters[n-tailLength:]...)
			}
			return string(cleaned)
		}
	}
	if !utf8.ValidString(value) {
		return strings.ToValidUTF8(value, "")
	}
	return value
}
