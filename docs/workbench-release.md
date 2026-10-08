# Workbench release cutover

> Language: English | [简体中文](../zh-cn/docs/workbench-release.md)

This is the source release's clean-start contract for the backend, host WebChat and
desktop workbench. Ship all three from the same tested revision. This document and
the implementation do not deploy a service, replace an installed application, reset
a profile or delete real data. An actual cutover is a separate operational action.

## Storage selection

Select a fresh deployment identity, backend business Store and file/workspace
storage for this release. Select a fresh, explicit `state.path` (`State.Path` in Go)
as its durable control-storage anchor. Keep these locations stable after the
cutover; an ordinary restart reopens them.

| Data | Location and authority |
|---|---|
| Host WebChat conversations, drafts, history and schedule definitions/occurrences | The selected backend Store; use File or PostgreSQL for restart persistence |
| Execution admission, request identity and delivery control | `State.Path + ".execution"` |
| Mail synchronization control | `State.Path + ".mailsync"`; mailbox content remains backend-authoritative |
| Desktop conversations, drafts, tasks, files and schedules | `<userData>/workbench` on that installation |
| Desktop mail cache | `<userData>/workbench/mail`; a local projection of the backend mailbox |
| Desktop selected backend and encrypted credential | `<userData>/backend.json` and the secure credential vault under that same profile |

PostgreSQL does not replace the filesystem control anchor. Configure `state.path`
explicitly even with `state.backend = "postgres"`, give it a fresh absolute path
whose parent is writable by the service, and persist its `.execution` and
`.mailsync` directories along with the selected database and authorized file
storage. Changing only the PostgreSQL DSN is not a complete fresh cutover. Memory
Store remains a transient test/runtime option and does not promise restart history.

For every desktop installation, set `SPARKCLAW_DESKTOP_USER_DATA_DIR` to a fresh
absolute directory before the first launch of the matched build. Reuse that exact
directory for subsequent launches. A relative value is rejected. Configure the
new backend and enroll again through the current credential flow; a fresh profile
receives its own installation identity. Ordinary startup loads only its selected
descriptor and secure vault. It does not discover old launcher paths or import a
plaintext credential. Explicitly selected loopback descriptors remain supported
where that platform permits them; fixture path injection is qualification-only.

There is no old-schema migration, legacy-data export/import pipeline, path alias, old-device
alias or automatic loading of old histories into the new workbench. Unsupported
desktop schemas fail explicitly instead of being upgraded or erased. Do not reuse
old business storage as a migration shortcut. Before an actual cutover, stop or
drain old execution and fence old writers within the separately authorized scope.
Preserve mail-service directories, deployment secrets and externally required
deduplication ledgers according to their own contracts; a fresh business Store is
not permission to clear them.

## Matched API surface

The installed desktop and backend use `/api/v1/installations`,
`/api/v1/executions`, `/api/v1/inputs`, `/api/v1/mail` and
`/api/v1/browser/hosts`. Execution/input digests use `X-SparkClaw-Digest`.
Do not mix this backend with a desktop built for the retired product routes or
headers; the release provides no compatibility aliases.

Host WebChat sends through `POST /api/sessions/{id}/messages` or
`POST /api/sessions/{id}/messages/stream`.
Each send supplies a UUID `request_id`; `draft_revision` is optional and identifies
the submitted saved draft. The request list and original-request lookup are
`GET /api/sessions/{id}/requests` and
`GET /api/sessions/{id}/requests/{request}`. Explicit cancellation uses
`POST /api/sessions/{id}/requests/{request}/cancel`. These host request controls
share execution fencing while retaining the host workbench's conversation Store.

Frozen external R3 App-CLI/JingSi contracts, their required ledgers and historical
qualification evidence are unchanged. Product route/storage naming in this release
does not redefine those external protocols or rewrite earlier evidence.

## Restart and due-time behavior

Within the selected release, an ordinary restart restores the workbench's saved
drafts, conversation history, files, schedule definitions and occurrence records.
Desktop stores belong to their installation; host WebChat data belongs to its host
repository and authenticated Owner. There is no automatic cross-workbench history,
draft, file or schedule synchronization. Mail is the exception: each cache
synchronizes the backend's authoritative mailbox using revisions and cursors.

Desktop scheduling is online only while its main process is running with a usable
authenticated backend connection. Hiding its window does not stop the scheduler;
exit, suspend, connection loss or an authentication-generation change breaks its
availability window. The host scheduler runs in the backend service independently
of an open browser tab. Its repository/publisher availability and service polling
continuity determine whether a due occurrence can be admitted.

An occurrence due while its owning scheduler is unavailable is permanently missed.
Startup, reconnect and wake scan and record missed work; they do not execute a
catch-up backlog. Normal timer delay within continuous availability remains
eligible. A recurring definition retains its future occurrences with distinct
request identities. An uncertain submit, including a lost response followed by
lookup 404, only reconciles the original request ID: it never replays the POST or
automatically creates a replacement request. Result redelivery and durable ACK
retry remain distinct from executing the request again.

## Qualification boundary

The release checks must use the matched build and isolated storage. Cover draft
save/reload/conflict, local history and selected-file restoration, recurring
definition/occurrence restoration, offline missed occurrences, retained future
occurrences, and lost-submit lookup without replay. The Mac native fixture runs
prepare/restore/revoke in three fresh Electron processes with a temporary profile,
real secure storage and a synthetic loopback backend. It verifies zero execution
POSTs for the occurrence that became overdue between processes. It does not
exercise real OS sleep or reset a keychain, installed application or real profile.

Native suspend/resume and installed-package acceptance remain separate evidence
for the exact target build. See [draft persistence](workbench-drafts.md),
[desktop scheduling](desktop-scheduling.md) and
[host scheduling](workbench-host-scheduler.md) for the implementation boundaries.
