# Resident mailbox observation

The Controller now has a registered `observe` operation for `qq_mail`, `gmail` and consumer `outlook`. It creates one owned background mailbox task per provider and returns after account validation. The CLI daemon stays attached; a native page binding pushes events to an owner-only Unix socket. Idle observation does not retain the Controller's provider reservation or the Reader lease. Reader discovery and original acquisition continue through their existing registered operations.

## Lifecycle and boundaries

Call the provider's `*.observe` script, revision 1, with exactly:

```json
{"schema_version":1,"action":"start","account_address":"signed-in@example.test","owner_scope":"<64 lowercase hex characters>"}
```

`action` can be `start`, `status` or `stop`. The binding includes provider, account, owner scope, browser credential generation and credential identity. A different binding is rejected. Repeated healthy starts reuse the task; a degraded task can be explicitly rebuilt. Account mismatch requires login handling, not automatic login. Opening the provider login flow stops its watcher.

A 15-second **local** heartbeat checks the document; it is not a provider HTTP query or repeated CLI evaluation. After 45 seconds without it, the supervisor marks the watcher degraded, waits with bounded backoff, retires its owned runtime and reinstalls the page hook before navigation. At most three successful reconstructions occur per lease; a failed reconstruction requires an explicit retry. Process ownership checks must succeed before removing runtime files. Shutdown attempts cleanup for every provider even if one fails.

Page/document identity and increasing sequence numbers fence stale or duplicate events. Replaced documents, gaps, lost bindings and unknown notification envelopes require reconciliation. The event ring is limited to 256 entries and 32 KiB; individual records and socket buffers are bounded. Native calls, messages and return values pass through unchanged. Outlook fetch bodies are neither cloned nor consumed. Qualification-only structural evidence is off by default (`SPARKCLAW_MAIL_OBSERVER_EVIDENCE=1` enables it on an isolated Controller). Normal status never carries mail bodies, addresses, provider message IDs or credentials.

The Browser Bridge production path now includes Gateway intent reconciliation and durable notification admission. The overall `qualified:false` flag continues to keep the five-minute quiet tier disabled; the normal 60-second verification remains active. Electron still requires separate qualification.

## Gateway delivery

Three fixed authenticated POST routes on the owner-only Controller socket implement the internal transport: `/v1/mail-observers/reconcile`, `/v1/mail-observers/events`, and `/v1/mail-observers/ack`. Credentials appear only in JSON request bodies. A new credential is proved through the native bridge; routine renewals and event waits never reserve Reader's browser lane. A 25-second local long poll returns immediately when metadata arrives. Each reconciliation renews a 90-second intent lease; losing Gateway eventually retires its owned observer tasks.

Gateway derives intent from enabled, ready provider settings and active intake bindings. It rechecks these after waiting, and Store fences admission against owner, mailbox binding generation, registered Controller epoch and credential generation. Per-mailbox sequence cursors and the discover job update commit in one owner transaction before acknowledgement. Retrying a delivery cannot advance the idle deadline twice; hints during a Reader query retain a newer signal revision, and retry backoff is preserved.

The delivery ring holds at most 128 metadata records, separate from the local diagnostic ring. Overflow replaces missing history with a catch-up hint for each current binding. New registrations and replaced documents also request reconciliation. Acknowledged events can be retired; unacknowledged events replay after reconnect. Controller restart creates a fresh delivery epoch, so delayed old-controller events cannot reactivate a mailbox. Transport failure degrades watch health while the existing periodic Reader continues. State snapshots restore health after reconnect without advancing the event cursor.

Normal receiving switches are authoritative. A disabled mailbox is not subscribed, and events from a retired binding are acknowledged without admitting new work. Multiple owners sharing a provider in one dedicated browser retain the existing one-account limitation: only one eligible binding is observed; other intake bindings retain periodic verification.

## Notification rules, version 1

| Provider | Accepted change hint | Excluded controls |
| --- | --- | --- |
| QQ Mail | Native `wx.mail.qq.com/socket` WebSocket `cmd=1`, `scene=notify`, base64 JSON with `type="0"` and a valid `mid`; deduplicate the ID inside the page | `cmd=0` connection initialization; malformed or unfamiliar change shapes are unqualified |
| Gmail | Length-prefixed JSON on the native `signaler-pa.clients6.google.com/punctual/multi-watch/channel`; a topic has a nonempty invalidation body at payload slot 1 | Connection records, scalar ACKs, subscription ACKs and the roughly 20-second keepalive in payload slot 2 |
| Consumer Outlook | A Worker/MessageChannel callback belonging to `subscribeToRowNotifications`; `RowAdded`, or a known row whose delivery signature changes | `Reload`, hierarchy/calendar results, ordinary query results, duplicates and read-flag-only changes |

