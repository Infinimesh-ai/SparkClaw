# SparkClaw R3 Mac/Linux isolated LAN acceptance

> Language: English | [简体中文](../zh-cn/docs/macos-r3-dual-host-acceptance.md)

Updated: 2026-10-03, Asia/Shanghai. Branch: `codex/sparkclaw-r3`. This continues the [initial Mac record](macos-r3-acceptance.md). The user authorized this Mac and `infinimesh@192.168.20.252` for paired testing and limited the Mac to internal use. **Executed evidence is listed below; full M01–M12 acceptance remains incomplete.**

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
- **Tests/packages:** final desktop suites passed all 90 tests on each host; 50 related tests passed under Mac Electron Node/SQLite. Final Mac native credential checks across three processes and both native Host suites passed. ARM64 DMG/ZIP was rebuilt locally, afterPack allowlists and `hdiutil verify` passed. The actual installed application refused a private future-Schema-5 database with exit code 1, preserving database/saved-file byte hashes. Known automated tokens were absent from Git/ASAR/logs/backend content/binaries; the human GUI token was not decrypted for scanning.

Mac and Linux use the same product frontend: all three actual built asset SHA-256 hashes match. Main JS `index-BC08-68r.js` is `31738392ed82e657f277b9397fb73245224d211c303a904a1c47f9576bf0157c`. Asset parity does not certify unexecuted physical Linux GUI checks.

## Real mail and the existing collection environment

The user authorized exactly three fresh mails, one attempt per route, subject prefix `SCW-mail-R3-20261003-405d1b61`, no attachments, recipients limited to the three existing Profile accounts. No mail was resent.

| Route | Actual outcome |
|---|---|
| QQ → Outlook | `email_draft_verification_failed`; sending unconfirmed; recipient's bounded new-mail window yielded zero candidates; FAIL/no replay |
| Outlook → Gmail | `email_page_contract_changed`; sending unconfirmed; recipient's bounded new-mail window yielded zero candidates; FAIL/no replay |
| Gmail → QQ | Send confirmed; one qualified new QQ candidate; real original capture/product MIME parsing and Mac/Linux native sync passed (Mac uses its independent native qualification Profile, without copying credentials/cache into the GUI Profile); Mac product GUI sync awaits restored login |

After the Mac completely exited, GB10 captured the unique new QQ message again: `cap_072ff1b4c907243de56194bdded252ea`, original hash `sha256:7eee34a1df4657c40ee06a497981329fc0f900445efe2372a545bca969a76e2d`. Product receipt/manifest/original validation was followed by isolated typed-store discovery/page-batch lease publication and the real MIME parser. Constructed text was not substituted for the original. Isolated mail ID is `d49178ad82a4f96227e580038f9c4a19814d64f292490c2b00725fd5446edaef`; Both native caches share body SHA-256 is `9de3b3bc68a6eef8026b871befc5381b35290f46b4eec11a999e29a464111c83`. This proves one actual collection operation remained independent of Mac exit, not long-running periodic collection qualification.

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

An operator mistake is also recorded: a bare Electron fixture read the ordinary SparkClaw Profile under a different Keychain application identity. Old startup logic cleared the saved GUI login. No token was extracted or local history deleted; the final repair protects subsequent decryption failures. The user must re-enter the original token in the actual product GUI. Login restoration has not been marked passed.

## Current M01–M12 status

`PASS (scoped)` certifies only the stated internal non-production cases. Unexecuted cases are not passes; user-scoped `N/A` does not certify formal distribution.

