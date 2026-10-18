# ISCP application stream implementation

Language: English | [简体中文](../zh-cn/docs/iscp-application-stream-implementation.md)

Date: 2026-10-18. This is a local implementation of the [internal v3 design](iscp-application-stream-design.md), based on `cd7d5ccc`. It is not an installed release or ISCP upstream acceptance. Development and all disposable profiles belong to the dedicated `codex/iscp-application-stream-v3` worktree. InfiniCenter decision 0036 retains the implementation authorization separately from proposed upstream standardization. Existing JingSi, IMMS and App-CLI contracts remain unchanged.

## Implementation

The final-answer source publishes only explicitly selected public plain-text/Markdown deltas. Structured JSON is validated before becoming visible. A process-local projection retains up to the existing 8 MiB result limit, with 30-second replay/recovery bounds. First and last batches flush immediately; intermediate batches flush at 50 ms or 4 KiB. Model reasoning, tool arguments and intermediate workflow text are excluded.

The optional generic envelope, control and completion schemas live in [application-stream.v1.schema.json](../packages/protocol/application-stream.v1.schema.json); SparkClaw's opaque body binding lives in [execution-stream.v1.schema.json](../packages/protocol/execution-stream.v1.schema.json). The local `internal/appstream` codec is a candidate extension, not an ISCP Core modification. The pinned SDK remains `v0.2.0-rc.1`. Both layers reject missing/unknown fields, invalid Unicode, duplicate keys and invalid integers; stateful validation additionally checks bindings, UTF-8 byte offsets and full-text SHA-256. The first terminal watermark is immutable, and a terminal arriving early cannot swallow missing text.

Endpoint negotiates `stream_schema_version=1` and the new `execution.progress.stream` operation. A stream uses a fresh subscription ID while retaining the original execution request ID. The new operation requires matched Gateway/helper/Electron capabilities; it never silently falls back to progress polling. Cancellation removes only the observer. The opening send and first-event wait share one ten-second deadline; ordered tail recovery allows two seconds. Stream failures cannot reset unrelated ordinary RPCs.

Authorization snapshots last at most five minutes from query initiation, refresh in the background, and are checked locally on both ends for every publication. Session, identity, revision, scope, Grant expiry and capability expiry fence stale callbacks. A replacement session hides the preview until its new authority is verified; verified text and cursor can remain private for recovery. Ordinary submit, approvals, lookup and ACK retain their original fresh-authority paths. Private IPC includes remaining validity and a deadline relative to helper call receipt. Electron anchors that deadline before sending the call, so time spent queuing in stdout cannot renew authority. Expired text is discarded immediately.

The self-managed Relay keeps receiving through its existing HTTP/WebSocket transport, including empty queues and token rotation. Artificial rate, concurrency and cumulative request gates are removed from that path and its issuer forwarder; identity, authorization, ciphertext integrity, message sizes and ordinary connection/queue errors remain effective. No resource scheduler was introduced.

ClientStore schema 11 maps each newly delivered assistant message to its original request in the same delivery transaction. Existing message IDs and history are retained. Preview and committed answer render through the existing `MessageBubble`, using one `execution:<request_id>` React key and scroll anchor. The shared component accepts optional `scrollAnchorId`; `data-message-id` retains the real message ID. No CSS, menu, layout or scrolling policy was changed. Notification reads coalesce while a read is in flight; publication versions prevent older responses from replacing newer content without starving updates. The desktop-experience branch owns scrolling/focus verification and integration.

## Verification

The affected Go packages pass race tests, build and vet. An isolated Linux container passes all 73 Gateway test packages; 11 command/support packages have no tests. On macOS, four existing top-level Gateway tests fail because `/dev/shm` is unavailable or an old assertion mistakes `/private/tmp` for leaked private content. Each failure reproduces in a clean archive of the common baseline. Desktop tests pass 424/424 with no skips; WebChat tests pass 326/326 and the production bundle builds.

The generic fixture set has four positive and seventeen negative cases; execution fixtures have six positive and nine negative cases. An independent Draft 2020-12 JSON Schema validator checks the structural cases; production stateful tests separately reject the intentionally wrong UTF-8 byte-offset fixture. Adversarial tests cover authentication before replay-sequence advancement, tampered envelopes, duplicates, out-of-order tails, conflicting terminal responses, cancellation before opening and after the opening horizon, expired/revoked snapshots, stale-session query responses and signed authorization revision changes.

