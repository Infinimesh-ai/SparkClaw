# Cross-round mailbox read reuse

Language: English | [简体中文](../zh-cn/docs/email-cross-round-reuse.md)

## Scope

This extends the [first performance optimization](email-sync-performance-20260916.md): that version reused a task page within a batch, but recreated the page and CLI connection for the next poll. The new path keeps a bounded, Controller-owned read page and connection between successful time-range collection rounds. Sending, manual login and generic browser sessions do not use this pool.

Each provider has at most one retained lease (QQ, Gmail and consumer Outlook: at most three). Reuse requires the same owner scope, mailbox account, browser credential generation/token and registered script source checksum. The idle expiry is 30 minutes; the maximum lease age is two hours. Expiry or an identity change closes the old owned page and connection before creating another. Restarting the Controller clears this in-memory pool. Pausing receiving stops scheduled mail requests but does not immediately close an idle page; the idle timer still applies. A mailbox website may generate its own background traffic.

## What each round does

1. First round: connect, create the owned background page, navigate and confirm the managed Reader/account. Subsequent rounds renew the invocation deadline and check ownership, actual origin, document nonce, Reader capability and account without navigation.
2. Reset invocation-local Reader state. Previous rows, original Blob URLs/bytes, query replies and pagination tokens cannot answer a new round. Learned native transport templates remain available. Outlook fences late replies from a previous range query.
3. Run the new time interval query and carry only eligible exact failed-message identities. Fetch missing originals, hash and publish local files with the existing recovery journal, then return the receipt. Original requests remain sequential.
4. After a complete, failure-free result, reset transient state and release the active reservation while retaining the idle page/connection. A cancelled or failed round is not retained as a reusable session.

Each new business round still has a new invocation identity. A repeated invocation may replay its verified durable receipt; this is different from a new poll and does not demonstrate incremental discovery by itself. The [timeline rules](email-timeline-incremental-sync-design.md) remain unchanged, including two qualified message failures, operational-error handling and persistent warnings.

Account/ownership/origin checks have not been cached away. A stale page/connection may be rebuilt once only before the provider handler starts. Failed provider requests are not silently reissued within the same round. Exclusive validation, login, sending and generic browser access drain idle reads first; shutdown waits for active reads and then closes the pool. Cleanup failures retain a fenced lease and recovery metadata until owned-process cleanup succeeds.

## Avoiding repeated full admission probes

At this optimization's original deployment, the intake-only admission cache expired after 60 seconds, shorter than the then-normal 20-minute polling interval. Its lifetime was extended to 30 minutes. The subsequent [one-minute scheduler](email-timeline-incremental-sync-design.md#19-one-minute-cadence-and-single-flight-refresh) changes the normal idle interval to 60 seconds without reducing this cache lifetime. A newly verified, complete, failure-free time-range result may renew cache freshness, while retaining the original login-proof timestamp. Cache hits alone, old journal replay, partial results and failures do not renew it. Local credential generation and setting/account bindings are still checked. First use, changed credentials/settings or a gap beyond the expiry requires a full probe. Send admission is unchanged.

This removes repeated cold setup during healthy polling; it does not make every future call a warm call. Browser restarts, page reloads, idle expiry, the two-hour age limit and exclusive operations legitimately cause another cold setup.

## Acceptance method

Use the installed managed Reader and the dedicated logged-in browser. Compare pool-disabled cold reads with pool-enabled warm reads against the same frozen IDs/intervals. Before each measured warm case, perform an empty query under the same owner scope without any target-original fetch. Time the following fresh 0/1/N-original case, then check replay and capture hashes. The workload is an empty time-range query plus exact missing-original targets, not a naturally arriving burst. No additional browser observation is inserted in the measured path.

The stopwatch covers the Go collection runner, browser control, original transfer, local capture publication and page reset/cleanup. Independent evidence verification, full MIME parsing, database intake, classification and model analysis are outside it. These small-message samples are individual observations, not P95, large-attachment or long-duration guarantees. Simulated 20-minute admission-cache tests are not a real-time 20-minute soak.

Outlook retains the upstream native-UI completion policy and 500 ms settling behavior: the earlier fast-completion shortcut remains withdrawn.

## Same-version cold/warm results

| Provider / originals | Pool disabled, cold | Cross-round warm | Reduction | Original bytes |
| --- | ---: | ---: | ---: | ---: |
| QQ / 0 | 4.418 s | 1.934 s | 56.2% | 0 |
| QQ / 1 | 5.293 s | 2.713 s | 48.7% | 45,489 |
| QQ / 11 | 13.296 s | 10.294 s | 22.6% | 342,051 |
| Gmail / 0 | 10.188 s | 2.157 s | 78.8% | 0 |
| Gmail / 1 | 12.426 s | 4.654 s | 62.5% | 69,491 |
| Gmail / 11 | 22.196 s | 15.127 s | 31.8% | 631,014 |
| Outlook consumer / 0 | 11.924 s | 6.099 s | 48.9% | 0 |
| Outlook consumer / 1 | 13.996 s | 13.585 s | 2.9% | 14,656 |
| Outlook consumer / 2 | 25.247 s | 22.648 s | 10.3% | 24,390 |

