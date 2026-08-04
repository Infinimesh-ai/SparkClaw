# OCR Usage & Refactor Execution Plan

> Language: English | [简体中文](../zh-cn/docs/ocr-usage-refactor-plan.md)

Status: Planned 2026-08-04. Not yet implemented. The OvisOCR2 integration
this plan builds on exists only in the uncommitted working tree.

## Context

The project has integrated a document OCR model (`ATH-MaaS/OvisOCR2`,
served by vLLM as an OpenAI-compatible endpoint under the alias
`sparkclaw-ocr`, port 8007, compose override `docker/compose.ocr.yaml`).
The adapter lives in `services/gateway/internal/documentocr/`, is
configured as `adapters.documentOCR` (disabled by default,
`SPARKCLAW_OCR_*` env overrides), and is wired end-to-end through three
paths in the toolhub/document layer:

1. Document enrichment — `ovisDocumentOCREnricher` in
   `services/gateway/internal/toolhub/document_ocr.go`, registered ahead
   of the Fast image-semantics enricher in `document_workflow.go`.
2. The `images.inspect` tool — OCR runs in parallel with the Fast
   multimodal model in `services/gateway/internal/toolhub/images.go`.
3. Scanned PDFs — `toolhub/scripts/pdf.py` rasterizes text-less pages
   (pypdfium2 + Pillow); successful page OCR markdown is promoted into
   page blocks.

Findings that motivate this plan:

1. **Gateway panics when OCR init fails.** `toolhub.go` (~line 76) panics
   if `documentocr.New` returns an error. A misconfigured *optional*
   adapter must not take down the whole gateway; the engineering baseline
   requires graceful degradation (startup warning + call-time error).
2. **Duplicated image text in document context.** For the same image,
   `smallDocumentContextSegments` in `document_pipeline.go` emits both an
   `ocr` segment (priority 90, full OCR markdown) and an
   `image_semantic` segment (priority 80) whose body repeats the text via
   `"Visible text: "` — doubling token spend.
3. **`images.inspect` merges both outputs unconditionally.** The desired
   policy: text-bearing image → OCR; text-free image → vision
   understanding only; mixed → both combined.
4. **No agent-level awareness of OCR.** Tool descriptions, the capability
   catalog, and the Weixin attachment clarification prompt predate OCR;
   the agent does not know verbatim text extraction is available.
5. **Two unrelated topics share the uncommitted working tree** (~86
   files): the OCR integration and a WebChat delivery refactor.

Decisions already confirmed:

- Channel images (Weixin/Telegram) trigger OCR **on demand** via
  `images.inspect` — no auto-OCR at ingest. Rationale: OCR has a 120 s
  timeout ceiling and the service is provisioned for concurrency 2;
  the image is data while the accompanying text is the instruction, so
  the agent decides when a verbatim read is needed.
- Existing uncommitted work is **split into per-topic local commits
  first** (no push), before any refactor commit lands.
- Explicitly out of scope for this pass (recorded as leftovers below):
  browser screenshot OCR, WebChat upload OCR, ingest-time auto-OCR, and
  a dedicated image-text-extraction workflow profile.

## Phase 0 — Baseline and per-topic commits of existing work

1. Establish the test baseline before touching anything:
   - `npm run setup:document-tools` (without it, 13 docx/xlsx/pptx tests
     fail and the baseline is garbage)
   - `cd services/gateway && go build ./... && go test ./... && go vet ./...`
   - `cd apps/webchat && npm run build`
   - Record pre-existing failures.
2. Audit `git status` in full; strip test artifacts (a Weixin merge once
   carried observation-dump JSON into main).
3. Commit locally per topic, one topic per commit, motivation in the
   message (do not push):
   - **WebChat delivery refactor** — `App.tsx`, `composer.tsx`,
     `messages.tsx`, the deleted `delivery.tsx` /
     `deliveryDraft` / `useExternalDelivery`, `useDeliveryTarget`,
     `i18n.ts`, `app.css`, related `client.ts` / `messageStream`
     changes. Unrelated to OCR; extract first.
   - **OCR adapter + config + deployment** — the new
     `internal/documentocr/` package, `DocumentOCRAdapterConfig` /
     normalization / env keys in `config.go`,
     `configs/sparkclaw.default.json`, `configs/model.profiles.json`,
     `docker/compose.ocr.yaml`, `docker/compose.yaml`, `docker/env/*`,
     `scripts/doctor.sh`, `serve_models_compose.sh`,
     `setup-document-tools.sh`, `docs/model-loading.md`,
     `docs/deployment.md` + zh-cn mirrors.
   - **Toolhub wiring + document enrichment** — `toolhub.go`,
     `document_ocr.go`, `document_workflow.go`, `document_pipeline.go`,
     `document/enrichment.go`, `docs/document-workflows.md` + mirror.
   - **`images.inspect` OCR merge** — `images.go` + tests, the
     description portion of `registry.go`.
   - **Scanned-PDF path** — `toolhub/scripts/pdf.py`,
     `tools/document-runtime/requirements.txt`, related stat/timeout
     changes.
   - Remaining files split by actual topic (gateway ingress,
     message_control, and agent-layer diffs belong to the parallel
     publish/delivery topic).
