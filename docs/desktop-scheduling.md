# Desktop workbench scheduling

> Language: English | [简体中文](../zh-cn/docs/desktop-scheduling.md)

The desktop main process owns schedule definitions and occurrence records in its
private workbench SQLite database. It submits due work through the ordinary
execution client. Saving a future schedule makes no backend request and creates
no execution-service lease or timer.

The first due time must be within the next 366 days. The UI supports one occurrence
or a fixed interval of 1 hour, 24 hours or 7 days; main accepts intervals from one
minute through 366 days. Intervals measure elapsed time, so “24 hours” is not a
calendar/timezone recurrence across a daylight-saving transition. Each repetition
retains the explicitly saved input/context, receives a new immutable request ID,
and remains in the originating conversation. Other workbenches do not receive
these definitions or histories automatically.

## Showing local schedules from chat

Chat input still enters the ordinary authenticated execution service. The desktop
main process adds a digest-bound `client_presentation` capability, IANA timezone
and request timestamp to explicit chat submissions. After the existing guard and
semantic router select `schedule.manage#read`, the backend interprets the view.
Complete simple calendar phrases use a bounded date parser; other requests use
a strict structured decision. It returns a `schedule.show` client action;
it does not query the backend reminder repository or receive local schedule data.
The request and conversation IDs come from the authenticated execution binding,
not model output. The backend reply says that opening was requested, never that
local data was already displayed.

The action contains a civil-date range (inclusive start, exclusive end), timezone
and five-minute expiry. Two empty dates mean all locally visible schedules; ranges
are limited to 366 days. Ambiguous or unsupported filters require clarification.
Simple phrases such as “显示明天的日程” extract a calendar symbol only after
semantic read admission. The parser matches the complete phrase, leaving
negation, exclusions, multiple dates, time-of-day filters and analysis to the
structured decision. Backend code resolves relative symbols such as `tomorrow`
or `next_week` from the original request timestamp and timezone. The model cannot
supply literal dates alongside a relative symbol.
Explicit single dates retain their written year and gain an exclusive end in code.
Analysis, counts, summaries and modifications are not performed by this action.
Existing schedule creation, editing, cancellation and due-time behavior retain
separate paths.

The desktop atomically saves the action with the verified result and delivery
receipt. Only a request explicitly submitted in the currently active conversation
can open the existing schedule page. Main checks the original scope, conversation,
capability and action identity, claims the action once, and reads and filters its local
schedule repository. The original page, expanded schedule list, sidebar, editing,
delete confirmation, creation dialog and chat drafts remain in use. Manual sidebar
entry clears the requested date filter. No local schedule contents are uploaded.

A renderer-view token rejects stale callbacks. History loads, duplicate delivery,
conversation changes and renderer/process restart cannot repeat navigation.
An interrupted claim becomes `skipped`, preserving uncertainty about whether it
was visible. The page reports `shown` only after rendering local data (including
an empty list); a local read failure reports `failed`. These bounded receipts are
persisted locally and retried through the original execution ACK, independently
of the durable result ACK, with the same request ID. A repeated identical receipt
is accepted; a conflicting receipt is rejected. No retry resubmits the chat task.

This is a SparkClaw application-payload extension over existing execution
operations; ISCP frames, authentication, transport recovery and other projects'
contracts are unchanged. Deploy the backend and desktop as a matched release:
older clients still use the original envelope/result format, but older strict
backends do not accept the new desktop capability field.

## Due-time behavior

A running main scheduler with an authenticated usable connection establishes an
online window. An occurrence due during that continuously available window is
claimed in an exclusive SQLite transaction. The claim records submission intent
and a private one-use dispatch capability before the ordinary execution POST.
Concurrent ticks or main-process recovery cannot claim that occurrence again.
Normal timer delay within that same window remains eligible for dispatch.

Process restart, authentication generation changes, connection loss and
`powerMonitor` suspend end the window. A new window excludes all already-due
unclaimed occurrences: they become permanently `missed`. The suspend handler also
prevents a late connected-status callback from starting the scheduler before
resume. Hiding the window does not stop the main scheduler.

For recurring definitions, offline missed due times are recorded as a bounded
range: `due_at`, `interval_ms`, `missed_count` and `missed_until` identify every
skipped occurrence without allocating an offline backlog. Only the next future
occurrence remains pending. Canceling a definition cancels its future occurrences;
an admitted occurrence uses ordinary execution cancellation. “Run now as a new
request” is an explicit new execution and cannot revive or reuse a missed ID.

A crash between the durable claim and POST is conservatively uncertain after
restart. It only reconciles the recorded request ID. A lost POST response followed
by 404 likewise never permits another POST, even from the explicit submit button.
An unconsumed live dispatch capability can be marked missed when authentication
changes before dispatch. Durable results, ACK retry, approvals and cancellation
retain the ordinary execution-client rules.

## Storage and verification

ClientStore schema 13 adds only the presentation-action ledger. Supported schemas
6–12 upgrade in place; existing installation IDs, contexts, messages, drafts,
files and schedules are retained. Unsupported versions still fail explicitly.
Original request digests are never rewritten. Definitions, claims, missed ranges,
delivery receipts and presentation receipts survive normal restarts.

The initial scheduler baseline passed 29 desktop tests. Its implementation
checks passed all 116 desktop tests and the WebChat production build; regression
coverage includes two SQLite claimants, live timer jitter, restart/reconnect,
sleep/wake lifecycle, authentication changes, disk failure, recurring future
identity, crash-before-POST, lost-response/404 and cancellation of an unsent claim.
The renderer test exercises both one-time and recurring schedule submission.
These deterministic checks do not constitute native OS suspend/resume or packaged
release qualification. The matched execution-service API cutover and removal of
its retired future-lease handlers are validated with the overall convergence
release.

The presentation chain has a dedicated isolated native qualification runner:
```sh
SPARKCLAW_STREAM_FIXTURE_ISCP_SOURCE=/path/to/ISCP \
SPARKCLAW_STREAM_FIXTURE_LINUX_IMAGE=<prepared-image> \
  node apps/desktop/test/run-schedule-native-qualification.mjs
```
It runs the production Go agent in Linux, encrypted ISCP and the production Mac
Electron UI with synthetic local schedules; use Node 26, Go 1.25 and Docker.
`--real-model` additionally requires the absolute private configuration path in
`SPARKCLAW_STREAM_FIXTURE_MODEL_CONFIG`. Native evidence and source tests must
be distinguished from deployment or installation of a packaged release.

On 2026-10-30, source `95d830d1` passed the real-model native chain using work2's
configured model service: six scenario groups, four original backend executions,
two durable `shown` receipts and one `failed` receipt. It verified the original
filtered page, edit/delete/cancel/create-dialog controls, return navigation,
drafts, history/reload deduplication, collapsed-list expansion, empty results,
owner isolation and local read failure. Local schedule contents uploaded and
direct business HTTP attempts both remained zero. Work2's existing container,
start time, mount set and configuration/catalog hashes remained unchanged and
healthy. The earlier mock-model native run also passed six scenario groups.

Final checks passed 593 desktop tests (one skip), 561 WebChat tests, production
build, 5,680 Linux Go tests including subtests (203 skips), Go build/vet, and
137 Markdown sources plus their mirrors/links. Native real-model failures that
misdated tomorrow or broadened it to all dates are retained; the calendar parser
and clock-bound arithmetic address those failures. The source, hashes, exact
scope and retained diagnostics are in the [validation receipt](evidence/desktop-schedule-presentation-2026-10-30.json).
No installed app, production Gateway, shared contract or deployment was changed.
