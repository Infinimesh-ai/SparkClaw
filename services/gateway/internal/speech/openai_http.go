package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const maxSpeechResponseBytes = 1 << 20

type OpenAIHTTPTranscriber struct {
	cfg        config.SpeechConfig
	client     *http.Client
	admitted   chan struct{}
	workers    chan struct{}
	sessionsMu sync.Mutex
	sessions   map[*openAIRealtimeSession]struct{}
}

func NewOpenAIHTTP(cfg config.SpeechConfig) (*OpenAIHTTPTranscriber, error) {
	if !cfg.Enabled || cfg.Backend != "openai-http" {
		return nil, errors.New("OpenAI-compatible speech backend is not enabled")
	}
	if err := validateOpenAIEndpoint(cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("speech model is required")
	}
	if cfg.MaxConcurrency <= 0 || cfg.MaxPending < 0 || cfg.TimeoutSeconds <= 0 {
		return nil, errors.New("OpenAI-compatible speech limits are invalid")
	}
	return &OpenAIHTTPTranscriber{
		cfg: cfg,
		client: &http.Client{
			Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		admitted: make(chan struct{}, cfg.MaxConcurrency+cfg.MaxPending),
		workers:  make(chan struct{}, cfg.MaxConcurrency),
		sessions: map[*openAIRealtimeSession]struct{}{},
	}, nil
}

func validateOpenAIEndpoint(cfg config.SpeechConfig) error {
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("OpenAI-compatible speech base URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("OpenAI-compatible speech base URL must use http or https")
	}
	allowed := false
	for _, host := range cfg.AllowedHosts {
		if strings.EqualFold(strings.TrimSpace(host), parsed.Hostname()) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("speech base URL host %q is not allowlisted", parsed.Hostname())
	}
	return nil
}

func (t *OpenAIHTTPTranscriber) Status(ctx context.Context) Status {
	status := baseStatus(t.cfg)
	status.Enabled = true
	status.Backend = "openai-http"
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(statusCtx, http.MethodGet, t.endpoint("/health"), nil)
	if err != nil {
		status.Reason = "speech service health request is invalid"
		return status
	}
	resp, err := t.client.Do(req)
	if err != nil {
		status.Reason = "speech service is unavailable"
		return status
	}
	defer resp.Body.Close()
	raw, err := readBoundedResponse(resp.Body)
	if err != nil {
		status.Reason = "speech service health response exceeded the limit"
		return status
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status.Reason = fmt.Sprintf("speech service health returned HTTP %d", resp.StatusCode)
		return status
	}
	status.Ready = true
	status.State = StateReady
	if len(bytes.TrimSpace(raw)) > 0 {
		var health struct {
			SupportsStreaming bool   `json:"supports_streaming"`
			Protocol          string `json:"protocol"`
			SampleRate        int    `json:"sample_rate"`
			FrameMS           int    `json:"frame_ms"`
		}
		if json.Unmarshal(raw, &health) == nil && health.SupportsStreaming && health.Protocol == RealtimeProtocol &&
			health.SampleRate == RealtimeSampleRate && health.FrameMS == RealtimeFrameMS {
			status.SupportsStreaming = true
			status.Realtime = &RealtimeCapabilities{
				Protocol: RealtimeProtocol, SampleRate: health.SampleRate,
				Channels: 1, BitsPerSample: 16, FrameMS: health.FrameMS,
			}
		}
	}
	if len(t.workers) >= cap(t.workers) {
		status.State = StateBusy
	}
	return status
}

func (t *OpenAIHTTPTranscriber) Transcribe(ctx context.Context, input Request) (Result, error) {
	release, err := t.acquireOperation(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileHeader := make(textproto.MIMEHeader)
	fileHeader.Set("Content-Disposition", `form-data; name="file"; filename="recording.wav"`)
	fileHeader.Set("Content-Type", "audio/wav")
	file, err := writer.CreatePart(fileHeader)
	if err != nil {
		return Result{}, NewError(CodeInferenceFailed, "failed to prepare speech request", false, err)
	}
	if _, err := file.Write(input.PCM16WAV); err != nil {
		return Result{}, NewError(CodeInferenceFailed, "failed to prepare speech request", false, err)
	}
	if err := writer.WriteField("model", t.cfg.Model); err != nil {
		return Result{}, NewError(CodeInferenceFailed, "failed to prepare speech request", false, err)
	}
	if input.Language != "" && input.Language != "auto" {
		if err := writer.WriteField("language", input.Language); err != nil {
			return Result{}, NewError(CodeInferenceFailed, "failed to prepare speech request", false, err)
		}
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return Result{}, NewError(CodeInferenceFailed, "failed to prepare speech request", false, err)
	}
	if err := writer.Close(); err != nil {
		return Result{}, NewError(CodeInferenceFailed, "failed to prepare speech request", false, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint("/v1/audio/transcriptions"), &body)
	if err != nil {
		return Result{}, NewError(CodeInferenceFailed, "failed to create speech request", false, err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-SparkClaw-Request-ID", input.RequestID)
	started := time.Now()
	resp, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, contextSpeechError(ctx.Err())
		}
		var netErr interface{ Timeout() bool }
		if errors.As(err, &netErr) && netErr.Timeout() {
			return Result{}, NewError(CodeTimeout, "speech transcription timed out", true, err)
		}
		return Result{}, NewError(CodeUnavailable, "speech service is unavailable", true, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, upstreamHTTPError(resp)
	}
	var payload struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Model    string `json:"model"`
	}
	if err := decodeBoundedJSON(resp.Body, &payload); err != nil {
		return Result{}, NewError(CodeInferenceFailed, "speech service returned invalid JSON", false, err)
	}
	if payload.Model == "" {
		payload.Model = t.cfg.Model
	}
	if payload.Language == "" {
		payload.Language = input.Language
	}
	return Result{
		Text:        payload.Text,
		Language:    payload.Language,
		Model:       payload.Model,
		InferenceMS: time.Since(started).Milliseconds(),
	}, nil
}

func (t *OpenAIHTTPTranscriber) acquire(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return contextSpeechError(ctx.Err())
	default:
	}
	select {
	case t.admitted <- struct{}{}:
		return nil
	default:
		return NewError(CodeBusy, "speech service is busy", true, nil)
	}
}

func (t *OpenAIHTTPTranscriber) acquireOperation(ctx context.Context) (func(), error) {
	if err := t.acquire(ctx); err != nil {
		return nil, err
	}
	select {
	case t.workers <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-t.workers
				<-t.admitted
			})
		}, nil
	case <-ctx.Done():
		<-t.admitted
		return nil, contextSpeechError(ctx.Err())
	}
}

