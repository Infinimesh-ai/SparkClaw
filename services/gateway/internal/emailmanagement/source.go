package emailmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

const parserVersion = "captured-mime-v4-original-single-pass"
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
	RawJSON           string       `json:"-"`
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
	DatePath          string       `json:"date_path"`
	ReceivedAt        string       `json:"received_at"`
	ReceivedSource    string       `json:"received_source"`
	ReceivedDisplay   string       `json:"received_display_text"`
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

// Legacy capture fixtures retain these shapes while v4 parses message.eml
// directly. Keeping the decoder types makes old on-disk test data readable
// without reintroducing the pre-commit MIME parse path.
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

var emailDatePathPattern = regexp.MustCompile(`^\d{4}/(?:0[1-9]|1[0-2])/(?:0[1-9]|[12]\d|3[01])$`)

// captureDirScope decomposes the canonical ten-segment capture layout
// email/<YYYY>/<MM>/<DD>/<owner-scope>/<mailbox>/<mail>/source/<capture>/capture.json.
// The owner scope is matched against this owner's digest, so a date prefix
// widens the layout without widening what a forged pointer can reach.
func captureDirScope(manifestPath, owner string) (mailboxID, mailID, datePath string, ok bool) {
	if !safeRelativePath(manifestPath) {
		return "", "", "", false
	}
	parts := strings.Split(manifestPath, "/")
	if len(parts) != 10 || parts[0] != "email" || parts[4] != ownerScope(owner) || parts[7] != "source" || parts[9] != "capture.json" {
		return "", "", "", false
	}
	datePath = strings.Join(parts[1:4], "/")
	if !emailDatePathPattern.MatchString(datePath) || parts[5] == "" || parts[6] == "" || parts[8] == "" {
		return "", "", "", false
	}
	return parts[5], parts[6], datePath, true
}

func loadManifest(ctx context.Context, workspace, owner string, capture app.EmailCaptureVersion) (sourceManifest, map[string]sourceFile, error) {
	var manifest sourceManifest
	mailboxID, mailID, datePath, ok := captureDirScope(capture.ManifestPath, owner)
	if !ok {
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
	// The manifest must agree with the directory it was found in, so a capture
	// committed under another mail's or another day's path cannot be adopted.
	if decodeSourceJSON(raw, &manifest) != nil || manifest.SchemaVersion != 1 || manifest.Stage != "script_capture" ||
		manifest.Acquisition != "rfc822" || manifest.CaptureID != capture.ID || len(manifest.Files) == 0 || len(manifest.Files) > 32 ||
		len(manifest.Attachments) > 20 || (manifest.Status != "collected" && manifest.Status != "partial") ||
		manifest.DatePath != datePath || manifest.MailboxID != mailboxID || manifest.MailID != mailID ||
		path.Base(path.Dir(capture.ManifestPath)) != capture.ID {
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
	manifest.RawJSON = string(raw)
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
	ref, ok := files[capture.OriginalPath]
	if !ok || ref.SHA256 != capture.OriginalSHA256 {
		return app.EmailRepresentation{}, errors.New("email_original_missing")
	}
	result := app.EmailRepresentation{ID: app.NewID("repr"), MailID: mail.ID, CaptureID: capture.ID,
		HeaderSignals: map[string]string{"_owner_scope": ownerScope(owner)}, State: app.EmailParseReady,
		Coverage: "complete_for_inputs", ParserVersion: parserVersion, CreatedAt: time.Now().UTC()}
	if err := parseMIMEOriginal(ctx, workspace, ref, &result); err != nil {
		return app.EmailRepresentation{}, err
	}
	if len(result.From) == 0 {
		return app.EmailRepresentation{}, errors.New("email_source_headers_invalid")
	}
	if !manifest.Coverage.InventoryComplete || !manifest.Coverage.AttachmentsComplete || manifest.Coverage.SkippedParts > 0 || capture.State != app.EmailCaptureComplete {
		result.State, result.Coverage = app.EmailParsePartial, "source_incomplete"
	}
	return result, ctx.Err()
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
