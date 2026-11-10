# Browser Controller candidate acceptance and adoption handbook — 1f7f20ee

> Language: English | [简体中文](../zh-cn/docs/desktop-browser-controller-candidate-acceptance.md)
>
> Updated: 2026-11-10. Product source: `1f7f20ee27f45efe5dbf15e43c612a4d0cff88d6`.
> Status: the owner accepted the `1f7f20ee` candidate within §8's scope. D2 source `50300074`
> passed a paired Mac/Linux ARM64 upgrade and rollback rehearsal. See §9 for the artifacts.
> Production rollout has a separate authorization decision.
> Final native scope is Mac ARM64 and Linux ARM64. Intel Mac is excluded.

This handbook is for the owner's remaining work: read status and performance first, then follow
"Next tasks" in order. The candidate embeds Controller and Playwright MCP in SparkX and carries
generic browser commands over authenticated ISCP. Mail, backend acquisition and personal scripts
retain their current paths. The
[design](desktop-embedded-browser-controller-iscp-design.md) §10–§15 holds the rationale, D1–D3
and full evidence boundaries.

## 1. Current status

| Item | Status | Notes |
|---|---|---|
| Source wiring | Done | Gateway always enables `ClientControllerHost`; desktop main assembles only the Controller forwarder |
| Old path | Removed in D2 | `50300074` removes the old reverse command and LAN browser transport and preserves the original workbench receipt and reconciliation controls |
| R01 TEST1 native path | Done | Mac ARM64 nine checks, Linux ARM64 eight, including fault recovery and zero bypass |
| R02 performance | New path measured, see §2 | Owner accepts the shortfall; the old path has no WAN measurement, only an estimate |
| R03 tool semantics | Done | Evidence for each of the 13 public tools |
| R04 Mac Dock | Mouse close and Dock reopen passed | Owner confirmed the original conversation, tab and window dimensions. The same candidate process was still running after reopen |
| R05 host integration | Candidate checks passed within scope | Linux ARM64 actual GNOME/X11 session and private-root install/offline/purge with the current deb. Mac ARM64 separate candidate on the host and process-scoped offline run |
| R06 intermittent ISCP failure | Evidence-limited diagnosis and source instrumentation | New submit failure located at response authorization. Earlier read failures and the underlying cause remain unclassified |
| R07 D2 removal and paired cutover | Isolated rehearsal passed | See §9 for the two platforms, rollback and checksums. Production rollout awaits authorization |
| R08 owner sign-off | Accepted within the recorded scope | 2026-11-10. The owner approved an isolated D2 rehearsal. Active mail account and personal-script review are tracked in §8 |

Branch `codex/client-browser-controller` was pushed to the configured repository with owner
authorization. Product artifacts in §4 remain at `1f7f20ee`.

## 2. Measured performance

### 2.1 Method

On 2026-11-10 the unmodified Mac ARM64 candidate ran 12 consecutive independent tasks from this
machine through the public TEST1 Relay to the dedicated work2 acceptance Gateway and the real
model. Each task creates a conversation, submits "fill Alice into Display name", has one
`browser.type` approval granted by script, and ends when the task is delivered and the page
holds the value. All 12 succeeded. Timing runs from submit to delivered; the script polls every
100 ms with one reconcile call per poll.

### 2.2 Results

| Metric | Value |
|---|---|
| Full task, median | 26.3 s |
| Minimum / maximum | 25.5 s / 27.7 s |
| First task (cold Controller start) | 27.7 s |
| Median of the following 11 | 26.3 s |
| Application launch to ready | 3.5 s |
| Reverse calls per task | 35 (acquire 1, execute 31, release 1, status 2) |

Cold start adds about 1.4 s and variation between tasks is small.

### 2.3 Where the time goes

Median breakdown of one task:

| Phase | Time | Determined by the new path |
|---|---|---|
| Submit to first browser call (model planning) | 8.9 s | No |
| 23 browser calls before approval, with model gaps | about 8 s | Partly |
| Waiting for the approval and granting it | about 3.9 s (the grant call itself 0.76 s) | No |
| 12 browser calls after approval, to delivered | 5.4 s | Partly |

Client-side cost of the 35 reverse calls:

| Per call | Median | Per task |
|---|---|---|
| Fresh issuer authorization check on request arrival | 66 ms | 4.8 s for both checks |
| Fresh issuer authorization check before the reply | 70 ms | — |
| Actual Controller/MCP/page execution | 14 ms | 1.0 s |
| Gap between calls (WAN round trip plus Gateway work) | 170 ms | about 4.3 s (excluding three long waits) |

