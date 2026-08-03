package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	defaultObservationReadBytes = 8000
	maxObservationReadBytes     = 32768
)

func (h *ToolHub) observationRead(ctx context.Context, args map[string]any, sessionID, _ string) (Result, error) {
	artifactURI := strings.TrimSpace(stringArg(args, "artifact_uri", ""))
	if artifactURI == "" {
		return Result{}, errors.New("artifact_uri is required")
	}
	if h.artifacts == nil {
		return Result{}, errors.New("artifact store is unavailable")
	}
	object, ok := currentSessionArtifact(h.store.ListArtifactObjects(0), sessionID, artifactURI)
	if !ok {
		return Result{}, errors.New("artifact is unavailable in the current session")
	}
	raw, err := h.artifacts.Get(ctx, object.Key)
	if err != nil {
		return Result{}, fmt.Errorf("read artifact: %w", err)
	}
	content, err := archivedObservationOutput(raw)
	if err != nil {
		return Result{}, err
	}
	offset := intArg(args, "offset", 0)
	maxBytes := intArg(args, "max_bytes", defaultObservationReadBytes)
	if maxBytes <= 0 {
		maxBytes = defaultObservationReadBytes
	}
	if maxBytes > maxObservationReadBytes {
		maxBytes = maxObservationReadBytes
	}
	if offset < 0 || offset > len(content) {
		return Result{}, errors.New("offset is outside the artifact output")
	}
	start := offset
	for start < len(content) && !utf8.RuneStart(content[start]) {
		start++
	}
	end := start + maxBytes
	if end > len(content) {
		end = len(content)
	}
	window := content[start:end]
	for len(window) > 0 && !utf8.Valid(window) {
		window = window[:len(window)-1]
	}
	if len(window) == 0 && start < len(content) {
		_, runeBytes := utf8.DecodeRune(content[start:])
		return Result{}, fmt.Errorf("max_bytes is too small for the next UTF-8 character (%d bytes required)", runeBytes)
	}
	nextOffset := start + len(window)
	return Result{Output: map[string]any{
		"artifact_uri": artifactURI,
		"offset":       start,
		"max_bytes":    maxBytes,
		"bytes":        len(window),
		"total_bytes":  len(content),
		"content":      string(window),
		"truncated":    nextOffset < len(content),
		"next_offset":  nextOffset,
		"untrusted":    true,
	}}, nil
}

func currentSessionArtifact(objects []app.ArtifactObject, sessionID, uri string) (app.ArtifactObject, bool) {
	for _, object := range objects {
		if object.URI == uri && object.SessionID == sessionID {
			return object, true
		}
	}
	return app.ArtifactObject{}, false
}

func archivedObservationOutput(raw []byte) ([]byte, error) {
	var record struct {
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(raw, &record); err != nil || len(record.Output) == 0 {
		return nil, errors.New("artifact does not contain an archived tool output")
	}
	return record.Output, nil
}
