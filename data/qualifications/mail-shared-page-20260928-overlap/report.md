# Shared-page phase overlap and QQ rapid-read follow-up

Date: 2026-09-28 UTC. See the [redacted manifest](manifest.json) for exact
timestamps and outcomes. Testing used the existing dedicated headed browser;
the separate-page production Controller was restored after each isolated run.

QQ → Outlook produced a confirmed send while the candidate Outlook page
repeatedly acquired a frozen historical original. The native
`outlook_delivery_change` event at 08:55:44.970Z fell inside the candidate's
`prepare_original` interval of 08:55:38.971–08:55:46.006Z. Six `collect_page`
rounds completed with verified originals; the test message's unique original
was independently verified and the watch remained `watching`. This is one
natural original-phase overlap, short of B02's three arrivals per phase and
provider.

In a subsequent Gmail → QQ run, the candidate completed 80 verified rapid
one-original rounds. Round 81 returned `email_network_original_unqualified`,
and the shared page/watch were retired under the failure policy. The send was
confirmed and its unique QQ original was verified later through the restored
production Controller. The isolated test ended before it captured a native
QQ hint, so its phase-overlap result is unknown. The message was not resent.

A read-only follow-up using the same frozen QQ target completed 100 rapid
rounds on the separate-page baseline. The candidate failed at round 78 on
one no-send run with the same original error, then completed 100 rounds on a
second no-send run. The failure is intermittent and not yet attributed to a
specific provider response or shared-page mechanism. It remains a release
blocker for the proposed stability gate; one successful rerun does not erase
the two failures.

The candidate remains opt-in. B02's full phase/provider arrival matrix, B04's
idle tests, B05's two-hour run, applicable C faults and the 30-pair p95
comparison have not passed. No receiving setting or public contract changed.
