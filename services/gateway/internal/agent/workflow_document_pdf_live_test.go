package agent

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestLiveOvisOCR2PDFReadCoverageTraceAndCache(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("SPARKCLAW_LIVE_OCR_BASE_URL"))
	if baseURL == "" {
		t.Skip("set SPARKCLAW_LIVE_OCR_BASE_URL to run the live OvisOCR2 PDF scenario")
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Hostname() == "" {
		t.Fatalf("invalid SPARKCLAW_LIVE_OCR_BASE_URL %q", baseURL)
	}
	model := strings.TrimSpace(os.Getenv("SPARKCLAW_LIVE_OCR_MODEL"))
	if model == "" {
		model = "sparkclaw-ocr"
	}

	root := t.TempDir()
	traceDir := filepath.Join(root, ".sparkclaw", "traces")
	cfg := agentTestConfig()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.TraceDir = traceDir
	cfg.Storage.ArtifactBackend = "filesystem"
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	cfg.Storage.ArtifactBucket = "live-ocr"
	cfg.Adapters.DocumentOCR.Enabled = true
	cfg.Adapters.DocumentOCR.Provider = "openai-http"
	cfg.Adapters.DocumentOCR.BaseURL = baseURL
	cfg.Adapters.DocumentOCR.AllowedHosts = []string{parsedURL.Hostname()}
	cfg.Adapters.DocumentOCR.Model = model
	cfg.Adapters.DocumentOCR.TimeoutSeconds = 180
	cfg.Adapters.DocumentOCR.MaxUploadBytes = 12 << 20
	cfg.Adapters.DocumentOCR.MaxOutputBytes = 1 << 20
	cfg.Adapters.DocumentOCR.MaxTokens = 16384
	cfg.Adapters.DocumentOCR.MaxConcurrency = 1
	cfg.Adapters.DocumentOCR.MaxPending = 0

	state := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, state, "live OvisOCR2 PDF", "live-ocr-owner", root, "test", false)
	hub := toolhub.New(cfg, state)
	t.Cleanup(func() { _ = hub.Close() })
	runtime := NewRuntime(state, hub, policy.New(cfg), modelrouter.New(cfg), trace.NewWriterFromConfig(cfg))

	readiness := hub.DocumentOCRReadiness()
	if !readiness.ConfiguredEnabled || !readiness.AdapterReady || readiness.RuntimeStatus != "ready" {
		t.Fatalf("live OCR adapter is not ready: %#v", readiness)
	}

	const fixtureName = "live-scanned.pdf"
	writeLiveScannedPDFFixture(t, filepath.Join(root, fixtureName))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	first, firstOutput, firstPage := invokeLivePDFRead(t, ctx, runtime, session.ID, fixtureName)
	if firstOutput["read_complete"] != true || firstOutput["coverage_status"] != "complete" {
		t.Fatalf("live OCR did not recover complete PDF coverage: %#v", firstOutput)
	}
	if firstPage["text_status"] != "ocr_succeeded" || firstPage["text_source"] != "ocr" ||
		firstPage["ocr_cache_result"] != "miss" || firstPage["ocr_model_call_id"] == "" ||
		firstPage["ocr_cache_record_id"] == "" || firstPage["ocr_preprocessing_version"] != "pdf_page_render_v1" ||
		firstPage["ocr_provenance_ref"] == "" {
		t.Fatalf("live OCR page provenance is incomplete: %#v", firstPage)
	}
	if !strings.Contains(digitsOnly(firstNonEmptyString(firstOutput["content"])), "4286") {
		t.Fatalf("live OCR did not recover the fixture total: %q", firstNonEmptyString(firstOutput["content"]))
	}

	modelCalls := state.ListModelCalls(session.ID, first.Call.RunID)
	if len(modelCalls) != 1 || modelCalls[0].Operation != "document_ocr" || modelCalls[0].Lane != "ocr" || modelCalls[0].Status != "completed" {
		t.Fatalf("fresh live OCR ModelCall is incomplete: %#v", modelCalls)
	}
	modelCallID := modelCalls[0].ID
	if modelCallID != firstPage["ocr_model_call_id"] {
		t.Fatalf("page provenance does not reference the fresh ModelCall: page=%#v call=%#v", firstPage, modelCalls[0])
	}
	assertLiveOCRAudit(t, state.ListAudit(session.ID), first.Call.RunID, "miss", modelCallID)
	assertLiveOCRTrace(t, runtime, state, traceDir, first.Call, 1, "miss", modelCallID)

	afterFirst := hub.DocumentOCRReadiness()
	if afterFirst.RuntimeStatus != "ready" || afterFirst.LastCallStatus != "succeeded" || afterFirst.LastCallAt == "" {
		t.Fatalf("live OCR readiness did not retain the successful call: %#v", afterFirst)
	}

	second, secondOutput, secondPage := invokeLivePDFRead(t, ctx, runtime, session.ID, fixtureName)
	if secondOutput["read_complete"] != true || secondPage["ocr_cache_result"] != "hit" ||
		secondPage["ocr_model_call_id"] != modelCallID || secondPage["ocr_cache_record_id"] != firstPage["ocr_cache_record_id"] {
		t.Fatalf("second PDF read did not reuse live OCR provenance: output=%#v page=%#v", secondOutput, secondPage)
	}
	if calls := state.ListModelCalls(session.ID, ""); len(calls) != 1 {
		t.Fatalf("cache hit created a duplicate live OCR ModelCall: %#v", calls)
	}
	assertLiveOCRAudit(t, state.ListAudit(session.ID), second.Call.RunID, "hit", modelCallID)
	assertLiveOCRTrace(t, runtime, state, traceDir, second.Call, 0, "hit", modelCallID)

	metrics := strings.Join(hub.DocumentMetrics(), "\n")
	for _, want := range []string{
		`sparkclaw_document_ocr_pages_total{status="succeeded",cache_result="hit"} 1`,
		`sparkclaw_document_ocr_pages_total{status="succeeded",cache_result="miss"} 1`,
		`sparkclaw_document_ocr_cache_total{result="hit"} 1`,
		`sparkclaw_document_ocr_cache_total{result="miss"} 1`,
		`sparkclaw_pdf_reads_total{coverage="complete"} 2`,
	} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("live OCR metrics missing %q:\n%s", want, metrics)
		}
	}

	t.Logf("live OCR verified: first_run=%s second_run=%s model_call=%s cache_record=%v", first.Call.RunID, second.Call.RunID, modelCallID, firstPage["ocr_cache_record_id"])
	t.Logf("readiness=%#v", afterFirst)
	t.Logf("metrics:\n%s", metrics)
}

