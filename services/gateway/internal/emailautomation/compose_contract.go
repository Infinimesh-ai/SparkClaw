package emailautomation

import (
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"net/mail"
	"path"
	"regexp"
	"strings"
)

var sendAttachmentDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var sendAttachmentStagedDirectory = regexp.MustCompile(`^\.sparkclaw-mail-send-[a-f0-9]{32}/[0-9a-f]{2}$`)

func validateComposeRequest(r SendRequest) (string, error) {
	invalid := func() (string, error) {
		return "", codedError(app.ToolErrorEmailInvalidInput, "Email compose binding is invalid")
	}
	if len(r.Attachments) > app.EmailSendMaxAttachments {
		return invalid()
	}
	var attachmentBytes int64
	for _, attachment := range r.Attachments {
		if r.Mode == "" || attachment.Name == "" || len(attachment.Name) > 255 || strings.HasPrefix(attachment.Name, ".") || path.Base(attachment.Name) != attachment.Name || strings.ContainsAny(attachment.Name, "\\:\x00\r\n") || attachment.SizeBytes < 0 || attachment.SizeBytes > app.EmailSendMaxAttachmentBytes || !sendAttachmentDigest.MatchString(attachment.SHA256) {
			return invalid()
		}
		if r.Mode != "reconcile" && (!sendAttachmentStagedDirectory.MatchString(path.Dir(attachment.StagedPath)) || path.Base(attachment.StagedPath) != attachment.Name) {
			return invalid()
		}
		attachmentBytes += attachment.SizeBytes
		if attachmentBytes > app.EmailSendMaxAttachmentBytes {
			return invalid()
		}
	}
	if r.Mode == "" && len(r.To) == 0 && len(r.CC) == 0 && r.ReplyTarget == nil && r.AccountAddress == "" {
		if err := validateMessage(r.Recipient, r.Subject, r.Body); err != nil {
			return "", err
		}
		return recipientDigest(r.Recipient), nil
	}
	if strings.TrimSpace(r.Subject) == "" {
		return invalid()
	}
	if r.Mode != "compose" && r.Mode != "reply" && r.Mode != "reply_all" && r.Mode != "reconcile" || r.Recipient != "" || len(r.To) == 0 || len(r.To)+len(r.CC) > 100 {
		return invalid()
	}
	account, err := mail.ParseAddress(r.AccountAddress)
	if err != nil || account.Address != r.AccountAddress {
		return invalid()
	}
	seen := map[string]bool{}
	for _, a := range append(append([]string{}, r.To...), r.CC...) {
		if err := validateMessage(a, r.Subject, r.Body); err != nil {
			return "", err
		}
		at := strings.LastIndex(a, "@")
		key := a[:at] + "@" + strings.ToLower(a[at+1:])
		if seen[key] {
			return invalid()
		}
		seen[key] = true
	}
	if r.Mode == "compose" {
		if r.ReplyTarget != nil {
			return invalid()
		}
	} else if r.Mode != "reconcile" || r.ReplyTarget != nil {
		t := r.ReplyTarget
		if t == nil || t.AccountAddress != r.AccountAddress || t.Folder == "" || !validMailTarget(t.EmailCaptureTarget) || strings.ContainsAny(t.Subject, "\r\n\x00") || len(t.Subject) > 4000 {
			return invalid()
		}
	}
	to, cc := r.To, r.CC
	if cc == nil {
		cc = []string{}
	}
	raw, _ := json.Marshal(struct {
		To []string `json:"to"`
		CC []string `json:"cc"`
	}{to, cc})
	return recipientDigest(string(raw)), nil
}

func validSendProviderID(value string, managed bool) bool {
	if !managed {
		return validOpaqueProviderID(value)
	}
	return value == "" || len(value) <= 1024 && mailLocatorPattern.MatchString(value)
}
