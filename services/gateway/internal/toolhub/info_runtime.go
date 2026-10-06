package toolhub

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationrun"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

const infoIntegrationID = "infinimesh-info"

type infoCall struct {
	generation uint64
	cancel     context.CancelCauseFunc
}

type infoRuntime struct {
	mu         sync.Mutex
	search     websearch.Adapter
	weather    WeatherInfoAdapter
	generation uint64
	updating   bool
	nextCallID uint64
	calls      map[uint64]infoCall
	runs       *integrationrun.Registry
}

func newInfoRuntime(search websearch.Adapter, weather WeatherInfoAdapter) *infoRuntime {
	return &infoRuntime{search: search, weather: weather, generation: 1, calls: map[uint64]infoCall{}}
}

func (h *ToolHub) WithIntegrationRuns(registry *integrationrun.Registry) *ToolHub {
	if h != nil && h.info != nil {
		h.info.mu.Lock()
		h.info.runs = registry
		h.info.mu.Unlock()
	}
	return h
}

// ReplaceInfoAdapters is the single publication point for an Info credential
// switch. Calls using the old generation are cancelled before the new clients
// become visible, and there is deliberately no runtime fallback.
func (h *ToolHub) ReplaceInfoAdapters(search websearch.Adapter, weather WeatherInfoAdapter) int {
	if h == nil || h.info == nil {
		return 0
	}
	cause := infoToolError(app.ToolErrorInfoCredentialsChanged, "Info credentials changed; the task was stopped")
	h.info.mu.Lock()
	h.info.updating = true
	oldGeneration := h.info.generation
	calls := make([]context.CancelCauseFunc, 0, len(h.info.calls))
	for _, call := range h.info.calls {
		if call.generation == oldGeneration {
			calls = append(calls, call.cancel)
		}
	}
	runs := h.info.runs
	h.info.mu.Unlock()

	cancelled := 0
	if runs != nil {
		cancelled += runs.CancelGeneration(infoIntegrationID, oldGeneration, cause)
	}
	for _, cancel := range calls {
		cancel(cause)
		cancelled++
	}

	h.info.mu.Lock()
	h.info.search = search
	h.info.weather = weather
	h.info.generation++
	h.info.updating = false
	h.info.mu.Unlock()
	return cancelled
}

func (h *ToolHub) InfoConfigured() bool {
	if h == nil || h.info == nil {
		return false
	}
	h.info.mu.Lock()
	defer h.info.mu.Unlock()
	return h.info.search != nil && h.info.weather != nil
}

type infoCallSnapshot struct {
	ctx        context.Context
	search     websearch.Adapter
	weather    WeatherInfoAdapter
	generation uint64
	finish     func()
}

func (h *ToolHub) beginInfoCall(ctx context.Context, runID string, needsSearch bool) (infoCallSnapshot, error) {
	if h == nil || h.info == nil {
		return infoCallSnapshot{}, infoToolError(app.ToolErrorInfoNotConfigured, "Info credentials are not configured")
	}
	h.info.mu.Lock()
	if h.info.updating {
		h.info.mu.Unlock()
		return infoCallSnapshot{}, infoToolError(app.ToolErrorInfoUpdating, "Info credentials are being updated")
	}
	if (needsSearch && h.info.search == nil) || (!needsSearch && h.info.weather == nil) {
		h.info.mu.Unlock()
		return infoCallSnapshot{}, infoToolError(app.ToolErrorInfoNotConfigured, "Info credentials are not configured")
	}
	generation := h.info.generation
	if h.info.runs != nil {
		if err := h.info.runs.Use(runID, infoIntegrationID, generation); err != nil {
			h.info.mu.Unlock()
			return infoCallSnapshot{}, infoToolError(app.ToolErrorInfoCredentialsChanged, "Info credentials changed; the task was stopped")
		}
	}
	callCtx, cancel := context.WithCancelCause(ctx)
	h.info.nextCallID++
	callID := h.info.nextCallID
	h.info.calls[callID] = infoCall{generation: generation, cancel: cancel}
	snapshot := infoCallSnapshot{
		ctx: callCtx, search: h.info.search, weather: h.info.weather, generation: generation,
		finish: func() {
			h.info.mu.Lock()
			delete(h.info.calls, callID)
			h.info.mu.Unlock()
			cancel(nil)
		},
	}
	h.info.mu.Unlock()
	return snapshot, nil
}

func mapInfoCallError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var coded *app.CodedToolError
	if cause := context.Cause(ctx); errors.As(cause, &coded) {
		return cause
	}
	var apiErr *infinimeshinfo.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden {
			return infoToolError(app.ToolErrorInfoAuthFailed, "Info credentials were rejected")
		}
		return infoToolError(app.ToolErrorInfoTemporarilyUnavailable, "Info is temporarily unavailable")
	}
	var transportErr *infinimeshinfo.TransportError
	if errors.As(err, &transportErr) {
		return infoToolError(app.ToolErrorInfoTemporarilyUnavailable, "Info is temporarily unavailable")
	}
	return err
}

func infoToolError(code app.ToolErrorCode, message string) error {
	return &app.CodedToolError{Code: code, Err: errors.New(message)}
}
