# Mail identity and recovery: release validation

[简体中文](../zh-cn/docs/mail-identity-release-validation.md) · [App-CLI integration validation](app-cli-implementation-validation.md)

Updated 2026-10-21. **Stopped at the user’s request. `.31` is installed on Mac and healthy on work2; original Mac mail checks report all three providers ready/healthy. The `.30` run delivered all six messages, four automatically captured and two excluded in Gmail Spam. The new `.31` Outlook reply failed before dispatch and sent no email. Gmail normal-Inbox autosync and the reply path remain unaccepted.** Normal startup and preservation are complete; source pushes and formal publication remain unfinished.

## Paired versions

| Component | Frozen value / status |
| --- | --- |
| Final SparkClaw release source | `14d1c8ac5065c883fb27ec199ce91141cc9cb544` |
| Tested Gateway/runtime source | `f219bbb6c628301094bf8c2a871ed46b54ef72c9` |
| Tested Controller fixture correction | `a1cde4fbe290ec24e85b6e1777c6d8f8808710b3`, merged as `3b5fae80`; test-only change |
| AppCLI release | `0.3.0-sparkclaw.31` |
| AppCLI source | `a91396eb68845b2f85f6786f2677e35858d95de6` |
| Runtime digest | `697d649969a9c5b405cae6be933661c4f15995d64f8d4c5ba6f8373a498db205` |
| Browser Bridge | `1.0.29`; closure `152141b6cf731ea05232eee510a9ea6459406774b75ea4170a004e1828332596` |
| Managed Linux extension | Tampermonkey upstream `5.5.0`, product `5.5.0.14` / patch `14`; second-start native import and actual installed gates passed; work2 healthy at 10:52:03.839267 UTC |
| Runtime / Host / ledger protocols | `3.0` / `1.0` / `2`, unchanged |
| Mac `.31` package | ARM64 DMG/ZIP built from `14d1c8ac`; 277-entry parity passed; installed 10:37:04.921526 UTC |

Artifact filenames and full hashes are pinned in [the release manifest](../configs/app-cli-release.json). Managed Reader source hashes and stable UUIDs are pinned in [the component manifest](../configs/browser-components.json). Reader version strings alone do not establish that the installed bytes are current.

## Repaired design boundaries

1. **Account identity and delivery addresses.** Outlook's authenticated internal account identifier is a valid Reader and storage identity but is not necessarily a deliverable recipient. The implementation preserves `account_address`, existing mailbox IDs, capture namespaces, originals and draft references. Paired optional `delivery_address` and `account_aliases` fields carry native same-account evidence separately. Outlook uses its authenticated native default sender and alias selectors, validated against the same account's proxy addresses; arbitrary message From headers cannot establish ownership. Missing or conflicting evidence withdraws the communication projection, without preventing a valid captured page from being committed. Reply-all excludes verified owner aliases, including case variants. Pausing intake retains the current transactional metadata instead of restoring a stale earlier copy.

   Gmail had a separate localization defect: its Reader expected the English account-button label, while the live signed-in page used Chinese. Release `.30` identifies only the unique visible header account button with the exact HTTPS Google Accounts `SignOutOptions` destination and one consistent address. The Reader and managed-send guard share this extraction. Mail body text, account-picker lists and global fallback addresses are not identity evidence. Existing expected-account and account-switch checks remain. Login readiness is distinct from the optional account hint.

2. **Unknown intake results and business commit evidence.** A ready login probe did not create a receiving mailbox, and an attempted intake operation with an unknown receipt had no safe way to become a new foreground action. `mail.intake.reconcile` now binds the original operation, scope and digest to its durable business result. A committed result is recovered; a provably uncommitted operation is closed as `not_applied` through a transactional provider-version CAS fence that prevents late execution of the old request. Receipt GET remains read-only. Conflicting versions or insufficient evidence stay unknown. A new explicit user action uses the current provider version, including when no mailbox exists. This mechanism does not replay an unknown send or other unrelated mutation. The evidence does not identify the original interruption that first produced the historical unknown intake.

3. **QQ write dispatch and read-state proof.** Successful dispatch did not prove that the remote message was read. The previous confirmation path could depend on stale cached state or an unsuitable list request. The new path makes a bounded fresh exact-target query, validates the account, message identity, receipt time, folder and response completeness, and only then reports read state. A previously released unknown or pending write may perform read-only confirmation; it does not click or dispatch again. Foreign, ambiguous, locked, overflowing or malformed results remain unconfirmed. A confirmation failure does not undo a durably stored original.

