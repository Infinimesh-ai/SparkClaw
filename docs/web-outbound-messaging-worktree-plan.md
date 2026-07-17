# Web Outbound Messaging Worktree Plan

> Language: English | [简体中文](../zh-cn/docs/web-outbound-messaging-worktree-plan.md)

## 1. Status And Approval Gate

Status: **proposal only**.

This document defines how three user-visible Codex tasks and their dedicated
worktrees will implement the approved
[Web-to-Connector Outbound Messaging Design](web-outbound-messaging-design.md).
Writing and reviewing these documents is the only authorized activity at this
stage. The owner separately authorized one WIP commit that preserves the
already-present primary-worktree state together with these planning documents;
that preservation commit does not authorize new implementation. No new task,
worktree, feature branch, merge, or push may be created until the owner
explicitly approves this plan.

After approval, exactly three visible tasks will be created. Hidden subagents
or additional implementation worktrees are not part of this plan.

## 2. Repository State Observed During Planning

At initial planning, the primary worktree was on `main` at `9e6afdb` with
extensive existing uncommitted work. The owner later authorized preserving
that exact state in one WIP commit. It must not be reset or used directly to
seed the three feature branches, and the preservation commit is not evidence
that the prior parallel results were integrated.

Three clean physical worktrees from the preceding router-first architecture
pass already exist:

| Existing worktree | Branch | Current head | Meaning |
|---|---|---|---|
| `01-workflows` | `codex/architecture-workflows` | `0a5486d` | Prior routing/workflow result |
| `02-connectors` | `codex/architecture-connectors` | `65cfb5e` | Prior message control/delivery result |
| `03-integration` | `codex/architecture-integration` | `8b65098` | Clean integration of both prior results |

These existing branches are reference inputs, not authorization to resume
work. The proposed code baseline is the clean integrated head `8b65098`, plus
one separately reviewed documentation commit containing the approved design
and this plan. The exact frozen SHA must be recorded before the three visible
tasks start.

If inspection after approval shows that `8b65098` no longer contains the
expected clean integration, execution stops and the base is re-selected in the
document. The dirty primary `main` worktree is never used as a fallback.

## 3. Visible Task Layout

The three tasks are created from the same frozen base commit.

| No. | Visible task | Proposed branch | Responsibility |
|---:|---|---|---|
| 1 | Message receive and send layer | `codex/web-outbound-message-io` | Canonical message ingress/egress, connector delivery, Gateway API, persistence, and WebChat external-send UI |
| 2 | Routing and Workflow layer | `codex/web-outbound-routing-workflow` | Router-first Agent path, WorkflowResult construction, return-route preservation, and routing/workflow invariants |
| 3 | Integration and acceptance | `codex/web-outbound-integration` | Review and merge Tasks 1 and 2, resolve shared assembly, run the compatibility matrix, and finalize bilingual docs |

Task 3 is opened at the same time so it remains visible, but after recording
the baseline it enters `waiting for inputs`. It must not merge incomplete work,
copy uncommitted files, or independently reimplement missing Task 1 or Task 2
behavior.

## 4. Task 1: Message Receive And Send Layer

### Objective

Implement the complete structured path from WebChat to a selected third-party
endpoint while preserving existing third-party inbound handling. Every
canonical part (`text`, `image`, `audio`, `file`) must be preflighted and either
delivered or rejected explicitly; no part may be inferred from Markdown or
silently dropped.

Receive and send state are separate persisted lifecycles. A binding represents
a software account; Task 1 must expose exact recipient endpoints scoped by the
current actor and must never use a binding ID as an ambiguous multi-user target.

### Primary ownership

- `services/gateway/internal/app/message_architecture.go` delivery and endpoint
  contracts;
- `services/gateway/internal/messageplane/`;
- `services/gateway/internal/messagecontrol/` and
  `services/gateway/internal/delivery/`;
- `services/gateway/internal/connector/`, `notification/`, `telegram/`, and
  `weixin/` provider delivery implementations;
