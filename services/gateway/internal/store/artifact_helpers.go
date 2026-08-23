package store

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
)

func ArchiveToolObservation(ctx context.Context, st ArtifactMetadataRepository, artifacts artifact.Store, call app.ToolCall, output any) string {
	if st == nil || artifacts == nil || call.ID == "" {
		return ""
	}
	record := map[string]any{
		"tool_call_id": call.ID,
		"tool":         call.Tool,
		"run_id":       call.RunID,
		"session_id":   call.SessionID,
		"status":       call.Status,
		"summary":      call.ObservationSummary,
		"output":       output,
		"archived_at":  time.Now().UTC(),
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return ""
	}
	object, err := artifacts.Put(ctx, filepath.Join("observations", call.RunID, call.ID+".json"), "application/json", raw)
	if err != nil {
		return ""
	}
	if _, err := st.SaveArtifactObject(ctx, app.ArtifactObject{
		ID:          app.NewID("obj"),
		Kind:        "tool_observation",
		RunID:       call.RunID,
		SessionID:   call.SessionID,
		Backend:     object.Backend,
		Bucket:      object.Bucket,
		Key:         object.Key,
		URI:         object.URI,
		Path:        object.Path,
		ContentType: object.ContentType,
		Bytes:       object.Bytes,
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		slog.Warn("tool observation artifact metadata unavailable", "tool_call_id", call.ID, "code", StoreErrorCodeOf(err))
		return ""
	}
	return object.URI
}