4. **Business completion and resource cleanup.** `.31` preserves a schema-validated business result after checking authorization, cancellation and deadline, then performs one bounded Host release. A cleanup failure records `host_cleanup_failed` and preserves Host fencing/retirement; it does not turn an already proved send into an unknown outcome. Terminal publication and ACK remain after the cleanup attempt, avoiding authorization teardown racing with release. An invalid or missing result, cancellation/deadline before validation, and a crash before durable terminal storage still follow the original failure/unknown rules. Existing uncertain sends are queried or reconciled under their original identities, never sent again. A native sent journal alone is distinct from the Executor having accepted a result.

5. **Owned page closure and event ordering.** Bridge `1.0.29` fixes an independently reproduced sequence: `chrome.tabs.remove` resolves before `onRemoved`/`onDetach`, the old code forgets ownership, and later events are filtered before Playwright receives its required detach. Successful removal of the exact authorized IDs now uses the existing ownership-checked removal-event path before forgetting them. Earlier/later native events deduplicate; failed removal and released/foreign tabs cannot fabricate closure. Actual pinned Playwright consumer tests prove the defect and fix. Production logs did not capture native event order, so the two live Outlook timeouts cannot be conclusively attributed to this sequence. This fix does not make cleanup health equivalent to business delivery.

6. **Deterministic managed component staging.** `.31` derives the provisioning generation from canonical public manifest inputs, verified script/dependency payload, native import options, retired managed UUIDs and a versioned generation schema. Time, stage/cache paths and private profile state are excluded. Two stages of the same pinned official CRX and unchanged four-script inputs previously produced different workers and policies; after the fix all 88 staged files are byte-identical. Reinstallation reuses the native consumed-generation key. Explicit repair/reimport requires a different product version, normally an increment with patch revision; the existing pre-write rejection of changed worker bytes at the same version remains, including support for a deliberate paired rollback to a different version. Tampermonkey `.14` is integrated, isolated stage/install tests pass, and live native import passed on the second controlled start; work2 subsequently passed actual installed gates and healthy cutover. No native consumed records or profile storage are cleared.

The existing menus, page structure, layout and controls are retained. Verified delivery labels are projected into the existing sending selector, mailbox filter, receiving settings and detail positions. Controls continue to use stable mailbox IDs and versions. No new permanent technical-status panel was added.

## Deployment gates and the first-import limitation

Runtime installation, Controller dependency selection, Linux managed Reader installation/import and Mac generated Reader assets are four separate deployment surfaces. The first `.29` cutover updated the runtime and dependency but left older system Reader bytes and policy installed. The Mac packaging check separately caught stale generated Reader files before an installable package was accepted. Both are deployment-completeness defects, not evidence that a ready socket or a matching version string qualifies the new Reader.

Linux Reader source changes also change the generated extension service-worker filename. The installer rejects changed managed worker bytes at an unchanged product version before modifying the installed system; a different-version paired rollback remains allowed. `.30` used product `5.5.0.13`; its stage included a wall-clock provisioning value, so restaging identical inputs produced a different worker identity. The integrated `.31` deterministic-stage correction removes this reproduced source of drift without weakening the version guard. Tampermonkey `.14` repeated-stage identity, isolated same-artifact installation, native-generation key preservation and versioned reimport checks pass. These artifact/install checks are separate from the live native-import result below and do not establish final work2 health.

The `.12` first-start observation has an explicit causal limit. Formal `wait-profile` failed because the current import hash was absent and the three mail Reader source hashes were old. The new worker was already registered, and the native scripts had `enabled=true` and `system=true`; there was no demonstrated native state named “unknown”. On a second controlled start the current hash was consumed and all four managed script hashes matched. No exception from the first attempt was captured, so neither a particular initialization race nor the version bump alone is established as its cause or cure. Historical mail-task `unknown`/`uncertain` outcomes are a separate effect-safety boundary.

The retained activation runner keeps Gateway and Executor admission stopped, requires no active/pending-release task or cleanup fence, cold-backs up the existing profile and components, and installs the fixed candidate through the formal installer. Only a failed `wait-profile` may lead to one additional ordered Controller/Browser restart, after rechecking quiet admission and exact historical unknown-row hashes. Activation permits at most two Browser starts. Installation, configuration, ledger, preference or final-gate failures are not automatically retried. Failure leaves admission stopped and preserves diagnostic evidence; it does not clear native storage, delete a fence or roll back a live profile. Automatic runtime-only rollback is disabled: recovery must keep the runtime, Bridge and managed components matched, with current business and execution ledgers retained. Native session restoration is requested, but arbitrary unsaved page state is not claimed to be preserved without inspection.

