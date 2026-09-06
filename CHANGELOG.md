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

- JingSi Runtime v1 tool exposure now understands `data_scope`/`network_scope`
  as tool-effect tokens (`local.compute` is the registered pure-compute
  exception). Each execution records the admission rule it was accepted under
  (`sparkclaw.admission:effect_scopes_legacy|effect_scopes_enforced`); JingSi's
  grant is persisted verbatim and never widened. `jingsi_runtime_v1.enforce_effect_scopes`
  (`SPARKCLAW_JINGSI_RUNTIME_V1_ENFORCE_EFFECT_SCOPES`, default `true` after
  InfiniCenter decision 0034 acceptance and real consumer proof) selects the rule for new executions; a run keeps
  its admission across restart re-entry.
- JingSi Runtime v1 operational bounds: the state directory is swept hourly
  under a new `jingsi_runtime_v1.retention_days` knob
  (`SPARKCLAW_JINGSI_RUNTIME_V1_RETENTION_DAYS`, default 30, `0` keeps every
  record, validated at load) that deletes terminal execution records and
  negative fences older than the window while never touching nonterminal
  work; Submit answers the retryable `runtime_unavailable` Problem
  (`retry_after_ms=5000`, no side effects) once more than 4 × `max_concurrent`
  executions are accepted but not running, so parked goroutines are bounded;
  every action answers `runtime_unavailable` until the Gateway has bound the
  provider lifecycle, so no execution runs under an uncancellable context;
  and the provider now logs bearer rejections (count only), idempotency
  conflicts, persist failures, queue rejections and terminal outcomes through
  `slog` without the goal, Memory Context, summary or bearer.
- JingSi Runtime v1 grant projection: the scope prefixes, adapter identity and
  approval-policy enum live in one leaf package (`internal/jingsiscope`) with a
  single fail-closed parser shared by tool exposure and the run budget; a
  projection that does not parse closes the run (no tools, no tool calls).
  `data_scope` and `network_scope` are now enforced at the tool-exposure
  boundary by mapping each effect a tool declares to the token JingSi must have
  granted (`external.*` to `network_scope`, `workspace.*`/`local.read`/
  `local.write` to `data_scope`); tools with no or unmapped effects are hidden
  from every JingSi execution, and the table is documented in
  `docs/jingsi-runtime-v1.md`. `budget.max_output_bytes` is no longer projected
  into the run (the provider alone bounds the result summary). Executions now
  report the attachments they delivered as opaque versioned `artifact_refs`
  with one `artifact.available` event per reference before the terminal event.
- Decisions closed from the sixth review: opening an email provider's login
  browser now requires the provider to be enabled first (login no longer flips
  the enable switch, and the WebChat login button follows the toggle); runs a
  previous Gateway process left in `executing` are marked `failed` at startup
  with audit `run.interrupted_by_restart` instead of reporting executing
  forever, and the LocalMind task design no longer claims polling resumes
  across restarts; a non-loopback `gateway.bind` without pairing or an API
  token logs a startup warning; the inert `model.fast.mtp`/`model.deep.mtp`
  fields, the never-produced `ModelCall.fallback`/`error_note` fields (Postgres
  migration `0011` drops the column) and their WebChat types were removed; the
  JingSi Runtime request key is documented as bound to the authenticated caller
  (the contract forbids cross-space reuse) and result summaries as capped at
  64 KiB inside the contract's 131072-byte response bound; ASR is documented as
  outside the model capacity contract. CI runs the central JingSi contract gate
  when an `INFINICENTER_TOKEN` secret grants read access to the hub.
- Store and Gateway telemetry: `GET /metrics` now derives its message, run,
  model call, tool call and episode summary totals from bounded Store
  aggregate reads (`CountVisibleMessages`, `CountVisibleRuns`,
  `ModelCallStats`, `CountToolCalls`, `CountEpisodeSummaries`, implemented on
  the memory, file and PostgreSQL backends) instead of listing every record
  on each scrape. Exported metric names and values are unchanged.
