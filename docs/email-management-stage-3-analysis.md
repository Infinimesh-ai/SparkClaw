# Stage 3: Analysis And Conversation Assignment

> Language: English | [简体中文](../zh-cn/docs/email-management-stage-3-analysis.md)

Current follow-up design (not implemented): [Classification and event conversations](email-management-classification-v3.md). The owner confirmed event as the smallest conversation unit, purpose-based event titles, no message/conversation summaries, and category → event list → complete detail panes. It supersedes conflicting proposals below for future implementation; historical implementation and deployment records remain unchanged.

2026-09-09 design proposal: [Analysis and processing improvements](email-management-analysis-v2.md) specifies notification/interaction routing, verification expiry and settings-aligned UI/generated language. It is not implemented or deployed.

Owner-confirmed refinement: sender-address overrides persist for future mail; uncertain mail appears in interaction; all model work excludes attachments; preserve matter-based conversations and add compose/reply. Do not assign responsibility. See the increment for precedence, recovery and acceptance.

Implementation contract, 2026-09-08. Code and engineering checks are in the worktree;
real-provider and semantic release gates remain separate. See the
[implementation report](email-management-implementation.md). Depends on [stage 1](email-management-stage-1-data.md)
and [stage 2](email-management-stage-2-intake.md). Unify receiving accounts within
one owner.

## Pipeline

Deterministically parse headers, bodies, MIME attachments and reply declarations;
use existing document extraction for attachments. Models summarize individual
mail and propose membership. Runtime commits membership atomically, then a
separate bounded model job refreshes the conversation summary. Retry these
without redownloading. Unassigned received mail remains visible.

An individual summary emphasizes the new contribution, distinguishing quoted
history. A conversation summary describes available progress and unresolved
questions with mail/attachment references; it does not replace the timeline.
Partial extraction/history permits explicitly partial analysis, refreshed later.

## Decision Order

1. Reuse current mail-ID membership for duplicate tasks. This skips assignment
   only; changed summary inputs still refresh summaries and relationship checks.
2. Retrieve conversations through observed reply headers and existing provider
   thread memberships. Thread lookup can return multiple topics; ambiguous RFC
   matches must remain ambiguous. Cross-account lookup uses only this owner's
   committed sources and retains mailbox provenance. Copies/conflicts return
   candidates rather than rewriting same-mailbox source facts into a unique
   cross-account parent.
3. Retrieve bounded candidates by counterpart/participants and subject/content,
   across receiving accounts. Preserve source addresses as context.
4. A new address without existing links proposes a new conversation and initial
   summary. A new participant with an existing reply link still enters continuity
   evaluation.
5. For known addresses compare new content, candidate summaries and necessary
   source mail; choose append, new or pending. Address, subject and “Re:” alone
   do not establish continuity.
6. For multi-message threads find existing management conversations and synchronize
   uncaptured members through separate jobs. Do not create one conversation per
   iteration.

Use participant sets for multi-party mail. Missing identities stay pending when
no reliable link exists. Permit one bounded candidate expansion; do not force the
highest-similarity match when evidence remains insufficient.

## Model Contract

Pin mail/representation IDs, content and extracted attachments, participants,
actual reply/thread evidence, candidate conversation IDs/member and summary
versions, selected context, gaps and owner assignment epoch.

Output action append/new/pending, candidate target ID or null, proposed title,
message summary, topic, evidence references, reason, uncertainty and missing
context. Runtime records exact inputs and model/prompt versions, validates owner,
candidate IDs, evidence and versions, and allocates new IDs itself.

Validated management assignments commit automatically without per-mail human
approval. They do not rewrite original From/Message-ID/In-Reply-To fields or
promote semantic similarity into an original reply declaration.

The append/new/pending envelope is used only for assignment of new/pending mail.
Summary-only jobs return summary text, evidence references and coverage; assigned
mail checks return none/suspected_duplicate/pending_correction, affected IDs from
the supplied candidates, reasons and evidence. Runtime binds each output to its
job kind and input versions. An assigned-mail check cannot invoke CommitAssignment
even if a model returns an assignment action.

