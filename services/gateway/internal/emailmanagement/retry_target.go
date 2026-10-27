package emailmanagement

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// SourceTime starts as the discovery observation, then parsing replaces it
// with the message's Date. Only the former is a provider receipt timestamp.
// Captured sources retain the original provider observation separately in
// their verified manifest; received_at there may itself be the EML Date.
func retryReceiptTime(mailbox app.EmailMailbox, mail app.EmailMail, capture *app.EmailCaptureVersion) (*time.Time, bool) {
	if mailbox.Provider != app.EmailProviderQQMail && mailbox.Provider != app.EmailProviderOutlook {
		return nil, true // Gmail discovery targets do not carry received_at.
	}
	if mail.CaptureID == "" && mail.RepresentationID == "" && !mail.SourceTime.IsZero() {
		received := mail.SourceTime
		return &received, true
	}
	if capture == nil || capture.ID != mail.CaptureID || capture.PurgedAt != nil || sourceHash([]byte(capture.ManifestJSON)) != capture.ManifestSHA256 {
		return nil, false
	}
	var manifest sourceManifest
	if json.Unmarshal([]byte(capture.ManifestJSON), &manifest) != nil || manifest.Provider != mailbox.Provider ||
		!strings.EqualFold(manifest.AccountAddress, mailbox.Address) || manifest.ProviderMessageID != mail.ProviderMessageID || manifest.CaptureID != capture.ID {
		return nil, false
	}
	received, err := time.Parse(time.RFC3339Nano, manifest.ReceivedDisplay)
	if err != nil || received.IsZero() {
		return nil, false
	}
	return &received, true
}
