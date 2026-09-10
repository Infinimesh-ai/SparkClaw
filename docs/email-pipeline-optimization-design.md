# Email Pipeline Optimization: Network Capture, Incremental Sync and Summaries

> Language: English | [简体中文](../zh-cn/docs/email-pipeline-optimization-design.md)

Status: 2026-09-10, implementation in `codex/email-pipeline-optimization`. Deployment/first-enable boundaries, removal of automatic unread scans, login recovery alerts, source-only summaries and collapsed local body loading are implemented and tested. **The new managed network readers and full provider pagination remain in qualification; the branch has not been deployed.** The remaining sections describe the target behavior; they are not blanket acceptance claims.

## 1. Scope and Precedence

Cover scripts → Tampermonkey → durable originals → incremental scheduling → classification/event assignment → summaries → reading UI. Build Outlook and Gmail network readers using the existing QQ reader as a reference, import all three into Tampermonkey, and fetch only previously uncaptured mail after initial organization. Restore summaries and make bodies available on click.

This supersedes the future requirements in [classification v3](email-management-classification-v3.md) that excluded summaries and capture scripts and presented complete bodies by default. Historical implementation/deployment records remain valid. It also supersedes the future activation/unread-based scope and pre-boundary thread backfill in [Stage 2](email-management-stage-2-intake.md). Keep notification/interaction routing and event-based conversations.

## 2. Verified Baseline

| Area | Current evidence | Required work |
|---|---|---|
| QQ | `scripts/email/userscripts/qq-internal-reader.user.js` 0.2.0 reads an ordinary folder's first page through `/list/maillist` and originals through `/read/readmail?func=5` | Production pagination, identity, checkpoints, continuous sync and pinned/grouped-row coverage |
| Qualification | [QQ trial](qq-internal-api-trial.md) proves small EML samples and targeted correspondence search, not general coverage or execution through installed Tampermonkey | Independently qualify all three providers; do not extrapolate QQ speed ratios |
| Gmail/Outlook | Existing browser capture is not equivalent to a QQ-style network userscript | Observe current authenticated web requests; implement and qualify each adapter |
| Provisioning | `configs/browser-components.json` and `scripts/browser_components.py` manage Tampermonkey and two AI exporters with UUIDs, hashes and native `jsonImport` | Add three mail scripts and generalize readiness checks; installer currently reads `tools/browser-userscripts/` |
| Intake | [Stage 2](email-management-stage-2-intake.md) has recent/unread lanes, interval continuation, overlap, durable page acknowledgement and recovery | Reuse durable progress; remove unread discovery and select by deployment boundary, receipt/change timeline and committed identity |
| UI/analysis | `emailPopup.tsx` has category/event/detail panes; `emailMessage.tsx` defaults bodies open without showing summaries. Some summary structures remain, while event policy constrains legacy jobs | Independent summary scheduling, previews and local body loading; do not simply re-enable coupled legacy jobs |

## 3. Readers and Managed Tampermonkey

Proposed managed entries are `qq-mail-reader.user.js`, `gmail-mail-reader.user.js` and `outlook-mail-reader.user.js`. Keep the QQ trial as evidence; choose one canonical production source under `tools/browser-userscripts/` rather than two editable copies.

Adapters share identity checks, bounded interval enumeration and pagination, individual thread-member discovery, stable-ID original reads, cancellation and explicit coverage/error results. QQ must qualify pinned/grouped rows and Sent; Gmail must qualify labels, individual thread members, multi-account identity and receipt-time semantics; Outlook must distinguish consumer/Microsoft 365 support, folder moves, identifiers, MIME and pagination.

Observe actual authenticated web requests before specifying endpoints. Gmail History API and Microsoft Graph delta are not implicitly available through web login; they may require separate OAuth. Use a change cursor only when actually available and verified. Otherwise use bounded time scans and deduplication. Unsupported coverage stays visible; never reconstruct HTML and label it an original EML.

Keep session credentials in the matched provider page. Bind every operation to the actual account and task; pause on account change or lost login. Allow only observed origins/operations with bounded requests, sizes and cancellation. Do not export Cookie/SID/tokens into results, logs or checkpoints.

