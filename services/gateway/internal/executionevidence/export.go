package executionevidence

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type frozen struct {
	manifest Manifest
	events   []byte
	valid    []Payload
}

// freeze takes a coherent metadata snapshot under the writer lock. Blob bytes
// remain immutable; the exporter verifies them again while streaming the ZIP.
func (r *Recorder) freeze(ctx context.Context, scope Scope, diagnostic bool) (frozen, error) {
	root, unlock, err := r.rootLock(ctx, false)
	if err != nil {
		return frozen{}, err
	}
	m, events, err := r.load(root, scope)
	eventsUnavailable := err != nil && events == nil && m.EventBytes > 0
	unlock()
	if err != nil {
		if !diagnostic || m.Scope != scope || m.SchemaVersion != SchemaVersion {
			return frozen{}, err
		}
		r.addGap(&m, "event_log_integrity", "the stored event log could not be fully validated")
	}
	if !m.Capture.Sealed {
		m.Capture.Complete = false
		r.addGap(&m, "run_not_terminal", "this snapshot has not been sealed at a terminal business state")
	}
	if len(m.Capture.OpenSpans) > 0 {
		r.addGap(&m, "interrupted_spans", "one or more operations have no terminal event in this snapshot")
	}
	m.Export = ExportStatus{Complete: true, Diagnostic: diagnostic, MissingEntries: []string{}, GeneratedAt: time.Now().UTC()}
	if eventsUnavailable {
		m.Export.Complete = false
		m.Export.MissingEntries = append(m.Export.MissingEntries, "events.jsonl")
	}
	path, err := runPath(scope)
	if err != nil {
		return frozen{}, err
	}
	root, err = os.OpenRoot(r.root)
	if err != nil {
		return frozen{}, err
	}
	defer root.Close()
	valid := []Payload{}
	var total int64 = int64(len(events))
	for _, p := range m.Payloads {
		if !validPayload(p) {
			return frozen{}, ErrIntegrity
		}
		total += p.Bytes
		if total > r.opts.MaxExportBytes {
			return frozen{}, fmt.Errorf("%w: uncompressed export bytes", ErrLimit)
		}
		if err = verifyBlob(ctx, root, path, p); err != nil {
			if !diagnostic {
				return frozen{}, fmt.Errorf("%w: payload %s", ErrIncomplete, p.ID)
			}
			m.Export.Complete = false
			m.Export.MissingEntries = append(m.Export.MissingEntries, p.Path)
			r.addGap(&m, "payload_unavailable", "payload "+p.ID+" is missing or failed verification")
			continue
		}
		valid = append(valid, p)
	}
	if !diagnostic && !m.Capture.Complete {
		return frozen{}, ErrIncomplete
	}
	return frozen{manifest: m, events: events, valid: valid}, nil
}
func verifyBlob(ctx context.Context, root *os.Root, path string, p Payload) error {
	file, err := safeOpen(root, path+"/blobs/"+p.SHA256)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != p.Bytes {
		return ErrIntegrity
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx: ctx, reader: file}, p.Bytes+1))
	if err != nil {
		return err
	}
	if n != p.Bytes || hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
		return ErrIntegrity
	}
	return nil
}

// Export creates an exclusive output only after all entries have been written
// and verified. It never generates a deliverable during Begin/Record/Seal.
func (r *Recorder) Export(ctx context.Context, scope Scope, output string, diagnostic bool) (Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, r.opts.ExportTimeout)
	defer cancel()
	f, err := r.freeze(ctx, scope, diagnostic)
	if err != nil {
		return Manifest{}, err
	}
	err = exclusiveOutput(output, func(dst io.Writer) error { return r.writeExport(ctx, scope, f, dst) })
	return f.manifest, err
}
func (r *Recorder) writeExport(ctx context.Context, scope Scope, f frozen, dst io.Writer) error {
	root, err := os.OpenRoot(r.root)
	if err != nil {
		return err
	}
	defer root.Close()
	path, err := runPath(scope)
	if err != nil {
		return err
	}
	bounded := &limitWriter{writer: dst, remaining: r.opts.MaxExportBytes}
	archive := zip.NewWriter(bounded)
	manifestRaw, err := json.MarshalIndent(f.manifest, "", "  ")
	if err != nil {
		return err
	}
	for _, entry := range []struct {
		name string
		raw  []byte
	}{{"evaluation.json", append(manifestRaw, '\n')}, {"events.jsonl", f.events}, {"report.md", r.report(root, path, f)}} {
		if entry.name == "events.jsonl" && containsEntry(f.manifest.Export.MissingEntries, entry.name) {
			continue
		}
		if err = writeZipBytes(ctx, archive, entry.name, entry.raw); err != nil {
			_ = archive.Close()
			return err
		}
	}
	for _, p := range f.valid {
		if err = writeZipBlob(ctx, archive, root, path, p, p.Path); err != nil {
			_ = archive.Close()
			return err
		}
	}
	return archive.Close()
}
func writeZipBytes(ctx context.Context, archive *zip.Writer, name string, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(0600)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(raw)
	return err
}
func writeZipBlob(ctx context.Context, archive *zip.Writer, root *os.Root, path string, p Payload, name string) error {
	file, err := safeOpen(root, path+"/blobs/"+p.SHA256)
	if err != nil {
		return err
	}
	defer file.Close()
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(0600)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(entry, h), io.LimitReader(contextReader{ctx: ctx, reader: file}, p.Bytes+1))
	if err != nil {
		return err
	}
	if n != p.Bytes || hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
		return ErrIntegrity
	}
	return nil
}

type limitWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, ErrLimit
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func exclusiveOutput(output string, write func(io.Writer) error) error {
	if strings.TrimSpace(output) == "" {
		return errors.New("an explicit export output path is required")
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	dir := filepath.Dir(abs)
	name := filepath.Base(abs)
	if name == "." || name == string(filepath.Separator) {
		return ErrIntegrity
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrIntegrity
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err = root.Lstat(name); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := ".document-evaluation-" + randomID() + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	err = write(f)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Hard-link publication is atomic and refuses an output created concurrently.
	if err = root.Link(tmp, name); err != nil {
		return err
	}
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (r *Recorder) report(root *os.Root, path string, f frozen) []byte {
	m := f.manifest
	var b strings.Builder
	fmt.Fprintf(&b, "# Document execution evidence\n\nRun: `%s`\n\nBusiness status: `%s`\n\nCapture complete: **%t**; package complete: **%t**.\n\nReview: **owner_external / not_performed**. This report lists recorded facts; it does not grade the answer.\n", m.Scope.RunID, m.Status, m.Capture.Complete, m.Export.Complete)
	b.WriteString("\n## Original request context\n\n")
	for _, p := range m.Payloads {
		if p.ID == m.RequestPayloadID {
			fmt.Fprintf(&b, "Full frozen request: [%s](%s).\n\n", mdText(p.Name), p.Path)
			if p.Bytes <= 32<<10 {
				if raw, err := readBounded(root, path+"/blobs/"+p.SHA256, 32<<10); err == nil {
					b.WriteString("````json\n")
					b.Write(raw)
					b.WriteString("\n````\n")
				}
			} else {
				b.WriteString("The request is larger than the report preview; its complete bytes are in the referenced payload.\n")
			}
		}
	}
	b.WriteString("\n## Capture limitations\n\n")
	if len(m.Capture.Gaps) == 0 {
		b.WriteString("No recorded capture gaps. This does not establish answer correctness or full document coverage.\n")
	}
	for _, g := range m.Capture.Gaps {
		fmt.Fprintf(&b, "- `%s`: %s\n", mdText(g.Code), mdText(g.Detail))
	}
	for _, s := range m.Capture.OpenSpans {
		fmt.Fprintf(&b, "- Operation without a terminal event: `%s`.\n", mdText(s))
	}
	b.WriteString("\n## Payload inventory\n\n| Kind | Original display name | Bytes | SHA-256 | Evidence |\n|---|---|---:|---|---|\n")
	for _, p := range m.Payloads {
		fmt.Fprintf(&b, "| %s | %s | %d | `%s` | [payload](%s)%s |\n", mdText(p.Kind), mdText(p.Name), p.Bytes, p.SHA256, p.Path, redactedLabel(p))
	}
	b.WriteString("\n## Recorded timeline\n\n| Seq | Event | Stage | Status | Duration (ms) | Payload IDs |\n|---:|---|---|---|---:|---|\n")
	scanner := bufio.NewScanner(bytes.NewReader(f.events))
	scanner.Buffer(make([]byte, 4096), int(r.opts.MaxEventBytes))
	for scanner.Scan() {
		var e Event
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %d | %s |\n", e.Seq, mdText(e.Kind), mdText(e.Stage), mdText(e.Status), e.DurationMS, mdText(strings.Join(e.PayloadIDs, ", ")))
	}
	b.WriteString("\nThe complete event data, causal links, coverage, model telemetry and error records are in `events.jsonl`. Sources and outputs retain their captured bytes. Redacted payloads list the original digest and redaction reason in `evaluation.json`. No external model was invoked to create this report.\n")
	return []byte(b.String())
}
func mdText(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ", "`", "'").Replace(s)
}
func redactedLabel(p Payload) string {
	if p.Redacted {
		return " (redacted)"
	}
	return ""
}

func containsEntry(entries []string, name string) bool {
	for _, entry := range entries {
		if entry == name {
			return true
		}
	}
	return false
}
