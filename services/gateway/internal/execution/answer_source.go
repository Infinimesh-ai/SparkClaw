package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const AnswerReplayRetention = 30 * time.Second
const AnswerDeltaBytes = 4 << 10

// Include cursor/record bookkeeping in the retained replay memory budget.
const answerReplayOverhead = 256

var ErrAnswerUnavailable = errors.New("answer preview unavailable")
var ErrAnswerGap = errors.New("answer replay gap")

type answerRecord struct {
	before AnswerCursor
	after  AnswerCursor
	text   string
	at     time.Time
}

type answerEntry struct {
	cursor                         AnswerCursor
	request, input, state, invalid string
	text                           strings.Builder
	hash                           hash.Hash
	replay                         []answerRecord
	replayBytes                    int
	changed                        chan struct{}
	pruneTimer                     *time.Timer
}

func (e *answerEntry) notify() { close(e.changed); e.changed = make(chan struct{}) }
func (e *answerEntry) event(kind string) AnswerEvent {
	return AnswerEvent{Kind: kind, RequestID: e.request, InputDigest: e.input, Epoch: e.cursor.Epoch, Generation: e.cursor.Generation, Revision: e.cursor.Revision}
}
func (e *answerEntry) clear(reason string) {
	e.text.Reset()
	e.hash = nil
	e.replay = nil
	e.replayBytes = 0
	e.invalid = reason
	if e.pruneTimer != nil {
		e.pruneTimer.Stop()
		e.pruneTimer = nil
	}
	e.notify()
}

type answerContextKey struct{}
type answerPublisher struct {
	append func(string)
	reset  func()
}

// PublishAnswer only accepts already-approved final answer content. It is
// separate from the capped legacy progress preview and never persists tokens.
func PublishAnswer(ctx context.Context, text string) {
	if ctx.Err() != nil {
		return
	}
	if p, ok := ctx.Value(answerContextKey{}).(answerPublisher); ok {
		p.append(text)
	}
}

// ResetAnswer fences a replacement final-answer generation. It does not create
// an execution, repeat a model call or change the durable request identity.
func ResetAnswer(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if p, ok := ctx.Value(answerContextKey{}).(answerPublisher); ok {
		p.reset()
	}
}

func (s *Service) newAnswerLocked(key string, f Fence) *answerEntry {
	e := &answerEntry{cursor: AnswerCursor{Epoch: s.progressEpoch, Generation: newUUID(), Revision: 1, ContentDigest: Digest(nil)}, request: f.RequestID, input: f.InputDigest, state: f.State, changed: make(chan struct{}), hash: sha256.New()}
	s.answers[key] = e
	return e
}

func (s *Service) resetAnswer(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.control.Fences[key]
	if !ok || s.closed || s.revokedClients[f.ClientID] || f.State != "running" || !f.Deadline.After(s.now()) {
		return
	}
	if old := s.answers[key]; old != nil {
		old.clear("reset")
	}
	s.newAnswerLocked(key, f)
}

func (s *Service) publishAnswer(key, text string) {
	if text == "" || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.control.Fences[key]
	if !ok || s.closed || s.revokedClients[f.ClientID] || f.State != "running" || !f.Deadline.After(s.now()) {
		return
	}
	e := s.answers[key]
	if e == nil || e.invalid != "" {
		return
	}
	if len(text) > ResultBytes-e.text.Len() {
		e.clear("preview_unavailable")
		return
	}
	for text != "" {
		n := min(len(text), AnswerDeltaBytes)
		for !utf8.ValidString(text[:n]) {
			n--
		}
		part := text[:n]
		text = text[n:]
		before := e.cursor
		e.text.WriteString(part)
		e.hash.Write([]byte(part))
		e.cursor.Offset += uint64(len(part))
		e.cursor.Revision++
		e.cursor.ContentDigest = hex.EncodeToString(e.hash.Sum(nil))
		// Clone only this small record: a substring must not retain a provider's
		// entire oversized delta after replay pruning.
		e.replay = append(e.replay, answerRecord{before: before, after: e.cursor, text: strings.Clone(part), at: time.Now()})
		e.replayBytes += len(part) + answerReplayOverhead
	}
	s.pruneAnswerReplayLocked(e)
	e.notify()
}

