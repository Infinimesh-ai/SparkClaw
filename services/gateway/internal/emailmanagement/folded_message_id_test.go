package emailmanagement

import (
	"bytes"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func verifyFoldedOriginal(t *testing.T, original []byte) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "message.eml"), original, 0600); err != nil {
		t.Fatal("could not prepare isolated MIME fixture")
	}
	representation := app.EmailRepresentation{ID: "folded-fixture", HeaderSignals: map[string]string{"_owner_scope": strings.Repeat("a", 64)}}
	ref := sourceFile{Path: "message.eml", Bytes: int64(len(original)), SHA256: sourceHash(original)}
	if err := parseMIMEOriginal(t.Context(), root, ref, &representation); err != nil {
		code := "other"
		if regexp.MustCompile(`^email_[a-z_]+$`).MatchString(err.Error()) {
			code = err.Error()
		}
		t.Fatalf("MIME parser rejected folded original: %s", code)
	}
	if !strings.HasPrefix(representation.MessageID, "<") || !strings.HasSuffix(representation.MessageID, ">") || len(representation.From) == 0 {
		t.Fatalf("folded metadata missing: id_start=%t id_end=%t sender_count=%d", strings.HasPrefix(representation.MessageID, "<"), strings.HasSuffix(representation.MessageID, ">"), len(representation.From))
	}
	after, err := os.ReadFile(filepath.Join(root, "message.eml"))
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("MIME parser changed original bytes")
	}
}

func TestMIMEOriginalAcceptsFoldedMessageID(t *testing.T) {
	verifyFoldedOriginal(t, []byte("From: sender@example.test\r\nMessage-ID:\r\n\t<folded@example.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nOriginal fixture."))
}

func TestMIMEOriginalAcceptsGB2312MultipartAndEncodedSubject(t *testing.T) {
	// Synthetic GB2312 bytes for 中文; never copied from a mailbox sample.
	raw := []byte{0xd6, 0xd0, 0xce, 0xc4}
	for _, label := range []string{"gb2312", "GB_2312-80", "csgb2312", "iso-ir-58"} {
		t.Run(label, func(t *testing.T) {
			body, err := decodeCharsetBytes(label, raw)
			if err != nil || string(body) != "中文" {
				t.Fatal("GB2312 alias decoding failed")
			}
			encoded := base64.StdEncoding.EncodeToString(raw)
			original := []byte("From: =?" + label + "?B?" + encoded + "?= <sender@example.test>\r\nMessage-ID:\r\n <folded@example.test>\r\nSubject: =?" + label + "?B?" + encoded + "?=\r\nContent-Type: multipart/alternative; boundary=fixture\r\n\r\n--fixture\r\nContent-Type: text/plain; charset=" + label + "\r\nContent-Transfer-Encoding: base64\r\n\r\n" + encoded + "\r\n--fixture\r\nContent-Type: text/html; charset=" + label + "\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n<p>=D6=D0=CE=C4</p>\r\n--fixture--\r\n")
			verifyFoldedOriginal(t, original)
		})
	}
}

func TestIsolatedQQFoldedOriginals(t *testing.T) {
	root := os.Getenv("SPARKCLAW_TEST_FOLDED_ORIGINAL_ROOT")
	if root == "" {
		t.Skip("opt-in read-only isolated source verification")
	}
	folded := regexp.MustCompile(`(?im)^message-id:[ \t]*\r?\n[ \t]+<`)
	count := 0
	err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "message.eml" || !strings.Contains(p, string(filepath.Separator)+"staging"+string(filepath.Separator)) {
			return nil
		}
		original, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !folded.Match(original) {
			return nil
		}
		verifyFoldedOriginal(t, original)
		count++
		return nil
	})
	if err != nil || count != 2 {
		t.Fatalf("isolated folded sample verification incomplete: count=%d", count)
	}
	t.Logf("folded originals parsed with unchanged bytes: count=%d", count)
}
