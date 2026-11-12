# ISCP reverse RPC diagnostics

[中文](../zh-cn/docs/iscp-rpc-diagnostics.md)

For a failed SparkX browser health check, start with the task's request ID or
the `browser.controller.status` RPC ID. The RPC ID is inside the authenticated
encrypted request. Relay cannot see it. The sending endpoint records the
mapping from RPC ID to envelope ID for the request **and** the response. The
receiving endpoint and Relay journal record the exact same outer envelope IDs.
Do not join by timestamp alone.

| Boundary | Evidence |
| --- | --- |
| Gateway | `browser.health_result` and `browser.status_check` distinguish no peer, reverse RPC failure, invalid status payload and runtime unavailable. `rpc.call` gives the transport phase and deadline/cancellation status; a successful transport can still return non-200. |
| Sender | `rpc.send_authorization`, `rpc.envelope_created`, `rpc.relay_enqueue`, `rpc.relay_write`, `rpc.relay_receipt` distinguish local enqueue, socket write, and signed Relay acceptance. A local socket write alone does not prove acceptance. |
| Relay | `relay.submit_received`, `relay.submit_admission`, `relay.receipt_write`, `relay.delivery_claimed`, `relay.delivery_write`, `relay.delivery_commit` distinguish the database commit and the downstream socket write. Relay delivery does not prove the peer decrypted or handled the RPC. |
| Receiver and SparkX | `rpc.relay_receive`, `rpc.envelope_received` / `rpc.envelope_rejected`, request authorization, and the `ipc.reverse_*` stages show receipt at the helper, handoff, and the returned HTTP status. `browser.controller_status` explains the local runtime or authority decision without copying the status body. |
| Return | Follow the response envelope through the same Relay stages. `rpc.response_received`, `rpc.response_authorization`, `rpc.delivered` and `rpc.response_dropped` distinguish verified completion from a late or invalid response. Late responses are logged and still cannot complete an ended call. |

Use a directory owned by the Gateway service with permissions `0700` and set
`SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR` to its absolute path before starting the
Gateway. Use the existing private trace mount on production. SparkX normally
provisions `iscp-diagnostics` inside its own profile and passes that path to its
helper. An explicit environment setting overrides the default; an empty value
disables it. The default main-process trace records reverse IPC and browser
status, while the helper records transport stages. At startup SparkX prunes
its old default traces (7 days / 128 MiB
of previous process files).

The separately built ISCP Relay requires `ISCP_WORKBENCH_TRACE=1` on
`iscp-relay.service` and a retained system journal. Export it for the incident
window with `journalctl -u iscp-relay.service --since ... --until ... -o cat`.
No protocol, identity, or database schema changes are required. Keep the
Gateway's private directory subject to the site's retention policy across
restarts. Each Go process retains three files of at most 64 MiB; SparkX's main
process retains three files of at most 100,000 records each.

```sh
node scripts/iscp-rpc-trace.mjs --request-id "$REQUEST_ID" \
  /private/gateway/go-*.ndjson /private/sparkx/go-*.ndjson \
  /private/sparkx/desktop-*.jsonl /private/relay-window.log
```

Use `--rpc-id` to inspect one probe. The command reads files locally and joins
IDs, then displays the available timestamps. Clocks can differ across hosts;
use stage ordering and process-local durations for causality. Check trace
health, coverage, retention and service restarts before treating a missing
record as evidence. Go and Relay enqueue logs without waiting on disk; the
desktop main process writes only short records on its default path. Pressure
or storage errors can leave gaps. The traces contain IDs, operation
names, stages, fixed reasons and statuses; they do not contain prompts, URLs,
credentials, raw errors, or encrypted payloads.
