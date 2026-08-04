package documentocr

import (
	"context"
	"fmt"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type Request struct {
	Content     []byte
	ContentType string
}

type Result struct {
	Markdown    string
	Model       string
	InferenceMS int64
}

type Adapter interface {
	Enabled() bool
	Parse(context.Context, Request) (Result, error)
	Close() error
}

func Disabled() Adapter { return disabledAdapter{} }

func New(cfg config.DocumentOCRAdapterConfig) (Adapter, error) {
	if !cfg.Enabled || cfg.Provider == "" || cfg.Provider == "disabled" {
		return Disabled(), nil
	}
	switch cfg.Provider {
	case "openai-http":
		return NewOpenAIHTTP(cfg)
	default:
		return nil, fmt.Errorf("unsupported document OCR provider %q", cfg.Provider)
	}
}

type disabledAdapter struct{}

func (disabledAdapter) Enabled() bool { return false }

func (disabledAdapter) Parse(context.Context, Request) (Result, error) {
	return Result{}, fmt.Errorf("document OCR is disabled")
}

func (disabledAdapter) Close() error { return nil }
