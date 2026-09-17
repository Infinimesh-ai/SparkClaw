# Mail synchronization performance — 2026-09-16

Language: English | [简体中文](../zh-cn/docs/email-sync-performance-20260916.md)

## Scope and measurement

This acceptance compares the same logged-in dedicated browser, fixed message identities and time intervals before and after optimization. Each fresh case has a new isolated owner/workspace and genuinely acquires its originals. The workload is one empty native time-range query plus 0, 1 or N exact missing-original targets, exercising the normal collector and local capture path. It is not a naturally arriving N-message burst. No messages were sent or marked read, and production receiving switches were not changed.

Elapsed time covers the Go runner's browser task, page preparation, collection, local publication and cleanup. Independent capture/hash verification is outside the stopwatch. It excludes later full MIME parsing, database intake, classification and model analysis. Earlier 16.5/26.8/29.8-second diagnostic results included extra browser observations and are not used as the comparison baseline here. These are single observations, not P95 or throughput guarantees.

## Implemented changes

1. For trusted, explicitly awaited QQ/Gmail read operations only, stop waiting for unrelated page traffic and the extra 500 ms settle delay. A task-scoped environment gate and Controller-owned code marker are both required. Outlook native-UI reads, sending and unmarked operations retain upstream completion behavior.
2. Validate actual page URLs before and after execution within the same browser call, retaining independent task ownership, tab topology, origin and account checks. Remove one redundant renderer round trip.
3. Combine QQ/Gmail local Reader/account readiness and the one time-range request into one call. Only local pre-request readiness may be retried; failed provider requests are not automatically repeated inside that call.
4. Create staging directories only when a new original is required. Preserve hash checks, durable journals, atomic publication and exact-ID replay.
5. Fix valid folded Message-ID whitespace and GB2312/encoded Chinese sender parsing. Keep original EML bytes unchanged; do not discard the two previously rejected QQ samples.

At this measurement point, only one batch reused a task page; cross-poll page/session caching was not implemented. The subsequent [cross-round reuse optimization](email-cross-round-reuse.md) supersedes that limitation. Parallel original downloads remain unimplemented. Original preparation includes provider response transfer and browser-to-host transport; the separate local-write timer must not be presented as the entire network download cost.

## Verified observations

| Provider / fresh originals | Before | After | Original bytes |
| --- | ---: | ---: | ---: |
| QQ / 0 | 10.333 s | 4.353 s | 0 |
| QQ / 1 | 12.719 s | 5.147 s | 45,489 |
| QQ / 11 | Failed: only 9/11 accepted | 12.946 s, 11/11 accepted | 342,051 |
| Gmail / 0 | 16.084 s | 9.782 s | 0 |
| Gmail / 1 | 18.944 s | 10.534 s | 69,491 |
| Gmail / 11 | 46.647 s | 22.918 s | 631,014 |
| Outlook consumer / 0 | 13.343 s | 11.901 s | 0 |
| Outlook consumer / 1 | 22.584 s | 19.096 s | 14,656 |
| Outlook consumer / 2 | 29.379 s | 26.302 s | 24,390 |

QQ single-mail time fell 59.5%; Gmail single-mail time fell 44.4%, and Gmail's 11-mail batch fell 50.9%; Outlook single-mail time fell 15.4%. No full-batch QQ speedup percentage is valid because its baseline rejected two legitimate originals. QQ samples range from 7,176 to 53,894 bytes; Gmail samples from 15,216 to 83,376 bytes; Outlook's two samples are 9,734 and 14,656 bytes. Large attachments are not represented.

QQ's optimized single-mail 5.147 s consists approximately of 2.94 s connection/page preparation, 0.76 s range discovery, 0.86 s original preparation, under 0.01 s local write/hash/journal/publication, and 0.57 s cleanup. For all 11 QQ originals, discovery took 0.74 s, original preparation 8.63 s, and measured local write/hash/journal/publication about 0.012 s. Disk durability was not the multi-second bottleneck in these samples; it has not been removed.

All three providers' same-batch replays verified the existing originals and acquired zero new originals. Replay still paid task/page setup in this intentionally forced Controller benchmark: QQ 11-mail replay 3.737 s; Gmail 11-mail replay 6.760 s; Outlook two-mail replay 7.767 s. Zero downloads does not mean zero total runtime.

## Separate model stage

A read-only check of the previous production QQ event found four model-operation timers totaling 5.032 s (subject classification 0.937 s, body confirmation 0.838 s, assignment 1.350 s, event summary 1.907 s). Source commit to final summary was 6.736 s including queue/local processing. These timers include capacity waiting and validation, not pure inference. This is a different production observation, not a post-optimization full-chain benchmark; model latency was not optimized in this change.

## Evidence and limits

The initial optimized Outlook matrix did not pass: range discovery timed out before acquiring originals, once in the empty case and again in the two-original case on rerun. A separate five-empty-query diagnostic passed three and failed two. Failure-only observations showed an empty, connected search input and no search resource request: failure occurred before native search submission, not during original download. The deeper UI timing cause is not established. Outlook's fast-completion optimization was withdrawn; it retains the original 500 ms settle and upstream completion policy, while keeping the redundant origin-evaluation reduction. Pre-withdrawal successes/failures are retained separately and are not mixed into final measurements.

The final three-provider matrix passed all 18 fresh/replay cases. Outlook's final compatibility policy also passed five consecutive new-task empty queries at 11.904/11.950/11.963/11.955/11.968 s. This is bounded stability evidence, not a long-running availability guarantee. Final automated validation passed full Go tests/build/vet, focused mail race tests, Controller 376 tests plus one existing environment skip, WebChat 135 tests/build/707 translation keys and 81 bilingual document checks.

Deployment completed at approximately 17:55 Asia/Shanghai: Gateway `b64a593df073`, WebChat `0e16058fee0b`, refreshed host Controller. Application readiness, external/PostgreSQL mode, container-to-Controller smoke and WebChat HTTP 200 passed. The dedicated browser PID stayed unchanged; QQ receiving remained on and Gmail/Outlook off. QQ retained its completed watermark and had no current timeline error. The timing table is the isolated live benchmark above, not a newly completed post-deployment production round. No commits or pushes were made.

Opt-in reproduction: `SPARKCLAW_TEST_EMAIL_PERFORMANCE=1` selects `TestTimelineLivePerformance` through `scripts/qualify-playwright-email.sh`. Private fixed targets, intervals, stage timings and capture artifacts remain outside Git under `/tmp/sparkclaw-incremental-fix.b4GNWC/`; public reporting contains no account identifiers, bodies or credentials. Failed attempts are retained rather than silently removed from the result.

Incremental semantics, exact failed-ID carryover, operational-error distinction, two-qualified-failure suppression and persistent warning behavior remain unchanged. Microsoft 365, real >50-message bursts, multiple live messages in the same receipt second, large attachments, long-duration P95 and destructive power-loss tests remain unqualified. See the [timeline design](email-timeline-incremental-sync-design.md).
