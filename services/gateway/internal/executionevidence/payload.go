package executionevidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

func Capture(ctx context.Context, kind, name, contentType string, reader io.Reader) (Payload, error) {
	b, ok := ctx.Value(bindingKey{}).(binding)
	if !ok || b.recorder == nil {
		return Payload{}, ErrUnbound
	}
	return b.recorder.Capture(ctx, b.scope, kind, name, contentType, reader)
}
func CaptureJSON(ctx context.Context, kind, name string, value any) (Payload, error) {
	b, ok := ctx.Value(bindingKey{}).(binding)
	if !ok || b.recorder == nil {
		return Payload{}, ErrUnbound
	}
	return b.recorder.CaptureJSON(ctx, b.scope, kind, name, value)
}
func (r *Recorder) CaptureJSON(ctx context.Context, scope Scope, kind, name string, value any) (Payload, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return Payload{}, err
	}
	return r.Capture(ctx, scope, kind, name, "application/json", bytes.NewReader(raw))
}
func (r *Recorder) Capture(ctx context.Context, scope Scope, kind, name, contentType string, reader io.Reader) (Payload, error) {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	path, err := runPath(scope)
	if err != nil {
		return Payload{}, err
	}
	root, unlock, err := r.rootLock(ctx, false)
	if err != nil {
		return Payload{}, err
	}
	defer unlock()
	m, err := r.loadForWrite(root, scope)
	if err != nil {
		return Payload{}, err
	}
	if m.Capture.Sealed {
		return Payload{}, ErrSealed
	}
	p, err := r.captureLocked(ctx, root, path, &m, kind, name, contentType, reader)
	if err != nil {
		return p, r.failLocked(root, path, &m, "payload_capture_failed", err)
	}
	return p, nil
}
func (r *Recorder) redact(raw []byte, contentType, kind string) ([]byte, bool, error) {
	if kind == "source" || kind == "output" || r.opts.Redact == nil {
		return raw, false, nil
	}
	ct, _, _ := mime.ParseMediaType(contentType)
	if ct != "application/json" && !strings.HasSuffix(ct, "+json") && !strings.HasPrefix(ct, "text/") {
		return raw, false, nil
	}
	if !utf8.Valid(raw) {
		return nil, false, fmt.Errorf("%w: text encoding", ErrIntegrity)
	}
	redacted := r.opts.Redact(bytes.Clone(raw))
	if !utf8.Valid(redacted) {
		return nil, false, fmt.Errorf("%w: redaction encoding", ErrIntegrity)
	}
	if (ct == "application/json" || strings.HasSuffix(ct, "+json")) && !json.Valid(redacted) {
		return nil, false, fmt.Errorf("%w: redaction JSON", ErrIntegrity)
	}
	return redacted, !bytes.Equal(raw, redacted), nil
}
func (r *Recorder) captureLocked(ctx context.Context, root *os.Root, path string, m *Manifest, kind, name, contentType string, reader io.Reader) (Payload, error) {
	if reader == nil || !validID(kind) || len(name) > 1024 || strings.Contains(name, "\x00") {
		return Payload{}, ErrIntegrity
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		return Payload{}, ErrIntegrity
	}
	remaining, err := r.remainingSpace(ctx, root, *m)
	if err != nil {
		return Payload{}, err
	}
	limit := min(r.opts.MaxPayloadBytes, remaining)
	if limit <= 0 {
		return Payload{}, ErrLimit
	}
	if err = safeMkdir(root, path+"/blobs"); err != nil {
		return Payload{}, err
	}
	// Reserve before creating any temporary bytes so a crash cannot leave
	// an uncharged orphan. Successful publication refunds unused capacity.
	if err = r.checkSpace(ctx, root, *m, limit); err != nil {
		return Payload{}, err
	}
	tmp := path + "/blobs/.tmp-" + randomID()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return Payload{}, err
	}
	defer root.Remove(tmp)
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(contextReader{ctx: ctx, reader: reader}, limit+1))
	if err == nil && n > limit {
		err = ErrLimit
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return Payload{}, err
	}
	if closeErr != nil {
		return Payload{}, closeErr
	}
	originalHash := hex.EncodeToString(hash.Sum(nil))
	exportHash := originalHash
	redacted := false
	ct, _, _ := mime.ParseMediaType(contentType)
	if kind != "source" && kind != "output" && r.opts.Redact != nil && (ct == "application/json" || strings.HasSuffix(ct, "+json") || strings.HasPrefix(ct, "text/")) {
		raw, readErr := readBounded(root, tmp, limit)
		if readErr != nil {
			return Payload{}, readErr
		}
		safe, changed, redactErr := r.redact(raw, contentType, kind)
		if redactErr != nil {
			return Payload{}, redactErr
		}
		if int64(len(safe)) > limit {
			return Payload{}, ErrLimit
		}
		if changed {
			if err = atomicWrite(root, tmp, safe); err != nil {
				return Payload{}, err
			}
			n = int64(len(safe))
			exportHash = digest(safe)
			redacted = true
		}
	}
	p := Payload{Kind: kind, Name: name, ContentType: contentType, Bytes: n, SHA256: exportHash, Redacted: redacted, CreatedAt: time.Now().UTC()}
	p.ID = digest([]byte(kind + "\x00" + name + "\x00" + contentType + "\x00" + exportHash))
	p.Path = payloadEntry(kind, p.ID, contentType)
	if redacted {
		p.OriginalSHA256 = originalHash
		p.RedactionReason = "configured_capture_redaction"
	}
	for _, existing := range m.Payloads {
		if existing.ID == p.ID {
			if err = r.releaseReservation(root, ctx, limit+4096); err != nil {
				return Payload{}, err
			}
			return existing, nil
		}
	}
	blob := path + "/blobs/" + p.SHA256
	if err = safePath(root, blob, true); err != nil {
		return Payload{}, err
	}
	if _, err = root.Lstat(blob); errors.Is(err, os.ErrNotExist) {
		err = root.Rename(tmp, blob)
	} else if err == nil {
		file, openErr := safeOpen(root, blob)
		if openErr != nil {
			return Payload{}, openErr
		}
		h := sha256.New()
		size, copyErr := io.Copy(h, contextReader{ctx: ctx, reader: file})
		file.Close()
		if copyErr != nil {
			return Payload{}, copyErr
		}
		if size != p.Bytes || hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
			return Payload{}, ErrIntegrity
		}
	}
	if err != nil {
		return Payload{}, err
	}
	m.Payloads = append(m.Payloads, p)
	if err = atomicJSON(root, path+"/manifest.json", m); err != nil {
		return Payload{}, err
	}
	if err = r.releaseReservation(root, ctx, limit-n); err != nil {
		return Payload{}, err
	}
	return p, nil
}
func payloadEntry(kind, id, contentType string) string {
	ext := payloadExtension(contentType)
	switch kind {
	case "source":
		return "sources/" + id + "/original" + ext
	case "output":
		return "outputs/" + id + "/output" + ext
	case "asset", "image":
		return "assets/" + id + ext
	case "model_request", "model_response", "model_call":
		return "calls/" + id + ext
	default:
		return "evidence/" + id + ext
	}
}
func payloadExtension(contentType string) string {
	ct, _, _ := mime.ParseMediaType(contentType)
	switch ct {
	case "application/json":
		return ".json"
	case "application/pdf":
		return ".pdf"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "text/markdown":
		return ".md"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".bin"
	}
}
