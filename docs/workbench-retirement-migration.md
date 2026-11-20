# Workbench Retirement Migration

> Language: English | [简体中文](../zh-cn/docs/workbench-retirement-migration.md)

Date: 2026-11-20. M0–M5 are implemented. Section 9 records verification. The original code survey used `6b26eb2e`.

This document plans the removal of the former workbench transport layer that still sits between the link layer and the business code. It follows [Client link layer design](client-link-layer-design.md), whose section 9 records that business domains run through a bridge and were not rewritten. The link contract is in [Link contract](link-contract.md).

## 0. Premises

1. The product is still in development and not in use. There are no installed clients to keep compatible and no conversation data to protect.
2. The three clients and the backend are built and installed together, so a contract change lands on all of them in one step.
3. The host WebChat stays. It keeps its HTTP interface, so the HTTP handlers of the business domains stay as well.
4. The UI change rules of this repository apply: pages, menus, layout and interactions do not change. Only the data path under existing components changes.
5. Control of the SparkX built-in browser keeps its behavior. Only how it is authorized and carried changes.

## 1. Where the code stands

### 1.1 The path of one business call today

A desktop business call, for example reading the mailbox list, passes through these steps:

1. The renderer issues an HTTP-shaped request.
2. The main process's former ISCP request pipeline maps the route to a v2 operation name (`iscp-routes.mjs`, `iscp-transport.mjs`).
3. The pipeline writes to `LinkDomainWire`, an object that imitates the former helper child process (`link-domain-wire.mjs`).
4. `LinkDomainWire` sends one link `domain.call`.
5. The Gateway builds an `iscpworkbench.Request` and a fabricated `iscpworkbench.SessionInfo` with a synthetic negotiation result (`link_domain_bridge.go`).
6. The former domain adapter runs (`workbench_iscp_domain.go`). For 92 operations it builds an `http.Request` and calls the HTTP handler.
7. The result returns to the device as an HTTP status and body.

Steps 2, 3, 5 and 6 exist only so that former workbench code runs unchanged.

### 1.2 Inventory

| Part | Location | Size (non-test) |
|---|---|---|
| Gateway domain adapters | `services/gateway/internal/gateway/workbench_iscp*.go`, 28 files | about 4,100 lines |
| Their tests | `workbench_iscp*_test.go`, 22 files | about 4,200 lines |
| Operation table and former transport package | `services/gateway/internal/iscpworkbench` | about 4,900 lines |
| Link bridge | `link_domain_bridge.go` | 216 lines |
| Desktop request pipeline | `apps/desktop/src/main/iscp-*.mjs`, 16 files | about 2,000 lines |
| Desktop former launcher path | `desktop-auth.mjs`, `execution-client.mjs` | about 1,200 lines |
| WebChat transport branches | `apps/webchat/src/desktop/LocalWorkbench.tsx` and 11 other files | 13 `iscp` conditionals in `LocalWorkbench.tsx` |
| Former helper binary | `services/gateway/cmd/iscp-workbench` | used only by the qualification launcher |

The operation table holds 159 operations:

| Group | Operations | State over the link |
|---|---|---|
| `mail` | 43 (40 HTTP-shaped) | Bridged |
| `settings` | 32 (all HTTP-shaped) | Bridged |
| `medicine` | 9 (all HTTP-shaped) | Bridged |
| `speech` | 8 | Bridged |
| `notifications` | 3 (all HTTP-shaped) | Bridged |
| `tools` | 2 | Bridged, and always refused (1.3) |
| `browser` | 7 | 2 forward bridged; 5 reverse carried by link reverse calls |
| `mcd` | 9 | 3 forward bridged; 6 reverse carried by link reverse calls |
| `events` | 6 | Bridged; duplicates link events |
| `object`, `transfer` | 8 | Bridged; duplicates link `object.*` |
| `operations` | 1 | Bridged; duplicates link receipts |
| `presentation` | 3 | Registered as direct domain methods already |
| `execution`, `approvals`, `phone`, `state`, `capabilities`, `workbench`, `installation`, `audio`, `clients`, `authorizations` | 28 | Excluded by the bridge; replaced by core link operations or the console |

