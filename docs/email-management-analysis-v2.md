# Email Analysis and Processing Improvements

> Language: English | [简体中文](../zh-cn/docs/email-management-analysis-v2.md)

Current follow-up design (not implemented): [Classification and event conversations](email-management-classification-v3.md). The owner confirmed event as the smallest conversation unit, purpose-based event titles, no message/conversation summaries, and category → event list → complete detail panes. It supersedes conflicting proposals below for future implementation; historical implementation and deployment records remain unchanged.



Status: implementation started in the worktree, 2026-09-09; not deployed. See section 8 for implementation and validation, and section 9 for sending evidence boundaries.
This proposes the next increment to the [overview](email-management-design.md),
[stage 3](email-management-stage-3-analysis.md) and [stage 4](email-management-stage-4-ui.md).
Where behavior differs, this document defines the proposed increment, not current runtime behavior.
Scope is internal to SparkClaw; no cross-project protocol is introduced.

## 1. Goals and Current Findings

Follow the configured window language and distinguish informational notifications from mail requiring confirmation
or interaction. Classify by a concrete need to confirm, reply, decide or continue an exchange; do not assign responsibility
or decide who should handle it. Users handle their own matters; provide reliable conversations, composing and replies.
Do not classify by official/personal
or machine/human sender identity. Keep top-level Conversations / Pending mail; within Conversations use
Information notices / Confirmation and interaction. Verification codes, advertising and routine login notices
belong to notifications. Verification, promotion, account security and general are optional notification filters,
not additional main categories. Display evidence-backed expiry; receiving timeliness remains deferred.

At the design baseline, the code passed `text` and `language` from `App.tsx` into the popup and localizes ordinary buttons
and dates. However, `emailPopup.tsx` displays backend titles, summaries, concern explanations and
`Error.message` directly. `emailmanagement/model.go` has neither output language nor notification classification.
The language setting is currently browser-local `sparkclaw.language`, not a durable owner setting.
The design therefore covers interface strings, generated presentations and classification together.

## 2. Window Structure and Interaction

```text
Email
├─ Conversations
│  ├─ Information notices (verification, promotion, account security, general)
│  └─ Confirmation and interaction
└─ Pending mail
```

Initially open Confirmation and interaction, then remember this device's selection. Mailbox filters, search and
unseen counts cover the selected category's complete admitted scope, not just loaded rows. Category changes
reset pagination; preserve per-category selection and keep details stable during refresh or language changes.

| View | List and detail |
|---|---|
| Confirmation and interaction | Participants, topic, requested confirmation/reply, summary and timeline; official and personal senders both qualify |
| Non-code information notices | Individual cards with service, purpose, source time and summary; body, attachments and original remain available |
| Verification codes | Individual cards with service, purpose, time and expiry state; details allow revealing/copying a reliably extracted code |
| Pending mail | Admission/parsing exceptions without usable display material, with recovery access. Uncertain classification and unassigned mail are visible in the interaction entry |

Information notices include codes, advertising, routine login alerts, paid-bill receipts, shipment updates and
subscriptions. Requests to verify a bill, confirm delivery dates or provide documents belong to interaction,
even when sent automatically by an official service. Do not group unrelated notices solely by sender.
Codes are not permanent conversations; retain previous messages. Mask codes in lists and require explicit
reveal/copy in details. Copy does not mean used. Do not automatically reply, submit codes, follow verification
links or delete expired mail. Viewing stays per actually rendered mail_id; category, expiry, unseen and errors
are separate facts.

## 3. Classification and Analysis

Mutually exclusive primary categories are `notification / interaction / unknown`. Unknown means undetermined,
not a third business category. Notification subtypes are `verification / promotion / account_security / general`;
other categories require an empty subtype. Verification extraction applies only to the verification subtype.
Unknown is an analysis conclusion, not an entry: without a manual rule its effective entry is interaction, labelled
classification uncertain. Queued/failed classification and unassigned interaction mail appear there as individual
cards. Successful assignment replaces that projection with a conversation member, deduplicated by mail_id.
Processing states remain separately `queued / running / ready / failed / stale`.

