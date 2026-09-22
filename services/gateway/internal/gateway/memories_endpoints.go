package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	memories, err := s.store.SearchMemories(r.Context(), r.URL.Query().Get("query"))
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	visible, err := s.filterMemoriesForOwner(r.Context(), memories, principalForRequest(r).OwnerID)
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": visible})
}

func (s *Server) getMemoryExport(w http.ResponseWriter, r *http.Request) {
	export, err := s.buildMemoryExport(r.Context(), principalForRequest(r).OwnerID)
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, export)
}

func (s *Server) archiveMemoryExport(w http.ResponseWriter, r *http.Request) {
	export, err := s.buildMemoryExport(r.Context(), principalForRequest(r).OwnerID)
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	raw, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	now := time.Now().UTC()
	object, err := s.artifacts.Put(r.Context(), filepath.Join("memory-exports", now.Format("20060102T150405Z")+"-"+app.NewID("snapshot")+".json"), "application/json", raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	artifactObject := app.ArtifactObject{
		ID:          app.NewID("obj"),
		Kind:        "memory_export",
		Backend:     object.Backend,
		Bucket:      object.Bucket,
		Key:         object.Key,
		URI:         object.URI,
		Path:        object.Path,
		ContentType: object.ContentType,
		Bytes:       object.Bytes,
		CreatedAt:   now,
	}
	stored, err := s.store.SaveArtifactObject(r.Context(), artifactObject)
	if err != nil {
		writeArtifactMetadataStoreError(w, err)
		return
	}
	artifactObject = stored
	s.addAudit(r.Context(), app.AuditEvent{
		Type:    "memory.exported",
		Actor:   "owner",
		Summary: artifactObject.URI,
		Fields: map[string]any{
			"artifact_id":        artifactObject.ID,
			"memory_count":       export.Counts.Memories,
			"candidate_count":    export.Counts.MemoryCandidates,
			"episode_count":      export.Counts.Episodes,
			"pending_candidates": export.Counts.PendingCandidates,
		},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"export": export, "artifact": artifactObject})
}

func (s *Server) buildMemoryExport(ctx context.Context, ownerID string) (app.MemoryExport, error) {
	candidates, err := s.store.ListMemoryCandidates(ctx, "")
	if err != nil {
		return app.MemoryExport{}, err
	}
	visibleCandidates := make([]app.MemoryCandidate, 0, len(candidates))
	pending := 0
	for _, candidate := range candidates {
		visible, visibilityErr := s.sessionIDVisibleToOwner(ctx, candidate.SessionID, ownerID)
		if visibilityErr != nil {
			return app.MemoryExport{}, visibilityErr
		}
		if !visible {
			continue
		}
		visibleCandidates = append(visibleCandidates, candidate)
		if candidate.Status == "pending" {
			pending++
		}
	}
	memories, err := s.store.SearchMemories(ctx, "")
	if err != nil {
		return app.MemoryExport{}, err
	}
	memories, err = s.filterMemoriesForOwner(ctx, memories, ownerID)
	if err != nil {
		return app.MemoryExport{}, err
	}
	episodes, err := s.store.ListEpisodeSummaries(ctx, "")
	if err != nil {
		return app.MemoryExport{}, err
	}
	visibleEpisodes := make([]app.EpisodeSummary, 0, len(episodes))
	for _, episode := range episodes {
		visible, visibilityErr := s.sessionIDVisibleToOwner(ctx, episode.SessionID, ownerID)
		if visibilityErr != nil {
			return app.MemoryExport{}, visibilityErr
		}
		if visible {
			visibleEpisodes = append(visibleEpisodes, episode)
		}
	}
	ownerProfile, found, err := s.store.GetOwnerProfileByID(ctx, ownerID)
	if err != nil {
		return app.MemoryExport{}, err
	}
	if !found {
		ownerProfile = app.DefaultOwnerProfile()
		ownerProfile.ID = ownerID
	}
	return app.MemoryExport{
		GeneratedAt:      time.Now().UTC(),
		OwnerProfile:     ownerProfile,
		Memories:         memories,
		MemoryCandidates: visibleCandidates,
		Episodes:         visibleEpisodes,
		Counts: app.MemoryExportCounts{
			Memories:          len(memories),
			MemoryCandidates:  len(visibleCandidates),
			PendingCandidates: pending,
			Episodes:          len(visibleEpisodes),
		},
	}, nil
}

func (s *Server) updateMemory(w http.ResponseWriter, r *http.Request) {
	if !s.memoryIDVisibleToOwner(r.Context(), r.PathValue("id"), principalForRequest(r).OwnerID, w) {
		return
	}
	var req struct {
		Kind    string `json:"kind"`
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.Content = strings.TrimSpace(req.Content)
	if req.Kind == "" {
		writeError(w, http.StatusBadRequest, errors.New("memory kind is required"))
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, errors.New("memory content is required"))
		return
	}
	if !s.cfg.Memory.AllowSensitiveMemory {
		if pattern, ok := memorySensitivePattern(req.Content, s.cfg.Memory.RedactPatterns); ok {
			writeError(w, http.StatusBadRequest, fmt.Errorf("memory appears sensitive (%s); sensitive memory is disabled", pattern))
			return
		}
	}
	memory, err := s.store.UpdateMemory(r.Context(), r.PathValue("id"), req.Kind, req.Content)
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, memory)
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	if !s.memoryIDVisibleToOwner(r.Context(), r.PathValue("id"), principalForRequest(r).OwnerID, w) {
		return
	}
	memory, err := s.store.DeleteMemory(r.Context(), r.PathValue("id"))
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, memory)
}