Conclusions:

- Actual browser execution is about 1 s (4%).
- Transport overhead of the new path is about 10 s (about 38%): roughly 290 ms per call, 35 calls.
- The remaining 15 s or so is model planning, approval and delivery, independent of the path.
- The two largest levers are the round-trip count (35) and the two authorization checks per
  call (4.8 s).

### 2.4 Gap to the old path

The WAN transport row estimates the old path using the new path's per-call cost.

| Comparison | Old path | New path | Basis |
|---|---|---|---|
| Local mock task median | 2.7 s | 4.3 s | Measured, 10 each, `4615ad82` |
| Reverse calls per task | 11 | 35–36 | Measured |
| WAN transport overhead | about 3 s | about 10 s | Old path estimated at the same per-call cost |

On that estimate the same form task is about 7 s slower on the new path over the WAN: 26 s
against roughly 19 s.

### 2.5 Not covered

- Linux ARM64 has one 26.1 s observation and no multi-sample statistics.
- Only the form-fill task was measured; screenshots, chunked large results and multi-page tasks
  were not timed.
- The maximum of 12 samples is not a production tail estimate.
- The old path's WAN figure is an estimate.

### 2.6 Measuring again

The benchmark script and evidence are in the private cache
`/Users/dev/.cache/sparkclaw-controller-20261109/test1-native-v1/`. The script is
`mac-network-fixture-v1/bench-test1-workflow-v2.mjs`; the evidence archive is
`mac-bench-r02-evidence-v1.tar.gz` (SHA-256
`b631a6ef026fad7b8f8ba07b13f8209aaf81afa61067471a92e65a3724e1da60`).

Run from the script's directory with a new evidence name each time:

```bash
SPARKCLAW_CONTROLLER_ISCP_LAB=/private/tmp/sx-controller-iscp-test1-mac-arm64-20261110-v1 SPARKCLAW_CONTROLLER_CANDIDATE="/Users/dev/Applications/SparkX Controller Candidate 1f7f20ee.app/Contents/MacOS/SparkX" SPARKCLAW_CONTROLLER_NODE_MANAGED=1 SPARKCLAW_CONTROLLER_EVIDENCE_NAME=test1-bench-r02-v3 SPARKCLAW_BENCH_SAMPLES=12 node bench-test1-workflow-v2.mjs
```

Results go to `<LAB>/evidence/<name>/bench.json`, with per-call latency traces in `traces/`
beside it. Each run calls the real model and leaves that many task records on the acceptance
Gateway.

## 3. Known issues

### 3.1 Intermittent submit failure: `Backend request is unavailable`

The first benchmark run delivered three tasks and encountered this error on the fourth submit.
The second run delivered 12 more tasks. The first run's trace identifies the failing stage:

1. The `execution.submit` request is sent normally; the Gateway accepts it and replies, and the
   encrypted response reaches the client.
2. The client helper runs its fresh issuer authorization check on that response, which returns
   an error after 47 ms (62–73 ms is normal).
3. The client discards the response and the UI reports `Backend request is unavailable`.

Effect: the Gateway has accepted the task while the client believes the submit failed, leaving
a task that exists only on the Gateway. Do not resubmit the original request.

The candidate's `authorizeResponse` trace records `error` for the 46.9 ms authorization
check. The cause is still unknown. The earlier Mac preapproval and Linux restore reads are
separate incidents and retain their original evidence and outcomes.

Source commit `b7551a9a` adds a correlated error class to the opt-in private trace, including
the node issuer HTTP status or a transport failure category where available. It preserves
the fail-closed authorization and original-request reconciliation behavior. Focused race tests
and vet pass. The candidate in §4 predates this diagnostic change.

To reproduce, run the §2.6 benchmark; `bench-test1-workflow-v1.mjs` stops at the first failure.

### 3.2 Other things to know

- **Hard cutover**: this branch has no old/new selector. A new Gateway with an old client has
  no browser automation, so Gateway and client must be released as a pair.
- **The acceptance device profile is in a temporary directory**: the enrolled Mac profile is at
  `/private/tmp/sx-controller-iscp-test1-mac-arm64-20261110-v1/controller-native-profile`.
  Once the system clears temporary files the enrollment is lost and the device must be enrolled
  again.