- Gateway endpoint discovery, artifact resolution, direct delivery handlers,
  typed errors, idempotency, and audit surfaces;
- actor-scoped recipient endpoint catalog and deterministic exact-address
  lookup support;
- delivery persistence in `store/`, all three backends, the file `Snapshot`,
  and PostgreSQL migration coverage;
- WebChat API types/client, external-send composer mode, multi-part upload,
  separate software/recipient selection, review, receipt, and retry states;
- WebChat message/delivery history labels for receive/send direction and exact
  software/recipient/account endpoint identity;
- focused provider, Gateway, store, and frontend tests.

### Exclusions

- no semantic intent classification, WorkflowProfile design, ToolExposure
  changes, or Agent planning behavior;
- no process-level assembly in `cmd/sparkclaw` unless Task 3 requests a narrow
  handoff commit;
- no edits to Task 2-owned Agent workflow files;
- no automatic scope expansion for existing reminder-only bindings.

### Required handoff

Task 1 reports its exact commit list, frozen-base diff, API and contract
changes, store migration notes, provider fallback behavior, targeted test
results, WebChat build result, and clean `git status`. Any generated state,
uploaded fixtures, screenshots, traces, or artifacts must be removed before
handoff.

## 5. Task 2: Routing And Workflow Layer

### Objective

Tighten the middle path so ordinary Agent chat consistently follows
`MessageEnvelope -> RouteDecision -> WorkflowProfile -> WorkflowResult`, while
the owner's direct Web external send remains an explicit delivery command that
bypasses Agent routing. The two paths may share the Delivery Gateway contract,
but they must not share or fabricate semantic workflow state.

The capability tree is registration-driven. Its current-stage snapshot has
only `browser/internet_search`, `browser/automation`, `document/read`, and
`document/edit`; this list must not become a Router switch or closed enum.
Future branches register new nodes and Workflows without changing core
traversal.

External delivery remains orthogonal to the business capability tree. Task 2
normalizes a delivery directive beside the business route, delegates exact
endpoint selection to the deterministic actor-scoped resolver, clarifies
missing or ambiguous targets, and freezes one endpoint before external-send
approval. It must not add a `message.send` capability leaf.

### Primary ownership

- `services/gateway/internal/capability/`;
- generic catalog node registration, parent/edge/cycle/leaf validation, and
  dynamically supplied routing descriptions;
- `services/gateway/internal/agent/intent_router.go`;
- `services/gateway/internal/agent/workflow_*.go` and their focused tests;
- Agent-side creation and resumption of typed `WorkflowResult` values;
- return-route, owner, authorization, causation, and idempotency metadata across
  success, clarify, blocked, approval, resume, and failure states;
- structured workflow output parts and references, without parsing Assistant
  display text to discover delivery resources;
- typed target-resolution states covering Web default, source reply, missing
  software, missing recipient, ambiguity, exact match, and unavailable target;
- current four Workflow implementations: Info-only search result, tab scan then
  exact focus/open, type-aware document read, and type-aware document edit;
- strict stage-scoped Tool Exposure that replaces the prior view on transition
  and never unions tools across stages;
- reuse of the legacy conversation/context assembler, with legacy tool/Skill
  candidates ignored as visibility authority;
- regression tests proving the direct Web delivery API does not invoke the
  Agent Runtime or create a fake workflow.

### Exclusions

- no provider API, credential, polling, media upload, binding, Gateway HTTP,
  store backend, migration, or WebChat UI implementation;
- no edits to Task 1-owned delivery and message contract files after the
  frozen base; requested contract changes are sent to Task 1 as a written
  handoff;
- no new provider-name or tool-name switch, parallel capability list, or
  fallback from an authoritative matched profile into legacy TaskHint routing;
- no model-selected endpoint IDs, display-name authorization, prior-target
  guessing, or provider calls before exact target resolution and approval.
- no context-assembly rewrite, new context graph, or per-Workflow context
  builder in this pass;
