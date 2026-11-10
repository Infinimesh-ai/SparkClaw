# Browser Controller candidate acceptance — b175325b

> Language: English | [简体中文](../zh-cn/docs/desktop-browser-controller-candidate-acceptance.md)
>
> Prepared: 2026-11-10. Engineering candidate; final owner acceptance is pending.
> Product source: `b175325b9e4c2976398288089e310dc2ea28a012`.
> This handoff does not close P3 or authorize production cutover.

The candidate embeds Controller and Playwright MCP in SparkX and carries generic browser
commands over authenticated ISCP. The existing workbench and browser panel remain the acceptance
surface. Mail, backend acquisition and personal scripts retain their current paths.
The [design](desktop-embedded-browser-controller-iscp-design.md) defines D1–D3 and the evidence
boundaries; its §15 is the finite engineering closeout ledger.

## Candidate and evidence

Installed Mac ARM64 candidate:
`/Users/dev/Applications/SparkX Controller Candidate b175325b.app`.
Its `Contents/Resources/app.asar` SHA-256 is
`f2deabcc9532e0f2ccc65a1f04903f06dd5e910f5bd0215418b78446041d1a8c`.
The production application at `/Applications/SparkX.app` has not been replaced.

All eight archives live below `/Users/dev/.cache/sparkclaw-controller-20261109/`.
Directory keys expand to:

- A: `assembly-enrollment-b175325b/apps/desktop/dist/`
- B: `assembly-mac-x64-b175325b/apps/desktop/dist/`
- C: `assembly-linux-arm64-b175325b/apps/desktop/dist/`

| Directory | Artifact | SHA-256 |
|---|---|---|
| A | `SparkX-0.1.0-mac-arm64.dmg` | `416aca39a8566f61c1cf632cdeecdeb0db053f2240ba07bd773cae163069d19f` |
| A | `SparkX-0.1.0-mac-arm64.zip` | `286a01b7e182480d7676a1d8b7a5436dcfc30c5960eb2f7a3e1ed7897d1bafc0` |
| A | `SparkX-0.1.0-linux-amd64.deb` | `bec9fe88f7251b962472ff2c8168611dc0e97334c59f5244a87e6a7cc448abc4` |
| A | `SparkX-0.1.0-linux-x86_64.AppImage` | `6acc14114327e07f417bd29b807a5f5e72caa0c03d8c21ab8179934ef2b05219` |
| B | `SparkX-0.1.0-mac-x64.dmg` | `083090d7c1063e8948374a48046e53cc8b2d3d51563bf8cfb286c90a90e15ba7` |
| B | `SparkX-0.1.0-mac-x64.zip` | `821df2593be0be6f71b7f2b816a004e39cbeefb843b3a6a2b00fdbdec26a27c2` |
| C | `SparkX-0.1.0-linux-arm64.AppImage` | `06eab873edd5a744f2cc6698545ef14af23eba0b0e9e68ffc54a1b674a8380ba` |
| C | `SparkX-0.1.0-linux-arm64.deb` | `6febe43b75d284dbea86a1d51bd3411213e8c5c754d70b873667cec4fb77a6fa` |

The frozen index is `candidate-b175325b-index-dpkg-native-v1.json` under that cache root:
99,080 bytes, SHA-256 `9028d24efe1f672a5b42e32e476cee52673e3b286af2b62b0a265d54f5c96b42`.
It records 136 evidence files, 61 frozen qualification sources and eight artifacts;
qualification source/docs are at `eea088a8`. These are private local evidence paths, not published
URLs. `node-lab-v1/candidate-owner-handoff-artifacts-b175325b-v1.json` independently rechecks
all eight archives and the installed Mac ASAR for this handoff. No credential is included.

