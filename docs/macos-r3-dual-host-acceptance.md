# SparkClaw R3 Mac/Linux isolated LAN acceptance

> Language: English | [简体中文](../zh-cn/docs/macos-r3-dual-host-acceptance.md)

Date: 2026-10-03, Asia/Shanghai. Branch: `codex/sparkclaw-r3`. This continues the [initial Mac record](macos-r3-acceptance.md) after the user supplied `infinimesh@192.168.20.252` and authorized paired acceptance. **The scoped checks below passed; full M01–M12 release acceptance remains incomplete.**

## Environment and exact versions

| Component | Actual version / location |
|---|---|
| Delivery baseline | `d797d6193b3679125dfccd1c274dd990e5cd9c8f` |
| Gateway build source | `40986ababcafd390b4501686f670fec7395d4d04`; its Go product source is identical to the final client source revision |
| Final client/build source | `f7074ba5116d2724a3dc0781b49e142185f8380b` |
| HTTPS DELETE repair | `da851139a9879499be57949a02050d0f8b4ab131` |
| Workbench clipboard repair | `f7074ba5116d2724a3dc0781b49e142185f8380b` |
| Mac | ARM64 macOS 26.6.2 (25G83), Node 26.2.0/npm 11.13.0; packaged application in normal product mode |
| Linux | `gx10-7660`, ARM64 Linux 6.17.0-1032-nvidia; Go 1.25.5, Node 26.2.0; native Electron clients on private Xvfb with the logged-in user's GNOME Secret Service |
| Both native client runtimes | Electron 44.4.3, Chromium 152.0.7977.130 |
| Gateway binary SHA-256 | `e1d18fd6c33db271487879d4dd7f9cb88edaf8a5bf5b606d856d0b8a9419c9d2` |
| New Linux root | `/home/infinimesh/.cache/sparkclaw-r3-dual-FZMMvi` |
| New Mac profile | Repository `.cache/dual-host-acceptance/mac-profile` |
| HTTPS origin / deployment / owner | `https://192.168.20.252:25543` / `dual-host-r3-20261003-FZMMvi` / `owner` |
| Leaf certificate SHA-256 | `1a2e2668378f11dcf44f0027a15f8318d55604d69ceb85f6fe432cca2d4b32cd` |

SSH was used for inspection, source transfer and test launch. Client HTTPS/WSS traffic traversed the LAN directly, without an SSH tunnel. The fresh public v2 descriptor includes the dedicated public CA; certificate chain, hostname and leaf pin validation were retained. The certificate is a two-day test certificate, not a production trust configuration.

The existing repository on Linux was at the baseline, but its existing Gateway container was restarting/unhealthy and HTTP `18790/healthz` returned 502; its runtime lacked the new local-management credential file. No existing service was restarted, reconfigured or repaired. All new state, workspaces, artifacts, TLS keys and logs are beneath the dedicated root. The Gateway uses mock models, no external MCP/integrations, an unused private browser-controller path and synthetic mail whose provider intake is disabled. No existing mail, browser Profile, token, owner data or production database was imported. InfiniCenter/0031 and main were untouched.

Mailbox collection runs only on the GB10/Linux backend. The Mac client synchronizes authorized backend mail and retains its local cache; it does not run a collector or copy the backend mailbox credentials/Profile. Existing Linux/shared acceptance remains baseline evidence. The missing real-provider evidence below concerns this paired data path, not a requirement to repeat Linux collector qualification.

## Executed evidence

