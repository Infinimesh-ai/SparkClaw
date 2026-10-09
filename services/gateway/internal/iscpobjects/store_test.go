package iscpobjects

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func binding() Binding { return Binding{"deployment", "owner", "client", "install", 1} }
func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "objects"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func openBytes(t *testing.T, s *Store, b Binding, raw []byte) Checkpoint {
	t.Helper()
	id, _ := newID()
	c, err := s.Open(context.Background(), b, OpenRequest{id, "file", "test.bin", "application/octet-stream", int64(len(raw)), digest(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func chunk(t *testing.T, s *Store, b Binding, id string, index int, raw []byte) {
	t.Helper()
	_, err := s.Chunk(context.Background(), b, ChunkRequest{id, index, int64(index * ChunkBytes), digest(raw), base64.StdEncoding.EncodeToString(raw)})
	if err != nil {
		t.Fatal(err)
	}
}
func TestDurableResumeOriginalBytesAndCommitReceipt(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	b := binding()
	raw := append(bytes.Repeat([]byte("x"), ChunkBytes), []byte("\n {  \"original\": true }\n")...)
	c := openBytes(t, s, b, raw)
	chunk(t, s, b, c.TransferID, 1, raw[ChunkBytes:])
	if _, err := s.Commit(ctx, b, c.TransferID); err == nil {
		t.Fatal("missing chunk committed")
	}
	restored, err := NewStore(s.directory, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := restored.Status(ctx, b, c.TransferID, 0)
	if err != nil || len(cp.Received) != 1 || cp.Received[0] != 1 {
		t.Fatalf("resume checkpoint %+v %v", cp, err)
	}
	chunk(t, restored, b, c.TransferID, 1, raw[ChunkBytes:])
	chunk(t, restored, b, c.TransferID, 0, raw[:ChunkBytes])
	cp, err = restored.Commit(ctx, b, c.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := restored.Commit(ctx, b, c.TransferID)
	if err != nil || again.Object != cp.Object {
		t.Fatalf("commit replay changed receipt %+v %v", again, err)
	}
	got, err := restored.ReadAll(ctx, b, cp.Object, 64<<20)
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatalf("original bytes changed %v", err)
	}
	if cp.AcknowledgedBytes != int64(len(raw)) {
		t.Fatal("incorrect acknowledged bytes")
	}
	changed := b
	changed.InstallationID = "other"
	if _, err = restored.ReadAll(ctx, changed, cp.Object, 64<<20); err == nil {
		t.Fatal("cross installation read allowed")
	}
	changed = b
	changed.AuthorizationRevision++
	if _, err = restored.Status(ctx, changed, c.TransferID, 0); err == nil {
		t.Fatal("new authorization inherited previous transfer")
	}
}
func TestEmptyCorruptConflictingAndExpiredTransfers(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	b := binding()
	empty := openBytes(t, s, b, nil)
	cp, err := s.Commit(ctx, b, empty.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadAll(ctx, b, cp.Object, 1); err != nil || len(got) != 0 {
		t.Fatal("empty object did not roundtrip")
	}
	raw := []byte("original")
	c := openBytes(t, s, b, raw)
	bad := ChunkRequest{c.TransferID, 0, 0, digest(raw), base64.StdEncoding.EncodeToString([]byte("changed!"))}
	if _, err = s.Chunk(ctx, b, bad); err == nil {
		t.Fatal("bad digest accepted")
	}
	chunk(t, s, b, c.TransferID, 0, raw)
	bad.SHA256 = digest([]byte("changed!"))
	if _, err = s.Chunk(ctx, b, bad); err == nil {
		t.Fatal("durable chunk overwritten")
	}
	if err = os.WriteFile(filepath.Join(s.dir(c.TransferID), "chunk-00000000"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Commit(ctx, b, c.TransferID); err == nil {
		t.Fatal("corrupted disk bytes committed")
	}
	s.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if err = s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Status(ctx, b, c.TransferID, 0); err == nil {
		t.Fatal("expired resume succeeded")
	}
	if _, err = s.Open(ctx, b, OpenRequest{c.TransferID, "file", "test.bin", "application/octet-stream", int64(len(raw)), digest(raw)}); err == nil {
		t.Fatal("expired ID began a new upload")
	}
	files, _ := os.ReadDir(s.dir(c.TransferID))
	if len(files) != 1 || files[0].Name() != "state.json" {
		t.Fatal("expired payload not reclaimed")
	}
}
func TestQuotaPurposeAndPagedCheckpoint(t *testing.T) {
	s := testStore(t)
	s.limits.MaxOwnerBytes = 2 * ChunkBytes
	s.limits.MaxTotalBytes = 2 * ChunkBytes
	b := binding()
	raw := bytes.Repeat([]byte("x"), 2*ChunkBytes)
	openBytes(t, s, b, raw)
	id, _ := newID()
	if _, err := s.Open(context.Background(), b, OpenRequest{id, "file", "x", "", 1, digest([]byte("x"))}); err == nil {
		t.Fatal("reservation exceeded disk budget")
	}
	ref := wb.ObjectReference{ObjectID: id, Version: 1, Size: 600 * ChunkBytes, SHA256: digest(raw), Purpose: "file"}
	r := record{TransferID: id, Object: ref, Received: map[int]string{}}
	for i := 0; i < 600; i++ {
		r.Received[i] = "digest"
	}
	p := r.checkpoint(0)
	if len(p.Received) != 256 || p.NextCursor == nil || p.AcknowledgedBytes != ref.Size {
		t.Fatalf("unbounded or incorrect checkpoint %+v", p)
	}
	q := r.checkpoint(*p.NextCursor)
	if q.Received[0] != 256 || q.AcknowledgedBytes != ref.Size {
		t.Fatal("checkpoint cursor skipped or double counted data")
	}
	payload, _ := json.Marshal(p)
	if len(payload) > wb.MaxBodyBytes {
		t.Fatal("checkpoint exceeds wire budget")
	}
}
func TestHandlerStrictSchemaAndNoPathEscape(t *testing.T) {
	s := testStore(t)
	for _, body := range []string{`{"object_id":"../../outside","version":1}`, `{"object_id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","version":1,"path":"/etc/passwd"}`} {
		r, ok := s.Handle(context.Background(), binding(), wb.Request{Profile: wb.ProfileV2, Operation: wb.OperationObjectDescribe, Body: json.RawMessage(body)})
		if !ok || r.Status < 400 {
			t.Fatalf("unsafe operation succeeded %+v", r)
		}
	}
}

func newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 15) | 64
	raw[8] = (raw[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:]), nil
}
func TestRepeatedPublicationKeepsOriginalReceiptAndReservation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	b := binding()
	first, err := s.Put(ctx, b, "execution_result", "result.json", "application/json", []byte(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Put(ctx, b, "execution_result", "result.json", "application/json", []byte(`{"ok":true}`))
	if err != nil || first != second {
		t.Fatalf("publication duplicated receipt: %+v %+v %v", first, second, err)
	}
	entries, _ := os.ReadDir(s.directory)
	if len(entries) != 1 {
		t.Fatal("repeated lookup reserved objects repeatedly")
	}
}
