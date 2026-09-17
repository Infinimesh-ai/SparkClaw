# Email Local Download, Processing and Window Display Design

> Language: English | [简体中文](../zh-cn/docs/email-local-download-storage-design.md)

Status: design created 2026-09-15 and revised 2026-09-16. This document collapses local file persistence, parsing, and window display of fetched mail into a single chain. The revision adds one bounded recovery journal per non-empty source batch, immutable same-ID source-conflict handling, a bounded header probe followed by exactly one full asynchronous MIME parse, and scheduler-free terminal warnings whose visual interruption can be acknowledged without changing audit state. Normal network polling, failure classification, one carry-forward retry, overflow gaps, and Refresh follow [Email timeline incremental sync, carry-forward retry, Refresh, and audit](email-timeline-incremental-sync-design.md), which specializes the earlier [email network read contract](email-pipeline-optimization-design.md); no DOM reading, menu export, or native fallback is added.

## Goal

The user triggers or awaits a sync in the dedicated browser mailbox window. The Reader continues to observe and replay real network requests from the signed-in page to obtain original message bytes. SparkClaw first saves the original into the dedicated email directory, then parses, deduplicates, classifies, and summarizes it, and finally writes displayable data into the existing Store database. The mail card keeps an entry point to the original so the user can preview or download the local copy when they need to check the source.

## End-to-end flow

```text
Dedicated browser network Reader
  → Controller download receipt
  → Gateway verification (owner / mailbox / stable ID / size / hash / MIME)
  → Atomic persistence into the dedicated directory
  → EML/MIME parsing and attachment indexing
  → Store transaction writes mail metadata, body representation, state
  → Asynchronous classification / event assignment / summary
  → Email window reads the database projection
```

Every step is idempotent on the canonical owner, mailbox binding generation, provider-stable message ID, and original hash. The download temporary file is written to the same filesystem and atomically renamed. Normal polling does not pay a per-file `fsync` barrier for refetchable source bytes; each non-empty source batch instead persists one bounded recovery journal before any rename, while an empty poll writes no journal. An unclean shutdown is detected and recorded, then the next scheduled poll carries its targeted repair while still discovering new mail. Refresh triggers that same bounded round sooner. The detection itself is operational and does not consume the mail-level threshold. If the same stable mail ID subsequently fails two consecutive qualified source-sync attempts under healthy shared prerequisites, it becomes `sync_suppressed`, incurs no later scheduled or Refresh work, and remains visible as an owner warning. Files that fail verification are quarantined with their error recorded and must not reach the window.

## Dedicated directory

The root is the dedicated `email/` directory under the current workspace data directory. No arbitrary path from a model or browser script is accepted. Originals are layered by received date, owner, mailbox, mail, and deterministic capture identity, canonicalized as a ten-segment path. `capture-id` identifies the one admitted storage instance; it is not an inbound content-revision number:

```text
email/<YYYY>/<MM>/<DD>/<owner-scope>/<mailbox-id>/<mail-id>/source/<capture-id>/
  message.eml               # immutable original
  capture.json              # MIME, byte count, SHA-256, provenance, date evidence, capture time
  body.txt / body.html      # parsed body representations
  headers.json              # normalized headers
  parts/<part-id>/<name>    # optional, saved and verified per MIME part
```

`capture.json` contains no cookie, SID, or temporary token. File permissions are granted only to the SparkClaw service account. The download endpoint accepts only a source pointer already committed in the database, and re-checks the owner.

The date directory comes from the **original's `Date:` header**, computed by a bounded header probe after the bytes land in staging but before the one full asynchronous MIME parse, and the tree is then atomically renamed into place. The probe reads only the bounded header block needed for date, identity evidence, and gross envelope validity; it does not decode body parts or attachments. The header is byte-derived, timezone-bearing evidence that resolves identically on every replay. List-row display text is a relative time on some providers ("today 14:32") and cannot be parsed reliably, so it is only a fallback. The chain, in order:

| # | Source | `received_source` |
|---|---|---|
| 1 | The original's `Date:` header | `eml_date` |
| 2 | List-row display time | `list_row` |
| 3 | Capture time (sampled once per attempt, so the path is stable within that attempt) | `captured_at` |

`capture.json` also records `date_path`, `received_at` (RFC3339 UTC), `received_source`, and `received_display_text` (the raw list-row text, as timezone evidence). Both Gateway validators parse this path by segment rather than reconstructing it — the date cannot be predicted server-side, but every other segment remains an exact equality against a value the request already holds, so the tamper surface is unchanged.

Deleting a mail record writes a tombstone before the bytes are removed, so the database never holds a dangling link.

