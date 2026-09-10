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
	"strings"
)

const maxCaptureManifestBytes = 1 << 20
const maxCaptureFileBytes = 110 << 20
const maxCaptureSourceBytes = 220 << 20

func verifyCapture(ctx context.Context, workspaceRoot string, request ReadRequest, result ReadResult) error {
	invalid := errors.New("invalid capture")
	ref := result.Capture
	if workspaceRoot == "" || ref == nil || result.Provider != request.Provider || result.Status != "collected" && result.Status != "partial" ||
		!scopeDigestPattern.MatchString(request.OwnerScope) || !mailboxIDPattern.MatchString(ref.MailboxID) || !mailIDPattern.MatchString(ref.MailID) || !captureIDPattern.MatchString(ref.CaptureID) {
		return invalid
	}
	expected := path.Join("email", request.OwnerScope, ref.MailboxID, ref.MailID, "source", ref.CaptureID, "capture.json")
	if ref.ManifestPath != expected || !captureDigestPattern.MatchString(ref.ManifestSHA256) {
		return invalid
	}
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
		Status            string `json:"status"`
		AccountAddress    string `json:"account_address"`
		ProviderMessageID string `json:"provider_message_id"`
		Files             []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.SchemaVersion != 1 || manifest.Stage != "script_capture" || manifest.Provider != request.Provider || manifest.MailID != ref.MailID || manifest.MailboxID != ref.MailboxID || manifest.CaptureID != ref.CaptureID || manifest.InvocationID != request.InvocationID || manifest.Status != result.Status || len(manifest.Files) == 0 || len(manifest.Files) > 32 {
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
		if err := verifyCaptureFile(root, source.Path, source.SHA256, source.Bytes); err != nil {
			return err
		}
	}
	return nil
}

func verifyCaptureFile(root *os.Root, relative, expectedHash string, expectedSize int64) error {
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
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, expectedSize+1))
	if err != nil {
		return err
	}
	if written != expectedSize || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expectedHash {
		return errors.New("capture file checksum mismatch")
	}
	return nil
}
