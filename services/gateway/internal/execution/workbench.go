package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// WorkbenchBinding is derived from authenticated host workspace access. It is
// not an installation and carries no conversation content or credentials.
type WorkbenchBinding struct {
	OwnerID                string    `json:"owner_id"`
	ActorID                string    `json:"actor_id"`
	ClientID               string    `json:"client_id,omitempty"`
	WorkspaceID            string    `json:"workspace_id"`
	SessionID              string    `json:"session_id"`
	RequestID              string    `json:"request_id"`
	InputDigest            string    `json:"input_digest"`
	SubmittedDraftRevision *int64    `json:"submitted_draft_revision,omitempty"`
	ContextDigest          string    `json:"context_digest"`
	ContextBefore          time.Time `json:"context_before"`
}

type WorkbenchStatus struct {
	InputMessageID         string    `json:"input_message_id"`
	SubmittedDraftRevision *int64    `json:"submitted_draft_revision,omitempty"`
	ContextDigest          string    `json:"context_digest"`
	ContextBefore          time.Time `json:"context_before"`
	DraftRevision          *int64    `json:"draft_revision,omitempty"`
	SchemaVersion          int       `json:"schema_version"`
	RequestID              string    `json:"request_id"`
	InputDigest            string    `json:"input_digest"`
	State                  string    `json:"state"`
	RunID                  string    `json:"run_id"`
	MessageID              string    `json:"message_id,omitempty"`
}

