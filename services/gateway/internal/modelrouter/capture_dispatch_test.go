package modelrouter

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
)

func TestModelCaptureCorrelatesEveryEmbeddingBatch(t *testing.T) {
	fixture := newModelCaptureFixture(t, executionevidence.Options{})
	actual := make(chan []byte, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		actual <- raw
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		vectors := make([]map[string]any, len(body.Input))
		for i, input := range body.Input {
			value, _ := strconv.Atoi(input)
			vectors[i] = map[string]any{"index": i, "embedding": []float64{float64(value)}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": vectors})
	}))
	defer server.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Embedding.BaseURL = server.URL
	inputs := make([]string, 65)
	for i := range inputs {
		inputs[i] = strconv.Itoa(i)
	}
	result, err := New(cfg).Embed(fixture.ctx, modelcapacity.OperationIntentCatalogEmbedding, inputs)
	if err != nil || len(result.Vectors) != 65 || result.Vectors[64][0] != 64 || result.CaptureID == "" {
		t.Fatalf("embedding result changed: %#v %v", result, err)
	}
	manifest := fixture.finish(t, "completed")
	requests := 0
	for _, payload := range manifest.Payloads {
		if payload.Kind == "model_request" {
			requests++
			if !bytes.Equal(fixture.bytes(t, payload), <-actual) {
				t.Fatal("captured embedding batch differs from HTTP body")
			}
		}
	}
	if requests != 2 {
		t.Fatalf("captured %d batches, want 2", requests)
	}
	var attempts []int
	for _, event := range fixture.events(t) {
		if event.Kind == "model.http.dispatched" {
			if event.CallID != result.CaptureID {
				t.Fatal("embedding batch lost its parent invocation")
			}
			attempts = append(attempts, event.Attempt)
		}
	}
	if !slices.Equal(attempts, []int{1, 2}) {
		t.Fatalf("dispatch order missing: %v", attempts)
	}
}

func TestModelCaptureMalformedProviderOutputMarksGapWithoutResend(t *testing.T) {
	fixture := newModelCaptureFixture(t, executionevidence.Options{})
	calls := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls <- struct{}{}
		_, _ = io.WriteString(w, `{"choices":[],"reasoning_content":"private unfinished thought`)
	}))
	defer server.Close()
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	result, err := New(cfg).ChatWithProfile(fixture.ctx, modelcapacity.OperationConversationAnswer, "fast", "system", "question")
	if err == nil || result.CaptureID == "" || len(calls) != 1 {
		t.Fatalf("invalid response behavior changed: %#v %v calls=%d", result, err, len(calls))
	}
	if err := fixture.recorder.Seal(t.Context(), fixture.scope, "failed"); err != nil {
		t.Fatal(err)
	}
	manifest, err := fixture.recorder.Read(t.Context(), fixture.scope)
	if err != nil || manifest.Capture.Complete || len(manifest.Capture.Gaps) == 0 || len(manifest.Capture.OpenSpans) != 0 {
		t.Fatalf("invalid output evidence must be diagnostic: %#v %v", manifest.Capture, err)
	}
	fixture.assertAbsent(t, "private unfinished thought")
}

func TestModelCaptureUnboundTransportPassesRequestAndBodyThrough(t *testing.T) {
	body := io.NopCloser(strings.NewReader("unchanged"))
	response := &http.Response{StatusCode: http.StatusOK, Body: body}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://example.invalid/model", strings.NewReader("input"))
	if err != nil {
		t.Fatal(err)
	}
	request.GetBody = func() (io.ReadCloser, error) {
		t.Error("unbound transport copied request")
		return nil, nil
	}
	transport := NewCaptureTransport(captureTestTransport(func(actual *http.Request) (*http.Response, error) {
		if actual != request {
			t.Error("unbound request was replaced")
		}
		return response, nil
	}))
	actual, err := transport.RoundTrip(request)
	if err != nil || actual != response || actual.Body != body {
		t.Fatalf("unbound response was wrapped: %#v %v", actual, err)
	}
}

type captureTestTransport func(*http.Request) (*http.Response, error)

func (f captureTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
