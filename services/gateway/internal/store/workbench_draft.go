package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const MaxWorkbenchDraftBytes = 1 << 20
const MaxWorkbenchDraftAttachments = 64

var ErrWorkbenchDraftConflict = errors.New("workbench draft revision changed")

func workbenchDraftKey(ownerID, sessionID string) string {
	encoded, _ := json.Marshal([]string{ownerID, sessionID})
	return string(encoded)
}

func validateWorkbenchDraftScope(ownerID, sessionID string) error {
	if ownerID == "" || ownerID != strings.TrimSpace(ownerID) || sessionID != strings.TrimSpace(sessionID) || len(ownerID) > 256 || len(sessionID) > 256 || strings.ContainsRune(ownerID+sessionID, 0) {
		return errors.New("invalid workbench draft scope")
	}
	return nil
}

func validateWorkbenchDraft(draft app.WorkbenchDraft) error {
	if !utf8.ValidString(draft.Content) || strings.ContainsRune(draft.Content, 0) || draft.Revision < 0 || draft.Revision >= 9007199254740991 || len(draft.Content) > MaxWorkbenchDraftBytes || len(draft.AttachmentIDs) > MaxWorkbenchDraftAttachments {
		return errors.New("workbench draft exceeds its content, attachment or revision limit")
	}
	for _, id := range draft.AttachmentIDs {
		if id == "" || !utf8.ValidString(id) || len(id) > 4096 || strings.ContainsRune(id, 0) {
			return errors.New("invalid draft attachment reference")
		}
	}
	return nil
}

func cloneWorkbenchDraft(draft app.WorkbenchDraft) app.WorkbenchDraft {
	draft.AttachmentIDs = append([]string{}, draft.AttachmentIDs...)
	return draft
}

func cloneWorkbenchDrafts(drafts map[string]app.WorkbenchDraft) map[string]app.WorkbenchDraft {
	out := make(map[string]app.WorkbenchDraft, len(drafts))
	for key, draft := range drafts {
		out[key] = cloneWorkbenchDraft(draft)
	}
	return out
}

func (s *MemoryStore) workbenchDraftSessionLocked(ownerID, sessionID string) bool {
	if sessionID == "" {
		return true
	}
	session, ok := s.sessions[sessionID]
	return ok && session.OwnerID == ownerID && session.Source != "mcp"
}

func (s *MemoryStore) GetWorkbenchDraft(ctx context.Context, ownerID, sessionID string) (app.WorkbenchDraft, error) {
	ctx, cancel := operationContext(ctx, OperationWorkbenchDraftGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationWorkbenchDraftGet, ctx); err != nil {
		return app.WorkbenchDraft{}, err
	}
	if err := validateWorkbenchDraftScope(ownerID, sessionID); err != nil {
		return app.WorkbenchDraft{}, storeError(ctx, OperationWorkbenchDraftGet, StoreErrorInvalid, err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationWorkbenchDraftGet, ctx); err != nil {
		return app.WorkbenchDraft{}, err
	}
	if !s.workbenchDraftSessionLocked(ownerID, sessionID) {
		return app.WorkbenchDraft{}, storeError(ctx, OperationWorkbenchDraftGet, StoreErrorNotFound, errors.New("draft session not found"))
	}
	return cloneWorkbenchDraft(s.workbenchDrafts[workbenchDraftKey(ownerID, sessionID)]), nil
}

func (s *MemoryStore) SaveWorkbenchDraft(ctx context.Context, ownerID, sessionID string, draft app.WorkbenchDraft) (app.WorkbenchDraft, error) {
	ctx, cancel := operationContext(ctx, OperationWorkbenchDraftSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationWorkbenchDraftSave, ctx); err != nil {
		return app.WorkbenchDraft{}, err
	}
	if err := errors.Join(validateWorkbenchDraftScope(ownerID, sessionID), validateWorkbenchDraft(draft)); err != nil {
		return app.WorkbenchDraft{}, storeError(ctx, OperationWorkbenchDraftSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationWorkbenchDraftSave, ctx); err != nil {
		return app.WorkbenchDraft{}, err
	}
	if !s.workbenchDraftSessionLocked(ownerID, sessionID) {
		return app.WorkbenchDraft{}, storeError(ctx, OperationWorkbenchDraftSave, StoreErrorNotFound, errors.New("draft session not found"))
	}
	key := workbenchDraftKey(ownerID, sessionID)
	current := s.workbenchDrafts[key]
	if current.Revision != draft.Revision {
		return app.WorkbenchDraft{}, storeError(ctx, OperationWorkbenchDraftSave, StoreErrorConflict, ErrWorkbenchDraftConflict)
	}
	draft.Revision++
	draft.UpdatedAt = nextRepositoryTime(time.Now().UTC(), current.UpdatedAt)
	s.workbenchDrafts[key] = cloneWorkbenchDraft(draft)
	return cloneWorkbenchDraft(draft), nil
}

func (s *FileStore) GetWorkbenchDraft(ctx context.Context, ownerID, sessionID string) (app.WorkbenchDraft, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationWorkbenchDraftGet, 1)
	if err != nil {
		return app.WorkbenchDraft{}, err
	}
	defer release()
	return s.inner.GetWorkbenchDraft(ctx, ownerID, sessionID)
}

func (s *FileStore) SaveWorkbenchDraft(ctx context.Context, ownerID, sessionID string, draft app.WorkbenchDraft) (app.WorkbenchDraft, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationWorkbenchDraftSave, fileAdmissionCapacity)
	if err != nil {
		return app.WorkbenchDraft{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationWorkbenchDraftSave, func(ctx context.Context) (app.WorkbenchDraft, error) {
		return s.inner.SaveWorkbenchDraft(ctx, ownerID, sessionID, draft)
	})
}
