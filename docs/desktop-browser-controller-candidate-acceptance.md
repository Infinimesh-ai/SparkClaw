# Browser Controller candidate acceptance — 1f7f20ee

> Language: English | [简体中文](../zh-cn/docs/desktop-browser-controller-candidate-acceptance.md)
>
> Updated: 2026-11-10. Work stopped at the owner's request after this checkpoint.
> Product source: `1f7f20ee27f45efe5dbf15e43c612a4d0cff88d6`.
> Engineering candidate; P3 and final owner acceptance remain incomplete. No production cutover.
> Final native scope is Mac ARM64 and Linux ARM64. Intel Mac is excluded.

The candidate embeds Controller and Playwright MCP in SparkX and carries generic browser
commands over authenticated ISCP. The existing workbench and browser panel remain the acceptance
surface. Mail, backend acquisition and personal scripts retain their current paths.
The [design](desktop-embedded-browser-controller-iscp-design.md) defines D1–D3 and the evidence
boundaries; §15.5 records the TEST1 checkpoint and retained failures.

## Candidate and evidence

Separate Mac ARM64 candidate: `/Users/dev/Applications/SparkX Controller Candidate 1f7f20ee.app`.
Its ASAR SHA-256 remains `f2deabcc9532e0f2ccc65a1f04903f06dd5e910f5bd0215418b78446041d1a8c`.
The new helper reserves a bounded initial Relay deadline margin. ASAR, Controller, original UI
and preview resources remain byte-identical to `b175325b`; its manifest provenance is retained.
The production `/Applications/SparkX.app` is unchanged.

The four current archives are under
`/Users/dev/.cache/sparkclaw-controller-20261109/assembly-test1-clock-arm64/apps/desktop/dist/`:

| Artifact | SHA-256 |
|---|---|
| `SparkX-0.1.0-mac-arm64.dmg` | `b502cf6e836034a1ea477ec36aa9e9f7757b47c437dff67415e791eec9265ccb` |
| `SparkX-0.1.0-mac-arm64.zip` | `0afe858d840d8f1bb90a25b6f347924d23e2fc096caf8e461e51462a90a9f9c1` |
| `SparkX-0.1.0-linux-arm64.deb` | `0f8849172d1f84f2b5224572aa9798c16121c834b47cfba6a8d6f6d507b1c2b8` |
| `SparkX-0.1.0-linux-arm64.AppImage` | `94ba0c0aef8b774d6fa94048842ecbd032a9c84723831eb54302305e836c9a37` |

The private checkpoint index is
`/Users/dev/.cache/sparkclaw-controller-20261109/candidate-1f7f20ee-index-test1-v1.json`.
Its immutable parent is `candidate-b175325b-index-arm64-native-v1.json`; the parent's eight
archives and earlier isolated checks remain historical evidence. Current native Mac runs use
the separate assembled application; Linux uses the new deb's private extraction. Earlier actual
dpkg installation/purge evidence retains its exact `b175325b` scope.

Both ARM64 platforms pass TEST1 real-provider approval/fill, original-panel interaction,
process-scoped Relay fault/recovery and zero-bypass checks. Mac also passes offline native
semantics with synthetic local authority. The final network runs observe all 14 required PIDs
per platform. Stopped clients retain 34 matched completed receipt pairs and one separate
Gateway-only unknown. The latter belongs to a failed Mac preapproval read whose cause remains
unclassified; later success does not convert that original request into a pass.

TEST1's additive acceptance node and the dedicated work2 issuer/Gateway remain running, with
identity and receipt state preserved for later continuation. Native fixtures, injected observers,
private displays, temporary firewall/AppArmor resources and diagnostic processes have exited.
Work stopped without publication, production installation replacement or active legacy removal.

## Engineering preparation still outstanding

1. R02: measure old and candidate paths on the same topology/load, including cold/warm samples,
   full-tool subcalls, byte counts, percentiles and throughput. The individual TEST1 workflow
   durations are not a comparative performance result.
2. R04/R05: complete actual Mac Dock interaction and remaining host desktop integration.
   Earlier Dock accessibility reads timed out; process-scoped offline checks are now recorded.
3. Keep original profiles, authority, data and unknown fences. The TEST1 acceptance identities
   are dedicated devices; do not retag an old isolated or revoked device.
4. R06: retain the explicit evidence-limited diagnosis for older lookup failures and the new Mac
   preapproval failure. The proven Linux deadline bug is separately fixed and regression-tested.
5. After applicable P3 gates, complete R07's D2 removal and full paired cutover/rollback. R08
   remains the owner's final experience decision. No further work runs while this task is stopped.

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
