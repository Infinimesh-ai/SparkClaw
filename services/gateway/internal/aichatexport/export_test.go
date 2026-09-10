package aichatexport

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func sample(url string) []byte {
	return []byte(`{"url":"` + url + `","author":"chatgpt","exporter":"3.1.0","messages":[{"author":"user","content":"same"},{"author":"ai","content":"OK"},{"author":"user","content":"same"}]}`)
}
func TestSavePreservesBytesAndNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	url := "https://chatgpt.com/c/test-123"
	raw := sample(url)
	a, err := Save(t.Context(), root, "chatgpt", url, raw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Save(t.Context(), root, "chatgpt", url, raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Path == b.Path || a.MessageCount != 3 || a.Coverage != "unknown" {
		t.Fatalf("invalid receipt: %+v", a)
	}
	bytes, err := os.ReadFile(filepath.Join(root, a.Path))
	if err != nil || string(bytes) != string(raw) {
		t.Fatal("original bytes changed", err)
	}
	sum := sha256.Sum256(bytes)
	if a.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("checksum mismatch")
	}
	if _, err := os.Stat(filepath.Join(root, a.ManifestPath)); err != nil {
		t.Fatal(err)
	}
}
func TestSaveRejectsWrongSourceAndWorkspaceEscape(t *testing.T) {
	url := "https://chatgpt.com/c/test-123"
	for _, u := range []string{"https://chatgpt.com.evil/c/abc", "https://chatgpt.com/c/../abc", "https://chatgpt.com/c/abc?token=secret", "https://grok.com/c/abc"} {
		if _, err := ConversationID("chatgpt", u); err == nil {
			t.Fatalf("accepted %s", u)
		}
	}
	if _, err := Save(t.Context(), t.TempDir(), "chatgpt", url, sample("https://chatgpt.com/c/other")); err == nil {
		t.Fatal("wrong source accepted")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "ai-chat-exports")); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(t.Context(), root, "chatgpt", url, sample(url)); err == nil {
		t.Fatal("escaped root")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) > 0 {
		t.Fatal("wrote outside workspace")
	}
}
func TestSaveCanceledDoesNotPublish(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	root := t.TempDir()
	url := "https://chatgpt.com/c/test-123"
	if _, err := Save(ctx, root, "chatgpt", url, sample(url)); err == nil {
		t.Fatal("canceled capture saved")
	}
	entries, _ := os.ReadDir(filepath.Join(root, "ai-chat-exports", "chatgpt", "test-123"))
	if len(entries) != 0 {
		t.Fatal("pending output leaked")
	}
}

type captureSession struct {
	root           string
	raw            []byte
	operations     []string
	released       bool
	savedAtRelease bool
}

func (s *captureSession) Lease() browsercontrol.SessionLease { return browsercontrol.SessionLease{} }
func (s *captureSession) Execute(_ context.Context, op string, _ map[string]any) (map[string]any, error) {
	s.operations = append(s.operations, op)
	if op == "page.navigate" {
		return map[string]any{}, nil
	}
	sum := sha256.Sum256(s.raw)
	return map[string]any{"content_base64": base64.StdEncoding.EncodeToString(s.raw), "sha256": hex.EncodeToString(sum[:])}, nil
}
func (s *captureSession) Release(context.Context) error {
	s.released = true
	matches, _ := filepath.Glob(filepath.Join(s.root, "ai-chat-exports", "chatgpt", "abc", "*", "*-ChatGPT.json"))
	s.savedAtRelease = len(matches) == 1
	return nil
}

type captureController struct{ s *captureSession }

func (c captureController) AcquireSession(context.Context, string, time.Duration, time.Duration) (browsercontrol.Session, error) {
	return c.s, nil
}
func TestExporterPersistsBeforeReleasingBrowser(t *testing.T) {
	root := t.TempDir()
	url := "https://chatgpt.com/c/abc"
	session := &captureSession{root: root, raw: sample(url)}
	receipt, err := (&Exporter{Controller: captureController{session}}).Export(t.Context(), root, "chatgpt", url)
	if err != nil {
		t.Fatal(err)
	}
	if !session.released || !session.savedAtRelease || receipt.Status != "saved" {
		t.Fatal("not persisted before release")
	}
	if len(session.operations) != 2 || session.operations[0] != "page.navigate" || session.operations[1] != "ai_chat.export" {
		t.Fatal(session.operations)
	}
}

func TestSaveUsesTitleAndPlatformInDedicatedDirectory(t *testing.T) {
	for provider, url := range map[string]string{"chatgpt": "https://chatgpt.com/c/test", "claude": "https://claude.ai/chat/test", "gemini": "https://gemini.google.com/app/test", "grok": "https://grok.com/c/test"} {
		raw, _ := json.Marshal(map[string]any{"title": "旅行计划", "url": url, "author": provider, "exporter": "3.1.0", "messages": []map[string]string{{"author": "user", "content": "hello"}}})
		root := t.TempDir()
		got, err := Save(t.Context(), root, provider, url, raw)
		if err != nil {
			t.Fatal(err)
		}
		platform := map[string]string{"chatgpt": "ChatGPT", "claude": "Claude", "gemini": "Gemini", "grok": "Grok"}[provider]
		if filepath.Base(got.Path) != "旅行计划-"+platform+".json" || !strings.HasPrefix(got.Path, "ai-chat-exports/") {
			t.Fatal(got.Path)
		}
		entries, _ := os.ReadDir(root)
		if len(entries) != 1 || entries[0].Name() != "ai-chat-exports" {
			t.Fatal("export mixed with other workspace files")
		}
		original, _ := os.ReadFile(filepath.Join(root, got.Path))
		if string(original) != string(raw) {
			t.Fatal("renaming modified JSON")
		}
		manifest, _ := os.ReadFile(filepath.Join(root, got.ManifestPath))
		var doc struct {
			Receipt Receipt `json:"receipt"`
		}
		if json.Unmarshal(manifest, &doc) != nil || doc.Receipt.Path != got.Path {
			t.Fatal("manifest path does not match actual filename")
		}
	}
}
func TestConversationFilenameIsBoundedAndPortable(t *testing.T) {
	for _, title := range []string{"", "   ", "..", "../../a\\b:c*?<>|\n", strings.Repeat("中文", 200), "隐形\u202e字符"} {
		name := conversationFilename(title, "chatgpt")
		if strings.ContainsAny(name, `/\\:*?"<>|`) || !utf8.ValidString(name) || len(name) > 200 || !strings.HasSuffix(name, "-ChatGPT.json") {
			t.Fatalf("unsafe filename %q", name)
		}
	}
	if got := conversationFilename("", "chatgpt"); got != "未命名对话-ChatGPT.json" {
		t.Fatal(got)
	}
}
