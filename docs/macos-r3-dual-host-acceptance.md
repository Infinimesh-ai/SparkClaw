# SparkClaw R3 Mac/Linux isolated LAN acceptance

> Language: English | [简体中文](../zh-cn/docs/macos-r3-dual-host-acceptance.md)

Updated: 2026-10-04, Asia/Shanghai; phase-close verification through 13:20. Physical checks below were executed on 2026-10-03–04. Branch: `codex/sparkclaw-r3`. This continues the [initial Mac record](macos-r3-acceptance.md). The user authorized this Mac and `infinimesh@192.168.20.252` for paired testing and limited the Mac to internal use. **This phase is closed; full M01–M12 acceptance remains incomplete. Follow-up actions and completion criteria are recorded below.**

## Environment and exact versions

| Component | Actual version / location |
|---|---|
| Delivery baseline | `d797d6193b3679125dfccd1c274dd990e5cd9c8f` |
| Final client/package source in this round | `86176211a7c51abb6ab8680f14e884fc06934430` |
| Running isolated Gateway build source | `ab8833dd9d4e418ef5a0d180d1afa8edb5f36e77`; later changes affect desktop code only, with identical Go product source |
| Gateway binary SHA-256 | `e879343ac14e1db4ccc71945f216e9ccc1bcfde7e4f4dfc0c375b56fb765e64b` |
| Mac | Apple M5 / ARM64, macOS 26.6.2 (25G83), internal Retina 2560×1664, scale factor 2; Node 26.2.0/npm 11.13.0 |
| Linux | `gx10-7660`, ARM64 Linux 6.17.0-1032-nvidia; Go 1.25.5/Node 26.2.0; native clients on private Xvfb with the user's GNOME Secret Service |
| Native runtimes on both hosts | Electron 44.4.3 / Chromium 152.0.7977.130; Electron Node 24.21.0 |
| Isolated Linux root | `/home/infinimesh/.cache/sparkclaw-r3-dual-FZMMvi` |
| Mac test profile | Repository `.cache/dual-host-acceptance/mac-profile` |
| Internal Mac installation | Repository `.cache/dual-host-acceptance/installed/SparkClaw.app`, actually mounted/copied from this round's DMG |
| HTTPS origin / deployment / owner | `https://192.168.20.252:25543` / `dual-host-r3-20261003-FZMMvi` / `owner` |
| Leaf certificate SHA-256 | `1a2e2668378f11dcf44f0027a15f8318d55604d69ceb85f6fe432cca2d4b32cd` |

SSH is used for inspection, source transfer and test launch. Client HTTPS/WSS traverses the LAN directly, without an SSH tunnel. A dedicated two-day test CA retains chain, hostname and leaf-pin validation. The isolated Gateway uses mock models with external MCP/integrations disabled; synthetic-mail intake is paused. The existing Linux Gateway was not repaired, restarted or reconfigured; the initial HTTP `18790/healthz` check returned 502.

Only GB10/Linux collects mail. The Mac synchronizes authorized backend mail and keeps its local cache. The existing dedicated browser's three mailbox Profiles were directly reused as authorized, without copying Profiles/mailbox credentials to Mac, importing historical mail or old test history, modifying production business data, merging main, or connecting to InfiniCenter/waiting for 0031.

## Executed physical-host evidence

