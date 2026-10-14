# ISCP latency optimization and native measurements

[简体中文](../zh-cn/docs/iscp-latency-optimization.md)

Measured on 2026-10-14 using the original installed Mac SparkX interface,
the existing Main identity, TEST1 public Relay and work2 Gateway. The final
Mac/work2 runtime is `3ddde02b`; TEST1 Relay is `f19847c3` with a 55-second
receive window. At that initial rollout, the other four Gateways retained their
existing binaries and passed original-interface compatibility checks with the
new Mac client. The subsequent work1 deployment is recorded below.

## Results

The before and after benchmarks each submit five independent `你好` conversations followed by one request
for an approximately 600-character Chinese description of a park after rain.
The benchmark uses the original composer, real model, helper and durable store.
Model configuration, Client identity and authorization remain unchanged.

| Measurement | Before | After | Reduction |
| --- | ---: | ---: | ---: |
| Greeting: first visible text, median of 5 | 20.948 s | 7.164 s | 65.8% |
| Greeting: complete local result, median of 5 | 18.070 s | 7.823 s | 56.7% |
| Greeting: durable delivery ACK, median of 5 | 23.308 s | 8.106 s | 65.2% |
| Long reply: first visible text, one sample | 23.968 s | 7.142 s | 70.2% |
| Long reply: complete local result, one sample | 22.109 s | 11.244 s | 49.1% |
| Long reply: delivery ACK, one sample | 29.312 s | 11.637 s | 60.3% |

Greeting completion ranges are 16.025–20.878 seconds before and 7.639–7.933
seconds after. Every measured request reaches `delivered`. The long reply changes
once before and nine times after (eight previews and the final result); outputs contain 679 and 617 stored characters,
respectively. Generated output and network conditions vary: this is a small
sequential production sample, not a percentile SLA or controlled counterfactual.

First visible text uses a renderer MutationObserver measured from composer
submission. Complete result and ACK use SQLite timestamps on the same Mac.
Before optimization, the result can exist in SQLite before the UI refreshes;
after optimization, temporary text can appear before the final durable result.
Do not subtract clocks from different machines or add overlapping RPC stages.

## What was waiting

The instrumented baseline adds timing only (`f91a4142`). Greeting execution on
the Gateway takes about 6.918 seconds, while complete local results take a median
18.070 seconds. Local commit work is below one millisecond; the Gateway's lookup
and ACK handlers take only a few milliseconds.

The old Relay drains and reconnects, introducing a one-second receive-loop wait
on each side. Response authorization blocks the receive callback: 94 recorded
fresh checks run without overlap. Read/lookup polling adds another wait. The UI
notification follows delivery ACK; in the actual baseline its three-second
fallback refresh displays results before ACK, with a 76–2,898 ms save-to-display
gap. This gap must not be described as the full ACK duration.

The event path also has a correctness defect: all 28 event-pull responses during
the measured submissions return 409. The typed `cursor_gap` body is obscured by
the generic `revision_conflict` envelope, preventing snapshot recovery. No event
ACK occurs in that baseline, so serial event ACK cannot be assigned a measured
share of its delay.

The final correlated traces separate model time from the removed waits:

| Stage | Before mean | After mean | Samples |
| --- | ---: | ---: | --- |
| Submit to admission | 4.294 s | 0.312 s | 5 greetings per phase |
| Gateway execution | 6.919 s | 6.913 s | 5 greetings per phase |
| Local commit to delivery ACK | 4.694 s | 0.291 s | 5 greetings per phase |
| Execution lookup RPC | 5.477 s | 0.408 s | 12 before / 19 after RPCs |

The extra elapsed time around Gateway execution falls from a mean 11.088 to
0.902 seconds for greetings. This nested-duration difference includes polling,
authorization and scheduling; it is not a pure network latency measurement.
All five greeting output digests match across phases. The new event path takes
one initial 409, successfully recovers its snapshot, then records 27 successful
pulls and 28 successful ACKs. No trace drop/limit markers or parse errors occur
in the captured evidence; best-effort tracing still is not a global audit log.

## Changes, in order

1. Add opt-in timing for admission, RPC authorization/send/receive, Relay cycles,
   handler execution, lookup, durable commit, notification, progress and ACK.
   `SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR` must name an existing private absolute
   directory. Records contain fixed timing/identity metadata, never bodies,
   credentials, URLs or error text. Go tracing has a bounded queue and 64 MiB
   file limit; drop/limit markers invalidate complete accounting. Desktop
   tracing stops at 100,000 records. Diagnostics never control delivery.
2. Notify the original UI immediately after verified durable commit. ACK keeps
   its existing persistent retry, digest and identity checks. After persisting
   events, reconcile and ACK run concurrently; the next pull waits for both.
