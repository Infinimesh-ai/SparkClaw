package emailmanagement

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	netmail "net/mail"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

const parserVersion = "captured-mime-v3-body-header-signals"
const maxSourceBytes int64 = 220 << 20

type DocumentExtractor interface {
	ExtractCommittedDocument(context.Context, string, int) (document.ReadResult, error)
}

type sourceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type sourceManifest struct {
	CapturedAt        time.Time    `json:"captured_at"`
	SchemaVersion     int          `json:"schema_version"`
	Stage             string       `json:"stage"`
	Provider          string       `json:"provider"`
	AccountAddress    string       `json:"account_address"`
	ProviderMessageID string       `json:"provider_message_id"`
	MailID            string       `json:"mail_id"`
	MailboxID         string       `json:"mailbox_id"`
	CaptureID         string       `json:"capture_id"`
	InvocationID      string       `json:"invocation_id"`
	Status            string       `json:"status"`
	Acquisition       string       `json:"acquisition"`
	Files             []sourceFile `json:"files"`
	Attachments       []struct {
		ID           string `json:"part_id"`
		Name         string `json:"name"`
		DeclaredType string `json:"declared_type"`
		Path         string `json:"path"`
		SHA256       string `json:"sha256"`
		Bytes        int64  `json:"bytes"`
		Status       string `json:"status"`
	} `json:"attachments"`
	Coverage struct {
		InventoryComplete   bool `json:"inventory_complete"`
		AttachmentsComplete bool `json:"attachments_complete"`
		SkippedParts        int  `json:"skipped_parts"`
	} `json:"coverage"`
}

type capturedHeaders struct {
	Subject    string          `json:"subject"`
	From       []addressHeader `json:"from"`
	To         []addressHeader `json:"to"`
	CC         []addressHeader `json:"cc"`
	ReplyTo    []addressHeader `json:"reply_to"`
	Date       *time.Time      `json:"date"`
	MessageID  string          `json:"message_id"`
	InReplyTo  string          `json:"in_reply_to"`
	References json.RawMessage `json:"references"`
}

type addressHeader struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

func ownerScope(owner string) string {
	digest := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(digest[:])
}

func sourceHash(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func readVerifiedFile(ctx context.Context, root *os.Root, ref sourceFile, limit int64) ([]byte, error) {
	file, err := openVerifiedFile(ctx, root, ref, limit)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(contextReader{ctx, file}, ref.Bytes+1))
	if err != nil || int64(len(raw)) != ref.Bytes || sourceHash(raw) != ref.SHA256 {
		return nil, errors.New("email_source_integrity")
	}
	return raw, ctx.Err()
}

