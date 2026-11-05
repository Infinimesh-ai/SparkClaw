package modelrouter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
)

// This bounds observation only. An oversized response remains governed by the
// adapter's own limit; capture must not alter the bytes/errors it receives.
const maxCapturedHTTPBytes = 32 << 20

type captureTransport struct{ base http.RoundTripper }

// NewCaptureTransport records model HTTP bodies only in an admitted run context.
// Headers, URL credentials and hidden reasoning are never written to evidence.
func NewCaptureTransport(base http.RoundTripper) http.RoundTripper {
	return captureTransport{base: base}
}

func (t captureTransport) transport() http.RoundTripper {
	if t.base == nil {
		return http.DefaultTransport
	}
	return t.base
}

func (t captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, bound := executionevidence.ScopeFromContext(req.Context()); !bound {
		return t.transport().RoundTrip(req)
	}
	ctx := req.Context()
	span := app.NewID("model_http")
	callID, attempt := "", 1
	if call, ok := ctx.Value(capturedCallKey{}).(*CapturedCall); ok {
		callID = call.ID()
		attempt = int(call.attempts.Add(1))
	}
	started := time.Now().UTC()
	refs := captureRequestBody(ctx, span, req)
	data := map[string]any{"method": req.Method, "endpoint_path": captureEndpointPath(req.URL.Path), "dispatch_attempted": true}
	recordHTTPEvent(ctx, executionevidence.Event{
		ID: span, Kind: "model.http.dispatched", SpanID: span, CallID: callID,
		ParentID: callID, Attempt: attempt, Status: "dispatched", PayloadIDs: refs, At: started, Data: data,
	})
	resp, err := t.transport().RoundTrip(req)
	if err != nil {
		status := "failed"
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			status = "cancelled"
		}
		recordHTTPEvent(ctx, executionevidence.Event{
			Kind: "model.http.finished", SpanID: span, CallID: callID, ParentID: span, Attempt: attempt,
			Status: status, ErrorCode: captureErrorCode(err), DurationMS: time.Since(started).Milliseconds(),
		})
		return resp, err
	}
	resp.Body = &capturedResponseBody{
		body: resp.Body, ctx: ctx, span: span, callID: callID, started: started,
		status: resp.StatusCode, attempt: attempt, stream: strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") || req.Header.Get("Accept") == "text/event-stream",
	}
	return resp, nil
}

// Keep only the provider operation: arbitrary deployment/path prefixes may
// contain private routing data or credentials just like URL query parameters.
func captureEndpointPath(path string) string {
	for _, suffix := range []string{"/chat/completions", "/embeddings", "/tokenize"} {
		if strings.HasSuffix(path, suffix) {
			return suffix
		}
	}
	return "unknown"
}

func (t captureTransport) CloseIdleConnections() {
	if closer, ok := t.transport().(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func captureRequestBody(ctx context.Context, span string, req *http.Request) []string {
	if req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if req.GetBody == nil {
		captureFailure(ctx, span, "model_request_body_unavailable")
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		captureFailure(ctx, span, "model_request_body_unavailable")
		return nil
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, maxCapturedHTTPBytes+1))
	if err != nil || len(raw) > maxCapturedHTTPBytes {
		captureFailure(ctx, span, "model_request_capture_limit")
		return nil
	}
	payload, err := executionevidence.Capture(context.WithoutCancel(ctx), "model_request", span+"-request.json", "application/json", bytes.NewReader(raw))
	if err != nil {
		captureFailure(ctx, span, "model_request_capture_failed")
		return nil
	}
	return []string{payload.ID}
}

type capturedResponseBody struct {
	body     io.ReadCloser
	ctx      context.Context
	span     string
	callID   string
	started  time.Time
	status   int
	attempt  int
	mu       sync.Mutex
	recorded bool
	stream   bool
	buffer   bytes.Buffer
	bytes    int64
	readErr  error
	closed   sync.Once
}

func (b *capturedResponseBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.recorded && n != 0 {
		captureFailure(b.ctx, b.span, "model_response_read_after_capture")
		return n, err
	}
	b.bytes += int64(n)
	if remaining := maxCapturedHTTPBytes - b.buffer.Len(); remaining > 0 {
		_, _ = b.buffer.Write(p[:min(n, remaining)])
	}
	if err != nil {
		b.readErr = err
	}
	return n, err
}

func (b *capturedResponseBody) Close() error {
	err := b.body.Close()
	b.closed.Do(b.record)
	return err
}

func (b *capturedResponseBody) record() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recorded = true
	status, code := "completed", ""
	data := map[string]any{"http_status": b.status, "observed_bytes": b.bytes, "stream": b.stream, "body_eof_observed": errors.Is(b.readErr, io.EOF)}
	if b.status < 200 || b.status >= 300 {
		status, code = "failed", "model_http_rejected"
		// Adapters may intentionally leave error bodies unread. Do not drain them
		// or persist arbitrary error text that could echo authorization material.
		data["response_body_omitted"] = "http_error_body"
		captureFailure(b.ctx, b.span, "model_http_error_body_omitted")
	}
	if b.readErr != nil && !errors.Is(b.readErr, io.EOF) {
		status, code = "failed", "model_response_read_failed"
	}
	if b.ctx.Err() != nil {
		status, code = "cancelled", captureErrorCode(b.ctx.Err())
	}
	b.recordTermination(data)
	var refs []string
	if b.bytes > maxCapturedHTTPBytes {
		captureFailure(b.ctx, b.span, "model_response_capture_limit")
		data["capture_truncated"] = true
	} else if b.status >= 200 && b.status < 300 && b.buffer.Len() != 0 {
		refs = b.captureResponse(data)
	}
	recordHTTPEvent(b.ctx, executionevidence.Event{
		Kind: "model.http.finished", SpanID: b.span, CallID: b.callID, ParentID: b.span,
		Status: status, ErrorCode: code, Attempt: b.attempt, PayloadIDs: refs, Data: data,
		DurationMS: time.Since(b.started).Milliseconds(),
	})
}