func (s *Service) pruneAnswerReplayLocked(e *answerEntry) {
	// Keep the process-monotonic clock: a wall-clock rollback cannot extend
	// retention of already generated answer records.
	now := time.Now()
	for len(e.replay) > 0 && (!e.replay[0].at.Add(AnswerReplayRetention).After(now) || e.replayBytes+e.text.Len() > ResultBytes) {
		e.replayBytes -= len(e.replay[0].text) + answerReplayOverhead
		e.replay[0] = answerRecord{}
		e.replay = e.replay[1:]
	}
	if e.pruneTimer != nil {
		e.pruneTimer.Stop()
		e.pruneTimer = nil
	}
	if len(e.replay) != 0 && !s.closed {
		delay := max(time.Millisecond, e.replay[0].at.Add(AnswerReplayRetention).Sub(now))
		e.pruneTimer = time.AfterFunc(delay, func() { s.mu.Lock(); defer s.mu.Unlock(); s.pruneAnswerReplayLocked(e) })
	}
}

// Called alongside progress synchronization under the service lock. Nothing in
// this projection is serialized into the durable execution control ledger.
func (s *Service) syncAnswersLocked() {
	for key, e := range s.answers {
		f := s.control.Fences[key]
		state, reason := f.State, ""
		if (state == "accepted" || state == "running") && !f.Deadline.After(s.now()) {
			state = "unknown"
		}
		if state == "completed" && (f.ExpiresAt == nil || !f.ExpiresAt.After(s.now())) {
			state = "delivery_expired"
		}
		if progressTerminal(state) && state != "completed" {
			reason = state
		}
		if s.closed {
			reason = "closed"
		}
		if s.revokedClients[f.ClientID] {
			reason = "revoked"
		}
		if e.state != state {
			e.state = state
			e.notify()
		}
		if reason != "" {
			e.clear(reason)
			delete(s.answers, key)
		}
	}
}

// AnswerSubscription owns observation only. Next is serial, canceling its
// context never invokes execution cancellation. A single source lock captures
// the baseline/replay cursor and live notification channel atomically.
type AnswerSubscription struct {
	s              *Service
	e              *answerEntry
	cursor         AnswerCursor
	snapshot       string
	snapshotID     string
	snapshotStage  int
	snapshotOffset int
	lastState      string
	ended          bool
}

