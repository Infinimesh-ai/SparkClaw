package documentocr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestOpenAIHTTPParsesOvisMarkdownAndFiltersImageTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected OCR path %q", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "sparkclaw-ocr" || int(payload["max_tokens"].(float64)) != 16384 || payload["temperature"].(float64) != 0 {
			t.Fatalf("unexpected generation contract: %#v", payload)
		}
		kwargs := payload["chat_template_kwargs"].(map[string]any)
		if kwargs["enable_thinking"] != false {
			t.Fatalf("thinking was not disabled: %#v", kwargs)
		}
		processor := payload["mm_processor_kwargs"].(map[string]any)["images_kwargs"].(map[string]any)
		if int(processor["min_pixels"].(float64)) != ovisOCR2MinPixels || int(processor["max_pixels"].(float64)) != ovisOCR2MaxPixels {
			t.Fatalf("unexpected OvisOCR2 image processor contract: %#v", processor)
		}
		messages := payload["messages"].([]any)
		content := messages[0].(map[string]any)["content"].([]any)
		imageURL := content[0].(map[string]any)["image_url"].(map[string]any)["url"].(string)
		prompt := content[1].(map[string]any)["text"].(string)
		if !strings.HasPrefix(imageURL, "data:image/png;base64,") || !strings.Contains(prompt, "single Markdown document") || !strings.Contains(prompt, "without translation") {
			t.Fatalf("OvisOCR2 multimodal prompt is incomplete: image=%q prompt=%q", imageURL, prompt)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"ATH-MaaS/OvisOCR2","choices":[{"message":{"content":"# Receipt\n\n<table><tr><td>Total</td><td>42</td></tr></table>\n\n<img src=\"images/bbox_1_2_3_4.jpg\" />"}}]}`))
	}))
	defer server.Close()

	adapter, err := NewOpenAIHTTP(testConfig(t, server.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	result, err := adapter.Parse(context.Background(), Request{Content: []byte("png"), ContentType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "ATH-MaaS/OvisOCR2" || !strings.Contains(result.Markdown, "<table>") || strings.Contains(result.Markdown, "bbox_") {
		t.Fatalf("unexpected cleaned OCR result: %#v", result)
	}
}

func TestOpenAIHTTPBoundsInputAndOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := strings.Repeat("x", 2048)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": response}}}})
	}))
	defer server.Close()
	cfg := testConfig(t, server.URL+"/v1")
	cfg.MaxUploadBytes = 3
	cfg.MaxOutputBytes = 1024
	adapter, err := NewOpenAIHTTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if _, err := adapter.Parse(context.Background(), Request{Content: []byte("large"), ContentType: "image/png"}); err == nil || !strings.Contains(err.Error(), "upload limit") {
		t.Fatalf("oversized input was accepted: %v", err)
	}
	if _, err := adapter.Parse(context.Background(), Request{Content: []byte("ok"), ContentType: "image/png"}); err == nil || !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("oversized output was accepted: %v", err)
	}
}

func TestCleanTruncatedRepeatsKeepsOneTailUnit(t *testing.T) {
	prefix := strings.Repeat("正文", 4000)
	cleaned := cleanTruncatedRepeats(prefix + strings.Repeat("abc", 50))
	if cleaned != prefix+"abc" {
		t.Fatalf("truncated repeat cleanup mismatch: suffix=%q length=%d", cleaned[max(0, len(cleaned)-20):], len(cleaned))
	}
}

func testConfig(t *testing.T, baseURL string) config.DocumentOCRAdapterConfig {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	return config.DocumentOCRAdapterConfig{
		Enabled: true, Provider: "openai-http", BaseURL: baseURL, AllowedHosts: []string{parsed.Hostname()}, Model: "sparkclaw-ocr",
		TimeoutSeconds: 5, MaxUploadBytes: 12 << 20, MaxOutputBytes: 1 << 20, MaxTokens: 16384, MaxConcurrency: 1, MaxPending: 1,
	}
}
