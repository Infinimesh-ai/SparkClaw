package toolhub

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
)

const (
	documentOCRPromptContractVersion       = "ovisocr2_markdown_v1"
	documentOCROutputNormalizationVersion  = "ovisocr2_output_normalization_v1"
	documentOCRDefaultPreprocessingVersion = "document_image_prepare_v1"
	documentOCRCacheMaxEntries             = 128
	documentOCRCacheMaxBytes               = 32 << 20
)

type documentOCRCallMetadata struct {
	SessionID            string
	OwnerID              string
	RunID                string
	PageIndex            int
	ClassifierVersion    string
	PreprocessingVersion string
	SourceSHA256         string
}

type documentOCRExecution struct {
	SessionID string
	RunID     string
}

type documentOCRExecutionContextKey struct{}

func withDocumentOCRExecution(ctx context.Context, sessionID, runID string) context.Context {
	return context.WithValue(ctx, documentOCRExecutionContextKey{}, documentOCRExecution{SessionID: sessionID, RunID: runID})
}

func documentOCRExecutionFromContext(ctx context.Context) documentOCRExecution {
	execution, _ := ctx.Value(documentOCRExecutionContextKey{}).(documentOCRExecution)
	return execution
}

type documentOCRInvocation struct {
	Result         documentocr.Result
	Err            error
	Status         string
	ReasonCode     string
	CacheResult    string
	ModelCallID    string
	CacheRecordID  string
	PreparedSHA256 string
	DurationMS     int64
	QueueWaitMS    int64
}

type documentOCRCacheEntry struct {
	key            string
	recordID       string
	result         documentocr.Result
	modelCallID    string
	preparedSHA256 string
	size           int
	element        *list.Element
}

type documentOCRFlight struct {
	done       chan struct{}
	invocation documentOCRInvocation
	waiters    int
}

type documentOCRPageMetricKey struct {
	Status      string
	CacheResult string
}

type documentOCRCallMetricKey struct {
	Provider string
	Model    string
	Status   string
}

type documentOCRQueueMetricKey struct {
	Provider string
	Model    string
}

type documentOCRRuntime struct {
	cfg config.DocumentOCRAdapterConfig

	mu                 sync.Mutex
	readiness          documentocr.RuntimeReadiness
	cache              map[string]*documentOCRCacheEntry
	cacheOrder         *list.List
	cacheBytes         int
	flights            map[string]*documentOCRFlight
	pageMetrics        map[documentOCRPageMetricKey]uint64
	cacheMetrics       map[string]uint64
	durationSeconds    map[documentOCRCallMetricKey]float64
	queueWaitSeconds   map[documentOCRQueueMetricKey]float64
	pdfClassifications map[string]uint64
	pdfReadCoverage    map[string]uint64
}

func newDocumentOCRRuntime(cfg config.DocumentOCRAdapterConfig, adapter documentocr.Adapter, constructorErr error) *documentOCRRuntime {
	runtime := &documentOCRRuntime{
		cfg:                cfg,
		cache:              map[string]*documentOCRCacheEntry{},
		cacheOrder:         list.New(),
		flights:            map[string]*documentOCRFlight{},
		pageMetrics:        map[documentOCRPageMetricKey]uint64{},
		cacheMetrics:       map[string]uint64{},
		durationSeconds:    map[documentOCRCallMetricKey]float64{},
		queueWaitSeconds:   map[documentOCRQueueMetricKey]float64{},
		pdfClassifications: map[string]uint64{},
		pdfReadCoverage:    map[string]uint64{},
	}
	runtime.readiness = configuredDocumentOCRReadiness(cfg, adapter, constructorErr)
	return runtime
}

func configuredDocumentOCRReadiness(cfg config.DocumentOCRAdapterConfig, adapter documentocr.Adapter, constructorErr error) documentocr.RuntimeReadiness {
	status := documentocr.RuntimeReadiness{
		ConfiguredEnabled: cfg.Enabled,
		Provider:          strings.TrimSpace(cfg.Provider),
		Model:             strings.TrimSpace(cfg.Model),
	}
	switch {
	case !cfg.Enabled || status.Provider == "" || status.Provider == "disabled":
		status.RuntimeStatus = "disabled"
		status.ReasonCode = "configuration_disabled"
	case constructorErr != nil:
		status.RuntimeStatus = "degraded"
		status.ReasonCode = "constructor_failed"
	case adapter != nil && adapter.Enabled():
		status.AdapterReady = true
		status.RuntimeStatus = "ready"
	default:
		status.RuntimeStatus = "degraded"
		status.ReasonCode = "adapter_disabled"
	}
	return status
}

