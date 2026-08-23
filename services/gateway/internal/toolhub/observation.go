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
	object, ok, err := h.store.FindArtifactObjectByURI(ctx, artifactURI, sessionID, "")
	if err != nil {
		return Result{}, errors.New("artifact metadata is unavailable")
	}
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
	if end < len(content) {
		window = trimTrailingPartialRune(window)
	}
	if idx, invalid := firstInvalidUTF8(window); invalid {
		return Result{}, binaryObservationError(start+idx, start+skipInvalidUTF8(window, idx))
	}
	if len(window) == 0 && start < len(content) {
		r, runeBytes := utf8.DecodeRune(content[start:])
		if r == utf8.RuneError && runeBytes <= 1 {
			return Result{}, binaryObservationError(start, start+1)
		}
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

// trimTrailingPartialRune drops a multi-byte rune that the window boundary
// cut short: the trailing lead byte plus its continuation bytes, at most
// utf8.UTFMax-1 bytes. Invalid bytes anywhere else are left in place for the
// validity check, so this never hides binary content.
func trimTrailingPartialRune(window []byte) []byte {
	for i := 1; i < utf8.UTFMax && i <= len(window); i++ {
		if !utf8.RuneStart(window[len(window)-i]) {
			continue
		}
		if !utf8.FullRune(window[len(window)-i:]) {
			return window[:len(window)-i]
		}
		break
	}
	return window
}

// firstInvalidUTF8 returns the index of the first byte that is not part of a
// valid UTF-8 encoding, scanning the window once.
func firstInvalidUTF8(window []byte) (int, bool) {
	for i := 0; i < len(window); {
		r, size := utf8.DecodeRune(window[i:])
		if r == utf8.RuneError && size <= 1 {
			return i, true
		}
		i += size
	}
	return 0, false
}

// skipInvalidUTF8 returns the window index just past the contiguous run of
// invalid bytes starting at idx, so the caller can resume at the next
// decodable position.
func skipInvalidUTF8(window []byte, idx int) int {
	for idx < len(window) {
		r, size := utf8.DecodeRune(window[idx:])
		if r != utf8.RuneError || size > 1 {
			break
		}
		idx++
	}
	return idx
}

func binaryObservationError(invalidOffset, nextOffset int) error {
	return &app.CodedToolError{
		Code: app.ToolErrorObservationBinaryContent,
		Err:  fmt.Errorf("artifact output contains binary (non-UTF-8) content at offset %d; retry with offset=%d (next_offset) to skip past it", invalidOffset, nextOffset),
	}
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