Deliver actual original bytes through the existing Controller download receipt → Gateway verification → Store publication flow into owner-scoped email source storage. Verify task, mailbox, stable ID, size, hash and MIME before acknowledgement. Scripts do not upload to arbitrary localhost endpoints or own the authoritative sync boundary. Preserve original/attachment extraction, existing send/reply approval and separate remote-read behavior; measure read side effects. Models use subject/body, not attachments.

Add stable UUIDs, versions and hashes to the managed manifest and native `jsonImport`. Generalize readiness beyond a fixed count of two exporters. Qualification requires installed/enabled exact scripts, execution in the correct mailbox page, durable original publication and acknowledgement. CLI injection of identical source is insufficient. Verify Local/Remote install, upgrade, repeated deployment and browser restart; wait visibly for browser/login availability and resume old checkpoints.

## 4. Initial Organization and Incremental Sync

Initial capture includes only mail received after the user’s deployment time, regardless of read state. Persist `deployment_started_at` for the user’s deployment instance on successful deployment, in UTC with local-time display. Restart, upgrade, redeployment, intake activation, first login and reauthentication must not reset it. Mailboxes bound later use the same deployment lower bound with independent checkpoints. Remove unread discovery and unread-based capture priority: pre-deployment unread mail is excluded, while post-deployment mail read on another device is included. For existing installations use a verifiable original deployment record; if unavailable, use the first successful enable of email receiving, as explicitly confirmed by the owner. Persist that fallback once per owner and reuse it for later mailboxes; never substitute upgrade time.

Default scope is qualified ordinary inbound folders/labels. Available Sent members of encountered events remain within the deployment lower bound; thread references must not automatically fetch pre-deployment originals. Mark preceding history as outside scope rather than claiming complete correspondence. Exclude drafts, Spam/Trash and unqualified areas explicitly. Keep already stored originals. Any future pre-deployment history import is a separately specified scope extension. Prioritize fresh arrivals over the initial post-deployment backlog and show capture/analysis progress separately.

“Last fetch time” means the **last continuously enumerated interval whose originals are durably stored**, not button-click time, job start, latest sender Date or summary completion.

Persist state per owner/provider/mailbox/folder-or-lane/scope version: immutable deployment lower bound, completed boundary, fixed current upper bound, continuation, stable original identities/versions and receipts, retry items/gaps, and separate initial-backfill progress.

1. Verify account and acquire the mailbox's single capture lease; fix the scan upper bound.
2. Resume unfinished intervals first. For new scans use the committed boundary plus overlap, initially the existing 24 hours, calibrated by provider. The lower bound is `max(deployment_started_at, completed_through - overlap)`, or deployment time before first completion. Use verified receipt/change ordering for discovery and actual receipt time for deployment-scope admission, never sender RFC Date. Moving or changing a pre-deployment message later must not automatically admit it.
3. Check committed stable IDs and source versions. Unchanged originals skip download, parsing and all model work, including repeated labels/pages.
4. Fetch only missing originals or necessary repairs of genuinely changed/missing/corrupt sources. Commit each source and acknowledge each page; preserve failures for retry.
5. Advance the boundary atomically only after complete interval enumeration and durable sources. A proved empty interval may advance; partial pages/errors/rate limits may not. Analysis failures do not block capture progress.
6. Analyze new or changed inputs only. No new or changed source means zero model calls.

If coverage ended at 10:00 and a scan to 10:20 fails on a 10:12 original, do not declare completion through 10:20. Skip a successfully committed 10:15 original during retry and advance only after filling the gap and completing enumeration.

Enumerate all tied timestamps/pages or use verified stable ordering; `time > lastTime` alone loses mail. Preserve source mappings for folder-move identity changes, and do not use RFC Message-ID alone to force cross-account merges. Recover expired cursors from the committed boundary rather than now. After days offline resume the full gap, not only the latest 24 hours. Retain independent recent observation so a fixed historical interval does not hide new arrivals.

Prefer verified change cursors for late-visible old mail when available. Time overlap alone cannot guarantee arbitrary delayed visibility: report limitations and support targeted repair instead of recurring full scans. Keep the existing 20-minute idle interval initially. Manual sync coalesces into the same queue/checkpoints; userscripts do not start a second autonomous scheduler.

### Login Expiry, Entry Red Dot and Catch-up

