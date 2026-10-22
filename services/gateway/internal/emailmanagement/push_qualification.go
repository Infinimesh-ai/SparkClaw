package emailmanagement

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"slices"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

// A provider's notification identifiers become capture targets only after
// they named the list's own row, with the same reconstructible row shape, for
// Threshold consecutive natural arrivals. Until then, and again after any
// unmatched candidate, non-canonical match or failed direct capture, the
// mailbox is list-only. The gate is per mailbox binding and survives restarts
// in the owner's email workspace; identifiers never reach logs.
//
// Direct targets are ordinary Reader retry targets: the Reader prefers its own
// listed row, a target it did not list is fetched through the retained
// original endpoint, and both paths publish through the same manifest checks,
// Store identity and receipts as every other capture.
type PushDirectFetchOptions struct {
	Enabled   bool
	Threshold int
	// QualifiedPollInterval relaxes the periodic sweep while a provider is
	// qualified and direct fetch is on. Zero keeps the current cadence.
	QualifiedPollInterval time.Duration
}

const (
	pushQualificationSchema = 1
	maxPushDirectPending    = 16
	maxPushDirectPerRound   = 8
)

// pushTargetShape records how the listed row relates to the notification
// identifier, so a direct target equals the row the Reader would list.
type pushTargetShape struct {
	Folder        string `json:"folder"`
	SelectionIsID bool   `json:"selection_is_id"`
	ThreadIsID    bool   `json:"thread_is_id"`
}

// direct reports whether a target can be rebuilt from the identifier alone.
// Native locators (Gmail) are never in a notification; other folders, such as
// Spam, stay excluded by the discovery folder policy.
func (s pushTargetShape) direct() bool { return s.Folder == "inbox" && s.SelectionIsID }

type pushPendingTarget struct {
	ProviderMessageID string    `json:"provider_message_id"`
	ObservedAt        time.Time `json:"observed_at"`
}

// pushInflight freezes the direct targets chosen for one checkpoint, so a
// yielded or restarted round replays the same Reader request.
type pushInflight struct {
	CheckpointRevision int64     `json:"checkpoint_revision"`
	IntervalStart      time.Time `json:"interval_start"`
	IDs                []string  `json:"ids"`
}

type pushQualificationRecord struct {
	Provider          string              `json:"provider"`
	BindingGeneration int64               `json:"binding_generation"`
	Consecutive       int                 `json:"consecutive"`
	Shape             *pushTargetShape    `json:"shape,omitempty"`
	QualifiedAt       *time.Time          `json:"qualified_at,omitempty"`
	Epoch             int64               `json:"epoch"`
	Matched           map[string]int      `json:"matched,omitempty"`
	Unmatched         int                 `json:"unmatched"`
	NoIdentifier      int                 `json:"no_identifier"`
	DirectCaptured    int                 `json:"direct_captured"`
	DirectFailed      int                 `json:"direct_failed"`
	Degraded          int                 `json:"degraded"`
	LastOutcome       string              `json:"last_outcome,omitempty"`
	LastOutcomeAt     time.Time           `json:"last_outcome_at"`
	Pending           []pushPendingTarget `json:"pending,omitempty"`
	Inflight          *pushInflight       `json:"inflight,omitempty"`
}

type pushQualificationState struct {
	SchemaVersion int                                 `json:"schema_version"`
	Mailboxes     map[string]*pushQualificationRecord `json:"mailboxes"`
}

// PushTargetStatus is the queryable projection of one mailbox's gate. It
// carries counts and state only, never identifiers.
type PushTargetStatus struct {
	DirectFetchEnabled    bool           `json:"direct_fetch_enabled"`
	Qualified             bool           `json:"qualified"`
	Consecutive           int            `json:"consecutive"`
	Threshold             int            `json:"threshold"`
	DirectCapable         bool           `json:"direct_capable"`
	QualifiedAt           *time.Time     `json:"qualified_at,omitempty"`
	Matched               map[string]int `json:"matched,omitempty"`
	Unmatched             int            `json:"unmatched"`
	NoIdentifier          int            `json:"no_identifier"`
	DirectCaptured        int            `json:"direct_captured"`
	DirectFailed          int            `json:"direct_failed"`
	Degraded              int            `json:"degraded"`
	PendingTargets        int            `json:"pending_targets"`
	LastOutcome           string         `json:"last_outcome,omitempty"`
	LastOutcomeAt         *time.Time     `json:"last_outcome_at,omitempty"`
	PollIntervalSeconds   int64          `json:"poll_interval_seconds,omitempty"`
	RelaxedPollingApplies bool           `json:"relaxed_polling_applies"`
}