1. Check manual sender rules before classifying committed originals and normalized bodies, without another browser task.
   All mail model jobs in this increment (classification, assignment, summaries, relationship checks and language
   presentation) exclude attachment contents and extraction results. Attachments remain downloadable for users.
2. Collect deterministic evidence: Auto-Submitted, List-Id, bulk indicators, address patterns, template structure,
   verification-purpose excerpts and reply relationships. Missing headers are unknown. Neither no-reply, a number
   nor an address alone decides category.
3. Use the existing Fast lane for strict structured classification, then validate evidence and input versions.
   Without a manual rule, uncertainty, conflicting evidence and model failure route to interaction with an explicit reason for user judgment.
4. The effective interaction entry enters existing candidate retrieval and append/new/pending assignment.
   Fallback routing does not prove a relationship: keep an independent card when evidence is insufficient rather
   than forcing append or creating a permanent conversation for every uncertain mail.
   Notifications publish mail projections without creating interaction conversations or entering their normal candidate pool.
5. Generate message summaries and language presentations asynchronously. A classified card remains visible while
   summaries are pending. Codes do not wait for history backfill or conversation summaries; insufficient body evidence uses interaction fallback.
6. Reanalyze changed evidence with durable, bounded retries and input-fingerprint deduplication; reuse originals.

| Content | Category | Rationale |
|---|---|---|
| Login code, registration link, routine login success | Notification | Account-flow information; entering a code or using a verification link alone is not an interactive mail matter |
| Advertising, “buy now / reply for an offer” | Notification | Generic marketing calls to action are not a concrete obligation to respond |
| Shipment status, paid receipt, ticket acknowledgement | Notification | Reports an event without a current confirmation request |
| Confirm contract/date, verify a bill, submit account documents | Interaction | A concrete request, including official, no-reply and platform senders |
| Quote request, invitation requiring RSVP, support follow-up, colleague discussion | Interaction | Requires reply, decision or continued communication |
| Routine login notice with generic “if this was not you” | Notification | Conditional boilerplate does not establish a current incident |
| Detected anomaly requiring verification, appeal or account recovery | Interaction | Explicit incident and requested handling |
| “Confirmed / completed, thank you” in an existing matter | Interaction | Continuation or closure belongs with the exchange even without a new action |
| Insufficient evidence, or body only says “see attachment” | Interaction | Unknown with fallback; label attachment not analyzed / classification uncertain for user judgment |

When a mail contains both information and a concrete request, interaction wins, subject to the code and generic
marketing exceptions above. Interaction is a matter type, not a claim that each mail has an unfinished task.
This increment adds no task-completion state machine and does not recategorize mail merely because it was viewed,
answered or completed. Summaries may describe a requested response but cannot execute it automatically.
Human-sent informational forwards can be notifications. Sender identity, automation headers and native threads
are supporting evidence only. Link acknowledgements to related exchanges without forced membership.
Extract only the current verification action, excluding quoted old codes, invoice/order numbers and phone numbers.
Human quotation and ambiguous multiple purposes must not automatically enable quick copy.

Proposed output includes category, notification_subtype, purpose, service_label, evidence_refs, reason_code, uncertainty,
missing_context, requested_response (a description of the source request, never a responsibility assignment) and optional verification candidates with purpose/expiry evidence. Runtime verifies an extracted
code occurs verbatim in the referenced body. Ambiguous multiple codes/purposes disable quick copy while leaving
the body available. Mail content cannot override the contract, execute actions or allocate authoritative IDs.


## 3.1 Manual Changes and Sender Rules

Each mail offers Move to information notices / Move to confirmation and interaction. Explain that the action
changes this mail and remembers its sender for subsequent mail. Show the parsed From address, not just a display name.
Rules match an exact normalized full From mailbox within one owner, across that owner's receiving accounts.
Do not extend them to domains, aliases or Reply-To. Reuse existing address normalization: case-insensitive domains,
without stripping dots/plus suffixes or inventing local-part case equivalence. Missing/ambiguous From permits only
a per-mail change, with an explicit explanation that a sender rule cannot be created.