### 1.3 Defects the bridge causes

These were found by reading code on 2026-11-20. They were not reproduced on a running system.

- **Authorization is read from an ISCP session that no longer exists.** `workbenchExecutionAuthorization` returns an empty authorization when the session has no `CheckAuthorization` (`execution_authorization.go:31`). The bridge's fabricated session has none. `tools.list` therefore returns an empty list and `tools.invoke` returns 403.
- **Runs started with `message.send` carry no execution authorization.** `executionMCDSelection` requires one (`execution_mcd.go:13`), so the McDonald's ordering resources are never attached to a link run. The feature is disabled by default, which hides this.
- **Two event mechanisms run side by side.** The desktop still polls `events.pull`, `events.ack` and `events.snapshot` through `domain.call` (`iscp-event-client.mjs`) while the link layer pushes its own sync and approval events.
- **Two large-object mechanisms and two receipt journals coexist**: the former `object.*`/`transfer.*` and domain receipt journal, and the link `object.*` and `receipts.json`.
- **A 64 KiB response limit** applies to domain calls on the desktop (`link-domain-wire.mjs:8`), although a link frame allows 600 KiB.
- **The v2 envelope is not covered by the conformance vectors.** A second client language that needs a business domain must reimplement `params`, `body`, `expected_revision` and `input_digest` from the Gateway source.

## 2. Target

```
 Device ──(LAN / Relay)──▶ connector ──▶ link entry ──▶ domain method ──▶ business function
                                                                                ▲
 Host WebChat ──(HTTP)──▶ HTTP handler ─────────────────────────────────────────┘
```

- A business domain has one set of business functions. The HTTP handler and the link domain method are two thin entries that parse input and call the same function.
- A link domain method takes typed arguments and returns a typed result. Failures use link error codes. No HTTP status travels to a device.
- Authorization has one source: the Principal and its device conditions.
- Events, large objects and idempotency receipts each have one mechanism, the link layer's.
- The desktop main process calls `link.request` directly. No ISCP pipeline and no imitated helper remain.
- The phone and the watch do not use the business domains yet (decision 7.3). Typed domain methods let them join later by adding vectors and a device projection for the domain they need, without reworking the Gateway.
- `internal/gateway` imports no `iscp*` package for the client path. The packages that serve JingSi and LocalMind (`iscpbridge` and what it needs) are outside this plan.

## 3. Rules during the migration

1. **No new entry in the operation table.** From M0 on, a new business operation is registered with `registerLinkDomain` and documented in the link contract.
2. **One domain at a time.** A domain is moved completely: Gateway methods, desktop calls, tests, contract text and vectors. Its adapter file and route mappings are deleted in the same change.
3. **Domains not yet moved keep using the bridge.** The migration can pause after any phase without leaving a half-moved domain.
4. **Tests are rewritten, not dropped.** Each `workbench_iscp_<domain>_test.go` case is carried to a test of the domain method or of the business function before the file is deleted.
5. **The interface is verified against the original interactions**, not only against the new function tests.
6. **Clients and backend move together.** Each phase ends with a paired build: Gateway image, connector, SparkX, and the phone and watch builds when the contract changed for them.

## 4. Phases

| Phase | Content | Deleted at the end |
|---|---|---|
| M0 | Fix confirmed link-layer defects; freeze the operation table; delete the `tools` operations | `tools.list`, `tools.invoke`, `workbench_iscp_tools.go` |
| M1 | Authorization from the Principal | The fabricated session's role in authorization |
| M2 | One mechanism each for events, large objects and receipts | `events.*`, `object.*`, `transfer.*`, `operations.receipt`; the desktop event, object and journal clients |
| M3 | Desktop main process calls the link directly; the qualification launcher moves onto the link layer | `LinkDomainWire`, `iscp-transport.mjs`, `iscp-routes.mjs`, `iscp-capabilities.mjs`; the WebChat `iscp` branches; `DesktopAuth`, `execution-client.mjs`, `cmd/iscp-workbench` and the `iscpnode*` client packages |
| M4 | Domains move one by one | One `workbench_iscp_<domain>.go` and its tests per domain |
| M5 | Remove the remainder | `link_domain_bridge.go`, the operation table, the client part of `iscpworkbench` |

