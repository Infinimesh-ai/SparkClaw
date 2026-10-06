# Desktop workbench scheduling

> Language: English | [简体中文](../zh-cn/docs/desktop-scheduling.md)

The desktop main process owns schedule definitions and occurrence records in its
private workbench SQLite database. It submits due work through the ordinary
execution client. Saving a future schedule makes no backend request and creates
no execution-service lease or timer.

The first due time must be within the next 366 days. The UI supports one occurrence
or a fixed interval of 1 hour, 24 hours or 7 days; main accepts intervals from one
minute through 366 days. Intervals measure elapsed time, so “24 hours” is not a
calendar/timezone recurrence across a daylight-saving transition. Each repetition
retains the explicitly saved input/context, receives a new immutable request ID,
and remains in the originating conversation. Other workbenches do not receive
these definitions or histories automatically.

## Due-time behavior

A running main scheduler with an authenticated usable connection establishes an
online window. An occurrence due during that continuously available window is
claimed in an exclusive SQLite transaction. The claim records submission intent
and a private one-use dispatch capability before the ordinary execution POST.
Concurrent ticks or main-process recovery cannot claim that occurrence again.
Normal timer delay within that same window remains eligible for dispatch.

Process restart, authentication generation changes, connection loss and
`powerMonitor` suspend end the window. A new window excludes all already-due
unclaimed occurrences: they become permanently `missed`. The suspend handler also
prevents a late connected-status callback from starting the scheduler before
resume. Hiding the window does not stop the main scheduler.

For recurring definitions, offline missed due times are recorded as a bounded
range: `due_at`, `interval_ms`, `missed_count` and `missed_until` identify every
skipped occurrence without allocating an offline backlog. Only the next future
occurrence remains pending. Canceling a definition cancels its future occurrences;
an admitted occurrence uses ordinary execution cancellation. “Run now as a new
request” is an explicit new execution and cannot revive or reuse a missed ID.

A crash between the durable claim and POST is conservatively uncertain after
restart. It only reconciles the recorded request ID. A lost POST response followed
by 404 likewise never permits another POST, even from the explicit submit button.
An unconsumed live dispatch capability can be marked missed when authentication
changes before dispatch. Durable results, ACK retry, approvals and cancellation
retain the ordinary execution-client rules.

## Storage and verification

The development cutover initializes the selected schema directly. Unsupported
older directories fail explicitly; this code neither upgrades their schema nor
imports/deletes their data. Data created in the selected release survives normal
restarts, including definitions, claims, missed ranges and delivery receipts.

The affected baseline passed 29 desktop tests before changes. Implementation
checks passed all 116 desktop tests and the WebChat production build; regression
coverage includes two SQLite claimants, live timer jitter, restart/reconnect,
sleep/wake lifecycle, authentication changes, disk failure, recurring future
identity, crash-before-POST, lost-response/404 and cancellation of an unsent claim.
The renderer test exercises both one-time and recurring schedule submission.
These deterministic checks do not constitute native OS suspend/resume or packaged
release qualification. The matched execution-service API cutover and removal of
its retired future-lease handlers are validated with the overall convergence
release.