- **Paired mail contracts:** independent clients/installations synchronized fresh synthetic mail and Chinese-name attachments through the real Gateway. Authoritative HTTP deletion rejects stale versions, retries the same command key without repeating deletion, and sends tombstones to both caches. Saved local attachment copies survive source deletion. An expired-epoch cursor resets and rebuilds the full cache; malformed cursors return 400 and are not mislabeled as recovery passes.
- **Independent local persistence:** fresh native processes restore each client's conversations/messages/request IDs/files. The Mac's “最终实机验收” message “休眠断网后保留” remains unsubmitted. Installation UUID is `0715c02e-fb84-4b14-a9e4-92b8fabdd446`; saved-file SHA-256 is `242a3103061c81ed1e1e1f9c380d9324980b8a3e4129dd829af3b3bdc0255803`. Its marker and conversation ID are absent from the Linux backend and Linux client's non-mail database. This does not include a macOS reboot yet.
- **Actual lid/network/quit:** the user's lid operation produced a system Clamshell Sleep record from 16:42:26 to 16:44:17, lasting 111 seconds. GUI/local message/file survived wake. Wi-Fi `en0` was actually off for 45.08 seconds; backend TCP was unreachable while local file hash/unsubmitted state remained valid, then LAN connectivity recovered. GUI Cmd+Q fully exited the process; application rebuild/relaunch retained local data. The first offline GUI mailbox action exposed the installation-binding defect. The complete GUI network recheck after repair is still pending.
- **Common adapter and ordinary product workflow:** an earlier disposable `25544` facade used the real Go Broker and both hosts' real WebContents for navigation, input, click/read, 20 A/B switches, same-view release/reacquire, late-event fencing, system-browser/popup rejection, and unknown-write fences following a real click whose response was lost. That facade is stopped. Subsequent ordinary workflows used the actual Gateway, navigating/reading `https://www.google.com` in each native Host; the mock answer was grounded in real page reads. Neither host used a copied Linux page or substituted backend page. After actual renderer crashes, explicit new tasks created new WebContents/page refs, preserved selection of B, and rejected old refs/cross-conversation display.
- **Actual lease expiry:** using the real Gateway/WSS and native pages, the fixtures suppressed two client heartbeats and delayed a started read, retaining real wall time. Mac closed the task page after 29.97 seconds and Linux after 30.05 seconds. Old references were invalid, and late results did not reopen pages. Each host submitted once. Unknown-write reconciliation is covered separately by the actual click case, not inferred from a read timeout.
- **ACK loss/restarts:** the fixture discarded an ACK response after the real Gateway accepted it. Actual isolated Gateway/native client restarts then reconciled one ACK with zero new execution submissions, the same digest, and exactly two local messages. Ordinary execution-context markers were absent from persistent backend state/workspace/traces/logs.
- **GUI/credentials:** the user entered an independent Mac credential, opened device settings, issued/revoked devices, and confirmed Copy/local paste after the clipboard repair. Revoked devices retaining timestamped metadata is expected. Real private TTY bootstrap/recovery was one-time; repeated completed retrieval did not redisplay tokens. Linux actually revoked the old client, authenticated a new one, and retained its installation UUID/old scoped data without cross-client migration. Native Mac Keychain/Linux `gnome_libsecret` process restoration, wrong pin/deployment/owner/token rejection and protected-event-stream abortion on logout were verified.
- **Recovered actual Mac product GUI:** on 2026-10-04 the user retrieved a replacement in a real terminal and entered it directly in the product. Settings matched the server's active replacement Client and showed the old Client revoked. GUI sync completed at 13:11:17 and expanded the real Gmail→QQ acceptance mail; its body hash matched Linux. After a complete Cmd+Q exit and relaunch of the actual installed product, Keychain unlocked automatically without token re-entry. The new scope's “凭据恢复后保留” message, file, unsubmitted request and mail cache remained visible in the GUI. The encrypted vault has mode 600 and the same hash before/after exit. This is application-process restart evidence; an actual macOS reboot has not occurred.
- **Tests/packages:** final desktop suites passed all 90 tests on each host; 50 related tests passed under Mac Electron Node/SQLite. Final Mac native credential checks across three processes and both native Host suites passed. ARM64 DMG/ZIP was rebuilt locally, afterPack allowlists and `hdiutil verify` passed. The actual installed application refused a private future-Schema-5 database with exit code 1, preserving database/saved-file byte hashes. Known automated tokens were absent from Git/ASAR/logs/backend content/binaries; the human GUI token was not decrypted for scanning.