Precedence: per-mail manual choice > active sender rule > model result > interaction fallback.
A saved rule takes effect immediately. Future admissions recheck its revision at classification publication;
in-flight work must not overwrite it with stale results. Change the selected mail, not all historical mail from
that address. Other admitted mail stays unchanged unless individually edited. Provide a rule list, edit and
disable/delete actions. Another change for the same address updates the existing rule; the latest committed
revision wins. Deleting a rule restores automatic routing for future mail without undoing per-mail manual choices.
Use expected_version to reject stale concurrent edits.

Rules choose the main entry, not fabricated verification subtypes. Uncertain notification subtype becomes general.
If a sender has been manually set to notifications, its later interactive requests also follow that rule and show
“Classified using your sender rule” so the user can change it. Record mail/rule IDs, revisions, time and classification
source. Never alter originals, viewing receipts or existing conversation membership. Outbound mail addressed to that
sender does not match a From rule; owner replies follow their explicitly selected management conversation.

## 3.2 Conversations, Context and Current State

Organize interaction by matter, not a single conversation per address. One address can have multiple matters;
multiple participants can continue one matter. Retain stage 3 bounded candidate selection and version checks.
Use the current new body, supported reply relationships and committed historical bodies/summaries. Prefer the
associated conversation, then a limited related candidate set. Record actual evidence references and truncation;
never imply the full archive was read. Exclude attachments and old summaries derived from attachments, regenerating
body-only summaries where necessary to avoid indirect attachment analysis. Body references to attachments mean
“attachment not analyzed”; attachment changes do not trigger semantic reanalysis in this increment.

Separate category, local viewed/unviewed state and source-described confirmation/reply requests. Do not add a task
completion state machine, assign a responsible person or infer completion from viewing/replying. Closing replies
stay in the matter. Preserve directions, receiving sources, participants, downloadable attachments and originals
throughout the timeline. Existing memberships stay fixed; suspected duplicate/pending correction retains evidence
and links. Changing entry does not merge, split or move members.

## 3.3 Compose and Reply

Provide Compose in the window and Reply / Reply all on individual mail, including notifications. This is an
end-to-end delivery requirement; an existing low-level send capability does not prove popup support.
Choose a verified send-enabled mailbox and edit To/CC/subject/body. Replies default to the original receiving
mailbox and clearly show account changes. Bind reply targets to a specific original mail and provider locator;
prefill valid Reply-To, otherwise From. Reply all shows deduplicated recipients, excluding known owner addresses,
for user review. Preserve native reply relationships: a new mail titled Re: is not a reply. If the reply target
cannot be proved, keep the draft and report the issue instead of silently sending a new message.

Only the user's Send action executes delivery; classification, summaries and mail content cannot send.
Reuse owner authentication, sending switches and the existing execution lane. Initial editor support includes
plain-text compose, reply and reply-all. Keep attachment downloads; uploading attachments and rich-text editing
are deferred. Persist owner-isolated drafts with edit revisions and frozen send snapshots; retain content on failure.
Replying to a notice explicitly creates or links an interaction matter without changing the original notice membership.

Persist idempotency keys and send states against double clicks, refreshes and timeout retries. An uncertain remote
outcome is “send result pending confirmation”: reconcile Sent evidence before any resend, with no automatic duplicate
send. Explicit failure allows user retry. A success receipt does not claim recipient delivery. Associate actual send
evidence and subsequently captured Sent sources with the same management conversation; reconcile local sends and
captured copies without duplicate timeline cards. Missing native headers stay pending verification, never fabricate
Message-ID. Account switches retain existing binding isolation. Implementation and live-validation status are recorded in sections 8 and 9; implementation tests send no real mail.

## 4. Expiry Display for Captured Verification Mail

Persist purpose, source sending/receipt times, explicit duration/deadline, timing basis and extraction revision.
Protect code values with existing owner mail/file authorization; exclude them from list search, generic summaries,
logs and notification previews. Existing controlled model analysis may still process original code-bearing bodies;
do not claim the model never sees codes.

- Compute expires_at only from an explicit absolute deadline or a duration explicitly anchored to a reliable time.
  Display “not expired / expired according to the email”, not guaranteed usable.
- An unanchored duration, conflicting timezone/date or anomalous timestamp yields validity_unknown, without a countdown.
- No stated lifetime also means unknown; never invent a universal five- or ten-minute expiry.
- Usage, revocation and supersession cannot be established from arrival alone; a newer code does not invalidate an older one automatically.
- Retain expired cards in searchable history; expiry does not schedule model work or change viewing receipts.