### M0. Link-layer defects first

Moving domains onto a defective base makes failures hard to attribute. These are fixed before anything moves:

| Defect | Location |
|---|---|
| Run events are sent from one goroutine each and carry no sequence number, so deltas can arrive out of order | `link_runs.go:398-400` |
| The desktop does not resubscribe when the path switches between LAN and ISCP | `link-conversations.mjs:77`, `link-auth.mjs:163` |
| The retry of an unanswered send omits attachments | `link-conversations.mjs:392` |
| "Sent or not" is decided by matching Go error text | `link-conversations.mjs:344`, `link-domain-wire.mjs:44` |
| Reverse calls do not prefer the LAN connection | `link_reverse.go:27-31` |
| Run state covers only runs started through the link | `link_runs.go:261` |
| A full request window blocks the read loop, including cancels and reverse replies | `internal/link/service.go:397` |

`tools.list` and `tools.invoke` are deleted in this phase (decision 7.2): the operation-table entries, `workbench_iscp_tools.go` and the desktop copy of the entries. They have no caller. A direct tool call can be added later as a domain method.

Exit check: an automated test switches paths during a run and the stream continues; a test sends bursts of deltas and receives them in order; `domain.call` for `tools.*` answers `unsupported_capability`.

### M1. Authorization from the Principal

- Replace `workbenchExecutionAuthorization(session)` with a function of the link Principal and its device conditions.
- Attach it to runs started by `message.send` and to domain methods.
- McDonald's and browser permissions require the browser surface and the device's declared reverse operations, not a negotiation list.

Exit check: a run started with `message.send` has the full tool set; with McDonald's selection enabled in a test configuration, a link run receives the ordering resources.

### M2. One mechanism each

| Former | Replacement |
|---|---|
| `events.pull`, `events.ack`, `events.snapshot`, `events.subscribe`, `events.resume`, `events.unsubscribe` | Link events as accelerators plus a per-domain cursor read. Mail, notification and medicine invalidations become link topics |
| `object.*`, `transfer.*` | Link `object.upload.*`, `object.describe`, `object.read` |
| `operations.receipt` and the domain receipt journal | Link receipts keyed by device, method and idempotency key |

The domain receipt journal carries the "outcome unknown" semantics that mail sending and mail intake depend on. The link receipt must offer the same three answers before the journal is removed: completed with the original result, input conflict, and outcome unknown.

Exit check: the desktop makes no `events.*`, `object.*`, `transfer.*` or `operations.*` call; a mail send whose reply is lost is reconciled to its original outcome.

### M3. Desktop calls the link directly

- The main process's request layer calls `link.request("domain.call", …)` without the ISCP pipeline and without `LinkDomainWire`.
- The 64 KiB limit goes away; the link frame limit applies.
- WebChat components keep their props and behavior. `operationBackend` and the `iscp` conditionals in `LocalWorkbench.tsx` collapse to one path.
- Notifications stop polling and follow link events. `activityUnavailable` is no longer set for the link.

**Qualification launcher (decision 7.1).** `npm run qualify` starts Electron with `--qualification`, which today selects `DesktopAuth` and the former `iscp-workbench` helper, a path ordinary launches no longer use.

- The fixture backend becomes a Gateway with the LAN connector and a local device directory; no console and no Relay are needed.
- The launcher joins with `LinkAuth` in its isolated profile, so the browser-control, native-input and crash checks run over the link layer.
- `DesktopAuth`, `execution-client.mjs`, `cmd/iscp-workbench` and the `iscpnodeclient`, `iscpnodeissuer` and `iscpnodeenroll` packages are deleted once the run passes.
- How much of the 812-line script depends on the former connection has not been measured; it is assessed at the start of M3.