| Case | Evidence/status | Remaining |
|---|---|---|
| M01 | PASS (scoped): direct LAN HTTPS/WSS, independent identity, full certificate validation/rejection | Production certificates/deployment outside this round |
| M02 | PARTIAL: process restart/upgrade retains local content; no Linux copy | Actual macOS reboot; verification script prepared |
| M03 | PARTIAL: synthetic paired delete/cursor recovery; real Gmail→QQ original, GB10 independent capture, product parser/paired native sync | Mac GUI real-mail sync after original login restoration; two QQ/Outlook routes failed; current R3 collection runtime not physically integrated |
| M04 | PARTIAL: common Broker navigation/input/read; ordinary Gateway paired public-site workflow | Full supported-site matrix/current R3 GB10 collection versus Mac Host roles |
| M05 | PASS (scoped): real managed Host rejects system browser/independent popup; no Profile copy/backend substitution | Authorized child pages currently unsupported; capability gaps are not passes |
| M06 | PARTIAL: A/B, same-view reacquire, ownership after release, actual renderer recovery/new ref, stale-ref rejection | Full authorized-child supported-site matrix |
| M07 | PARTIAL: actual lid/sleep, 45-second Wi-Fi loss/local retention, Cmd+Q, native revoke/logout, actual 30-second lease deadline and lost-write-response fencing | Repaired GUI network recovery and GUI self-revoke/logout combinations |
| M08 | PARTIAL: actual bounded 32 MiB HFS+ ENOSPC, ACK loss/backend-client restart without replay | Real 24-hour expiry running; filling the physical system disk/power cuts are not extra requirements of original M08 |
| M09 | PARTIAL: normal GUI, manual Chinese IME, internal 2× Retina, clipboard/native permission checks | Full supported-site matrix; external display N/A (no hardware); mic N/A (not enabled in this local workbench round) |
| M10 | PASS (internal scope): native ARM64 package/audit, actual DMG install, app upgrade retention, actual future-schema startup refusal | Developer ID/notarization/formal Gatekeeper distribution N/A (no certificate/internal testing only) |
| M11 | PARTIAL: normal GUI login/earlier Keychain restoration, native bad-token/logout/revoke fencing/local retention | Restoration after operator error, product GUI self-revoke/logout matrix, macOS reboot restoration |
| M12 | PARTIAL: TTY one-time bootstrap/recovery, independent Mac identity, GUI issuance/Copy/revoke, reconnect reuse/package-log scans | GUI restoration after decryption mistake; production rollout outside this round |

## Real 24-hour checks and artifacts

One result per host is retained using real time; each has one execution POST and zero ACKs. Actual Gateway restarts retained request IDs/digests/deadlines. No clock advance or shorter TTL was used.

- Linux deadline: 2026-10-04 16:23:47.870 (Asia/Shanghai).
- Mac deadline: 2026-10-04 16:24:33.787 (Asia/Shanghai).
- Fixtures wait another 65 seconds before requiring `delivery_expired` and no readable result. Both results can be collected no earlier than 16:25:39. Current phase is `waiting_real_24_hours`, not PASS.

Final artifacts were built from `86176211a7c51abb6ab8680f14e884fc06934430`; subsequent record commits do not change that build source.

| Local file under `apps/desktop/dist/` | SHA-256 |
|---|---|
| `SparkClaw-0.1.0-mac-arm64.dmg` | `5bcd1995dfa44b22d81d30c8039e4879e10f55585fe2c87532726828f03cf278` |
| `SparkClaw-0.1.0-mac-arm64.zip` | `c1c78688c25192ef7cd4a085033b2c2c1996f1c067d0410fbb1392a2b71d677b` |

Ignored private evidence lives in `.cache/dual-host-acceptance/`: build/native-test/network/crash/lease/real-mail logs, artifact manifests, fixture sources/hashes and original receipts. Temporary automated credentials remain in private mode-600 files, outside Git/packages. `start-product.sh` starts the actual internal installation. After restoring original GUI login and Mac mail sync, refresh `reboot-baseline.json`, actually reboot macOS, then run `verify-post-reboot.mjs` under Electron Node. It requires a changed OS boot epoch and checks local message/file/unsubmitted request/mail cache/encrypted vault; app restart cannot substitute for M02. Actual GUI Keychain unlocking and absence of Linux copies must also be observed.

The `25544` facade is stopped. The isolated `25543` Gateway, two real 24-hour check processes and Mac test profile are retained. No startup service/production deployment was added. Test certificates expire after two days.
