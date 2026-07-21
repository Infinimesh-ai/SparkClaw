package document

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDetectFormatRecognizesSupportedImageSignatures(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name        string
		contentType string
		prefix      []byte
	}{
		{name: "image.png", contentType: "image/png", prefix: []byte("\x89PNG\r\n\x1a\n")},
		{name: "image.jpg", contentType: "image/jpeg", prefix: []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")},
		{name: "image.jpeg", contentType: "image/jpeg", prefix: []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")},
		{name: "image.gif", contentType: "image/gif", prefix: []byte("GIF89a\x01\x00\x01\x00")},
		{name: "image.webp", contentType: "image/webp", prefix: []byte("RIFF\x04\x00\x00\x00WEBPVP8 ")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, test.name)
			if err := os.WriteFile(path, test.prefix, 0o644); err != nil {
				t.Fatal(err)
			}
			if format, err := DetectFormat(path); err != nil || format != app.DocumentFormatImage {
				t.Fatalf("supported image was not detected: format=%q err=%v", format, err)
			}
			metadata, err := InspectFile(context.Background(), root, path)
			if err != nil || metadata.Format != app.DocumentFormatImage || metadata.ContentType != test.contentType {
				t.Fatalf("image metadata was not normalized: metadata=%#v err=%v", metadata, err)
			}
		})
	}
}

func TestDetectFormatRejectsImageExtensionSignatureMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake.png")
	if err := os.WriteFile(path, []byte("plain text pretending to be an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DetectFormat(path); err == nil {
		t.Fatal("image extension and signature mismatch was accepted")
	}
}
