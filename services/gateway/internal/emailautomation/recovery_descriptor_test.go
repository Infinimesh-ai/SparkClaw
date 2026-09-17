package emailautomation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"strings"
	"testing"
)

func TestRecoveryCaptureDescriptorValidation(t *testing.T) {
	id := "cap_" + strings.Repeat("a", 32)
	original := "email/2026/09/16/owner/mailbox/mail/source/" + id + "/message.eml"
	hash := "sha256:" + strings.Repeat("b", 64)
	raw, _ := json.Marshal(map[string]any{"schema_version": 1, "stage": "script_capture", "account_address": "owner@example.test", "provider_message_id": "m1", "capture_id": id, "files": []any{map[string]string{"path": original, "sha256": hash}}})
	digest := sha256.Sum256(raw)
	descriptor := app.EmailCaptureVersion{ID: id, ManifestJSON: string(raw), ManifestSHA256: "sha256:" + hex.EncodeToString(digest[:]), ManifestPath: strings.TrimSuffix(original, "message.eml") + "capture.json", OriginalPath: original, OriginalSHA256: hash}
	target := app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "m1", ProviderSelectionID: "m1", RecoveryCapture: &descriptor}
	if !validMailTarget(target) {
		t.Fatal("valid trusted descriptor rejected")
	}
	target.ProviderMessageID = "other"
	if validMailTarget(target) {
		t.Fatal("foreign identity accepted")
	}
	target.ProviderMessageID = "m1"
	descriptor.ManifestJSON += " "
	if validMailTarget(target) {
		t.Fatal("changed manifest accepted")
	}
	descriptor.ManifestJSON = string(raw)
	descriptor.PurgeReason = "user_requested"
	if validMailTarget(target) {
		t.Fatal("purged source resurrected")
	}
}
