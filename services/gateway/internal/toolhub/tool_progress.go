package toolhub

import (
	"context"
	"strings"
)

type toolProgressKey struct{}

// WithProgressReporter lets a long-running tool publish a short owner-facing
// note on its running call. The agent owns persistence and delivery.
func WithProgressReporter(ctx context.Context, report func(string)) context.Context {
	if report == nil {
		return ctx
	}
	return context.WithValue(ctx, toolProgressKey{}, report)
}

func reportToolProgress(ctx context.Context, text string) {
	if report, ok := ctx.Value(toolProgressKey{}).(func(string)); ok && strings.TrimSpace(text) != "" {
		report(text)
	}
}