4. Run the full gateway suite before each commit; for doc changes,
   verify zh-cn mirrors and bidirectional language links.

## Phase 1 — Replace the init panic with graceful degradation + load-time validation

- `services/gateway/internal/config/config.go`
  (`normalizeDocumentOCRConfig`, ~line 651): complete the
  construction-time checks (valid provider, parseable base URL, host
  allowlist) so an enabled-but-invalid config **fails at Load** — the
  existing `TestLoadRejectsUnsafeDocumentOCREndpoint` can be extended.
- `services/gateway/internal/toolhub/toolhub.go` (~lines 74–77): drop
  the panic; on `documentocr.New` failure log a startup warning and fall
  back to the `disabledAdapter` (already present in
  `documentocr/types.go`). Follow the weatherInfo pattern in the same
  file (~lines 57–73).
- Tests: config-level "enabled + invalid config → Load errors";
  toolhub-level "constructor failure → New does not panic and
  `images.inspect` reports `ocr_status=disabled`".
- Ships as its own commit (functional fix).

## Phase 2 — Smart merge in `images.inspect` (text → OCR, no text → vision, mixed → both)

`images.go` (~lines 94–149) already runs `h.ocr.Parse` and the Fast
`ChatWithImage` call in parallel. **OCR itself is the text detector** —
on a text-free image OvisOCR2 yields empty/trivial markdown — so no
extra detection call and no added latency; only the merge logic changes:

- OCR succeeded with non-empty markdown → emit `ocr_markdown` plus the
  vision description (mixed case: both combined; the vision half covers
  semantics/layout rather than repeating the text).
- OCR succeeded but markdown is empty/whitespace → classify as text-free;
  omit all `ocr_*` field noise; vision understanding only.
- OCR disabled/failed → existing degradation unchanged (vision only +
  `ocr_status` / `ocr_warning`).
- Add an output field (e.g. `text_detected: bool`) so the agent sees the
  classification explicitly.
- Add a helper judging "is this markdown a trivial/empty yield" (empty
  after trim, or only OCR-cleanup residue), colocated with
  `cleanOvisOCR2Output` in the `documentocr` package or in `images.go`.
- Tests: one case per branch (httptest-mocked OCR returning
  empty / non-empty / error).

## Phase 3 — Deduplicate document enrichment output

- `document_pipeline.go` (`smallDocumentContextSegments`, ~line 194):
  when an image record's `ocr.status == "succeeded"` with non-empty
  markdown, the `image_semantic` segment **skips the `"Visible text:"`
  concatenation** and keeps only the description / text-relationship.
  When OCR is disabled or failed, `ocr_text` remains the fallback — the
  Fast enricher's prompt/output structure is untouched (single point of
  adjudication preserves the degradation path).
- Tests: (a) OCR succeeded → `ocr` segment present, semantic segment has
  no Visible text; (b) OCR disabled/failed → semantic segment keeps
  Visible text.
- Commit message flags the behavior change (dedup, token savings).

## Phase 4 — Agent/channel-layer awareness

- `services/gateway/internal/toolhub/registry.go` (`images.inspect`
  registration, ~line 258): tool description states "with OCR enabled,
  extracts verbatim in-image text (markdown), auto-classifying
  text / no-text / mixed". `registry_test.go` consistency tests cover it.
- `services/gateway/internal/capability/catalog.go`: if the DocumentRead
  capability description text changes, bump the corresponding
  leafRevision and `DefaultCatalogRevision` (currently 2026-08-04.v14).
- `services/gateway/internal/weixin/chat.go`
  (`attachmentClarificationPrompt`, ~line 391): the clarification prompt
  for image attachments mentions that in-image text can be read out
  directly (copy only; no new flow).
- Docs: update `docs/workflow-capabilities.md`, `docs/intent-routing.md`,
  `docs/document-workflows.md` + zh-cn mirrors accordingly.

## Leftovers (deliberately not in this pass)

1. Auto-OCR at ingest for Weixin image-only messages (needs a config
   toggle + short timeout budget + async worker) — revisit after
   observing the on-demand path in practice.
2. A dedicated image-text-extraction capability/profile — only if
   "extract the text in this image" requests demonstrably misroute.
3. Browser screenshot OCR (as an explicit `inspect` parameter) — only if
   a canvas-heavy page produces a real comprehension failure.
4. OCR quality measurement — `docs/model-loading.md` itself records that
   broader quality measurements are still required.

## Validation (at the close of every phase)

1. `cd services/gateway && go build ./... && go test ./... && go vet ./...`
   against the Phase 0 baseline — zero new failures.
2. Confirm `npm run setup:document-tools` ran before judging
   document-tool tests.
3. Frontend-touching phases: `cd apps/webchat && npm run build`.
4. Every changed `.md` has a zh-cn mirror + bidirectional links (docs CI).
5. Verify against the DEFAULT config (file state backend); OCR stays
   disabled by default; validate the enabled state with
   `docker/env/sparkclaw.ocr.env` + the `scripts/doctor.sh` endpoint
   probe.
6. At close, refresh the sparkclaw-sop skill's dated status line and
   append any new lessons.
