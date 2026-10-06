package execution

import (
	"context"
	"encoding/json"
	"sort"
)

// Approval content exists only for the lifetime of the running computation.
// The durable fence deliberately contains neither arguments nor decisions.
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
	if len(rows) >= 32 {
		s.mu.Unlock()
		return "", ErrCapacity
	}
	wait = &approvalWait{row: checked, ready: make(chan string, 1)}
	rows[row.ApprovalID] = wait
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case decision := <-wait.ready:
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
