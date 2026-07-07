# SparkClaw Engineering Baseline Rules

> Language: English | [简体中文](../zh-cn/docs/engineering-baseline.md)

This document is the **non-negotiable baseline** for every code contribution, not a style guide. Every rule traces back to a real problem found in this repository and fixed during the 2026-07 architecture cleanup. PRs violating any rule will not be merged.

---

## 1. Dependencies & environment: a fresh clone must run

- **Every runtime dependency must be declared in a version-controlled manifest** (`go.mod`, `package.json`, `requirements.txt`). Never rely on packages that "happen to be installed" on your machine.
  - *Incident*: document tools did `require("exceljs")` at runtime with no manifest anywhere in the repo; 13 tests failed on a clean environment. The correct home is now [tools/document-runtime](../tools/document-runtime/package.json), installed via `npm run setup:document-tools`.
- **Never hardcode developer-machine paths or platforms.** Paths like `.tools/node-v24.14.0-darwin-arm64/bin/node` may only be optional probes with a cross-platform fallback.
- When adding an external dependency, update the setup/doctor scripts so that `git clone && setup && go test ./...` passes in one shot.

## 2. Subprocesses & outbound calls: timeouts and ownership

- **Every `exec` and outbound HTTP call must have a timeout.** If the caller's context carries no deadline, the callee must apply its own bound (see the 60s fallback in `runSubprocessAdapter`).
  - *Incident*: all document subprocess calls had zero timeout; one hung node process could pin a request forever.
- **Long-lived subprocesses must be rooted in a cancellable context and expose `Close()`**, wired into process shutdown.
  - *Incident*: the browser-automation MCP subprocess was rooted in `context.Background()` with no close hook; Chrome survived gateway shutdown as an orphan.
- **Process failure and business errors must be distinguishable.** Never `print(error json)` then `exit(0)`; either exit non-zero or make the caller check both error channels.

## 3. Single source of truth: define each fact in exactly one place

- **Adding a tool/enum/protocol constant must touch exactly one registration point.** If you find yourself syncing the same name into 2+ places, stop and build a registry or constant table first.
  - *Incident*: adding a tool required syncing a schema list, a 90-line dispatch switch, and feature-gate name lists; missing one only failed at runtime. The registration point is now [registry.go](../services/gateway/internal/toolhub/registry.go), backed by a consistency test.
- **Never restate an existing enum as a string literal.** Before writing `"small_direct"`, look for `app.DocumentStrategySmallDirect`.
- **Never branch on user-facing display strings** (e.g. `strings.HasPrefix(line, "兜底策略：")`). Express semantics as typed fields; copy changes at any time.
- WeChat protocol constants, crypto, and headers live in [weixinproto](../services/gateway/internal/weixinproto/proto.go) — do not copy them into your package.

## 4. Interface implementations must be complete; missing capability must be explicit

- **When adding a method to an interface, update every implementation at once** (the store has memory/file/postgres backends).
- **Never create "optional capabilities" via type assertions that silently degrade.**
  - *Incident*: `DocumentStore` was implemented only by Postgres; on the default file backend, document search silently returned empty through `if ds, ok := store.(DocumentStore); ok`. If a capability is genuinely optional, log a startup warning and return an explicit error at call time.

## 5. Declared features must work end to end

- **A persisted field is not an implemented feature.** Every advertised behavior needs one end-to-end test proving it actually happens.
  - *Incident*: reminder `Recurrence` was captured and stored, but the scheduler never computed the next occurrence — "daily" reminders fired exactly once.
- Test failure paths too: failures marked `retryable` must actually be retried.

## 6. No dead code on main

- **Code with no production caller does not merge.** "We'll need it later" belongs on your branch.
  - *Incident*: 269 lines of document-pipeline types had zero references repo-wide; a 110-line planner was called only by tests, faking coverage with 50+ test cases.
- Deleting beats commenting out; git history is the recycle bin.

## 7. HTTP & concurrency semantics

- **GET must have no side effects** (no writes, no state-machine advancement). Poll-style state progression uses an explicit POST or a background reconciler.
- **Never construct heavy resources per request** (policy engines, connection pools, clients). Build once, hang off the server object.
- **No slow operations inside poll/ticker goroutines** (LLM calls, large downloads). Dispatch slow work to a bounded worker pool; protect the polling cadence.
  - *Incident*: WeChat message handling ran agent turns synchronously inside the poll goroutine; one slow reply blocked every user.

## 8. Configuration

- **One switch, one field.** No `Enabled`/`Disabled` boolean pairs that humans must keep inverse-synced.
- Every new config knob needs: a default, plus load-time validation or backfill (see `normalizeRuntimeLimits` — an invalid zero must not silently disable the runtime).

## 9. Commit discipline

- **One topic per commit.** No 30k-line "Upload project" commits — they defeat review, bisect, and rollback.
- **Never commit test-run data** (artifacts, temp dirs). Run `git status`; investigate any file you don't recognize.
- Pre-commit checklist: `go build ./... && go vet ./... && go test ./...` green; add `npm run build:webchat` for frontend changes.

## 10. File and function size (soft limits; exceeding requires justification in the PR)

- Single file > 800 lines, single function > 100 lines, or one package with 2+ unrelated responsibilities — hitting any of these requires an explanation in the PR description for why it isn't split.
  - *Incident*: `agent.go` once reached 4032 lines mixing six responsibilities; frontend `App.tsx` reached 3609 lines.