func (s *Server) applyMemoryRetention(ctx context.Context) ([]app.Memory, error) {
	if s.cfg.Memory.RetentionDays <= 0 {
		return []app.Memory{}, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -s.cfg.Memory.RetentionDays)
	return s.store.PruneMemories(ctx, cutoff)
}

func memorySensitivePattern(content string, patterns []string) (string, bool) {
	lower := strings.ToLower(content)
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if strings.Contains(lower, pattern) {
			return pattern, true
		}
	}
	return "", false
}

func (s *Server) listMemoryCandidates(w http.ResponseWriter, r *http.Request) {
	ownerID := principalForRequest(r).OwnerID
	candidates := []app.MemoryCandidate{}
	stored, err := s.store.ListMemoryCandidates(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	for _, candidate := range stored {
		visible, err := s.sessionIDVisibleToOwner(r.Context(), candidate.SessionID, ownerID)
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		if visible {
			candidates = append(candidates, candidate)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"memory_candidates": candidates})
}

func (s *Server) acceptMemoryCandidate(w http.ResponseWriter, r *http.Request) {
	if !s.memoryCandidateVisibleToOwner(r.Context(), r.PathValue("id"), principalForRequest(r).OwnerID, w) {
		return
	}
	candidate, memory, err := s.store.ResolveMemoryCandidate(r.Context(), r.PathValue("id"), "accepted")
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidate": candidate, "memory": memory})
}

func (s *Server) rejectMemoryCandidate(w http.ResponseWriter, r *http.Request) {
	if !s.memoryCandidateVisibleToOwner(r.Context(), r.PathValue("id"), principalForRequest(r).OwnerID, w) {
		return
	}
	candidate, _, err := s.store.ResolveMemoryCandidate(r.Context(), r.PathValue("id"), "rejected")
	if err != nil {
		writeMemoryStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, candidate)
}

func (s *Server) filterMemoriesForOwner(ctx context.Context, memories []app.Memory, ownerID string) ([]app.Memory, error) {
	visible := make([]app.Memory, 0, len(memories))
	for _, memory := range memories {
		allowed, err := s.memoryVisibleToOwner(ctx, memory, ownerID)
		if err != nil {
			return nil, err
		}
		if allowed {
			visible = append(visible, memory)
		}
	}
	return visible, nil
}

func (s *Server) memoryVisibleToOwner(ctx context.Context, memory app.Memory, ownerID string) (bool, error) {
	if strings.TrimSpace(memory.SourceID) == "" {
		return strings.TrimSpace(ownerID) == app.DefaultOwnerID, nil
	}
	run, found, err := s.store.GetRun(ctx, memory.SourceID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return s.sessionIDVisibleToOwner(ctx, run.SessionID, ownerID)
}

func (s *Server) memoryIDVisibleToOwner(ctx context.Context, memoryID, ownerID string, w http.ResponseWriter) bool {
	memories, err := s.store.SearchMemories(ctx, "")
	if err != nil {
		writeMemoryStoreError(w, err)
		return false
	}
	for _, memory := range memories {
		if memory.ID != memoryID {
			continue
		}
		visible, visibilityErr := s.memoryVisibleToOwner(ctx, memory, ownerID)
		if visibilityErr != nil {
			writeMemoryStoreError(w, visibilityErr)
			return false
		}
		if !visible {
			writeError(w, http.StatusNotFound, errors.New("memory record not found"))
		}
		return visible
	}
	writeError(w, http.StatusNotFound, errors.New("memory record not found"))
	return false
}

func (s *Server) memoryCandidateVisibleToOwner(ctx context.Context, candidateID, ownerID string, w http.ResponseWriter) bool {
	candidates, err := s.store.ListMemoryCandidates(ctx, "")
	if err != nil {
		writeMemoryStoreError(w, err)
		return false
	}
	for _, candidate := range candidates {
		if candidate.ID != candidateID {
			continue
		}
		visible, visibilityErr := s.sessionIDVisibleToOwner(ctx, candidate.SessionID, ownerID)
		if visibilityErr != nil {
			writeMemoryStoreError(w, visibilityErr)
			return false
		}
		if !visible {
			writeError(w, http.StatusNotFound, errors.New("memory record not found"))
		}
		return visible
	}
	writeError(w, http.StatusNotFound, errors.New("memory record not found"))
	return false
}

func writeMemoryStoreError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("memory request is invalid"))
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("memory record not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("memory candidate was already resolved"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, errors.New("memory request was canceled"))
	case store.StoreErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, errors.New("memory operation timed out"))
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("memory service is unavailable"))
	}
}
