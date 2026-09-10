// Package aichatexport captures original RevivalStack files without processing their text.
package aichatexport

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

const MaxBytes = 4 << 20

var urls = map[string]*regexp.Regexp{
	"chatgpt": regexp.MustCompile(`^https://chatgpt\.com/c/([A-Za-z0-9-]+)/?$`),
	"claude":  regexp.MustCompile(`^https://claude\.ai/chat/([A-Za-z0-9-]+)/?$`),
	"gemini":  regexp.MustCompile(`^https://gemini\.google\.com/app/([A-Za-z0-9_-]+)/?$`),
	"grok":    regexp.MustCompile(`^https://grok\.com/c/([A-Za-z0-9-]+)/?$`),
}

func ConversationID(provider, url string) (string, error) {
	r := urls[provider]
	if r == nil {
		return "", errors.New("ai_chat_provider_invalid")
	}
	m := r.FindStringSubmatch(url)
	if len(m) != 2 || len(m[1]) > 128 {
		return "", errors.New("ai_chat_url_invalid")
	}
	return m[1], nil
}

type Controller interface {
	AcquireSession(context.Context, string, time.Duration, time.Duration) (browsercontrol.Session, error)
}
type Exporter struct{ Controller Controller }
type Receipt struct {
	Status       string `json:"status"`
	Path         string `json:"path"`
	ManifestPath string `json:"manifest_path"`
	SHA256       string `json:"sha256"`
	Bytes        int    `json:"bytes"`
	MessageCount int    `json:"message_count"`
	Provider     string `json:"provider"`
	SourceURL    string `json:"source_url"`
	Coverage     string `json:"coverage"`
}

func (e *Exporter) Export(ctx context.Context, workspace, provider, url string) (Receipt, error) {
	if _, err := ConversationID(provider, url); err != nil {
		return Receipt{}, err
	}
	if e == nil || e.Controller == nil {
		return Receipt{}, errors.New("ai_chat_controller_unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	session, err := e.Controller.AcquireSession(ctx, "ai-chat-"+rand.Text(), 10*time.Second, 2*time.Minute)
	if err != nil {
		return Receipt{}, err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Release(release)
	}()
	if _, err = session.Execute(ctx, "page.navigate", map[string]any{"url": url}); err != nil {
		return Receipt{}, err
	}
	result, err := session.Execute(ctx, "ai_chat.export", map[string]any{"provider": provider, "url": url})
	if err != nil {
		return Receipt{}, err
	}
	encoded, ok := result["content_base64"].(string)
	if !ok || len(encoded) > base64.StdEncoding.EncodedLen(MaxBytes) {
		return Receipt{}, errors.New("ai_chat_download_invalid")
	}
	bytes, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return Receipt{}, errors.New("ai_chat_download_invalid")
	}
	sum := sha256.Sum256(bytes)
	if result["sha256"] != hex.EncodeToString(sum[:]) {
		return Receipt{}, errors.New("ai_chat_checksum_mismatch")
	}
	return Save(ctx, workspace, provider, url, bytes)
}

// Save keeps downloaded bytes unchanged and publishes a directory only after both files are durable.
func Save(ctx context.Context, workspace, provider, url string, bytes []byte) (Receipt, error) {
	id, err := ConversationID(provider, url)
	if err != nil {
		return Receipt{}, err
	}
	if len(bytes) == 0 || len(bytes) > MaxBytes {
		return Receipt{}, errors.New("ai_chat_size_invalid")
	}
	var doc struct {
		Title    string `json:"title"`
		URL      string `json:"url"`
		Author   string `json:"author"`
		Exporter string `json:"exporter"`
		Messages []struct {
			Author  string  `json:"author"`
			Content *string `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(bytes, &doc) != nil || strings.TrimSuffix(doc.URL, "/") != strings.TrimSuffix(url, "/") || doc.Author != provider || doc.Exporter == "" || len(doc.Messages) == 0 {
		return Receipt{}, errors.New("ai_chat_export_invalid")
	}
	for _, m := range doc.Messages {
		if m.Content == nil || (m.Author != "user" && m.Author != "ai") {
			return Receipt{}, errors.New("ai_chat_messages_invalid")
		}
	}
	if workspace == "" {
		return Receipt{}, errors.New("ai_chat_workspace_missing")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return Receipt{}, err
	}
	defer root.Close()
	base := path.Join("ai-chat-exports", provider, id)
	if err = root.MkdirAll(base, 0700); err != nil {
		return Receipt{}, err
	}
	capture := time.Now().UTC().Format("20060102T150405Z") + "-" + rand.Text()
	temp := path.Join(base, ".pending-"+capture)
	final := path.Join(base, capture)
	if err = root.Mkdir(temp, 0700); err != nil {
		return Receipt{}, err
	}
	defer root.RemoveAll(temp)
	sum := sha256.Sum256(bytes)
	filename := conversationFilename(doc.Title, provider)
	receipt := Receipt{Status: "saved", Path: path.Join(final, filename), ManifestPath: path.Join(final, "manifest.json"), SHA256: hex.EncodeToString(sum[:]), Bytes: len(bytes), MessageCount: len(doc.Messages), Provider: provider, SourceURL: url, Coverage: "unknown"}
	manifest, err := json.MarshalIndent(map[string]any{"schema_version": 1, "receipt": receipt, "exporter": "revivalstack", "exporter_version": doc.Exporter, "saved_at": time.Now().UTC(), "coverage_note": "Original userscript output; complete history and account scope are not established."}, "", "  ")
	if err != nil {
		return Receipt{}, err
	}
	for name, data := range map[string][]byte{filename: bytes, "manifest.json": manifest} {
		if err = ctx.Err(); err != nil {
			return Receipt{}, err
		}
		f, e := root.OpenFile(path.Join(temp, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return Receipt{}, e
		}
		_, e = f.Write(data)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return Receipt{}, e
		}
		if closeErr != nil {
			return Receipt{}, closeErr
		}
	}
	if err = ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if err = root.Rename(temp, final); err != nil {
		return Receipt{}, fmt.Errorf("ai_chat_save_failed: %w", err)
	}
	return receipt, nil
}

// Human-readable names stay within a single portable path component. The capture
// directory, not the title, provides uniqueness and protects earlier exports.
func conversationFilename(title, provider string) string {
	const maxTitleBytes = 180
	var name strings.Builder
	for _, r := range title {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) || strings.ContainsRune(`/\\:*?"<>|`, r) {
			r = '_'
		} else if unicode.IsSpace(r) {
			r = ' '
		}
		if name.Len()+utf8.RuneLen(r) > maxTitleBytes {
			break
		}
		name.WriteRune(r)
	}
	safe := strings.Trim(name.String(), " .")
	if strings.Trim(safe, "_ ") == "" {
		safe = "未命名对话"
	}
	platform := map[string]string{"chatgpt": "ChatGPT", "claude": "Claude", "gemini": "Gemini", "grok": "Grok"}[provider]
	return safe + "-" + platform + ".json"
}
