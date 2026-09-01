package toolhub

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestImagesInspectUsesMockMultimodalModel(t *testing.T) {
	root := t.TempDir()
	raw, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.png"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "images.inspect", map[string]any{
		"path":     "sample.png",
		"question": "这张图片是什么？",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("unexpected output type %T", result.Output)
	}
	if output["status"] != "completed" || output["content_type"] != "image/png" || output["mock"] != true {
		t.Fatalf("unexpected image inspection output: %#v", output)
	}
	if output["lane"] != "fast" {
		t.Fatalf("image inspection must use only the Fast model in the first phase: %#v", output)
	}
	if output["width"] != 1 || output["height"] != 1 {
		t.Fatalf("expected 1x1 dimensions, got %#v x %#v", output["width"], output["height"])
	}
	summary, _ := output["summary"].(string)
	if !strings.Contains(summary, "Mock image inspection") {
		t.Fatalf("missing mock image summary: %#v", output["summary"])
	}
}

func TestImagesInspectResizesLargeImagesBeforeModelCall(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.jpg")
	if err := writeTestJPEG(path, 1200, 3600); err != nil {
		t.Fatal(err)
	}
	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "images.inspect", map[string]any{
		"path":     "large.jpg",
		"question": "这张图片是什么？",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("unexpected output type %T", result.Output)
	}
	if output["resized"] != true {
		t.Fatalf("expected large image to be resized: %#v", output)
	}
	if output["width"] != 1200 || output["height"] != 3600 {
		t.Fatalf("expected original dimensions to be preserved, got %#v x %#v", output["width"], output["height"])
	}
	if output["model_width"] != 800 || output["model_height"] != 2400 {
		t.Fatalf("expected model dimensions to fit 2400 long edge, got %#v x %#v", output["model_width"], output["model_height"])
	}
	if output["model_content_type"] != "image/jpeg" {
		t.Fatalf("expected resized model input to be jpeg, got %#v", output["model_content_type"])
	}
	if output["fallback_policy"] != "" {
		t.Fatalf("normal resize should not be marked as fallback: %#v", output["fallback_policy"])
	}
}

func TestPrepareImageForModelMarksOriginalSendFallback(t *testing.T) {
	prepared, err := prepareImageForModel([]byte("not actually a png"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.FallbackPolicy != "image.inspect_dimension_decode_failed_original_sent" {
		t.Fatalf("expected visible fallback policy, got %#v", prepared)
	}
	if !strings.Contains(prepared.ResizeNote, "sent original bytes") {
		t.Fatalf("expected resize note to explain fallback: %#v", prepared.ResizeNote)
	}
}
