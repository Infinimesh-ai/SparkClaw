package emailautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

const maxCaptureManifestBytes = 1 << 20
const maxCaptureFileBytes = 110 << 20
const maxCaptureSourceBytes = 220 << 20

var captureDatePathPattern = regexp.MustCompile(`^\d{4}/(?:0[1-9]|1[0-2])/(?:0[1-9]|[12]\d|3[01])$`)

// captureManifestDate decomposes the canonical ten-segment capture layout
// email/<YYYY>/<MM>/<DD>/<owner-scope>/<mailbox>/<mail>/source/<capture>/capture.json
// and returns the date directory it claims.
func captureManifestDate(manifestPath, ownerScope, mailboxID, mailID, captureID string) (string, bool) {
	if path.Clean(manifestPath) != manifestPath || path.IsAbs(manifestPath) || strings.ContainsAny(manifestPath, "\\\x00") {
		return "", false
	}
	parts := strings.Split(manifestPath, "/")
	if len(parts) != 10 || parts[0] != "email" || parts[4] != ownerScope || parts[5] != mailboxID ||
		parts[6] != mailID || parts[7] != "source" || parts[8] != captureID || parts[9] != "capture.json" {
		return "", false
	}
	datePath := strings.Join(parts[1:4], "/")
	if !captureDatePathPattern.MatchString(datePath) {
		return "", false
	}
	return datePath, true
}

func verifyCapture(ctx context.Context, workspaceRoot string, request ReadRequest, result ReadResult) error {
	invalid := errors.New("invalid capture")
	ref := result.Capture
	if workspaceRoot == "" || ref == nil || result.Provider != request.Provider || result.Status != "collected" && result.Status != "partial" ||
		!scopeDigestPattern.MatchString(request.OwnerScope) || !mailboxIDPattern.MatchString(ref.MailboxID) || !mailIDPattern.MatchString(ref.MailID) || !captureIDPattern.MatchString(ref.CaptureID) {
		return invalid
	}
	// The Gateway cannot predict the date directory, because it is derived from
	// the message bytes the Controller just downloaded. Every other segment is
	// still an exact equality against a value held here, so parsing the layout
	// rather than reconstructing it leaves the tamper surface unchanged.
	datePath, ok := captureManifestDate(ref.ManifestPath, request.OwnerScope, ref.MailboxID, ref.MailID, ref.CaptureID)
	if !ok || !captureDigestPattern.MatchString(ref.ManifestSHA256) {
		return invalid
	}
	expected := ref.ManifestPath
	root, err := os.OpenRoot(workspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.Open(ref.ManifestPath)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > maxCaptureManifestBytes {
		return invalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxCaptureManifestBytes+1))
	if err != nil || len(raw) > maxCaptureManifestBytes {
		return invalid
	}
	digest := sha256.Sum256(raw)
	if "sha256:"+hex.EncodeToString(digest[:]) != ref.ManifestSHA256 {
		return invalid
	}
	var manifest struct {
		SchemaVersion     int    `json:"schema_version"`
		Stage             string `json:"stage"`
		Provider          string `json:"provider"`
		MailID            string `json:"mail_id"`
		MailboxID         string `json:"mailbox_id"`
		CaptureID         string `json:"capture_id"`
		InvocationID      string `json:"invocation_id"`
		DatePath          string `json:"date_path"`
		Status            string `json:"status"`
		AccountAddress    string `json:"account_address"`
		ProviderMessageID string `json:"provider_message_id"`
		Files             []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.SchemaVersion != 1 || manifest.Stage != "script_capture" || manifest.Provider != request.Provider || manifest.MailID != ref.MailID || manifest.MailboxID != ref.MailboxID || manifest.CaptureID != ref.CaptureID || manifest.InvocationID != request.InvocationID || manifest.Status != result.Status || manifest.DatePath != datePath || len(manifest.Files) == 0 || len(manifest.Files) > 32 {
		return invalid
	}
	prefix := path.Dir(expected) + "/"
	if request.Target != nil && (!strings.EqualFold(manifest.AccountAddress, request.Target.AccountAddress) || manifest.ProviderMessageID != request.Target.ProviderMessageID) {
		return invalid
	}
	seen := map[string]bool{}
	var total int64
	for _, source := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if path.Clean(source.Path) != source.Path || !strings.HasPrefix(source.Path, prefix) || source.Path == expected || strings.ContainsAny(source.Path, "\\\x00") || seen[source.Path] || !captureDigestPattern.MatchString(source.SHA256) || source.Bytes < 0 || source.Bytes > maxCaptureFileBytes || source.Bytes > maxCaptureSourceBytes-total {
			return invalid
		}
		total += source.Bytes
		seen[source.Path] = true
		if err := verifyCaptureFileStat(root, source.Path, source.Bytes); err != nil {
			return err
		}
	}
	return nil
}

// Capture already computes the original digest while performing its one
// bounded local read. The asynchronous MIME parser verifies that digest while
// consuming the original exactly once; the Controller hot path therefore does
// only a size/type availability check and never adds a standalone full-file
// checksum pass.
func verifyCaptureFileStat(root *os.Root, relative string, expectedSize int64) error {
	file, err := root.Open(relative)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() || stat.Size() != expectedSize {
		return errors.New("invalid capture file")
	}
	return nil
}