// A canceled operation can have complete evidence of its observed prefix.
// Without cancellation, an unexplained early close leaves an evidence gap.
func (b *capturedResponseBody) recordTermination(data map[string]any) {
	complete := errors.Is(b.readErr, io.EOF)
	termination := "eof"
	if b.stream {
		value, _ := capturedStreamResponse(b.buffer.Bytes())
		complete, _ = value.(map[string]any)["done_received"].(bool)
		termination = "stream_done"
	}
	if b.ctx.Err() != nil {
		complete = false
		termination = "canceled"
		if errors.Is(b.ctx.Err(), context.DeadlineExceeded) {
			termination = "deadline_exceeded"
		}
	} else if b.readErr != nil && !errors.Is(b.readErr, io.EOF) {
		complete, termination = false, "read_error"
		captureFailure(b.ctx, b.span, "model_response_read_incomplete")
	} else if !complete {
		termination = "closed_before_completion"
		captureFailure(b.ctx, b.span, "model_response_terminated_early")
	}
	if b.status < 200 || b.status >= 300 {
		complete, termination = false, "http_error_body_omitted"
	}
	data["response_complete"], data["termination"] = complete, termination
}

func (b *capturedResponseBody) captureResponse(data map[string]any) []string {
	raw := b.buffer.Bytes()
	digest := sha256.Sum256(raw)
	data["observed_body_sha256"] = hex.EncodeToString(digest[:])
	var value any
	var err error
	if b.stream {
		value, err = capturedStreamResponse(raw)
		data["representation"] = "stream_events_without_hidden_reasoning"
	} else {
		err = json.Unmarshal(raw, &value)
		data["representation"] = "provider_json_without_hidden_reasoning"
	}
	if err != nil {
		captureFailure(b.ctx, b.span, "model_response_capture_unparseable")
		data["response_parseable"] = false
		if !b.stream {
			return nil
		}
	}
	value, removed := removeHiddenReasoning(value)
	data["hidden_reasoning_removed"] = removed
	payload, err := executionevidence.CaptureJSON(context.WithoutCancel(b.ctx), "model_response", b.span+"-response.json", value)
	if err != nil {
		captureFailure(b.ctx, b.span, "model_response_capture_failed")
		return nil
	}
	return []string{payload.ID}
}

func capturedStreamResponse(raw []byte) (any, error) {
	events := []any{}
	done := false
	partial := false
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			if len(line) != 0 && !bytes.HasPrefix(line, []byte(":")) && !bytes.HasPrefix(line, []byte("event:")) && !bytes.HasPrefix(line, []byte("id:")) && !bytes.HasPrefix(line, []byte("retry:")) {
				partial = true
			}
			continue
		}
		content := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(content, []byte("[DONE]")) {
			done = true
			break
		}
		var event any
		if err := json.Unmarshal(content, &event); err != nil {
			partial = true
			continue
		}
		events = append(events, event)
	}
	value := map[string]any{"events": events, "done_received": done, "incomplete_event": partial}
	if partial {
		return value, errors.New("stream contains an incomplete event")
	}
	return value, nil
}

func removeHiddenReasoning(value any) (any, bool) {
	removed := false
	switch typed := value.(type) {
	case map[string]any:
		for key, entry := range typed {
			if key == "reasoning" || key == "reasoning_content" {
				delete(typed, key)
				removed = true
				continue
			}
			filtered, changed := removeHiddenReasoning(entry)
			typed[key], removed = filtered, removed || changed
		}
	case []any:
		for i, entry := range typed {
			filtered, changed := removeHiddenReasoning(entry)
			typed[i], removed = filtered, removed || changed
		}
	}
	return value, removed
}

func recordHTTPEvent(ctx context.Context, event executionevidence.Event) {
	if err := executionevidence.Record(context.WithoutCancel(ctx), event); err != nil {
		captureFailure(ctx, event.SpanID, "model_http_event_capture_failed")
	}
}