- **Evidence is private and local**. The non-cache handoff at
  `~/Library/Application Support/SparkClaw Acceptance/controller-1f7f20ee-2026-11-10/`
  contains 444 verified copies of indexed files, indexes, artifacts and the benchmark archive.
  `evidence-manifest.json` maps the frozen paths to the copies and records their SHA-256 digests.
  The handoff is on the same Mac disk. Credentials and enrolled profiles remain in their original
  private locations.
- **Acceptance services are still running**: the TEST1 acceptance node
  `sx-controller-test1-work2-20261110-v1` and the dedicated work2 issuer and Gateway remain up;
  decide whether to stop them after acceptance.
- **The Linux ARM64 host's original Gateway** was already in a restart loop before any candidate
  activity; this work did not address it.
- **Running tests on this machine**: the default `TMPDIR` passes through the `/var` symlink and
  makes `browserhost` and some Controller tests report unsafe paths; run
  `export TMPDIR=$(cd "$TMPDIR" && pwd -P)/` first. `gateway` package tests that need
  `/dev/shm` must run on Linux.

## 4. Candidate and evidence

Separate Mac ARM64 candidate: `/Users/dev/Applications/SparkX Controller Candidate 1f7f20ee.app`.
Its ASAR SHA-256 is `f2deabcc9532e0f2ccc65a1f04903f06dd5e910f5bd0215418b78446041d1a8c`.
ASAR, Controller, original UI and preview resources are byte-identical to `b175325b`; only the
ISCP helper changed. The production `/Applications/SparkX.app` is unchanged.

The four current archives are under
`/Users/dev/.cache/sparkclaw-controller-20261109/assembly-test1-clock-arm64/apps/desktop/dist/`;
their digests were recomputed and matched on 2026-11-10:

| Artifact | SHA-256 |
|---|---|
| `SparkX-0.1.0-mac-arm64.dmg` | `b502cf6e836034a1ea477ec36aa9e9f7757b47c437dff67415e791eec9265ccb` |
| `SparkX-0.1.0-mac-arm64.zip` | `0afe858d840d8f1bb90a25b6f347924d23e2fc096caf8e461e51462a90a9f9c1` |
| `SparkX-0.1.0-linux-arm64.deb` | `0f8849172d1f84f2b5224572aa9798c16121c834b47cfba6a8d6f6d507b1c2b8` |
| `SparkX-0.1.0-linux-arm64.AppImage` | `94ba0c0aef8b774d6fa94048842ecbd032a9c84723831eb54302305e836c9a37` |

The private checkpoint index is `candidate-1f7f20ee-index-test1-v1.json` and its parent is
`candidate-b175325b-index-arm64-native-v1.json`, both in that cache root. Earlier actual dpkg
installation/purge evidence retains its exact `b175325b` scope.

TEST1 acceptance result: both ARM64 platforms pass real-model approval/fill, original-panel
interaction, process-scoped Relay fault/recovery and zero-bypass checks, observing all 14
required PIDs per platform. Mac also passes offline native operation with synthetic local
authority. The frozen checkpoint retains 34 matching completed receipt pairs and one Gateway-only
unknown from a Mac preapproval read. The benchmark submit failure in §3.1 is a later incident.

On the Linux ARM64 host's existing GNOME/X11 session, the current candidate passes seven checks
including the actual form fill, original panel controls, Chromium renderer namespace and clean
quit. This run used the dedicated TEST1 profile and private keyring with the extracted deb. The
same deb separately passes private-root `dpkg` install, offline launch and purge with 7,011
verified entries and the closed profile preserved. The host package, desktop entries and
AppArmor baseline were preserved. Evidence directories in the local handoff are
`linux-host-x11-v1/` and `linux-dpkg-1f7f20ee-v1/`.

## 5. Launching the candidate

The candidate uses its own enrolled profile and reaches the dedicated acceptance Gateway through
the public Relay, without touching the production installation:

```bash
SPARKCLAW_DESKTOP_USER_DATA_DIR=/private/tmp/sx-controller-iscp-test1-mac-arm64-20261110-v1/controller-native-profile "/Users/dev/Applications/SparkX Controller Candidate 1f7f20ee.app/Contents/MacOS/SparkX"
```

The benchmark and the manual Mac Dock check used this separate profile. The
`https://controller.fixture.test/` page used by the
automated checks is injected by the script and does not exist in a manual session, so prepare a
test page with no real business submission.

## 6. Next tasks

This is the delivery sequence. §9 records the completed isolated upgrade and rollback.