Outlook handles both `Conversation` rows (conversation ID, delivery time, message count and item IDs) and received `Item` rows (item ID and receipt time; explicit To/Cc-me evidence, excluding drafts). Signatures remain inside the page. Unknown modified rows require catch-up rather than being asserted to be arrivals. Row movement can also invalidate a view. A hint is a request for Reader reconciliation, **never** proof of one new mail or a coverage watermark. Gmail's signal is a mailbox invalidation; its name does not promise an exclusively new-mail event.

## Live qualification, 2026-09-24

The dedicated Browser Bridge profile supplied three freshly authenticated identities. Outlook's delivery address came from a verified outgoing original; its login alias was not used as a delivery address. Each send used a new `SCW-mail-*` marker, and uncertain outcomes were checked without resending.

Two complete three-provider idle/Reader control windows produced zero hints for every provider. In the second window, the same Controller completed QQ, Gmail and Outlook range queries in 4.36 s, 9.24 s and 17.22 s while all watchers remained resident. The earlier parked run lasted 1,049 seconds and cleaned up its owned tasks. QQ and Gmail positive controls each produced one hint and a verified Reader original:

- Gmail → QQ: `SCW-mail-935680acf76487f1`, `qq_inbound_envelope`.
- QQ → Gmail: `SCW-mail-025becc34080122c`, `gmail_topic_invalidation`.
- QQ → Outlook after automatic reconstruction: `SCW-mail-fed11e7136b0d61d`, one `outlook_delivery_change`, original verified. Explicitly changing this test mail from unread to read produced `RowModified` with UnreadCount 1 → 0 and no extra hint.
- Outlook → QQ: `SCW-mail-51bac6b718e87c76`, delivered original verified; used to prove the actual Outlook SMTP sender.

Gmail outgoing sends and opening only the new Gmail test message did not increase its hint count. QQ → Outlook `SCW-mail-caf877ff1057662d` supplied the original verified `RowAdded`/`RowModified` conversation evidence. Gmail → Outlook `SCW-mail-82a4fa1a8b1dff80` instead landed in Junk and exposed the received `Item` variant. That message was visible by its unique marker, but its Reader inbox original was not verified in the allotted query window; it is not counted as a successful Reader receipt. This is an explicit folder-coverage boundary, not an inferred delivery failure.

A forced owned-page close exposed a recovery bug: a stale-page cleanup error could stop reconstruction even after the daemon had been safely reaped. Recovery now distinguishes a retired page from a failed process-ownership fence. The final qualification result and compact measurements are recorded in [the evidence file](email-resident-observer-validation.json).

## Reproduce

Use `scripts/qualify-playwright-email.sh --help` for private profile and isolated Controller setup. `SPARKCLAW_TEST_RESIDENT_NOTIFICATION=1` starts watchers, observes 30 idle seconds, runs the same-Controller Reader controls, asserts healthy zero-hint controls, and stops every owned watcher. `SPARKCLAW_TEST_RESIDENT_HOLD_SECONDS=1..900` permits authorized mutual-send controls during a bounded hold. No mail is sent by this resident test unless a separate send flag is supplied.

While a hold is active, `SPARKCLAW_TEST_NOTIFICATION_RESIDENT_SEND=1` with the notification test's provider route sends once and verifies its unique original without starting the old blocking diagnostic watch. Outlook recipients require the private JSON proof exported from a verified outgoing original. The ordinary Outlook compose helper failed draft checks in this browser; the successful outgoing proof used the native compose UI, so those failed attempts are not reported as successful sends.

Local regression tests cover native message forwarding, fragmented Gmail frames, malformed streams, classification, read-state exclusion, bounded buffering, account/owner fencing, old-document rejection, provider-reservation release and recovery ownership checks.

The second long hold exceeded Go test’s default ten-minute timeout after its controls; the shared qualification entry point now sets `-timeout=30m`. This harness failure is retained in the evidence and is not counted as a successful long run.

## Production verification, 2026-09-24

The Gateway image and host Controller were deployed, with the final service start at 10:51:14 UTC. Existing production deployment identity and client settings were preserved. All three receiving switches are now enabled and their production observers are watching. Gmail and Outlook were enabled through the authenticated Gateway settings API at the owner’s explicit request and were then tested end to end below.

