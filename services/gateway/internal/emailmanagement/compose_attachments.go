package emailmanagement

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"golang.org/x/sys/unix"
)

var ErrAttachmentInvalid = errors.New("email_attachment_invalid")
var ErrAttachmentChanged = errors.New("email_attachment_changed")
var ErrAttachmentPermission = errors.New("email_attachment_permission_denied")

// AttachmentObjectReader is supplied by an authenticated transport. The domain
// never chooses a filesystem root or treats possession of an object ID as access.
type AttachmentObjectReader interface {
	ReadAttachmentObject(context.Context, string, app.EmailAttachmentObject, int64) (app.EmailAttachmentObject, []byte, error)
}

type attachmentAccessKey struct{}
type attachmentAccess struct {
	permitted bool
	reader    AttachmentObjectReader
}

// WithAttachmentObjects carries the already verified caller's object resolver
// and files.read scope into the single authoritative draft read. No resolver
// means no attachment access, including legacy host HTTP calls.
func WithAttachmentObjects(ctx context.Context, permitted bool, reader AttachmentObjectReader) context.Context {
	return context.WithValue(ctx, attachmentAccessKey{}, attachmentAccess{permitted, reader})
}
func checkAttachmentPermission(ctx context.Context, attachments []app.EmailSendAttachment) error {
	access, ok := ctx.Value(attachmentAccessKey{}).(attachmentAccess)
	if len(attachments) > 0 && (!ok || !access.permitted || access.reader == nil) {
		return ErrAttachmentPermission
	}
	return nil
}

var attachmentLocalID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validAttachmentName(name string) bool {
	if name == "" || len(name) > 255 || !utf8.ValidString(name) || strings.HasPrefix(name, ".") || path.Base(name) != name || strings.ContainsAny(name, "\\:") {
		return false
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func (s *Service) readDraftAttachments(ctx context.Context, owner string, attachments []app.EmailSendAttachment, verify bool) ([]app.EmailSendAttachment, [][]byte, error) {
	if len(attachments) == 0 {
		return nil, nil, nil
	}
	if err := checkAttachmentPermission(ctx, attachments); err != nil {
		return nil, nil, err
	}
	invalid := ErrAttachmentInvalid
	if verify {
		invalid = ErrAttachmentChanged
	}
	if len(attachments) > app.EmailSendMaxAttachments {
		return nil, nil, invalid
	}
	access := ctx.Value(attachmentAccessKey{}).(attachmentAccess)
	manifest := make([]app.EmailSendAttachment, 0, len(attachments))
	contents := make([][]byte, 0, len(attachments))
	seenFiles := map[string]bool{}
	var total int64
	for _, attachment := range attachments {
		if attachment.Path != "" || !attachmentLocalID.MatchString(attachment.LocalFileID) || seenFiles[attachment.LocalFileID] || attachment.Object.Purpose != app.EmailSendAttachmentPurpose {
			return nil, nil, invalid
		}
		seenFiles[attachment.LocalFileID] = true
		object, data, err := access.reader.ReadAttachmentObject(ctx, owner, attachment.Object, app.EmailSendMaxAttachmentBytes-total)
		if err != nil || !validAttachmentName(object.Name) || object.Purpose != app.EmailSendAttachmentPurpose || int64(len(data)) > app.EmailSendMaxAttachmentBytes-total {
			return nil, nil, invalid
		}
		sum := sha256.Sum256(data)
		if object.Size != int64(len(data)) || object.SHA256 != hex.EncodeToString(sum[:]) {
			return nil, nil, invalid
		}
		item := app.EmailSendAttachment{LocalFileID: attachment.LocalFileID, Object: object, Name: object.Name, SizeBytes: int64(len(data)), SHA256: "sha256:" + object.SHA256}
		if verify && item != attachment {
			return nil, nil, invalid
		}
		total += item.SizeBytes
		manifest = append(manifest, item)
		contents = append(contents, data)
	}
	return manifest, contents, nil
}

// stageAttachments pins exactly the reviewed bytes in an isolated private
// directory shared with the browser runtime. Neither the provider nor replay
// reads a host workspace source path.
func (s *Service) stageAttachments(attachments []app.EmailSendAttachment, contents [][]byte) ([]app.EmailSendAttachment, func(), error) {
	cleanup := func() {}
	if len(attachments) == 0 {
		return nil, cleanup, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if err != nil {
		return nil, cleanup, err
	}
	entries, err := os.ReadDir(s.opts.WorkspaceRoot)
	if err != nil {
		root.Close()
		return nil, cleanup, err
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sparkclaw-mail-send-") {
			count++
		}
	}
	if count >= 32 {
		root.Close()
		return nil, cleanup, ErrAttachmentInvalid
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		root.Close()
		return nil, cleanup, err
	}
	directory := ".sparkclaw-mail-send-" + hex.EncodeToString(token[:])
	if err = root.Mkdir(directory, 0700); err != nil {
		root.Close()
		return nil, cleanup, err
	}
	cleanup = func() { _ = root.RemoveAll(directory); _ = root.Close() }
	// The newly created, unpredictable directory is opened without symlinks.
	fd, err := unix.Open(s.opts.WorkspaceRoot, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	dirfd, err := unix.Openat(fd, directory, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	unix.Close(fd)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	defer unix.Close(dirfd)
	out := append([]app.EmailSendAttachment(nil), attachments...)
	for i, item := range out {
		// Different desktop files may have the same basename; numbered storage
		// names prevent overwrite while provider upload still uses the original name.
		index := hex.EncodeToString([]byte{byte(i)})
		if err := unix.Mkdirat(dirfd, index, 0700); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		childfd, err := unix.Openat(dirfd, index, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		name := item.Name
		filefd, err := unix.Openat(childfd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		unix.Close(childfd)
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		file := os.NewFile(uintptr(filefd), name)
		_, err = file.Write(contents[i])
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		out[i].StagedPath = path.Join(directory, index, name)
	}
	return out, cleanup, nil
}

// A process restart terminates in-flight sends through the durable send fence.
// Their private staging copies are no longer executable and can be removed.
func (s *Service) cleanupAttachmentStages() error {
	root, err := os.OpenRoot(s.opts.WorkspaceRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := os.ReadDir(s.opts.WorkspaceRoot)
	if err != nil {
		return err
	}
	pattern := regexp.MustCompile(`^\.sparkclaw-mail-send-[a-f0-9]{32}$`)
	for _, entry := range entries {
		if pattern.MatchString(entry.Name()) {
			if err := root.RemoveAll(entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}