func (r *documentOCRRuntime) setAdapter(adapter documentocr.Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readiness = configuredDocumentOCRReadiness(r.cfg, adapter, nil)
	if adapter != nil && adapter.Enabled() {
		r.readiness.AdapterReady = true
		r.readiness.RuntimeStatus = "ready"
		r.readiness.ReasonCode = ""
	}
}

func (r *documentOCRRuntime) readinessSnapshot() documentocr.RuntimeReadiness {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readiness
}

func (h *ToolHub) parseDocumentOCR(ctx context.Context, input documentocr.Request, metadata documentOCRCallMetadata) documentOCRInvocation {
	if metadata.PreprocessingVersion == "" {
		metadata.PreprocessingVersion = documentOCRDefaultPreprocessingVersion
	}
	if h == nil || h.ocrRuntime == nil || h.ocr == nil || !h.ocr.Enabled() {
		return h.recordDocumentOCRBypass(metadata, "disabled", "ocr_adapter_disabled")
	}
	ownerID := strings.TrimSpace(metadata.OwnerID)
	if ownerID == "" {
		var err error
		ownerID, err = h.ownerIDForSession(ctx, metadata.SessionID)
		if err != nil {
			return documentOCRInvocation{Err: err, Status: "failed", ReasonCode: "session_store_failed"}
		}
	}
	logicalKey, preparedSHA := documentOCRLogicalKey(
		input.Content,
		h.ocrRuntime.cfg.Provider,
		h.ocrRuntime.cfg.Model,
		documentOCRPromptContractVersion,
		metadata.PreprocessingVersion,
		documentOCROutputNormalizationVersion,
	)
	flightKey := ownerID + "\x00" + logicalKey

	h.ocrRuntime.mu.Lock()
	if entry, ok := h.ocrRuntime.cache[flightKey]; ok {
		h.ocrRuntime.cacheOrder.MoveToFront(entry.element)
		invocation := documentOCRInvocation{
			Result: entry.result, Status: documentOCRResultStatus(entry.result, nil), ReasonCode: documentOCRReasonCode(entry.result, nil),
			CacheResult: "hit", ModelCallID: entry.modelCallID, CacheRecordID: entry.recordID, PreparedSHA256: entry.preparedSHA256,
		}
		h.ocrRuntime.recordInvocationLocked(invocation, false)
		h.ocrRuntime.mu.Unlock()
		h.addDocumentOCRAudit(metadata, invocation)
		return invocation
	}
	if flight, ok := h.ocrRuntime.flights[flightKey]; ok {
		flight.waiters++
		h.ocrRuntime.mu.Unlock()
		select {
		case <-ctx.Done():
			invocation := documentOCRInvocation{Err: ctx.Err(), Status: "cancelled", ReasonCode: "request_cancelled", CacheResult: "coalesced", PreparedSHA256: preparedSHA}
			h.ocrRuntime.recordInvocation(invocation, false)
			h.addDocumentOCRAudit(metadata, invocation)
			return invocation
		case <-flight.done:
			invocation := flight.invocation
			invocation.CacheResult = "coalesced"
			h.ocrRuntime.recordInvocation(invocation, false)
			h.addDocumentOCRAudit(metadata, invocation)
			return invocation
		}
	}
	flight := &documentOCRFlight{done: make(chan struct{})}
	h.ocrRuntime.flights[flightKey] = flight
	h.ocrRuntime.mu.Unlock()

	started := time.Now().UTC()
	modelCallID := app.NewID("mcall")
	result, callErr := h.ocr.Parse(ctx, input)
	completed := time.Now().UTC()
	status := documentOCRResultStatus(result, callErr)
	reasonCode := documentOCRReasonCode(result, callErr)
	model := strings.TrimSpace(result.Model)
	if model == "" {
		model = strings.TrimSpace(h.ocrRuntime.cfg.Model)
	}
	if model == "" {
		model = "unknown"
	}
	provider := strings.TrimSpace(h.ocrRuntime.cfg.Provider)
	if provider == "" || provider == "disabled" {
		provider = "document-ocr"
	}
	modelStatus := "completed"
	errorText := ""
	if callErr != nil {
		modelStatus = "failed"
		errorText = callErr.Error()
	}
	if h.store != nil {
		if _, err := h.store.SaveModelCall(ctx, app.ModelCall{
			ID: modelCallID, SessionID: metadata.SessionID, RunID: metadata.RunID,
			Lane: "ocr", Profile: provider, Model: model, Operation: "document_ocr", Status: modelStatus,
			LatencyMS: completed.Sub(started).Milliseconds(), Error: errorText, StartedAt: started, CompletedAt: &completed,
		}); err != nil {
			callErr = fmt.Errorf("persist OCR model call: %w", err)
			status = documentOCRResultStatus(result, callErr)
			reasonCode = documentOCRReasonCode(result, callErr)
		}
	}
	invocation := documentOCRInvocation{
		Result: result, Err: callErr, Status: status, ReasonCode: reasonCode, CacheResult: "miss", ModelCallID: modelCallID,
		PreparedSHA256: preparedSHA, DurationMS: completed.Sub(started).Milliseconds(), QueueWaitMS: result.QueueWaitMS,
	}

	h.ocrRuntime.mu.Lock()
	if callErr == nil {
		entry := h.ocrRuntime.addCacheEntryLocked(flightKey, result, modelCallID, preparedSHA)
		invocation.CacheRecordID = entry.recordID
	}
	flight.invocation = invocation
	delete(h.ocrRuntime.flights, flightKey)
	close(flight.done)
	h.ocrRuntime.updateLastCallLocked(status, reasonCode, completed)
	h.ocrRuntime.recordInvocationLocked(invocation, true)
	h.ocrRuntime.mu.Unlock()
	h.addDocumentOCRAudit(metadata, invocation)
	return invocation
}

