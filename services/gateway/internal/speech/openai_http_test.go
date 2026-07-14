package speech

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestOpenAIHTTPTranscriberStatusAndTranscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("model") != "test-asr" || r.FormValue("language") != "en" || r.FormValue("response_format") != "json" {
				t.Fatalf("unexpected upstream form: model=%q language=%q response_format=%q", r.FormValue("model"), r.FormValue("language"), r.FormValue("response_format"))
			}
			if r.Header.Get("X-SparkClaw-Request-ID") != "voice-test" {
				t.Fatalf("request ID header missing: %#v", r.Header)
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			file.Close()
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "transcribed by the real protocol"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	transcriber, err := NewOpenAIHTTP(testSpeechConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer transcriber.Close()
	status := transcriber.Status(context.Background())
	if !status.Ready || status.State != StateReady || status.Model != "test-asr" || status.Backend != "openai-http" {
		t.Fatalf("unexpected status: %#v", status)
	}
	result, err := transcriber.Transcribe(context.Background(), Request{
		RequestID: "voice-test",
		SessionID: "ses-test",
		Language:  "en",
		PCM16WAV:  testWAV(16000, 1, 16, 8000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "transcribed by the real protocol" || result.Language != "en" || result.Model != "test-asr" || result.InferenceMS < 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestOpenAIHTTPTranscriberTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		time.Sleep(100 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "too late"})
	}))
	defer server.Close()

	transcriber, err := NewOpenAIHTTP(testSpeechConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = transcriber.Transcribe(ctx, Request{RequestID: "voice-timeout", PCM16WAV: testWAV(16000, 1, 16, 8000)})
	if errorCode(err) != CodeTimeout {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestOpenAIHTTPTranscriberDoesNotFollowRedirects(t *testing.T) {
	reachedTarget := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedTarget = true
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "should not be reached"})
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	transcriber, err := NewOpenAIHTTP(testSpeechConfig(redirect.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer transcriber.Close()
	_, err = transcriber.Transcribe(context.Background(), Request{RequestID: "voice-redirect", PCM16WAV: testWAV(16000, 1, 16, 8000)})
	if errorCode(err) != CodeInferenceFailed {
		t.Fatalf("redirect error = %v", err)
	}
	if reachedTarget {
		t.Fatal("speech client followed a redirect")
	}
}

func TestOpenAIHTTPTranscriberBoundsConcurrency(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "done"})
	}))
	defer server.Close()
	cfg := testSpeechConfig(server.URL)
	cfg.MaxPending = 0
	transcriber, err := NewOpenAIHTTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer transcriber.Close()

	firstDone := make(chan error, 1)
	go func() {
		_, err := transcriber.Transcribe(context.Background(), Request{RequestID: "voice-first", PCM16WAV: testWAV(16000, 1, 16, 8000)})
		firstDone <- err
	}()
	<-started
	_, err = transcriber.Transcribe(context.Background(), Request{RequestID: "voice-second", PCM16WAV: testWAV(16000, 1, 16, 8000)})
	if errorCode(err) != CodeBusy {
		t.Fatalf("second request should be busy, got %v", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIHTTPTranscriberDoesNotExposeUpstreamErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"private transcript fragment"}}`))
	}))
	defer server.Close()
	transcriber, err := NewOpenAIHTTP(testSpeechConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer transcriber.Close()
	_, err = transcriber.Transcribe(context.Background(), Request{RequestID: "voice-private", PCM16WAV: testWAV(16000, 1, 16, 8000)})
	if errorCode(err) != CodeUnavailable || strings.Contains(err.Error(), "private transcript fragment") {
		t.Fatalf("unexpected upstream error mapping: %v", err)
	}
}

func testSpeechConfig(baseURL string) config.SpeechConfig {
	req, _ := http.NewRequest(http.MethodGet, baseURL, nil)
	return config.SpeechConfig{
		Enabled:         true,
		Backend:         "openai-http",
		BaseURL:         baseURL,
		AllowedHosts:    []string{req.URL.Hostname()},
		Model:           "test-asr",
		DefaultLanguage: "auto",
		TimeoutSeconds:  2,
		MaxAudioSeconds: 60,
		MaxUploadBytes:  3 << 20,
		MaxConcurrency:  1,
		MaxPending:      1,
	}
}
