package toolhub

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
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
	cfg := config.Default()
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
	cfg := config.Default()
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
	result, err := hub.Execute(context.Background(), "images.inspect", map[string]any{"path": "evidence.png"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["ocr_status"] != "disabled" {
		t.Fatalf("image inspection did not report disabled OCR after constructor failure: %#v", output)
	}
}
