package execution

import (
	"context"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"sort"
)

// Approval content exists only for the lifetime of the running computation.
// The durable fence contains only the bound ID/digest and decision receipt.
type approvalWait struct {
	row          PendingApproval
	decision     string
	inputs       app.ApprovalInputValues
	inputsDigest string
	ready        chan string
}

func NewPendingApproval(id, tool, summary string, arguments map[string]any) (PendingApproval, error) {
	return NewPendingApprovalWithForm(id, tool, summary, arguments, nil)
}

func NewPendingApprovalWithForm(id, tool, summary string, arguments map[string]any, form *app.ApprovalInputForm) (PendingApproval, error) {
	if form != nil {
		if err := app.ValidateApprovalInputForm(form); err != nil {
			return PendingApproval{}, ErrConflict
		}
	}
	row := PendingApproval{ApprovalID: id, Tool: tool, Summary: summary, Arguments: arguments, InputForm: form}
	if !identityPattern.MatchString(id) || !identityPattern.MatchString(tool) {
		return row, ErrConflict
	}
	if row.Arguments == nil {
		row.Arguments = map[string]any{}
	}
	raw, err := json.Marshal(row)
	if err != nil || len(raw) > (64<<10)-128 {
		return PendingApproval{}, ErrCapacity
	}
	row.Digest = Digest(raw)
	// Detach nested maps/slices so no caller can alter a published decision.
	raw, _ = json.Marshal(row)
	if err = json.Unmarshal(raw, &row); err != nil {
		return PendingApproval{}, ErrConflict
	}
	return row, nil
}

func (s *Service) AwaitApproval(ctx context.Context, e Envelope, row PendingApproval) (string, error) {
	checked, err := NewPendingApprovalWithForm(row.ApprovalID, row.Tool, row.Summary, row.Arguments, row.InputForm)
	if err != nil || checked.Digest != row.Digest {
		return "", ErrConflict
	}
	key := keyFor(e.OwnerID, e.ClientID, e.RequestID)
	s.mu.Lock()
	f, found := s.control.Fences[key]
	if s.closed || !found || f.State != "running" || f.InstallationID != e.InstallationID || !f.Deadline.After(s.now()) || ctx.Err() != nil {
		s.mu.Unlock()
		return "", ErrExpired
	}
	rows := s.approvals[key]
	if rows == nil {
		rows = map[string]*approvalWait{}
		s.approvals[key] = rows
	}
	wait := rows[row.ApprovalID]
	if wait != nil {
		if wait.row.Digest != row.Digest {
			s.mu.Unlock()
			return "", ErrConflict
		}
		// A duplicate waiter could execute a tool twice. Only the original
		// workflow is permitted to consume the decision.
		s.mu.Unlock()
		return "", ErrConflict
	}
	if len(f.Approvals) >= maxExecutionApprovals {
		s.mu.Unlock()
		return "", ErrCapacity
	}
	// Save the waiting phase before Lookup can publish the approval. A crash
	// after this point can invalidate it without retaining the continuation.
	f.Revision++
	f.Approvals = append(append([]ApprovalRecord(nil), f.Approvals...), ApprovalRecord{
		ApprovalID: checked.ApprovalID, Digest: checked.Digest, Revision: f.Revision, State: ApprovalPending,
	})
	s.control.Fences[key] = f
	if err = s.saveLocked(); err != nil {
		s.failApprovalPersistenceLocked(key, f)
		s.mu.Unlock()
		return "", ErrUnavailable
	}
	wait = &approvalWait{row: checked, ready: make(chan string, 1)}
	rows[row.ApprovalID] = wait
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case decision := <-wait.ready:
		s.mu.Lock()
		f := s.control.Fences[key]
		valid := !s.closed && f.State == "running" && f.Deadline.After(s.now()) && ctx.Err() == nil
		s.mu.Unlock()
		if !valid {
			return "", ErrExpired
		}
		return decision, nil
	}
}