## Database projection

The existing EmailRepository and three-layer model are reused; no second email database or queue is introduced. The `PublishEmailCapture` transaction writes the original pointer, mail identity, parse job, and source state together. The parse result becomes an immutable representation version; classification, events, and summaries reference only that representation version.

The email window queries a light projection by default: subject, participants, received time, remote read state, classification/event, summary state, and original availability. Body and attachments are read by pointer only when the user asks for them.

Authorization for the original uses a bearer token plus four owner checks (mail ownership, capture ownership, owner-scope path prefix, and the manifest-to-mailbox identity triple) rather than a one-time signed URL: the credential never enters the URL or the Referer, which is in practice stronger. The projection returns an `original_available` boolean rather than an `original_url`; the URL is assembled client-side from a fixed route, avoiding a second source of truth in the database.

Beyond checking the database pointer, `original_available` also performs one read-only stat against the workspace: a pointer in the database is not evidence the bytes are still there. Once a file is deleted externally, the window stops offering a download. A separate `original_purged` flag marks originals the user cleaned up deliberately, distinguishing that from a file lost by accident.

## Deduplication and failure handling

- Only one authoritative inbound original is kept per owner, mailbox binding generation, mailbox, and provider-stable message ID. Repeated list rows, labels, and pagination only add observation records; the same byte hash is an idempotent replay.
- Inbound mail has no implicit content-update version. If the same stable ID later returns different bytes without a separately qualified provider revision token, retain the first verified original, quarantine the later bytes, and record `source_conflict`. Under healthy prerequisites this is mail-specific only when isolated to that ID; cohort conflicts are provider-operational. The first qualified conflict gets one exact reread, and a second consecutive qualified conflict becomes `sync_suppressed`. A future provider-proved revision model requires a separate design.
- Download succeeded but parsing failed: the mail still shows subject, time, and the original entry point in the window, with parse state `failed`, awaiting retry. Parse/model retries are downstream work and do not consume the source-sync suppression threshold.
- Parsing succeeded but the model failed: the body and original are kept, while summary/classification show pending or failed. The capture boundary is not blocked.
- When an ordinary provider list/range result is incomplete, `poll_through` does not advance and the next poll extends the same lower bound to its new upper bound; overflow instead freezes one interval for one confirmation and then leaves a terminal visible gap while live-tail polling continues. When only an original or capture receipt fails, the stable ID is recorded with its first qualified failure and discovery may advance; the next poll or an earlier Refresh gives that exact target its one remaining attempt. A second consecutive qualified mail-level failure marks it `sync_suppressed`; new-mail polling continues, but this stable ID is never requested again. The immutable warning remains in paginated history, while owner acknowledgement removes only its badge/banner interruption.

The immutable-source decision is deterministic:

| Observation for one stable ID | Decision |
|---|---|
| No accepted original exists | Verify and admit the bytes as the one authoritative original |
| Accepted original exists and SHA-256 is identical | Treat as an idempotent replay; create no file, parse job, or failure |
| SHA-256 differs and no qualified provider revision token exists | Keep the first original, quarantine the new bytes, record the first qualified `source_conflict`, and schedule only one exact reread |
| Exact reread returns the first accepted hash | Resolve the conflict attempt, discard the quarantined candidate, and keep the original unchanged |
| Exact reread confirms another hash or repeats the isolated conflict | Record the second qualified mail-specific failure and set `sync_suppressed`; never overwrite either in place |
| Two or more IDs show the same conflict signature in one round | Promote the cohort to `provider_operational`; do not consume any mail's two-attempt threshold |
| A provider later exposes a qualified revision token | Do not infer versioning; keep the mailbox unqualified for that update path until a separate revision design is accepted |

## Removed redundant paths

The following duplicated responsibilities are removed: DOM reading and page-click export; script upload to an arbitrary localhost; a second copy of the original made by the Gateway; unread as an independent discovery channel; body entering the list projection by default; summaries triggering classification or event loops in reverse; multiple timers scanning the same mailbox. What remains is the network Reader, the single Controller→Gateway→Store path, one bounded header probe during capture, exactly one full MIME parse after source commit, independent analysis jobs, and on-demand body reads.

“One parse” does not mean the bytes are never read during capture. The network capture stream necessarily writes the full EML and calculates size/SHA-256 inline, but it inspects only a bounded header block. After commit, the asynchronous parser performs the sole full semantic read: MIME structure, body, and attachments are decoded in that pass, and any verification hash is calculated inline rather than by a separate hash-only reopen. Normal synchronization therefore has no full MIME parse before commit, no second full MIME parse afterwards, and no standalone full-file hash pass. A later user download, backup restore, or explicit corruption check is on-demand verification, not part of every poll.

