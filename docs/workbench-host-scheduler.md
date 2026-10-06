# Host workbench scheduling

> Language: English | [简体中文](../zh-cn/docs/workbench-host-scheduler.md)

WebChat owns its schedule definitions and occurrence ledger in the deployment
host's Store. The backend scheduler runs independently of browser tabs: closing
WebChat does not stop it. File and PostgreSQL retain the records across ordinary
service restart; Memory is transient. Definitions and history do not synchronize
to desktop installations. See [release cutover](workbench-release.md) for the fresh
storage requirement and [desktop scheduling](desktop-scheduling.md) for the
separate main-process/authenticated-connection availability rule.

## Ownership and durable claims

The Schedule Registry derives ownership from the persisted, authorized originating
session (`webchat` or the hidden `schedule` session), independently of the return
destination. A request cannot assert this authority. Connector-owned schedules
remain service responsibilities with their existing retry/reclaim rules.

Before publishing an online workbench occurrence, the repository atomically records
its stable request ID and advances a recurring definition to its next due time.
Memory, File and PostgreSQL implement the same compare-and-swap operation; the
File snapshot persists the definition and occurrence ledger together. Competing
workers cannot claim the same definition revision twice. Execution uses the
ordinary Message Runtime rather than a backend future-execution lease.

The occurrence ledger ID is also the Message Runtime envelope ID; its run ID is
`run_` followed by that ID. If publication fails ambiguously, the occurrence becomes
`unknown` with reconciliation required. A crash after the durable claim, including
before publication, never authorizes replay. Restart does not republish submitted
or missed occurrences. Reconciliation uses the original identity only; missing
results or a failed lookup do not authorize a replacement execution. Because the
next due time was committed with the claim, future recurring work survives that
uncertainty and receives its own occurrence identity.

## Availability and missed occurrences

The first scan after startup, a failed repository access, unavailable publisher,
backward clock movement, a poll gap exceeding two normal intervals, or a detected
wall/monotonic clock suspension gap resets the availability boundary. Due
workbench occurrences at or before that boundary become permanently `missed`.
This is a service availability rule; a browser's connection or visibility does
not define host scheduler availability.

Healthy continuous polls retain one stable availability boundary. Occurrences due
after it remain eligible despite normal timer jitter or a bounded scan/worker
queue delay. Each scan bounds repository work. After a long outage, old due times
remain unchanged until later scans record them as missed; they never become a
runnable catch-up backlog. Recurring definitions advance through missed occurrences
and retain future due times. A wake or service restart cannot execute work that
was due while unavailable.

The WebChat task list shows one-time missed occurrences as “Skipped while offline”
and unfinished submitted occurrences as “Submitted”. It does not offer to edit,
rearm or automatically resubmit those occurrences. Historical records stay in the
host repository. An explicit new user request has a new identity and does not
revive a missed or uncertain occurrence.

## Verification boundary

Deterministic scheduler and Memory/File contract tests cover startup, availability
gaps, continuous jitter, bounded batches, concurrent claims, recurring future
identity, restart, rollback and unknown commit outcomes. The same Store contract
is exercised against fresh isolated PostgreSQL storage. WebChat tests cover missed
and submitted presentation. These checks change neither a running service nor real
data. Native host suspend/resume qualification is separate hardware evidence for
the exact matched release; clock-injection tests are not a claim of physical sleep
acceptance.
