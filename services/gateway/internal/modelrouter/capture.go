package modelrouter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
)

// CaptureMetadata describes a model invocation without changing its routing.
// The OCR adapter also uses it, while retaining its own provider and admission.
type CaptureMetadata struct {
	Operation string
	Lane      string
	Profile   string
	Model     string
	Mock      bool
}

type capturedCallKey struct{}

// CapturedCall links one model operation to its actual transport attempts. An
// embedding operation can have multiple batches, each with its own HTTP span.
type CapturedCall struct {
	ctx      context.Context
	id       string
	started  time.Time
	metadata CaptureMetadata
	attempts atomic.Int32
}

// BeginCapturedCall is a no-op without an admitted execution recorder. It must
// precede admission so a rejected request remains distinct from one dispatched.
func BeginCapturedCall(ctx context.Context, metadata CaptureMetadata) (context.Context, *CapturedCall) {
	if _, bound := executionevidence.ScopeFromContext(ctx); !bound {
		return ctx, nil
	}
	call := &CapturedCall{ctx: ctx, id: app.NewID("model"), started: time.Now().UTC(), metadata: metadata}
	call.record(executionevidence.Event{
		ID: call.id, Kind: "model.call.started", SpanID: call.id, CallID: call.id,
		Status: "started", At: call.started, Data: call.fields(),
	})
	ctx = executionevidence.WithParent(ctx, call.id)
	return context.WithValue(ctx, capturedCallKey{}, call), call
}

func (c *CapturedCall) ID() string {
	if c == nil {
		return ""
	}
	return c.id
}

// Prepared records unsent semantic inputs only when admission rejects them, or
// when a mock serves them. It must never be mistaken for an HTTP request body.
func (c *CapturedCall) Prepared(input any, mocked bool) {
	if c == nil {
		return
	}
	status := "not_dispatched"
	if mocked {
		status = "mocked"
	}
	refs := c.payload("model_prepared", c.id+"-prepared.json", input)
	c.record(executionevidence.Event{
		Kind: "model.input.prepared", CallID: c.id, ParentID: c.id, Status: status,
		PayloadIDs: refs, Data: map[string]any{"mock": mocked, "http_dispatched": false},
	})
}

func (c *CapturedCall) observedOutput(output any) {
	if c == nil {
		return
	}
	c.record(executionevidence.Event{
		Kind: "model.output.observed", CallID: c.id, ParentID: c.id, Status: "partial",
		PayloadIDs: c.payload("model_partial_result", c.id+"-partial.json", output),
	})
}

// Finish records the application-visible output and status. Capture failure is
// deliberately not returned to the caller: it cannot retry a business action.
func (c *CapturedCall) Finish(output any, err error) {
	if c == nil {
		return
	}
	status, code := "completed", ""
	if err != nil {
		status, code = "failed", captureErrorCode(err)
		if c.ctx.Err() != nil {
			code = captureErrorCode(c.ctx.Err())
		}
		if errors.Is(err, context.Canceled) || errors.Is(c.ctx.Err(), context.Canceled) {
			status = "cancelled"
		}
	}
	data := c.fields()
	if err != nil {
		// Error text can contain request content, provider URLs or credentials.
		// Typed codes and the separately captured response are sufficient here.
		data["error_type"] = fmt.Sprintf("%T", err)
	}
	c.record(executionevidence.Event{
		Kind: "model.call.finished", SpanID: c.id, CallID: c.id, ParentID: c.id,
		Status: status, ErrorCode: code, PayloadIDs: c.payload("model_result", c.id+"-result.json", output),
		Data: data, DurationMS: time.Since(c.started).Milliseconds(),
	})
}

func (c *CapturedCall) fields() map[string]any {
	return map[string]any{
		"operation": c.metadata.Operation, "lane": c.metadata.Lane,
		"profile": c.metadata.Profile, "model": c.metadata.Model, "mock": c.metadata.Mock,
	}
}

func (c *CapturedCall) payload(kind, name string, value any) []string {
	payload, err := executionevidence.CaptureJSON(context.WithoutCancel(c.ctx), kind, name, value)
	if err != nil {
		captureFailure(c.ctx, c.id, "model_payload_capture_failed")
		return nil
	}
	return []string{payload.ID}
}

func (c *CapturedCall) record(event executionevidence.Event) {
	if err := executionevidence.Record(context.WithoutCancel(c.ctx), event); err != nil {
		captureFailure(c.ctx, c.id, "model_event_capture_failed")
	}
}

func captureFailure(ctx context.Context, callID, code string) {
	_ = executionevidence.MarkGap(context.WithoutCancel(ctx), code, callID)
	slog.Warn("model execution evidence is incomplete", "call_id", callID, "code", code)
}

func captureErrorCode(err error) string {
	var tooLong *InputTooLongError
	var timeout interface{ Timeout() bool }
	switch {
	case errors.Is(err, context.Canceled):
		return "model_call_cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "model_call_timeout"
	case errors.As(err, &timeout) && timeout.Timeout():
		return "model_call_timeout"
	case errors.As(err, &tooLong):
		return "model_input_too_long"
	case IsUnavailable(err):
		return "model_unavailable"
	default:
		return "model_call_failed"
	}
}
