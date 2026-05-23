package artifact

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileStorePutCleansKey(t *testing.T) {
	root := t.TempDir()
	store := FileStore{Root: root, Bucket: "bucket"}
	object, err := store.Put(context.Background(), "../traces/run.json", "application/json", []byte(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if object.Key != "traces/run.json" || object.URI != "artifact://bucket/traces/run.json" {
		t.Fatalf("unexpected object metadata: %#v", object)
	}
	raw, err := os.ReadFile(filepath.Join(root, "bucket", "traces", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("unexpected artifact content: %s", raw)
	}
}

func TestS3StorePutObject(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotHash string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotAuth = r.Header.Get("Authorization")
		gotHash = r.Header.Get("X-Amz-Content-Sha256")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := S3Store{
		Endpoint:  server.URL,
		Region:    "us-test-1",
		Bucket:    "spark bucket",
		AccessKey: "access",
		SecretKey: "secret",
		Client:    server.Client(),
		Now: func() time.Time {
			return time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
		},
	}
	object, err := store.Put(context.Background(), "traces/run 1.json", "application/json", []byte(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/spark%20bucket/traces/run%201.json" {
		t.Fatalf("unexpected S3 path: %s", gotPath)
	}
	if !strings.Contains(gotAuth, "AWS4-HMAC-SHA256 Credential=access/20260522/us-test-1/s3/aws4_request") {
		t.Fatalf("missing SigV4 auth header: %s", gotAuth)
	}
	if gotHash != sha256Hex([]byte(`{"ok":true}`)) || gotBody != `{"ok":true}` {
		t.Fatalf("unexpected S3 body/hash: %s %s", gotBody, gotHash)
	}
	if object.URI != "s3://spark bucket/traces/run 1.json" || object.Backend != "s3" {
		t.Fatalf("unexpected S3 object metadata: %#v", object)
	}
}