func safeRelativePath(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\\\x00") && !path.IsAbs(value) &&
		path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func loadManifest(ctx context.Context, workspace, owner string, capture app.EmailCaptureVersion) (sourceManifest, map[string]sourceFile, error) {
	var manifest sourceManifest
	prefix := "email/" + ownerScope(owner) + "/"
	if !safeRelativePath(capture.ManifestPath) || !strings.HasPrefix(capture.ManifestPath, prefix) || path.Base(capture.ManifestPath) != "capture.json" {
		return manifest, nil, errors.New("email_source_scope")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return manifest, nil, err
	}
	defer root.Close()
	file, err := root.Open(capture.ManifestPath)
	if err != nil {
		return manifest, nil, errors.New("email_source_missing")
	}
	info, statErr := file.Stat()
	file.Close()
	if statErr != nil || !info.Mode().IsRegular() {
		return manifest, nil, errors.New("email_source_invalid")
	}
	raw, err := readVerifiedFile(ctx, root, sourceFile{Path: capture.ManifestPath, SHA256: capture.ManifestSHA256, Bytes: info.Size()}, 1<<20)
	if err != nil {
		return manifest, nil, err
	}
	if decodeSourceJSON(raw, &manifest) != nil || manifest.SchemaVersion != 1 || manifest.Stage != "script_capture" ||
		manifest.Acquisition != "rfc822" || manifest.CaptureID != capture.ID || len(manifest.Files) == 0 || len(manifest.Files) > 32 ||
		len(manifest.Attachments) > 20 || (manifest.Status != "collected" && manifest.Status != "partial") {
		return manifest, nil, errors.New("email_source_invalid")
	}
	files := map[string]sourceFile{}
	total := int64(0)
	sourcePrefix := path.Dir(capture.ManifestPath) + "/"
	for _, ref := range manifest.Files {
		if !safeRelativePath(ref.Path) || !strings.HasPrefix(ref.Path, sourcePrefix) || ref.Path == capture.ManifestPath ||
			ref.Bytes < 0 || ref.Bytes > 110<<20 || total > maxSourceBytes-ref.Bytes {
			return manifest, nil, errors.New("email_source_invalid")
		}
		if _, exists := files[ref.Path]; exists {
			return manifest, nil, errors.New("email_source_invalid")
		}
		total += ref.Bytes
		files[ref.Path] = ref
	}
	return manifest, files, nil
}

func parseCapturedMail(ctx context.Context, workspace, owner string, mail app.EmailMail, mailbox app.EmailMailbox, capture app.EmailCaptureVersion) (app.EmailRepresentation, error) {
	manifest, files, err := loadManifest(ctx, workspace, owner, capture)
	if err != nil {
		return app.EmailRepresentation{}, err
	}
	if manifest.Provider != mailbox.Provider || !strings.EqualFold(manifest.AccountAddress, mailbox.Address) || manifest.ProviderMessageID != mail.ProviderMessageID {
		return app.EmailRepresentation{}, errors.New("email_source_identity")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return app.EmailRepresentation{}, err
	}
	defer root.Close()
	directory := path.Dir(capture.ManifestPath)
	headersRaw, err := readVerifiedFile(ctx, root, files[path.Join(directory, "headers.json")], 1<<20)
	if err != nil {
		return app.EmailRepresentation{}, err
	}
	body, err := readVerifiedFile(ctx, root, files[path.Join(directory, "body.txt")], 2<<20)
	if err != nil || !utf8.Valid(body) {
		return app.EmailRepresentation{}, errors.New("email_source_body_invalid")
	}
	var headers capturedHeaders
	if decodeSourceJSON(headersRaw, &headers) != nil || len(headers.From) == 0 {
		return app.EmailRepresentation{}, errors.New("email_source_headers_invalid")
	}
	result := app.EmailRepresentation{ID: app.NewID("repr"), MailID: mail.ID, CaptureID: capture.ID, Subject: headers.Subject,
		From: headerAddresses(headers.From), To: headerAddresses(headers.To), CC: headerAddresses(headers.CC), ReplyTo: headerAddresses(headers.ReplyTo),
		MessageID: headers.MessageID, BodyText: string(body), State: app.EmailParseReady, Coverage: "complete_for_inputs", ParserVersion: parserVersion, CreatedAt: time.Now().UTC()}
	if ref, ok := files[capture.OriginalPath]; ok {
		original, err := openVerifiedFile(ctx, root, ref, maxSourceBytes)
		if err != nil {
			return app.EmailRepresentation{}, err
		}
		native, parseErr := netmail.ReadMessage(bufio.NewReader(io.LimitReader(contextReader{ctx, original}, 128<<10)))
		original.Close()
		if parseErr == nil {
			result.HeaderSignals = map[string]string{}
			for _, key := range []string{"Auto-Submitted", "List-Id", "Precedence", "X-Auto-Response-Suppress"} {
				value, _ := boundedUTF8(strings.TrimSpace(native.Header.Get(key)), 512)
				if value != "" {
					result.HeaderSignals[key] = value
				}
			}
		}
	}
	if headers.Date != nil {
		result.SourceTime = *headers.Date
	}
	if len(headers.References) > 0 && string(headers.References) != "null" {
		if json.Unmarshal(headers.References, &result.ReplyReferences) != nil {
			var one string
			if json.Unmarshal(headers.References, &one) != nil {
				return result, errors.New("email_source_headers_invalid")
			}
			result.ReplyReferences = strings.Fields(one)
		}
	}
	if headers.InReplyTo != "" {
		result.ReplyReferences = append(result.ReplyReferences, headers.InReplyTo)
	}
	if !manifest.Coverage.InventoryComplete || !manifest.Coverage.AttachmentsComplete || manifest.Coverage.SkippedParts > 0 || capture.State != app.EmailCaptureComplete {
		result.State, result.Coverage = app.EmailParsePartial, "source_incomplete"
	}
	for _, part := range manifest.Attachments {
		attachment := app.EmailAttachment{ID: part.ID, Name: part.Name, MIMEType: part.DeclaredType, SizeBytes: part.Bytes, State: part.Status, SHA256: part.SHA256}
		if part.Status == "available" {
			attachment.Path = path.Join(directory, part.Path)
			ref, ok := files[attachment.Path]
			if !ok || !safeRelativePath(part.Path) || part.Bytes != ref.Bytes || part.SHA256 != ref.SHA256 {
				return result, errors.New("email_source_attachment_invalid")
			}
			verified, err := openVerifiedFile(ctx, root, ref, 25<<20)
			if err != nil {
				return result, err
			}
			verified.Close()
			// Analysis v2 verifies downloadable source bytes only. Document
			// extraction may invoke OCR/models and is intentionally excluded.
			attachment.State = "not_analyzed"
		}
		if attachment.State != "not_analyzed" {
			result.State = app.EmailParsePartial
			if result.Coverage == "complete_for_inputs" {
				result.Coverage = "attachment_source_incomplete"
			} else if !strings.Contains(result.Coverage, "attachment_source_incomplete") {
				result.Coverage += ";attachment_source_incomplete"
			}
		}
		result.Attachments = append(result.Attachments, attachment)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func headerAddresses(values []addressHeader) []string {
	addresses := make([]string, 0, len(values))
	for _, value := range values {
		if value.Address != "" {
			addresses = append(addresses, value.Address)
		}
	}
	return addresses
}

// publishJSON writes immutable, attempt-addressed output before Store admission.
// A replay may reuse identical bytes but cannot overwrite an existing version.
func publishJSON(ctx context.Context, workspace, relative string, value any) error {
	if !safeRelativePath(relative) {
		return errors.New("email_output_path_invalid")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(path.Dir(relative), 0700); err != nil {
		return err
	}
	file, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		_, err = readVerifiedFile(ctx, root, sourceFile{Path: relative, SHA256: sourceHash(raw), Bytes: int64(len(raw))}, int64(len(raw)))
		return err
	}
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	dir, err := root.Open(filepath.Dir(relative))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