The final `.31` attempt began at 10:45:42.250894 UTC. Its first native import check failed with the current hash absent (10:45:46.079244–10:46:47.129516); the permitted second start passed native import at **10:46:56.044456 UTC**. This recurrence leaves the first-import cause unresolved; deterministic staging is not evidence that the initialization problem is cured. Installed component closure and the native Bridge `1.0.29` handshake had passed, but a newly added private final gate incorrectly required `Preferences` to contain `manifest.version` for unpacked extensions. Both the cold `.30` snapshot and current `.31` state omit that field. The gate stopped at 10:46:56.842233 UTC with admission still closed and all **81 cutover-baseline unknown rows unchanged**.

Read-only diagnosis corrected that gate to check the persisted exact path, location `8` and enabled registration, while installed manifests, closure hashes and native handshakes establish the component version. All 23 changed preference paths were accounted for as browser bookkeeping; Secure Preferences was unchanged. The revised private activation, one-shot resume and parent-runner suites passed **17 + 5 + 18 = 40** tests. The reviewed one-shot component resume completed at 10:50:59.844167 UTC, using the existing imported components without reinstalling or performing a third Browser start; Controller starts established the loaded handshake. The temporary runtime override was removed. The parent cutover then verified actual Bridge `1.0.29`, native Reader import and paired runtime gates before reopening admission and reaching healthy state at 10:52:03.839267 UTC. This was a deployment-gate correction, not an additional product/runtime patch.

The activation interruption and corrected gate are retained in `activation-evidence/activation.json`, `script-review/resume-review-result.json`, and the activation/resume/parent test logs. These records preserve the failed first attempt and the stopped-admission interval rather than rewriting the rollout as a single successful start.

Before reopening admission, the release must pass runtime-file verification, system receipt/policy checks, native consumption of the current provisioning hash, exact enabled Reader source/dependency hashes and the read-only installed-release qualification. A fresh owned-page Reader must then expose the intended capabilities and its result must reach the Gateway mailbox projection. Browser restart by itself is not success.

## Verified test and build scope

| Check | Evidence-backed result |
| --- | --- |
| AppCLI `.31` runtime | 457 passed, 2 explicit skips, 0 failed |
| AppCLI Python | 86 passed |
| Paired release qualification | Fresh install, exact native runtime, tamper/mixed-release/drift/live-activation rejection, whole-set rollback, durable-state preservation and monotonic epoch passed |
| Gateway on Linux, `f219bbb6` | Build, vet and all 75 test packages passed with `go test -count=1`; final `14d1c8ac` Gateway build inputs are identical |
| Linux Controller, `f219bbb6` plus test-only `a1cde4fb` | 306 passed, 109 existing platform/opt-in skips, 0 failed; all 10 locked dependencies freshly installed offline, `.31` runtime verified across 82 files, postinstall patch and generated contracts checked |
| Browser Bridge | 70 passed; two event-order cases against the actual pinned Playwright consumer passed after the causally reproduced failure |
| Bridge release parity | Controller native protocol 5 passed; artifact/package suite 15 passed; version and source closure verified |
| Intake/address storage and concurrency | Earlier memory/file/PostgreSQL fault cases passed, including late commit exclusion, scope isolation, negative fences and preserved original/capture/draft identities; unchanged in `.31` |
| WebChat | Prior integrated 432 passed with unchanged final source; final `.31` package TypeScript/Vite build passed |
| Desktop / Mac `.31` | Final source 507 passed, 0 skipped/failed; fresh ARM64 package passed 66 desktop sources, 8 Bridge sources, 5 UI files, helper and all 277 manifest entries; original Mac mail checks passed; normal startup and final preservation passed |
| Deterministic stage / TM `.14` | Integrated; 40 component/stage/upstream-digest/installed-qualification tests passed, 0 skipped, using the pinned official CRX; two actual four-script stages have 88 identical files; live import passed on the second start; actual installed gates and resumed work2 health passed |
| Private activation/resume gates | 17 activation + 5 resume + 18 parent-runner tests passed; read-only real preference proof covers 23 bookkeeping changes; one-shot resume and work2 healthy cutover passed |

