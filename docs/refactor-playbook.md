# SparkClaw Periodic Refactoring Playbook (Agent Instructions)

> Language: English | [简体中文](../zh-cn/docs/refactor-playbook.md)

This is the standard instruction document handed to an Agent (e.g. Claude Code) when kicking off a periodic architecture refactoring task. The repo mounts the Team-Skills `sparkclaw-sop` skill (auto-injected at session start, routing here), so starting a task only requires stating the review scope (e.g. "the last N commits" or "package X"); everything else follows this playbook without restating it.

---

## Mission & boundaries

- **Goal**: optimize code architecture while keeping existing functionality working, so the project has a robust foundation for long-term evolution.
- **Red line**: do not change externally visible behavior — unless the "behavior" is itself a defect (silently broken features, declared-but-inert capabilities). Any behavior change must be listed separately in the final report.
- **Referee**: tests are the sole referee of refactoring correctness. Where coverage is thin, add tests before cutting.
- Review against the [Engineering Baseline Rules](engineering-baseline.md) — refactoring means bringing existing code back above that baseline.

## Step 0: Establish the baseline (never skip)

1. Clean build: `cd services/gateway && go build ./...`
2. Environment deps: document-tool tests need `npm run setup:document-tools` (installs Node packages through the root workspace and Python libraries into the host user site).
3. Full tests: `go test ./...`; frontend `npm install && npm run build` (in `apps/webchat`, includes `tsc -b`).
4. **Record the baseline**: which packages pass, which fail and why. Without separating "already broken" from "broken by me", all later verification is meaningless.

## Step 1: Parallel reconnaissance

- Fan out read-only explore agents by package partition (historical partitioning: `agent` / `toolhub` / `gateway`+`store`+`config` / `weixin`+`notification`+`reminder`+`binding`+`browserautomation` / frontend).
- Every finding must carry: `file:line`, one-sentence description, severity (high/medium/low), concrete refactoring suggestion.
- Draw the package dependency graph yourself with `go list` to verify coupling claims — don't take agent summaries on faith.

## Step 2: Prioritize and select

Rank by value/risk ratio, highest priority first:

1. **Silently broken functionality** (type-assertion degradation, persisted-but-inert fields) — these are user-visible bugs;
2. **Resource leaks and unbounded execution** (missing timeouts, orphan processes, no retry backoff);
3. **Single-source-of-truth violations** (multi-site switch/name-list/constant syncing, string-restated enums);
4. **Dead code removal** (grep to confirm zero production callers first; delete the fake tests with it);
5. **God-file splits** (same-package pure moves, zero API change);
6. Pure style issues — usually not worth doing; put them in the leftovers list.

Large independent items (concurrency model changes, functional bug fixes, giant-file splits) become **separate background tasks** (spawn_task / dedicated worktrees) that don't block the main line; small certain items go sequentially in the main working tree.

## Step 3: Implementation discipline

- **Small steps**: after each topic, run `go build ./...` + affected-package tests; only proceed on green.
- **Separate mechanical moves from behavior changes** in commits. When doing bulk moves with scripts, verify via byte-identical extraction + end-to-end tests.
- **One topic per commit**, message explaining the motivation (why the status quo was a problem), ending with the `Co-Authored-By` attribution.
- When test assertions need changing, first understand **why** they changed: an intended behavioral improvement (e.g. backend capability alignment changing a return type) may update assertions with an explanation in the commit; an unexpected change is a regression — fix the code instead.
- Items where "fixing it changes behavior" (defect class) are functional fixes, not refactoring: commit separately and flag in the report.

## Step 4: Verification

- `go test ./...` fully green (compared against the Step-0 baseline — no new failures allowed); `go vet ./...` clean.
- Frontend changes: `tsc -b` + `npm run build`; for pure-move splits, use bundle-size comparison as no-behavior-change evidence.
- With background tasks: verify each in its own worktree first (build + tests + diff spot-check), merge only after quality is confirmed; run affected tests after each branch merge.
- When merging changes from other sessions, check `git status` and strip stray test-run artifacts.

## Step 5: Wrap-up

- After merges complete and tests are green: remove merged worktrees and branches.
- Committing is autonomous; **before pushing `origin main`, list the full commit set in the report** (unless the task instruction explicitly pre-authorizes pushing).
- Unfinished high-value items: file them as background task suggestions (spawn_task); each task prompt must be self-contained (file paths, problem description, acceptance criteria) without depending on the current session's context.
- **Lessons write-back**: capture this pass's new failure patterns as lessons appended under `Team-Skills/projects/SparkClaw/sparkclaw-sop/lessons/` (one file per lesson, append-only, `status: pending`); promotion to guardrails happens at the next consolidation. This document stays lean — do not pile experience into it directly.

## Report format

- **Conclusion first**: the opening sentence answers "what changed and what state are we in".
- Three lists: completed (by topic, with file links), behavior changes (if any), leftover recommendations (with reasons for not doing them).
- Write for someone returning in two weeks; no codenames only this session would understand.

## Project quick reference (skip re-discovery)

- Package dependency direction: `app` is a pure-types leaf (imports no internal packages); `gateway` is the HTTP layer; `agent` is the orchestration core; `toolhub` is the tool hub. Edges like `toolhub → agent` or `app → anything` are forbidden.
- Tool registration: [registry.go](../services/gateway/internal/toolhub/registry.go); schemas in `defaultDefinitions()`; the two are kept consistent by `registry_test.go`.
- Store implementations: `memory.go` (the real one) / `file.go` (write-through decorator) / `postgres.go`. Interface additions touch all three; the snapshot struct is `Snapshot` in `file.go`.
- Document tool scripts: `internal/toolhub/scripts/*.py|.js`, included via `//go:embed`; subprocesses go through `runSubprocessAdapter` (built-in timeout).
- WeChat protocol/crypto: unified in [weixinproto](../services/gateway/internal/weixinproto/proto.go).
- Manual tool invocation orchestration: `agent.Runtime.InvokeToolManually` (manual.go); the HTTP handler only decodes/maps.
- Docs CI: every `.md` needs a `zh-cn/` mirror and bidirectional language links (see `.github/workflows/ci.yml`).
