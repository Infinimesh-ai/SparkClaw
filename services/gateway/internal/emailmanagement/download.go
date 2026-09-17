package emailmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// OpenFile resolves only an owner-authorized committed mail and manifest part.
// No caller-provided path participates in filesystem resolution.
func (s *Service) OpenFile(ctx context.Context, owner, mailID, partID string) (*os.File, string, error) {
	mail, found, err := s.repository.GetEmailMail(ctx, owner, mailID)
	if err != nil {
		return nil, "", err
	}
	if !found {
		return nil, "", ErrNotFound
	}
	capture, found, err := s.repository.GetEmailCapture(ctx, owner, mail.CaptureID)
	if err != nil {
		return nil, "", err
	}
	if !found || capture.MailID != mail.ID {
		return nil, "", ErrNotFound
	}
	// Refuse before touching the filesystem. A purged capture keeps its pointer
	// so cleanup can be finished later, but the bytes must never be served.
	if capture.PurgedAt != nil {
		return nil, "", ErrPurged
	}
	if mail.CaptureState == app.EmailCaptureSourceMissing && partID == "original" {
		return nil, "", errors.New("email_source_missing")
	}
	manifest, files, err := loadManifest(ctx, s.opts.WorkspaceRoot, owner, capture)
	if err != nil {
		return nil, "", err
	}
	mailbox, found, err := s.repository.GetEmailMailbox(ctx, owner, mail.MailboxID)
	if err != nil {
		return nil, "", err
	}
	if !found || manifest.Provider != mailbox.Provider || !strings.EqualFold(manifest.AccountAddress, mailbox.Address) || manifest.ProviderMessageID != mail.ProviderMessageID {
		return nil, "", errors.New("email_source_identity")
	}
	var ref sourceFile
	name := "message.eml"
	if partID == "original" {
		ref = files[capture.OriginalPath]
		if ref.SHA256 != capture.OriginalSHA256 {
			return nil, "", errors.New("email_source_integrity")
		}
	} else {
		representation, found, err := s.repository.GetEmailRepresentation(ctx, owner, mail.RepresentationID)
		if err != nil {
			return nil, "", err
		}
		if !found || representation.MailID != mail.ID || representation.CaptureID != capture.ID {
			return nil, "", ErrNotFound
		}
		for _, attachment := range representation.Attachments {
			if attachment.ID == partID && attachment.Path != "" && attachment.State != app.EmailAttachmentPurged {
				ref = sourceFile{Path: attachment.Path, SHA256: attachment.SHA256, Bytes: attachment.SizeBytes}
				name = path.Base(attachment.Name)
				break
			}
		}
	}
	if ref.Path == "" {
		return nil, "", ErrNotFound
	}
	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	file, err := openVerifiedFile(ctx, root, ref, maxSourceBytes)
	return file, name, err
}

func openVerifiedFile(ctx context.Context, root *os.Root, ref sourceFile, limit int64) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref.Bytes < 0 || ref.Bytes > limit || !safeRelativePath(ref.Path) || len(ref.SHA256) != 71 {
		return nil, errors.New("email_source_invalid")
	}
	file, err := root.Open(ref.Path)
	if err != nil {
		return nil, errors.New("email_source_missing")
	}
	ok := false
	defer func() {
		if !ok {
			file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != ref.Bytes {
		return nil, errors.New("email_source_invalid")
	}
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(contextReader{ctx, file}, ref.Bytes+1))
	if err != nil {
		return nil, err
	}
	if written != ref.Bytes || "sha256:"+hex.EncodeToString(digest.Sum(nil)) != ref.SHA256 {
		return nil, errors.New("email_source_integrity")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	ok = true
	return file, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
