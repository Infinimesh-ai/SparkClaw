package toolhub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type fakeImageOCR struct {
	markdown string
	err      error
}

func (fakeImageOCR) Enabled() bool { return true }

func (f fakeImageOCR) Parse(context.Context, documentocr.Request) (documentocr.Result, error) {
	if f.err != nil {
		return documentocr.Result{}, f.err
	}
	return documentocr.Result{
		Markdown:    f.markdown,
		Model:       "ATH-MaaS/OvisOCR2",
		InferenceMS: 7,
	}, nil
}

func (fakeImageOCR) Close() error { return nil }

func TestImageInspectCombinesOvisOCR2MarkdownWithFastSummary(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "evidence.png")
	writeEmbeddedImageDocumentFixtures(t, root, imagePath)
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore()).WithDocumentOCRAdapter(fakeImageOCR{
		markdown: "| Item | Total |\n| --- | --- |\n| Tea | 42 |",
	})

	result, err := hub.Execute(context.Background(), "images.inspect", map[string]any{"path": "evidence.png"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["ocr_status"] != "succeeded" || output["ocr_model"] != "ATH-MaaS/OvisOCR2" ||
		!strings.Contains(stringArg(output, "ocr_markdown", ""), "| Tea | 42 |") ||
		strings.TrimSpace(stringArg(output, "summary", "")) == "" {
		t.Fatalf("image inspection did not combine OCR and Fast evidence: %#v", output)
	}
}

func TestNewDegradesInvalidDocumentOCRConfiguration(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "evidence.png")
	writeEmbeddedImageDocumentFixtures(t, root, imagePath)
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Adapters.DocumentOCR.Enabled = true
	cfg.Adapters.DocumentOCR.Provider = "openai-http"
	cfg.Adapters.DocumentOCR.BaseURL = "://invalid"
	cfg.Adapters.DocumentOCR.AllowedHosts = []string{"invalid"}

	hub := New(cfg, store.NewMemoryStore())
	if hub.ocr == nil || hub.ocr.Enabled() {
		t.Fatalf("invalid OCR configuration did not degrade to the disabled adapter: %#v", hub.ocr)
	}
	if readiness := hub.DocumentOCRReadiness(); !readiness.ConfiguredEnabled || readiness.AdapterReady || readiness.RuntimeStatus != "degraded" || readiness.ReasonCode != "constructor_failed" {
		t.Fatalf("constructor failure was not exposed as degraded readiness: %#v", readiness)
	}
	result, err := hub.Execute(context.Background(), "images.inspect", map[string]any{"path": "evidence.png"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["ocr_status"] != "disabled" {
		t.Fatalf("image inspection did not report disabled OCR after constructor failure: %#v", output)
	}
}

func TestImageInspectClassifiesOCRHTTPResults(t *testing.T) {
	for _, test := range []struct {
		name            string
		status          int
		content         string
		wantDetected    any
		wantOCRStatus   string
		wantOCRMarkdown bool
		wantOCRNoise    bool
	}{
		{name: "text", status: http.StatusOK, content: "# Receipt\n\nTotal: 42", wantDetected: true, wantOCRStatus: "succeeded", wantOCRMarkdown: true, wantOCRNoise: true},
		{name: "no text", status: http.StatusOK, content: `<img src="images/bbox_1_2_3_4.jpg" />`, wantDetected: false},
		{name: "failure", status: http.StatusServiceUnavailable, wantOCRStatus: "failed", wantOCRNoise: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				if test.status == http.StatusOK {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"model":   "ATH-MaaS/OvisOCR2",
						"choices": []any{map[string]any{"message": map[string]any{"content": test.content}}},
					})
				}
			}))
			defer server.Close()

			parsedURL, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			ocr, err := documentocr.NewOpenAIHTTP(config.DocumentOCRAdapterConfig{
				Enabled: true, Provider: "openai-http", BaseURL: server.URL, AllowedHosts: []string{parsedURL.Hostname()}, Model: "sparkclaw-ocr",
				TimeoutSeconds: 5, MaxUploadBytes: 12 << 20, MaxOutputBytes: 1 << 20, MaxTokens: 16384, MaxConcurrency: 1, MaxPending: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer ocr.Close()

			root := t.TempDir()
			imagePath := filepath.Join(root, "evidence.png")
			writeEmbeddedImageDocumentFixtures(t, root, imagePath)
			cfg := configtest.MustLoadDefault()
			cfg.Model.Mock = true
			cfg.Workspaces.DefaultRoot = root
			cfg.Workspaces.Allowlist = []string{root}
			hub := New(cfg, store.NewMemoryStore()).WithDocumentOCRAdapter(ocr)
			result, err := hub.Execute(context.Background(), "images.inspect", map[string]any{"path": "evidence.png"}, "session", "run")
			if err != nil {
				t.Fatal(err)
			}
			output := result.Output.(map[string]any)
			if output["text_detected"] != test.wantDetected || stringArg(output, "ocr_status", "") != test.wantOCRStatus {
				t.Fatalf("unexpected OCR classification: %#v", output)
			}
			_, hasMarkdown := output["ocr_markdown"]
			if hasMarkdown != test.wantOCRMarkdown {
				t.Fatalf("unexpected OCR Markdown projection: %#v", output)
			}
			if !test.wantOCRNoise {
				for key := range output {
					if strings.HasPrefix(key, "ocr_") {
						t.Fatalf("text-free image retained OCR field %q: %#v", key, output)
					}
				}
			}
		})
	}
}
