package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/speech"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestReadyzProjectsFiveResidentServicesAndLatestCallStatus(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Speech.Enabled = true
	cfg.Speech.Backend = "openai-http"
	cfg.Speech.Model = "configured-asr"
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	now := time.Now().UTC()
	for _, call := range []app.ModelCall{
		{ID: "fast-old", Lane: "fast", Status: "failed", Error: "private old failure", StartedAt: now.Add(-2 * time.Minute)},
		{ID: "fast-new", Lane: "fast", Status: "completed", StartedAt: now.Add(-time.Minute)},
		{ID: "embedding-new", Lane: "embedding", Status: "failed", Error: "private embedding failure", StartedAt: now},
		{ID: "asr-new", Lane: "asr", Status: "completed", StartedAt: now},
		{ID: "ocr-new", Lane: "ocr", Status: "failed", Error: "private ocr failure", StartedAt: now},
		{ID: "deep-new", Lane: "deep", Status: "completed", StartedAt: now.Add(time.Minute)},
	} {
		storetest.SaveModelCall(st, call)
	}
	// The status projection is external, while the unrelated Agent fixture stays
	// deterministic and must not contact the configured model endpoints.
	cfg.Model.Mock = false
	transcriber := &fakeSpeechTranscriber{status: speech.Status{
		Enabled: true, Ready: true, State: speech.StateReady, Backend: "runtime-state", Model: "runtime-asr",
	}}
	server := New(cfg, st, tools, runtime, WithSpeechTranscriber(transcriber))
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	response, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("readyz returned %d: %s", response.StatusCode, raw)
	}
	if strings.Contains(string(raw), "private") {
		t.Fatalf("resident projection exposed model-call errors: %s", raw)
	}
	var payload struct {
		Services []residentServiceStatus `json:"resident_services"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	wantLanes := []string{"fast", "embedding", "guard", "asr", "ocr"}
	if len(payload.Services) != len(wantLanes) {
		t.Fatalf("resident services = %#v", payload.Services)
	}
	for index, lane := range wantLanes {
		if payload.Services[index].Lane != lane {
			t.Fatalf("resident service order = %#v", payload.Services)
		}
	}
	byLane := map[string]residentServiceStatus{}
	for _, service := range payload.Services {
		byLane[service.Lane] = service
	}
	if byLane["fast"].Backend != "openai-http" || byLane["fast"].Readiness != "configured" || byLane["fast"].LastCallStatus != "completed" {
		t.Fatalf("fast projection = %#v", byLane["fast"])
	}
	if byLane["embedding"].LastCallStatus != "failed" || byLane["guard"].LastCallStatus != "" {
		t.Fatalf("model projections = %#v", byLane)
	}
	if byLane["asr"].Backend != "openai-http" || byLane["asr"].Model != "runtime-asr" || byLane["asr"].Readiness != speech.StateReady || byLane["asr"].LastCallStatus != "completed" {
		t.Fatalf("ASR projection = %#v", byLane["asr"])
	}
	if byLane["ocr"].Readiness != "disabled" || byLane["ocr"].LastCallStatus != "failed" {
		t.Fatalf("OCR projection = %#v", byLane["ocr"])
	}
	if _, ok := byLane["deep"]; ok {
		t.Fatalf("logical Deep profile appeared as a sixth resident service: %#v", byLane)
	}
}