Mac and Linux use the same product frontend: all three actual built asset SHA-256 hashes match. Main JS `index-BC08-68r.js` is `31738392ed82e657f277b9397fb73245224d211c303a904a1c47f9576bf0157c`. Asset parity does not certify unexecuted physical Linux GUI checks.

## Real mail and the existing collection environment

The user authorized exactly three fresh mails, one attempt per route, subject prefix `SCW-mail-R3-20261003-405d1b61`, no attachments, recipients limited to the three existing Profile accounts. No mail was resent.

| Route | Actual outcome |
|---|---|
| QQ → Outlook | `email_draft_verification_failed`; sending unconfirmed; recipient's bounded new-mail window yielded zero candidates; FAIL/no replay |
| Outlook → Gmail | `email_page_contract_changed`; sending unconfirmed; recipient's bounded new-mail window yielded zero candidates; FAIL/no replay |
| Gmail → QQ | Send confirmed; one qualified new QQ candidate; real original capture/product MIME parsing and Mac/Linux native sync passed; the recovered Mac product GUI also synchronized over actual HTTPS and expanded the body, without copied credentials/cache |

After the Mac completely exited, GB10 captured the unique new QQ message again: `cap_072ff1b4c907243de56194bdded252ea`, original hash `sha256:7eee34a1df4657c40ee06a497981329fc0f900445efe2372a545bca969a76e2d`. Product receipt/manifest/original validation was followed by isolated typed-store discovery/page-batch lease publication and the real MIME parser. Constructed text was not substituted for the original. Isolated mail ID is `d49178ad82a4f96227e580038f9c4a19814d64f292490c2b00725fd5446edaef`; both native caches and the recovered Mac GUI cache share body SHA-256 `9de3b3bc68a6eef8026b871befc5381b35290f46b4eec11a999e29a464111c83`. This proves one actual collection operation remained independent of Mac exit, not long-running periodic collection qualification.

The existing browser Controller runs `/home/infinimesh/.local/share/sparkclaw/qualification/20260930/pre-extraction-baseline/tools/browser-controller/src/main.mjs`, entry SHA-256 `e211904e52c1940d207eac32ab0dedc460ea0988c65d03eb0e5becb426ff9b16`, without the current R3 `AppCLIClientFactory`/release configuration. Site failures are attributed to that running version, not presented as complete pass/fail results for the current R3 App-CLI release. The existing service was not upgraded or restarted. Default discover is account bootstrap; zero candidates does not mean an empty mailbox. Recipient outcomes used subsequent explicit new-time-window discovery.

## Source repairs committed and pushed

| SHA | Defect and resulting behavior |
|---|---|
| `da851139a9879499be57949a02050d0f8b4ab131` | pinned HTTPS DELETE lacked UTF-8 byte framing and the real Gateway received an empty body; framing fixed and authoritative deletion verified |
| `f7074ba5116d2724a3dc0781b49e142185f8380b` | clipboard writes allowed only for the trusted workbench main frame; reads/other origins remain denied; user revalidation passed |
| `c8f81144c6b0a3144522ccd579a5deb1aac4a87e` | ordinary GUI mailbox refresh called removed `executionClient.register`; uses completed DesktopAuth installation binding; actual GUI catalog/sync recovered |
| `ab8833dd9d4e418ef5a0d180d1afa8edb5f36e77` | ScopedAdapter omitted legacy ToolHub result fields and real navigation stopped before read; result contracts and cross-layer workflow regression fixed |
| `0f24edc2610499ffa5b75ac24735f322b0076fdf` | local conversation ownership was lost on lease release; retained ownership constrains display/navigation/popups; actual paired crash recovery passed |
| `86176211a7c51abb6ab8680f14e884fc06934430` | temporary OS key-store decryption failure must not destroy ciphertext; access stays locked without protected requests, and a later normal process can recover |