1. **Branch and evidence.** The branch is published and the verified private copy is in §3.2.
2. **Experience acceptance.** The owner confirmed the Dock check (R04) and accepted the
   candidate experience (R08) within §8's scope.
3. **Intermittent submit failure.** Keep fail-closed behavior and preserve original requests.
   Use the error classes in `b7551a9a` to diagnose a recurrence in the next build.
4. **D2 removal of the old path (R07, complete).** Design §10 is authoritative for scope:
   - Remove: the `browser.command` registration and generated entries (`operations.json` on both
     sides), `Broker` reverse dispatch,
     `apps/desktop/src/browser/{host-agent,native-page-commands,host-socket}.mjs`, acceptance of
     `browser.command` in `iscp-transport.mjs`, and the LAN host grant/connect/revoke/fences
     routes with their dedicated grant/heartbeat.
   - Keep: broker scope/receipt/fence services, `HostJournal`, workbench IPC and reconcile UI,
     backend `BackendHost`, shared page/isolation helpers.
   - Migrate helpers still in use before deleting files. Active runtime and build references
     must reach zero; historical documents need no edits.
5. **Rebuild and rehearse the paired upgrade (passed).** Stage the matching Gateway, helper and both
   ARM64 clients. Stop new browser admission, drain active tasks, retain pending fences and the
   original ClientStore/profile/installation binding, install the matched versions and verify
   capability before enabling.
6. **Verify rollback (passed).** Stop admission and child processes and restore the previous
   Gateway, helper and clients. Verify the original installation identity, profile and pending
   fences. Use the rehearsal record in §9 for the separate production rollout decision.
7. **Wrap up after rollout and evidence handoff.** Stop the dedicated acceptance services after
   the rollback record and outstanding outcomes are preserved. Reclaim rebuildable cache data
   after checking the independent copies and retaining the indexes and unknown receipts.

## 7. Owner experience acceptance

Launch the candidate as in §5 and check each row. Use dedicated acceptance tasks and pages with
no real business submission.

| Step | Owner action | Expected result |
|---|---|---|
| 1 | Open the original workbench, settings, history, files and browser entry | Original menus/layout/interactions remain; existing data and drafts are present |
| 2 | Open a personal tab, resize the panel, switch conversations, then hide/reopen | Selection, geometry and the personal page remain; no persistent technical status is added |
| 3 | Run an independent new task to read a prepared page and capture a screenshot | The correct task page and screenshot appear through the original result/file UI |
| 4 | On a prepared form, fill, type while focused, select and submit, approving only expected actions | Page values match, submission occurs once, and the personal tab is unaffected |
| 5 | Cancel one new task, then run an independent task | Cancellation takes effect, earlier uncertain writes are not replayed, and data remains |
| 6 | On Mac, mouse-close the window, click the candidate's actual Dock icon, then quit/reopen normally | The original workbench returns with correct geometry, selected conversation and data |
| 7 | Reopen the original mail and personal-script entries | Their original entry points and expected functions remain available for authorized accounts |

Per §2 a form task takes about 26 s, with about 20 s before the approval appears; this is
expected. On the §3.1 submit failure, note the time, do not resubmit the original request, and
continue with a new task. For other failures retain the original task/command and evidence and
do not clear logs; record a new attempt after any fix separately.

## 8. Acceptance record

Review date is 2026-11-10. Engineering checks used the §4 package hashes, bundled Electron
44.4.3 and Node 24.21.0, a separate Mac ARM64 candidate and a Linux ARM64 GNOME/X11 host.
The live checks ran on dedicated TEST1 devices over the public Relay to the work2 acceptance
Gateway and real model. Offline checks used synthetic local authority.

| §7 step | Recorded observation | Evidence and scope |
|---|---|---|
| 1 | Original workbench, history, file and browser entries visible; prior draft and attachment reviewed in an earlier candidate | Current Mac Dock session and Linux host workbench screenshot. Earlier cached UI review `arm64-native-v1/mac-ui-cua-review-v1.json` |
| 2 | Original tabs, hide/reopen and widen/restore pass | Both current TEST1 fixtures and `linux-host-x11-v1/` |
| 3 | Large native PNG delivered over encrypted ISCP with original file IPC and matching dimensions/hash | Current Linux GNOME/X11, `linux-host-experience-v1/screenshot/` |
| 4 | Real-model approval and actual Alice fill pass. Focused typing and a single submit pass in current offline fixtures. Select is covered in the scoped tool evidence | §4 and design §15.2, §15.5. These are separate scenarios |
| 5 | Cancel retires the original approval, keeps the cancel intent and earlier fences, and allows a separate task | Current Linux GNOME/X11, `linux-host-experience-v1/cancellation/` |
| 6 | Owner clicked the actual Mac close control and Dock icon, then confirmed the same conversation, tab and dimensions. The reopened state was checked in the same process | Owner review plus original workbench state. Current fixtures separately verify normal quit and retained data |
| 7 | Original mail entry visible. Active account and personal-script operation require owner review | Mail and script entry points are outside the generic Controller change |