| Platform | Qualified scope | Still required |
|---|---|---|
| Mac ARM64 | Separate installed candidate; native semantics, routes and scoped encrypted-ISCP/lifecycle evidence | TEST1, actual Dock/original UI, Mac offline GUI |
| Linux x64 | Native work2 evidence; isolated-root real dpkg install/configure/offline GUI/purge, final 7 installation + 8 native checks | TEST1 and host desktop/install integration |
| Mac x64 | DMG/ZIP and architecture/resource audit; bundled Node executes under translation on ARM64 | Intel Mac native installation/GUI/offline checks |
| Linux ARM64 | AppImage/deb, architecture and resource audit | ARM64 host native installation/GUI/offline checks |

## Engineering preparation

The engineer owns these steps. The owner supplies unavailable access/hosts and performs the
final experience review; the owner is not expected to run fault fixtures.

1. Restore TEST1 access and record its deployment/configuration baseline. Complete R01 on the
   required topology, then R02's same-topology old/new timing and transfer measurements. A
   reachable SSH port or successful work2 lab run does not close these gates.
2. Complete R04 after manual Mac unlock and the remaining R05 checks on corresponding hosts.
   Keep Chromium sandboxing enabled; static archive audits do not establish native passes.
3. Start the exact candidate with an explicitly selected acceptance profile; verify installation,
   device and connection before presenting its window. Preserve existing profile authority.
   TEST1 needs a correctly enrolled TEST1 acceptance device: do not retag the isolated work2
   identity, reuse a revoked device or launch against default production user data. Preserve
   history, drafts, files, cookies and unknown-write fences.
4. Keep R06 classified as evidence-limited: original lookup errors lack raw cause/status/helper
   stage. The review is available; the original runs did not pass. If a new run reproduces it,
   use the existing bounded read observer and retain both runs.
5. After applicable P3 gates pass, complete R07: D2 legacy-path removal and paired
   Gateway/issuer/helper/client upgrade and rollback rehearsal. Present the final candidate and
   evidence before requesting any separately authorized production publication.

At this handoff, TEST1's known SSH entry rejects the local key and CUA reports the Mac locked.
No work2 password was tried on TEST1. Isolated lab activity is stopped; opening the app alone
neither restores that lab nor establishes TEST1 connectivity. The remote b175 AppImage archive
is retained, but its extracted tree was reclaimed. Re-extraction requires the existing 4 GiB
free-space reserve. Earlier shared-disk exhaustion interrupted production PostgreSQL; recovery
and the changed runtime baseline remain recorded in the design and evidence index.

## Owner experience review

Run these steps once engineering presents the prepared candidate and names its ready platform
and topology. Use dedicated acceptance tasks and a fixture with no real business submissions.
Record platform, candidate hash, device and evidence reference for each result.

| Step | Owner action | Expected result |
|---|---|---|
| 1 | Open the existing workbench, settings, history, files and browser entry | Original menus/layout/interactions remain; existing data and drafts are present |
| 2 | Open a personal tab, resize the panel, select another conversation, then hide/reopen the panel | Selection, dimensions and personal page remain; no permanent technical status UI appears |
| 3 | Ask a new task to read the prepared page and produce a screenshot | Correct task page and screenshot appear through the existing result/file UI |
| 4 | Use the prepared form for fill, focused typing, selection and submit; approve only the expected action | Actual values match the task, submit occurs once, personal tabs remain untouched |
| 5 | Cancel a new task, run an independent task, and observe the separately staged connection-loss/recovery case | Cancellation takes effect; uncertain earlier writes are not replayed; data is preserved |
| 6 | On Mac, close the window with the pointer, click the actual candidate Dock icon, then quit/reopen normally | Original workbench restores with correct geometry, selected conversation and data |
| 7 | Reopen existing mail and personal-script entries | Original entry points and expected behavior remain available within authorized accounts |

Keep the original task/command and evidence on failure. Do not clear journals or resubmit an
uncertain write to obtain a passing result. Record an independent attempt separately after a fix.

## Acceptance record

Record reviewer/date, source/artifact hash, OS/architecture, device/topology, step results and
evidence, accepted limitations, unresolved defects and final decision. The decision is currently
**pending**. Scoped UI acceptance does not accept untested platforms or authorize publication.
R08 handoff preparation is complete; final owner signoff remains open.
