package toolhub

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/docindex"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/documentocr"
)

const (
	// documentIndexOCRPagesPerCall bounds the pages one targeted read
	// recognizes, matching a complete read's scanned-page budget.
	documentIndexOCRPagesPerCall = documentFullImageLimit
	// documentIndexFullOCRMaxSourceBytes is the WebChat upload limit. A
	// scanned file within it may be recognized whole; a larger workspace file
	// only on the pages a request selects (design section 4.5).
	documentIndexFullOCRMaxSourceBytes = 25 << 20
	documentIndexOCRPreprocessing      = "pdf_page_render_v1"
)

// recognizeDocumentPages runs OCR on text-less pages of an indexed PDF,
// caching each batch as it finishes, and returns the rebuilt index with the
// pages that stay unrecognized. Without an enabled OCR adapter every page
// stays unrecognized and the index is unchanged.
func (h *ToolHub) recognizeDocumentPages(ctx context.Context, metadata document.Metadata, index *docindex.Index, pages []int) (*docindex.Index, []int, error) {
	if len(pages) == 0 || h.ocr == nil || !h.ocr.Enabled() {
		return index, pages, nil
	}
	target := h.documentIndexTarget(metadata)
	target.SourceSHA256 = index.Manifest.SourceSHA256
	execution := documentOCRExecutionFromContext(ctx)
	ownerID, err := h.ownerIDForSession(ctx, execution.SessionID)
	if err != nil {
		return index, pages, err
	}
	recognized := map[int]bool{}
	done := 0
	for start := 0; start < len(pages) && ctx.Err() == nil; start += documentIndexOCRPagesPerCall {
		batch := pages[start:min(len(pages), start+documentIndexOCRPagesPerCall)]
		reportToolProgress(ctx, fmt.Sprintf("正在识别扫描页（%d/%d）", done, len(pages)))
		texts, err := h.recognizePageBatch(ctx, metadata, index.Manifest.SourceSHA256, ownerID, batch)
		if err != nil {
			return index, pages, err
		}
		if err := h.documentIndex.CacheRecognizedPages(target, texts); err != nil {
			return index, pages, err
		}
		for page := range texts {
			recognized[page] = true
		}
		done += len(batch)
	}
	unrecognized := []int{}
	for _, page := range pages {
		if !recognized[page] {
			unrecognized = append(unrecognized, page)
		}
	}
	if len(recognized) == 0 {
		return index, unrecognized, nil
	}
	rebuilt, err := h.documentIndex.RecognizedPages(ctx, target, nil)
	if err != nil {
		return index, pages, err
	}
	return rebuilt, unrecognized, nil
}

// recognizePageBatch renders one batch and recognizes it with at most the
// configured OCR concurrency. Pages that fail or yield no usable text are
// left out of the result.
func (h *ToolHub) recognizePageBatch(ctx context.Context, metadata document.Metadata, sourceSHA256, ownerID string, batch []int) (map[int]string, error) {
	target := h.documentIndexTarget(metadata)
	target.SourceSHA256 = sourceSHA256
	var out map[string]any
	err := docindex.WithSourceSnapshot(ctx, target, func(snapshotCtx context.Context, snapshotPath string) error {
		var err error
		out, err = runPythonAdapter(snapshotCtx, pdfIndexAdapterScript, map[string]any{"operation": "render", "path": snapshotPath, "pages": batch})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("render scanned pages: %w", err)
	}
	execution := documentOCRExecutionFromContext(ctx)
	texts := map[int]string{}
	var mu sync.Mutex
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, max(1, h.cfg.Adapters.DocumentOCR.MaxConcurrency))
	for _, raw := range documentAnySlice(out["pages"]) {
		rendered, _ := documentAnyMap(raw)
		page := intArg(rendered, "page", 0)
		content, err := base64.StdEncoding.DecodeString(stringArg(rendered, "data_base64", ""))
		if page <= 0 || err != nil || len(content) == 0 {
			continue
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-semaphore }()
			prepared, err := prepareImageForModel(content, stringArg(rendered, "content_type", "image/jpeg"))
			if err != nil {
				return
			}
			invocation := h.parseDocumentOCR(ctx, documentocr.Request{Content: prepared.Content, ContentType: prepared.ContentType}, documentOCRCallMetadata{
				SessionID: execution.SessionID, OwnerID: ownerID, RunID: execution.RunID, PageIndex: page,
				PreprocessingVersion: documentIndexOCRPreprocessing, SourceSHA256: sourceSHA256,
			})
			if invocation.Err != nil || documentocr.IsTrivialMarkdown(invocation.Result.Markdown) {
				return
			}
			mu.Lock()
			texts[page] = invocation.Result.Markdown
			mu.Unlock()
		}()
	}
	wait.Wait()
	return texts, nil
}

func (h *ToolHub) documentIndexTarget(metadata document.Metadata) docindex.Target {
	cfg := h.cfg.Adapters.DocumentOCR
	raw, _ := json.Marshal([]any{cfg.Enabled, cfg.Provider, cfg.Model, cfg.BaseURL, documentOCRPromptContractVersion, documentIndexOCRPreprocessing, documentOCROutputNormalizationVersion})
	digest := sha256.Sum256(raw)
	return docindex.Target{WorkspaceRoot: h.cfg.Workspaces.DefaultRoot, RelPath: metadata.Relative, Format: metadata.Format, SourceSHA256: metadata.SHA256, EnrichmentVersion: hex.EncodeToString(digest[:])}
}
