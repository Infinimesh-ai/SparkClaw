# Shared-page candidate: controlled mail and paired original reads

Run date: 2026-09-28 UTC. Environment: the existing signed-in, dedicated headed
Chromium for QQ Mail, consumer Outlook and Gmail. Each layout ran on an isolated
Controller in sequence; the production Controller was restored after every
run. See [redacted manifest](manifest.json) for exact counts and timings.

## Results

| Check | Baseline | Candidate |
| --- | ---: | ---: |
| Retained mail task tabs and CLI sessions | 6 | 3 |
| Empty rounds per provider | 2 | 2 |
| One-original rounds per provider, including replay | 2 | 2 |
| Two-original rounds per provider, including replay | 2 | 2 |
| Originals verified and equal to baseline/replay | 6 per provider | 6 per provider |

All watches remained `watching` during the reads. The task window stayed
unfocused, with zero stale tabs; the pre-existing non-task tab stayed in its
own window. The nonempty runs used the same two frozen historical retry targets
per provider and verified each original's on-disk manifest and content hash.
They do not establish parity for a naturally nonempty time interval. Four
timings per nonempty workload are insufficient for the 30-pair p95 gate.

Three user-authorized sends were confirmed, one into each receiver:

| Sender → receiver | Native candidate-watch hint | Original |
| --- | --- | --- |
| Gmail → QQ Mail | `qq_inbound_envelope` | Unique original verified |
| QQ Mail → Gmail | `gmail_topic_invalidation` | Unique original verified |
| QQ Mail → Outlook | `outlook_delivery_change` | Unique original verified |

These arrived while the receiver watch was idle. A separate isolated sender
Controller used the same dedicated browser profile so its exclusive send did
not drain the receiver Controller. Receipt verification used independent
`Discover`/capture operations after the hint; it did not exercise the shared
`collect_page` borrower during delivery. One Outlook → QQ attempt returned
`email_page_contract_changed`; no matching recipient hint or original was found,
and the uncertain send was not repeated.

## Decision

The shared page is usable for the measured read and idle-watch cases, but the
release gate is **not met**. B02 requires three confirmed arrivals per provider
in each of the idle, list and original phases; only one idle arrival per
provider exists. B04's 45-minute pinned idle and unpinned 30-minute expiry,
B05's two-hour/100-round soak, C01–C05 fault and exclusive-path runs, and the
30-pair p95 comparison remain. The production default therefore remains the
existing separate observer and Reader pages.

No receiving setting was changed. Private target identities, account addresses,
message bodies, raw event payloads and original files remain outside this
report. An earlier read-only empty-interval run is recorded separately in
[the smoke manifest](../mail-shared-page-20260928-smoke/manifest.json).
Later phase and rapid-round findings are in the
[overlap follow-up](../mail-shared-page-20260928-overlap/report.md).
