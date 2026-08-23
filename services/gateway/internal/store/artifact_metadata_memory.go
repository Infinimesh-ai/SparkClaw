package store

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) SaveArtifactObject(ctx context.Context, object app.ArtifactObject) (app.ArtifactObject, error) {
	ctx, cancel := operationContext(ctx, OperationArtifactMetadataSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationArtifactMetadataSave, ctx); err != nil {
		return app.ArtifactObject{}, err
	}
	object = prepareArtifactObject(object, time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationArtifactMetadataSave, ctx); err != nil {
		return app.ArtifactObject{}, err
	}
	if existing, ok := s.artifactObjects[object.ID]; ok && existing.URI != object.URI {
		s.unindexArtifactObjectLocked(existing)
	}
	s.artifactObjects[object.ID] = object
	s.indexArtifactObjectLocked(object)
	s.appendAuditLocked("artifact.saved", object.SessionID, object.RunID, "artifact-store", object.URI, map[string]any{
		"kind":    object.Kind,
		"backend": object.Backend,
		"key":     object.Key,
		"bytes":   object.Bytes,
		"eval_id": object.EvalID,
	})
	s.appendEventLocked("artifact.saved", object.SessionID, object.RunID, object)
	return object, nil
}

func (s *MemoryStore) ListArtifactObjects(ctx context.Context, limit int) ([]app.ArtifactObject, error) {
	ctx, cancel := operationContext(ctx, OperationArtifactMetadataList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationArtifactMetadataList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationArtifactMetadataList, ctx); err != nil {
		return nil, err
	}
	out := []app.ArtifactObject{}
	for _, object := range s.artifactObjects {
		out = append(out, object)
	}
	slices.SortFunc(out, func(a, b app.ArtifactObject) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) FindArtifactObjectByURI(ctx context.Context, uri, sessionID, runID string) (app.ArtifactObject, bool, error) {
	ctx, cancel := operationContext(ctx, OperationArtifactMetadataFindByURI, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationArtifactMetadataFindByURI, ctx); err != nil {
		return app.ArtifactObject{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationArtifactMetadataFindByURI, ctx); err != nil {
		return app.ArtifactObject{}, false, err
	}
	var newest app.ArtifactObject
	found := false
	for id := range s.artifactObjectIDsByURI[uri] {
		object, ok := s.artifactObjects[id]
		if !ok || (sessionID != "" && object.SessionID != sessionID) || (runID != "" && object.RunID != runID) {
			continue
		}
		if !found || object.CreatedAt.After(newest.CreatedAt) || object.CreatedAt.Equal(newest.CreatedAt) && object.ID < newest.ID {
			newest = object
			found = true
		}
	}
	return newest, found, nil
}

func (s *MemoryStore) indexArtifactObjectLocked(object app.ArtifactObject) {
	ids := s.artifactObjectIDsByURI[object.URI]
	if ids == nil {
		ids = map[string]struct{}{}
		s.artifactObjectIDsByURI[object.URI] = ids
	}
	ids[object.ID] = struct{}{}
}

func (s *MemoryStore) unindexArtifactObjectLocked(object app.ArtifactObject) {
	ids := s.artifactObjectIDsByURI[object.URI]
	delete(ids, object.ID)
	if len(ids) == 0 {
		delete(s.artifactObjectIDsByURI, object.URI)
	}
}