Return server_now, expires_at and timing evidence. Derive countdowns from server time offset and recalibrate on
focus. Unknown validity is excluded from the explicitly unexpired filter. Default sorting is newest first with
clear state and accessible expired history.

Owner clarification on 2026-09-09: defer receiving timeliness and optimize it in future work.
This increment processes already captured mail. Faster polling, real-time receiving and dedicated verification
priority scheduling are not designed here and are neither implementation dependencies nor acceptance gates.
Continue to display verification expiry from supported mail evidence.

## 5. Language Consistency

Use existing `Language = zh | en` settings as the sole UI language source, with no separate mail language switch.

| Content | Behavior |
|---|---|
| Controls, categories, states, empty views, hints, accessibility labels | Existing dictionaries; update immediately |
| Dates, numbers, expiry | Current locale and existing application timezone convention; UTC storage |
| API/script errors | Stable codes mapped to localized copy; localized generic fallback, restricted technical details |
| Generated titles, summaries, category and concern explanations | Versioned presentation in the requested language |
| Original subject/body, addresses, proper service names, attachment names, originals | Preserve verbatim and label as original content |

Separate factual analysis from presentation. Cache generated presentations by owner + target + target_kind +
analysis_revision + language + prompt_version. Changing language requests missing presentations only, without
reclassification, reassignment, recapture or viewing changes. Initial analysis may publish a language-tagged base
summary; on opening, request the language selected on that device. GET reads only; explicit idempotent POST queues
missing presentations. Do not introduce a global owner preference that lets English and Chinese devices overwrite each other.

While missing, show a localized “Generating Chinese/English summary” state. An explicitly expanded, language-labelled
older version is optional; never silently show an English result as Chinese. Localize failures and permit retry;
originals remain accessible. Compare language and analysis dependencies on publication. Late English work may
populate only its English cache, never the Chinese view. Include output_language in prompts; user explanations
follow it, enums remain stable. Old English titles require actual regeneration, not relabelling. Avoid eager bilingual
regeneration of the entire archive.

## 6. Persistence, Queries and Compatibility

The following are proposed internal structures and APIs, not deployed interfaces.

| Record | Constraints |
|---|---|
| EmailClassification | owner/mail_id, model category, effective entry, source (manual/rule/model/fallback), notification_subtype/state, input_fingerprint, analysis_revision, rule revision, evidence, reason_code |
| EmailSenderRule / MailOverride | owner, normalized full From, target entry, revision, effective time/disabled state; independent per-mail choices |
| EmailDraft / SendIntent | owner, draft revision, sender binding, exact reply target, send snapshot, idempotency key, outcome evidence/state |
| EmailVerification | mail_id, protected code, purpose, deadline/timing basis, evidence and revision; no fabricated used status |
| EmailLocalizedPresentation | target kind/ID, analysis dependencies, language, title/summary/explanation, state and revision |
| Query projections | Category, language/state, per-mail unseen counts, pending reason, historical mixed-membership marker |

Atomically publish classification, projections and downstream intents, checking source versions and classification
revision. A classified notification without conversation_id is routed, not pending. Unknown/failed classification routes to interaction; unassigned mail is an individual card there.
Top-level pending only holds admission/parsing exceptions lacking display material. Store classification, relationship
assignment and parsing states separately, with identical routing/count semantics. Coordinate classification and assignment
for new/pending mail; reject stale new/append results. Implement Memory, default File with snapshots, PostgreSQL,
migrations and indexes together.

Proposed query/command changes:

- GET /api/email/notifications: subtype=verification|promotion|account_security|general (omit for all notices), mailbox/search/validity filters, stable cursor, language.
- Interaction queries return conversation and unassigned-mail item_type variants: extend the existing query or add
  an aggregate query, never fabricate conversation_id. Pending only exposes admission/parsing exceptions.
- Add per-mail classification changes and sender-rule list/update/disable commands with owner checks, idempotency
  and expected_version. Add draft save/read, send submit and outcome queries using existing send execution; GET never sends.
