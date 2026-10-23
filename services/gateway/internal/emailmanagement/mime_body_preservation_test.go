package emailmanagement

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func parseBodyFixture(t *testing.T, original []byte) (string, app.EmailRepresentation, emailRenderCandidate, error) {
	t.Helper()
	workspace := t.TempDir()
	originalPath := filepath.Join(workspace, "message.eml")
	if err := os.WriteFile(originalPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	representation := app.EmailRepresentation{
		ID: "body-preservation", State: app.EmailParseReady,
		HeaderSignals: map[string]string{"_owner_scope": ownerScope("fixture-owner")},
	}
	candidate, err := parseMIMEOriginalContent(t.Context(), workspace, sourceFile{
		Path: "message.eml", Bytes: int64(len(original)), SHA256: sourceHash(original),
	}, &representation, true)
	after, readErr := os.ReadFile(originalPath)
	if readErr != nil || !bytes.Equal(after, original) {
		t.Fatal("parsing changed the immutable original")
	}
	return workspace, representation, candidate, err
}

func plainBodyFixture(body string) []byte {
	return []byte("From: sender@example.test\r\nContent-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString([]byte(body)))
}

// These are the MIME shapes of the two received originals, with synthetic
// identities, text and attachment bytes. No mailbox content is included.
func alternativeBodyFixture(t *testing.T, charset, htmlEncoding, plain string, attachment []byte) []byte {
	t.Helper()
	plainBytes := []byte(plain)
	if charset == "gb2312" {
		var err error
		plainBytes, err = simplifiedchinese.GBK.NewEncoder().Bytes(plainBytes)
		if err != nil {
			t.Fatal(err)
		}
	}
	html := "<p>HTML fallback</p>"
	if htmlEncoding == "base64" {
		html = base64.StdEncoding.EncodeToString([]byte(html))
	}
	return []byte("From: sender@example.test\r\nTo: owner@example.test\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=outer\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/alternative; boundary=inner\r\n\r\n" +
		"--inner\r\nContent-Type: text/plain; charset=" + charset + "\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(plainBytes) + "\r\n" +
		"--inner\r\nContent-Type: text/html; charset=" + charset + "\r\n" +
		"Content-Transfer-Encoding: " + htmlEncoding + "\r\n\r\n" + html + "\r\n--inner--\r\n" +
		"--outer\r\nContent-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=proof.bin\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(attachment) + "\r\n--outer--\r\n")
}

func TestMIMEPlainBodyPreservesWhitespace(t *testing.T) {
	for name, body := range map[string]string{
		"leading-and-trailing": " \t\r\nFirst line\r\n\r\nLast line\t \r\n\r\n",
		"LF-only":              "\nFirst line\n\nLast line\n",
		"unicode-space":        "\u00a0中文\u3000\r\n",
		"whitespace-only":      " \t\r\n\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, representation, candidate, err := parseBodyFixture(t, plainBodyFixture(body))
			if err != nil {
				t.Fatal(err)
			}
			if representation.BodyText != body || candidate.Plain != body {
				t.Fatalf("plain text bytes changed: representation=%q preview=%q want=%q", representation.BodyText, candidate.Plain, body)
			}
		})
	}
}

func TestMIMEReceivedAlternativePreservesPlainAndAttachment(t *testing.T) {
	attachment := []byte("synthetic attachment\x00\xff\r\n")
	for _, fixture := range []struct {
		name, charset, htmlEncoding, body string
	}{
		{"outlook-gb2312", "gb2312", "quoted-printable", " \t中文\r\n\r\nBody line\r\n\r\n"},
		{"gmail-utf8", "utf-8", "base64", " \t中文\r\n\r\nBody line\r\n"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			original := alternativeBodyFixture(t, fixture.charset, fixture.htmlEncoding, fixture.body, attachment)
			workspace, representation, candidate, err := parseBodyFixture(t, original)
			if err != nil {
				t.Fatal(err)
			}
			if representation.BodyText != fixture.body || candidate.Plain != fixture.body || candidate.HTML != "<p>HTML fallback</p>" {
				t.Fatal("decoded plain body changed or HTML replaced nonempty plain text")
			}
			if len(representation.Attachments) != 1 {
				t.Fatalf("attachments=%d", len(representation.Attachments))
			}
			part := representation.Attachments[0]
			stored, readErr := os.ReadFile(filepath.Join(workspace, part.Path))
			if readErr != nil || !bytes.Equal(stored, attachment) || part.SHA256 != sourceHash(attachment) || part.SizeBytes != int64(len(attachment)) || part.State != "not_analyzed" {
				t.Fatal("attachment bytes, hash or metadata changed")
			}
		})
	}
}

func TestMIMEWhitespacePlainStillUsesHTMLFallback(t *testing.T) {
	_, representation, candidate, err := parseBodyFixture(t, alternativeBodyFixture(t, "utf-8", "base64", " \t\r\n\u00a0", nil))
	if err != nil {
		t.Fatal(err)
	}
	if representation.BodyText != "HTML fallback" || candidate.Plain != "HTML fallback" {
		t.Fatalf("HTML fallback changed: %q", representation.BodyText)
	}
}

func TestMIMEPreservedPlainBodySizeLimit(t *testing.T) {
	for _, size := range []int{maxMIMEBodyBytes, maxMIMEBodyBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			body := " " + strings.Repeat("x", size-3) + "\r\n"
			_, representation, _, err := parseBodyFixture(t, plainBodyFixture(body))
			if size > maxMIMEBodyBytes {
				if err == nil || err.Error() != "email_source_body_invalid" {
					t.Fatalf("oversized body error=%v", err)
				}
			} else if err != nil || representation.BodyText != body {
				t.Fatalf("body at limit was changed/rejected: length=%d error=%v", len(representation.BodyText), err)
			}
		})
	}
}

func TestMIMEPreservedPlainRetainsAttachmentSizeLimit(t *testing.T) {
	for _, size := range []int{maxMIMEAttachmentBytes, maxMIMEAttachmentBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			attachment := bytes.Repeat([]byte{'x'}, size)
			workspace, representation, _, err := parseBodyFixture(t, alternativeBodyFixture(t, "utf-8", "base64", "Body\r\n", attachment))
			if err != nil || representation.BodyText != "Body\r\n" || len(representation.Attachments) != 1 {
				t.Fatalf("body or attachment inventory changed: error=%v", err)
			}
			part := representation.Attachments[0]
			if part.SizeBytes != int64(size) {
				t.Fatalf("attachment size=%d want=%d", part.SizeBytes, size)
			}
			if size > maxMIMEAttachmentBytes {
				if representation.State != app.EmailParsePartial || part.State != "skipped_limit" || part.Path != "" || part.SHA256 != "" {
					t.Fatal("oversized attachment did not retain its explicit partial/limit state")
				}
			} else {
				stored, readErr := os.ReadFile(filepath.Join(workspace, part.Path))
				if readErr != nil || !bytes.Equal(stored, attachment) || part.SHA256 != sourceHash(attachment) || part.State != "not_analyzed" {
					t.Fatal("attachment at limit was rejected or changed")
				}
			}
		})
	}
}
