# Mail Send Latency: Resident Page Transport and Login-Check Reuse

> Language: English | [简体中文](../zh-cn/docs/email-send-latency.md)

- Status: **deployed with release 35 on 2026-10-22; the resident page transport is switched off on work2 after it exposed a QQ page-readiness race, and login-check reuse did not take effect live (see [section 9.10](email-network-native-architecture.md))**
- Date: 2026-10-22
- Baseline: SparkClaw `0560d221`, App-CLI `e147d45` (release 34), Tampermonkey 5.5.0.17
- Context: [network-native mail architecture](email-network-native-architecture.md),
  sections 9.4–9.6

## 1. What costs time today

A routine send takes about 83 s at the API (work2, release 34, DOM path). Two
costs are independent of the provider page:

| Cost | Where | Size on work2 |
|---|---|---|
| One `playwright-cli.js` process per page command | `tools/browser-controller/src/host-page.mjs` | 70–85 commands per send, about 0.42 s process start each |
| A full login check before every send | Gateway `emailautomation.Controller.Admit` | executor `probe` task 1.85–2.0 s (API about 2.7 s) |

Each App-CLI page call (`click`, `fill`, `waitFor`, ...) is guarded by HostPage
with a `tab-list` topology check and, around effects, a `location.href` read.
Every one of those commands used to be its own process.

## 2. Changes

### 2.1 Resident page transport (Browser Controller)

`cli-page-transport.mjs` is one process per task page. It proves daemon
ownership exactly as `cli-read-batch.mjs` does (metadata, `/proc/<pid>/cmdline`,
pinned Playwright version, registry entry, socket owner), then serves requests
over stdin/stdout:

- `{"args": [...]}` runs one CLI command with the same parser, flag and
  argument validation, messages and exit codes as `playwright-cli.js`, and
  answers with the exit code, stdout and stderr that process would have
  produced;
- `{"batch": {...}}` runs the existing guarded read batch.

HostPage's control flow is unchanged: every guard, retry, effect marker and
error mapping stays in HostPage, and each command's result goes through the
same `settleProcessOutput` (forbidden output, exit classification, output
transform) as a spawned process. Only the process boundary moves.

| Property | Behavior |
|---|---|
| Scope | Linux, the pinned CLI entry point, no Electron adapter; pages with activity `mutation` (send, mark-read) by default. `SPARKCLAW_BROWSER_PAGE_TRANSPORT=all` adds Reader/watch pages, `off` restores per-command processes. |
| Lifecycle commands | `attach` and `close` keep their own processes. |
| Ordering | One request at a time; a concurrent command falls back to its own process. |
| Deadline / abort | The parent kills the helper (`browser_script_timeout` / `browser_extension_unavailable`, as for a killed CLI process). |
| Failure after a command was written | Final, never retried; the helper is discarded and the next command starts a new one. |
| Failure before anything was written | Startup or ownership proof failed: the command runs through its ordinary process and the page stops using the helper (`application_page_transport_unavailable`). |
| Idle | The helper exits after 30 s without requests and is restarted on demand. |
| Accounting | When the page closes the Controller logs `application_page_processes` with `cli_processes`, `transport_processes`, `transport_commands`, `transport_fallbacks` and `lifetime_ms`. |

Send lifecycle semantics do not change: the effect marker (`beforeEffect`,
`effectAttempted`) is set before the effect command is written, exactly as
before. A failure before the marker still means "not sent"; a failure at or
after it still means "may have been sent". App-CLI's dispatch release, Sent
confirmation and unknown classification are untouched.

### 2.2 Login-check reuse (Gateway)

`Admit` reuses the result of a full login check made by this Gateway within a
window, configured by
`adapters.browserAutomation.playwrightExtension.mailSendLoginReuseSeconds` or
`SPARKCLAW_MAIL_SEND_LOGIN_REUSE_SECONDS` (default 120, range 0–600, 0 always
checks). Only a check that `Admit` or `Check` ran and persisted as ready seeds
it; the window starts at that check and is never extended by reuse.

A cached check is dropped, and the next send checks again, on any of:

- setting change (version, account, enabled, state, error code), including
  opening the login page;