- Reuse per-mail source access; any added single-message detail endpoint uses owner authorization without local paths.
- Lists/details accept language and return presentation_language, state and revision. Bind cursors to filters/categories.
- POST /api/email/presentations/ensure: allowlisted target types, at most 100 IDs, language, owner checks and coalesced jobs.
- Extend reanalyze to classification/summary/relationship review; viewed remains an idempotent mail_id command.

Backfill classifications with a resumable, bounded cursor. Unclassified legacy data must not be silently treated as
interaction or disappear. Show legacy conversations as classification pending with historical access; classify new mail
before routing. Pure notification legacy conversations surface as individual notices while retaining a read-only
original conversation link. Mixed conversations retain their complete timeline and original membership; notification
cards can link to that history with a mixed/pending-correction marker. Entry badges count effective routes, including interaction fallback;
notifications in mixed timelines do not also count as interaction unseen mail. Share receipts for each mail_id.

Presentation categories may change with evidence, but existing conversation membership stays fixed. A notice
reclassified as interaction without prior membership can enter assignment; already assigned mail only updates
presentations/concerns. Rollback disables new views/dispatch and preserves records/sources. Do not let old pending
queries reclaim routed notices: stop new analysis first and use compatible queries or a forward repair to avoid old
workers creating conversations for notifications.

## 7. Delivery and Acceptance

1. Localization: audit visible copy/errors, implement presentation storage/queue, verify language switching and old English content.
2. Classification: strict evidence contract, all three backends, notification queries, pending semantics and bounded legacy backfill.
3. Window: navigation, manual sender rules, notice cards, expiry/copy, per-mail viewing, conversation context, compose/reply and mobile behavior.
4. Integration: frozen corpus review, recovery and performance qualification before deployment/live mailbox verification.

| Scenario | Required result |
|---|---|
| Open in Chinese, switch to English | Immediate UI consistency; matching generated language or explicit waiting, no default English leakage |
| Two languages/devices, out-of-order model completion | Independent caches, no stale overwrite, unchanged membership/viewing |
| Codes, ads, routine login notices versus confirmation/discussion | Route by notification/interaction intent, not official/human sender |
| Official document request, marketing CTA, anomaly verification, completed reply | Interaction, notification, interaction, interaction continuation respectively; no keyword-only rule |
| OTP, order number, quoted old code, multiple codes, human quotation | Quick copy only for clear current verification purpose; no guessing |
| Expired, unknown lifetime, abnormal timestamps, several codes | Honest state, no claims of usability/usage, preserved history |
| Captured code has an evidenced deadline before now | Display expired, never a fresh usable code; capture latency is not evaluated |
| Model outage, partial body, attachment-only request | Interaction fallback without manual rules, explicit reason, no attachment analysis or recapture |
| Manual edit, future same-address mail, multiple receiving accounts, deletion/races | Current/future mail follows exact From rule, no bulk historical rewrite or stale-model override |
| Two matters per sender, multiple participants, closing reply, owner merely CCed | Correct matters/full timelines, no responsibility assignment or moving closure to notices |
| Compose/reply/reply-all, double click, uncertain send, repeated Sent capture | Correct targets/recipients and native relationships, no duplicate send/display, recoverable drafts |
| Replay, concurrent assignment, restart, stale language jobs | Consistent backends, fixed membership, stale publications rejected |
| Legacy notice-only and mixed conversations | Stable IDs/membership/sources, no unreachable mail or duplicate unseen counts |
| Search, paging, filtering, reopen and expiry | Complete query scope, stable selection/receipts, no raw codes in logs/search |

Freeze at least 120 de-identified, reviewed samples: at least 30 each for interaction, non-code notices, verification and
ambiguous/adversarial cases, covering Chinese/English, three provider sources and human support. Report precision,
recall, unknown/interaction fallback rate and a confusion matrix separately; test manual rules separately from model quality. Initial release targets: at least 90% automatic coverage on
clear samples and at least 95% precision for interaction and notification routing. Additional release gate: at most 2% of required-interaction samples without manual overrides may be wrongly routed
to notifications; every ambiguous sample must use interaction fallback. Each card exposes a localized reason and
body evidence; rules and fallbacks identify their true source rather than claiming confident model classification.
Quick-copy extraction must have zero
false extractions on the fixed corpus and no human mail wrongly exposed as an automated copyable code. These are
unverified gates, not universal zero-error claims. Mock tests qualify state handling, not model understanding.
Engineering acceptance covers default File, Memory, real PostgreSQL, bilingual WebChat interaction and documentation checks.