## Legacy files and attachments

The old receive chain and its history are deleted outright at cutover; no migration or runtime compatibility path remains. Intake is stopped first, every email-management record except mailbox bindings is deleted, and the contents of the dedicated `email/` directory are removed. The mailbox address, provider, connection state, and intake toggle survive; activation and synchronization boundaries are reset to the pinned deployment anchor. Attachments in the new chain do not currently enter body viewing or model analysis, but remain associated with their mail. The original EML is retained; parsed attachments are saved per MIME part with name, MIME type, size, hash, and pointer recorded.

## Security, capacity and operations

Originals and attachments are protected by a per-file size limit, a per-capture total limit, and owner isolation; exceeding a limit produces an explicit failure state. `capture.json`, the database pointer, and the actual files are fully verified against size and SHA-256 on every read. Backups must cover both the database and `email/`; after a restore, hashes are verified before the window is opened. Logs record only the source ID, version, size, hash, and error code — never body content.

Originals are retained permanently by default and never expire. The user can manually clean up a single mail, a date directory, a single mailbox, or all originals; a "original manually cleaned up" state is retained afterwards. The system warns on workspace capacity: 85% used or under 2 GiB free is a warning, 95% used or under 512 MiB free is critical. The warning only prompts the user and offers manual cleanup entry points; it **never deletes any file automatically**.

## Implementation order and acceptance

1. Complete the date directory end to end: the Controller splits by `Date:` header, staging moves out of the date tree, and an `index/` pointer is written; both Gateway validators switch to segment parsing.
2. Capture performs only the bounded header probe. The parser consumes a committed `message.eml` exactly once for full MIME/body/attachment parsing and writes the representation; any integrity hash required during that parse is computed inline with the same read, never by a standalone reopen pass.
3. The window switches to the light database projection, with an owner-verified original entry point, on-demand body loading, and a truthful availability probe.
4. Four-level manual cleanup, bounded startup failure detection, one scheduled carry-forward attempt, terminal `sync_suppressed` state plus owner warning, the immediate Refresh trigger, and capacity warnings; remove the legacy DOM/native/unread branches and duplicate schedulers, and update documentation and configuration migration.

Acceptance covers: rediscovering the same mail does not persist it twice; a same-ID/different-hash replay never overwrites the first verified original; a half-written or hash-invalid file is never exposed after a power loss or process kill; a crash before journal publication leaves no final-tree rename and is bounded by the stale-staging sweep, while every crash after publication is recoverable by exact journal paths without a date-tree scan or final-tree orphan; an empty poll performs zero journal write and zero `fsync`; the interruption leaves a durable failure trace carried by the next poll or an earlier Refresh; the same stable ID's second consecutive qualified mail-level failure creates a persistent warning and causes zero future source requests from either schedule or Refresh; network, login, provider-wide template, disk-capacity, permission, and Store failures never consume that threshold; hash/MIME/owner verification rejects a forged receipt; the original remains viewable after a parse or model failure; a link can only reach its own mail; links work after a backup restore; a sync with nothing new triggers no download, parse, or model call.

This design does not change any cross-project interface or protocol. If another project ever needs to read email originals directly, a contract must first be established and accepted in InfiniCenter.

## Confirmed implementation decisions (2026-09-15)

- The date directory uses the mail's received time; once parsed into a stable timestamp it is stored as `YYYY/MM/DD`, and the original display text and timezone evidence are retained.
- `emailWorkspaceRoot` is a Browser Controller host configuration set by `SPARKCLAW_BROWSER_EMAIL_WORKSPACE_ROOT`, defaulting to the project's `data/workspaces`. The email directory is fixed at `email/` inside that workspace, and file handling never leaves the workspace. See `tools/browser-controller/src/main.mjs` and `scripts/setup-browser-controller.sh`.
- At cutover, the old receive chain, old mail/analysis/job/checkpoint records, old files, and old attachments are deleted. Only mailbox bindings and their enablement settings survive; all activation and synchronization boundaries are reset to the pinned deployment anchor.
- The original offers two entry points: a safe parsed preview and a raw `.eml` download.
- Manual cleanup has four levels — single mail, date directory, single mailbox, and all local mail — with no confirmation step. Mail metadata and an "original manually cleaned up" state are retained afterwards.
- Received mail is handled by immutable original identity; no implicit content-update version is designed for inbound mail. Same-ID/same-hash is idempotent, while same-ID/different-hash is `source_conflict` and cannot overwrite the first verified source. Multi-message conversations are expressed through the existing event partitioning and membership relations, never by copying or overwriting an original.
- Per-mailbox download is serial, matching the shared browser session; concurrency happens between mailboxes. A batch holds at most 50 messages. A first qualified mail-level failure is retained in the checkpoint but never pauses the mailbox; the next poll carries it once while also discovering new mail. If that one remaining qualified attempt also fails, the stable ID becomes `sync_suppressed`, is never automatically or manually requeued by Refresh, and remains as an owner warning. Scheduler indexes exclude its detail row; acknowledgement affects only the unacknowledged UI count.
- When files have landed but the database transaction failed, startup records the unresolved attempt. A Store failure is operational and does not increment the mail-level counter. Once Store is healthy, the next scheduled poll or an earlier Refresh verifies the manifest, size, and SHA-256 and registers the capture after the fact without downloading again.
- Every recognizable MIME part is saved, including inline images. The current interface shows only attachment metadata and a download entry point; it does not view or analyze attachment content.
- Originals are kept permanently and never cleaned up automatically — only by the user. The system adds a workspace capacity warning that prompts the user at the threshold but deletes nothing.

