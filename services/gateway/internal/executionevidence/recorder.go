package executionevidence

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Recorder writes append-only events and immutable payload snapshots. The
// private root is separate from ordinary trace JSON and contains no report/ZIP.
type Recorder struct {
	root string
	opts Options
}

func DefaultOptions() Options {
	return Options{
		MaxPayloadBytes: 128 << 20, MaxRunBytes: 512 << 20, MaxTotalBytes: 2 << 30,
		MaxExportBytes: 1 << 30, MaxEventBytes: 1 << 20, MaxEvents: 20000,
		OperationTimeout: 30 * time.Second, ExportTimeout: 2 * time.Minute,
	}
}
func New(root string, opts Options) *Recorder {
	d := DefaultOptions()
	if opts.MaxPayloadBytes <= 0 {
		opts.MaxPayloadBytes = d.MaxPayloadBytes
	}
	if opts.MaxRunBytes <= 0 {
		opts.MaxRunBytes = d.MaxRunBytes
	}
	if opts.MaxTotalBytes <= 0 {
		opts.MaxTotalBytes = d.MaxTotalBytes
	}
	if opts.MaxExportBytes <= 0 {
		opts.MaxExportBytes = d.MaxExportBytes
	}
	if opts.MaxEventBytes <= 0 {
		opts.MaxEventBytes = d.MaxEventBytes
	}
	if opts.MaxEvents <= 0 {
		opts.MaxEvents = d.MaxEvents
	}
	if opts.OperationTimeout <= 0 {
		opts.OperationTimeout = d.OperationTimeout
	}
	if opts.ExportTimeout <= 0 {
		opts.ExportTimeout = d.ExportTimeout
	}
	return &Recorder{root: root, opts: opts}
}
func (r *Recorder) Root() string {
	if r == nil {
		return ""
	}
	return r.root
}
func (r *Recorder) timeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, r.opts.OperationTimeout)
}
func runPath(scope Scope) (string, error) { key, err := scopeKey(scope); return "runs/" + key, err }

