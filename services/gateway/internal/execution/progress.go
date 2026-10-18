package execution

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/latencytrace"
)

const ProgressBytes = 8 << 10
const progressTotalBytes = 8 << 20

// At most 8 MiB / 8 KiB retained previews, plus the existing eight active runs.
// Saturation suppresses previews only; execution and reliable delivery continue.
const progressRetainedEntries = progressTotalBytes / ProgressBytes

// Progress is an ephemeral preview. Only Lookup followed by a durable Ack
// delivers a result; neither this snapshot nor its digest is a delivery receipt.
type Progress struct {
	SchemaVersion int    `json:"schema_version"`
	Epoch         string `json:"epoch"`
	RequestID     string `json:"request_id"`
	InputDigest   string `json:"input_digest"`
	Revision      uint64 `json:"revision"`
	Content       string `json:"content"`
	Digest        string `json:"digest"`
	State         string `json:"state"`
	Truncated     bool   `json:"truncated"`
}

type ProgressInput struct {
	InputDigest   string `json:"input_digest"`
	AfterRevision uint64 `json:"after_revision"`
	WaitMS        int    `json:"wait_ms"`
}

func (p ProgressInput) Valid() bool {
	return len(p.InputDigest) == 64 && p.AfterRevision <= 9007199254740991 && p.WaitMS >= 0 && p.WaitMS <= 10000
}

type progressEntry struct {
	snapshot Progress
	changed  chan struct{}
}
type progressContextKey struct{}
type progressPublisher func(string)

// PublishProgress accepts final-answer text deltas only. The caller must filter
// model/tool/planning events before invoking it. It is a no-op outside a run.
func PublishProgress(ctx context.Context, text string) {
	if ctx.Err() != nil {
		return
	}
	if publish, ok := ctx.Value(progressContextKey{}).(progressPublisher); ok {
		publish(text)
	}
}
func (s *Service) progressEntryLocked(key string, f Fence) *progressEntry {
	p := s.progress[key]
	if p == nil {
		p = &progressEntry{snapshot: Progress{SchemaVersion: 1, Epoch: s.progressEpoch, RequestID: f.RequestID, InputDigest: f.InputDigest, Revision: max(1, f.progressRevision), Digest: Digest(nil), State: f.progressState, Truncated: f.progressTruncated}, changed: make(chan struct{})}
		if p.snapshot.State == "" {
			p.snapshot.State = f.State
		}
		s.progress[key] = p
	}
	s.refreshProgressLocked(key, p)
	return p
}
func progressTerminal(state string) bool {
	return state != "accepted" && state != "running" && state != "approval_pending" && state != "browser_login_blocked"
}
func (p *progressEntry) notify() { close(p.changed); p.changed = make(chan struct{}) }
func (s *Service) refreshProgressLocked(key string, p *progressEntry) {
	f := s.control.Fences[key]
	state := f.State
	if (state == "accepted" || state == "running") && !f.Deadline.After(s.now()) {
		state = "unknown"
	}
	if state == "completed" && (f.ExpiresAt == nil || !f.ExpiresAt.After(s.now())) {
		state = "delivery_expired"
	}
	changed := p.snapshot.State != state
	p.snapshot.State = state
	if ((progressTerminal(state) && state != "completed") || s.closed || s.revokedClients[f.ClientID]) && p.snapshot.Content != "" {
		s.progressBytes -= len(p.snapshot.Content)
		p.snapshot.Content, p.snapshot.Digest = "", Digest(nil)
		changed = true
	}
	if changed {
		p.snapshot.Revision++
		p.notify()
	}
	s.rememberProgressLocked(key, p)
}
func (s *Service) rememberProgressLocked(key string, p *progressEntry) {
	f := s.control.Fences[key]
	f.progressRevision, f.progressState, f.progressTruncated = p.snapshot.Revision, p.snapshot.State, p.snapshot.Truncated
	s.control.Fences[key] = f
}

// Run after business rollback paths have settled, while holding mu.
func (s *Service) pruneProgressLocked() {
	s.syncProgressLocked()
	for key, p := range s.progress {
		if progressTerminal(p.snapshot.State) && p.snapshot.Content == "" {
			delete(s.progress, key)
		}
	}
}
func (s *Service) syncProgressLocked() {
	s.syncAnswersLocked()
	for key, p := range s.progress {
		s.refreshProgressLocked(key, p)
	}
}
func (s *Service) publishProgress(ctx context.Context, key, text string) {
	if text == "" || !utf8.ValidString(text) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.control.Fences[key]
	if !ok || s.closed || s.revokedClients[f.ClientID] || f.State != "running" || !f.Deadline.After(s.now()) {
		return
	}
	p := s.progressEntryLocked(key, f)
	if p.snapshot.Truncated {
		return
	}
	remaining := min(ProgressBytes-len(p.snapshot.Content), progressTotalBytes-s.progressBytes)
	if len(s.progress) > progressRetainedEntries && p.snapshot.Content == "" {
		remaining = 0
	}
	if len(text) > remaining {
		text = text[:remaining]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		p.snapshot.Truncated = true
	}
	firstText := p.snapshot.Content == "" && text != ""
	p.snapshot.Content += text
	s.progressBytes += len(text)
	p.snapshot.Digest = Digest([]byte(p.snapshot.Content))
	p.snapshot.Revision++
	p.notify()
	s.rememberProgressLocked(key, p)
	if firstText {
		latencytrace.At(ctx, "execution.progress_first_text", latencytrace.Clock())
	}
}

// ReadProgress binds every read and wake-up to the original execution identity
// and digest. It never creates, resumes, or repeats an execution.
func (s *Service) ReadProgress(ctx context.Context, owner, client, installation, request string, input ProgressInput) (result Progress, err error) {
	started := latencytrace.Clock()
	defer func() { latencytrace.Record(ctx, "execution.progress_read", started, err, 0) }()
	if !input.Valid() || !UUID(request) {
		return Progress{}, ErrConflict
	}
	deadline := time.NewTimer(time.Duration(input.WaitMS) * time.Millisecond)
	defer deadline.Stop()
	wait := input.WaitMS > 0
	for {
		if err := ctx.Err(); err != nil {
			return Progress{}, err
		}
		s.mu.Lock()
		if s.closed || s.revokedClients[client] {
			s.mu.Unlock()
			return Progress{}, ErrExpired
		}
		if !UUID(installation) || s.control.Installations[keyFor(owner, client, "")] != installation {
			s.mu.Unlock()
			return Progress{}, ErrConflict
		}
		key := keyFor(owner, client, request)
		f, ok := s.control.Fences[key]
		if !ok {
			s.mu.Unlock()
			return Progress{}, ErrNotFound
		}
		if f.InstallationID != installation || f.InputDigest != input.InputDigest {
			s.mu.Unlock()
			return Progress{}, ErrConflict
		}
		p := s.progressEntryLocked(key, f)
		snapshot, changed := p.snapshot, p.changed
		if progressTerminal(snapshot.State) && snapshot.Content == "" {
			delete(s.progress, key)
		}
		s.mu.Unlock()
		if !wait || snapshot.Revision != input.AfterRevision || progressTerminal(snapshot.State) {
			return snapshot, nil
		}
		select {
		case <-ctx.Done():
			return Progress{}, ctx.Err()
		case <-deadline.C:
			wait = false
		case <-changed:
		}
	}
}