The first fresh `.31` Linux Controller run retained 299 passes, 109 skips and four reported failures: two old leaf assertions (plus their parent) expected a validated send followed by failed cleanup to remain uncertain, and one short-timeout A/B test ran under concurrent Go CPU load. The test correction preserves a real unknown path by injecting result loss after the provider journal but before validation; it separately proves completed results survive cleanup failure, restart and subsequent journal loss, with one send effect, one release attempt and a cleanup diagnostic. The unchanged A/B file passed all 14 tests in isolation. The subsequent full Controller run without concurrent Go work passed. Original failure logs remain retained.

The Gateway artifact uses Go `1.25.5`, `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`, and `-trimpath`. Its 44,036,276 bytes and SHA-256 `92deb024f573b7d82088137f9b8a335c54e8bb76eba67ade1ee1ff90c88eceea` match the `.30` binary exactly. `final-source-equivalence.json` proves all Gateway build inputs, WebChat source and Controller production source are unchanged between the qualified `f219bbb6` and final `14d1c8ac`; later changes cover staging/configuration, documentation and a Controller test.

The final Mac package built from clean `14d1c8ac` on Node `26.2.0` for `darwin/arm64` at 10:35:40.355–10:36:06.089 UTC (25.734 seconds). Package allowlist and source/UI/helper parity passed. DMG SHA-256 is `9dfe6bb87d604476bfb8fa10de4349331f8b27d707d814aacd39bb670f24b6be`; ZIP SHA-256 is `7be4215dd9195d7e8990adbb8665bf3afe6cfac409f4ddada2173f99c042f927`. The package is not Developer ID signed or notarized. Installation completed at **10:37:04.921526 UTC / 18:37:04.921526 Beijing time**, with all 277 files/links matching and all six existing profiles byte-exact before launch; the data backup was verified. This installation result does not establish subsequent normal-launch, connection, original-UI or final runtime-preservation acceptance.

Private evidence is retained in `20261021-mail-r31/` (`final-go/`, `controller-linux/result.json`, `evidence/{server-build,final-source-equivalence}.json`, `evidence/{desktop-final,mac-build}.log`, `mac-upgrade/evidence/{build-source,package-parity,installation}.json`, `staging/{baseline-reproducer,r14-reproducer}.json`, `staging/component-tests-r14.log`, `paired-qualification.log`) and `.30` (`outlook-close/root-cause-and-fix.json`, `mac-upgrade/evidence/{six-mail-results,native-originals-four}.json`, `mac-upgrade/gmail-receipt/`). Earlier `.29` evidence supplies intake/address fault tests and first-import diagnosis. Do not publish account addresses, profile contents, credentials, original mail or raw receipt logs.

## `.31` rollout and remaining live acceptance

Automatic runtime-only rollback is disabled. Any reviewed recovery must keep runtime, Bridge and managed components matched and retain current business and execution ledgers. Preserve existing configuration, mailbox IDs, originals, drafts, receipts, login/profile data and the original UI. Historical unknown sends, including the older Outlook task, are not test sends and must never be resubmitted. Neither intake negative proof nor read-only QQ confirmation relaxes this boundary.

Final `.31` work2 cutover ran from **10:44:51.615242 to 10:52:03.839267 UTC / 18:44:51.615242 to 18:52:03.839267 Beijing time**, including the retained gate interruption and reviewed resume. Source `14d1c8ac`, AppCLI `.31`, Bridge `1.0.29` and Tampermonkey `.14` are paired. `evidence/server-cutover.json` verifies all **41 business tables, 2,051 workspace files and 70,935 original receipt files** were preserved. `activation-evidence/activation-resume.json` records no component reinstall or Browser restart during resume, exact historical unknown-row preservation and removal of the temporary runtime override. All 23 preference changes were reviewed as browser bookkeeping, window geometry or the current extension path; selected existing user preferences remained intact.

The first post-cutover snapshot at **10:52:28.721921 UTC** returned health HTTP 200 in **0.917 ms**, with no cleanup fence, all three watch tasks active, all 81 baseline unknown rows unchanged, no new unknown task and all three historical draft records exact. This is an initial observation, not a completed long-duration stability test. The first Mac connection check overlapped maintenance and showed unavailable after HTTP 503; the existing retry cleared it. The installed Mac ISCP path and original mail-check controls then returned HTTP 200, `ready` and `sync_health=healthy` for Gmail, Outlook and QQ from **10:53:03.252 to 10:53:37.558 UTC / 18:53:03.252 to 18:53:37.558 Beijing time**. The checks took 7.875 s, 11.236 s and 15.104 s respectively. Evidence is `mac-upgrade/evidence/final-mail-checks-recovered.private.json`. This proves connection/check behavior, not new-message capture or reply-send success. Final startup and preservation evidence appears below.

