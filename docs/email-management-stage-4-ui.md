# Stage 4: Unified Email Popup

> Language: English | [简体中文](../zh-cn/docs/email-management-stage-4-ui.md)

Current follow-up design (not implemented): [Classification and event conversations](email-management-classification-v3.md). The owner confirmed event as the smallest conversation unit, purpose-based event titles, no message/conversation summaries, and category → event list → complete detail panes. It supersedes conflicting proposals below for future implementation; historical implementation and deployment records remain unchanged.

2026-09-09 design proposal: [Analysis and processing improvements](email-management-analysis-v2.md) specifies notification/interaction routing, verification expiry and settings-aligned UI/generated language. It is not implemented or deployed.

Owner-confirmed refinement: sender-address overrides persist for future mail; uncertain mail appears in interaction; all model work excludes attachments; preserve matter-based conversations and add compose/reply. Do not assign responsibility. See the increment for precedence, recovery and acceptance.

Implementation contract, 2026-09-08. Code and engineering checks are in the worktree;
real-provider and semantic release gates remain separate. See the
[implementation report](email-management-implementation.md). Reference JingSi-mail presentation, not its
receiving logic. Depends on [stage 3](email-management-stage-3-analysis.md);
fixed projections can validate layout earlier.

## Views

An Email entry opens a large closable WebChat popup, not necessarily a new OS
window. Reopening queries Store and retains selection preference by stable ID.
Desktop uses list/detail panes; small screens navigate between them. Provide
keyboard selection, focus restoration and an explicit close action.

| Area | Content |
|---|---|
| Header | receiving-address filter, participant/content search, intake status and manual sync trigger |
| Conversation list | participants, topic title, summary preview, last activity, unseen count and suspected duplicate/pending correction marker |
| Detail | participants, overview, timeline, backfill/analysis progress and versioned concern evidence with links to related conversations |
| Mail card | direction, actual From/To/CC, time, receiving address, individual summary, body, attachments and original |
| Pending area | received but unassigned mail, accessible sources and reanalysis |

No provider-specific main columns. Provider appears only where configuration or
provenance requires it. The same address can have several topic cards; group
topics show participant sets.

Use mail IDs as timeline keys and source time plus stable ID for ordering; unknown
source time uses explicitly labeled arrival time. Repeated discovery adds no
card. Cross-account copies retain provenance and collapse only when the backend
supplies proved copy associations, with sources expandable. Frontend subject,
summary or RFC-ID matching cannot deduplicate. Distinguish conversation and
individual summaries; previews must not masquerade as model results.

## Local Viewing

Remote read after capture does not mean the user viewed local results. Record
ViewReceipt per owner/mail ID. A mail card counts as presented when it enters the
visible viewport of the active popup detail or pending view; opening a conversation,
loading/prefetching a page or showing a conversation preview acknowledges nothing
by itself. Reading the whole expanded body is not required. Send only explicit
presented mail IDs in batches of at most 100, including pending mail. Replays and
multiple tabs merge receipts idempotently. A delayed request cannot acknowledge
IDs absent from it. Unseen counts come from backend receipts, not arrival maxima.

Example: late historical mail 101 is on an unloaded older page while new mail 102
is visible. Acknowledging 102 leaves 101 unseen. Hidden copies in a folded group
are not acknowledged until their own source cards are presented. Assignment and
summary refresh preserve receipts. Viewing neither marks provider mail read nor
recaptures it. Originals/attachments use existing owner-scoped file access, never
host paths or tokens.

Suspected duplicate/pending correction is separate from unassigned pending mail,
local unseen state and processing failure. Both existing conversations remain
visible with their original members. Related-conversation links expose evidence;
reanalysis may update the concern, but there is no first-release repair/merge
button or automatic regrouping.

## Proposed Local Gateway Surface

These are proposed interfaces, not registered routes or cross-project contracts.

| Method/path | Meaning |
|---|---|
| GET /api/email/conversations | owner-scoped address/source/content filters and cursor pagination |
| GET /api/email/conversations/{id} | participants, summary, unseen count, concerns/related IDs and processing/coverage state |
| GET /api/email/conversations/{id}/messages | independent timeline pagination, sources and per-mail viewed state |
| GET /api/email/pending | paginated unassigned received mail with per-mail viewed state |
| GET /api/email/sync-status | scans, admitted intervals/folder scope/gaps, binding pauses, backfill, backlog and bounded errors |
| POST /api/email/sync | coalesced scheduling, not inline browser execution |
| POST /api/email/messages/viewed | explicit mail_ids, maximum 100; authenticated owner validation and idempotent per-mail receipts, including pending mail |
| POST /api/email/messages/{id}/reanalyze | coalesced current-input summary/check refresh; assign only new/pending mail, explicitly rearm exhausted failures |

Reuse and extend provider settings/login endpoints with the stage 1 verified
mailbox, separate intake switch and account-switch pause state. Show one active
account per provider; historical mailboxes remain source filters. Initially poll
bounded versioned
projections; stale responses cannot overwrite newer data. Consider existing event
infrastructure later rather than inventing another bus. Preserve loaded pages
and current selection on refresh. Server search covers admitted data, not just
currently loaded frontend rows; indexes are rebuildable projections.

## Acceptance

Require the 101/102 pagination gap, viewing pending mail before assignment, hidden
copies and multi-tab replays. Both suspected duplicate conversations stay visible
with unchanged memberships; reanalysis must not perform an implicit repair.

Cover multiple topics per address, multi-party/cross-account topics, pending and
failed analysis, backfill, originals/attachments, late arrivals, local unseen
markers, refresh after pagination, reconnect, closed-popup intake and stable IDs
after restart. Check keyboard, small screens and long content. Static reference
styling alone does not complete the stage.