func documentOCRLogicalKey(content []byte, provider, model, contractVersion, preprocessingVersion, normalizationVersion string) (string, string) {
	preparedDigest := sha256.Sum256(content)
	preparedSHA := hex.EncodeToString(preparedDigest[:])
	parts := []string{preparedSHA, provider, model, contractVersion, preprocessingVersion, normalizationVersion}
	logicalDigest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(logicalDigest[:]), preparedSHA
}

func documentOCRResultStatus(result documentocr.Result, err error) string {
	if err != nil {
		message := strings.ToLower(err.Error())
		switch {
		case strings.Contains(message, "busy"):
			return "busy"
		case strings.Contains(message, "timed out"), strings.Contains(message, "deadline"):
			return "timeout"
		case strings.Contains(message, "cancel"):
			return "cancelled"
		default:
			return "failed"
		}
	}
	if documentocr.IsTrivialMarkdown(result.Markdown) {
		return "no_text"
	}
	return "succeeded"
}

func documentOCRReasonCode(result documentocr.Result, err error) string {
	switch documentOCRResultStatus(result, err) {
	case "succeeded":
		return "ocr_usable_text"
	case "no_text":
		return "no_usable_text"
	case "busy":
		return "provider_busy"
	case "timeout":
		return "provider_timeout"
	case "cancelled":
		return "request_cancelled"
	default:
		return "provider_failed"
	}
}

func (r *documentOCRRuntime) addCacheEntryLocked(key string, result documentocr.Result, modelCallID, preparedSHA string) *documentOCRCacheEntry {
	entry := &documentOCRCacheEntry{
		key: key, recordID: app.NewID("ocrcache"), result: result, modelCallID: modelCallID, preparedSHA256: preparedSHA,
		size: len([]byte(result.Markdown)) + len(result.Model) + len(modelCallID) + len(preparedSHA) + 128,
	}
	entry.element = r.cacheOrder.PushFront(entry)
	r.cache[key] = entry
	r.cacheBytes += entry.size
	for len(r.cache) > documentOCRCacheMaxEntries || r.cacheBytes > documentOCRCacheMaxBytes {
		oldest := r.cacheOrder.Back()
		if oldest == nil {
			break
		}
		removed := oldest.Value.(*documentOCRCacheEntry)
		delete(r.cache, removed.key)
		r.cacheBytes -= removed.size
		r.cacheOrder.Remove(oldest)
	}
	return entry
}

func (r *documentOCRRuntime) updateLastCallLocked(status, reason string, at time.Time) {
	r.readiness.LastCallStatus = status
	r.readiness.LastCallReason = reason
	r.readiness.LastCallAt = at.Format(time.RFC3339Nano)
}

func (r *documentOCRRuntime) recordInvocation(invocation documentOCRInvocation, freshCall bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recordInvocationLocked(invocation, freshCall)
}

