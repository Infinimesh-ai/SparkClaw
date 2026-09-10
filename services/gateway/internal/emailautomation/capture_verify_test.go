package emailautomation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func captureFixture(t *testing.T) (string, ReadRequest, ReadResult, string) {
	t.Helper()
	root := t.TempDir()
	request := validReadRequest()
	ownerDigest := sha256.Sum256([]byte("owner"))
	request.OwnerScope = hex.EncodeToString(ownerDigest[:])
	var result ReadResult
	if err := json.Unmarshal([]byte(strings.ReplaceAll(validReadOutput(), strings.Repeat("a", 64), request.OwnerScope)), &result); err != nil {
		t.Fatal(err)
	}
	relative := filepath.ToSlash(filepath.Join(filepath.Dir(result.Capture.ManifestPath), "body.txt"))
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("captured source"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"schema_version": 1, "stage": "script_capture", "provider": request.Provider, "mail_id": result.Capture.MailID, "mailbox_id": result.Capture.MailboxID, "capture_id": result.Capture.CaptureID, "invocation_id": request.InvocationID, "status": "collected", "files": []any{map[string]any{"path": relative, "bytes": len("captured source"), "sha256": captureTestDigest([]byte("captured source"))}}}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(result.Capture.ManifestPath)), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	result.Capture.ManifestSHA256 = captureTestDigest(raw)
	return root, request, result, absolute
}

func TestControllerVerifiesCaptureBeforeReturningReceipt(t *testing.T) {
	root, request, result, source := captureFixture(t)
	st := store.NewMemoryStore()
	checked := time.Now().UTC()
	setting, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "owner", Provider: request.Provider, Account: request.Account, Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checked}, 0)
	if err != nil {
		t.Fatal(err)
	}
	request.SettingVersion = setting.Version
	runner := &fakeScriptRunner{readResult: result}
	controller := NewController(st, DefaultRegistry(), nil, runner).WithCaptureWorkspaceRoot(root)
	if _, err := controller.ReadForOwner(t.Context(), "owner", request); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ReadForOwner(t.Context(), "owner", request); ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
		t.Fatalf("missing source err=%v", err)
	}
}

func captureTestDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func TestVerifyCaptureChecksDurableManifestAndEverySource(t *testing.T) {
	for _, mode := range []string{"valid", "no root", "manifest hash", "source hash", "missing source", "cross owner", "escaped source"} {
		t.Run(mode, func(t *testing.T) {
			root, request, result, source := captureFixture(t)
			switch mode {
			case "no root":
				root = ""
			case "manifest hash":
				result.Capture.ManifestSHA256 = "sha256:" + strings.Repeat("f", 64)
			case "source hash":
				if err := os.WriteFile(source, []byte("modified source"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing source":
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
			case "cross owner":
				request.OwnerScope = strings.Repeat("f", 64)
			case "escaped source":
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "private")
				if err := os.WriteFile(outside, []byte("captured source"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, source); err != nil {
					t.Fatal(err)
				}
			}
			err := verifyCapture(t.Context(), root, request, result)
			if mode == "valid" && err != nil {
				t.Fatal(err)
			}
			if mode != "valid" && err == nil {
				t.Fatal("invalid capture accepted")
			}
		})
	}
}
