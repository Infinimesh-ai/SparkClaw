package speech

import (
	"context"
	"errors"
	"fmt"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const (
	StateDisabled    = "disabled"
	StateUnavailable = "unavailable"
	StateWarming     = "warming"
	StateReady       = "ready"
	StateBusy        = "busy"
)

const (
	CodeInvalidRequest  = "speech_invalid_request"
	CodeTooShort        = "speech_too_short"
	CodeTooLarge        = "speech_too_large"
	CodeUnsupported     = "speech_unsupported_format"
	CodeBusy            = "speech_busy"
	CodeCancelled       = "speech_cancelled"
	CodeDisabled        = "speech_disabled"
	CodeUnavailable     = "speech_model_unavailable"
	CodeTimeout         = "speech_timeout"
	CodeInferenceFailed = "speech_inference_failed"
	CodeStreamOverrun   = "speech_stream_overrun"
	CodeStreamProtocol  = "speech_stream_protocol_error"
)

const (
	RealtimeProtocol       = "sparkclaw.speech.realtime.v1"
	RealtimeSampleRate     = 16000
	RealtimeFrameMS        = 100
	RealtimeFrameSamples   = 1600
	RealtimeMaxUnackedMS   = 5000
	RealtimeConnectTimeout = 5
	RealtimeFinalTimeout   = 12
	RealtimeTicketTTL      = 30
)

type RealtimeCapabilities struct {
	Protocol      string `json:"protocol"`
	SampleRate    int    `json:"sample_rate"`
	Channels      int    `json:"channels"`
	BitsPerSample int    `json:"bits_per_sample"`
	FrameMS       int    `json:"frame_ms"`
}

type Status struct {
	Enabled              bool                  `json:"enabled"`
	Ready                bool                  `json:"ready"`
	State                string                `json:"state"`
	Backend              string                `json:"backend"`
	Model                string                `json:"model"`
	SupportsStreaming    bool                  `json:"supports_streaming"`
	AcceptedContentTypes []string              `json:"accepted_content_types"`
	MaxAudioSeconds      int                   `json:"max_audio_seconds"`
	MaxUploadBytes       int64                 `json:"max_upload_bytes"`
	Reason               string                `json:"reason,omitempty"`
	Realtime             *RealtimeCapabilities `json:"realtime,omitempty"`
}

type Request struct {
	RequestID  string
	SessionID  string
	Language   string
	PCM16WAV   []byte
	DurationMS int64
}

type Result struct {
	Text        string
	Language    string
	Model       string
	InferenceMS int64
}

type RealtimeRequest struct {
	RequestID       string
	SessionID       string
	Language        string
	MaxAudioSeconds int
}

type RealtimeFormat struct {
	SampleRate    int `json:"sample_rate"`
	Channels      int `json:"channels"`
	BitsPerSample int `json:"bits_per_sample"`
	FrameMS       int `json:"frame_ms"`
}

type RealtimeLimits struct {
	MaxAudioSeconds int `json:"max_audio_seconds"`
	MaxFrameSamples int `json:"max_frame_samples"`
}

type RealtimeEvent struct {
	Event            string          `json:"event"`
	Protocol         string          `json:"protocol,omitempty"`
	Format           *RealtimeFormat `json:"format,omitempty"`
	Limits           *RealtimeLimits `json:"limits,omitempty"`
	AcceptedSequence *uint32         `json:"accepted_sequence,omitempty"`
	ReceivedAudioMS  int64           `json:"received_audio_ms,omitempty"`
	Revision         int64           `json:"revision,omitempty"`
	Text             string          `json:"text,omitempty"`
	Language         string          `json:"language,omitempty"`
	AudioEndMS       int64           `json:"audio_end_ms,omitempty"`
	DurationMS       int64           `json:"duration_ms,omitempty"`
	InferenceMS      int64           `json:"inference_ms,omitempty"`
	StopReason       string          `json:"stop_reason,omitempty"`
	Model            string          `json:"model,omitempty"`
	Code             string          `json:"code,omitempty"`
	Retryable        bool            `json:"retryable,omitempty"`
}

type RealtimeSession interface {
	ReadyEvent() RealtimeEvent
	WriteAudio(context.Context, uint32, []byte) error
	Finish(context.Context, uint32, int64, string) error
	Cancel(context.Context, uint32) error
	ReadEvent(context.Context) (RealtimeEvent, error)
	Close() error
}

type Transcriber interface {
	Status(context.Context) Status
	Transcribe(context.Context, Request) (Result, error)
	StartRealtime(context.Context, RealtimeRequest) (RealtimeSession, error)
	Close() error
}

type Error struct {
	Code      string
	Message   string
	Retryable bool
	Err       error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewError(code, message string, retryable bool, err error) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable, Err: err}
}

func ErrorDetails(err error) (code string, retryable bool) {
	var speechErr *Error
	if errors.As(err, &speechErr) {
		if speechErr.Code == "" {
			return CodeInferenceFailed, speechErr.Retryable
		}
		return speechErr.Code, speechErr.Retryable
	}
	return CodeInferenceFailed, false
}

func New(cfg config.SpeechConfig) (Transcriber, error) {
	switch cfg.Backend {
	case "", "disabled":
		return NewDisabled(cfg), nil
	case "openai-http":
		return NewOpenAIHTTP(cfg)
	default:
		return nil, fmt.Errorf("unsupported speech backend %q", cfg.Backend)
	}
}

func baseStatus(cfg config.SpeechConfig) Status {
	return Status{
		Enabled:              cfg.Enabled,
		Ready:                false,
		State:                StateUnavailable,
		Backend:              cfg.Backend,
		Model:                cfg.Model,
		SupportsStreaming:    false,
		AcceptedContentTypes: []string{"audio/wav"},
		MaxAudioSeconds:      cfg.MaxAudioSeconds,
		MaxUploadBytes:       cfg.MaxUploadBytes,
	}
}
