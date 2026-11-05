# SparkClaw Periodic Refactoring Playbook (Agent Instructions)

> Language: English | [简体中文](../zh-cn/docs/refactor-playbook.md)

This is the standard instruction document handed to an Agent (e.g. Claude Code) when kicking off a periodic architecture refactoring task. The repo mounts the Team-Skills `sparkclaw-sop` skill (auto-injected at session start, routing here), so starting a task only requires stating the review scope (e.g. "the last N commits" or "package X"); everything else follows this playbook without restating it.

---

## Mission & boundaries

- **Goal**: optimize code architecture while keeping existing functionality working, so the project has a robust foundation for long-term evolution.
- **Red line**: do not change externally visible behavior — unless the "behavior" is itself a defect (silently broken features, declared-but-inert capabilities). Any behavior change must be listed separately in the final report.
- **Referee**: tests are the sole referee of refactoring correctness. Where coverage is thin, add tests before cutting.
- Review against the [Engineering Baseline Rules](engineering-baseline.md) — refactoring means bringing existing code back above that baseline.

## Subagent decision rules

These rules also apply to large feature tasks. Keep one main agent responsible for the requirement, design decisions, integration and final acceptance. Task size, package count, or a long-running command alone does not justify another agent; a background command or an isolated worktree does not itself need a separate reasoning context.

- **Check the context boundary first**: will later decisions need the intermediate observations and reasoning, or just a compact result with inspectable evidence? Keep tightly coupled work in the main loop, including sequential producer/consumer changes and changes that repeatedly read or write the same state. Load only the relevant Skill references and tools before adding agents to solve context pressure.
- **Delegate when isolation has a concrete benefit**: independent investigations with disposable intermediate output, components with settled interfaces and exclusive ownership, or independent review/black-box acceptance. If context growth or tool-selection confusion is the reason, record an observed failure or comparative evaluation; task length alone is not evidence. When no suitable boundary exists, keep even a large task in the main loop.
- **Define the assignment before spawning**: specify the current requirement and constraints, exact checkout/base, required inputs, dependencies, read-only or writable scope, acceptance checks and expected return. Assign only ready tasks; settle shared interfaces first. Use the smallest useful number of agents within actual capacity, and require the main agent to approve further decomposition instead of recursive fan-out by default.
- **Keep the critical context with its owner**: send a self-contained, minimal task brief rather than the whole conversation. Return conclusions, file/line or artifact references, actual checks/results and unresolved questions; retain raw evidence for inspection. If agents need repeated clarification, overlapping edits or substantial intermediate context, fold that work back into the main loop or sequence it.
- **Review independently when warranted**: for substantial or risky changes, give a read-only reviewer the current requirements/constraints, diff and test results, with access to relevant source. Start with a fresh context rather than the author's conversation or rationale. The main agent checks each finding against code/tests before applying it; routine small edits do not require a review agent.
- **Preserve state and validate the whole task**: maintain the existing design/progress record for long work, including accepted decisions, ownership, dependencies, review dispositions and next checks. Update it before compaction or handoff. Subagent success is only local evidence; verify the integrated user flow and original UI interactions where affected. For a recurring delegation strategy, compare representative end-to-end tasks before/after a change, repeat variable model runs, and inspect individual regressions as well as time/cost rather than claiming a gain from agent count.