// A nil gate (a Service assembled without New) is permanently list-only.
type pushQualifications struct {
	mu      sync.Mutex
	root    string
	opts    PushDirectFetchOptions
	byOwner map[string]*pushQualificationState
}

func newPushQualifications(root string, opts PushDirectFetchOptions) *pushQualifications {
	if opts.Threshold < 1 {
		opts.Threshold = 3
	}
	return &pushQualifications{root: root, opts: opts, byOwner: map[string]*pushQualificationState{}}
}

func pushQualificationPath(owner string) string {
	return path.Join("email", ownerScope(owner), "push", "qualification.json")
}

func (q *pushQualifications) qualifiedRecord(r *pushQualificationRecord) bool {
	return r.Shape != nil && r.Shape.direct() && r.Consecutive >= q.opts.Threshold
}

// ownerState loads lazily. An unreadable state starts list-only (fail closed).
func (q *pushQualifications) ownerState(owner string) *pushQualificationState {
	if state, ok := q.byOwner[owner]; ok {
		return state
	}
	state := &pushQualificationState{SchemaVersion: pushQualificationSchema, Mailboxes: map[string]*pushQualificationRecord{}}
	if root, err := os.OpenRoot(q.root); err == nil {
		raw, readErr := root.ReadFile(pushQualificationPath(owner))
		root.Close()
		var loaded pushQualificationState
		switch {
		case errors.Is(readErr, fs.ErrNotExist):
		case readErr != nil || json.Unmarshal(raw, &loaded) != nil || loaded.SchemaVersion != pushQualificationSchema:
			slog.Warn("email push qualification state unreadable; starting list-only")
		default:
			for id, record := range loaded.Mailboxes {
				if record != nil {
					state.Mailboxes[id] = record
				}
			}
		}
	}
	q.byOwner[owner] = state
	return state
}

func (q *pushQualifications) record(owner string, box app.EmailMailbox) *pushQualificationRecord {
	state := q.ownerState(owner)
	r := state.Mailboxes[box.ID]
	if r == nil || r.Provider != box.Provider || r.BindingGeneration != box.BindingGeneration {
		r = &pushQualificationRecord{Provider: box.Provider, BindingGeneration: box.BindingGeneration}
		state.Mailboxes[box.ID] = r
	}
	return r
}

// save writes atomically. If the state cannot be persisted the owner falls
// back to list-only in memory, so direct fetch never runs on unsaved state.
func (q *pushQualifications) save(owner string) {
	state := q.ownerState(owner)
	err := func() error {
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		root, err := os.OpenRoot(q.root)
		if err != nil {
			return err
		}
		defer root.Close()
		target := pushQualificationPath(owner)
		if err := root.MkdirAll(path.Dir(target), 0o700); err != nil {
			return err
		}
		next := target + ".next"
		if err := root.WriteFile(next, raw, 0o600); err != nil {
			return err
		}
		return root.Rename(next, target)
	}()
	if err != nil {
		slog.Warn("email push qualification state not saved; list-only", "code", safeCode(err))
		for _, r := range state.Mailboxes {
			q.reset(r, "state_unsaved", time.Now().UTC())
		}
	}
}

func (q *pushQualifications) outcome(r *pushQualificationRecord, outcome string, now time.Time) {
	r.LastOutcome, r.LastOutcomeAt = outcome, now
}

func (q *pushQualifications) reset(r *pushQualificationRecord, outcome string, now time.Time) {
	if q.qualifiedRecord(r) {
		r.Degraded++
		r.Epoch++
		slog.Info("email push direct fetch degraded", "provider", r.Provider, "reason", outcome)
	}
	r.Consecutive, r.Shape, r.QualifiedAt, r.Pending, r.Inflight = 0, nil, nil, nil, nil
	q.outcome(r, outcome, now)
}

