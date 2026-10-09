package execution

import (
	"context"
	"encoding/json"
	"sort"
)

// Approval content exists only for the lifetime of the running computation.
// The durable fence contains only the bound ID/digest and decision receipt.
type approvalWait struct {
	row      PendingApproval
	decision string
	ready    chan string
}

func NewPendingApproval(id, tool, summary string, arguments map[string]any) (PendingApproval, error) {
	row := PendingApproval{ApprovalID: id, Tool: tool, Summary: summary, Arguments: arguments}
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
	checked, err := NewPendingApproval(row.ApprovalID, row.Tool, row.Summary, row.Arguments)
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

func (s *Service) DecideApproval(owner, client, request, id, digest, decision string) error {
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
	if s.closed || f.State != "running" || !f.Deadline.After(s.now()) {
		return ErrExpired
	}
	wait := s.approvals[key][id]
	if wait == nil || wait.row.Digest != digest {
		return ErrConflict
	}
	if wait.decision != "" {
		if wait.decision == decision {
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
