package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type runtimeOCRAdapter struct {
	result  documentocr.Result
	err     error
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type documentOCRSessionFailureStore struct {
	Repository
	err error
}

func (s *documentOCRSessionFailureStore) GetSession(context.Context, string) (app.Session, bool, error) {
	return app.Session{}, false, s.err
}

func (*runtimeOCRAdapter) Enabled() bool { return true }

func (a *runtimeOCRAdapter) Parse(ctx context.Context, _ documentocr.Request) (documentocr.Result, error) {
	a.calls.Add(1)
	if a.started != nil {
		a.once.Do(func() { close(a.started) })
	}
	if a.release != nil {
		select {
		case <-a.release:
		case <-ctx.Done():
			return documentocr.Result{}, ctx.Err()
		}
	}
	return a.result, a.err
}

func (*runtimeOCRAdapter) Close() error { return nil }

func TestDocumentOCRCacheIsOwnerScopedAndPersistsOnlyFreshModelCalls(t *testing.T) {
	state := store.NewMemoryStore()
	ownerAFirst := storetest.MustCreateSessionWithScope(t, state, "owner A first", "owner-a", "", "webchat", false)
	ownerASecond := storetest.MustCreateSessionWithScope(t, state, "owner A second", "owner-a", "", "webchat", false)
	ownerB := storetest.MustCreateSessionWithScope(t, state, "owner B", "owner-b", "", "webchat", false)
	adapter := &runtimeOCRAdapter{result: documentocr.Result{Markdown: "sensitive OCR marker", Model: "ATH-MaaS/OvisOCR2", InferenceMS: 7}}
	hub := newDocumentOCRRuntimeTestHub(state, adapter)
	input := documentocr.Request{Content: []byte("prepared-page"), ContentType: "image/jpeg"}

	first := hub.parseDocumentOCR(context.Background(), input, documentOCRCallMetadata{SessionID: ownerAFirst.ID, RunID: "run-a1", PageIndex: 1, PreprocessingVersion: "pdf_page_render_v1"})
	hit := hub.parseDocumentOCR(context.Background(), input, documentOCRCallMetadata{SessionID: ownerASecond.ID, RunID: "run-a2", PageIndex: 1, PreprocessingVersion: "pdf_page_render_v1"})
	isolated := hub.parseDocumentOCR(context.Background(), input, documentOCRCallMetadata{SessionID: ownerB.ID, RunID: "run-b", PageIndex: 1, PreprocessingVersion: "pdf_page_render_v1"})

	if first.CacheResult != "miss" || hit.CacheResult != "hit" || isolated.CacheResult != "miss" {
		t.Fatalf("unexpected owner-scoped cache results: first=%#v hit=%#v isolated=%#v", first, hit, isolated)
	}
	if first.ModelCallID == "" || hit.ModelCallID != first.ModelCallID || hit.CacheRecordID != first.CacheRecordID || isolated.ModelCallID == first.ModelCallID {
		t.Fatalf("cache provenance did not retain the originating call: first=%#v hit=%#v isolated=%#v", first, hit, isolated)
	}
	if adapter.calls.Load() != 2 {
		t.Fatalf("owner isolation should require two fresh calls, got %d", adapter.calls.Load())
	}
	calls := testListModelCalls(state, "", "")
	if len(calls) != 2 {
		t.Fatalf("cache hit created a fake model call: %#v", calls)
	}
	for _, call := range calls {
		if call.Operation != "document_ocr" || call.Lane != "ocr" || call.Status != "completed" {
			t.Fatalf("fresh OCR call provenance is incomplete: %#v", call)
		}
	}
	auditJSON, err := json.Marshal(append(mustToolHubListAudit(t, state, ownerAFirst.ID), mustToolHubListAudit(t, state, ownerASecond.ID)...))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(auditJSON), "sensitive OCR marker") {
		t.Fatalf("OCR text leaked into audit fields: %s", auditJSON)
	}
	if !hasDocumentOCRAudit(mustToolHubListAudit(t, state, ownerASecond.ID), "hit", first.ModelCallID) {
		t.Fatalf("cache-hit audit omitted provenance: %#v", mustToolHubListAudit(t, state, ownerASecond.ID))
	}
	readiness := hub.DocumentOCRReadiness()
	if !readiness.ConfiguredEnabled || !readiness.AdapterReady || readiness.RuntimeStatus != "ready" || readiness.LastCallStatus != "succeeded" {
		t.Fatalf("runtime readiness did not retain configured and last-call state: %#v", readiness)
	}
	metrics := strings.Join(hub.DocumentMetrics(), "\n")
	for _, want := range []string{
		`sparkclaw_document_ocr_pages_total{status="succeeded",cache_result="hit"} 1`,
		`sparkclaw_document_ocr_pages_total{status="succeeded",cache_result="miss"} 2`,
		`sparkclaw_document_ocr_cache_total{result="hit"} 1`,
		`sparkclaw_document_ocr_cache_total{result="miss"} 2`,
	} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("OCR metrics missing %q:\n%s", want, metrics)
		}
	}
}

