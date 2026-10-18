package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func (a *iscpDomainAdapter) executionProgressStream(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	emitter, ok := iscpworkbench.StreamFromContext(ctx)
	if !ok {
		return domainError(400, "stream_required")
	}
	input, err := execution.ParseAnswerStreamInput(request.Body)
	requestID := request.RequestID
	if err != nil || !execution.UUID(requestID) || (request.Params["request_id"] != "" && request.Params["request_id"] != requestID) {
		return domainError(400, "invalid_input")
	}
	principal, service, err := a.executionIdentity(ctx, request)
	if err != nil {
		return domainError(403, "installation_required")
	}
	sub, err := service.SubscribeAnswer(ctx, principal.OwnerID, principal.ClientID, request.InstallationID, requestID, input.InputDigest, input.Cursor)
	if errors.Is(err, execution.ErrAnswerUnavailable) {
		return domainJSON(200, map[string]any{"reason": "preview_unavailable"})
	}
	if err != nil {
		return domainExecutionError(err)
	}
	reason := forwardExecutionAnswer(ctx, sub, emitter.Send, emitter.Invalidate)
	return domainJSON(200, map[string]any{"reason": reason})
}

// One sending owner serializes the snapshot baseline and subsequent deltas.
// The first nonempty increment flushes immediately; later increments coalesce
// for at most 50 ms or 4 KiB, and business completion flushes the remainder.
// Send validates local stream authority in the Endpoint; it never waits for a
// remote authority query in this loop.
func forwardExecutionAnswer(ctx context.Context, sub *execution.AnswerSubscription, send, invalidate func(context.Context, json.RawMessage) error) string {
	var pending *execution.AnswerEvent
	firstText := true
	publish := func(event execution.AnswerEvent) error {
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if event.Kind == "invalidate" {
			return invalidate(ctx, raw)
		}
		return send(ctx, raw)
	}
	flush := func() error {
		if pending == nil {
			return nil
		}
		err := publish(*pending)
		pending = nil
		return err
	}
	var deadline time.Time
	for {
		readCtx := ctx
		cancel := func() {}
		if pending != nil {
			readCtx, cancel = context.WithDeadline(ctx, deadline)
		}
		event, err := sub.Next(readCtx)
		cancel()
		if err != nil {
			if pending != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				if flush() != nil {
					return "reset"
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				if flush() != nil {
					return "reset"
				}
				return "terminal"
			}
			if errors.Is(err, execution.ErrAnswerGap) {
				return "gap"
			}
			return "canceled"
		}
		if event.Kind == "invalidate" {
			pending = nil // Never flush stale text ahead of invalidation.
			if publish(event) != nil {
				return "reset"
			}
			if event.Reason == "preview_unavailable" {
				return "preview_unavailable"
			}
			return "reset"
		}
		if event.Kind != "delta" {
			if flush() != nil || publish(event) != nil {
				return "reset"
			}
			continue
		}
		if firstText {
			firstText = false
			if publish(event) != nil {
				return "reset"
			}
			continue
		}
		if pending != nil && (pending.ToOffset != event.FromOffset || len(pending.Text)+len(event.Text) > execution.AnswerDeltaBytes) {
			if flush() != nil {
				return "reset"
			}
		}
		if pending == nil {
			pending = &event
			deadline = time.Now().Add(50 * time.Millisecond)
		} else {
			pending.Text += event.Text
			pending.ToOffset = event.ToOffset
			pending.Revision = event.Revision
			pending.ContentDigest = event.ContentDigest
		}
		if len(pending.Text) >= execution.AnswerDeltaBytes {
			if flush() != nil {
				return "reset"
			}
		}
	}
}
