# Parallel Feature Integration Plan

> Language: English | [简体中文](../zh-cn/docs/parallel-feature-integration.md)

This document is the merge contract for the `codex/integration-voice-telegram-info`
worktree. It coordinates the Infinimesh information, speech, and Telegram branches
without taking ownership of their core implementations.

## Baseline And Current State

The fixed integration base is local `main` commit
`081c67a8c77ab0b838b547cd7b244d2eabc34c21` on 2026-07-14. Local `main` is nine
commits ahead of `origin/main`; this work never pushes or merges back to `main`.

Baseline setup and validation were run before this document was created:

| Check | Result |
|---|---|
| `npm run setup:document-tools` | Passed; Node and Python document runtimes installed under ignored `.tools/` paths |
| `go build ./...` in `services/gateway` | Passed |
| `go vet ./...` in `services/gateway` | Passed |
| `go test ./...` in `services/gateway` | Passed, including `internal/toolhub` after setup |
| `npm --workspace @sparkclaw/webchat run build` | Passed; `tsc -b` and Vite production build completed |
| Environment note | Host Go is `go1.25.12`; host Node `v22.23.0` is below the declared Node 24 minimum even though the build passed |

Branch readiness at baseline:

| Branch | Commits after base | Worktree state | Merge state |
|---|---:|---|---|
| `codex/infinimesh-info` | 0 | Clean | Waiting for completion report and commits |
| `codex/voice-complete` | 0 | Clean | Waiting for completion report and commits |
| `codex/telegram-hardening` | 0 | No completed branch work available | Waiting for completion report, commits, and clean worktree evidence |

The integration branch remained in **waiting for merge** state until each branch had
a reviewable commit range and a clean worktree. All three gates were later satisfied;
no uncommitted or snapshot code was copied into this branch.

## Exact Commit Sets

Infinimesh Info:

- `e97b2e5b78adc1c34a9fff9a49512560a49ee3c1`
- `1bf7f56c9c1b8076b009be3e9c407beb643313f5`

Speech and WebChat voice:

- `aacf123ef8fc817d8a635289a7cdfc72e4ba6f5b`
- `ab2db5318c9aef0a36f0209c063a3c534c0a38cf`
- `185a83c5308d6f6c03ca15614d3376fb2f081eaf`
- `0ff4be1b15b64026e211f266ed735c8498a2bb9c`

Telegram hardening:

- `c489baf05c2a1997a0e92862744c8835b3134f30`
- `7a2b3f45647096482c5753187ebea8288ed57512`
- `a992a65fa36ad02a68f4870f756ffb8fa2f42e6e`
- `28da22a2d4369fb83b08988d05f89bfa06b7c9b1`
- `79670cf97c254e473a2fc66a625f8d74f0fa2f7c`
- `affa1cf2eb0dded269817dbe3231d3f1cc9c0661`
- `b9fcb24b3a27f7a5828a424b036fc0978009f708`
- `d6cd1067c065516a7c9829654ac65fbd19527f6d`
- `0bde823768b1847fdffe5cd9e3b0554e27c078b2`
- `8de0ed11b6e1a5acb281dc551bb4f5d7a133addb`
- `d0e81a95fc4b8d8248e7e4cd907212235693ebc6`

## Compatibility Matrix

| Scenario | Expected behavior | Required evidence |
|---|---|---|
| Infinimesh info, speech, and Telegram all disabled | Existing local chat, WebChat, default tools, and file state backend work with no new required credentials or startup errors | Config tests, Gateway tests, scoped mock integration tests, default Compose config |
| Telegram enabled, speech disabled | Telegram text and attachments remain usable; a voice message returns an explicit speech-unavailable response and creates no fabricated transcript | Telegram unit/integration test with a disabled transcriber |
| Speech enabled, Telegram disabled | Speech service can initialize and serve its own supported path; no Telegram poller, webhook, or credential requirement is activated | Speech package tests and startup test |
| Infinimesh token exhausted | Infinimesh request fails in a bounded, redacted way; local chat and Telegram continue operating | Fault-injection test for quota/auth exhaustion plus local and Telegram control requests |
| Infinimesh returns cloud 5xx | Timeout/retry behavior remains bounded and does not poison local runtime or Telegram state | 5xx stub test plus local and Telegram control requests |
| All three enabled with default `file` backend | Features initialize through one assembly path; Telegram voice uses the real `speech.Transcriber`; state persists without Postgres-only assumptions | End-to-end startup/API test using file state |
| Sensitive input traverses failure paths | Tokens, queries, and transcripts do not appear in logs, traces, status JSON, artifacts, or tracked diffs in plaintext | Canary-based redaction tests and final secret scan |
| WebChat at desktop and mobile sizes | New controls and status surfaces remain readable and do not overlap existing navigation, chat, or action controls | Build, responsive layout review, and screenshots when the in-app browser runtime is available |

Configuration must preserve independent feature switches. Enabling one feature must
not silently enable either of the others, and a disabled feature must not make its
credentials mandatory.

## Shared File Ownership