func TestDocumentOCRSessionStoreFailureStopsBeforeAdapterExecution(t *testing.T) {
	base := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, base, "OCR session failure")
	rawCause := errors.New("session backend unavailable")
	failing := &documentOCRSessionFailureStore{Repository: base, err: &store.StoreError{
		Code: store.StoreErrorUnavailable, Operation: store.OperationSessionGet, Err: rawCause,
	}}
	adapter := &runtimeOCRAdapter{result: documentocr.Result{Markdown: "must not execute"}}
	hub := newDocumentOCRRuntimeTestHub(failing, adapter)
	invocation := hub.parseDocumentOCR(t.Context(), documentocr.Request{Content: []byte("page"), ContentType: "image/jpeg"}, documentOCRCallMetadata{
		SessionID: session.ID, RunID: "run", PreprocessingVersion: "pdf_page_render_v1",
	})
	if invocation.ReasonCode != "session_store_failed" || !errors.Is(invocation.Err, rawCause) || adapter.calls.Load() != 0 {
		t.Fatalf("invocation=%#v adapter calls=%d", invocation, adapter.calls.Load())
	}
}

func TestDocumentOCRSingleflightCoalescesConcurrentOwnerMisses(t *testing.T) {
	state := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, state, "owner", "owner-a", "", "webchat", false)
	adapter := &runtimeOCRAdapter{
		result:  documentocr.Result{Markdown: "coalesced", Model: "ATH-MaaS/OvisOCR2"},
		started: make(chan struct{}), release: make(chan struct{}),
	}
	hub := newDocumentOCRRuntimeTestHub(state, adapter)
	input := documentocr.Request{Content: []byte("same-page"), ContentType: "image/jpeg"}
	metadata := documentOCRCallMetadata{SessionID: session.ID, RunID: "run", PreprocessingVersion: "pdf_page_render_v1"}
	results := make(chan documentOCRInvocation, 2)

	go func() { results <- hub.parseDocumentOCR(context.Background(), input, metadata) }()
	<-adapter.started
	go func() { results <- hub.parseDocumentOCR(context.Background(), input, metadata) }()
	waitForDocumentOCRWaiter(t, hub.ocrRuntime)
	close(adapter.release)
	first, second := <-results, <-results

	cacheResults := map[string]int{first.CacheResult: 1}
	cacheResults[second.CacheResult]++
	if cacheResults["miss"] != 1 || cacheResults["coalesced"] != 1 || adapter.calls.Load() != 1 {
		t.Fatalf("singleflight did not coalesce the miss: first=%#v second=%#v calls=%d", first, second, adapter.calls.Load())
	}
	if first.ModelCallID == "" || first.ModelCallID != second.ModelCallID || len(testListModelCalls(state, "", "")) != 1 {
		t.Fatalf("coalesced result did not share one real model call: first=%#v second=%#v", first, second)
	}
}

