# Workbench draft persistence

> Language: English | [简体中文](../zh-cn/docs/workbench-drafts.md)

Drafts belong to the originating workbench. Saving or loading a draft creates no
message, request ID or execution. Desktop and host WebChat both use the shared
frontend `WorkbenchDrafts` queue with narrow load/save adapters. Desktop connects
through trusted IPC and `useWorkbenchDraft`; host WebChat connects through its
authenticated API and `useHostWorkbenchDrafts`. This UI integration is implemented.

## Shared save and conflict behavior

A draft contains `content`, `attachment_ids` and a numeric `revision`. The queue
debounces typing for 250 ms, serializes saves and compares the saved revision before
writing. A late receipt advances the revision without replacing newer typing.
The UI distinguishes loading, unsaved, saving, saved and error states; sending is
gated until the selected draft is loaded. A failed save retains the edited text
and offers retry. Ordinary retry does not silently advance a conflicting revision.

Conflict recovery is explicit: “Replace saved draft with my text” on desktop or
“Keep my draft and save” on host WebChat reads the latest saved revision and then
compares-and-saves the retained text. A concurrent newer write can still conflict.
Credential/scope changes fence old adapters, delayed receipts and disposal flushes;
they cannot save the previous account's edits under the new identity.

Desktop conversation selection flushes the current draft before changing selection;
failed saves or destination loads retain the previous editable draft. Host WebChat
keeps a queue per conversation, loads the selected draft and flushes the previous
queue when leaving it. Its attachment selections use the same save queue. Hiding
or unloading the page attempts a flush, and pending edits trigger the browser's
unsaved-change guard. These are best-effort lifecycle saves: sudden process/tab
loss before a persistence receipt can still lose unsaved keystrokes. Only a saved
receipt establishes durability.

Both repositories enforce the shared 64 KiB UTF-8 input and 32 selected-file bounds.
Desktop stores owned local file IDs; host WebChat stores bounded presentation
references, at most 4 KiB each. References grant no file or approval authority;
normal resource validation still applies on use. Whitespace is preserved in the
draft. Clearing saves an empty value with an advanced revision so a stale autosave
cannot recreate sent or cleared text.

## Desktop repository and send

The private SQLite repository under `<userData>/workbench` persists welcome-screen
and per-conversation drafts, including selected local file IDs. Scope and attachment
ownership are checked in main. Trusted IPC compares the adapter's original
authenticated scope with the current scope before mutation. Moving the welcome
draft into its first conversation is atomic and advances the source revision.

Sending flushes the selected draft, then consumes that exact revision in the same
SQLite transaction that saves the user message, immutable context and task request.
A transaction failure preserves the draft and creates no task. Submission to the
execution service is a separate operation using the durable task ID. Scheduling
saves a separate explicit definition and does not consume the conversation draft.

## Host repository and send

Host drafts live beside WebChat conversations in the backend Store, scoped by
authenticated Owner and conversation; tabs in the same scope share a draft. The
welcome draft has that Owner and no conversation ID. Desktop repositories remain
independent and do not synchronize these drafts.

`GET` and `PUT /api/sessions/{id}/draft` access conversation drafts; `GET` and
`PUT /api/workbench/draft` access the welcome draft. Missing drafts read as
`{content:"", attachment_ids:[], revision:0}` without creating records. Conversation
access validates the persisted Owner and excludes MCP-managed sessions. PUT sends
the content, attachment references and last-read revision; successful CAS returns
the next revision and `updated_at`, while a stale revision returns HTTP 409.

Memory, File and PostgreSQL implement the same repository contract. File persists
content and revision together, with definite-failure rollback and unknown-outcome
fencing. PostgreSQL uses transactional CAS in `workbench_drafts`. Persistent File
and PostgreSQL repositories restore drafts after ordinary restart; Memory is
transient. Deleting a conversation deletes its draft without deleting the Owner's
welcome draft. On a welcome send, the UI saves the new conversation draft before
CAS-clearing the welcome copy; these are separate host operations, and a conflict
preserves a newer welcome draft.

A host send supplies a UUID `request_id` and may supply `draft_revision`. The
gateway first records a durable admission fence, then persists the user message
under its stable message ID. Only after verifying that message does it CAS-clear
the submitted draft revision and record the resulting revision for the acceptance
response/SSE event. Execution admission, message storage and draft clear are
separate persistence steps, not one database transaction. A draft conflict
preserves newer text and does not silently overwrite it. Other persistence failures
after admission leave the original request fenced and uncertain.

The UI adopts a confirmed clear revision from the stream and reconciles saved
draft/request state after a detached or lost response. It displays request status
and prevents a retained submitted draft revision from becoming another send.
Unknown outcomes only look up the original request ID, including when lookup
returns 404; retrying a draft save is never permission to replay execution.

## Verification and cutover

Coverage includes desktop atomic enqueue/clear, host Memory/File/PostgreSQL CAS,
ordinary restart, ownership and attachment isolation, definite/unknown write
failures, stale saves after clear, navigation, remount/StrictMode, identity changes,
explicit conflict recovery and stable-request admission before SSE acceptance.
The Mac native fixture verifies text and selected-file references across three
fresh Electron processes. These use isolated fixtures and do not modify a running
deployment or real profile. See [release cutover](workbench-release.md): fresh
storage is required for this version; old drafts and schemas are not imported or
migrated, while ordinary restarts retain data created in the selected release.