When background sync/account checks confirm expired login, persist “reauthentication required” per mailbox and retain committed boundaries, continuation and retries. Pause authenticated requests for that mailbox; healthy mailboxes continue. Network timeout, rate limiting or an unavailable browser alone does not prove expired login.

Show a red dot on the email-window opening button (`EmailPopupEntry`) whenever any enabled mailbox for the current owner requires reauthentication. Subscribe or poll at page level even while the popup is closed; do not depend on popup-only hooks. Provide accessible text such as “Mailbox login expired. Please sign in again.” Keep this signal distinct from unseen-mail counts.

Opening the popup lists affected providers/accounts with a sign-in action and explains automatic catch-up afterward. Opening, dismissing, refreshing or clicking sign-in does not clear the dot. Verify the actual reauthenticated account against the original binding; signing into a different account neither clears the old account’s state nor advances its checkpoint.

After successful verification, automatically resume from the last complete boundary, or deployment time if no sync completed, through the current time. Include mail read elsewhere during the outage. Never reset to login time or restrict recovery to 24 hours. Reuse pagination acknowledgements, deduplication and retries; display catching-up progress and remaining failures separately from login success and analysis.

The dot indicates a required login action: clear each account’s expired state after verification, retaining the dot until all affected enabled accounts recover. Ongoing catch-up remains visible inside the window after the dot clears; catch-up failures show incomplete intervals and retry actions rather than misusing the login dot. Closing the popup does not stop catch-up. Persist alert/recovery state across refresh and restart.

Example: deployed September 10 at 09:00, synced through 10:00, login expired at 10:05, reauthenticated September 12 at 11:00. Keep the 09:00 deployment boundary, resume from the 10:00 completed boundary and fill the entire gap, including mail already read elsewhere; exclude pre-deployment unread mail.

## 5. Summaries and Reading

| Surface | Default presentation |
|---|---|
| Event list | Short event title, activity, unseen count and 1–2 sentence event preview |
| Event detail | What happened, latest developments and explicit requests, with source-mail links |
| Mail card | Parties/time and a 2–4 sentence summary retaining material amounts, dates, conclusions and requests; collapsed “View body” action |
| Pending mail | Independent per-mail summary while classification/assignment status remains visible; body/original stay accessible |

Generate summaries by default. Initial Chinese length targets are 80–200 characters per mail and 120–300 per event; simple notices can be shorter. Do not invent obligations or act on mail. Follow the selected language and distinguish queued, running, failed and updating states. Missing summaries fall back to subject/parties/status, not body excerpts disguised as generated text. Stale event summaries may remain visibly marked as updating; unavailable/revoked sources must not leave misleading source content accessible.

Load bodies from committed local originals/parsed data on click, cache locally, and omit full bodies from default list/timeline projections. Expansion does not contact the provider or model. Preserve plain text; any future HTML view sanitizes active content and blocks remote resources by default. Originals and attachments remain separately downloadable.

Keep viewing semantics: a mail card including its summary actually visible in the active detail viewport can acknowledge that mail. List previews, prefetch and generation cannot. Body expansion is not required and local viewing does not mark provider mail read.

Analysis must be one-way: original → parsed source → classification/event assignment and per-mail summary; fixed event membership plus original evidence → event summary; summaries → display only. Generated summaries must not feed candidate retrieval, classification versions or assignment evidence. Long events may use bounded extraction retaining original references, never recursive old-summary-only inference.

- Cache per-mail summaries by original content version, summary policy and language; event summaries by member IDs/source versions, policy and language.
- Viewing, paging, sync heartbeats and read receipts do not invalidate summaries. Reclassification without changed membership does not recompute mail summaries.
- Coalesce event updates within capture batches. Prioritize visible events and new mail over historical summaries.
- Admit one valid task per target/input. Initially allow one request plus at most one technical retry; expose failure and explicit retry, and fence stale results.
- Bound/chunk long inputs using model capacity, retaining dates, numbers, negation, requests and source references; disclose incomplete coverage. Treat mail as data.
- Build missing historical summaries once with resumable checkpoints, without recapture or forced reclassification. Use a new independent policy while keeping coupled legacy tasks fenced. Measure actual model calls, not queue iterations.

The larger one-time organization budget does not justify restoring summary → candidate → classification → summary cycles.