type workbenchFence struct {
	DraftRevision *int64 `json:"draft_revision,omitempty"`
	WorkbenchBinding
	State     string    `json:"state"`
	RunID     string    `json:"run_id"`
	MessageID string    `json:"message_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Deadline  time.Time `json:"deadline"`
}

func workbenchKey(b WorkbenchBinding) string {
	return "workbench\x00" + b.OwnerID + "\x00" + b.WorkspaceID + "\x00" + b.RequestID
}
func validWorkbenchBinding(b WorkbenchBinding) bool {
	return identityPattern.MatchString(b.OwnerID) && identityPattern.MatchString(b.ActorID) && (b.ClientID == "" || identityPattern.MatchString(b.ClientID)) && digestPattern.MatchString(b.WorkspaceID) && identityPattern.MatchString(b.SessionID) && UUID(b.RequestID) && digestPattern.MatchString(b.InputDigest)
}
func workbenchStatus(f workbenchFence) WorkbenchStatus {
	return WorkbenchStatus{InputMessageID: "m_" + Digest([]byte(workbenchKey(f.WorkbenchBinding) + "\x00input"))[:24], SchemaVersion: 1, RequestID: f.RequestID, InputDigest: f.InputDigest, State: f.State, RunID: f.RunID, MessageID: f.MessageID, DraftRevision: f.DraftRevision, SubmittedDraftRevision: f.SubmittedDraftRevision, ContextDigest: f.ContextDigest, ContextBefore: f.ContextBefore}
}
func sameWorkbenchAuthority(a, b WorkbenchBinding) bool {
	return sameWorkbenchScope(a, b) && a.ActorID == b.ActorID && a.ClientID == b.ClientID
}

func sameWorkbenchScope(a, b WorkbenchBinding) bool {
	return a.OwnerID == b.OwnerID && a.WorkspaceID == b.WorkspaceID && a.SessionID == b.SessionID
}

// WorkbenchLease represents one durable admission. Finish must be called even
// if the stream's accepted response is lost before its worker starts.
type WorkbenchLease struct {
	DraftRevision    *int64
	recordDraftClear func(int64) error
	Context          context.Context
	RunID            string
	InputMessageID   string
	once             sync.Once
	finish           func(string, string, string) error
	err              error
}

func (l *WorkbenchLease) Finish(state, messageID string) error {
	return l.FinishRun(state, l.RunID, messageID)
}

func (l *WorkbenchLease) FinishRun(state, runID, messageID string) error {
	l.once.Do(func() { l.err = l.finish(state, runID, messageID) })
	return l.err
}

func (l *WorkbenchLease) RecordDraftClear(revision int64) error {
	if err := l.recordDraftClear(revision); err != nil {
		return err
	}
	l.DraftRevision = &revision
	return nil
}

// BeginWorkbench shares the execution service's private atomic ledger, capacity,
// cancellation and process lock. Repeated IDs return status only, never a lease.
func (s *Service) BeginWorkbench(ctx context.Context, binding WorkbenchBinding) (*WorkbenchLease, WorkbenchStatus, error) {
	if !validWorkbenchBinding(binding) {
		return nil, WorkbenchStatus{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, WorkbenchStatus{}, ErrUnavailable
	}
	key := workbenchKey(binding)
	if previous, exists := s.control.WorkbenchFences[key]; exists {
		if !sameWorkbenchAuthority(previous.WorkbenchBinding, binding) || previous.InputDigest != binding.InputDigest {
			return nil, WorkbenchStatus{}, ErrConflict
		}
		return nil, workbenchStatus(previous), nil
	}
	if !digestPattern.MatchString(binding.ContextDigest) || binding.ContextBefore.IsZero() || binding.SubmittedDraftRevision != nil && *binding.SubmittedDraftRevision < 0 {
		return nil, WorkbenchStatus{}, ErrConflict
	}
	if len(s.control.Fences)+len(s.control.WorkbenchFences) >= MaxFences || len(s.active) >= 8 {
		return nil, WorkbenchStatus{}, ErrCapacity
	}
	now := s.now()
	f := workbenchFence{WorkbenchBinding: binding, State: "running", RunID: "run_" + Digest([]byte(key))[:24], CreatedAt: now, Deadline: now.Add(ExecutionBudget)}
	s.control.WorkbenchFences[key] = f
	if err := s.saveLocked(); err != nil {
		// A rename followed by failed fsync has an uncertain durable outcome. Keep
		// the in-memory fence too; this process must never replay it either.
		f.State = "unknown"
		s.control.WorkbenchFences[key] = f
		return nil, WorkbenchStatus{}, ErrUnavailable
	}
	return s.startWorkbenchLeaseLocked(ctx, key, f), workbenchStatus(f), nil
}

func (s *Service) startWorkbenchLeaseLocked(ctx context.Context, key string, f workbenchFence) *WorkbenchLease {
	executionCtx, cancel := context.WithDeadline(ctx, f.Deadline)
	s.active[key] = cancel
	s.wg.Add(1)
	lease := &WorkbenchLease{Context: executionCtx, RunID: f.RunID, InputMessageID: "m_" + Digest([]byte(key + "\x00input"))[:24]}
	lease.recordDraftClear = func(revision int64) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		f := s.control.WorkbenchFences[key]
		if revision < 1 || f.State != "running" {
			return ErrConflict
		}
		f.DraftRevision = &revision
		s.control.WorkbenchFences[key] = f
		if err := s.saveLocked(); err != nil {
			f.State = "unknown"
			s.control.WorkbenchFences[key] = f
			return ErrUnavailable
		}
		return nil
	}
	lease.finish = func(state, runID, messageID string) error {
		defer s.wg.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		if executionCtx.Err() != nil {
			state = "unknown"
		}
		switch state {
		case "completed", "approval_pending", "browser_login_blocked", "blocked", "failed", "delivery_failed", "unknown":
		default:
			state = "unknown"
		}
		if !identityPattern.MatchString(runID) || messageID != "" && !identityPattern.MatchString(messageID) {
			state = "unknown"
		}
		current := s.control.WorkbenchFences[key]
		if identityPattern.MatchString(runID) {
			current.RunID = runID
		}
		current.State, current.MessageID = state, messageID
		s.control.WorkbenchFences[key] = current
		cancel()
		delete(s.active, key)
		if err := s.saveLocked(); err != nil {
			current.State = "unknown"
			s.control.WorkbenchFences[key] = current
			return ErrUnavailable
		}
		return nil
	}
	return lease
}

// LookupWorkbench never advances execution or delivery state.
func (s *Service) LookupWorkbench(binding WorkbenchBinding) (WorkbenchStatus, error) {
	return s.lookupWorkbench(binding, false)
}

// LookupWorkbenchClaim enforces original caller binding for a repeated submit.
func (s *Service) LookupWorkbenchClaim(binding WorkbenchBinding) (WorkbenchStatus, error) {
	return s.lookupWorkbench(binding, true)
}

func (s *Service) lookupWorkbench(binding WorkbenchBinding, claim bool) (WorkbenchStatus, error) {
	if !UUID(binding.RequestID) {
		return WorkbenchStatus{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, exists := s.control.WorkbenchFences[workbenchKey(binding)]
	if !exists || !sameWorkbenchScope(f.WorkbenchBinding, binding) {
		return WorkbenchStatus{}, ErrNotFound
	}
	if claim && !sameWorkbenchAuthority(f.WorkbenchBinding, binding) {
		return WorkbenchStatus{}, ErrConflict
	}
	status := workbenchStatus(f)
	if (status.State == "running" || status.State == "approval_pending" || status.State == "browser_login_blocked") && !f.Deadline.After(s.now()) {
		status.State = "unknown"
	}
	return status, nil
}

func (s *Service) validateWorkbenchFences() error {
	for key, f := range s.control.WorkbenchFences {
		if key != workbenchKey(f.WorkbenchBinding) || !validWorkbenchBinding(f.WorkbenchBinding) || !identityPattern.MatchString(f.RunID) || f.CreatedAt.IsZero() || f.Deadline.IsZero() || f.ContextBefore.IsZero() || !digestPattern.MatchString(f.ContextDigest) || f.SubmittedDraftRevision != nil && *f.SubmittedDraftRevision < 0 {
			return errors.New("invalid workbench execution fence")
		}
		switch f.State {
		case "running":
			f.State = "unknown"
			s.control.WorkbenchFences[key] = f
		case "completed", "approval_pending", "browser_login_blocked", "blocked", "failed", "delivery_failed", "unknown":
		default:
			return errors.New("invalid workbench execution state")
		}
		if f.DraftRevision != nil && *f.DraftRevision < 1 {
			return errors.New("invalid workbench draft revision")
		}
		if f.MessageID != "" && !identityPattern.MatchString(f.MessageID) {
			return errors.New("invalid workbench result reference")
		}
	}
	return nil
}

type WorkbenchReader interface {
	LookupWorkbench(WorkbenchBinding) (WorkbenchStatus, error)
	ListWorkbench(WorkbenchBinding, string, bool) ([]WorkbenchStatus, string, error)
}

// ReadWorkbench opens a read-only atomic snapshot before this process admits
// work. Interrupted execution is projected unknown without modifying its fence.
func ReadWorkbench(root string) (WorkbenchReader, error) {
	raw, err := readPrivate(filepath.Join(root, "control.json"), 64<<20)
	if errors.Is(err, os.ErrNotExist) {
		return &Service{closed: true, control: control{WorkbenchFences: map[string]workbenchFence{}}, now: func() time.Time { return time.Now().UTC() }}, nil
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	var ledger control
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&ledger) != nil || !json.Valid(raw) || ledger.Version != 2 || ledger.WorkbenchFences == nil || len(ledger.WorkbenchFences)+len(ledger.Fences) > MaxFences {
		return nil, ErrUnavailable
	}
	reader := &Service{closed: true, control: ledger, now: func() time.Time { return time.Now().UTC() }}
	if err := reader.validateWorkbenchFences(); err != nil {
		return nil, ErrUnavailable
	}
	return reader, nil
}

func (s *Service) ListWorkbench(binding WorkbenchBinding, cursor string, attention bool) ([]WorkbenchStatus, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fences := []workbenchFence{}
	for _, f := range s.control.WorkbenchFences {
		if sameWorkbenchScope(f.WorkbenchBinding, binding) && (!attention || f.State != "completed") {
			fences = append(fences, f)
		}
	}
	sort.Slice(fences, func(i, j int) bool {
		if fences[i].CreatedAt.Equal(fences[j].CreatedAt) {
			return fences[i].RequestID > fences[j].RequestID
		}
		return fences[i].CreatedAt.After(fences[j].CreatedAt)
	})
	start := 0
	if cursor != "" {
		found := false
		for i, f := range fences {
			if f.RequestID == cursor {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, "", ErrConflict
		}
	}
	end := min(start+100, len(fences))
	out := make([]WorkbenchStatus, 0, end-start)
	for _, f := range fences[start:end] {
		status := workbenchStatus(f)
		if (status.State == "running" || status.State == "approval_pending" || status.State == "browser_login_blocked") && !f.Deadline.After(s.now()) {
			status.State = "unknown"
		}
		out = append(out, status)
	}
	next := ""
	if end < len(fences) {
		next = fences[end-1].RequestID
	}
	return out, next, nil
}

func (s *Service) CancelWorkbench(binding WorkbenchBinding) (WorkbenchStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := workbenchKey(binding)
	f, found := s.control.WorkbenchFences[key]
	if !found || !sameWorkbenchScope(f.WorkbenchBinding, binding) {
		return WorkbenchStatus{}, ErrNotFound
	}
	if f.State == "running" || f.State == "approval_pending" || f.State == "browser_login_blocked" {
		f.State = "unknown"
		s.control.WorkbenchFences[key] = f
		if cancel := s.active[key]; cancel != nil {
			cancel()
		}
		if err := s.saveLocked(); err != nil {
			return WorkbenchStatus{}, ErrUnavailable
		}
	}
	return workbenchStatus(f), nil
}

// WorkbenchForRun resolves the original authority and immutable budget for a
// suspended host run. It does not grant authority to execute it.
func (s *Service) WorkbenchForRun(scope WorkbenchBinding, runID string) (WorkbenchBinding, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var selected workbenchFence
	found := false
	for _, f := range s.control.WorkbenchFences {
		if sameWorkbenchScope(f.WorkbenchBinding, scope) && f.RunID == runID && (!found || f.CreatedAt.Before(selected.CreatedAt)) {
			selected = f
			found = true
		}
	}
	return selected.WorkbenchBinding, found, nil
}

func (s *Service) ContinueWorkbench(ctx context.Context, binding WorkbenchBinding) (*WorkbenchLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrUnavailable
	}
	key := workbenchKey(binding)
	f, found := s.control.WorkbenchFences[key]
	if !found || !sameWorkbenchAuthority(f.WorkbenchBinding, binding) {
		return nil, ErrNotFound
	}
	if f.State != "approval_pending" && f.State != "browser_login_blocked" {
		return nil, ErrConflict
	}
	if !f.Deadline.After(s.now()) {
		return nil, ErrExpired
	}
	if len(s.active) >= 8 {
		return nil, ErrCapacity
	}
	f.State = "running"
	s.control.WorkbenchFences[key] = f
	if err := s.saveLocked(); err != nil {
		f.State = "unknown"
		s.control.WorkbenchFences[key] = f
		return nil, ErrUnavailable
	}
	return s.startWorkbenchLeaseLocked(ctx, key, f), nil
}

func (s *Service) RevokeWorkbenchClient(clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for key, f := range s.control.WorkbenchFences {
		if f.ClientID == clientID && (f.State == "running" || f.State == "approval_pending" || f.State == "browser_login_blocked") {
			f.State = "unknown"
			s.control.WorkbenchFences[key] = f
			changed = true
			if cancel := s.active[key]; cancel != nil {
				cancel()
			}
		}
	}
	if changed {
		if err := s.saveLocked(); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}