func (s *Service) SubscribeAnswer(ctx context.Context, owner, client, installation, request, digest string, cursor *AnswerCursor) (*AnswerSubscription, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !UUID(request) || !answerDigest.MatchString(digest) || (cursor != nil && !cursor.Valid()) {
		return nil, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.revokedClients[client] {
		return nil, ErrExpired
	}
	if !UUID(installation) || s.control.Installations[keyFor(owner, client, "")] != installation {
		return nil, ErrConflict
	}
	key := keyFor(owner, client, request)
	f, ok := s.control.Fences[key]
	if !ok {
		return nil, ErrNotFound
	}
	if f.InstallationID != installation || f.InputDigest != digest {
		return nil, ErrConflict
	}
	s.syncAnswersLocked()
	e := s.answers[key]
	if e == nil || e.invalid != "" {
		return nil, ErrAnswerUnavailable
	}
	s.pruneAnswerReplayLocked(e)
	sub := &AnswerSubscription{s: s, e: e, cursor: e.cursor}
	if cursor != nil && cursor.Epoch == e.cursor.Epoch && cursor.Generation == e.cursor.Generation {
		if *cursor == e.cursor {
			return sub, nil
		}
		for _, record := range e.replay {
			if record.before == *cursor {
				sub.cursor = *cursor
				return sub, nil
			}
			if record.before.Revision == cursor.Revision {
				return nil, ErrConflict
			}
		}
		if cursor.Revision >= e.cursor.Revision {
			return nil, ErrConflict
		}
	}
	// A stale cursor is not trusted; a complete new snapshot replaces it only
	// after the receiver verifies the fixed baseline and full digest.
	sub.snapshot = strings.Clone(e.text.String())
	sub.snapshotID = newUUID()
	sub.snapshotStage = 1
	return sub, nil
}

func (sub *AnswerSubscription) Next(ctx context.Context) (AnswerEvent, error) {
	for {
		if err := ctx.Err(); err != nil {
			return AnswerEvent{}, err
		}
		sub.s.mu.Lock()
		sub.s.syncAnswersLocked()
		e := sub.e
		if sub.ended {
			sub.s.mu.Unlock()
			return AnswerEvent{}, io.EOF
		}
		if e.invalid != "" {
			event := e.event("invalidate")
			event.Reason = e.invalid
			sub.snapshot = ""
			sub.ended = true
			sub.s.mu.Unlock()
			return event, nil
		}
		if sub.snapshotStage != 0 {
			event := sub.nextSnapshotLocked()
			sub.s.mu.Unlock()
			return event, nil
		}
		sub.s.pruneAnswerReplayLocked(e)
		if sub.cursor != e.cursor {
			for _, record := range e.replay {
				if record.before == sub.cursor {
					event := e.event("delta")
					event.Revision = record.after.Revision
					event.FromOffset = record.before.Offset
					event.ToOffset = record.after.Offset
					event.Text = record.text
					event.ContentDigest = record.after.ContentDigest
					sub.cursor = record.after
					sub.s.mu.Unlock()
					return event, nil
				}
			}
			sub.ended = true
			sub.s.mu.Unlock()
			return AnswerEvent{}, ErrAnswerGap
		}
		if sub.lastState != e.state {
			event := e.event("state")
			event.State = e.state
			sub.lastState = e.state
			if progressTerminal(e.state) {
				sub.ended = true
			}
			sub.s.mu.Unlock()
			return event, nil
		}
		changed := e.changed
		sub.s.mu.Unlock()
		select {
		case <-ctx.Done():
			return AnswerEvent{}, ctx.Err()
		case <-changed:
		}
	}
}

func (sub *AnswerSubscription) nextSnapshotLocked() AnswerEvent {
	event := sub.e.event("snapshot_begin")
	event.Revision = sub.cursor.Revision
	event.SnapshotID = sub.snapshotID
	event.TotalBytes = uint64(len(sub.snapshot))
	event.ContentDigest = sub.cursor.ContentDigest
	switch sub.snapshotStage {
	case 1:
		sub.snapshotStage = 2
	case 2:
		if sub.snapshotOffset < len(sub.snapshot) {
			event.Kind = "snapshot_chunk"
			event.Offset = uint64(sub.snapshotOffset)
			end := snapshotChunkEnd(sub.snapshot, sub.snapshotOffset, event)
			event.Text = sub.snapshot[sub.snapshotOffset:end]
			sub.snapshotOffset = end
			return event
		}
		event.Kind = "snapshot_end"
		sub.snapshot = ""
		sub.snapshotStage = 0
	}
	return event
}

func snapshotChunkEnd(text string, start int, event AnswerEvent) int {
	// Leave 1 KiB for the generic application frame. Measure the encoded body
	// so ordinary text uses the budget without breaking escaped JSON or UTF-8.
	low, high := start, min(len(text), start+(63<<10))
	for low < high {
		mid := low + (high-low+1)/2
		end := mid
		for end > start && end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		event.Text = text[start:end]
		raw, err := json.Marshal(event)
		if event.Text == "" || (err == nil && len(raw) <= 63<<10) {
			low = mid
		} else {
			high = mid - 1
		}
	}
	for low > start && low < len(text) && !utf8.RuneStart(text[low]) {
		low--
	}
	return low
}
