package emailmanagement

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestRetryReceiptRequiresBoundProviderObservation(t *testing.T) {
	box := app.EmailMailbox{Provider: app.EmailProviderOutlook, Address: "owner@example.com"}
	mail := app.EmailMail{ProviderMessageID: "old", CaptureID: "capture", SourceTime: time.Date(2026, 9, 7, 2, 0, 0, 0, time.UTC)}
	for _, test := range []struct {
		name string
		edit func(*sourceManifest, *app.EmailCaptureVersion)
	}{
		{"missing provider receipt", func(m *sourceManifest, _ *app.EmailCaptureVersion) { m.ReceivedDisplay = "" }},
		{"display text is not an instant", func(m *sourceManifest, _ *app.EmailCaptureVersion) { m.ReceivedDisplay = "yesterday" }},
		{"different account", func(m *sourceManifest, _ *app.EmailCaptureVersion) { m.AccountAddress = "other@example.com" }},
		{"different provider", func(m *sourceManifest, _ *app.EmailCaptureVersion) { m.Provider = app.EmailProviderQQMail }},
		{"different message", func(m *sourceManifest, _ *app.EmailCaptureVersion) { m.ProviderMessageID = "other" }},
		{"different capture", func(m *sourceManifest, _ *app.EmailCaptureVersion) { m.CaptureID = "other" }},
		{"purged source", func(_ *sourceManifest, c *app.EmailCaptureVersion) { c.PurgedAt = &mail.SourceTime }},
		{"hash mismatch", func(_ *sourceManifest, c *app.EmailCaptureVersion) { c.ManifestSHA256 = "invalid" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := sourceManifest{Provider: box.Provider, AccountAddress: box.Address, ProviderMessageID: mail.ProviderMessageID, CaptureID: mail.CaptureID, ReceivedAt: mail.SourceTime.Format(time.RFC3339), ReceivedDisplay: "2026-09-07T02:00:07.2267782Z"}
			capture := app.EmailCaptureVersion{ID: mail.CaptureID}
			test.edit(&m, &capture)
			raw, _ := json.Marshal(m)
			capture.ManifestJSON = string(raw)
			if capture.ManifestSHA256 == "" {
				capture.ManifestSHA256 = sourceHash(raw)
			}
			if received, qualified := retryReceiptTime(box, mail, &capture); qualified || received != nil {
				t.Fatal("unqualified observation became a batch retry target")
			}
		})
	}
}