func (r *documentOCRRuntime) recordInvocationLocked(invocation documentOCRInvocation, freshCall bool) {
	r.pageMetrics[documentOCRPageMetricKey{Status: invocation.Status, CacheResult: invocation.CacheResult}]++
	r.cacheMetrics[invocation.CacheResult]++
	if freshCall {
		key := documentOCRCallMetricKey{Provider: metricValue(r.cfg.Provider, "document-ocr"), Model: metricValue(r.cfg.Model, "unknown"), Status: invocation.Status}
		r.durationSeconds[key] += float64(invocation.DurationMS) / 1000
		r.queueWaitSeconds[documentOCRQueueMetricKey{Provider: key.Provider, Model: key.Model}] += float64(invocation.QueueWaitMS) / 1000
	}
}

func (h *ToolHub) recordAdditionalDocumentOCRPages(invocation documentOCRInvocation, count int) {
	if h == nil || h.ocrRuntime == nil || count <= 0 {
		return
	}
	h.ocrRuntime.mu.Lock()
	h.ocrRuntime.pageMetrics[documentOCRPageMetricKey{Status: invocation.Status, CacheResult: invocation.CacheResult}] += uint64(count)
	h.ocrRuntime.mu.Unlock()
}

func (h *ToolHub) recordDocumentOCRBypass(metadata documentOCRCallMetadata, status, reasonCode string) documentOCRInvocation {
	invocation := documentOCRInvocation{Status: status, ReasonCode: reasonCode, CacheResult: "bypass", PreparedSHA256: metadata.SourceSHA256}
	if h != nil && h.ocrRuntime != nil {
		h.ocrRuntime.recordInvocation(invocation, false)
	}
	if h != nil {
		h.addDocumentOCRAudit(metadata, invocation)
	}
	return invocation
}

func (h *ToolHub) addDocumentOCRAudit(metadata documentOCRCallMetadata, invocation documentOCRInvocation) {
	if h == nil || h.store == nil {
		return
	}
	fields := map[string]any{
		"status": invocation.Status, "reason_code": invocation.ReasonCode, "cache_result": invocation.CacheResult,
		"duration_ms": invocation.DurationMS, "queue_wait_ms": invocation.QueueWaitMS,
	}
	for key, value := range map[string]string{
		"classifier_version": metadata.ClassifierVersion, "preprocessing_version": metadata.PreprocessingVersion,
		"model_call_id": invocation.ModelCallID, "cache_record_id": invocation.CacheRecordID,
		"source_sha256": metadata.SourceSHA256, "prepared_sha256": invocation.PreparedSHA256,
	} {
		if strings.TrimSpace(value) != "" {
			fields[key] = value
		}
	}
	if metadata.PageIndex > 0 {
		fields["page_index"] = metadata.PageIndex
	}
	h.store.AddAudit(app.AuditEvent{
		ID: app.NewID("audit"), Time: time.Now().UTC(), SessionID: metadata.SessionID, RunID: metadata.RunID,
		Actor: "toolhub", Type: "document.ocr.page", Summary: "Recorded bounded document OCR page outcome", Fields: fields,
	})
}

func (h *ToolHub) recordPDFReadMetrics(sessionID, runID, coverage string, pages []any, missing []any) {
	if h == nil || h.ocrRuntime == nil {
		return
	}
	coverage = metricValue(coverage, "unavailable")
	h.ocrRuntime.mu.Lock()
	h.ocrRuntime.pdfReadCoverage[coverage]++
	for _, raw := range pages {
		page, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		quality, _ := page["native_text_quality"].(map[string]any)
		classification := metricValue(stringArg(quality, "classification", ""), "unknown")
		h.ocrRuntime.pdfClassifications[classification]++
	}
	h.ocrRuntime.mu.Unlock()
	if h.store != nil {
		h.store.AddAudit(app.AuditEvent{
			ID: app.NewID("audit"), Time: time.Now().UTC(), SessionID: sessionID, RunID: runID, Actor: "toolhub",
			Type: "document.pdf.read.coverage", Summary: "Recorded PDF page-read coverage",
			Fields: map[string]any{"coverage": coverage, "page_count": len(pages), "missing_page_indexes": missing},
		})
	}
}