func (t *OpenAIHTTPTranscriber) Close() error {
	t.sessionsMu.Lock()
	sessions := make([]*openAIRealtimeSession, 0, len(t.sessions))
	for session := range t.sessions {
		sessions = append(sessions, session)
	}
	t.sessionsMu.Unlock()
	for _, session := range sessions {
		_ = session.Close()
	}
	t.client.CloseIdleConnections()
	return nil
}

func (t *OpenAIHTTPTranscriber) endpoint(path string) string {
	return strings.TrimRight(t.cfg.BaseURL, "/") + path
}

func decodeBoundedJSON(reader io.Reader, target any) error {
	raw, err := readBoundedResponse(reader)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func readBoundedResponse(reader io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxSpeechResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSpeechResponseBytes {
		return nil, errors.New("speech service response exceeds limit")
	}
	return raw, nil
}

func discardBoundedResponse(reader io.Reader) error {
	_, err := readBoundedResponse(reader)
	return err
}

func upstreamHTTPError(resp *http.Response) error {
	_ = discardBoundedResponse(resp.Body)
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return NewError(CodeBusy, "speech service is busy", true, nil)
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return NewError(CodeTimeout, "speech service timed out", true, nil)
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return NewError(CodeUnavailable, "speech service is unavailable", true, nil)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return NewError(CodeInferenceFailed, "speech service rejected the request", false, nil)
	default:
		return NewError(CodeInferenceFailed, fmt.Sprintf("speech service returned HTTP %d", resp.StatusCode), false, nil)
	}
}

func contextSpeechError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return NewError(CodeTimeout, "speech transcription timed out", true, err)
	}
	return NewError(CodeCancelled, "speech transcription was cancelled", true, err)
}