- no speculative `browser.read`, navigate/click/type/select, file search,
  edit verification stage, or tool exposure beyond the current simple flows.

### Required handoff

Task 2 reports its exact commit list, frozen-base diff, routing invariants,
behavior changes, authoritative-versus-legacy boundary, focused Agent and
capability test results, full Gateway test result when practical, and clean
`git status`.

## 6. Task 3: Integration And Acceptance

### Objective

Produce one reviewable integrated branch without taking ownership of the two
feature cores. Task 3 owns merge discipline, shared assembly, compatibility
tests, final documentation, and the evidence needed to decide whether the
result may later merge into `main`.

### Primary ownership

- the integration ledger in this document after execution starts;
- `services/gateway/cmd/sparkclaw` composition changes required to expose one
  Delivery Gateway to HTTP, connectors, reminders, and workflow-result return;
- narrow shared changes in `connectorruntime`, config, policy, public status,
  or test assembly that cannot belong cleanly to one feature branch;
- cross-layer integration tests, default-file-backend verification, failure
  isolation, secret scans, and final English/Chinese architecture and
  development documentation.

### Restrictions

- Task 3 does not rewrite provider delivery, WebChat feature core, intent
  routing, or WorkflowProfile algorithms during conflict resolution;
- a defect isolated to Task 1 or Task 2 is returned to that visible task for a
  follow-up commit;
- conflict resolution keeps both tested behaviors and never accepts an entire
  side wholesale in shared files;
- no merge to `main`, push, worktree removal, or branch deletion occurs without
  a later explicit owner instruction.

## 7. Shared Contract And File Rules

1. The approved design document is the contract source of truth.
2. Task 1 owns changes to `MessageContent`, `DeliveryRequest`, capabilities,
   endpoint discovery, per-part receipts, and provider delivery interfaces.
3. Task 2 treats those contracts as frozen. It may consume existing fields but
   must request incompatible changes instead of editing the same source.
4. Task 2 owns workflow semantics and Agent result construction. Task 1 must
   not add semantic routing shortcuts to Gateway or connector adapters.
5. Task 3 owns only the composition seam after both branches provide clean
   commits. It may add adapters, not duplicate domain logic.
6. Store interface additions land together in memory, file, PostgreSQL, and
   `Snapshot`; incomplete optional type assertions are rejected.
7. Web direct send is an explicit owner action. It does not call Agent Runtime,
   create `RouteDecision`, or create `WorkflowState`.
8. Agent Workflow results and direct Web sends converge only at the typed
   Delivery Gateway and retain distinct authorization/audit provenance.
9. A third-party binding is a credential/account boundary, not a recipient.
   One endpoint must identify one exact user/chat/thread.
10. Recipient clarification and external-send approval are separate gates;
    sole-candidate resolution skips only clarification.

## 8. Merge Order And Gates

Task 3 uses this fixed order:

1. Task 1 message receive/send branch;
2. Task 2 routing/workflow branch;
3. integration-only assembly, compatibility tests, and documentation commits.

Before each merge, Task 3 must:

1. receive the branch owner's completion report and exact commit set;
2. verify the source worktree is clean;
3. review `git log` and the complete diff from the frozen base;
4. reject unrelated refactors, generated output, runtime state, credentials,
   transcripts, uploaded media, and dependency drift without justification;
5. rerun the source branch's affected tests independently;
6. record the current integration head as the rollback point;
7. merge with an explicit non-fast-forward merge commit.

Immediately after each merge, Task 3 runs the affected suites. A failed gate
stops the sequence; later commits are not merged on top of a known failure.

## 9. Compatibility Matrix