An operator mistake is also recorded: a bare Electron fixture read the ordinary SparkClaw Profile under a different Keychain application identity. Old startup logic cleared the saved GUI login. No token was extracted or local history deleted; the final repair protects subsequent decryption failures. On 2026-10-04 the user confirmed the original token was lost. Old Mac GUI client `client_web_sigBFXKm2wzNAefA-Kz0wJCN` was first revoked through the isolated management socket at 13:04:19.696 (Asia/Shanghai). The user then performed real CLI recovery; the latest server revocation timestamp is 13:09:18.503. The recovery journal is `completed`; replacement `client_web_tZJiI4cjW_StCCX9A6SSD3j6` / `Mac GUI Recovered Acceptance 20261004` is active and matches product settings. Only the user retrieved the token in a real terminal and entered it in the GUI; the agent did not obtain or decrypt it.

Lost-token recovery, old-client revocation, replacement GUI unlocking, real-mail sync and automatic Keychain restoration after Cmd+Q are now complete. The original installation UUID, conversation, unsubmitted request and file hash remain intact. The replacement has a new local scope; original history remains in its original scope without cross-client migration. A completed recovery command will not redisplay the token. Any future loss requires a new recovery for the then-active Client, rather than repeating this old Client's command.

## Current M01–M12 status

`PASS (scoped)` certifies only the stated internal non-production cases. Unexecuted cases are not passes; user-scoped `N/A` does not certify formal distribution.

| Case | Evidence/status | Remaining |
|---|---|---|
| M01 | PASS (scoped): direct LAN HTTPS/WSS, independent identity, full certificate validation/rejection | Production certificates/deployment outside this round |
| M02 | PARTIAL: process restart/upgrade retains local content; recovered identity's message/file/unsubmitted request/cache also survive; no Linux copy | Actual macOS reboot; replacement identity baseline is `ready_for_os_reboot` |
| M03 | PARTIAL: synthetic paired delete/cursor recovery; real Gmail→QQ original, GB10 independent capture, product parser/paired native sync and Mac product GUI sync | Two QQ/Outlook routes failed; current R3 collection runtime not physically integrated |
| M04 | PARTIAL: common Broker navigation/input/read; ordinary Gateway paired public-site workflow | Full supported-site matrix/current R3 GB10 collection versus Mac Host roles |
| M05 | PASS (scoped): real managed Host rejects system browser/independent popup; no Profile copy/backend substitution | Authorized child pages currently unsupported; capability gaps are not passes |
| M06 | PARTIAL: A/B, same-view reacquire, ownership after release, actual renderer recovery/new ref, stale-ref rejection | Full authorized-child supported-site matrix |
| M07 | PARTIAL: actual lid/sleep, 45-second Wi-Fi loss/local retention, Cmd+Q, native revoke/logout, actual 30-second lease deadline and lost-write-response fencing | Repaired GUI network recovery and GUI self-revoke/logout combinations |
| M08 | PARTIAL: actual bounded 32 MiB HFS+ ENOSPC, ACK loss/backend-client restart without replay | Real 24-hour expiry running; filling the physical system disk/power cuts are not extra requirements of original M08 |
| M09 | PARTIAL: normal GUI, manual Chinese IME, internal 2× Retina, clipboard/native permission checks | Full supported-site matrix; external display N/A (no hardware); mic N/A (not enabled in this local workbench round) |
| M10 | PASS (internal scope): native ARM64 package/audit, actual DMG install, app upgrade retention, actual future-schema startup refusal | Developer ID/notarization/formal Gatekeeper distribution N/A (no certificate/internal testing only) |
| M11 | PARTIAL: normal GUI login, actual automatic Keychain app-restart unlocking after lost-token recovery, native bad-token/logout/revoke fencing/local retention | Product GUI self-revoke/logout matrix, actual macOS reboot restoration |
| M12 | PARTIAL: TTY one-time retrieval/lost-token recovery, old-client revocation, new Mac GUI unlocking, GUI issuance/Copy/revoke, app-restart/reconnect reuse, package-log scans | Actual macOS reboot reuse (jointly with M02/M11); production rollout outside this round |

## Real 24-hour checks and artifacts

