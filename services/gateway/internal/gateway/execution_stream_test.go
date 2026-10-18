package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// This exercises the actual Gateway workflow caller, Agent final-answer
// stream, Service projection and subscription, with the configured mock model.
// It is not a real-model/Relay/UI latency qualification.
func TestExecutionAnswerSourceProductionWorkflowCaller(t *testing.T) {
	s, _, _, handler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, handler)
	e := workbenchISCPEnvelope()
	answer := "{ordinary Markdown} " + strings.Repeat("你好", 100)
	e.Messages = []execution.Message{{Role: "user", Content: "Say hello.\nMOCK_CONVERSATION_RESPONSE:" + answer}}
	raw, _ := json.Marshal(e)
	if r := handler(t.Context(), workbenchISCPRequest(wb.OperationSubmit, raw)); r.Status != 202 {
		t.Fatal(r)
	}
	s.executions.Wait()
	sub, err := s.executions.SubscribeAnswer(t.Context(), e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, execution.Digest(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for {
		event, err := sub.Next(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == "snapshot_chunk" {
			text += event.Text
		}
		if event.Kind == "snapshot_end" {
			if execution.Digest([]byte(text)) != event.ContentDigest {
				t.Fatal("snapshot digest mismatch")
			}
			break
		}
	}
	if text != answer {
		status, _ := s.executions.Lookup(e.OwnerID, e.ClientID, e.RequestID)
		t.Fatalf("production answer source truncated: bytes=%d want=%d status=%+v result=%+v", len(text), len(answer), status, status.Result)
	}
	status, err := s.executions.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || status.Result == nil {
		t.Fatal(status, err)
	}
	var payload execution.Payload
	if json.Unmarshal([]byte(status.Result.Payload), &payload) != nil || payload.Content != answer {
		t.Fatal("authoritative result differs")
	}
}

// Real encrypted Endpoints and Gateway domain dispatch with a controlled
// executor. Relay delivery here is an in-process queue, not hosted Relay/UI.
func TestExecutionAnswerEncryptedProductionHandlerBeforeCompletion(t *testing.T) {
	started := make(chan context.Context, 1)
	finish := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	end := func() { once.Do(func() { close(finish) }) }
	t.Cleanup(end)
	s, _, cfg, _ := workbenchISCPFixture(t, func(ctx context.Context, _ execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		calls.Add(1)
		started <- ctx
		select {
		case <-finish:
			return execution.Output{Content: strings.Repeat("中文", 6)}, nil
		case <-ctx.Done():
			return execution.Output{}, ctx.Err()
		}
	})
	f := startV2EncryptedFixture(t, s, cfg, nil, []string{wb.OperationExecutionProgressStream})
	requireV2Status(t, f.jsonCall(t, wb.OperationBind, map[string]any{"schema_version": 1, "installation_id": iscpTestInstallation}), 200)
	raw, _ := json.Marshal(workbenchISCPEnvelope())
	digest := execution.Digest(raw)
	requireV2Status(t, f.call(t, v2Request(wb.OperationSubmit, raw)), 202)
	run := <-started
	open, _ := json.Marshal(execution.AnswerStreamInput{SchemaVersion: 1, InputDigest: digest, LatestOpenAt: time.Now().UTC().Add(10 * time.Second)})
	events := make(chan execution.AnswerEvent, 20)
	result := make(chan error, 1)
	go func() {
		response, err := f.client.StreamCall(f.ctx, v2Request(wb.OperationExecutionProgressStream, open), func(raw json.RawMessage) error {
			frame, err := appstream.Decode(raw)
			if err != nil {
				return err
			}
			event, err := execution.ParseAnswerEvent(frame.Body)
			if err == nil {
				events <- event
			}
			return err
		})
		if err == nil && response.Status != 200 {
			err = execution.ErrUnavailable
		}
		result <- err
	}()
	next := func() execution.AnswerEvent {
		select {
		case e := <-events:
			return e
		case err := <-result:
			t.Fatal("subscription ended early", err)
		case <-time.After(3 * time.Second):
			t.Fatal("encrypted answer timed out")
		}
		return execution.AnswerEvent{}
	}
	for next().Kind != "state" {
	}
	text := ""
	for range 6 {
		execution.PublishAnswer(run, "中文")
		event := next()
		if event.Kind != "delta" || event.FromOffset != uint64(len(text)) {
			t.Fatal("invalid incremental delivery", event)
		}
		text += event.Text
		if event.ContentDigest != execution.Digest([]byte(text)) {
			t.Fatal("digest changed in transport")
		}
		if status, err := s.executions.Lookup("iscp-owner", "iscp-desktop-client", iscpTestRequest); err != nil || status.State != "running" || status.Result != nil {
			t.Fatal("stream waited until result", status, err)
		}
	}
	end()
	if e := next(); e.Kind != "state" || e.State != "completed" {
		t.Fatal(e)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	lookup := f.call(t, v2Request(wb.OperationLookup, nil))
	requireV2Status(t, lookup, 200)
	var status execution.Status
	if json.Unmarshal(lookup.Body, &status) != nil || status.Result == nil || execution.Digest([]byte(status.Result.Payload)) != status.Result.Digest {
		t.Fatal("invalid original result")
	}
	requireV2Status(t, f.jsonCall(t, wb.OperationAck, map[string]any{"sequence": 1, "digest": status.Result.Digest, "durable": true}), 200)
	if calls.Load() != 1 || text != strings.Repeat("中文", 6) {
		t.Fatal("subscription repeated execution or lost text", calls.Load(), text)
	}
}

func TestExecutionAnswerForwardFirstBatchCompletionAndObservationCancel(t *testing.T) {
	started := make(chan context.Context, 1)
	finish := make(chan struct{})
	s, _, _, handler := workbenchISCPFixture(t, func(ctx context.Context, _ execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		started <- ctx
		select {
		case <-finish:
			return execution.Output{Content: "abc"}, nil
		case <-ctx.Done():
			return execution.Output{}, ctx.Err()
		}
	})
	bindWorkbenchISCP(t, handler)
	e := workbenchISCPEnvelope()
	raw, _ := json.Marshal(e)
	if r := handler(t.Context(), workbenchISCPRequest(wb.OperationSubmit, raw)); r.Status != 202 {
		t.Fatal(r)
	}
	run := <-started
	sub, err := s.executions.SubscribeAnswer(t.Context(), e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, execution.Digest(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan execution.AnswerEvent, 20)
	done := make(chan string, 1)
	send := func(_ context.Context, raw json.RawMessage) error {
		event, err := execution.ParseAnswerEvent(raw)
		if err == nil {
			events <- event
		}
		return err
	}
	go func() { done <- forwardExecutionAnswer(t.Context(), sub, send, send) }()
	for event := range events {
		if event.Kind == "state" {
			break
		}
	}
	execution.PublishAnswer(run, "a")
	select {
	case event := <-events:
		if event.Kind != "delta" || event.Text != "a" {
			t.Fatal(event)
		}
	case <-time.After(time.Second):
		t.Fatal("first increment buffered")
	}
	execution.PublishAnswer(run, "b")
	execution.PublishAnswer(run, "c")
	close(finish)
	text := "a"
	for event := range events {
		if event.Kind == "delta" {
			text += event.Text
		}
		if event.Kind == "state" && event.State == "completed" {
			break
		}
	}
	if reason := <-done; reason != "terminal" || text != "abc" {
		t.Fatal(reason, text)
	}
}