## Implementation addenda (2026-09-15)

### Date source and staging

The date is only knowable after the bytes have landed far enough for the bounded header probe; no full MIME parse is required. Staging therefore cannot live inside the date tree. The staging root is `email/<owner-scope>/staging/<mailbox-id>/<mail-id>/attempt_<invocation-hash>/`, sitting beside `invocations/`, `pages/`, and `index/` on the same filesystem, so the commit is still a single atomic rename and the date tree contains only completed captures.

### The `index/` pointer and replay

A committed capture lives under a date directory determined by the message content, so a replay cannot reconstruct its path from stable identity alone. A date-independent pointer remains the normal lookup accelerator:

```text
email/<owner-scope>/index/<mailbox-id>/<mail-id>/<capture-id>.json
```

The pointer is rebuildable and is not the power-loss authority. Timeline-v2 adds one deterministic journal per non-empty source batch:

```text
email/<owner-scope>/journal/<mailbox-id>/<invocation-id>.json
```

The bounded journal contains no credential or content. Each entry records stable identity, capture ID, staging path, final path, size, SHA-256, and intended Store command key. The sequence is:

1. Write each source and `capture.json` to staging, then compute its final date path.
2. Write all batch entries to a temporary journal; `fsync` that one file, atomically rename it into `journal/`, and `fsync` the journal directory once.
3. Write or refresh the rebuildable `index/` pointers and atomically rename each staging tree to its recorded final path. Source files, index files, and per-mail directories receive no individual synchronous barrier.
4. Execute the deterministic Store batch. Delete the journal only after Store confirms that command committed. A crash before durable journal publication has not renamed any source; a crash later always leaves either Store truth or the exact journal paths needed for reconciliation. A stale journal after a committed Store command is simply verified and reaped.

This is a constant two durability barriers per non-empty batch, not two barriers per file or mail; an empty poll pays none. It closes the otherwise possible window in which the final date tree survives but both the Store record and rebuildable index pointer are lost. The source bytes themselves remain refetchable: if neither a valid staging tree nor final tree survived, recovery schedules the exact stable ID for reread. A pointer whose final manifest is absent is never treated as complete; a fully landed and verified capture is adopted without downloading again. Detection is operational and does not consume the two-failure mail threshold; only a provider per-mail reread failure after healthy prerequisites qualifies.

The source-batch crash matrix is:

| Crash point | Durable evidence | Recovery |
|---|---|---|
| While writing staging, before journal publication | No final source and no published journal | A later bounded stale-staging sweep removes the abandoned attempt; nothing can be advertised or adopted |
| After journal file `fsync` but before journal-directory `fsync` | Journal publication may be absent after power loss; no source rename has begun | Treat exactly like pre-publication staging; the final tree is still untouched |
| After durable journal publication, before any source rename | Exact staging/final paths, hashes, identities, and Store key | Verify staging, then finish the safe rename or leave one exact reread target |
| After some or all source renames, before Store commit | The same journal plus any landed final trees | Verify and adopt landed captures; finish valid remaining moves; never scan the date tree |
| Store commit outcome is unknown | Journal and deterministic Store command key | Query/replay the command key first; do not create a second capture or advance the checkpoint twice |
| Store committed, journal not yet deleted | Store truth and a stale journal | Verify the committed result and reap the journal; no provider reread |

The only unjournaled residue can therefore be staging created before publication. It is neither user-visible nor inside the date tree and is removed by the bounded stale-staging pass. Once any source can enter the final tree, the durable journal is already the recovery authority.

