# Changelog

> Language: English | [简体中文](zh-cn/CHANGELOG.md)

All notable project-level changes should be recorded here.

The project is pre-1.0. Breaking changes may occur, but they should be documented when they affect users, operators or contributors.

## [Unreleased]

### Added

- ISCP v0.2 managed Bridge operation: an `iscp-bridge enroll-ticket`
  subcommand that redeems a Cloud-issued pairing ticket v3 into a
  `mode: "managed"` enrollment bundle, a managed session layer in which the
  Bridge holds the Trust Grant and initiates toward the responder-only phone,
  and proactive grant auto-renewal plus Relay credential recovery gated on the
  Relay descriptor's advertised capabilities. The legacy externally-issued
  dual-grant enrollment contract is unchanged.
- Read-only phone home-screen projections `agent.activity.list.v1` and
  `agent.snapshot.get.v1` (capability `agent.snapshot` v1), aggregated from
  existing approval, run, and notification state with no new store entity.
- Native record-time WebChat speech transcription with revisioned Qwen3-ASR
  partials, an authoritative same-session final, browser-local silence stop
  modes (Off by default), and automatic complete-WAV batch recovery after any
  mid-recording realtime failure.
- A website-streamable installer and GB10 DGX Spark deployment entrypoint that
  safely clone/update the checkout, preserve an interactive secret prompt across
  `curl | bash`, prepare local configuration, download and warm the resident
  model group, start Gateway/Sandbox/WebChat, and verify readiness.
- Streamable HTTP MCP discovery and ToolHub registration for independent Happy
  Team task and personal bridge endpoints, plus a durable Happy supervised-plan
  approval inbox with live plan retry, editing, and remote-first reconciliation.
- Document OCR: an opt-in OvisOCR2 adapter (`internal/documentocr`, the
  `sparkclaw-ocr` compose service and `SPARKCLAW_OCR_*` settings) that recovers
  scanned PDF pages with bounded OCR, enriches image evidence, and degrades to
  disabled when unconfigured.
- LocalMind scoped workspace MCP integration: identity-pinned discovery,
  bounded catalog selection, namespaced `localmind.*` dynamic tools with
  redacted, size-bounded result projection; opt-in via environment-resolved
  URL/token settings.
- Managed inbound MCP/ISCP access: single-use hash-bound access tickets,
  durable peer bindings and idempotent conversation operations exposed over the
  encrypted ISCP bridge and an opt-in LAN `/mcp` endpoint, with owner-facing
  transport toggles and access-record deletion in WebChat.
- Passive ISCP collaboration notifications with a durable per-owner inbox and
  a global WebChat notification center.
- WeChat notification-binding QR login now opens inside the managed visible
  Chromium profile instead of the host default browser.
- Current-state architecture, deployment and development documentation.
- Chinese documentation mirror under `zh-cn/` for project docs.
- DGX Spark model-serving guidance and benchmark evidence.
- Open-source project files: license, contribution guide, security policy, support guide, code of conduct and GitHub templates.

### Changed

- The experimental JingSi LAN presentation routes moved under one
  `/api/jingsi/v0/` prefix (`POST /api/jingsi/v0/messages/stream`,
  `GET /api/jingsi/v0/client-events{,/head,/stream}`, and the phone-facing
  readiness probe is now `GET /api/jingsi/v0/readyz`). The gateway itself now
  rejects non-private peers and non-private browser origins on these routes,
  and the LAN port is configurable via `SPARKCLAW_JINGSI_LAN_PORT`
  (default `18793`).
- The Qwen3-ASR image now runs one SparkClaw-owned batch/realtime runtime,
  serializes all model calls on one owner thread, and completes a first-inference
  warm-up before advertising readiness. Gateway exposes realtime only through
  an authenticated, single-use WebSocket ticket and shares admission capacity
  with batch transcription.
- Boot startup now bounds each Docker/NVIDIA readiness probe, detects stale
  installed systemd units in doctor, and includes Qwen3-ASR in the atomic
  single-Fast resident group and Gateway runtime by default, with a fixed ASR
  KV cache budget that avoids negative utilization-based cache estimates.
- Deployment startup now aligns the product template on PostgreSQL without
  migrating legacy file snapshots, retains healthy/current model groups while
  atomically recovering degraded groups, offers an explicit force-refresh flag,
  owns the WebChat host port through one validated setting, embeds readiness in
  the vLLM image with best-effort tmpfs markers, and bounds boot reconciliation
  with a four-hour oneshot systemd unit.
- Managed Weixin QR-login Chromium windows now use independent per-binding
  locks and a fixed 10-minute sliding lease. A 30-second janitor retries failed
  expiry cleanup, graceful shutdown releases every tracked window before the
  browser adapter closes, and unrelated owners no longer serialize behind
  another window's browser round trips.
- Connector activation is now owner-isolated inside one household Gateway:
  startup restores every owner's persisted setting into a write-through cache,
  one shared worker per channel uses owner gates, one owner's opt-out cannot
  stop another's runtime, admitted replies drain while undispatched input
  pauses, and preload failure prevents Gateway listen. `/api/config` now reports
  the real static bootstrap default in `operator_enabled`.
- Unified consumer-scoped evidence projections across document decisions,
  document/browser model stages, and finalization with lineage/coverage audit;
  added normalized document operation candidates, one bounded PPTX semantic
  repair with ephemeral layout/preservation preflight before approval, PDF
  claim coverage, browser transition evidence, repeated-action blocking, and
  deterministic visible-presentation equivalence.
- The default `npm start` and installer paths now use the PostgreSQL-backed
  product runtime, start and wait for PostgreSQL before Gateway, and no longer
  apply the former file-backed `minimal` override.
- Model-serving health checks and joint startup now allow bounded multi-hour
  cold downloads instead of failing after the previous short readiness window.
- Advanced `document.edit` to revision 6 for XLSX: typed bounded sheet evidence,
  evidence-bound workbook/cell/row/sheet edits, prefix-only `update_row`, six
  explicit operation-selection boundaries, and fail-closed OOXML package
  verification now protect every generated copy.
- Replaced old planning, audit and handoff documents with current maintainable docs.
- Consolidated intent routing, messaging/scheduling, browser, document,
  integration and WebChat documentation into six current component guides plus
  one documentation index; removed 29 completed or superseded document pairs.
- Excluded runtime skill packages from the bilingual documentation mirror because skills evolve independently.

### Validated

- Qwen3-ASR candidate cold readiness and first-request warm-up, batch output
  parity, a genuine 4.439-second partial/final stream, a record-paced 60-second
  stream below the 5-second backpressure bound, and realtime/batch capacity
  exclusion and release; desktop/mobile fake-microphone passes also verified
  the AudioWorklet-to-draft path without a healthy-path batch request.
- Gateway build/test/vet, WebChat tests/build, bilingual documentation checks,
  doctor, and 47 isolated mock/file golden evals for evidence projection changes.
- PostgreSQL product-start Compose selection and readiness.
- WebChat production build.
- Gateway skill registry test.
- Docker Compose config validation.
- `scripts/doctor.sh`.
- Markdown link and language-switch checks.
