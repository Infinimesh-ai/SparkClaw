package emailautomation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func sentMatchFixture() *app.EmailSentMatch {
	return &app.EmailSentMatch{Folder: "sent", Subject: "验收进度汇报 [case-3]", Recipient: "sink@example.test", IntervalStart: "2026-10-23T13:00:00Z", IntervalEnd: "2026-10-23T14:00:00Z"}
}

func sentOriginalFixture() string {
	return "From: " + mime.BEncoding.Encode("utf-8", "发件人") + " <owner@example.test>\r\nTo: " + mime.QEncoding.Encode("utf-8", "收件人") + " <sink@example.test>\r\nSubject: " + mime.BEncoding.Encode("utf-8", sentMatchFixture().Subject) + "\r\nDate: Fri, 23 Oct 2026 13:30:00 +0000\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=synthetic\r\n\r\n--synthetic\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n原有正文\r\n\r\n--synthetic\r\nContent-Disposition: attachment; filename=fixture.bin\r\nContent-Transfer-Encoding: base64\r\n\r\nAAEC/w==\r\n--synthetic--\r\n"
}

func TestSentOriginalHeaderPredicate(t *testing.T) {
	base := sentOriginalFixture()
	for name, raw := range map[string]string{
		"valid":                     base,
		"folded encoded Chinese":    strings.Replace(base, mime.BEncoding.Encode("utf-8", sentMatchFixture().Subject), mime.BEncoding.Encode("utf-8", "验收进度汇报 ")+"\r\n\t"+"=?utf-8?b?W2Nhc2UtM10=?=", 1),
		"decoded gb2312":            strings.Replace(base, mime.BEncoding.Encode("utf-8", sentMatchFixture().Subject), "=?gb2312?B?0enK1b34tsi747Go?= [case-3]", 1),
		"domain case":               strings.ReplaceAll(base, "@example.test", "@EXAMPLE.test"),
		"lower bound":               strings.Replace(base, "13:30:00 +0000", "13:00:00 +0000", 1),
		"date offset":               strings.Replace(base, "13:30:00 +0000", "21:30:00 +0800", 1),
		"upper bound":               strings.Replace(base, "13:30:00 +0000", "14:00:00 +0000", 1),
		"before bound":              strings.Replace(base, "13:30:00 +0000", "12:59:59 +0000", 1),
		"missing subject":           strings.Replace(base, "Subject:", "X-Subject:", 1),
		"duplicate subject":         "sUbJeCt: duplicate\r\n" + base,
		"duplicate from":            "From: owner@example.test\r\n" + base,
		"duplicate to":              "To: sink@example.test\r\n" + base,
		"duplicate date":            "Date: Fri, 23 Oct 2026 13:30:00 +0000\r\n" + base,
		"invalid date":              strings.Replace(base, "Fri, 23 Oct 2026 13:30:00 +0000", "not a date", 1),
		"invalid numeric zone":      strings.Replace(base, "+0000", "+9999", 1),
		"unknown numeric zone":      strings.Replace(base, "+0000", "-0000", 1),
		"unknown zone":              strings.Replace(base, "+0000", "XYZ", 1),
		"cc empty":                  "Cc:\r\n" + base,
		"bcc":                       "Bcc: hidden@example.test\r\n" + base,
		"wrong sender":              strings.Replace(base, "owner@example.test", "other@example.test", 1),
		"sender alias":              strings.Replace(base, "owner@example.test", "owner+alias@example.test", 1),
		"local part case":           strings.Replace(base, "owner@example.test", "Owner@example.test", 1),
		"wrong recipient":           strings.Replace(base, "sink@example.test", "other@example.test", 1),
		"extra to":                  strings.Replace(base, "<sink@example.test>", "<sink@example.test>, another@example.test", 1),
		"single group":              strings.Replace(base, "<sink@example.test>", "Group: sink@example.test;", 1),
		"subject prefix whitespace": strings.Replace(base, "Subject: ", "Subject:  ", 1),
		"subject suffix whitespace": strings.Replace(base, "\r\nDate:", " \r\nDate:", 1),
		"malformed encoded word":    strings.Replace(base, mime.BEncoding.Encode("utf-8", sentMatchFixture().Subject), "=?UTF-8?B?!!!?=", 1),
		"unknown charset":           strings.Replace(base, "utf-8?b?", "unknown-charset?b?", 1),
		"header truncated":          strings.Split(base, "\r\n\r\n")[0],
		"invalid header name":       "Bad Name: x\r\n" + base,
		"orphan fold":               " folded\r\n" + base,
		"nul":                       "X-Header: a\x00b\r\n" + base,
		"line bound":                "X-Large: " + strings.Repeat("x", 1000) + "\r\n" + base,
		"total bound":               strings.Repeat("X-Large: "+strings.Repeat("x", 900)+"\r\n", 80) + base,
	} {
		t.Run(name, func(t *testing.T) {
			headers, err := readSentHeaders(bufio.NewReader(strings.NewReader(raw)))
			if err == nil {
				err = matchSentHeaders(headers, "owner@example.test", *sentMatchFixture())
			}
			valid := name == "valid" || name == "folded encoded Chinese" || name == "decoded gb2312" || name == "domain case" || name == "lower bound" || name == "date offset"
			if (err == nil) != valid {
				t.Fatalf("accepted=%v", err == nil)
			}
		})
	}
}