// observeNotification counts identifier-less notifications and, once the
// mailbox is qualified with direct fetch on, keeps the identifiers as pending
// direct targets.
func (q *pushQualifications) observeNotification(owner string, box app.EmailMailbox, reason string, targets []browsercontrol.MailObserverTarget, now time.Time) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	r := q.record(owner, box)
	if len(targets) == 0 {
		if pushLifecycleReasons[reason] {
			return // observer lifecycle wakes are not arrivals
		}
		r.NoIdentifier++
		q.outcome(r, "no_identifier", now)
		q.save(owner)
		return
	}
	if !q.opts.Enabled || !q.qualifiedRecord(r) {
		return
	}
	r.Pending = prunePushPending(r.Pending, now)
	for _, target := range targets {
		if target.ProviderMessageID == "" || slices.ContainsFunc(r.Pending, func(p pushPendingTarget) bool { return p.ProviderMessageID == target.ProviderMessageID }) {
			continue
		}
		r.Pending = append(r.Pending, pushPendingTarget{ProviderMessageID: target.ProviderMessageID, ObservedAt: now})
	}
	if len(r.Pending) > maxPushDirectPending {
		r.Pending = r.Pending[len(r.Pending)-maxPushDirectPending:]
	}
	q.save(owner)
}

var pushLifecycleReasons = map[string]bool{"registration": true, "document_replaced": true, "sequence_gap": true, "observer_degraded": true, "executor_restarted": true}

func prunePushPending(pending []pushPendingTarget, now time.Time) []pushPendingTarget {
	kept := pending[:0]
	for _, p := range pending {
		if now.Sub(p.ObservedAt) < pushCandidateTTL {
			kept = append(kept, p)
		}
	}
	return kept
}

// pushRoundDirect is what a committed round learned about its direct targets.
type pushRoundDirect struct {
	attempted []string
	captured  int
	failed    string // first failure reason; empty when every attempt held
}

// observeRound applies one committed round: expired candidates and
// non-canonical matches reset the gate, canonical matches with a stable
// direct shape advance it, and a failed direct capture degrades it.
func (q *pushQualifications) observeRound(owner string, box app.EmailMailbox, matches []pushMatch, expired []pushCandidate, direct pushRoundDirect, now time.Time) {
	if q == nil {
		return
	}
	if len(matches) == 0 && len(expired) == 0 && len(direct.attempted) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	r := q.record(owner, box)
	for range expired {
		r.Unmatched++
		q.reset(r, "unmatched", now)
	}
	if r.Matched == nil && len(matches) > 0 {
		r.Matched = map[string]int{}
	}
	consumed := map[string]bool{}
	for _, m := range matches {
		r.Matched[m.kind]++
		consumed[m.candidate.target.ProviderMessageID] = true
		if m.kind != "message_id" {
			q.reset(r, "non_canonical_match", now)
			continue
		}
		id := m.candidate.target.ProviderMessageID
		shape := pushTargetShape{Folder: m.member.Folder, SelectionIsID: m.member.ProviderSelectionID == id, ThreadIsID: m.member.ProviderThreadID == id}
		if !shape.direct() || m.member.ProviderNativeID != "" {
			q.reset(r, "not_direct_capable", now)
			continue
		}
		if r.Shape != nil && *r.Shape != shape {
			q.reset(r, "shape_changed", now)
		}
		was := q.qualifiedRecord(r)
		r.Shape = &shape
		r.Consecutive++
		q.outcome(r, "matched", now)
		if !was && q.qualifiedRecord(r) {
			at := now
			r.QualifiedAt = &at
			r.Epoch++
			slog.Info("email push direct fetch qualified", "provider", r.Provider, "consecutive", r.Consecutive)
		}
	}
	r.DirectCaptured += direct.captured
	if direct.failed != "" {
		r.DirectFailed++
		q.reset(r, direct.failed, now)
	}
	for _, id := range direct.attempted {
		consumed[id] = true
	}
	if len(consumed) > 0 {
		r.Pending = slices.DeleteFunc(r.Pending, func(p pushPendingTarget) bool { return consumed[p.ProviderMessageID] })
	}
	r.Inflight = nil
	q.save(owner)
}