Two real Gmail→QQ messages traversed the production Gateway, without the smoke invoking Reader discovery/capture after sending. `SCW-mail-349cb322e46cbbc1` was admitted at 10:47:23.457499 UTC, Reader began at 10:47:23.597253, and the original committed at 10:47:24.239137 (about 140 ms and 782 ms after admission). The original SHA-256 was independently checked. After Gateway/Controller restart, `SCW-mail-23343fa87c5b040a` also produced a durable hint and a verified production original. Its hint retained an existing retry deadline: a scheduled Reader attempt had conflicted with the exclusive test-send operation. Final signal/reconciled revisions were both 11, with watch state `watching`.

Restart testing found a cleanup-error path that left the HTTP listener alive until systemd's 60-second stop timeout. Shutdown now closes the listener/socket even after cleanup failure and attempts both observer and Reader cleanup. The subsequent service restart logged normal Controller shutdown and completed in 0.22 seconds.

The full deployment wrapper's read-only preflight rejected this machine's legacy all-in-one private environment file. Deployment therefore used the existing Compose configuration to replace Gateway and restart the host Controller, preserving deployment identity, desktop capability path, search setting and model services. The previous Gateway image remains tagged `sparkclaw-gateway:before-mail-observer-20260924` for rollback.

For a production smoke, add `SPARKCLAW_TEST_NOTIFICATION_GATEWAY_RECEIPT=1` to the authorized resident-send test. After its single send, it reads only Store and local original bytes and checks the classified hint and SHA-256. It does not create intake subscriptions or enable receiving switches.

### Gmail and Outlook production activation

Earlier positive controls qualified the rules and Reader behavior. The following additional tests enabled the two receiving switches and verified the deployed Gateway path, without invoking Reader discovery/capture from the smoke after sending:

| Route / unique marker | Notification persisted (UTC) | Original committed (UTC) | Hint to original | Result |
| --- | --- | --- | --- | --- |
| QQ → Gmail / `SCW-mail-744e2d9d42900d56` | 11:04:41.055607 | 11:05:41.143568 | 60.088 s | Original SHA-256 verified; signal/reconciled 3/3 |
| QQ → Outlook / `SCW-mail-749af8363e730259` | 11:11:29.875987 | 11:12:04.987498 | 35.112 s | Original SHA-256 verified; signal/reconciled 3/3 |

Both classified hints were admitted while an existing Reader retry deadline remained in effect. Test sending requires an exclusive browser operation and can conflict with scheduled Reader attempts; the scheduler retained its existing retry backoff. These times are observed samples, not a promised delivery bound. The observers themselves remained `watching` and do not hold Reader reservations while waiting.

Enabling Gmail exposed more than 50 messages in the initial historical interval. The existing overflow policy recorded one durable `coverage_gap` and one unacknowledged warning, then resumed current intervals. The genuine new Gmail test message was captured successfully; historical completeness is not claimed and the warning was neither cleared nor acknowledged by this test.

The first 45-second production no-send control (11:02:33–11:03:18 UTC) produced zero classified hints for each provider with all three watchers healthy. Receiving remains enabled for all three accounts, with the existing 60-second fallback.

A second 90-second no-send production control (11:12:16–11:13:47 UTC) also passed: zero classified hints for each provider, all three observers `watching` at the start, midpoint and end, and each production Reader advanced its polling watermark. All three ended without a current sync error; Gmail retained only the historical coverage warning described above.

## Browser contention, 2026-09-28

Reader requests now wait up to two seconds for the Controller reservation. An early Gateway `browser_busy` is retried within the same bounded wait; only explicit contention is eligible. If the browser stays occupied, `email_browser_busy` releases the job lease and persists a two-second retry. It does not create a provider/list failure, alter login health, spend a failed attempt, settle a notification revision, or finish the round. The frozen interval and final-check intent survive the yield and Store restart. Begin-command identity includes the worker lease, so repeated claims of a frozen checkpoint cannot conflict with an earlier upper-bound proposal. Notifications received during a yield remain pending and the resumed round includes its final check.

This replaces the earlier accidental classification of `browser_busy` as `email_provider_unavailable`, which imposed a one-minute provider-error delay. Genuine script timeouts and provider failures keep their existing error handling. An uncertain send/transport result is never automatically replayed. Regression coverage includes cancellation, bounded contention, notification arrival while yielding, final-check contention, and repeated yields beyond the normal attempt limit in memory, file and isolated PostgreSQL stores.