- probe script revision change;
- browser credential generation change;
- the provider's mailbox observer state: new registration or watch epoch, a
  resync for a replaced document or registration, a watch-state change, a
  mailbox error, an unreadable state, and never while it reports
  `login_required`;
- a failed login check (Check, Admit or intake) or a send that did not end
  `sent` (including not dispatched and unknown).

The send script still proves the exact account on the page before any effect;
`SendForOwner` still re-checks the setting version and credential generation.

## 3. Expected savings (estimates, not measurements)

| Item | Before | After | Saving per send |
|---|---|---|---|
| CLI processes per send page | 70–85 page commands + attach + close | 1 helper + attach + close | about 29–35 s on work2 (0.42 s × commands) |
| Local fixture, 68-command send-shaped page (Linux container) | 11.4 s, 68 processes | 0.33 s, 1 process | 11 s on that machine |
| Login check | every send | first send in each 120 s window | 1.9–2.7 s for later sends |

A DOM send (83.8 s at the API) is expected to fall to roughly 45–55 s; a native
replay (23.9 s) by the share of its page commands, perhaps 5–10 s. Provider
page waits, navigation and proof polling are unchanged. These figures must be
replaced by the measurement below.

## 4. Measuring before and after on work2

The deployment owner runs this after the paired release. It needs no code
changes, debug ports or extra listeners; every source is read-only.

1. **Configuration.** Record the deployed source and release. Confirm the
   Controller unit does not set `SPARKCLAW_BROWSER_PAGE_TRANSPORT` (or sets
   `mutation`) and the Gateway's effective `mailSendLoginReuseSeconds`.
2. **Baseline from history (before).** Without sending anything, read the
   executor ledger read-only
   (`~/.local/share/sparkclaw/app-cli/state/executor/ledger.sqlite`, opened
   with `?mode=ro`) and list `command`, `status`, `created_ms`, `finished_ms`
   for `send` and `probe` tasks. Snapshot on 2026-10-22 (release 33/34):
   DOM sends 61.6–80.4 s, a native replay 21.0 s, each preceded within about
   3 s by a 1.85–2.0 s `probe`. API wall times from the release 34
   qualification: 83.823 s (DOM) and 23.874 s (replay).
3. **Approved test sends (after).** With the owner's approval and only brief
   new messages (never a historical unknown), send per provider:
   two messages less than 120 s apart, then one more after the window. Time
   each request at the client from HTTP request start to response, as the
   release 34 qualification did.
4. **Process count.** Read
   `journalctl --user -u sparkclaw-browser-controller.service --since <start> -o cat`
   and select `application_page_processes`. For a send page expect
   `transport_processes` 1, `cli_processes` 2 (attach and close),
   `transport_fallbacks` 0 and `transport_commands` near 70–85. Confirm there
   is no `application_page_transport_unavailable`. A warm QQ mutation page
   reports once, when it is finally closed, with totals for all its sends.
5. **Login reuse.** In the ledger, the first send of step 3 has a `probe`
   immediately before it; the second has none; the send after the window has
   one again.
6. **Safety checks.** Every test send ends `sent` with its normal proof; the
   number of unknown executor rows is unchanged; no message is sent twice.
7. **Compare.** Report API wall time, ledger `send` duration and `probe`
   presence before/after per provider and path (DOM, native replay).
8. **Rollback without a code change.** `SPARKCLAW_BROWSER_PAGE_TRANSPORT=off`
   on the Controller and `SPARKCLAW_MAIL_SEND_LOGIN_REUSE_SECONDS=0` on the
   Gateway restore the previous behavior after a restart.

## 5. Validation

- Controller (Linux container, Node 26): send-shaped page A/B against the
  pinned CLI Session protocol fixture; identical daemon command logs, results
  and effect markers; one helper instead of 68 processes; eight failure
  positions (before/at/after the effect, destroyed context, forbidden output,
  syntax error, closed page) stop both paths at the same call with the same
  code, reason and command; timeout/abort never replays a command; failed
  ownership proof falls back before sending; read batches share the helper.
- Gateway: consecutive sends share one check; the window never slides;
  every invalidation signal above forces a full check; a failed check is
  never cached; `Check` seeds reuse.
- No live send, deployment or release change was made.