func TestDocumentOCRCacheValidationAndVersionInvalidation(t *testing.T) {
	t.Run("transient failures are not cached", func(t *testing.T) {
		state := store.NewMemoryStore()
		session := storetest.MustCreateSessionWithScope(t, state, "owner", "owner-a", "", "webchat", false)
		adapter := &runtimeOCRAdapter{err: errors.New("document OCR inference timed out")}
		hub := newDocumentOCRRuntimeTestHub(state, adapter)
		metadata := documentOCRCallMetadata{SessionID: session.ID, RunID: "run", PreprocessingVersion: "pdf_page_render_v1"}
		for range 2 {
			result := hub.parseDocumentOCR(context.Background(), documentocr.Request{Content: []byte("page"), ContentType: "image/jpeg"}, metadata)
			if result.CacheResult != "miss" || result.Status != "timeout" || result.ReasonCode != "provider_timeout" {
				t.Fatalf("transient failure classification changed: %#v", result)
			}
		}
		if adapter.calls.Load() != 2 || len(testListModelCalls(state, "", "")) != 2 {
			t.Fatalf("transient failure was cached or not recorded: calls=%d model_calls=%d", adapter.calls.Load(), len(testListModelCalls(state, "", "")))
		}
		if readiness := hub.DocumentOCRReadiness(); readiness.RuntimeStatus != "ready" || readiness.LastCallStatus != "timeout" {
			t.Fatalf("request failure rewrote configured runtime state: %#v", readiness)
		}
	})

	t.Run("busy responses are explicit and uncached", func(t *testing.T) {
		state := store.NewMemoryStore()
		session := storetest.MustCreateSessionWithScope(t, state, "owner", "owner-a", "", "webchat", false)
		adapter := &runtimeOCRAdapter{err: errors.New("document OCR service is busy")}
		hub := newDocumentOCRRuntimeTestHub(state, adapter)
		metadata := documentOCRCallMetadata{SessionID: session.ID, RunID: "run", PreprocessingVersion: "pdf_page_render_v1"}
		for range 2 {
			result := hub.parseDocumentOCR(context.Background(), documentocr.Request{Content: []byte("page"), ContentType: "image/jpeg"}, metadata)
			if result.Status != "busy" || result.ReasonCode != "provider_busy" || result.CacheResult != "miss" {
				t.Fatalf("busy OCR result lost its bounded status: %#v", result)
			}
		}
		if adapter.calls.Load() != 2 {
			t.Fatalf("busy response was cached: calls=%d", adapter.calls.Load())
		}
	})

	t.Run("validated no-text and preprocessing versions", func(t *testing.T) {
		state := store.NewMemoryStore()
		session := storetest.MustCreateSessionWithScope(t, state, "owner", "owner-a", "", "webchat", false)
		adapter := &runtimeOCRAdapter{result: documentocr.Result{Model: "ATH-MaaS/OvisOCR2"}}
		hub := newDocumentOCRRuntimeTestHub(state, adapter)
		input := documentocr.Request{Content: []byte("page"), ContentType: "image/jpeg"}
		v1 := documentOCRCallMetadata{SessionID: session.ID, RunID: "run", PreprocessingVersion: "render-v1"}
		v2 := documentOCRCallMetadata{SessionID: session.ID, RunID: "run", PreprocessingVersion: "render-v2"}
		first := hub.parseDocumentOCR(context.Background(), input, v1)
		hit := hub.parseDocumentOCR(context.Background(), input, v1)
		invalidated := hub.parseDocumentOCR(context.Background(), input, v2)
		if first.Status != "no_text" || first.CacheResult != "miss" || hit.Status != "no_text" || hit.CacheResult != "hit" || invalidated.CacheResult != "miss" || adapter.calls.Load() != 2 {
			t.Fatalf("validated no-text/version behavior changed: first=%#v hit=%#v invalidated=%#v calls=%d", first, hit, invalidated, adapter.calls.Load())
		}
	})

	base, _ := documentOCRLogicalKey([]byte("page"), "provider", "model", "contract-v1", "render-v1", "normalize-v1")
	variants := []struct {
		name string
		key  string
	}{
		{name: "model", key: logicalOCRKeyForTest("provider", "model-v2", "contract-v1", "render-v1", "normalize-v1")},
		{name: "contract", key: logicalOCRKeyForTest("provider", "model", "contract-v2", "render-v1", "normalize-v1")},
		{name: "preprocessing", key: logicalOCRKeyForTest("provider", "model", "contract-v1", "render-v2", "normalize-v1")},
		{name: "normalization", key: logicalOCRKeyForTest("provider", "model", "contract-v1", "render-v1", "normalize-v2")},
	}
	for _, variant := range variants {
		if variant.key == base {
			t.Fatalf("%s version did not invalidate the logical OCR key", variant.name)
		}
	}
}