| Item | Record |
|---|---|
| Accepted performance | Form task median 26.3 s on Mac ARM64. The roughly 7 s WAN gap is an estimate. Owner acceptance dated 2026-11-10 |
| Open issue | §3.1 response authorization failure. Existing unknowns remain fenced. Source adds diagnostics for a future recurrence |
| Candidate experience decision | **R08 accepted**. On 2026-11-10 the owner accepted the documented scope and approved an isolated D2 upgrade and rollback rehearsal. Active mail account and personal-script operation remain for owner review |
| Paired release decision | **R07 isolated rehearsal passed. Production rollout awaits separate authorization** |

Record the owner and the accepted scope when signing. Production rollout has a separate decision.

## 9. D2 delivery and paired rehearsal

On 2026-11-10, source `50300074` removed `browser.command`, old Broker dispatch and the LAN
browser grant, connection, heartbeat and receipt routes. The original workbench IPC continues to
display and reconcile write receipts. Reconciliation submits over ISCP and its controls stay
visible but disabled on LAN. Existing receipt formats and backend acquisition remain in use.

The four clients and matching Gateway are in `d2-50300074/artifacts/` under the handoff in §3.2.

| Artifact | SHA-256 |
|---|---|
| `SparkX-0.1.0-mac-arm64.dmg` | `fa510efa8be13fba0ed9c08852d661ce0dc4e0beef5edf9334b0f27770cc22ce` |
| `SparkX-0.1.0-mac-arm64.zip` | `49c6a78627c90d98c0d55bad9f988cca0ba2328b0e012396e604011d82254b94` |
| `SparkX-0.1.0-linux-arm64.deb` | `f188860cd801f2d5d16e9cc271397dea54c1113bfb63ea8ae4d9c60fbc1e04b8` |
| `SparkX-0.1.0-linux-arm64.AppImage` | `9fcad734b586256d1256494ef7f1e7aef6cddc655a3b1b1ceec0930724f94b79` |
| `gateway-d2-50300074` (work2, Linux amd64) | `8c2770abfb964de38cfaf8bf167f1e11df19a5dff420bc384375e64d2c12a03e` |

Mac ran the separate candidate installed from the read-only DMG, with 7,156 files and symlinks
verified. Linux ran a private deb extraction with the sandbox enabled. The current deb separately
passed private-root dpkg installation, offline execution and purge with 7,011 verified entries.
Both platforms connected
through TEST1 to the dedicated Gateway and passed prepare, upgrade and rollback in the sequence
`1f7f20ee` → `50300074` → `1f7f20ee`. Upgrade and rollback each passed eight checks per platform,
including the same installation identity, original history, draft, attachment, cookie, cancellation
records and unknown write. New commands for the unknown and canceled tasks were refused before
native dispatch. Separate new tasks delivered.

The final package rehearsal preserved 134 existing Gateway receipts byte for byte on upgrade
and 140 on rollback. The final read matched the original unknowns from both clients by command, digest and
complete scope. Production containers and the dedicated issuer kept their configurations and
start times. The dedicated Gateway is back on the previous binary and passed its health check.
See `paired-summary.json` and the `gateway/`, `mac/` and `linux/` evidence directories.

The first Gateway switch encountered an evidence writer error. A subsequent audit treated
Docker's variable mount order as a change. The repeated switches passed with a stable mount
comparison. The initial Linux upgrade stopped at keyring socket setup, then passed with a
shorter private runtime path. The first package build used restrictive installation directory
modes. Private extraction ran successfully and the ordinary-user launch after root installation
failed. Rebuilding all four clients under `umask 022` passed installation and the paired rehearsal.
The first Mac rollback with the final packages hit a backend error on a reconciliation read.
A later read-only check found the original task delivered, and the separate new task passed.
The first result and the unclassified read failure remain in the evidence.

Validation passed for 82 Go packages on Linux and `go vet`, 631 desktop tests with one skip,
597 WebChat tests, and the native browser panel's 48 layout cases and 12 task-page cases.