The prior `.30` work2 cutover completed at 09:56:38.137490 UTC and preserved 41 business tables, 1,680 workspace files, 70,560 prior receipts and all 80 historical unknown rows. Its Mac install completed at 09:51:34.153769 UTC, with 277 package entries and six profiles exact before launch. Gmail intake was enabled through the original UI at 09:57:04.096–09:57:41.365 UTC (HTTP 200; 37.269 seconds). These are prior-release facts, separate from the `.31` preservation evidence above.

```json
{
  "release": "0.3.0-sparkclaw.31",
  "acceptance_status": "partial_stopped_at_user_request",
  "final_sparkclaw_source": "14d1c8ac5065c883fb27ec199ce91141cc9cb544",
  "work2_cutover_utc": "2026-10-21T10:52:03.839267Z",
  "work2_cutover_preservation": {
    "business_tables": 41,
    "workspace_files": 2051,
    "original_receipt_files": 70935
  },
  "work2_initial_snapshot_utc": "2026-10-21T10:52:28.721921Z",
  "baseline_unknown_rows_unchanged_at_initial_snapshot": 81,
  "mac_install_utc": "2026-10-21T10:37:04.921526Z",
  "mac_profiles_exact_before_launch": 6,
  "mac_normal_launch_utc": "2026-10-21T11:01:49.966932Z",
  "mac_debug_ports_closed": true,
  "final_package_parity_evidence": "mac-upgrade/evidence/package-parity.json",
  "pre_and_post_business_preservation_evidence": "mac-upgrade/evidence/preservation.json",
  "historical_unknown_rows_unchanged": 81,
  "original_ui_live_verification_evidence": "mac-upgrade/evidence/final-mail-checks-recovered.private.json",
  "three_provider_health_evidence": "mac-upgrade/evidence/final-mail-checks-recovered.private.json",
  "post_31_outlook_reply_result": {
    "state": "failed",
    "error_code": "email_send_not_dispatched",
    "effect": 0,
    "attempt": 0,
    "dispatch": null,
    "sent": false,
    "retried": false,
    "elapsed_seconds": 48.069
  },
  "post_31_gmail_normal_inbox_capture": "not_accepted",
  "final_observation_start_utc": "2026-10-21T10:52:28.721921Z",
  "final_observation_end_utc": "2026-10-21T11:01:51.616175Z",
  "source_publication_status": "not_pushed_at_user_stop"
}
```

## Final closeout

Normal Mac startup completed at **11:01:49.966932 UTC / 19:01:49.966932 Beijing time**: one main process, no inspector argument, debug port closed. The original Workbench, three conversations and mail entry remain visible. Final preservation verifies 277 package entries, six profiles, 97 tables, 22 original messages, original conversations/drafts, stable identity configuration and launchers. Only expected event projections and three mutable transport journals changed. Earlier mail checks establish connection behavior; no new API check was performed after this final restart. Evidence: `mac-upgrade/evidence/{normal-launch,preservation}.json`.

Final work2 snapshot at **11:01:51.616175 UTC / 19:01:51.616175 Beijing time**: HTTP 200/ready in **1.268 ms**, no cleanup fence, all 81 baseline unknown rows unchanged, no new unknown task and three historical drafts exact. This bounded observation does not establish long-term stability. Temporary diagnostics stopped and private handles were released, retaining ten user pages and three resident pages. The separate pre-existing Mac browser-host reconnect/grant-recovery issue remains unresolved; successful mail checks used the work2 ISCP path.

Both installations remain in place. Product source and artifacts are retained locally; SparkClaw/AppCLI source pushes and formal publication remain unfinished. This is partial acceptance at the user's stop request.

## Six-direction live results on `.30`

Run `506172fb` is complete for provider delivery: all six messages were sent once and all six arrived at their providers. Four were automatically captured and parsed. Both Gmail-bound messages arrived in Spam and remain excluded by the existing Reader rule; this is **not six-direction autosync acceptance**. No spam policy, label, read state or checkpoint was changed to manufacture a pass. Outlook → QQ exercised the original compose/save/reopen/send UI; the other installed native ISCP actions are not described as UI clicks. The two Outlook unknown responses were reconciled under their original operations, with zero resend.