## Concurrency, Disorder And Revision

Models run with bounded concurrency; owner assignment epochs coordinate commits.
If two models see no prior topic, the second must retrieve and re-evaluate after
the first commits. This rejects stale decisions, not semantic mistakes due to
missing context. No database transaction remains open during model calls.

If reply B precedes parent A, B may form a conversation or remain pending with a
missing link. Later A resolves evidence and triggers assignment for pending/new
members, summary refresh and relationship checks for assigned members. It does
not automatically create another conversation. Source time orders the timeline;
arrival sequences still identify newly received history.

A thread can change topic; new management conversations retain its native thread
ID. Source/context/membership changes invalidate summaries. Stale results cannot
replace newer pointers. Retain decision history. Initial implementation keeps
existing committed members stable and assigns only pending/new members;
conflicting established relationships gain suspected_duplicate/pending_correction
concerns. If B and C already occupy conversations X and Y, later evidence linking
them leaves both memberships and IDs unchanged. Store the concern with affected
IDs, evidence and input versions, show it on both conversations and permit source
navigation; do not merge, move members or hide either timeline. Checks may
supersede concerns with versioned evidence but do not repair membership. Missing
parents/RFC matches alone do not prove duplication. Historical correction and
merge/split commands/UI are future work. The first release does not promise that
out-of-order or incomplete inputs never create duplicate topics.

## Refresh Triggers And Recovery

Separate desired generations and input fingerprints for the following targets:

| Target | Inputs and durable triggers |
|---|---|
| Individual summary | committed body/representation and attachment extraction/coverage; a changed version refreshes even an assigned mail; any selected related context is also a dependency |
| Assignment for new/pending mail | representation, reply links, thread observation and candidate versions; new evidence, resolved missing references or explicit retry schedules re-evaluation |
| Relationship check for assigned mail | relevant reply/context/candidate evidence changes; may publish a concern, never a new assignment |
| Conversation summary | membership, member source/extraction versions, selected individual summaries, selected context and history coverage; changes to any actual input refresh it |

Persist dependencies on concrete IDs and unresolved owner-scoped reply keys;
resolve reverse references when a parent or thread member arrives. New searchable
evidence also schedules a bounded sweep of pending assignments/related concerns,
so semantic candidates without reply headers can be reconsidered. Store a sweep
cursor; coalesce bursts and skip unchanged target fingerprints. Unrelated owner
epoch changes are commit guards, not a reason to regenerate every owner summary.

Stage 1 publication atomically records invalidation plus RefreshIntent; bounded
fan-out admits work and its continuation together. A no-op membership lookup
cannot finish a summary job. Publishing an individual summary schedules its
dependent conversation refresh. Reject obsolete current pointers against all
actual inputs, not member count alone. Preserve old text as stale/failed with
coverage, and retain the latest desired work after a stale attempt finishes.
Restart resumes durable work; backoff is bounded, exhausted failures are visible
and explicitly retryable. Reanalysis of assigned mail refreshes summaries/checks
only, without moving members or downloading again.

## Acceptance

Cover new addresses, same-address unrelated topics, continuations, new
participants replying to known mail, five-member backfill, cross-account topics,
concurrent new mail, reply-before-parent, thread topic changes and same subjects
with different participants. Model failures or incomplete context leave sources
visible and do not trigger capture again.

Evaluate real model assignments against reviewed sample expectations. Mock
state-machine tests prove commit semantics, not semantic understanding. Require
stale concurrent decisions to re-evaluate; separately test B/C already assigned
before A arrives, with both IDs retained and a visible concern. Test attachment
extraction improvement without a membership change, resolved reverse references,
fan-out crash/restart, stale job completion and explicit retry after exhaustion.
Apply the sample counts and quality thresholds in stage 5; do not assert zero
semantic duplicates for arbitrary input order.
