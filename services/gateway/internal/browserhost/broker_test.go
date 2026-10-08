package browserhost

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var testIdentity = Identity{OwnerID: "owner", ClientID: "client", InstallationID: "installation"}

func newTestBroker(t *testing.T) *Broker {
	t.Helper()
	b, err := NewBroker(filepath.Join(t.TempDir(), "control"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}
func connectHost(t *testing.T, b *Broker, identity Identity, grant Grant) (*websocket.Conn, map[string]any) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { b.ServeHost(r.Context(), w, r, identity) }))
	t.Cleanup(server.Close)
	headers := http.Header{"X-Sparkclaw-Host-Id": []string{grant.HostID}, "X-Sparkclaw-Host-Grant": []string{grant.Token}, "X-Sparkclaw-Runtime": []string{"runtime_test"}}
	// TLS skip belongs only to this synthetic Go fixture. HostSocket tests
	// separately exercise actual chain, hostname and pin checks without bypass.
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	conn, _, err := dialer.Dial("wss"+strings.TrimPrefix(server.URL, "https"), headers)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	var welcome map[string]any
	if err := conn.ReadJSON(&welcome); err != nil {
		t.Fatal(err)
	}
	return conn, welcome
}
func respond(t *testing.T, conn *websocket.Conn, command Command, status string) {
	t.Helper()
	err := conn.WriteJSON(Message{SchemaVersion: 1, Type: "result", CommandID: command.CommandID, Binding: &command.Binding, Status: status, Output: json.RawMessage(`{"text":"fixture","url":"https://example.test"}`)})
	if err != nil {
		t.Fatal(err)
	}
}
func acquireFixture(t *testing.T, b *Broker, conn *websocket.Conn) Binding {
	t.Helper()
	done := make(chan struct {
		binding Binding
		err     error
	}, 1)
	go func() {
		v, e := b.Acquire(context.Background(), Scope{Identity: testIdentity, ConversationID: "conversation", TaskID: "task"})
		done <- struct {
			binding Binding
			err     error
		}{v, e}
	}()
	var command Command
	if err := conn.ReadJSON(&command); err != nil {
		t.Fatal(err)
	}
	if command.Operation != "acquire" {
		t.Fatal(command)
	}
	respond(t, conn, command, "completed")
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	return result.binding
}
func TestAuthenticatedGrantIdentityAndTLSBoundary(t *testing.T) {
	b := newTestBroker(t)
	grant, err := b.IssueGrant(testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "https://example.test/api/v1/browser/hosts/connect", nil)
	request.Header.Set("X-SparkClaw-Host-ID", grant.HostID)
	request.Header.Set("X-SparkClaw-Host-Grant", grant.Token)
	request.Header.Set("X-SparkClaw-Runtime", "runtime_test")
	request.TLS = &tls.ConnectionState{}
	for _, mutate := range []func(*http.Request){func(r *http.Request) { r.TLS = nil }, func(r *http.Request) { r.URL.RawQuery = "token=forbidden" }, func(r *http.Request) { r.Header.Set("Origin", "https://untrusted.test") }, func(r *http.Request) { r.Header.Set("X-SparkClaw-Host-Grant", "wrong") }} {
		clone := request.Clone(context.Background())
		clone.Header = request.Header.Clone()
		mutate(clone)
		w := httptest.NewRecorder()
		b.ServeHost(clone.Context(), w, clone, testIdentity)
		if w.Code != 403 {
			t.Fatalf("boundary allowed %d", w.Code)
		}
	}
	wrong := testIdentity
	wrong.ClientID = "another"
	w := httptest.NewRecorder()
	b.ServeHost(request.Context(), w, request, wrong)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestBoundReadAndDurableContentFreeWriteFence(t *testing.T) {
	b := newTestBroker(t)
	grant, _ := b.IssueGrant(testIdentity)
	conn, _ := connectHost(t, b, testIdentity, grant)
	binding := acquireFixture(t, b, conn)
	read := make(chan error, 1)
	go func() {
		_, err := b.Dispatch(context.Background(), binding, "cmd_read", "read", map[string]any{})
		read <- err
	}()
	var command Command
	if err := conn.ReadJSON(&command); err != nil {
		t.Fatal(err)
	}
	wrong := command
	wrong.Binding.PageID = "page_other"
	respond(t, conn, wrong, "completed")
	respond(t, conn, command, "completed")
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	write := make(chan error, 1)
	go func() {
		_, err := b.Dispatch(context.Background(), binding, "cmd_write", "fill", map[string]any{"ref": "snapshot_fixture:e1", "snapshot_id": "snapshot_fixture", "value": "PRIVATE_SITE_CANARY"})
		write <- err
	}()
	if err := conn.ReadJSON(&command); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err := <-write; !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(b.root, "cmd_write.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "PRIVATE_SITE_CANARY") || strings.Contains(string(raw), "snapshot_fixture") || strings.Contains(string(raw), "output") {
		t.Fatalf("content retained: %s", raw)
	}
	restarted, err := NewBroker(b.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	fences := restarted.Fences(testIdentity)
	if len(fences) != 1 || fences[0].State != "unknown" {
		t.Fatal(fences)
	}
	if err := restarted.Reconcile(testIdentity, "cmd_write", fences[0].Digest, "observed_completed"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(testIdentity, "cmd_write", fences[0].Digest, "observed_completed"); err != nil {
		t.Fatalf("reconciliation replay must be idempotent: %v", err)
	}
	if err := restarted.Reconcile(testIdentity, "cmd_write", fences[0].Digest, "observed_not_applied"); !errors.Is(err, ErrFence) {
		t.Fatalf("outcome replacement allowed: %v", err)
	}
	if len(restarted.Fences(testIdentity)) != 0 {
		t.Fatal("explicit reconciliation did not release fence")
	}
}
func TestLeaseExpiryRevocationAndUnsupportedOperations(t *testing.T) {
	b := newTestBroker(t)
	grant, _ := b.IssueGrant(testIdentity)
	conn, _ := connectHost(t, b, testIdentity, grant)
	binding := acquireFixture(t, b, conn)
	for _, op := range []string{"evaluate", "Runtime.evaluate", "debugger", "arbitrary"} {
		if _, err := b.Dispatch(context.Background(), binding, opaque("cmd_"), op, map[string]any{"script": "alert(1)"}); !errors.Is(err, ErrFence) {
			t.Fatal(err)
		}
	}
	if _, err := b.Dispatch(context.Background(), binding, "cmd_injected", "read", map[string]any{"file_path": "/etc/passwd"}); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
	if _, err := b.Dispatch(context.Background(), binding, "cmd_http", "navigate", map[string]any{"url": "http://example.test"}); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
	b.mu.Lock()
	host := b.hosts[testIdentity.key()]
	expired := host.leases[binding.LeaseID]
	expired.LeaseExpiresAt = time.Now().Add(-time.Second)
	host.leases[binding.LeaseID] = expired
	b.mu.Unlock()
	if _, err := b.Dispatch(context.Background(), binding, "cmd_expired", "read", map[string]any{}); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
	if err := b.RevokeGrant(testIdentity, grant.HostID); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("revoked grant left socket open")
	}
}
func TestCommandDigestIsCanonicalAndControlsFailClosed(t *testing.T) {
	command := Command{SchemaVersion: 1, Type: "command", CommandID: "cmd_digest", Binding: Binding{Scope: Scope{Identity: testIdentity, ConversationID: "conversation", TaskID: "task"}}, Operation: "navigate", Arguments: map[string]any{"url": "https://example.test/?a=1&b=2"}}
	first := commandDigest(command)
	command.Digest = "ignored"
	if commandDigest(command) != first {
		t.Fatal("digest included itself")
	}
	raw, _ := json.Marshal(command)
	var canonical map[string]any
	json.Unmarshal(raw, &canonical)
	canonical["digest"] = ""
	if digest(canonical) != first {
		t.Fatal("canonical order differs")
	}
	bad := []struct {
		op   string
		args map[string]any
	}{{"fill", map[string]any{"ref": "e1", "snapshot_id": "s", "value": 13}}, {"click", map[string]any{"ref": "e1"}}, {"wait", map[string]any{"milliseconds": 6000}}, {"navigate", map[string]any{"url": "https://user:secret@example.test"}}}
	for _, input := range bad {
		if validateOperation(input.op, input.args) == nil {
			t.Fatal(input)
		}
	}
}
