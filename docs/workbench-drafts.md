# Workbench draft persistence

> Language: English | [简体中文](../zh-cn/docs/workbench-drafts.md)

Drafts belong to the originating workbench. Saving one does not create a message,
request ID or execution. The shared frontend `WorkbenchDrafts` queue and
`useWorkbenchDraft` hook accept narrow load/save adapters, independent of transport
or storage placement. Drafts contain text, an attachment-ID list and a numeric
revision; an adapter with no draft attachments supplies an empty list.

The queue debounces typing for 250 ms, serializes writes and uses compare-and-set
revisions. A late save receipt cannot replace newer typing. Switching conversations
flushes the current draft first; a failed flush leaves the current draft available
and exposes the error. The UI shows whether a draft is saved or pending and offers
a save retry after failure. Unsaved edits are not reported as durable. Disposal
flushes pending edits, but abrupt process loss before a persistence receipt can
still lose the pending keystrokes.

The desktop adapter persists scoped welcome-screen and per-conversation drafts in
SQLite, including selected local file IDs. It validates attachment ownership and
the common input-byte budget. Moving the welcome draft into its first conversation
is atomic, and advances the source revision so a delayed write cannot recreate it.
Trusted IPC compares the draft's original authenticated scope with the current
scope before any mutation; a delayed welcome autosave cannot land in another
account or deployment.

Sending first flushes the selected draft, then consumes that exact revision in the
same transaction that saves the user message, immutable context and task request.
Clearing the draft advances its revision. A transaction failure preserves the
draft and creates no task; a stale save cannot resurrect sent text. Submission to
the execution service remains a separate operation, using that durable task ID.
Scheduling uses its separate explicitly saved definition and does not consume the
conversation draft.

The desktop implementation initializes the selected development schema directly;
there is no old-schema migration or draft import. Verification at this commit:
121 desktop tests, 202 WebChat tests across 48 files, and the WebChat type check and
production build passed. Focused cases cover restart persistence, scope isolation,
attachment ownership, CAS conflicts, disk failure, atomic enqueue/clear, delayed
save receipts, navigation/remount and preservation of edits during in-flight saves.
The host WebChat adapter and its atomic send boundary must use the same queue and
revision semantics as part of overall workbench convergence.

## Host repository

Host drafts are workbench-local data stored beside WebChat conversations on the deployment host. The authenticated Owner and conversation identify the draft; browser tabs share that scope. The welcome draft uses the same Owner scope without a conversation ID. Desktop installations retain their own local draft repository.

`GET` and `PUT /api/sessions/{id}/draft` access a conversation draft. `GET` and `PUT /api/workbench/draft` access the welcome draft. Both use the normal authenticated host API. A missing draft reads as `{content:"", attachment_ids:[], revision:0}` without creating a record. Conversation access checks the persisted session's Owner and excludes MCP-managed sessions.

A PUT supplies `content`, `attachment_ids` and the last read `revision`. The repository preserves whitespace, atomically compares that revision, and returns the next revision plus `updated_at`. A stale version returns HTTP 409. Clearing saves an empty draft with a new revision; it does not remove the version fence. Consequently a late autosave from another tab cannot recreate text already cleared by a newer operation. GET is read-only, and neither saving nor loading a draft submits execution.

The Store currently permits at most 1 MiB of UTF-8 draft text and 64 attachment references, each at most 4 KiB. These are draft-storage bounds, not execution-admission limits. References do not grant file authority: normal attachment/resource validation still applies at execution admission. Drafts contain no executable approval authority.

Memory, the default File snapshot and PostgreSQL implement the same `SessionRepository` methods. File commits cover the content and revision together, with definite-failure rollback and unknown-outcome fencing. PostgreSQL uses a fresh `workbench_drafts` table and transactional compare-and-swap. Session deletion removes its draft; an Owner's welcome draft is unaffected. No old draft import or migration path is provided.

After a stable execution request has been admitted, a workbench can clear the exact submitted draft revision. The execution fence and host draft repository are separate persistence domains: a clear failure must be reconciled by reading the draft revision, and a conflict must preserve newer text. Never turn a retained draft into an automatic resubmission. The shared frontend draft controller serializes saves and reconciles the adapter's resulting revision; its UI wiring is a separate implementation step from this host repository/API change.

Validation covers the same Memory/File/PostgreSQL contract: empty reads, whitespace and attachment isolation, Owner/session isolation, welcome drafts, concurrent CAS, stale-write rejection after clear, ordinary restart, session deletion, definite write failure and lost-commit reconciliation. No running deployment or real data is changed by these tests.