3. Keep an authenticated Relay receive socket open for later messages, with
   immediate enqueue wakeup and bounded liveness/replacement handling. Frames,
   cryptography and upstream ISCP contracts remain unchanged. PostgreSQL claims,
   writes and marks one message in a transaction; failed writes/cancellation
   roll back. See [Relay deployment details](public-iscp-node-deployment.md#10-bounded-continuous-receive).
4. Move matching-response fresh authorization off the serial receive callback.
   No completed authorization result is cached. Original pending-call, deadline,
   session, Grant and revocation fences are rechecked before delivery.
5. Negotiate optional `execution.progress` using the existing v2 operation
   registry and authorization. Only final-answer text deltas reach an 8 KiB
   cumulative preview per execution, bounded to 8 MiB globally. Epoch/revision,
   input digest and identity fences reject stale or mismatched snapshots.
   Capacity pressure suppresses previews only. Original message components
   render temporary text; complete lookup, files, digest verification, local
   transaction and durable ACK remain authoritative. Old nodes safely negotiate
   their original operation set without replaying business requests.
6. Preserve typed cursor-gap errors through the encrypted envelope and desktop
   adapter. Old-node compatibility accepts only the exact `events.pull` 409
   cursor-gap shape; other revision conflicts retain their existing behavior.

Intermediate greeting completion medians are 18.584 seconds after decoupling
UI/ACK (3 samples), 9.792 seconds after continuous Relay receive (3), and 9.547
seconds with asynchronous response authorization (3). These cumulative phases
are not additive isolated savings. A separate three-wave, eight-read burst did
**not** show additional speed improvement from asynchronous authorization;
its benefit here is removing a proven serialization point while preserving
fresh checks, not a separately demonstrated production speedup.

## Preservation and validation

Desktop 372 tests, WebChat 310 tests, strict production packaging, affected Go
race tests and isolated real PostgreSQL 18 Relay tests pass. Linux/arm64 passes
all 71 tested packages, `go vet ./...` and `go build ./...` with Node 26.2.0,
Go 1.25.5 and document dependencies installed. The Mac full Go run retains
reproduced baseline `/tmp` symlink, `/dev/shm` and resident-projection failures;
it is not reported as a green full Mac Go suite. Linux/x64 full suites are
outside this acceptance scope.

Native checks cover all five existing profiles: original identities and history,
nine settings entries, device controls and schedule-create/cancel. Installed
61 desktop source files, five UI assets and the helper match the build. No CSS,
layout, menu or permanent technical status panel is introduced.

A separate real 1,800-character request is interrupted after a 50-character
preview while both final messages and delivery receipts are absent. Cold restart
uses the same request ID/input digest and receives one 1,979-character final
message and one delivery receipt. Preview state is never treated as a completed
result or ACK. Gateway traces confirm one execution, with no second submission
after restart. All six final benchmark requests likewise have one execution,
one final local commit and one successful delivery ACK.

After a cold evidence backup, the original guarded ClientStore API removes only
the 23 delivered test conversations created by this task. All original messages,
tasks, files and conversation fields in the five profiles survive. Event cursors
advance as recovery resumes. Four cold profiles apply the previously shipped
schema 9→10 title migration, adding `title_pending=0` without changing their
original columns. The default launch selection remains byte-identical.

The work2 upgrade preserves original records in 41 business tables, ports,
mounts, model configuration and authority. The only responder qualification
addition is `execution.progress`; the only diagnostic environment change is
the private trace directory. TEST1 retains identity/authority records across
27 tables and its prior rate limits. App, profile, database and authority
checkpoints are paired; restoring old state requires reviewing later effects
and revocations, not simply overwriting a live ledger.

## Evidence and reproduction

Run `scripts/iscp-latency-benchmark.mjs` with Node 26, an explicit installed
executable, real profile, new private output directory, `--samples 5` and
`--long-reply`. It creates real conversations and waits for every delivery.
Keep paired cold backups before removing only its own completed conversations
through the original ClientStore API.

The operator-private evidence archive is `sparkclaw-latency-opt-20261014`.
It contains before/after JSON, timing traces, screenshots, intermediate phases,
comparison data, restart checks, test logs and upgrade receipts. Credentials
and database dumps are excluded from Git. work2 checkpoint:
`/var/backups/sparkclaw-latency-opt-20261014`; TEST1 checkpoint:
`/var/backups/sparkclaw-latency-relay-20261014-r2`.

InfiniCenter is absent at the prescribed ancestor locations. No central
decision/contract update or cross-project consensus is claimed. All changes
remain within SparkClaw's implementation and optional application operation;
upstream ISCP wire frames are unchanged.

## Subsequent work1 deployment

On 2026-10-14 main was published through `e8d6dbaa`, then work1 was upgraded
from `9fc956e2` to that source. Its rebuilt Linux/amd64 Gateway SHA-256 is
`492f256ba341963e17c00297c6155fc733ee1a0f8327dadce68735a638c5c22c`, exactly
matching the already qualified work2 binary; the intervening source changes
are documentation only. Existing functional validation therefore applies.

work1 retains its environment, complete Docker HostConfig, command, network
addresses, model configuration and original records in 41 business tables.
Responder qualification adds only `execution.progress`. Gateway and issuer are
healthy, the latest image tag is updated, and the stopped old container plus
paired database/config/authority checkpoint remain available at
`/var/backups/sparkclaw-work1-latency-20261014`.

The existing VM103 client identity, original interface and two delivered smoke
requests passed. The temporary conversations were backed up and removed through
the original guarded API; its original two conversations, four messages and
files remain. These requests are deployment smoke evidence, not another
before/after performance comparison. Private evidence archive:
`sparkclaw-work1-latency-20261014`. Further rollouts of this identical binary
reuse the existing acceptance results and need only deployment health,
state/config preservation and one connectivity check unless a failure or
environment difference calls for more.