- **Native paired mail:** two independently issued clients and installation UUIDs authenticated to the real Gateway. The typed repository was seeded offline with two fresh synthetic messages, structured previews and verified Chinese-name attachments, using the existing discovery/capture/parse/classification/assignment contracts. Both native clients synchronized the same mailbox. Authoritative HTTP deletes rejected stale versions, replayed the same command key without repeating deletion, and propagated durable tombstones to both caches, including an empty final mailbox. A syntactically valid expired-epoch cursor produced the server reset path and both clients rebuilt their caches. Malformed cursors correctly return 400 and were not mislabeled as expired-cursor recovery.
- **Persistence and independence:** fresh Electron processes restored each client's own Chinese conversation, immutable request ID, local file and saved attachment. A stopped isolated Gateway left each complete mail cache/cursor and local input intact. Reconnection resumed using persisted identity. Synthetic Mac non-mail markers were absent from Linux backend/other-client storage; Linux markers were absent from Mac non-mail storage. Deleting backend mail did not remove saved local attachment copies. This was application/process restart, not a macOS reboot.
- **Shared LAN Broker:** a disposable test facade around the real Go `r3browser.Broker` listened at `25544` with separate Host identities. Both physical hosts used the same adapter with real embedded WebContents to navigate, fill, click and read, switch A/B 20 times, release/reacquire the same view, reject late A presentation over B, block system-browser commands and Host popups, and fence a lost response after a real click as an unknown write on both sides. The facade is a qualification endpoint, not a production Gateway API. Its listener was stopped after the checks. Mac Host/GB10 backend-acquisition role isolation in the deployed product and real supported sites were not tested in this paired round.
- **Real delivery ACK loss and restart:** each native client explicitly submitted a fresh ordinary request to the real Gateway's mock-model workflow. The fixture discarded the first ACK response *after the real Gateway accepted the ACK*. The local durable receipt remained unacknowledged. After an actual isolated Gateway restart and fresh client process, reconciliation issued one ACK, zero execution submissions, retained the same digest and exactly two local messages, and marked the receipt acknowledged. Execution context markers were absent from persistent backend state/workspace/traces/logs. This does not replace expiry/power-loss qualification.
- **Credential bootstrap/recovery:** the actual `credentials.mjs` CLI ran on Linux in real private TTYs against the private management socket. Initial/recovery retrieval displayed a credential once; repeated completed retrieval did not redisplay it. Journals contain only metadata. The Mac and Linux clients used independent credentials, not the provisioned Linux desktop token. Real Linux recovery revoked the old client (subsequent authentication returned invalid/401), issued a new client, retained the installation UUID and old local data, and authenticated/synchronized using the replacement. Old data remains in its original client scope; no cross-client data migration was performed.
- **Mac product GUI:** the user completed terminal recovery and entered the one-time credential directly in the packaged application. Settings → Devices & credentials was reachable; GUI issuance and revocation succeeded. Revoked entries remain visibly marked `revoked` with a timestamp as device records. The user initially reported Copy failure. After the clipboard repair/rebuild, the packaged application restarted and unlocked from its own Keychain vault without token entry; the user confirmed Copy/paste revalidation succeeded. The revalidation device was actually named `My device`, and backend metadata confirmed it revoked; the suggested test name was not substituted for this observation. No one-time GUI token was captured in screenshots/chat.
- **Secure storage/TLS:** Mac Keychain and Linux `gnome_libsecret` encrypted vaults survived native process restart; `basic_text` was not accepted. Wrong pin, deployment, owner and invalid credentials were rejected. Logout aborted a real protected event stream while preserving local data. Real recovery/revocation clears the old vault. GUI self-revocation and the complete sleep/lease/exit matrix remain outstanding.
- **Verification:** all 88 desktop tests passed on both hosts. The 49 store/execution/approval/schedule/mail tests also passed under Mac Electron's embedded Node/SQLite. Native OS clipboard write tests passed on Mac and Linux; clipboard reads remained denied. Final ARM64 packaging, afterPack allowlist audit and `hdiutil verify` passed. Known automated test tokens were absent from tracked files, actual ASAR, logs, backend content and binaries. Package allowlists exclude private profiles, all backend credentials and other-device state; the human GUI credential was not decrypted for inspection.

## Source repairs found by paired acceptance

The pinned HTTPS implementation wrote a DELETE body without framing it. Node did not automatically send its JSON length for that method, so the Gateway received an invalid empty request (400). The repair computes the encoded byte length, overrides conflicting Content-Length, and removes conflicting Transfer-Encoding before writing the body. A real HTTPS regression check includes Chinese UTF-8 JSON. The repaired LAN request exercised authoritative deletion/CAS/replay on the real Gateway.