All 18 cold and 18 warm fresh/replay cases passed. Warm target replays acquired zero new originals: QQ 1/11 replay took 1.176/1.236 seconds, Gmail 1.142/1.200 seconds, Outlook 2.229/2.152 seconds. Every measured warm round omitted attach/navigation. Warm preload queried an empty interval and did not prefetch those originals. Private evidence is retained under `/tmp/sparkclaw-incremental-fix.b4GNWC/` as `pooled-cold-performance.jsonl`, `pooled-warm-performance.jsonl` and corresponding stage files.

Outlook's single-original improvement in this same-version comparison is only 2.9%. The preceding report's 19.096-second sample must not replace the new 13.996-second cold sample to advertise a larger benefit. These observations include provider/UI variance; they do not isolate a causal per-stage savings budget.

## Subsequent same-version command-batching comparison

All three daemon commands remain: pre-read ownership/origin check, awaited read, and post-read check. One bounded short-lived Node helper replaces three CLI client starts. Only the pinned standard Linux CLI entry, QQ/Gmail read operations and registrations without a custom signed-out predicate qualify. Outlook, send and unsupported/custom entries retain their prior path. Originals remain serial.

| Provider / originals | Batching off, warm | Batching on, warm | Reduction |
| --- | ---: | ---: | ---: |
| QQ / 0 | 1.944 s | 0.386 s | 80.1% |
| QQ / 1 | 2.770 s | 0.642 s | 76.8% |
| QQ / 11 | 10.610 s | 2.648 s | 75.0% |
| Gmail / 0 | 2.094 s | 0.552 s | 73.6% |
| Gmail / 1 | 4.049 s | 1.335 s | 67.0% |
| Gmail / 11 | 15.437 s | 9.054 s | 41.3% |

Each mode passed twelve fresh/replay cases using the same frozen originals and byte counts as above; replays acquired zero originals. The initial batching-off QQ inventory attempt failed during attach at 15.017 seconds, before navigation/provider handling. That failure is retained and excluded from the timing comparison; a later explicit QQ run passed. Private evidence: `/tmp/sparkclaw-read-batch-ab.fN8T28/{off,on}/` stage/performance files. These small-sample collection timings exclude classification/models and do not establish P95 or provider-wide guarantees. See [timeline section 19](email-timeline-incremental-sync-design.md#19-one-minute-cadence-and-single-flight-refresh) for cadence acceptance.

## Earlier pool-only continuous-round evidence

Each provider passed five consecutive empty queries under one owner scope: the first was cold and the following four reused its page. All 15 passed; every warm stage trace contained resume/handler/park and no attach or navigation.

| Provider | First cold round | Following four warm rounds |
| --- | ---: | --- |
| QQ | 4.856 s | 1.904 / 1.928 / 1.861 / 1.910 s |
| Gmail | 10.299 s | 2.160 / 2.101 / 2.136 / 2.146 s |
| Outlook consumer | 14.530 s | 10.852 / 5.565 / 5.939 / 5.423 s |

Outlook's slow warm sample spent 8.677 seconds in the provider handler, with about 1.09 seconds each in resume and park. It did not rebuild the page. Reuse removes repeated initialization but does not eliminate provider/UI latency variance. The continuous tests were back-to-back, not real 20-minute waits.

The remaining original-preparation stage measured 0.769/8.376 seconds for QQ 1/11 originals, 2.436/12.905 seconds for Gmail 1/11, and 8.251/14.108 seconds for Outlook 1/2. Corresponding local write/hash/journal/publication totals were 5.0/16.6, 3.8/19.9 and 4.9/5.7 milliseconds. Original preparation includes browser control and provider preparation/transfer, not just wire throughput. Durable local publication was retained; deleting it would not recover the multi-second cost seen here.

## Validation and deployment

Final automated checks passed: Controller 401 tests plus one existing environment skip; full Go tests/build/vet and focused email race tests; WebChat 135 tests/build and 707 matching translation keys; 82 bilingual document checks; generated userscript consistency. Cleanup fault injection covers failed reaping, partial initialization, provider failure, stale-page cleanup and cancellation during parking. Simulated admission tests cover repeated 20-minute intervals and expiry without changing the original proof timestamp.

Deployed on 2026-09-16 at approximately 18:37 Asia/Shanghai: Gateway `503d8003e774`, WebChat `0e16058fee0b`, host Controller PID `1769212`. The managed Reader was actually updated through the component installer earlier in this acceptance, requiring one dedicated-browser restart; its profile/login data were preserved and all three live tests subsequently passed. The final application deployment did not restart that browser again (PID `1620182` stayed unchanged). Readiness, external/PostgreSQL mode, container-to-Controller smoke and WebChat HTTP 200 passed. QQ receiving remained enabled and Gmail/Outlook disabled. The last observed QQ production round was already complete with no current timeline error; the table above is isolated live-runner evidence, not a newly measured post-deployment production/model round.

Temporary qualification Controllers shut down cleanly and their runtime directories/socket were cleared. Private benchmark artifacts were retained. No commits or pushes were made. Microsoft 365, large attachments, long-duration availability/P95 and destructive power-loss tests remain outside this acceptance.
