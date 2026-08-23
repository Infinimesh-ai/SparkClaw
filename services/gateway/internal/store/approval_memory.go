package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) SaveApproval(ctx context.Context, approval app.Approval) (app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalSave, ctx); err != nil {
		return app.Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationApprovalSave, ctx); err != nil {
		return app.Approval{}, err
	}
	var existing *app.Approval
	if current, ok := s.approvals[strings.TrimSpace(approval.ID)]; ok {
		existing = &current
	}
	approval, err := prepareApproval(approval, existing, time.Now())
	if err != nil {
		return app.Approval{}, storeError(ctx, OperationApprovalSave, StoreErrorInvalid, err)
	}
	if existing != nil {
		if approvalsEqual(*existing, approval) {
			return cloneApproval(approval)
		}
		return app.Approval{}, storeError(ctx, OperationApprovalSave, StoreErrorConflict, ErrApprovalConflict)
	}
	if approval.ExternalID != "" {
		for id, current := range s.approvals {
			if id != approval.ID && current.Source == approval.Source && current.ExternalID == approval.ExternalID {
				return app.Approval{}, storeError(ctx, OperationApprovalSave, StoreErrorConflict, ErrApprovalConflict)
			}
		}
	}
	s.approvals[approval.ID] = approval
	s.appendAuditLocked("approval."+string(approval.Status), approval.SessionID, approval.RunID, approvalActor(approval), approval.Summary, approvalLifecycleFields(approval))
	s.appendEventLocked("approval."+string(approval.Status), approval.SessionID, approval.RunID, approval)
	return cloneApproval(approval)
}

func (s *MemoryStore) GetApproval(ctx context.Context, id string) (app.Approval, bool, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalGet, ctx); err != nil {
		return app.Approval{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationApprovalGet, ctx); err != nil {
		return app.Approval{}, false, err
	}
	approval, ok := s.approvals[id]
	if !ok {
		return app.Approval{}, false, nil
	}
	approval, err := normalizePersistedApproval(approval)
	if err != nil {
		return app.Approval{}, false, storeError(ctx, OperationApprovalGet, StoreErrorCorrupt, err)
	}
	return approval, true, nil
}

func (s *MemoryStore) FindApprovalByExternalRef(ctx context.Context, source app.ApprovalSource, externalID string) (app.Approval, bool, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalFindExternalRef, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalFindExternalRef, ctx); err != nil {
		return app.Approval{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationApprovalFindExternalRef, ctx); err != nil {
		return app.Approval{}, false, err
	}
	var matched app.Approval
	found := false
	for _, approval := range s.approvals {
		if approval.Source == source && approval.ExternalID == externalID {
			if !found || approval.CreatedAt.After(matched.CreatedAt) || (approval.CreatedAt.Equal(matched.CreatedAt) && approval.ID < matched.ID) {
				matched = approval
				found = true
			}
		}
	}
	if !found {
		return app.Approval{}, false, nil
	}
	matched, err := normalizePersistedApproval(matched)
	if err != nil {
		return app.Approval{}, false, storeError(ctx, OperationApprovalFindExternalRef, StoreErrorCorrupt, err)
	}
	return matched, true, nil
}

func (s *MemoryStore) UpdatePendingApproval(ctx context.Context, command ApprovalUpdateCommand) (app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalUpdatePending, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalUpdatePending, ctx); err != nil {
		return app.Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationApprovalUpdatePending, ctx); err != nil {
		return app.Approval{}, err
	}
	current, ok := s.approvals[command.Candidate.ID]
	if !ok {
		return app.Approval{}, storeError(ctx, OperationApprovalUpdatePending, StoreErrorNotFound, ErrApprovalNotFound)
	}
	approval, err := preparePendingApprovalUpdate(command, current)
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrApprovalConflict) {
			code = StoreErrorConflict
		}
		return app.Approval{}, storeError(ctx, OperationApprovalUpdatePending, code, err)
	}
	s.approvals[approval.ID] = approval
	s.appendAuditLocked("approval.modified", approval.SessionID, approval.RunID, approvalUpdateActor(approval), approval.Summary, approvalUpdateFields(approval, command.Note))
	s.appendEventLocked("approval.pending", approval.SessionID, approval.RunID, approval)
	return cloneApproval(approval)
}

func (s *MemoryStore) ResolveApproval(ctx context.Context, id string, status app.ApprovalStatus, note string) (app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalResolve, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalResolve, ctx); err != nil {
		return app.Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationApprovalResolve, ctx); err != nil {
		return app.Approval{}, err
	}
	approval, ok := s.approvals[id]
	if !ok {
		return app.Approval{}, storeError(ctx, OperationApprovalResolve, StoreErrorNotFound, ErrApprovalNotFound)
	}
	approval, replay, err := prepareApprovalResolution(approval, status, note, time.Now())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrApprovalConflict) {
			code = StoreErrorConflict
		}
		return app.Approval{}, storeError(ctx, OperationApprovalResolve, code, err)
	}
	if replay {
		return cloneApproval(approval)
	}
	s.approvals[id] = approval
	s.appendAuditLocked("approval."+string(status), approval.SessionID, approval.RunID, approvalResolutionActor(status), approval.Summary, approvalLifecycleFields(approval))
	s.appendEventLocked("approval."+string(status), approval.SessionID, approval.RunID, approval)
	return cloneApproval(approval)
}

func (s *MemoryStore) ListApprovals(ctx context.Context, status app.ApprovalStatus) ([]app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationApprovalList, ctx); err != nil {
		return nil, err
	}
	out := []app.Approval{}
	for _, approval := range s.approvals {
		approval, err := normalizePersistedApproval(approval)
		if err != nil {
			return nil, storeError(ctx, OperationApprovalList, StoreErrorCorrupt, err)
		}
		if status == "" || approval.Status == status {
			out = append(out, approval)
		}
	}
	sortApprovals(out)
	return out, nil
}
