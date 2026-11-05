package documentocr

import (
	"bytes"
	"encoding/base64"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
)

func TestOpenAIHTTPCapturesImageDispatchAndOriginalOCRBeforeCleaning(t *testing.T) {
	root := t.TempDir()
	recorder := executionevidence.New(root, executionevidence.Options{})
	scope := executionevidence.Scope{OwnerID: "owner", SessionID: "session", RunID: "run"}
	if err := recorder.Begin(t.Context(), scope, "inspect document"); err != nil {
		t.Fatal(err)
	}
	actual := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		actual <- raw
		_, _ = io.WriteString(w, `{"model":"actual-ocr-model","choices":[{"message":{"content":"# Receipt\n\nTotal: 42\n\n<img src=\"images/bbox_1_2_3_4.jpg\" />","reasoning_content":"hidden-ocr-reasoning"}}]}`)
	}))
	defer server.Close()
	adapter, err := NewOpenAIHTTP(testConfig(t, server.URL+"/v1"))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	image := []byte("exact-page-image-input")
	result, err := adapter.Parse(recorder.Bind(t.Context(), scope), Request{Content: image, ContentType: "image/png"})
	if err != nil || result.CaptureID == "" || result.Model != "actual-ocr-model" || strings.Contains(result.Markdown, "bbox_") {
		t.Fatalf("OCR behavior changed: %#v %v", result, err)
	}
	if err := recorder.Seal(t.Context(), scope, "completed"); err != nil {
		t.Fatal(err)
	}
	manifest, err := recorder.Read(t.Context(), scope)
	if err != nil || !manifest.Capture.Complete {
		t.Fatalf("OCR capture incomplete: %#v %v", manifest.Capture, err)
	}
	content := map[string][]byte{}
	for _, payload := range manifest.Payloads {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || entry.Name() != payload.SHA256 {
				return err
			}
			content[payload.Kind], err = os.ReadFile(path)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(content["model_request"], <-actual) || !bytes.Contains(content["model_request"], []byte(base64.StdEncoding.EncodeToString(image))) {
		t.Fatal("OCR dispatch body/image differs from capture")
	}
	if !bytes.Contains(content["model_response"], []byte("bbox_1_2_3_4")) || bytes.Contains(content["model_response"], []byte("hidden-ocr-reasoning")) {
		t.Fatalf("original OCR output or reasoning boundary lost: %s", content["model_response"])
	}
	if !bytes.Contains(content["model_result"], []byte("Total: 42")) || bytes.Contains(content["model_result"], []byte("bbox_")) {
		t.Fatalf("cleaned OCR result missing: %s", content["model_result"])
	}
}