| Scenario | Required result |
|---|---|
| Existing Web Agent chat | Streaming chat, tools, approvals, and session history remain unchanged. |
| Web direct send | Calls Delivery Gateway directly and creates no Agent run, route decision, workflow state, or model call. |
| Web/Agent request without external-send intent | Returns to the current Web endpoint even when third-party history or endpoints exist. |
| Request names software and user | Resolves one authorized exact endpoint, freezes it in Message Control/ReturnRoute state, then requests send approval. |
| Agent names software but no user; one candidate | Shows the sole recipient and proceeds to send approval without a recipient question. |
| Agent names software but no user; multiple candidates | Clarifies who should receive the message and performs no provider call. |
| Same display name on multiple endpoints | Requires selection of exact account/chat; display name never authorizes delivery. |
| Capability-tree extension | A test-only branch/leaf registration routes and validates without changing Router core code or a name switch. |
| Browser Internet search | Only Info-backed `web.search` is exposed; its typed result completes the Workflow without page reading. |
| Browser automation, target tab exists | Only `browser.list_tabs` is visible first; then only `browser.focus` for the exact matched page ID. |
| Browser automation, target tab absent | Only `browser.list_tabs` is visible first; then only `browser.open` for the frozen URL. |
| Document read | Type preflight exposes no broad tool set; the read stage sees only the compatible exact-path reader. |
| Document edit | Type preflight exposes no broad tool set; the edit stage sees only compatible format/operation editors and returns the output copy. |
| Legacy context assembly | Existing history/owner/attachment context remains byte/semantics compatible while stage Exposure stays authoritative. |
| Telegram endpoint | Text, image, ordinary audio, voice-note audio, and file follow the documented native/fallback mapping. |
| Weixin endpoint | Text and image use native items; audio/voice and general files retain bytes through an explicitly disclosed file representation. |
| Third-party inbound reply | Ingress is normalized once, owner/auth/return route survive workflow execution, and the result returns to the source endpoint. |
| Cross-user endpoint attempt | Candidate is invisible or denied before artifact access and provider calls. |
| Approval and resume | Resumed WorkflowResult retains its original return route and never leaks delivery authority to another endpoint. |
| Reminder-only binding | Does not appear as a direct-send endpoint until the owner grants `message_send_self`. |
| Revoked or stale binding | Cached Web state cannot send; Gateway returns a typed binding error before provider calls. |
| Default file backend | Endpoint, scope, delivery, receipt, and idempotency state work without PostgreSQL. |
| Provider timeout or partial failure | Receipt distinguishes retryable, outcome unknown, and partial success without duplicate automatic sends. |
| All optional connectors disabled | Local Web Agent chat starts and works without connector credentials or new required configuration. |

## 10. Validation Plan

Before branching, run and record the frozen-base baseline:

```bash
npm run setup:document-tools
cd services/gateway && go build ./... && go vet ./... && go test ./...
cd ../.. && npm --workspace @sparkclaw/webchat run build
```

Task 1 runs focused tests for `app`, `messageplane`, `messagecontrol`,
`delivery`, `connector`, `notification`, `telegram`, `weixin`, `gateway`, and
`store`, plus WebChat unit tests and production build.

Task 2 runs focused tests for `capability` and `agent`, followed by
`go test ./services/gateway/...` when its branch is stable.

Task 3 runs after each merge and at final close:

```bash
npm run setup:document-tools
cd services/gateway && go build ./... && go vet ./... && go test ./...
cd ../.. && npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
bash scripts/run-eval.sh
```

Final verification also includes default Compose config, docs mirror/link
validation, secret and generated-artifact scans, and desktop/mobile WebChat
screenshots when browser tooling is available.

## 11. Execution Ledger

Execution was approved and integrated on 2026-07-17. Task 3 independently
verified each source commit graph, diff ownership, worktree cleanliness,
generated-artifact state, secret scan, and validation evidence before merging.
The approved design semantics above remain unchanged.

