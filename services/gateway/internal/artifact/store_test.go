package artifact

import (
	"context"
	"errors"
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
	loaded, err := store.Get(context.Background(), object.Key)
	if err != nil || string(loaded) != `{"ok":true}` {
		t.Fatalf("artifact did not read back: raw=%s err=%v", loaded, err)
	}
	if err := store.Delete(context.Background(), object.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), object.Key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted artifact remained readable: %v", err)
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

func TestS3StoreGetObject(t *testing.T) {
	var gotMethod string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"stored":true}`))
	}))
	defer server.Close()

	store := S3Store{
		Endpoint: server.URL, Region: "us-test-1", Bucket: "spark bucket",
		AccessKey: "access", SecretKey: "secret", Client: server.Client(),
	}
	raw, err := store.Get(context.Background(), "observations/run 1/call.json")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodGet || gotPath != "/spark%20bucket/observations/run%201/call.json" || string(raw) != `{"stored":true}` {
		t.Fatalf("unexpected S3 read: method=%s path=%s raw=%s", gotMethod, gotPath, raw)
	}
}

func TestS3StoreDeleteObject(t *testing.T) {
	var gotMethod string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	store := S3Store{
		Endpoint: server.URL, Region: "us-test-1", Bucket: "spark bucket",
		AccessKey: "access", SecretKey: "secret", Client: server.Client(),
	}
	if err := store.Delete(context.Background(), "observations/run 1/call.json"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/spark%20bucket/observations/run%201/call.json" {
		t.Fatalf("unexpected S3 delete: method=%s path=%s", gotMethod, gotPath)
	}
}

func TestFileStoreListIsPrefixBoundedAndCursorContinuable(t *testing.T) {
	root := t.TempDir()
	store := FileStore{Root: root, Bucket: "bucket"}
	for _, key := range []string{"pptx/sealed/b/2.json", "pptx/sealed/a/1.pptx", "pptx/sealed/a/1.json", "pptx/other.bin", "traces/run.json"} {
		if _, err := store.Put(context.Background(), key, "application/octet-stream", []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "bucket", "pptx", "sealed", "a", "1.json"), old, old); err != nil {
		t.Fatal(err)
	}

	page, err := store.List(context.Background(), "pptx/sealed/", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Key != "pptx/sealed/a/1.json" || page[1].Key != "pptx/sealed/a/1.pptx" {
		t.Fatalf("first page was not the two lowest sealed keys: %#v", page)
	}
	if page[0].Bytes != int64(len("pptx/sealed/a/1.json")) || time.Since(page[0].ModifiedAt) < 47*time.Hour {
		t.Fatalf("listing did not report size and modification time: %#v", page[0])
	}
	rest, err := store.List(context.Background(), "pptx/sealed/", page[1].Key, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0].Key != "pptx/sealed/b/2.json" {
		t.Fatalf("cursor continuation returned the wrong page: %#v", rest)
	}
	if all, err := store.List(context.Background(), "pptx/sealed", "", 0); err != nil || len(all) != 3 {
		t.Fatalf("prefix without trailing slash listed %d objects (err=%v), want 3", len(all), err)
	}
	if none, err := store.List(context.Background(), "missing/prefix/", "", 10); err != nil || len(none) != 0 {
		t.Fatalf("missing prefix should list nothing: objects=%#v err=%v", none, err)
	}
	if _, err := (FileStore{Bucket: "bucket"}).List(context.Background(), "pptx/", "", 10); err == nil {
		t.Fatal("empty root was accepted")
	}
}

func TestS3StoreListObjectsSignsQueryAndDecodesListing(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>spark bucket</Name><Prefix>pptx/sealed/</Prefix><KeyCount>2</KeyCount><MaxKeys>2</MaxKeys><IsTruncated>true</IsTruncated>
  <Contents><Key>pptx/sealed/b/2.json</Key><LastModified>2026-05-20T10:00:00.000Z</LastModified><Size>12</Size></Contents>
  <Contents><Key>pptx/sealed/a/1.pptx</Key><LastModified>2026-05-21T10:00:00.000Z</LastModified><Size>34</Size></Contents>
</ListBucketResult>`)
	}))
	defer server.Close()

	store := S3Store{
		Endpoint: server.URL, Region: "us-test-1", Bucket: "spark bucket",
		AccessKey: "access", SecretKey: "secret", Client: server.Client(),
		Now: func() time.Time { return time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC) },
	}
	objects, err := store.List(context.Background(), "pptx/sealed/", "pptx/sealed/a/0 z", 2)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/spark%20bucket" || gotQuery != "list-type=2&max-keys=2&prefix=pptx%2Fsealed%2F&start-after=pptx%2Fsealed%2Fa%2F0%20z" {
		t.Fatalf("unexpected list request: path=%s query=%s", gotPath, gotQuery)
	}
	if !strings.Contains(gotAuth, "AWS4-HMAC-SHA256 Credential=access/20260522/us-test-1/s3/aws4_request") {
		t.Fatalf("missing SigV4 auth header: %s", gotAuth)
	}
	if len(objects) != 2 || objects[0].Key != "pptx/sealed/a/1.pptx" || objects[0].Bytes != 34 ||
		!objects[0].ModifiedAt.Equal(time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)) || objects[1].Key != "pptx/sealed/b/2.json" {
		t.Fatalf("listing was not decoded and key-sorted: %#v", objects)
	}
}

func TestS3StoreListObjectsRejectsErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	store := S3Store{Endpoint: server.URL, Bucket: "bucket", AccessKey: "access", SecretKey: "secret", Client: server.Client()}
	if _, err := store.List(context.Background(), "pptx/sealed/", "", 5); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("forbidden listing was not surfaced: %v", err)
	}
}

func TestNotImplementedStoreListIsExplicit(t *testing.T) {
	_, err := NotImplementedStore{Backend: "gcs"}.List(context.Background(), "pptx/sealed/", "", 5)
	if err == nil || !strings.Contains(err.Error(), "not implemented: gcs") {
		t.Fatalf("not-implemented backend listing did not fail explicitly: %v", err)
	}
}