The workbench session's permission handlers allowed only audio media and rejected clipboard writes. They now allow `clipboard-sanitized-write` only for the trusted workbench's main frame. Clipboard reading, sibling WebContents, subframes and other origins remain denied; audio/video rules are preserved. Security-boundary unit checks, actual Electron/OS clipboard checks on both hosts and the user's packaged-Mac Copy revalidation passed. Both repairs were committed and pushed to R3.

## Updated M01–M12 scope

`PASS (scoped)` applies only to this fresh non-production LAN/test-data workflow. `PARTIAL` does not certify the full case. The initial record's NOT_RUN entries describe the earlier local-only round, not this follow-up.

| Case | Current evidence/status | Still not executed |
|---|---|---|
| M01 | PASS (scoped): real LAN HTTPS, independent credentials/installations, identity/pin rejection | Formal production certificate/distribution deployment |
| M02 | PARTIAL: two physical native clients, process restart and non-mail independence; local copies survive mail deletion | macOS OS reboot |
| M03 | PASS (scoped): paired typed-mail sync, authoritative deletion/tombstones, offline cache and expired-cursor recovery | Real GB10-collected mail → backend → Mac synchronization, including Mac exit while GB10 collection continues; collector implementation itself uses the existing Linux acceptance |
| M04 | PARTIAL: shared Go Broker controls real Mac/Linux embedded pages over LAN | Mac Host/GB10 backend-acquisition role isolation in the deployed product and product workflow/site integration |
| M05 | PARTIAL: both Hosts reject system-browser commands and independent popups | Full supported-site/Profile/substitution matrix |
| M06 | PARTIAL: both Hosts preserve A/B ownership and same-view reacquire | Authorized child-page and page-crash/restart matrix |
| M07 | PARTIAL: real logout/revocation controls, Gateway outage; actual write-response-loss unknown fences | Physical network loss, lid/sleep, all GUI quit/lease-expiry combinations |
| M08 | PARTIAL: real ACK response loss + actual backend/client restart without resubmission; earlier real ENOSPC check | 24-hour result expiry, physical disk/power-loss cases |
| M09 | PARTIAL: normal unlocked Mac GUI/settings, earlier manual Chinese IME and successful OS clipboard | External/multiple displays, full Retina/permissions, enabled microphone, supported sites |
| M10 | PARTIAL: rebuilt audited ARM64 development DMG/ZIP; native schema safeguards | Developer ID/notarization/Gatekeeper acceptance, formal install/upgrade/rollback |
| M11 | PARTIAL: real LAN Mac GUI login/Keychain restart; real native bad-token/logout/revoke checks | GUI self-revocation/logout and complete protected-channel matrix |
| M12 | PASS (scoped): real TTY bootstrap/recovery, independent identities, GUI one-time issuance/Copy/revoke, reuse and package/log checks | Formal production rollout is outside this session |

## Final artifacts and retained environment

Artifacts were rebuilt from `f7074ba5116d2724a3dc0781b49e142185f8380b`; later documentation commits do not change their source. They replace the same-named local packages listed in the initial record. Signing/notarization remains unexecuted; the development package is not a formal distribution pass.

| Local file under `apps/desktop/dist/` | SHA-256 |
|---|---|
| `SparkClaw-0.1.0-mac-arm64.dmg` | `87fd3e2b899f4d4bebd90719e988f1cfe0aba06501b4cb9d0e36ec19b36f1c94` |
| `SparkClaw-0.1.0-mac-arm64.zip` | `15b33a20cf8d7be787d861aba12b5ac9485ddabbeb30fef233d890dd09d724b7` |

Ignored local evidence: `.cache/dual-host-acceptance/` contains phase JSONL files, device metadata without tokens, Mac/Linux test/build/DMG logs, `artifact-manifest.json`, private fixture sources and their `fixture-manifest.json` hashes. Fixtures may hold temporary synthetic credentials in mode-600 files beneath private test roots; these are neither deploy logs nor Git/package content. SSH-side mail-seeding and Browser test facades are test-only additions, not source repairs or shipped endpoints.

The `25544` Broker facade is stopped. The isolated `25543` Gateway and Mac test profile remain available for follow-up; there is no startup service or production deployment. The test certificate expires after two days. Existing Linux production services/data were not modified. Full acceptance should continue from the explicitly outstanding cases above, using new authorized test data.
