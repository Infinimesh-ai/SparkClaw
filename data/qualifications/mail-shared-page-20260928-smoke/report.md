# Shared mailbox page: read-only three-provider smoke (2026-09-28)

Both sequential isolated Controller runs passed the same read-only harness on
the existing dedicated Chromium profile. The baseline retained six mail task
tabs and six CLI sessions; the opt-in shared-page candidate retained three of
each. QQ Mail, consumer Outlook and Gmail each completed two frozen empty
interval rounds after watch registration. Post-round watcher status remained
`watching` with no accepted query-induced hints. In both snapshots the task
window was unfocused, had zero stale/other tabs, and one pre-existing non-task
tab remained in a separate unfocused window.

The managed Reader artifacts were installed before the runs. The production
Controller was stopped while each isolated Controller held the same profile,
then restored with shared-page mode off. Browser and Controller services were
active afterward. No test mail was sent and receiving settings were unchanged.
No owned runtime session directories remained under the isolated runtimes after
their Controllers stopped. Exact source and installed receipt digests, page
counts, per-round durations and case statuses are in [manifest.json](manifest.json).

This passes the **smoke**, not B01–B05 or C01–C05 release gates. B01 still needs
nonempty original/replay comparison. B03 needs an independently verified
non-arrival control. Native arrivals during idle, list and original phases,
45-minute idle behavior, two-hour/100-round retention, faults and paired p95
remain untested. The candidate remains opt-in.
