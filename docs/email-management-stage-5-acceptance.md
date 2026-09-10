# Stage 5: System Acceptance And Enablement

> Language: English | [简体中文](../zh-cn/docs/email-management-stage-5-acceptance.md)

Current follow-up design (not implemented): [Classification and event conversations](email-management-classification-v3.md). The owner confirmed event as the smallest conversation unit, purpose-based event titles, no message/conversation summaries, and category → event list → complete detail panes. It supersedes conflicting proposals below for future implementation; historical implementation and deployment records remain unchanged.

Implementation contract, 2026-09-08. Code and engineering checks are in the worktree;
real-provider and semantic release gates remain separate. See the
[implementation report](email-management-implementation.md). Qualify the [whole system](email-management-design.md),
not just successful script receipts.

2026-09-09 amendment: automatic receiving now uses same-page collection batches
and a default 20-minute idle after each round. Per-mail queues serve local parsing
and model work. Page recovery, source reuse and read ordering follow the
[stage 2 update](email-management-stage-2-intake.md).

## Matrix

Grouping examples assume the stated continuation evidence is available unless
the row explicitly tests missing, conflicting or delayed evidence.

| Scenario | Required system outcome |
|---|---|
| New counterpart mail on each provider | timer, verified original/attachments, summary, new conversation and visible popup |
| Two unrelated topics from one address | distinct persistent conversations |
| Multiple replies on one topic | reused conversation ID, individual synchronization and no duplicate cards |
| Thread with read/Sent/draft items | bounded non-draft backfill with explicit gaps |
| Same topic across receiving accounts | shared conversation and distinct provenance |
| New participant / changed topic | address/thread rules do not mechanically force membership |
| Concurrent related arrivals | stale epoch rejected and candidates re-evaluated; no replay of an obsolete new decision |
| B/C assigned separately before parent A | original conversation IDs/members remain; evidence can surface suspected duplicate/pending correction on both |
| New thread read before first scan | recent-inbound lane admits it regardless of unread state |
| Pause longer than overlap / more than 50 arrivals | resume admitted boundary across batches, no silent time-window or page truncation |
| Account switch and return | old browser work paused/fenced, sources isolated, same-account identity and progress recovered |
| Automatic read then process exit | durable member/thread jobs recover without unread dependence |
| Files written then publication failure | no unadmitted UI entries; attempt reconciliation |
| Model outage / unsupported attachment | continued intake, accessible sources and explicit analysis coverage |
| New members during analysis | stale summary cannot replace current data |
| Improved attachment extraction / resolved parent without membership changes | dependent summaries/checks refresh durably; crash during fan-out loses no intent |
| Popup close / Gateway restart / reopen | background recovery, stable IDs and viewing state |
| Viewed batch with new arrivals / unloaded history | only submitted mail IDs acknowledged; historical 101 stays unseen when visible 102 is acknowledged |

Record each provider's actual coverage separately. Use reviewed semantic samples
and error categories rather than a single lucky model output. Classify missed
discovery, wrong capture, duplicate admission, wrong assignment, unsupported
summary claims and state projection errors by responsible stage.

## Initial Release Gates

These are acceptance targets, not measured results or production latency promises.
Freeze corpus IDs/hashes, expected outcomes, model/prompt/parser versions, ordering,
runtime configuration and host before qualification; do not tune on the scored
cases or lower thresholds to pass a failing run. Each gate must pass separately.

| Gate | Required evidence and threshold |
|---|---|
| Identity and state | Zero wrong-owner/account admission, wrong originals, duplicate mail records, lost admitted jobs, stale-current publication, unintended membership moves or acknowledgements of unsubmitted mail IDs in the frozen corpus and fault cases |
| Assignment with sufficient evidence | At least 100 reviewed decisions covering stage 3 cases; three runs with recorded distinct seeds/orders, each with correct append/new at least 95%, false merge at most 1%, false split at most 5% and pending at most 5%; all rates use the full sufficient-evidence decision count, including abstentions |
| Missing/conflicting evidence | At least 20 separate reviewed cases, including B/C before A, in each run; at least 95% appropriate pending/concern outcomes. Preserve established membership in every case. Do not count retained duplicate conversations here as a forbidden automatic-repair failure; report their count and missed/false concerns separately |
| Summary grounding | Review at least 100 individual and 20 conversation summaries per model run: zero invented critical amounts/dates/commitments or unresolved evidence references; at least 95% factual claims supported; every known extraction/history/input-truncation gap explicitly disclosed |
| Real idle intake | Each provider separately: 10 in-scope new mails, including 5 read before discovery, with empty initial queue and healthy browser/Store; all durably discovered within two 60-second ticks of observable availability, originals visible within 10 minutes of admission. Provider/account lock contention and failures are recorded and cannot be omitted from the report |
| Recovery and refresh | With a test lease of 60 seconds and tick of 60 seconds, abandoned runnable jobs reclaimed within 3 minutes after dependencies recover; after final input publication and recovery, each small qualification conversation reaches a fresh summary or explicit exhausted-failure state within 10 minutes |
| Capacity and UI | On File and PostgreSQL: 10,000 admitted mails, 1,000 queued jobs, 30-minute model outage, restart and a bounded-capacity overflow attempt; zero loss of admitted work or boundary advancement over unadmitted candidates. On the recorded host, list/detail queries have p95 at most 1 second over 100 requests; after a commit, the active popup reflects state within two 5-second poll intervals |

For assignment metrics, false merge means append into an unrelated topic; false
split means new despite a supported existing continuation. The incomplete-evidence
set is scored separately, so pending cannot inflate accuracy by removing hard
cases from a denominator. Record counts as well as percentages and check all
three runs, not their best or pooled average. Exact identity/state gates use
deterministic assertions; semantic outcomes require reviewed real model outputs.
Capacity/recovery may use bounded fake adapters to inject faults but do not prove
real provider throughput. Report actual capture/model throughput and pending age
alongside these gates; latency outside the stated workload remains unqualified.

## Engineering Checks

Run affected Controller tests and generated-contract checks, Gateway build/test/vet,
three-backend Store contracts plus real PostgreSQL integration, frontend tests/build
and bilingual docs. Fault tests cover expired leases, unknown command outcomes,
process exit and changed inputs. Future DOM compatibility tests are not required.
Check real headers, body/attachment bytes, mail/conversation IDs and final UI,
not only success logs.

## Enablement

Exercise isolated qualification data first, then enable configured accounts.
Resident service deployment belongs to implementation, not this design delivery.
Default File configuration must work alongside PostgreSQL. Private test mail is
not automatically imported; existing product captures use stage 1 explicit
verified admission.

Pause new discovery/browser claims per mailbox without deleting local records;
retain discovery boundaries and apply the stage 1 binding fences on account changes.
Analysis retries and
source retention are independent; model failure does not redownload mail.
Persistent failures remain visible without blocking unrelated work. Observe
scan/backfill intervals and gaps, capture/analysis backlog, pending age, assignment concerns, retries
and last success through bounded IDs/errors rather than raw mail logs.

Stage reports distinguish implementation, real qualification, remaining gaps and
deployment. Update current architecture/development guides after acceptance;
do not advertise this draft as shipped. Sending/editing, contact merging and
bulk conversation merge/split remain separate requirements.