// Begin freezes the request once. Repeating an identical request is idempotent;
// a later context cannot replace the original request of a resumed run.
func (r *Recorder) Begin(ctx context.Context, scope Scope, request any) error {
	path, err := runPath(scope)
	if err != nil {
		return err
	}
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	root, unlock, err := r.rootLock(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	if err = safePath(root, path, true); err != nil {
		return err
	}
	if _, statErr := root.Lstat(path + "/manifest.json"); statErr == nil {
		m, err := r.loadForWrite(root, scope)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(request)
		if err != nil {
			return err
		}
		raw, _, err = r.redact(raw, "application/json", "request")
		if err != nil {
			return err
		}
		for _, p := range m.Payloads {
			if p.ID == m.RequestPayloadID && p.SHA256 == digest(raw) {
				return nil
			}
		}
		return fmt.Errorf("%w: request differs from frozen context", ErrIntegrity)
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return statErr
	}
	m := Manifest{SchemaVersion: SchemaVersion, Scope: scope, Status: "running", StartedAt: time.Now().UTC(), Payloads: []Payload{}, Capture: CaptureStatus{Gaps: []Gap{}, OpenSpans: []string{}}, Export: ExportStatus{MissingEntries: []string{}}, Review: ReviewStatus{Mode: "owner_external", Status: "not_performed"}}
	// Reserve bounded metadata/gap space before admitting a new capture.
	if err = r.checkSpace(ctx, root, Manifest{}, 64<<10); err != nil {
		return err
	}
	if err = safeMkdir(root, path); err != nil {
		return err
	}
	if err = atomicJSON(root, path+"/manifest.json", m); err != nil {
		return err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return r.failLocked(root, path, &m, "request_encode_failed", err)
	}
	p, err := r.captureLocked(ctx, root, path, &m, "request", "request.json", "application/json", bytes.NewReader(raw))
	if err != nil {
		return r.failLocked(root, path, &m, "request_capture_failed", err)
	}
	m.RequestPayloadID = p.ID
	if err = r.appendLocked(ctx, root, path, &m, Event{Kind: "run.begin", Status: "running", PayloadIDs: []string{p.ID}}); err != nil {
		return r.failLocked(root, path, &m, "begin_record_failed", err)
	}
	return atomicJSON(root, path+"/manifest.json", m)
}
func Record(ctx context.Context, event Event) error {
	b, ok := ctx.Value(bindingKey{}).(binding)
	if !ok || b.recorder == nil {
		return nil
	}
	if event.ParentID == "" {
		event.ParentID = b.parentID
	}
	return b.recorder.Record(context.WithoutCancel(ctx), b.scope, event)
}
func (r *Recorder) Record(ctx context.Context, scope Scope, event Event) error {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	path, err := runPath(scope)
	if err != nil {
		return err
	}
	root, unlock, err := r.rootLock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := r.loadForWrite(root, scope)
	if err != nil {
		return err
	}
	if m.Capture.Sealed {
		return ErrSealed
	}
	if event.ID != "" {
		_, raw, readErr := r.load(root, scope)
		if readErr != nil {
			return readErr
		}
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 4096), int(r.opts.MaxEventBytes))
		for scanner.Scan() {
			var previous Event
			if json.Unmarshal(scanner.Bytes(), &previous) == nil && previous.ID == event.ID {
				return fmt.Errorf("%w: duplicate event ID", ErrIntegrity)
			}
		}
	}
	if err = r.appendLocked(ctx, root, path, &m, event); err != nil {
		return r.failLocked(root, path, &m, "event_capture_failed", err)
	}
	return nil
}
func (r *Recorder) appendLocked(ctx context.Context, root *os.Root, path string, m *Manifest, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if int(m.EventCount) >= r.opts.MaxEvents {
		return fmt.Errorf("%w: events", ErrLimit)
	}
	if event.Kind == "" {
		return errors.New("execution evidence event kind is empty")
	}
	if event.ID == "" {
		event.ID = "ev_" + randomID()
	}
	if !validID(event.ID) {
		return ErrIntegrity
	}
	for _, id := range event.PayloadIDs {
		found := false
		for _, p := range m.Payloads {
			if p.ID == id {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: event payload not captured", ErrIntegrity)
		}
	}
	event.RunID = m.Scope.RunID
	event.Seq = m.EventCount + 1
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	raw, changed, err := r.redact(raw, "application/json", "event")
	if err != nil {
		return err
	}
	if changed {
		if err = json.Unmarshal(raw, &event); err != nil {
			return err
		}
		// Identity fields are not mutable through content redaction.
		event.RunID = m.Scope.RunID
		event.Seq = m.EventCount + 1
		if event.Data == nil {
			event.Data = map[string]any{}
		}
		event.Data["capture_redacted"] = true
		raw, err = json.Marshal(event)
		if err != nil {
			return err
		}
	}
	raw = append(raw, '\n')
	if int64(len(raw)) > r.opts.MaxEventBytes {
		return fmt.Errorf("%w: event bytes", ErrLimit)
	}
	if err = r.checkSpace(ctx, root, *m, int64(len(raw))); err != nil {
		return err
	}
	if err = safePath(root, path+"/events.jsonl", true); err != nil {
		return err
	}
	f, err := root.OpenFile(path+"/events.jsonl", os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
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
	m.EventCount++
	m.EventBytes += int64(len(raw))
	updateOpenSpans(&m.Capture, event)
	return atomicJSON(root, path+"/manifest.json", m)
}
func updateOpenSpans(c *CaptureStatus, event Event) {
	id := event.SpanID
	if id == "" {
		id = event.CallID
	}
	if id == "" {
		return
	}
	open := map[string]bool{}
	for _, s := range c.OpenSpans {
		open[s] = true
	}
	switch event.Status {
	case "started", "running", "dispatched", "pending":
		open[id] = true
	case "completed", "failed", "cancelled", "canceled", "skipped", "blocked", "rejected":
		delete(open, id)
	}
	c.OpenSpans = c.OpenSpans[:0]
	for s := range open {
		c.OpenSpans = append(c.OpenSpans, s)
	}
	sort.Strings(c.OpenSpans)
}
func MarkGap(ctx context.Context, code, detail string) error {
	b, ok := ctx.Value(bindingKey{}).(binding)
	if !ok || b.recorder == nil {
		return nil
	}
	return b.recorder.MarkGap(context.WithoutCancel(ctx), b.scope, code, detail)
}
func (r *Recorder) MarkGap(ctx context.Context, scope Scope, code, detail string) error {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	path, err := runPath(scope)
	if err != nil {
		return err
	}
	root, unlock, err := r.rootLock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := r.loadForWrite(root, scope)
	if err != nil {
		return err
	}
	r.addGap(&m, code, detail)
	return atomicJSON(root, path+"/manifest.json", m)
}
func (r *Recorder) addGap(m *Manifest, code, detail string) {
	m.Capture.Complete = false
	if len(detail) > 512 {
		detail = detail[:512]
	}
	for _, g := range m.Capture.Gaps {
		if g.Code == code && g.Detail == detail {
			return
		}
	}
	if len(m.Capture.Gaps) >= 32 {
		return
	}
	if r.opts.Redact != nil {
		raw, err := json.Marshal(detail)
		if err == nil {
			safe := r.opts.Redact(raw)
			if json.Valid(safe) {
				_ = json.Unmarshal(safe, &detail)
			}
		}
	}
	m.Capture.Gaps = append(m.Capture.Gaps, Gap{Code: code, Detail: detail, At: time.Now().UTC()})
}
func (r *Recorder) failLocked(root *os.Root, path string, m *Manifest, code string, cause error) error {
	r.addGap(m, code, cause.Error())
	if err := atomicJSON(root, path+"/manifest.json", m); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}
func (r *Recorder) Seal(ctx context.Context, scope Scope, status string) error {
	if !terminalStatus(status) {
		return fmt.Errorf("%w: business state %q is not terminal", ErrIncomplete, status)
	}
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	path, err := runPath(scope)
	if err != nil {
		return err
	}
	root, unlock, err := r.rootLock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	// A seal is an integrity boundary. Reconcile the complete chain, including
	// request and payload references, before asserting capture completeness.
	m, _, err := r.load(root, scope)
	if err != nil {
		return err
	}
	if m.Capture.Sealed {
		if m.Status == status {
			return nil
		}
		return ErrSealed
	}
	if err = r.appendLocked(ctx, root, path, &m, Event{Kind: "run.sealed", Status: status}); err != nil {
		return r.failLocked(root, path, &m, "seal_event_failed", err)
	}
	m.Status = status
	m.Capture.Sealed = true
	now := time.Now().UTC()
	m.CompletedAt = &now
	if len(m.Capture.OpenSpans) > 0 {
		r.addGap(&m, "interrupted_spans", "one or more dispatched operations lack a terminal event")
	}
	m.Capture.Complete = len(m.Capture.Gaps) == 0 && len(m.Capture.OpenSpans) == 0
	return atomicJSON(root, path+"/manifest.json", m)
}

func terminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "blocked", "cancelled":
		return true
	default:
		return false
	}
}
func (r *Recorder) Read(ctx context.Context, scope Scope) (Manifest, error) {
	ctx, cancel := r.timeout(ctx)
	defer cancel()
	root, unlock, err := r.rootLock(ctx, false)
	if err != nil {
		return Manifest{}, err
	}
	defer unlock()
	m, _, err := r.load(root, scope)
	return m, err
}
func (r *Recorder) loadForWrite(root *os.Root, scope Scope) (Manifest, error) {
	path, err := runPath(scope)
	if err != nil {
		return Manifest{}, err
	}
	raw, err := readBounded(root, path+"/manifest.json", 16<<20)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if json.Unmarshal(raw, &m) != nil {
		return Manifest{}, ErrIntegrity
	}
	if m.Scope != scope || m.SchemaVersion != SchemaVersion {
		return Manifest{}, ErrScope
	}
	if err := validateCaptureMetadata(m); err != nil {
		return m, err
	}
	info, err := root.Lstat(path + "/events.jsonl")
	if errors.Is(err, fs.ErrNotExist) && m.EventCount == 0 {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return m, ErrIntegrity
	}
	if info.Size() == m.EventBytes {
		return m, nil
	}
	recovered, _, err := r.load(root, scope)
	return recovered, err
}
func (r *Recorder) load(root *os.Root, scope Scope) (Manifest, []byte, error) {
	path, err := runPath(scope)
	if err != nil {
		return Manifest{}, nil, err
	}
	raw, err := readBounded(root, path+"/manifest.json", 16<<20)
	if err != nil {
		return Manifest{}, nil, err
	}
	var m Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return m, nil, ErrIntegrity
	}
	if m.Scope != scope || m.SchemaVersion != SchemaVersion {
		return Manifest{}, nil, ErrScope
	}
	events, err := readBounded(root, path+"/events.jsonl", min(r.opts.MaxRunBytes, int64(r.opts.MaxEvents)*r.opts.MaxEventBytes))
	if errors.Is(err, fs.ErrNotExist) && m.EventCount == 0 {
		events = nil
		err = nil
	}
	if err != nil {
		return m, nil, err
	}
	if err := validateCaptureMetadata(m); err != nil {
		return m, events, err
	}
	payloads := make(map[string]bool, len(m.Payloads))
	for _, p := range m.Payloads {
		payloads[p.ID] = true
	}
	scanner := bufio.NewScanner(bytes.NewReader(events))
	scanner.Buffer(make([]byte, 4096), int(r.opts.MaxEventBytes))
	var count uint64
	var last Event
	seen := map[string]bool{}
	m.Capture.OpenSpans = []string{}
	for scanner.Scan() {
		var e Event
		if json.Unmarshal(scanner.Bytes(), &e) != nil || e.RunID != scope.RunID || e.Seq != count+1 || !validID(e.ID) || seen[e.ID] {
			return m, events, ErrIntegrity
		}
		if count == 0 && (e.Kind != "run.begin" || len(e.PayloadIDs) != 1 || e.PayloadIDs[0] != m.RequestPayloadID) {
			return m, events, ErrIntegrity
		}
		for _, id := range e.PayloadIDs {
			if !payloads[id] {
				return m, events, ErrIntegrity
			}
		}
		seen[e.ID] = true
		count++
		last = e
		updateOpenSpans(&m.Capture, e)
	}
	if scanner.Err() != nil || len(events) > 0 && events[len(events)-1] != '\n' {
		return m, events, ErrIntegrity
	}
	if count == 0 || count < m.EventCount || count > uint64(r.opts.MaxEvents) {
		return m, events, ErrIntegrity
	}
	if m.Capture.Sealed && (!terminalStatus(m.Status) || m.CompletedAt == nil || last.Kind != "run.sealed" || last.Status != m.Status) {
		return m, events, ErrIntegrity
	}
	m.EventCount = count
	m.EventBytes = int64(len(events))
	if !m.Capture.Sealed || len(m.Capture.OpenSpans) > 0 || len(m.Capture.Gaps) > 0 {
		m.Capture.Complete = false
	}
	return m, events, nil
}

func validateCaptureMetadata(m Manifest) error {
	// A crash before Begin publishes its request/begin event must never become
	// a complete run merely because later hooks resume this directory.
	requestFound := false
	seen := make(map[string]bool, len(m.Payloads))
	for _, p := range m.Payloads {
		if !validPayload(p) || seen[p.ID] {
			return ErrIntegrity
		}
		seen[p.ID] = true
		requestFound = requestFound || p.ID == m.RequestPayloadID && p.Kind == "request"
	}
	if !requestFound {
		return fmt.Errorf("%w: frozen request is unavailable", ErrIntegrity)
	}
	return nil
}
func validPayload(p Payload) bool {
	return validID(p.Kind) && len(p.ID) == 64 && hexDigest(p.ID) && len(p.SHA256) == 64 && hexDigest(p.SHA256) && p.Bytes >= 0 &&
		p.ID == digest([]byte(p.Kind+"\x00"+p.Name+"\x00"+p.ContentType+"\x00"+p.SHA256)) &&
		p.Path == payloadEntry(p.Kind, p.ID, p.ContentType) && len(p.Name) <= 1024 && !strings.Contains(p.Name, "\x00")
}

func hexDigest(s string) bool {
	for _, ch := range s {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}