func TestDocumentOCRCacheIsBounded(t *testing.T) {
	state := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, state, "owner", "owner-a", "", "webchat", false)
	adapter := &runtimeOCRAdapter{result: documentocr.Result{Markdown: "bounded", Model: "ATH-MaaS/OvisOCR2"}}
	hub := newDocumentOCRRuntimeTestHub(state, adapter)
	metadata := documentOCRCallMetadata{SessionID: session.ID, RunID: "run", PreprocessingVersion: "pdf_page_render_v1"}
	for index := 0; index < documentOCRCacheMaxEntries+2; index++ {
		hub.parseDocumentOCR(context.Background(), documentocr.Request{Content: []byte(fmt.Sprintf("page-%d", index)), ContentType: "image/jpeg"}, metadata)
	}
	hub.ocrRuntime.mu.Lock()
	entries, bytes := len(hub.ocrRuntime.cache), hub.ocrRuntime.cacheBytes
	hub.ocrRuntime.mu.Unlock()
	if entries > documentOCRCacheMaxEntries || bytes > documentOCRCacheMaxBytes {
		t.Fatalf("OCR cache exceeded its process bounds: entries=%d bytes=%d", entries, bytes)
	}
}

func newDocumentOCRRuntimeTestHub(state Repository, adapter documentocr.Adapter) *ToolHub {
	cfg := config.Default()
	cfg.Adapters.DocumentOCR.Enabled = true
	cfg.Adapters.DocumentOCR.Provider = "openai-http"
	cfg.Adapters.DocumentOCR.BaseURL = "https://ocr.example.test/v1"
	cfg.Adapters.DocumentOCR.AllowedHosts = []string{"ocr.example.test"}
	cfg.Adapters.DocumentOCR.Model = "ATH-MaaS/OvisOCR2"
	return New(cfg, state).WithDocumentOCRAdapter(adapter)
}

func hasDocumentOCRAudit(events []app.AuditEvent, cacheResult, modelCallID string) bool {
	for _, event := range events {
		if event.Type == "document.ocr.page" && event.Fields["cache_result"] == cacheResult && event.Fields["model_call_id"] == modelCallID {
			return true
		}
	}
	return false
}

func waitForDocumentOCRWaiter(t *testing.T, runtime *documentOCRRuntime) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runtime.mu.Lock()
		waiters := 0
		for _, flight := range runtime.flights {
			waiters += flight.waiters
		}
		runtime.mu.Unlock()
		if waiters > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("second OCR request did not join the in-flight owner-scoped call")
}

func logicalOCRKeyForTest(provider, model, contract, preprocessing, normalization string) string {
	key, _ := documentOCRLogicalKey([]byte("page"), provider, model, contract, preprocessing, normalization)
	return key
}