- Approvals carry `arguments_immutable`, projected from the new
  `ToolDefinition.ArgumentsImmutable` flag (set for `email.send`); the
  executor fingerprint check, the `409` from `POST /api/approvals/{id}/modify`,
  and the WebChat edit affordance all read the flag instead of matching the
  tool name. Postgres migration `0010` adds the column; approvals persisted
  before the upgrade report the flag as `false` while the server still rejects
  modification.
- Email automation: the Gateway provider registry is generated from the
  Controller registry (`provider_scripts.json`, regenerated with
  `npm run sync:provider-contract --prefix tools/browser-controller`); the Go
  copies of login URLs and allowed origins, which had drifted and were never
  used, are gone. Provider script failures are classified through the shared
  `app.ToolErrorEmail*` codes; QQ Mail's `body_too_large` now reports
  `email_invalid_input` instead of `email_provider_unavailable`.
- Integration settings: the credential-check API never returned the documented
  `checking` state (it was overwritten before any response), so the state table
  drops it; WebChat keeps rendering its own in-flight label. Browser control
  still reports `checking` while a validation is running.
- Sealed PPTX candidates no longer accumulate in the artifact store. The
  candidate bytes and manifest are discarded right after an approved
  publication's completed status is durable (audited as
  `document.pptx.candidate_discarded`), and the hourly retention coordinator
  sweeps `pptx/sealed/` objects older than the 24h approval TTL in bounded
  pages (audited as `document.pptx.candidate_expired`). The artifact store
  interface gained a bounded `List(prefix, startAfter, limit)`; the
  filesystem and S3 backends implement it and unimplemented backends return
  an explicit error, so the sweep logs and skips rather than silently
  doing nothing.
- Store: the in-memory and File backends keep their per-session tool-call and
  episode ordering by binary-search insertion and rebuild it with one sort per
  session on load, so Gateway startup on the default File backend no longer
  slows down quadratically with session length (1.37 s to 7 ms per session at
  4000 tool calls and 4000 episodes). The recent-history ordering contract is
  unchanged.
- Configuration: `SPARKCLAW_MODEL_MODE` and the JSON `model.mock` field are
  retired; both had been inert since the capacity contract landed because the
  selected capacity profile's `mock` flag is the only source of mock routing.
  Setting either now fails config load with a message pointing at
  `model.capacity_profile` / `SPARKCLAW_MODEL_CAPACITY_PROFILE`. The variable
  was removed from compose, the env profiles, the deployment validator, CI,
  `run-eval.sh` (which now derives `SPARKCLAW_EXPECT_REAL_MODELS` from the
  capacity profile), and `local-dev-env.sh`.
- Configuration: `OPENAI_API_KEY` is a real config knob (`ModelConfig.APIKey`,
  environment only) attached by the model router on every request instead of
  being read from the process environment per request; a non-mock lane whose
  base URL points at a remote host while the key is empty is logged as a
  startup warning.
- Configuration: the gateway binary no longer carries a build-machine path to
  `configs/model.profiles.json`; `model.capacity_catalog` resolves relative to
  the config file and the built-in default relative to the working directory,
  with `SPARKCLAW_MODEL_CAPACITY_CATALOG` as the explicit override.
- Configuration: `docker/compose.yaml` falls back to the documented 20000 ms
  `SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS` (was 15000); a contract test
  now pins the Go defaults against `configs/sparkclaw.default.json`,
  `docker/env/sparkclaw.product.env`, and the compose fallbacks, including the
  product profile's deliberate 200000-byte stage evidence budget.
- Deployment: `docker compose` now requires an explicit
  `SPARKCLAW_MODEL_CAPACITY_PROFILE` (the previous `mock` default silently
  routed every model call to the mock router when the deploy scripts were
  bypassed), the vLLM capacity entrypoint refuses profiles flagged `mock`,
  `SPARKCLAW_PAIRING_REQUIRED` defaults to `true` in compose, and the
  duplicate `dgx-spark-local` capacity profile was removed.
- Managed browser windows (WeChat QR login) are handed to the owner after
  opening; the Playwright bridge opens tabs in the background, so the page was
  previously created but never shown.