### Purge as a field, not a fourth state

`PurgedAt *time.Time` is the single authority for "these bytes must not exist", rather than a fourth value on `CaptureState`. `CaptureState` is consumed by the recapture-skip check, the page-batch short circuit, the parse and mark-read triggers, and the counters and projection. A new state would push a cleaned-up mail out of `complete`, and the capture job would immediately download back the bytes the user just deleted. A nullable timestamp preserves exactly the semantics wanted: this mail was captured, and its bytes were deleted at the user's request.

What survives a purge:

| Data | Outcome |
|---|---|
| Representation body | Survives — preview keeps working after cleanup |
| Attachment `Path` | Survives (for audit) |
| Attachment `State` | Set to `purged` |
| `CaptureID` / `CaptureState` | Survive, keeping the recapture guard armed |
| `ManifestPath` / `OriginalPath` / both hashes | Survive as the unlink target and the recovery job's tombstone evidence |

### Cleanup ordering and the crash matrix

The sequence is: ① write the tombstone in the database → ② remove `dir(ManifestPath)` for each → ③ prune empty ancestors upward with non-recursive removes (stop at the first `ENOTEMPTY`, never touch `email/`) → ④ continue by cursor.

| Crash point | Disk | Database | Repaired by |
|---|---|---|---|
| Before ① commits | Files present | No tombstone | Nothing to repair; the user retries |
| After ①, before ② | Files present | Tombstoned | Recovery pass 1 |
| Mid ② | Partially removed | Tombstoned | Recovery pass 1 (removal is idempotent) |
| After ②, before ③ | Empty ancestors | Tombstoned | Recovery pass 1 pruning |

**There is no window in which the database advertises bytes that are already gone** — that is the point of tombstoning first. The inverse window is safe: the download endpoint refuses on the tombstone before it ever touches the filesystem.

Enumeration always goes through database queries rather than filesystem traversal: the owner scope is a digest, so traversal cannot map a directory back to an owner; authorization must be database-derived; the database is the tombstone authority; and every scope is an indexed, cursored query, whereas a full-tree walk would be O(all captures) for a single-mail cleanup.

### The local recovery worker reused by scheduled sync and Refresh

The existing owner-level `source_recovery` machinery is reused as an implementation primitive, but timeline-v2 changes its trigger: startup only detects and records an unresolved operational failure; the next scheduled composite sync or an earlier user-operated Refresh invokes reconciliation. Detection and local adoption do not consume the mail-level threshold. Tombstone cleanup remains part of the explicit cleanup operation. The worker shares a slot with the parse worker, carries no mailbox, and does not consume a model worker. Four bounded passes:

1. **Outstanding journal reconciliation** (cap 16 journals and 50 mail entries per invocation). Check Store command outcome first, then verify the exact final or staging path. Adopt a complete source, finish a safe staging rename, or leave one exact reread target; never walk the date tree.
2. **Tombstone → unlink** (cap 200). Found by the `purged` state index and marked as reaped afterwards so the index does not grow without bound.
3. **Indexed landed source → register without downloading** (cap 50 mails). Bounded by admitted mails that still lack a committed capture. It uses a valid `index/` pointer only as a secondary accelerator, re-hashes files against recorded size and SHA-256, cross-checks the identity triple, and then registers. Registration uses a separate adoption path that carries no job lease and is restricted to filling a hole: a mail that already has a capture is left alone, an existing capture ID is a conflict, and a live capture always wins the race.
4. **Stale staging sweep** (cap 200). Removes `attempt_*` directories whose mtime is older than twice the job timeout and are not named by an outstanding journal; anything younger is skipped.
Three anti-race guards: skip capture jobs running under an unexpired lease; adoption uses a deterministic command key so it meets a live publish inside the store's existing replay/conflict logic; staging is created fresh per attempt, so anything in flight is young by construction.

### Deployment anchor verification

This round of receive verification pins the deployment anchor to `2026-09-11T00:00:00Z` in `data/workspaces/.sparkclaw-deployment.json`. After the service starts, call `GET /api/email/sync-status` to check that `coverage_start` begins at that anchor. In timeline-v2, the scheduler carries only retry-eligible first mail failures into the next ordinary incremental poll; the owner may use `POST /api/email/sync/refresh` to run that same composite round immediately. Suppressed IDs are warning-only: neither path requeues or scans their detail rows, and the status endpoint reads transactionally maintained aggregate counts. With the service not running there is no receive effect at all; a connection failure must be recorded and must not be misread as zero mail.