func invokeLivePDFRead(t *testing.T, ctx context.Context, runtime Runtime, sessionID, path string) (ManualInvocation, map[string]any, map[string]any) {
	t.Helper()
	invocation, err := runtime.InvokeToolManually(ctx, "pdf.extract_text", map[string]any{"path": path}, sessionID)
	if err != nil {
		t.Fatalf("invoke live PDF read: %v", err)
	}
	output, ok := anyMap(invocation.Result)
	if !ok {
		t.Fatalf("live PDF read returned an invalid output: %#v", invocation.Result)
	}
	documentValue, ok := anyMap(output["document"])
	if !ok {
		t.Fatalf("live PDF read omitted the document representation: %#v", output)
	}
	pages := documentAnySliceFromAny(documentValue["pages"])
	if len(pages) != 1 {
		t.Fatalf("live PDF fixture returned %d pages: %#v", len(pages), documentValue)
	}
	page, ok := anyMap(pages[0])
	if !ok {
		t.Fatalf("live PDF page has an invalid shape: %#v", pages[0])
	}
	return invocation, output, page
}

func assertLiveOCRTrace(t *testing.T, runtime Runtime, state *store.MemoryStore, traceDir string, call app.ToolCall, wantModelCalls int, cacheResult, modelCallID string) {
	t.Helper()
	run, ok := state.GetRun(call.RunID)
	if !ok {
		t.Fatalf("manual live OCR run %s was not persisted", call.RunID)
	}
	runtime.writeTrace(context.Background(), run, modelrouter.ChatResult{}, []app.ToolCall{call}, nil, nil, nil)
	raw, err := os.ReadFile(filepath.Join(traceDir, run.ID+".json"))
	if err != nil {
		t.Fatalf("read live OCR trace: %v", err)
	}
	var decoded trace.RunTrace
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode live OCR trace: %v", err)
	}
	if len(decoded.ModelCalls) != wantModelCalls || decoded.Artifact == nil || decoded.Artifact.URI == "" {
		t.Fatalf("live OCR trace is incomplete: %#v", decoded)
	}
	assertLiveOCRAudit(t, decoded.Audit, run.ID, cacheResult, modelCallID)
	if wantModelCalls == 1 && (decoded.ModelCalls[0].ID != modelCallID || decoded.ModelCalls[0].Operation != "document_ocr") {
		t.Fatalf("live OCR trace lost ModelCall provenance: %#v", decoded.ModelCalls)
	}
}

func assertLiveOCRAudit(t *testing.T, events []app.AuditEvent, runID, cacheResult, modelCallID string) {
	t.Helper()
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "4286") {
		t.Fatalf("live OCR text leaked into audit metadata: %s", raw)
	}
	for _, event := range events {
		if event.RunID == runID && event.Type == "document.ocr.page" && event.Fields["cache_result"] == cacheResult && event.Fields["model_call_id"] == modelCallID {
			return
		}
	}
	t.Fatalf("live OCR audit missing run=%s cache=%s model_call=%s: %#v", runID, cacheResult, modelCallID, events)
}

func writeLiveScannedPDFFixture(t *testing.T, path string) {
	t.Helper()
	const script = `
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont
import sys

path = Path(sys.argv[1])
image = Image.new("RGB", (1800, 1200), "white")
draw = ImageDraw.Draw(image)
try:
    title = ImageFont.truetype("DejaVuSans-Bold.ttf", 96)
    body = ImageFont.truetype("DejaVuSans.ttf", 76)
except OSError:
    title = ImageFont.load_default()
    body = ImageFont.load_default()
draw.rectangle((60, 60, 1740, 1140), outline="black", width=8)
draw.text((150, 220), "SPARKCLAW LIVE OCR", font=title, fill="black")
draw.text((150, 500), "INVOICE TOTAL 4286", font=body, fill="black")
draw.text((150, 720), "SCANNED PDF PAGE ONE", font=body, fill="black")
image.save(path, "PDF", resolution=180.0)
`
	command := exec.Command("python3", "-c", script, path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create live scanned PDF fixture: %v\n%s", err, output)
	}
}

func digitsOnly(value string) string {
	var out strings.Builder
	for _, character := range value {
		if character >= '0' && character <= '9' {
			out.WriteRune(character)
		}
	}
	return out.String()
}