// AwaitApprovalWithInputs keeps submitted values only in the live execution.
// The durable fence stores their digest, never user-entered content.
func (s *Service) AwaitApprovalWithInputs(ctx context.Context, e Envelope, row PendingApproval) (string, app.ApprovalInputValues, error) {
	decision, err := s.AwaitApproval(ctx, e, row)
	if err != nil {
		return "", nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	wait := s.approvals[keyFor(e.OwnerID, e.ClientID, e.RequestID)][row.ApprovalID]
	if wait == nil {
		return "", nil, ErrExpired
	}
	raw, err := json.Marshal(wait.inputs)
	if err != nil {
		return "", nil, err
	}
	var inputs app.ApprovalInputValues
	if err = json.Unmarshal(raw, &inputs); err != nil {
		return "", nil, err
	}
	return decision, inputs, nil
}

func (s *Service) DecideApproval(owner, client, request, id, digest, decision string) error {
	return s.decideApproval(owner, client, request, id, digest, decision, nil, app.ApprovalInputSubmission{})
}

// DecideApprovalAtRevision binds an ISCP decision to the exact published state.
// The precondition and durable decision share the execution cancellation lock.
func (s *Service) DecideApprovalAtRevision(owner, client, request, id, digest, decision string, revision uint64) error {
	return s.decideApproval(owner, client, request, id, digest, decision, &revision, app.ApprovalInputSubmission{})
}
func (s *Service) DecideApprovalWithInputs(owner, client, request, id, digest, decision string, input app.ApprovalInputSubmission) error {
	return s.decideApproval(owner, client, request, id, digest, decision, nil, input)
}
func (s *Service) DecideApprovalWithInputsAtRevision(owner, client, request, id, digest, decision string, revision uint64, input app.ApprovalInputSubmission) error {
	return s.decideApproval(owner, client, request, id, digest, decision, &revision, input)
}
func (s *Service) decideApproval(owner, client, request, id, digest, decision string, revision *uint64, input app.ApprovalInputSubmission) error {
	if !digestPattern.MatchString(digest) || (decision != "approve" && decision != "reject") {
		return ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := keyFor(owner, client, request)
	f, found := s.control.Fences[key]
	if !found {
		return ErrNotFound
	}
	if revision != nil && *revision != f.Revision {
		return ErrConflict
	}
	if s.closed || f.State != "running" || !f.Deadline.After(s.now()) {
		return ErrExpired
	}
	wait := s.approvals[key][id]
	if wait == nil || wait.row.Digest != digest {
		return ErrConflict
	}
	valuesDigest := ""
	var values app.ApprovalInputValues
	if wait.row.InputForm != nil && decision == "approve" {
		if input.SchemaVersion != app.ApprovalInputSchemaVersion {
			return ErrConflict
		}
		if err := app.ValidateApprovalInputs(wait.row.InputForm, input.Inputs); err != nil {
			return ErrConflict
		}
		raw, err := json.Marshal(input.Inputs)
		if err != nil {
			return ErrConflict
		}
		if json.Unmarshal(raw, &values) != nil {
			return ErrConflict
		}
		valuesDigest, err = app.ApprovalInputsDigest(values)
		if err != nil {
			return ErrConflict
		}
	} else if input.SchemaVersion != 0 || len(input.Inputs) > 0 {
		return ErrConflict
	}
	if wait.decision != "" {
		if wait.decision == decision && wait.inputsDigest == valuesDigest {
			return nil
		}
		return ErrConflict
	}
	// Serialize the durable decision with cancellation and service shutdown.
	// Never release a continuation on an uncertain persistence outcome.
	index := -1
	for i, row := range f.Approvals {
		if row.ApprovalID == id && row.Digest == digest && row.State == ApprovalPending {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrConflict
	}
	f.Revision++
	f.Approvals = append([]ApprovalRecord(nil), f.Approvals...)
	at := s.now()
	f.Approvals[index].State = ApprovalDecided
	f.Approvals[index].Decision = decision
	f.Approvals[index].InputsDigest = valuesDigest
	f.Approvals[index].DecidedAt = &at
	f.Approvals[index].Revision = f.Revision
	s.control.Fences[key] = f
	if err := s.saveLocked(); err != nil {
		f.Approvals[index].State = ApprovalDecisionUnknown
		f.Approvals[index].Revision = f.Revision + 1
		s.failApprovalPersistenceLocked(key, f)
		return ErrUnavailable
	}
	wait.decision = decision
	wait.inputs = values
	wait.inputsDigest = valuesDigest
	wait.ready <- decision
	return nil
}

func (s *Service) pendingLocked(key string) []PendingApproval {
	rows := []PendingApproval{}
	for _, wait := range s.approvals[key] {
		if wait.decision == "" {
			rows = append(rows, wait.row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ApprovalID < rows[j].ApprovalID })
	// Lookup never exposes internal mutable maps to its caller.
	raw, _ := json.Marshal(rows)
	_ = json.Unmarshal(raw, &rows)
	return rows
}

// A failed atomic replacement may already be visible on disk. Keep the fence
// and any chosen decision, stop the computation and reconcile as unknown;
// rolling back to an actionable pending row could authorize an uncertain retry.
func (s *Service) failApprovalPersistenceLocked(key string, f Fence) {
	f.State = "unknown"
	f.Revision++
	invalidateApprovals(&f)
	s.control.Fences[key] = f
	delete(s.approvals, key)
	if cancel := s.active[key]; cancel != nil {
		cancel()
	}
	_ = s.saveLocked()
}