func sentCaptureFixture(t *testing.T) (string, ReadRequest, ReadResult, map[string]any, string) {
	t.Helper()
	root, request, result, _ := captureFixture(t)
	request.SentMatch = sentMatchFixture()
	request.AccountAddress = "owner@example.test"
	mailboxHash := sha256.Sum256([]byte(request.Provider + "\x00" + request.AccountAddress))
	result.Capture.MailboxID = "mb_" + hex.EncodeToString(mailboxHash[:])[:32]
	id := "gmail:message-1"
	mailHash := sha256.Sum256([]byte(result.Capture.MailboxID + "\x00" + id))
	result.Capture.MailID = "mail_" + hex.EncodeToString(mailHash[:])[:32]
	result.Capture.ManifestPath = "email/2026/10/23/" + request.OwnerScope + "/" + result.Capture.MailboxID + "/" + result.Capture.MailID + "/source/" + result.Capture.CaptureID + "/capture.json"
	rel := path.Dir(result.Capture.ManifestPath) + "/message.eml"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte(sentOriginalFixture())
	if err := os.WriteFile(filepath.Join(root, rel), body, 0600); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"schema_version": 1, "stage": "script_capture", "provider": request.Provider, "mail_id": result.Capture.MailID, "mailbox_id": result.Capture.MailboxID, "capture_id": result.Capture.CaptureID, "invocation_id": request.InvocationID, "status": "collected", "date_path": "2026/10/23", "account_address": request.AccountAddress, "provider_message_id": id, "provider_selection_id": "thread-1", "provider_native_id": "native-1", "acquisition": "rfc822", "folder": "sent", "sent_match": request.SentMatch, "files": []any{map[string]any{"path": rel, "bytes": len(body), "sha256": captureTestDigest(body)}}}
	writeSentManifest(t, root, &result, m)
	return root, request, result, m, filepath.Join(root, rel)
}

func writeSentManifest(t *testing.T, root string, result *ReadResult, m map[string]any) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, result.Capture.ManifestPath), raw, 0600); err != nil {
		t.Fatal(err)
	}
	result.Capture.ManifestSHA256 = captureTestDigest(raw)
}

func TestSentCaptureBindsImmutableOriginalAndRejectsTampering(t *testing.T) {
	for _, mode := range []string{"valid", "same size tamper", "source missing", "manifest tamper", "selector replacement", "account replacement", "wrong invocation", "wrong folder", "wrong acquisition", "missing selection id", "invalid native id", "partial capture", "extra file", "mail identity", "manifest symlink", "source symlink inside", "source symlink outside", "cancelled", "size limit", "wrong path"} {
		t.Run(mode, func(t *testing.T) {
			root, request, result, m, source := sentCaptureFixture(t)
			before, _ := os.ReadFile(source)
			ctx := context.Background()
			switch mode {
			case "same size tamper":
				_ = os.WriteFile(source, bytes.Replace(before, []byte("原有正文"), []byte("更改正文"), 1), 0600)
			case "source missing":
				_ = os.Remove(source)
			case "manifest tamper":
				result.Capture.ManifestSHA256 = "sha256:" + strings.Repeat("a", 64)
			case "selector replacement":
				copy := *request.SentMatch
				copy.Subject = "other"
				m["sent_match"] = copy
			case "account replacement":
				m["account_address"] = "other@example.test"
			case "wrong invocation":
				m["invocation_id"] = "other"
			case "wrong folder":
				m["folder"] = "inbox"
			case "wrong acquisition":
				m["acquisition"] = "reconstructed"
			case "missing selection id":
				delete(m, "provider_selection_id")
			case "invalid native id":
				m["provider_native_id"] = "native\nother"
			case "partial capture":
				result.Status, m["status"] = "partial", "partial"
			case "extra file":
				m["files"] = append(m["files"].([]any), m["files"].([]any)[0])
			case "mail identity":
				m["provider_message_id"] = "different"
			case "source symlink inside", "source symlink outside":
				dest := filepath.Join(root, "alternate.eml")
				if mode == "source symlink outside" {
					dest = filepath.Join(t.TempDir(), "outside.eml")
				}
				_ = os.WriteFile(dest, before, 0600)
				_ = os.Remove(source)
				_ = os.Symlink(dest, source)
			case "manifest symlink":
				p := filepath.Join(root, result.Capture.ManifestPath)
				raw, _ := os.ReadFile(p)
				dest := filepath.Join(root, "alternate.json")
				_ = os.WriteFile(dest, raw, 0600)
				_ = os.Remove(p)
				_ = os.Symlink(dest, p)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "size limit":
				m["files"].([]any)[0].(map[string]any)["bytes"] = maxCaptureFileBytes + 1
			case "wrong path":
				m["files"].([]any)[0].(map[string]any)["path"] = "../message.eml"
			}
			if mode != "manifest tamper" && mode != "manifest symlink" {
				writeSentManifest(t, root, &result, m)
			}
			err := verifyCapture(ctx, root, request, result)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("accepted=%v", err == nil)
			}
			if mode == "valid" {
				after, _ := os.ReadFile(source)
				if !bytes.Equal(before, after) {
					t.Fatal("original changed")
				}
			}
		})
	}
}