| Direction | Send start UTC | Definitive confirmation UTC / elapsed | Receive evidence UTC | Original / read proof |
| --- | --- | --- | --- | --- |
| QQ → Outlook | 09:58:40.713 | 10:00:09.453 / 88.740 s | Capture 10:00:41.030439; parse 10:00:41.055297 | Native original 11,178 bytes; hash matches |
| QQ → Gmail | 10:00:33.682 | 10:02:04.685 / 91.003 s | Provider Spam 10:01:52.008; exact native search; no auto-capture | Still unread; spam excluded |
| Outlook → QQ | 10:05:42.688 | Original-operation reconciliation 10:09:07.128 / 204.440 s | Capture 10:08:26.775885; parse 10:08:27.247516 | Native original 11,868 bytes; hash matches; QQ read confirmed |
| Outlook → Gmail | 10:11:27.994 | Original-operation reconciliation 10:22:13.024 / 645.030 s | Provider Spam 10:12:40.368; exact native search; no auto-capture | Still unread; spam excluded |
| Gmail → QQ | 10:15:08.287 | 10:16:33.190 / 84.903 s | Capture 10:16:50.351232; parse 10:16:50.716321 | Native original 6,989 bytes; hash matches; QQ read confirmed |
| Gmail → Outlook | 10:16:40.865 | 10:18:25.154 / 104.289 s | Capture 10:20:15.120204; parse 10:20:15.745634 | Native original 14,512 bytes; hash matches |

The four originals were downloaded through the installed client's native route at 10:23:51.842–10:23:55.595 UTC; all four hashes matched the stored manifest. For Outlook, the durable native sent journals predate cleanup failure: i2 at **10:07:04.451 UTC**, i3 at **10:12:39.804 UTC**. The 204.440 s and 645.030 s confirmation intervals include operator waiting and original-request reconciliation; they are not pure provider-send latency measurements. Native receipt, provider arrival, Gateway capture/parse and final foreground confirmation are distinct timestamps. The message Date header is not an exact arrival timestamp.

The Gmail searches used exact unique test markers on independent owned read-only pages, including Spam, and did not open or move the messages. The current Reader excludes Spam/Trash/Drafts and filters by received-time windows. Moving an old message after its timestamp has fallen behind the current overlap would not establish normal autosync; no watermark manipulation or scope expansion was performed. Provider-native modification hints or an explicit authorized rescan could be a future design, not part of this release.

## Stopped follow-up and unfinished work

The new Outlook reply draft was saved/reopened through the original UI, then sent through the original send control at **10:55:07.599–10:55:55.668 UTC / 18:55:07.599–18:55:55.668 Beijing time** (48.069 s). The request returned HTTP 200 with business state `failed` and `email_send_not_dispatched`. Its task has `effect=0`, `attempt=0`, `dispatch=null`, no native send journal and no cleanup fence. **No email was sent, and the operation was not retried.** The failed draft and original message remain; this is separate from historical unknown sends, which were not replayed.

A retained read-only owned-page diagnostic reached `verifySendAccount` and timed out during `inspect`/`eval` after **10.425 s**, before selecting a reply target or clicking a reply button. It released its private handles, retained user pages and left no fence. The diagnostic narrows the failing prerequisite but does not yet distinguish why the account inspection timed out. Separately, source review found that `openNativeReplyTarget` uses network collection without selecting/opening the actual Outlook target or supplying the older DOM proof required by the reply editor. That gap is unrepaired and is **not established as the cause of the earlier account-inspection timeout**. Evidence: `reply-diagnostic/owned-prerequisites-observation.private.json`, `reply-diagnostic/first.private.json`, and `mac-upgrade/evidence/result-reply-send-original.private.json`.

At the user’s stop request, the remaining work is:

- Resolve the Outlook sender-account inspection timeout and separately repair/qualify native reply-target selection. The reply path is not accepted; no more send attempts are planned in this task.
- Qualify a new Gmail message arriving in normal Inbox and automatically reaching Gateway capture/parse. The two old Spam deliveries remain correctly excluded; no spam-policy or checkpoint change was made. Six-direction provider delivery passed, while only four directions established automatic capture.
- Determine the cause of the repeated first native-import failure. A bounded second start passed in `.12` and `.14`; the cause remains unproved, despite the independent deterministic-staging fix.
- General Outlook attachment upload remains unqualified. Plain-text sends and original downloads do not establish attachment-send acceptance, and the historical unknown task must not be resent.
- Source pushes and formal publication remain unfinished. Mac normal startup and preservation are complete. No further fixes, live diagnostics or sends followed the stop request.
- The separate pre-existing Mac browser-host reconnect/grant-recovery issue remains unresolved; work2 ISCP mail checks passed.