func (r *documentOCRRuntime) prometheusLines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := []string{
		"# HELP sparkclaw_document_ocr_pages_total Document OCR page outcomes by bounded status and cache result.",
		"# TYPE sparkclaw_document_ocr_pages_total counter",
	}
	pageKeys := make([]documentOCRPageMetricKey, 0, len(r.pageMetrics))
	for key := range r.pageMetrics {
		pageKeys = append(pageKeys, key)
	}
	slices.SortFunc(pageKeys, func(a, b documentOCRPageMetricKey) int {
		return strings.Compare(a.Status+"\x00"+a.CacheResult, b.Status+"\x00"+b.CacheResult)
	})
	for _, key := range pageKeys {
		lines = append(lines, fmt.Sprintf("sparkclaw_document_ocr_pages_total{status=%s,cache_result=%s} %d", prometheusLabel(key.Status), prometheusLabel(key.CacheResult), r.pageMetrics[key]))
	}
	lines = append(lines,
		"# HELP sparkclaw_document_ocr_duration_seconds Total fresh document OCR call duration in seconds.",
		"# TYPE sparkclaw_document_ocr_duration_seconds counter",
	)
	callKeys := make([]documentOCRCallMetricKey, 0, len(r.durationSeconds))
	for key := range r.durationSeconds {
		callKeys = append(callKeys, key)
	}
	slices.SortFunc(callKeys, func(a, b documentOCRCallMetricKey) int {
		return strings.Compare(a.Provider+"\x00"+a.Model+"\x00"+a.Status, b.Provider+"\x00"+b.Model+"\x00"+b.Status)
	})
	for _, key := range callKeys {
		labels := fmt.Sprintf("provider=%s,model=%s,status=%s", prometheusLabel(key.Provider), prometheusLabel(key.Model), prometheusLabel(key.Status))
		lines = append(lines, fmt.Sprintf("sparkclaw_document_ocr_duration_seconds{%s} %s", labels, strconv.FormatFloat(r.durationSeconds[key], 'f', 6, 64)))
	}
	lines = append(lines,
		"# HELP sparkclaw_document_ocr_queue_wait_seconds Total fresh document OCR queue wait in seconds.",
		"# TYPE sparkclaw_document_ocr_queue_wait_seconds counter",
	)
	queueKeys := make([]documentOCRQueueMetricKey, 0, len(r.queueWaitSeconds))
	for key := range r.queueWaitSeconds {
		queueKeys = append(queueKeys, key)
	}
	slices.SortFunc(queueKeys, func(a, b documentOCRQueueMetricKey) int {
		return strings.Compare(a.Provider+"\x00"+a.Model, b.Provider+"\x00"+b.Model)
	})
	for _, key := range queueKeys {
		labels := fmt.Sprintf("provider=%s,model=%s", prometheusLabel(key.Provider), prometheusLabel(key.Model))
		lines = append(lines, fmt.Sprintf("sparkclaw_document_ocr_queue_wait_seconds{%s} %s", labels, strconv.FormatFloat(r.queueWaitSeconds[key], 'f', 6, 64)))
	}
	lines = append(lines,
		"# HELP sparkclaw_document_ocr_cache_total Document OCR cache lookups by result.",
		"# TYPE sparkclaw_document_ocr_cache_total counter",
	)
	cacheKeys := sortedMetricKeys(r.cacheMetrics)
	for _, key := range cacheKeys {
		lines = append(lines, fmt.Sprintf("sparkclaw_document_ocr_cache_total{result=%s} %d", prometheusLabel(key), r.cacheMetrics[key]))
	}
	lines = append(lines,
		"# HELP sparkclaw_pdf_page_classifications_total PDF native-text page classifications.",
		"# TYPE sparkclaw_pdf_page_classifications_total counter",
	)
	for _, key := range sortedMetricKeys(r.pdfClassifications) {
		lines = append(lines, fmt.Sprintf("sparkclaw_pdf_page_classifications_total{classification=%s} %d", prometheusLabel(key), r.pdfClassifications[key]))
	}
	lines = append(lines,
		"# HELP sparkclaw_pdf_reads_total PDF reads by typed page coverage.",
		"# TYPE sparkclaw_pdf_reads_total counter",
	)
	for _, key := range sortedMetricKeys(r.pdfReadCoverage) {
		lines = append(lines, fmt.Sprintf("sparkclaw_pdf_reads_total{coverage=%s} %d", prometheusLabel(key), r.pdfReadCoverage[key]))
	}
	return lines
}

func sortedMetricKeys[V ~uint64 | ~float64](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func metricValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "unknown"
}

func prometheusLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}