Native qualification uses a fresh PostgreSQL 18 container, the production continuous Relay and encrypted Endpoint, the actual helper private pipe, production Electron entrypoint/ClientStore, and the original renderer. It blocks desktop/Gateway business HTTP. Evidence contains correlation IDs, lengths, digests, timings and build fingerprints, not answer text or credentials. Animation-frame observation is a compositor-paint proxy. Cross-process clock uncertainty is measured; end-to-end percentile claims use its conservative upper bound.

The [sanitized evidence receipt](evidence/iscp-stream-2026-10-18.json) records source revisions, executable/module hashes, original receipt hashes, test-log hashes and all 23 durable deliveries. The production implementation ends at `a43100bd`, the additional blocked-pipe regression at `c3a524d2`, and the opt-in fixtures at `6962371b`.

| Native case | Observed result | Scope |
| --- | --- | --- |
| 20 healthy answers, 600 characters each, 2,000 source batches | First batch at most 59.26 ms; P95 113.08 ms; P99 119.72 ms; maximum growth gap 166.40 ms; final commit-to-handoff at most 49.38 ms | One active answer, local encrypted chain, 100 ms controlled producer; at least 98% visible before completion |
| 6,000-character / 18,000-byte answer | Full source, committed and renderer digests agree; 99% visible before completion | No previous preview truncation; 85 explicit test-harness scrolls keep the tail visible |
| Helper killed after 240 visible characters | Original request recovered in 1,328.20 ms; one submit, two subscriptions, one final ACK | One required hide during session replacement; no remount, duplicate or missing row |
| Authorized work2 model configuration, production Agent | 730 visible characters, 63 live text observations, first text before workflow completion; one submit/subscription/final ACK | Real model, `mock=false`; submit-to-first-visible 6.98 seconds includes model/workflow time |

All healthy cases had zero blank, missing, duplicate or remounted answer rows; all recorded nonempty observations had their answer tail in the original viewport. The 20-sample and real-model cases needed no harness scrolling. The long-text case does not qualify production automatic following. Timing is a viewport-plus-next-animation-frame proxy, without an independent per-layer latency breakdown. The real Agent ran in an isolated Linux fixture to preserve the existing `/dev/shm` requirement, using work2's configured model service and the actual macOS helper/Electron UI. Before/after checks found the work2 Gateway container, start time, configuration, mounts and model configuration files unchanged.

The 20-sample receipt anchors the clean tracked production tree at `a43100bd`; subsequent real-model/recovery/long-text receipts anchor `c3a524d2` and additionally hash the progress, projection, store, capability and auth modules. Test fixtures were untracked during measurement; their driver and compiled-binary hashes identify the executed versions. The final runner adds explicit Linux-image selection and execution-start counting. Earlier same-conversation DOM-only timing is not used for viewport claims. A failed ninth development sample, the initial recovery fixture's incorrect zero-hide expectation, and the macOS real-workflow `/dev/shm` failure remain recorded. Healthy thresholds were not relaxed.

The full Linux package run preceded the final helper deadline sampling-order adjustment; focused helper race/vet and blocked-writer checks cover that adjustment. The native fixture is opt-in and therefore is not implied by an ordinary `go test` pass.

## Reproduction and integration

Use Node 26, npm 11, Go 1.25.5 or newer compatible Go 1.25, and run `npm run setup:document-tools` before Gateway validation. The opt-in native runner requires macOS, the local Electron dependency and Docker PostgreSQL 18:

```sh
node apps/desktop/test/run-stream-native-qualification.mjs 20
node apps/desktop/test/run-stream-native-qualification.mjs 1 --long-text
node apps/desktop/test/run-stream-native-qualification.mjs 1 --recovery
SPARKCLAW_STREAM_FIXTURE_MODEL_CONFIG=/private/tmp/model-test.json \
  SPARKCLAW_STREAM_FIXTURE_LINUX_IMAGE=sparkclaw-linux-test-runtime:local \
  node apps/desktop/test/run-stream-native-qualification.mjs 1 --real-model
```

The real-model mode copies only model settings into the disposable configuration. It must use an explicitly authorized model environment; it does not deploy or change that environment. Supply a prepared Linux test image containing the repository's Node/document-runtime dependencies, and keep the private model configuration and referenced catalog in the same private directory. The example image tag is a prerequisite, not an image downloaded by the runner. Generated test identities, private profiles and raw evidence remain outside Git. The runner removes only its labeled temporary Gateway/database containers and its own child processes.

Before integration, retain both `data-scroll-anchor` and `data-message-id` when reconciling the desktop branch's shared components. Run that branch's scrolling/selection/focus matrix against the merged source. A matched release must include this Gateway, Relay/forwarder, helper and Electron source; schema 11 is a real storage migration and an old binary must not be assumed to support it. No merge to main, package, installation, push or live-service deployment is part of this work.
