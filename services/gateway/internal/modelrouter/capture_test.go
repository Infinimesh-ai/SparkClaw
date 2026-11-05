package modelrouter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
)

func TestModelCaptureStoresDispatchedSchemaAndExcludesHeadersAndReasoning(t *testing.T) {
	fixture := newModelCaptureFixture(t, executionevidence.Options{})
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies <- raw
		if r.Header.Get("Authorization") != "Bearer test-private-provider-key" {
			t.Error("provider authorization changed")
		}
		_, _ = io.WriteString(w, `{"model":"resolved-provider-model","choices":[{"message":{"content":"  exact answer\n","reasoning":"hidden-provider-reasoning"},"finish_reason":"stop"}],"usage":{"prompt_tokens":13,"completion_tokens":4,"total_tokens":17}}`)
	}))
	defer server.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL + "/private-provider-url-secret"
	cfg.Model.APIKey = "test-private-provider-key"
	options := ChatOptions{ForceDisableThinking: true, StrictJSONSchema: &StrictJSONSchema{
		Name: "capture_answer", Schema: map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}, "required": []string{"answer"}, "additionalProperties": false},
	}}
	result, err := New(cfg).ChatWithProfileOptions(fixture.ctx, modelcapacity.OperationConversationAnswer, "fast", "system payload", "question payload", options)
	if err != nil || result.Content != "exact answer" || result.CaptureID == "" {
		t.Fatalf("captured call result changed: %#v %v", result, err)
	}
	manifest := fixture.finish(t, "completed")
	request := fixture.onlyPayload(t, manifest, "model_request")
	actual := <-bodies
	if !bytes.Equal(fixture.bytes(t, request), actual) {
		t.Fatal("captured request differs from actual HTTP dispatch body")
	}
	var body map[string]any
	_ = json.Unmarshal(actual, &body)
	if body["response_format"] == nil || body["chat_template_kwargs"] == nil || body["max_tokens"] == nil {
		t.Fatalf("actual post-admission request is incomplete: %s", actual)
	}
	response := fixture.bytes(t, fixture.onlyPayload(t, manifest, "model_response"))
	if !bytes.Contains(response, []byte(`  exact answer\n`)) || !bytes.Contains(response, []byte("resolved-provider-model")) {
		t.Fatalf("provider response content/model lost: %s", response)
	}
	fixture.assertAbsent(t, "test-private-provider-key", "private-provider-url-secret", "hidden-provider-reasoning")
	for _, event := range fixture.events(t) {
		if strings.HasPrefix(event.Kind, "model.http.") && event.CallID != result.CaptureID {
			t.Fatalf("transport call lost invocation correlation: %#v", event)
		}
	}
}

func TestModelCaptureIncludesExactImageInput(t *testing.T) {
	fixture := newModelCaptureFixture(t, executionevidence.Options{})
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies <- raw
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"image answer"}}]}`)
	}))
	defer server.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	input := ImageInput{ContentType: "image/png", Content: []byte("exact-image-bytes")}
	_, err := New(cfg).ChatWithImage(fixture.ctx, modelcapacity.OperationConversationAnswer, "fast", "image system", "image question", input)
	if err != nil {
		t.Fatal(err)
	}
	manifest := fixture.finish(t, "completed")
	raw := fixture.bytes(t, fixture.onlyPayload(t, manifest, "model_request"))
	if !bytes.Equal(raw, <-bodies) || !bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(input.Content))) {
		t.Fatal("actual image content was not preserved in dispatched request")
	}
}

func TestModelCaptureKeepsPartialStreamWhenConsumerCancels(t *testing.T) {
	fixture := newModelCaptureFixture(t, executionevidence.Options{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first answer\",\"reasoning_content\":\"hidden-stream-thought\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	ctx, cancel := context.WithCancel(fixture.ctx)
	defer cancel()
	result, err := New(cfg).ChatStreamWithProfile(ctx, modelcapacity.OperationConversationAnswer, "fast", "system", "question", func(event ModelStreamEvent) error {
		if event.Type == "text_delta" {
			cancel()
			return context.Canceled
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || result.Content != "" || result.CaptureID == "" {
		t.Fatalf("stream error behavior changed: %#v %v", result, err)
	}
	manifest := fixture.finish(t, "cancelled")
	partial := fixture.bytes(t, fixture.onlyPayload(t, manifest, "model_partial_result"))
	if !bytes.Contains(partial, []byte("first answer")) {
		t.Fatalf("observed stream prefix missing: %s", partial)
	}
	response := fixture.bytes(t, fixture.onlyPayload(t, manifest, "model_response"))
	if !bytes.Contains(response, []byte("first answer")) || !bytes.Contains(response, []byte(`"done_received":false`)) {
		t.Fatalf("partial provider stream not identified: %s", response)
	}
	fixture.assertAbsent(t, "hidden-stream-thought")
}

func TestModelCaptureFailureDoesNotRetryOrChangeAnswer(t *testing.T) {
	fixture := newModelCaptureFixture(t, executionevidence.Options{MaxPayloadBytes: 32})
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"answer still succeeds"}}]}`)
	}))
	defer server.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	result, err := New(cfg).ChatWithProfile(fixture.ctx, modelcapacity.OperationConversationAnswer, "fast", "system", "question")
	if err != nil || result.Content != "answer still succeeds" || count.Load() != 1 {
		t.Fatalf("capture failure changed model execution: %#v count=%d error=%v", result, count.Load(), err)
	}
	if err := fixture.recorder.Seal(t.Context(), fixture.scope, "completed"); err != nil {
		t.Fatal(err)
	}
	manifest, err := fixture.recorder.Read(t.Context(), fixture.scope)
	if err != nil || manifest.Capture.Complete || len(manifest.Capture.Gaps) == 0 {
		t.Fatalf("capture failure was hidden: %#v %v", manifest.Capture, err)
	}
}