// degrade returns a mailbox to list-only after a round that carried direct
// targets failed before it could be committed.
func (q *pushQualifications) degrade(owner string, box app.EmailMailbox, reason string, now time.Time) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	r := q.record(owner, box)
	r.DirectFailed++
	q.reset(r, reason, now)
	q.save(owner)
}

// directTargets returns the frozen direct targets for one checkpoint, or
// selects and persists them first. Nothing is returned unless direct fetch is
// on and the mailbox is qualified.
func (q *pushQualifications) directTargets(owner string, box app.EmailMailbox, checkpointRevision int64, intervalStart time.Time, now time.Time) ([]app.EmailCaptureTarget, []string) {
	if q == nil {
		return nil, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	r := q.record(owner, box)
	if !q.opts.Enabled || !q.qualifiedRecord(r) {
		return nil, nil
	}
	if r.Inflight == nil || r.Inflight.CheckpointRevision != checkpointRevision || !r.Inflight.IntervalStart.Equal(intervalStart) {
		r.Pending = prunePushPending(r.Pending, now)
		if len(r.Pending) == 0 {
			if r.Inflight != nil {
				r.Inflight = nil
				q.save(owner)
			}
			return nil, nil
		}
		ids := make([]string, 0, maxPushDirectPerRound)
		for _, p := range r.Pending {
			if len(ids) == maxPushDirectPerRound {
				break
			}
			ids = append(ids, p.ProviderMessageID)
		}
		r.Inflight = &pushInflight{CheckpointRevision: checkpointRevision, IntervalStart: intervalStart, IDs: ids}
		q.save(owner)
		if r.Inflight == nil {
			return nil, nil // not persisted; save reset the gate
		}
	}
	targets := make([]app.EmailCaptureTarget, 0, len(r.Inflight.IDs))
	for _, id := range r.Inflight.IDs {
		target := app.EmailCaptureTarget{AccountAddress: box.Address, ProviderMessageID: id, Folder: r.Shape.Folder}
		if r.Shape.SelectionIsID {
			target.ProviderSelectionID = id
		}
		if r.Shape.ThreadIsID {
			target.ProviderThreadID = id
		}
		targets = append(targets, target)
	}
	return targets, slices.Clone(r.Inflight.IDs)
}

// relaxedInterval reports the qualified poll interval and the gate epoch.
// It returns zero unless direct fetch is on, an interval is configured and the
// mailbox is qualified.
func (q *pushQualifications) relaxedInterval(owner string, box app.EmailMailbox) (time.Duration, int64) {
	if q == nil {
		return 0, 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	r := q.record(owner, box)
	if !q.opts.Enabled || q.opts.QualifiedPollInterval <= 0 || !q.qualifiedRecord(r) {
		return 0, r.Epoch
	}
	return q.opts.QualifiedPollInterval, r.Epoch
}

func (q *pushQualifications) status(owner string, box app.EmailMailbox) PushTargetStatus {
	q.mu.Lock()
	defer q.mu.Unlock()
	r := q.record(owner, box)
	out := PushTargetStatus{DirectFetchEnabled: q.opts.Enabled, Qualified: q.qualifiedRecord(r), Consecutive: r.Consecutive, Threshold: q.opts.Threshold,
		DirectCapable: r.Shape != nil && r.Shape.direct(), QualifiedAt: r.QualifiedAt, Unmatched: r.Unmatched, NoIdentifier: r.NoIdentifier,
		DirectCaptured: r.DirectCaptured, DirectFailed: r.DirectFailed, Degraded: r.Degraded, PendingTargets: len(r.Pending), LastOutcome: r.LastOutcome}
	if len(r.Matched) > 0 {
		out.Matched = map[string]int{}
		for kind, count := range r.Matched {
			out.Matched[kind] = count
		}
	}
	if !r.LastOutcomeAt.IsZero() {
		at := r.LastOutcomeAt
		out.LastOutcomeAt = &at
	}
	return out
}