| Item | Value |
|---|---|
| Approved by owner | Approved for the three visible worktrees on 2026-07-17 |
| Primary WIP preservation | Separately authorized; not the frozen feature base |
| Frozen base | `codex/web-outbound-parallel-base` at `f9b6e804c1caec067d58bb81ade4d793c8e116aa` |
| Task 1 source task | `threadId=019f6e2c-ad06-7713-851f-cb5448a49acf`, `hostId=local`; branch `codex/web-outbound-message-io`; clean HEAD `497435a9d854376fd80841fe0b5ce9becc353a2d` |
| Task 1 commit set | `60c57d54cf318bae4b39ad23a91d7ea1aecec3ca`, `2dfacb30c4bad7747dda1582b2e1e948b12258b3`, `ccd13c3c83a05c69805e42910d07f8b53c52f56b`, `72c7454f37f0181676725149b862fa0d7a873900`, `8208ef2606677a032e2e68850e4b42c862312352`, `d8648288762ec8507b4599635d8ee7d16cbec884`, `497435a9d854376fd80841fe0b5ce9becc353a2d` |
| Task 1 source validation | Gateway build/vet/test and affected uncached packages passed; WebChat direct `tsc -b`, 11 Vitest tests, Vite build, doctor, isolated 43/43 eval, desktop 1440x900 and mobile 390x844 visual checks passed; source worktree was clean with only dependency caches ignored |
| Task 2 source task | `threadId=019f6e2c-ad05-76e3-94de-f9c21dcc760b`, `hostId=local`; branch `codex/web-outbound-task2-routing-workflows`; clean HEAD `802370b4808ecafe1d0c60975825b3c7f97097b4` |
| Task 2 commit set | `ae1678b47c13da44c6b1b95fea88b31d43305fbe`, `f86129c04e251ebda12a03e3ecbb3428e902b5e3`, `6b2eeaf95bfaa445a4a5de8486fdf3c7d9803c27`, `00e7c5deed99190213f13f9b7d856257e7d86695`, `802370b4808ecafe1d0c60975825b3c7f97097b4` |
| Task 2 source validation | Affected and full uncached Go build/vet/test passed; doctor passed with only occupied-port warnings; isolated 43/43 eval passed; typed-directive grounding, send approval/rejection/replay, dynamic registration, stage exposure, and legacy-context tests passed; source worktree was clean with only dependency caches ignored |
| Task 1 merge commit | `accc5eaf7e632278b73605ad24e8ead634d47df7` (`--no-ff`; parents `e687b87be088a47e2052e2e0ffdc3c1e11b67163` and Task 1 HEAD) |
| Task 1 post-merge validation | Go build/vet plus eight affected uncached packages passed; WebChat direct `tsc -b`, 11 Vitest tests, and Vite production build passed; merge diff and secret/artifact gates passed |
| Task 2 merge commit | `93ff4ca7e03d90f190c325717e0e77dcbfac601b` (`--no-ff`; parents Task 1 merge and Task 2 HEAD) |
| Task 2 conflict and validation | One `web_workflow_test.go` conflict retained Task 2's typed document result and mechanically adapted Task 1's unified Delivery Gateway assertion; build/vet and agent/capability/toolhub/modelrouter/gateway uncached tests passed |
| Integration commit | `ac98afcd1cb066b32dd8a934583583a1b30e034a`; canonical `app.DeliveryTargetSelection`, typed Router-to-resolver adapter, shared runtime assembly, and Gateway delivery wiring |
| Final validation | Gateway `go build ./...`, `go vet ./...`, and `go test ./... -count=1` passed; WebChat direct Node 24 `tsc -b`, 11 Vitest tests, and Vite production build passed; doctor passed; isolated port 18889 mock eval passed 43/43 plus extended checks |
| Documentation, Compose, and security | English/Chinese mirror and local-link validation passed; default Compose rendered with `docker/env/sparkclaw.example.env`; `git diff --check`, commit-trailer, secret, generated-artifact, and listener cleanup gates passed |
| Residual risks | `SPARKCLAW_TEST_POSTGRES_DSN` was absent, so live PostgreSQL integration tests were skipped while PostgreSQL code compiled and memory/file backends passed; no real Telegram/Weixin credentials were used; desktop npm wrapper commands were avoided because of the known SIGKILL issue and equivalent direct Node stages passed; ports 18789/18790 belonged to existing processes, so eval used isolated 18889 |
