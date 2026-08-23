package toolhub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (h *ToolHub) memorySearch(ctx context.Context, args map[string]any, sessionID string) (Result, error) {
	if _, err := h.applyMemoryRetention(ctx); err != nil {
		return Result{}, err
	}
	query := stringArg(args, "query", "")
	memories, err := h.store.SearchMemories(ctx, query)
	if err != nil {
		return Result{}, err
	}
	ownerID, err := h.ownerIDForSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	filtered := memories[:0]
	for _, memory := range memories {
		visible, err := h.memoryVisibleToOwner(ctx, memory, ownerID)
		if err != nil {
			return Result{}, err
		}
		if visible {
			filtered = append(filtered, memory)
		}
	}
	memories = filtered
	return Result{Output: map[string]any{"query": query, "results": memories, "count": len(memories)}}, nil
}

func (h *ToolHub) ownerIDForSession(ctx context.Context, sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" || h.store == nil {
		return app.DefaultOwnerID, nil
	}
	session, ok, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("resolve session owner: %w", err)
	}
	if ok && strings.TrimSpace(session.OwnerID) != "" {
		return strings.TrimSpace(session.OwnerID), nil
	}
	return app.DefaultOwnerID, nil
}

func (h *ToolHub) sessionVisibleToOwner(ctx context.Context, sessionID, ownerID string) (bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return ownerID == "" || ownerID == app.DefaultOwnerID, nil
	}
	session, ok, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("resolve visible session: %w", err)
	}
	if !ok {
		return ownerID == app.DefaultOwnerID, nil
	}
	sessionOwner := strings.TrimSpace(session.OwnerID)
	if sessionOwner == "" {
		sessionOwner = app.DefaultOwnerID
	}
	if strings.TrimSpace(ownerID) == "" {
		ownerID = app.DefaultOwnerID
	}
	return sessionOwner == ownerID, nil
}

func (h *ToolHub) memoryVisibleToOwner(ctx context.Context, memory app.Memory, ownerID string) (bool, error) {
	if strings.TrimSpace(memory.SourceID) == "" {
		return ownerID == "" || ownerID == app.DefaultOwnerID, nil
	}
	run, ok, err := h.store.GetRun(ctx, memory.SourceID)
	if err != nil {
		return false, fmt.Errorf("load memory source run: %w", err)
	}
	if !ok {
		return ownerID == "" || ownerID == app.DefaultOwnerID, nil
	}
	return h.sessionVisibleToOwner(ctx, run.SessionID, ownerID)
}

func (h *ToolHub) memoryWriteCandidate(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	content := stringArg(args, "content", "")
	if content == "" {
		return Result{}, errors.New("content cannot be empty")
	}
	if !h.cfg.Memory.AllowSensitiveMemory {
		if pattern, ok := h.memorySensitivePattern(content, stringArg(args, "sensitivity", "")); ok {
			return Result{}, fmt.Errorf("memory candidate appears sensitive (%s); sensitive memory is disabled", pattern)
		}
	}
	candidate, err := h.store.AddMemoryCandidate(ctx, app.MemoryCandidate{
		SessionID:   sessionID,
		RunID:       runID,
		Kind:        stringArg(args, "kind", "profile"),
		Content:     content,
		Sensitivity: stringArg(args, "sensitivity", "normal"),
		Reason:      stringArg(args, "reason", "User asked SparkClaw to remember this."),
		Status:      "pending",
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: candidate}, nil
}

func (h *ToolHub) memoryWriteSensitive(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	content := stringArg(args, "content", "")
	if content == "" {
		return Result{}, errors.New("content cannot be empty")
	}
	kind := stringArg(args, "kind", "profile")
	memoryCandidate, err := h.store.AddMemoryCandidate(ctx, app.MemoryCandidate{
		SessionID:   sessionID,
		RunID:       runID,
		Kind:        kind,
		Content:     content,
		Sensitivity: "sensitive",
		Reason:      stringArg(args, "reason", "Owner approved writing sensitive memory."),
		Status:      "pending",
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return Result{}, err
	}
	candidate, memory, err := h.store.ResolveMemoryCandidate(ctx, memoryCandidate.ID, "accepted")
	if err != nil {
		return Result{}, err
	}
	if memory == nil {
		return Result{}, errors.New("sensitive memory was not accepted")
	}
	out := map[string]any{
		"id":            memory.ID,
		"kind":          memory.Kind,
		"content":       memory.Content,
		"source_run_id": memory.SourceID,
		"created_at":    memory.CreatedAt.Format(time.RFC3339),
		"sensitivity":   candidate.Sensitivity,
	}
	return Result{Output: out}, nil
}

func (h *ToolHub) memorySensitivePattern(content, sensitivity string) (string, bool) {
	if strings.EqualFold(strings.TrimSpace(sensitivity), "sensitive") {
		return "sensitivity", true
	}
	lower := strings.ToLower(content)
	for _, pattern := range h.cfg.Memory.RedactPatterns {
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

func (h *ToolHub) applyMemoryRetention(ctx context.Context) ([]app.Memory, error) {
	if h.cfg.Memory.RetentionDays <= 0 {
		return []app.Memory{}, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -h.cfg.Memory.RetentionDays)
	return h.store.PruneMemories(ctx, cutoff)
}