One result per host is retained using real time; each has one execution POST and zero ACKs. Actual Gateway restarts retained request IDs/digests/deadlines. No clock advance or shorter TTL was used.

- Linux deadline: 2026-10-04 16:23:47.870 (Asia/Shanghai).
- Mac deadline: 2026-10-04 16:24:33.787 (Asia/Shanghai).
- Fixtures wait another 65 seconds before requiring `delivery_expired` and no readable result. Both results can be collected no earlier than 16:25:39. The actual check at 13:20 on 2026-10-04 found both in `waiting_real_24_hours`, last state `completed`, one execution POST and zero ACKs per host; this is not PASS.

Final artifacts were built from `86176211a7c51abb6ab8680f14e884fc06934430`; subsequent record commits do not change that build source.

| Local file under `apps/desktop/dist/` | SHA-256 |
|---|---|
| `SparkClaw-0.1.0-mac-arm64.dmg` | `5bcd1995dfa44b22d81d30c8039e4879e10f55585fe2c87532726828f03cf278` |
| `SparkClaw-0.1.0-mac-arm64.zip` | `c1c78688c25192ef7cd4a085033b2c2c1996f1c067d0410fbb1392a2b71d677b` |

Ignored private evidence lives in `.cache/dual-host-acceptance/`: build/native-test/network/crash/lease/real-mail logs, artifact manifests, fixture sources/hashes and original receipts. Temporary automated credentials remain in private mode-600 files, outside Git/packages. New metadata is in `mac-gui-recovery-20261004.json`, `mac-gui-real-mail-sync.json` and `mac-gui-recovered-restart.json`; none contains a token. `start-product.sh` starts the actual internal installation.

Fresh acceptance content was created after recovery: conversation “凭据恢复实机验收 2026-10-04”, message “凭据恢复后保留”, file “凭据恢复验收文件.txt” (69 bytes). File SHA-256 is `a591d627d1baf584985fcb148512ffcc28f77c4acc0d749207ef3ff9a4ca321f`; request `a8e84f1b-e080-4815-81b7-4c48e8d8a359` remains explicitly unsubmitted. New content is absent from the Linux backend and Linux client's non-mail database. `recovered-manual-state-expected.json` records expected values. At 13:15:13, `reboot-baseline.json` recorded the active replacement, original installation UUID, message/file/cache and encrypted-vault hashes, status `ready_for_os_reboot`, and boot epoch `1788266014`. Neither the OS reboot nor its verification script has been executed.

The `25544` facade is stopped. The isolated `25543` Gateway, two real 24-hour check processes and Mac test profile are retained. No startup service/production deployment was added. Test certificates expire after two days.

## Phase closure and follow-up work

This phase delivers the native ARM64 internal installation, pushed R3 source repairs and bilingual evidence record. The actual GUI remains unlocked, showing the recovered identity's fresh acceptance conversation and real-mail cache. At the user's request, this phase ends without initiating an OS reboot, expanding site testing or sending mail again. Collect existing evidence first, then complete the following gaps.

