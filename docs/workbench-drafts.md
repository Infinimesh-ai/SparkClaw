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