Core feature packages stay owned by their feature branches. The integration branch
may resolve shared assembly and cross-feature compatibility only.

| Shared surface | Integration responsibility | Must preserve |
|---|---|---|
| `services/gateway/internal/config/config.go` | Reconcile config fields, defaults, environment overrides, validation, and redacted public status | One switch per feature; no secret-bearing status fields; default/minimal config remains valid |
| `services/gateway/cmd/sparkclaw/main.go` | Compose lifecycles and dependencies; add the single Telegram-to-speech wiring point | Bounded shutdown; disabled features do not initialize; no duplicate transcriber construction |
| `services/gateway/internal/gateway/server.go` | Reconcile API/status exposure and shared handlers | Gateway remains authoritative; secrets, raw query text, and transcripts are not exposed by status endpoints |
| `apps/webchat/src/App.tsx` and `apps/webchat/src/api/types.ts` | Reconcile shared types, status rendering, and control placement | Existing workbench workflows, strict TypeScript build, desktop/mobile layout |
| `docker/compose.yaml` and environment examples | Reconcile optional services and environment propagation | Default file backend, mock model mode, and minimal profile work without new secrets |
| `scripts/doctor.sh` | Add capability checks that distinguish disabled, configured, reachable, and degraded states | Doctor output is redacted and does not require optional services when disabled |
| `docs/architecture.md`, `docs/development.md`, and `zh-cn/` mirrors | Document final boundaries, configuration, operations, failure isolation, and verification | Bidirectional language links and docs mirror CI |

The integration branch must not rewrite the Infinimesh client, speech engine, or
Telegram transport/handler core. Any defect isolated inside one feature branch is
returned to that branch unless a minimal shared-contract correction is required for
the three features to coexist.

## Merge Order And Gates

The order is fixed:

1. `codex/infinimesh-info`
2. `codex/voice-complete`
3. `codex/telegram-hardening`

Before each merge:

1. Confirm the branch owner has reported completion and supplied the exact commit set.
2. Confirm its worktree is clean with `git status --short --branch`.
3. Review `git log` and the full diff from the fixed base; reject unrelated refactors,
   generated output, state, traces, transcripts, observation dumps, build products,
   and dependency drift without justification.
4. Review test artifacts and rerun the branch's affected tests independently.
5. Scan the commit range for secrets and plaintext tokens, queries, or transcripts.
6. Record the pre-merge head as the rollback point.

Use a non-fast-forward merge so each feature has an explicit rollback boundary. After
each merge, immediately run the affected tests and update this table before moving on:

| Feature merge | Feature commits | Merge commit | Immediate validation | Result |
|---|---|---|---|---|
| Infinimesh info | 2 commits listed above | `1168c6503910eaeb9cfffde9ccf9b3de8982bde5` | Gateway build; config/gateway/infinimeshinfo/websearch/toolhub tests | Passed |
| Speech | 4 commits listed above | `092c1d766d4bd733761020f1467811542ebe0bd4` | Gateway build; config/gateway/speech/Infinimesh controls; 6 voice frontend tests; WebChat build | Passed after refreshing merged frontend dependencies with `npm ci` |
| Telegram | 11 commits listed above | `eac193ceb76efd4790bd71f7ece90dfecc32f067` | Gateway build; agent/binding/config/credential/gateway/notification/reminder/store/telegram/toolhub/weixin/speech/Infinimesh tests; voice tests; WebChat build | Passed |

Actual conflict decisions:

- Speech merge: `config.go` had two same-location additions. Both Infinimesh and speech
  load-time normalizers were retained without changing either core implementation.
- Telegram merge: `App.tsx` retained both microphone helpers and Telegram activation
  state preservation; `config.go` retained all three normalizers; `server.go` retained
  speech, credential-vault and binding-cancellation options; `main.go` retained both
  lifecycles. Vite `8.0.16` from the speech branch was preserved.
- No feature-core algorithm was rewritten during conflict resolution.

Integration-only commits:

- `4299c1035168fa4a4cce202b64d858f684330d09` wires the single production
  `speech.Transcriber` into Telegram.
- `bb3fdb7454d8a83b98b1edbf4cb4152507b114ae` keeps all optional features off by
  default and removes disabled Telegram startup/credential side effects.
- `e63a50df2d94a81b26025aca7e8c10fcfb5a2051` proves Infinimesh token and 5xx
  failures do not disable local chat or Telegram text handling.
- `5ed48acfbbe36cb89e35d442cd8569497826b81f` verifies all three features compose
  on the default file backend and that public readiness/config remain secret-free.
- `46dc1aabe905ca8df4e021251db6d48e49975713` updates existing Telegram tests to
  opt in explicitly after the production default changed to disabled.

## Final Validation Record