| Order/cases | Next action and prerequisites | Completion criteria |
|---|---|---|
| 1 / M08 | After 2026-10-04 16:25:39, read GB10's two `*-expiry-soak.json` files/logs; retain the existing Gateway/check processes until completion | Both show `phase=passed`, `passed=true`, `last_state=delivery_expired`, no readable original result, unchanged request/digest/deadline, one submission and zero ACKs; record actual times/results without resubmission or clock changes |
| 2 / M02, M11, M12 | User chooses a convenient window for an actual Mac reboot; the baseline is ready. Refresh it first if active identity or acceptance content changes | OS boot epoch changes; script verifies new/old scoped data, file hashes, unsubmitted requests, mail cache and encrypted vault; actual GUI unlocks from Keychain; no Linux non-mail copy |
| 3 / M07, M11 | Use a temporary independent identity for actual GUI network loss/recovery, logout/self-revocation; first extend the network verifier to cover the recovered scope | Local data/cache survive about 45 seconds offline, connection recovers; protected execution/browser/mail/scheduling channels stop on logout/revocation; late commands are fenced, unknown writes reconciled without automatic re-execution |
| 4 / M03, M04, M09 | Prepare an isolated, exact-SHA current R3 pinned App-CLI/collection runtime on GB10 and confirm supported sites; investigate QQ draft verification and Outlook page-contract failures first. The existing old Controller does not prove the current release | Current R3 GB10 collection role and Mac/Linux embedded Host roles have paired evidence; every supported site's GUI/permission checks are recorded; only GB10 collects mail. New mail sends require explicit authorization; do not replay this phase's three routes |
| 5 / M05, M06 | Authorized child pages are currently unsupported; if included in the next delivery, implement task/page ownership before site qualification | Child pages, A/B switching, popups and crash recreation preserve ownership; stale refs/late events/cross-conversation display are rejected; retain the capability gap until implemented |
| 6 / Environment | After the 24-hour results and required evidence are collected, inspect test processes/Clients/private paths before cleanup | Stop only the isolated root's test Gateway/processes and revoke confirmed unused test identities; retain evidence/needed local data; production/shared services are unaffected. Test certificates expire around 2026-10-05 15:19; check validity before further LAN acceptance |

External displays, Developer ID signing/notarization and formal Gatekeeper distribution remain N/A in this internal round; the local workbench has no enabled microphone. Future changes to hardware or release scope require new qualification. InfiniCenter/0031, main merges, historical-data migration and production operations remain outside scope.

### Read existing 24-hour results

Run this read-only command after the actual deadline above. If still waiting or failing, inspect private logs/live processes first; do not recreate requests, send ACKs or restart submission flows.

```sh
ssh infinimesh@192.168.20.252 'python3 -' <<'PY'
import json, pathlib
root = pathlib.Path('/home/infinimesh/.cache/sparkclaw-r3-dual-FZMMvi')
for side in ('mac', 'linux'):
    value = json.loads((root / f'{side}-expiry-soak.json').read_text())
    keys = ('side', 'request_id', 'phase', 'passed', 'last_state',
            'expires_at', 'finished_at', 'submission_posts', 'ack_posts')
    print(json.dumps({key: value.get(key) for key in keys}))
PY
```

### Verify after an actual OS reboot

After the user actually reboots/unlocks macOS, verify local data first, then launch the product and observe automatic GUI unlocking. The script requires a greater OS boot epoch than the baseline; app restart cannot replace it. This Electron Node command checks only databases/cache/ciphertext hashes, without invoking safe-storage decryption or DesktopAuth. The actual installed SparkClaw application must restore the ordinary GUI Profile's login.

```sh
cd /Users/dev/Documents/ChatGPT/sprakclaw-r3
env ELECTRON_RUN_AS_NODE=1 node_modules/electron/dist/Electron.app/Contents/MacOS/Electron \
  .cache/dual-host-acceptance/verify-post-reboot.mjs
zsh .cache/dual-host-acceptance/start-product.sh
```

Retain `macos-reboot-result.json`. Verify the GUI's “凭据恢复后保留” message, 69-byte file, unsubmitted request and real-mail cache; check Linux again for absence of the non-mail conversation/message/request markers. Do not submit the acceptance request. Update M02/M11/M12 according to actual evidence and record the documentation commit SHA.

### Retention and cleanup boundaries

Private `.cache` evidence/helpers exist only on Mac/GB10 and cannot be reconstructed from Git alone; a later session should first confirm the paths still exist. This phase retains the isolated environment for follow-up and adds no automation schedule/startup service. Before cleanup, confirm `gateway.pid` resolves via `/proc/<pid>/exe` to the isolated root's `gateway`; existing `stop.py` checks that path before stopping it. Do not revoke the Mac/Linux browser Clients or stop the Gateway before the 24-hour fixtures finish. Review credential names/IDs/purposes before cleanup; the recovered GUI identity remains in use for internal testing. Do not automatically revoke identities whose ownership is unconfirmed.