- PPTX final-render visual QA re-reviews the whole authorized slide selection
  after every repair attempt (a blocking issue on a slide without a repair
  could previously disappear before sealing), reports a missing Python runtime
  as renderer unavailability instead of an evidence-integrity failure (so the
  shadow phase no longer aborts governed mutations), and records
  `pptx_render_repair_exhausted` when the repair budget ends.
- PPTX final-render visual QA verifies the renderer stack it attests to: the
  Gotenberg, LibreOffice, and pypdfium2 pins are configuration
  (`SPARKCLAW_PPTX_VISUAL_QA_{GOTENBERG,LIBREOFFICE,PDFIUM}_VERSION`, defaults
  unchanged and checked against the Compose and document-runtime pins by test),
  the sealed manifest records the configured values, and a running Gotenberg
  or pypdfium2 whose version differs from the pin fails preparation with
  `pptx_render_stack_mismatch` in every phase. A Fast assessment that omits a
  required fact review is reported as `pptx_render_model_invalid` instead of
  `pptx_render_model_unavailable`.
- Email automation: provider script timeouts and invalid-output codes map to
  `email_script_timeout` / `email_script_invalid_output` instead of
  `email_provider_unavailable`; `/api/email` errors carry `code` and
  `retryable`; store failures return a bounded message; controller calls are
  bounded by the script budget. The inert `adapters.emailAutomation.scriptDir`
  setting and `SPARKCLAW_EMAIL_SCRIPT_DIR` were removed.
- Integration settings: a failed operator or household activation can be
  retried from the settings panel, and a failed credential write no longer
  leaves the in-memory credential list corrupted.
- JingSi Runtime v1 routes share the gateway rate limiter, an execution whose
  running state cannot be persisted becomes an explicit `failed` outcome, and
  the central-contract gate in `internal/contracttest` skips (or reads
  `SPARKCLAW_JINGSI_CONTRACT_MANIFEST`) instead of failing every fresh clone.
- LocalMind grounding replaces the final answer only for runs of the explicit
  LocalMind workflows and no longer scans the complete session tool-call
  history on every routed message; a malformed JingSi `max_tool_calls` budget
  now fails closed in the step loop as it already did in tool exposure.
- Browser controller: `browser_lane_unavailable` and
  `browser_controller_stopping` are mapped explicitly, deadline-less
  controller calls get a five-minute backstop, and CI now runs the browser
  bridge test suite. The Bridge native host and the Controller Unix sockets
  are now created owner-only (bound under a `0077` umask and set to `0600`
  before readiness) instead of being tightened after the listener was already
  accepting connections.
  bridge test suite.
- Browser runtime, seventh pass: Playwright tab-list and snapshot parsing is
  pinned to a golden recorded from the pinned MCP/CLI packages (the fakes had
  invented shapes); the Bridge tracks the tab groups it created in
  `chrome.storage.session` and stale cleanup closes only those (an owner group
  titled "SparkClaw task" was previously emptied), which adds the `storage`
  permission and new artifact checksums; a failed relay handshake now closes
  its socket; Gateway shutdown and session release no longer wait behind an
  in-flight controller call (a release deferred behind one returns
  `browser_busy` and completes in the background); `tools.browserAutomation.provider`
  must be `playwright-extension` and `adapters.browserAutomation.startupTimeoutMs`
  (now the session-acquire wait, 500 to 30000 ms) is validated; the unread
  `require_visible_environment` argument, the never-emitted `tabs.select` /
  `page.reload` controller operations, and the `browser_click` requirement were
  removed; the browser control status reports `cli` / `cli_version`; the
  Controller-to-Gateway error codes live in one shared table.
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

- `GET /readyz` resident-service status now reads only the newest model call
  per lane through the new bounded `RunRepository.LatestModelCallsByLane`
  (Postgres `DISTINCT ON` plus schema migration `0009`, which adds a
  `(lane, started_at DESC, id DESC)` index on `model_calls`) instead of
  loading every persisted model call on each five-second WebChat poll.

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