Exit check: every desktop screen behaves as before on LAN and on ISCP; the packaged app contains no `iscp-transport.mjs`; `npm run qualify` passes on the link layer.

### M4. Domains

Order, from small to large. Each row is one change.

| Order | Domain | Operations | Gateway files removed | Notes |
|---|---|---|---|---|
| 1 | `medicine` | 9 | `workbench_iscp_medicine.go` | Smallest HTTP-shaped domain; establishes the pattern |
| 2 | `notifications` | 3 | `workbench_iscp_notifications.go` | |
| 3 | `speech` | 8 | `workbench_iscp_speech.go`, `workbench_iscp_speech_recovery.go` | Requires the microphone condition; uses streamed frames |
| 4 | `browser`, `mcd` forward operations | 5 | `workbench_iscp_browser.go`, `workbench_iscp_mcd.go`, `workbench_iscp_mcd_host.go` | Reverse operations are already link reverse calls |
| 5 | `settings` | 32 | `workbench_iscp_settings.go`, `workbench_iscp_connections.go`, `workbench_iscp_mcp_settings.go` | |
| 6 | `mail` | 43 | `workbench_iscp_mail*.go`, `workbench_iscp_intake_recovery.go` | Largest; real-mailbox verification required |

For each domain:

1. Factor the business function out of the HTTP handler where the handler still holds logic.
2. Register typed domain methods with `registerLinkDomain`.
3. Write the methods, arguments, results and errors into the link contract, with the vectors the desktop needs.
4. Point the desktop calls at the new methods.
5. Carry the tests over, then delete the adapter file, its tests and the route mappings.

`browser` and `mcd` forward operations require the browser surface and stay desktop-only (premise 5 of the link layer design).

**When the phone or the watch needs a domain later**, that domain gets full conformance vectors (success, each failure code, idempotent replay for mutations) and a device projection (what the phone receives; trimmed plain text, fewer items and file names only for the watch). This is separate work and not part of this migration.

### M5. Remainder

- Delete `link_domain_bridge.go`, `workbench_iscp_domain.go`, the receipt files, `operations.json` and its generated code, and the desktop copy `apps/desktop/src/shared/iscp-operations.json`.
- Reduce `iscpworkbench` to what other entries still import, or delete it.
- Update section 9 of the link layer design and this document's status.

Exit check: `grep` finds no `iscpworkbench` import under `services/gateway/internal/gateway` or `apps/desktop/src`.

## 5. Verification

| Level | What runs |
|---|---|
| Gateway | `go build`, `go vet`, focused race tests; link tests on LAN and, with the Relay test environment, on ISCP |
| Link module | `go test ./...` in `services/link`; new tests for `linkclient` path switching |
| Desktop | The desktop and WebChat test suites; a manual pass over every original screen on both paths |
| Real services | Mail: send, draft, attachment and intake against a real mailbox on work2. Speech: one live transcription |
| Phone and watch | Rebuild and run a conversation and an approval after any contract change that reaches them |

Known local test failures that predate this work are listed in the SparkClaw SOP and are not regressions.

## 6. Risks and costs

- **Test protection thins while tests are rewritten.** Rule 4 limits this to one domain at a time.
- **Mail cannot be proven by unit tests alone.** Its idempotency and unknown-outcome behavior need a real mailbox.
- **The volume is real**: about 4,100 lines of adapters, 4,900 lines of the former package, 3,200 lines on the desktop, and as many lines of tests.
- **A paired build is needed at every phase**, including the Keychain prompt that each unsigned SparkX rebuild triggers.

## 7. Decisions

The user decided these on 2026-11-20.

1. **The qualification launcher moves onto the link layer.** It is done in M3.
2. **`tools.list` and `tools.invoke` are deleted** in M0. They are added again as domain methods if a need appears.
3. **The phone and the watch do not join the business domains for now.** They join when later development needs a domain. M4 serves the desktop only and keeps the methods typed so that a later join is additive.

## 8. Out of scope