func TestModelCaptureMockAndRejectedAdmissionHaveNoDispatch(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "mock", true: "rejected"}[reject], func(t *testing.T) {
			fixture := newModelCaptureFixture(t, executionevidence.Options{})
			cfg := configtest.MustLoadDefault()
			cfg.Model.Mock = true
			question := "short question"
			if reject {
				question = strings.Repeat("x", cfg.Model.Fast.ContextTokens*5)
			}
			result, err := New(cfg).ChatWithProfile(fixture.ctx, modelcapacity.OperationConversationAnswer, "fast", "system", question)
			if (err != nil) != reject || result.CaptureID == "" {
				t.Fatalf("unexpected admitted result: %#v %v", result, err)
			}
			status := "completed"
			if reject {
				status = "failed"
			}
			manifest := fixture.finish(t, status)
			fixture.onlyPayload(t, manifest, "model_prepared")
			for _, payload := range manifest.Payloads {
				if payload.Kind == "model_request" {
					t.Fatal("mock/rejected input was labeled as dispatched")
				}
			}
		})
	}
}

func TestModelCaptureGuardFallbackRetainsFailedTransport(t *testing.T) {
	fixture := newModelCaptureFixture(t, executionevidence.Options{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":"sensitive-provider-error-text"}`)
	}))
	defer server.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Guard.BaseURL = server.URL
	result, err := New(cfg).Guard(fixture.ctx, "ordinary request")
	if err != nil || !result.Mock || result.CaptureID == "" {
		t.Fatalf("guard fallback changed: %#v %v", result, err)
	}
	fixture.finish(t, "completed")
	fixture.assertAbsent(t, "sensitive-provider-error-text")
	found := false
	for _, event := range fixture.events(t) {
		if event.Kind == "model.http.finished" && event.Status == "failed" && event.CallID == result.CaptureID {
			found = true
		}
	}
	if !found {
		t.Fatal("successful fallback hid failed external guard attempt")
	}
}

func TestModelCaptureLeavesUnboundInvocationUnchanged(t *testing.T) {
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	result, err := New(cfg).ChatWithProfile(t.Context(), modelcapacity.OperationConversationAnswer, "fast", "system", "question")
	if err != nil || result.CaptureID != "" {
		t.Fatalf("unbound call unexpectedly captured: %#v %v", result, err)
	}
}

type modelCaptureFixture struct {
	root     string
	recorder *executionevidence.Recorder
	scope    executionevidence.Scope
	ctx      context.Context
}

func newModelCaptureFixture(t *testing.T, options executionevidence.Options) modelCaptureFixture {
	t.Helper()
	root := t.TempDir()
	recorder := executionevidence.New(root, options)
	scope := executionevidence.Scope{OwnerID: "capture-owner", SessionID: "capture-session", RunID: "capture-run"}
	if err := recorder.Begin(t.Context(), scope, "task"); err != nil {
		t.Fatal(err)
	}
	return modelCaptureFixture{root: root, recorder: recorder, scope: scope, ctx: recorder.Bind(t.Context(), scope)}
}

func (f modelCaptureFixture) finish(t *testing.T, status string) executionevidence.Manifest {
	t.Helper()
	if err := f.recorder.Seal(t.Context(), f.scope, status); err != nil {
		t.Fatal(err)
	}
	m, err := f.recorder.Read(t.Context(), f.scope)
	if err != nil || !m.Capture.Complete || len(m.Capture.OpenSpans) != 0 {
		t.Fatalf("capture did not close correctly: %#v %v", m.Capture, err)
	}
	return m
}

func (f modelCaptureFixture) onlyPayload(t *testing.T, m executionevidence.Manifest, kind string) executionevidence.Payload {
	t.Helper()
	var selected []executionevidence.Payload
	for _, p := range m.Payloads {
		if p.Kind == kind {
			selected = append(selected, p)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("payload kind %s count=%d want 1", kind, len(selected))
	}
	return selected[0]
}

func (f modelCaptureFixture) bytes(t *testing.T, payload executionevidence.Payload) []byte {
	t.Helper()
	var found []byte
	err := filepath.WalkDir(f.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != payload.SHA256 {
			return err
		}
		found, err = os.ReadFile(path)
		return err
	})
	if err != nil || found == nil {
		t.Fatalf("recorded payload %s unavailable: %v", payload.ID, err)
	}
	digest := sha256.Sum256(found)
	if hex.EncodeToString(digest[:]) != payload.SHA256 {
		t.Fatal("payload checksum mismatch")
	}
	return found
}

func (f modelCaptureFixture) events(t *testing.T) []executionevidence.Event {
	t.Helper()
	var events []executionevidence.Event
	err := filepath.WalkDir(f.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "events.jsonl" {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
			var event executionevidence.Event
			if err := json.Unmarshal(line, &event); err != nil {
				return err
			}
			events = append(events, event)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func (f modelCaptureFixture) assertAbsent(t *testing.T, secrets ...string) {
	t.Helper()
	err := filepath.WalkDir(f.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		for _, secret := range secrets {
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("excluded content persisted in %s", entry.Name())
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