## 8. Implementation and Validation (2026-09-09)

This records implementation-stage validation; the same-day remote deployment is recorded in section 10.
Implemented notification/interaction routing, uncertain/failed
interaction fallback, exact From rules, bounded legacy backfill, fixed conversation memberships,
body-only analysis without attachment extraction/OCR, verification evidence/details, localized
presentations and category navigation. Manual changes affect the selected mail and future admissions
from the exact sender; rule management supports pagination and disabling. Source excerpts, requested
responses, service labels and purpose use the current presentation language. Late language or source
versions cannot replace the current display.

Notification queries support subtype, validity and full-scope mail/unseen counts. Interaction counts
include assigned and standalone interaction mails, using the source/body mail-search scope rather
than counting conversations. Cursors bind owner and filters. Expiry starts a fresh validity page scope
without dropping the selected detail, and focus recalibrates server time. Historical mixed matters
are labelled without moving members or resetting viewed receipts. Replies to notices include bounded
original-body context without moving the original. Pending retains admission/capture exceptions with
no original; explicit recovery respects receiving switches and never enables them.
Known verification codes are masked in ordinary lists and classification/expiry descriptions,
including purpose and evidence excerpts; originals and explicit code reveal retain their detail paths.
Expiry waiting is segmented across the browser timer limit, so long validity periods still refresh.

The window connects durable drafts, compose/reply/reply-all, To/CC, outcome reconciliation, paginated
captured Sent-source selection, local send timelines and links to originals. Owner-confirmed source
association is distinct from a provider receipt. Section 9 defines sending evidence boundaries;
provider-native editor qualification must come from actual pages, not mock tests.

Engineering checks pass: full Go test/build/vet, scoped race, complete Store tests with real PostgreSQL,
118 WebChat tests, 232 Controller tests, provider contract parity and 18 proxy/Compose-related tests.
The default File backend was retested with 10,000 synthetic mails, 900 matters
and 1,000 jobs: list/detail p95 94.82 ms, conversation search p95 214.74 ms, restart 588.55 ms. These
Store measurements do not qualify complete HTTP/UI performance or real receiving throughput.

The real Fast model evaluated 120 frozen bilingual synthetic cases, 30 per group. Baseline coverage
was 97.78%, interaction precision 93.55%, notification precision 98.31%, required-interaction misrouting
3.33%, with two ambiguous cases not falling back and two output-validation errors: gates failed.
After general intent/code/missing-context corrections, the same corpus produced 120 valid outputs:
100% clear coverage and both-class precision/recall, 30/30 ambiguous fallbacks, 30/30 correct verification
extractions and zero false extractions, passing the gates on this fixed corpus.

This is a post-correction in-sample check, not a blind held-out or production-mailbox accuracy claim.
The profile was sparkclaw-product-v1, returning the sparkclaw-fast alias; the checkpoint was not pinned.
Baseline, recheck, prompts and hashes are retained in the
[evaluation metadata](evaluation/email-analysis-v2/qualification-metadata.json). Provider labels are
synthetic metadata, not browser qualification. Four separate real-model bilingual presentation smoke
cases pass with the v4 prompt; Chinese titles were manually inspected. Nonempty Chinese generated
fields containing no Chinese characters cannot publish ready. Exact redundant classification-enum
parentheses are removed only from generated Chinese explanations, preserving original evidence.
The archived raw smoke output precedes that deterministic cleanup. No real email was sent, receiving switch changed or remote
service rebuilt in this increment.

## 9. Compose Implementation Boundary (2026-09-09)

Owner-isolated drafts, optimistic edit versions, frozen send snapshots and per-attempt outcomes
are persisted in Memory, File and PostgreSQL. Draft lists use bounded, filter-scoped pagination.
The authenticated Send endpoint passes explicit compose/reply/reply-all mode, up to 100 combined
To/CC recipients, exact account address and captured original locator to the provider script.
Legacy single-recipient requests remain compatible. Empty subjects can be saved in drafts; sending requires a subject (up to 998 Unicode code points) and a body of at most 200 KiB. Scripts must prove the current account and
native reply editor linkage; an unproved target cannot fall back to a new Re:-titled message.
Only an explicit user Send action can invoke delivery; tests never send live mail.
Draft HTTP decoding and its proxy route allow up to 2 MiB of encoded JSON to accommodate escaping;
the decoded 200 KiB body limit is still enforced.