- The Bridge `agent.*.v1` entry used by JingSi and LocalMind.
- The host WebChat's HTTP interface.
- The self-hosted console and the console adapter.
- Any change to pages, menus, layout or interactions.

## 9. Implementation (2026-11-20)

All six phases are implemented.

| Phase | Result |
|---|---|
| M0 | Run events carry a per-conversation `seq` and are sent in order by one sender per subscription; the desktop resubscribes on a path switch, resyncs on a gap and retries a send with its attachments and `replay_only`; `not_sent` replaces error-text matching; reverse calls prefer the LAN connection; `run.status` answers from stored runs; a full request window no longer blocks the read loop (excess requests answer `busy` with `not_sent`). `tools.*` is deleted |
| M1 | `linkExecutionAuthorization` derives authorization from the Principal and its conditions; `message.send` runs get the McDonald's selection when it is enabled |
| M2 | Business invalidations are link `domain` events; large inputs and outputs are link domain objects (`purpose`); domain mutations use link receipts with a fence and the three answers (original result, conflict, `outcome_unknown`) plus `replay_only` |
| M3 | The desktop calls `domain.call` through `LinkDomainClient`; the ISCP pipeline, `LinkDomainWire`, `DesktopAuth`, `execution-client.mjs`, `cmd/iscp-workbench` and the `iscpnode*` packages are deleted. The qualification launcher starts `link-qualification-backend` |
| M4 | Medicine, notifications, speech, browser/McDonald's, settings and mail are typed domain methods (link contract section 4). The settings binding, browser-extension, MCP and pairing handlers and the mail error writers were factored into business functions returning a `domainReply`, shared by the HTTP handler and the link method; the mail methods call the mail services directly |
| M5 | `link_domain_bridge.go`, every `workbench_iscp*.go`, the domain receipt journal and its sweepers, `operations.json` with its generator and desktop copy, and the `iscpworkbench` and `iscpobjects` packages are deleted. No file under `services/gateway` or `apps/desktop/src` imports `iscpworkbench` |

**Differences from this plan**

- **Settings and mail answer the domain result `{status, body}`**, the status and body the host WebChat gives for the same call, instead of typed values with link errors. Their screens branch on HTTP statuses and bodies, and the phone and the watch do not use these domains yet (decision 7.3). When one of them joins, its methods get a typed projection.
- **Typed arguments ignore unknown fields**, as every link operation does, where the former adapters rejected them. The owner always comes from the Principal, so an extra field cannot widen a call. One closed input remains: a receiving change (`mail.intake.update`) refuses sending-provider fields.
- **`mail.intake.reconcile` takes `{idempotency_key, original}`**, the original change's own arguments. It waits for an attempt still in flight instead of racing it, so a late original can no longer overwrite a settled answer.
- **Link receipts are bounded by count** (4096 entries, oldest first) instead of the former journal's time-based retention and sweeper.
- **The LAN mail-object routes `/api/v1/mail/attachments*` are deleted** with `iscpobjects`. Only the deleted LAN desktop client used them; the host WebChat never reports workspace attachments, since it has no device files.
- **`activityUnavailable` stays** in WebChat: the activity data is not reachable over the link, and the original entry is kept disabled (UI rules).
- **The retired harnesses**: `cmd/iscp-workbench`, the ISCP lab scripts under `scripts/iscp-*` and `scripts/lib/iscp-*`, `qualify:desktop-mac-client`, and the desktop's former ISCP and LAN client tests.

**Verification**

| Level | Result |
|---|---|
| Gateway | Build, vet and the full Go suite pass with pypdfium2 5.15.2. The three installed-executor success cases also passed on Linux arm64 with real tmpfs. macOS verifies rejection of a disk-backed workspace. |
| Link module | `go test ./...` in `services/link` passes |
| Desktop | The desktop suite and opt-in `link-backend-integration.test.mjs` pass. The integration run verified settings revisions, domain events, an ordered run and `replay_only` through a real Gateway and LAN connector. |
| WebChat | 622 pass |
| Release acceptance | The work2 mailbox, live transcription, Linux qualification, paired rollout and original screens are checked against the installed versions. |
