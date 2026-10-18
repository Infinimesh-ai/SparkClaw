package agent

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

// FinalAnswerFormat is selected by the trusted workflow caller, never inferred
// from model text. Ordinary Markdown may start with a brace or a code fence.
type FinalAnswerFormat string

const (
	FinalAnswerMarkdown   FinalAnswerFormat = "markdown"
	FinalAnswerStructured FinalAnswerFormat = "final_json"
)

type finalAnswerFormatKey struct{}
type finalAnswerStartKey struct{}

func WithFinalAnswerFormat(ctx context.Context, format FinalAnswerFormat) context.Context {
	return context.WithValue(ctx, finalAnswerFormatKey{}, format)
}

// WithFinalAnswerStart lets the owning execution fence replacements, including
// a retry in the same model span, before any new answer text is emitted.
func WithFinalAnswerStart(ctx context.Context, start func()) context.Context {
	return context.WithValue(ctx, finalAnswerStartKey{}, start)
}

func startFinalAnswer(ctx context.Context) {
	if start, ok := ctx.Value(finalAnswerStartKey{}).(func()); ok && ctx.Err() == nil {
		start()
	}
}

func finalAnswerFormatFromContext(ctx context.Context) FinalAnswerFormat {
	if format, ok := ctx.Value(finalAnswerFormatKey{}).(FinalAnswerFormat); ok {
		return format
	}
	// Both production final-answer prompts request only user-visible content.
	return FinalAnswerMarkdown
}

func finalAnswerContent(content string, format FinalAnswerFormat) (string, error) {
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) || len(content) > app.WorkbenchResultBytes {
		return "", errors.New("invalid final answer text")
	}
	answer := content
	switch format {
	case FinalAnswerMarkdown:
	case FinalAnswerStructured:
		var envelope struct {
			Type   string `json:"type"`
			Answer string `json:"answer"`
		}
		if appstream.DecodeStrict([]byte(content), &envelope) != nil || envelope.Type != "final" {
			return "", errors.New("invalid final answer envelope")
		}
		answer = envelope.Answer
	default:
		return "", errors.New("unsupported final answer format")
	}
	answer = cleanUserFinalAnswer(answer)
	if answer == "" || strings.ContainsRune(answer, 0) || isBlockedFinalAnswer(answer) {
		return "", errors.New("workflow finalizer returned no usable answer")
	}
	return answer, nil
}

type workflowFinalAnswerStream struct {
	forward modelrouter.StreamHandler
	format  FinalAnswerFormat
	pending strings.Builder
	bytes   int
}

func (s *workflowFinalAnswerStream) emit(event modelrouter.ModelStreamEvent) error {
	switch event.Type {
	case "text_delta":
		if !utf8.ValidString(event.Text) || strings.ContainsRune(event.Text, 0) || len(event.Text) > app.WorkbenchResultBytes-s.bytes {
			return errors.New("final answer stream exceeds text limit")
		}
		s.bytes += len(event.Text)
		switch s.format {
		case FinalAnswerMarkdown:
			return s.forward(event)
		case FinalAnswerStructured:
			s.pending.WriteString(event.Text)
			return nil
		default:
			return errors.New("unsupported final answer format")
		}
	case "done":
		if s.format == FinalAnswerStructured {
			answer, err := finalAnswerContent(s.pending.String(), s.format)
			s.pending.Reset()
			if err != nil {
				return err
			}
			if err = s.forward(modelrouter.ModelStreamEvent{Type: "text_delta", Text: answer}); err != nil {
				return err
			}
		}
		return s.forward(event)
	case "error":
		s.pending.Reset()
		return s.forward(event)
	}
	// Tool arguments and reasoning are never answer-source events.
	return nil
}