This project policy adapts the context-isolation and independent-review principles in [MCode Engineering Note 01](https://my.feishu.cn/wiki/DBtRwh8fpi4ADUkbbEPcuIi8nHe). The article's model-specific token/tool counts and performance anecdotes are not project thresholds. Agent-side assignment and worktree details live in the mounted SOP's `references/project-sop.md`.

## Step 0: Establish the baseline (never skip)

1. Clean build: `cd services/gateway && go build ./...`
2. Environment deps: document-tool tests need `npm run setup:document-tools` (installs Node packages through the root workspace and Python libraries into the host user site).
3. Full tests: `go test ./...`; frontend `npm install && npm run build` (in `apps/webchat`, includes `tsc -b`).
4. **Record the baseline**: which packages pass, which fail and why. Without separating "already broken" from "broken by me", all later verification is meaningless.

## Step 1: Reconnaissance; parallelize independent questions

- Inspect the scope and dependencies in the main loop first. Delegate bounded read-only questions only when they pass the subagent decision rules above; package boundaries are search scopes, not an automatic agent-per-package assignment.
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

For a large item, first decide whether its context and write ownership can be isolated. Delegate ready, independent items with explicit acceptance criteria; keep shared-contract, sequential and small certain changes in the main loop. A long test/build can run as a background command without spawning an agent. Use subagents within the current task; create a separate user-owned chat only when the user asks for one.

## Step 3: Implementation discipline

- **Small steps**: after each topic, run `go build ./...` + affected-package tests; only proceed on green.
- **Separate mechanical moves from behavior changes** in commits. When doing bulk moves with scripts, verify via byte-identical extraction + end-to-end tests.
- **One topic per commit**, message explaining the motivation (why the status quo was a problem), ending with the `Co-Authored-By` attribution.
- When test assertions need changing, first understand **why** they changed: an intended behavioral improvement (e.g. backend capability alignment changing a return type) may update assertions with an explanation in the commit; an unexpected change is a regression — fix the code instead.
- Items where "fixing it changes behavior" (defect class) are functional fixes, not refactoring: commit separately and flag in the report.

## Step 4: Verification

- `go test ./...` fully green (compared against the Step-0 baseline — no new failures allowed); `go vet ./...` clean.
- Frontend changes: `tsc -b` + `npm run build`; for pure-move splits, use bundle-size comparison as no-behavior-change evidence.
- With implementation subagents: inspect each result and diff, verify isolated branches in their own worktrees first, then integrate one at a time and run affected tests after each merge. Shared-checkout work has no branch merge: review the owned diffs and validate the combined tree. Run the task's end-to-end acceptance after integration; individually passing branches do not establish overall correctness.
- When merging changes from other sessions, check `git status` and strip stray test-run artifacts.

## Step 5: Wrap-up

- After integration and acceptance are complete: collect every subagent's result or explicit unresolved status, stop remaining task work, and remove only this task's fully integrated worktrees and branches. Preserve unfinished work for handoff.
- Committing is autonomous; **before pushing `origin main`, list the full commit set in the report** (unless the task instruction explicitly pre-authorizes pushing).
- Unfinished high-value items: record self-contained follow-up suggestions (file paths, problem description, acceptance criteria and reason left open). Do not turn a suggestion into a new user-owned chat without the user's request.
- **Lessons write-back**: capture this pass's new failure patterns as lessons appended under `Team-Skills/projects/SparkClaw/sparkclaw-sop/lessons/` (one file per lesson, append-only, `status: pending`); promotion to guardrails happens at the next consolidation. This document stays lean — do not pile experience into it directly.

## Report format

- **Conclusion first**: the opening sentence answers "what changed and what state are we in".
- Three lists: completed (by topic, with file links), behavior changes (if any), leftover recommendations (with reasons for not doing them).
- Write for someone returning in two weeks; no codenames only this session would understand.

## Project quick reference (skip re-discovery)

- Package dependency direction: `app` is a pure-types leaf (imports no internal packages); `gateway` is the HTTP layer; `agent` is the orchestration core; `toolhub` is the tool hub. Edges like `toolhub → agent` or `app → anything` are forbidden.
- Tool registration: [registry.go](../services/gateway/internal/toolhub/registry.go); schemas in `defaultDefinitions()`; the two are kept consistent by `registry_test.go`.
- Store implementations: `memory.go` (the real one) / `file.go` (write-through decorator) / `postgres.go`. Interface additions touch all three; the snapshot struct is `Snapshot` in `file.go`.
- Document tool scripts: `internal/toolhub/scripts/*.py|.js` plus the `pptx_slide/` Python package, included via `//go:embed`; single scripts go through `runSubprocessAdapter` (built-in timeout) and the package runs via `runPythonPackageAdapter`.
- WeChat protocol/crypto: unified in [weixinproto](../services/gateway/internal/weixinproto/proto.go).
- Manual tool invocation orchestration: `agent.Runtime.InvokeToolManually` (manual.go); the HTTP handler only decodes/maps.
- Docs CI: every `.md` needs a `zh-cn/` mirror and bidirectional language links (see `.github/workflows/ci.yml`).