Concurrent clicks and repeated keys cannot invoke Send twice. Definite pre-Send failures preserve
an editable draft; interrupted/uncertain outcomes remain fenced. Explicit reconciliation uses
the same frozen invocation in a read-only script journal/Sent-evidence path, never resends, and
retains unknown when evidence is insufficient. A provider receipt confirms submission, not delivery.
Existing provider drafts cannot be overwritten. Settings-language messages distinguish existing-draft
conflicts, content/Send-control verification failures and uncertain outcomes.

A confirmed local send creates or joins the selected management matter using its immutable user
snapshot and receipt. It is marked as a local send with source_pending until an actual source arrives;
no RFC Message-ID, original file or native identity is fabricated. An actual returned provider ID
uses the canonical captured-mail identity, so later Sent capture upgrades the same card. Replying
to a notice records the original mail link without moving its membership. Message and matter
summary jobs are queued from committed body-only evidence.

When native identity is unavailable, the owner can explicitly select a fully captured Sent source.
The endpoint verifies owner/mailbox/direction/capture and rejects a conflicting existing matter.
This association is labelled owner_confirmed_capture, distinct from provider_receipt. Superseding
the provisional card preserves viewed receipts, source subject/participants/headers and existing
manual classification. Repeating the same confirmation is idempotent; changing a confirmed source
is rejected. Native reply/editor behavior still requires actual provider-page qualification: safe
proof failure is not evidence that every live mailbox layout has been qualified.

Provider-page preparation evidence is retained separately in
[native-prepare.json](evaluation/email-analysis-v2/native-prepare.json). It records each provider/mode,
account and original-message proof, private-slot fills, recipient/content hash readback, and owned-draft
and tab cleanup. The harness prohibits the Send selector; preparation passes do not establish dispatch,
receipt detection or delivery. Existing user drafts remain protected, and a protected-draft refusal is
reported separately from a successful preparation.

| Provider | Compose | Reply | Reply all |
| --- | --- | --- | --- |
| Gmail | Real private-slot preparation passed | Passed, including changed subject and original linkage | Not qualified: no provably clean target in the final bounded run |
| QQ Mail | Real private-slot preparation passed | Passed with original linkage | Passed via the actual top-toolbar Reply All control |
| Outlook | Real private-slot preparation and discard passed | Not qualified: available Inbox/Sent candidates contain existing drafts | Not qualified under the same existing-draft protection |

These are preparation results, not live sending results or claims about every mailbox layout.
The Outlook read-only page had no visible reply editor, so native reply-editor structure could not
be additionally qualified without opening an existing draft. QQ recipient inputs can be replaced
by React after chip deletion; controls are revalidated and rebound before each recipient fill.
QQ Close can save a draft and is not treated as discard. The uniquely tagged final test draft was
deleted through the native checkbox/confirmation flow and its disappearance verified. Three earlier
fixed-test-subject candidates lacked sufficient ownership evidence and were preserved; the report
does not claim that every possible test draft was deleted. Production cleanup likewise does not
delete a QQ draft without proved ownership and a verified native discard path.

## 10. Remote Deployment (2026-09-09)

Following the owner's rebuild request, `npm run start:remote` rebuilt and started the application
images after remote-profile/Bridge preflight. Gateway `153bf1fc7a35` and WebChat `1a417cb3894a`
are running with existing PostgreSQL volumes. All five application services passed startup checks;
WebChat, health and readiness return HTTP 200 with external models and PostgreSQL state.
The host Controller was restarted to load the current script registry; its smoke and provider
contract parity pass. The persistent browser process remained running with Bridge 1.0.22.
Fast, Embedding, Guard and OCR model inventories return HTTP 200; this is reachability evidence,
not a new semantic or end-to-end sending qualification. Receiving settings were not changed and
no explicit Send was executed. Section 9's native qualification/cleanup limitations still apply.