## 6. Delivery and Integration

Deliver in three stages: (1) production adapters and actual managed Tampermonkey execution/original ingestion for each provider; (2) incremental checkpoints, deduplication and recovery; (3) independent summaries, historical summary backfill, previews and on-demand bodies.

Likely areas include mail userscripts and Controller integration, managed component manifest/installer, Gateway emailmanagement/store/endpoints, WebChat email API/hooks/components/styles/i18n and their tests. Implement only in the email worktree. The other worktree owns AI export algorithms and third-party scripts; manifest, provisioning and readiness edits may overlap and require entry-by-entry integration retaining every managed script. The original task integrates results; this worktree does not merge itself into the main checkout.

No JingSi runtime or IMMS evidence-service contract change is proposed; other projects need no follow-up. Any later cross-project interface change requires an InfiniCenter decision first. Report commits, checks, qualification limits and integration notes when implemented, and update cluster status for observable changes.

## 7. Acceptance

Verify pre-deployment unread exclusion, post-deployment read inclusion, unchanged deployment boundaries across upgrades/restarts/late mailbox binding, and thread/overlap scope enforcement. Test the login dot with a closed popup, persistence across opening/refresh, multiple expired accounts, wrong-account reauthentication and ordinary network failures. Verify automatic full-gap catch-up after correct-account login without advancing progress prematurely.

Qualify each provider/account type with actual installed userscripts, covered folders, parseable originals, attachment extraction where present, and recorded byte/semantic differences. Exercise zero-new-mail repeated sync (zero repeated originals, records and model calls), >50 messages, tied timestamps, labels, pinned/grouped rows, already-read arrivals, partial failure, rate limits, restarts, account switching, multiple tabs, folder moves and old sender dates with new receipt/change times. Report unqualified late-visibility limitations.

Verify inbound and Sent additions within existing thread scope update only affected events. Use independent synthetic summary cases covering notices, interactions, multi-mail events, negation, dates, amounts, requests and long content; trace claims to originals and avoid mixed events. Report initial versus daily mail counts, original requests, actual model calls/tokens, retries and latency. After initial convergence, 100 refreshes and 30 minutes idle must not increase task versions or model calls.

Exercise collapsed bodies and local-only expansion, summary failure with accessible originals, keyboard/narrow-screen/language behavior and per-mail viewing. Verify repeated Local/Remote deployment and restart preserve exact script identities and working AI exporters. Engineering checks cover adapters, incremental state machines and memory/file/PostgreSQL recovery, summary dependencies/cache, WebChat interactions and build. Report synthetic tests separately from real-provider qualification.

This turn delivers documentation only: no Gmail/Outlook script implementation or installation, synchronization, inference or production mail-data changes.

## Implementation evidence (2026-09-10)

- Email management, automation, Store and Gateway Go suites pass. WebChat: 40 files / 128 tests pass; production build passes. Browser capture/read/page suites: 77 tests pass.
- Configured real model, synthetic correspondence only: message and event summaries retain the 240→260 price change, pending approval and the instruction not to order. This is a format/content smoke test, not a reviewed quality-corpus gate.
- SparkClaw dedicated browser: Gmail and Outlook login probes succeeded. Observed Gmail `/sync/u/0/i/bv`, `/i/fd`, `/i/s`, and Outlook startup/service responses. This evidence does not prove complete pagination or network original capture.
- Source summary dependencies contain original sources and membership only. A 100-refresh regression verifies stable generations and no summary-to-classification feedback. Summaries currently follow source language; language-specific caching remains a future requirement.
- Both page capture and thread expansion now exclude historical members using individual receipt timestamps. Missing timestamps remain explicit partial coverage, particularly for Outlook grouped rows; aggregate LastDeliveryTime is not assigned to every member.
- The one-time baseline is `.sparkclaw-deployment.json` in the workspace root when available, otherwise the first enabled mailbox baseline persisted per owner. Existing stored originals and boundaries survive reauthentication.
- Deployment lifecycle correction: tracked `.gitkeep` directories do not imply a prior install. Before service start, persist a fresh/legacy attempt based on actual Compose resources or Gateway state; only successful completion records the boundary. A failed fresh attempt stays fresh on retry. Seven lifecycle tests and five isolated Remote entrypoint tests pass.
