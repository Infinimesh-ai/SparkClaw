package emailmanagement

import (
	"context"
	"encoding/base64"
	"mime"
	"net/textproto"
	"os"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestHTMLTextDropsNonRenderedContent(t *testing.T) {
	result := htmlText(`<html><head><style>@font-face { font-family: Test; }</style><script>unsafe()</script></head><body><p>Hello &amp; welcome.</p><noscript>fallback</noscript></body></html>`)
	for _, hidden := range []string{"@font-face", "unsafe()", "fallback"} {
		if strings.Contains(result, hidden) {
			t.Fatalf("non-rendered content leaked into body text: %q", result)
		}
	}
	if !strings.Contains(result, "Hello & welcome.") {
		t.Fatalf("visible body missing from parsed text: %q", result)
	}
}

func TestMIMEHTMLBodyWithContentIDRemainsTheRenderBody(t *testing.T) {
	representation := app.EmailRepresentation{HeaderSignals: map[string]string{}}
	state := &mimeParseState{
		ctx: context.Background(), representation: &representation,
		decoder: &mime.WordDecoder{}, render: emailRenderCandidate{Inline: map[string]string{}},
	}
	headers := textproto.MIMEHeader{
		"Content-Type": {`text/html; charset="utf-8"`},
		"Content-Id":   {"<root-part@example.test>"},
	}
	if err := state.walk(headers, strings.NewReader("<p>Visible root body</p>"), 0); err != nil {
		t.Fatal(err)
	}
	if len(state.htmlBodies) != 1 || !strings.Contains(state.htmlBodies[0], "Visible root body") {
		t.Fatalf("html bodies=%q", state.htmlBodies)
	}
}

func TestMIMEAttachmentIsPersistedForDownloadButExcludedFromPreview(t *testing.T) {
	workspace := t.TempDir()
	attachment := []byte("untrusted-image-attachment")
	original := []byte("From: sender@example.test\r\n" +
		"To: owner@example.test\r\n" +
		"Subject: Download only\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=fixture\r\n\r\n" +
		"--fixture\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>Readable body</p><img src=\"cid:attachment@example.test\" alt=\"attachment preview\">\r\n" +
		"--fixture\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: attachment; filename=proof.png\r\n" +
		"Content-ID: <attachment@example.test>\r\n\r\n" +
		base64.StdEncoding.EncodeToString(attachment) + "\r\n" +
		"--fixture--\r\n")
	if err := os.WriteFile(workspace+"/message.eml", original, 0600); err != nil {
		t.Fatal(err)
	}
	representation := app.EmailRepresentation{
		ID:            "representation-download-only",
		HeaderSignals: map[string]string{"_owner_scope": ownerScope("owner")},
	}
	candidate, err := parseMIMEOriginalContent(t.Context(), workspace, sourceFile{
		Path: "message.eml", SHA256: sourceHash(original), Bytes: int64(len(original)),
	}, &representation, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(representation.Attachments) != 1 {
		t.Fatalf("attachments=%d want 1", len(representation.Attachments))
	}
	part := representation.Attachments[0]
	if part.Name != "proof.png" || part.State != "not_analyzed" || part.Path == "" || part.Text != "" || part.TextPath != "" {
		t.Fatalf("attachment was not retained as download-only metadata: %+v", part)
	}
	stored, err := os.ReadFile(workspace + "/" + part.Path)
	if err != nil || string(stored) != string(attachment) {
		t.Fatalf("stored attachment mismatch: %v", err)
	}
	if len(candidate.Inline) != 0 {
		t.Fatalf("download-only attachment leaked into inline preview resources: %#v", candidate.Inline)
	}
	document, failure := prepareEmailRenderDocument(candidate.HTML, candidate.Inline)
	if failure != "" {
		t.Fatalf("preview failure=%s", failure)
	}
	if countRenderKind(document.Content, "image") != 0 || strings.Contains(renderDocumentDebug(document.Content), "attachment preview") {
		t.Fatalf("download-only attachment was rendered in the body preview: %s", renderDocumentDebug(document.Content))
	}
}
