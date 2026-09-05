package gateway

import (
	"context"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
)

type residentServiceStatus struct {
	Lane           string `json:"lane"`
	Backend        string `json:"backend"`
	Model          string `json:"model"`
	Readiness      string `json:"readiness"`
	LastCallStatus string `json:"last_call_status,omitempty"`
}

func (s *Server) residentServiceStatuses(ctx context.Context, speechStatus speech.Status) ([]residentServiceStatus, error) {
	calls, err := s.store.ListModelCalls(ctx, "", "")
	if err != nil {
		return nil, err
	}
	latest := latestResidentServiceCalls(calls)
	ocr := s.tools.DocumentOCRReadiness()
	services := []residentServiceStatus{
		modelResidentService(modelcapacity.LaneFast, s.cfg.Model.Fast, s.cfg.Model, latest),
		modelResidentService(modelcapacity.LaneEmbedding, s.cfg.Model.Embedding, s.cfg.Model, latest),
		modelResidentService(modelcapacity.LaneGuard, s.cfg.Model.Guard, s.cfg.Model, latest),
		{
			Lane: string(modelcapacity.LaneASR), Backend: firstNonEmpty(s.cfg.Speech.Backend, speechStatus.Backend),
			Model: firstNonEmpty(speechStatus.Model, s.cfg.Speech.Model), Readiness: firstNonEmpty(speechStatus.State, speech.StateUnavailable),
			LastCallStatus: latestCallStatus(latest, string(modelcapacity.LaneASR)),
		},
		{
			Lane: string(modelcapacity.LaneOCR), Backend: firstNonEmpty(ocr.Provider, s.cfg.Adapters.DocumentOCR.Provider),
			Model: firstNonEmpty(ocr.Model, s.cfg.Adapters.DocumentOCR.Model), Readiness: firstNonEmpty(ocr.RuntimeStatus, "unavailable"),
			LastCallStatus: latestCallStatus(latest, string(modelcapacity.LaneOCR)),
		},
	}
	return services, nil
}

func modelResidentService(lane modelcapacity.Lane, profile config.ModelProfile, cfg config.ModelConfig, latest map[string]app.ModelCall) residentServiceStatus {
	backend := "openai-http"
	readiness := "configured"
	if cfg.Mock {
		backend = "mock"
		readiness = speech.StateReady
	} else if strings.TrimSpace(profile.BaseURL) == "" || strings.TrimSpace(profile.Model) == "" {
		readiness = speech.StateUnavailable
	}
	return residentServiceStatus{
		Lane: string(lane), Backend: backend, Model: strings.TrimSpace(profile.Model), Readiness: readiness,
		LastCallStatus: latestCallStatus(latest, string(lane)),
	}
}

var residentServiceLanes = map[modelcapacity.Lane]bool{
	modelcapacity.LaneFast: true, modelcapacity.LaneDeep: true, modelcapacity.LaneEmbedding: true,
	modelcapacity.LaneGuard: true, modelcapacity.LaneASR: true, modelcapacity.LaneOCR: true,
}

func latestResidentServiceCalls(calls []app.ModelCall) map[string]app.ModelCall {
	latest := map[string]app.ModelCall{}
	for _, call := range calls {
		if !residentServiceLanes[modelcapacity.Lane(call.Lane)] {
			continue
		}
		previous, ok := latest[call.Lane]
		if !ok || call.StartedAt.After(previous.StartedAt) || (call.StartedAt.Equal(previous.StartedAt) && call.ID > previous.ID) {
			latest[call.Lane] = call
		}
	}
	return latest
}

func latestCallStatus(latest map[string]app.ModelCall, lane string) string {
	return strings.TrimSpace(latest[lane].Status)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