| Gate | Result |
|---|---|
| Document tool setup | Passed |
| Gateway `go build ./...`, `go vet ./...`, `go test ./...` | Passed |
| Scoped mock/integration matrix | Passed: single transcriber mapping, disabled speech response, Infinimesh failure isolation, all-enabled file assembly, all-disabled/Telegram-only/speech-only config |
| WebChat voice tests and production build | Passed: 6 tests; TypeScript and Vite build completed |
| Doctor | Passed; temporary integration servers intentionally occupied ports 18789 and 18790 during the check |
| Compose config | Passed with `docker/env/sparkclaw.example.env` |
| Docs mirror and local links | Passed for 31 project Markdown files; generated local tool paths excluded |
| WebChat layout | Production build and responsive CSS/DOM review passed. Live desktop/mobile screenshots were not captured because the bundled in-app browser plugin failed during initialization while redefining its protected `process` global. |
| Broad legacy golden eval | Not used as the final gate after scope was narrowed. Its detailed email, calendar, and knowledge cases describe features that are not concretely expanded in this integration. |

The browser-plugin initialization issue and the deferred broad legacy eval are residual
validation risks, not merge conflicts or known regressions in the three integrated
features.

## Conflict Resolution Principles

- Keep the feature branch's tested core implementation intact when conflicts are only
  caused by shared imports, constructors, config layout, or UI assembly.
- Resolve shared files against the post-merge integration state, not by choosing an
  entire side wholesale.
- Preserve independent enablement and failure isolation over convenience defaults.
- Construct the real `speech.Transcriber` once in the composition root and inject it
  into Telegram's `VoiceTranscriber` contract. Do not add a second adapter or mock in
  production wiring.
- When speech is disabled, inject an explicit unavailable implementation or absence
  state that leaves Telegram text and attachments operational and makes voice failure
  clear to the user.
- Prefer typed capability/status fields over parsing display strings. Public status
  includes availability and reason codes, never credentials, raw queries, or transcript
  content.
- If a conflict requires changing feature-core behavior, stop and return it to the
  owning branch rather than silently redesigning it during integration.

## Regression And Privacy Verification

Every integration checkpoint runs the smallest affected suite. The final checkpoint
runs all of the following from the repository root unless a directory is specified:

```bash
npm run setup:document-tools
cd services/gateway && go build ./... && go vet ./... && go test ./...
cd ../.. && npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
docker compose --env-file .env -f docker/compose.yaml config --quiet
```

Mock validation for this integration is the focused `cmd/sparkclaw` and config matrix
covering the three merged features. Detailed email, calendar, and knowledge golden
cases are deferred until those product areas have concrete expansion plans.

The final matrix additionally includes:

- explicit config/startup tests for all-disabled, Telegram-only, speech-only, and
  all-enabled/default-file-backend combinations;
- Infinimesh quota exhaustion and 5xx fault injection while local chat and Telegram
  control requests continue to pass;
- Telegram voice coverage proving the production object is a real
  `speech.Transcriber`, plus the disabled-speech unavailable response;
- canary values for credential, query, and transcript data followed by scans of logs,
  traces, state/status output, artifacts, and `git diff`;
- WebChat responsive layout review, plus live desktop/mobile screenshots when the
  bundled browser runtime is operational;
- the authoritative docs mirror/link check from `.github/workflows/ci.yml`;
- tracked-diff scans for common credential prefixes, authorization headers, private
  keys, raw transcripts, generated state, and test artifacts.

Use a temporary `.env` copied from `docker/env/sparkclaw.example.env` for Compose
validation; never add it to Git.

## Rollback Points

| Checkpoint | Rollback action |
|---|---|
| Baseline documentation commit | Reset later integration work to this commit; retain the plan and baseline evidence |
| Infinimesh merge commit | Revert that merge commit if provider isolation or local controls fail |
| Speech merge commit | Revert that merge commit if disabled startup or independent speech behavior regresses |
| Telegram merge commit | Revert that merge commit if text/attachment behavior, voice degradation, or polling lifecycle regresses |
| Shared wiring commit | Revert only the assembly commit if the feature cores pass independently but coexistence fails |
| Docs/validation commit | Revert documentation or test-record changes separately from behavior commits |

No rollback uses destructive history rewriting, and no checkpoint is pushed. A failed
gate stops the sequence; later branches are not merged on top of a known failure.

## Definition Of Done

Integration is complete only when:

- the three branches were merged in the fixed order with exact commit sets recorded;
- every pre-merge worktree was clean and every commit range was reviewed for scope,
  generated artifacts, and secrets;
- conflicts and behavior decisions are recorded in this document and the final report;
- Telegram uses the single real `speech.Transcriber` wiring, with explicit voice
  unavailability when speech is disabled and unaffected text/attachment handling;
- every compatibility-matrix scenario passes, including file-backend and failure
  isolation coverage;
- setup, Go build/vet/test, WebChat build, doctor, scoped mock integration tests,
  Compose config, docs mirror/link validation, secret scan, and responsive layout
  review pass, with unavailable live screenshot tooling recorded as residual risk;
- architecture and development documentation is updated in English and Chinese;
- each topic is committed separately, no push or `main` merge occurred, and the final
  integration worktree is clean;
- the final report lists merged commits, conflict decisions, behavior changes, full
  validation evidence, and residual risks.
