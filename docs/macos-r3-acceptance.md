# SparkClaw R3 native Mac build and isolated acceptance record

> Language: English | [简体中文](../zh-cn/docs/macos-r3-acceptance.md)

Date: 2026-10-03, Asia/Shanghai. Branch: `codex/sparkclaw-r3`.

The native ARM64 development package, launch checks and isolated checks below passed. **Full M01–M12 acceptance has not passed.** The user confirmed that a non-production Linux R3 backend and Linux client are currently unavailable and requested Mac builds and local isolated qualification first. This session did not connect InfiniCenter, wait for 0031, merge main, migrate old test data or operate production data.

## Versions and environment

| Item | Observed value |
|---|---|
| Linux/shared delivery baseline | `d797d6193b3679125dfccd1c274dd990e5cd9c8f` |
| Qualification fixes and final artifact source | `57616e26dac0449ee12b67aa848eb82bacc59590` |
| Machine | macOS 26.6.2, Build 25G83, ARM64 |
| Build tools | Node 26.2.0 `darwin/arm64`, npm 11.13.0, electron-builder 26.15.3 |
| Application runtime | Electron 44.4.3, Chromium 152.0.7977.130, embedded Node 24.21.0, ARM64 |
| Native tools | `/Library/Developer/CommandLineTools`; Go 1.25.12 `darwin/arm64` compiled only a disposable Broker fixture |
| Node toolchain | `/Users/dev/.cache/sparkclaw-r3/toolchains/node-v26.2.0-darwin-arm64/bin`; official archive SHA-256 verified; global Node 22 was not replaced |
| Product qualification profile | `/Users/dev/Documents/ChatGPT/sprakclaw-r3/.cache/mac-acceptance/product-profile`, newly created and dedicated |
| Local evidence | Repository `.cache/mac-acceptance/`, ignored by Git; no real token or mail body |

The final artifacts were built from `57616e26…`; subsequent documentation commits do not change client source. The original Linux implementation record is baseline evidence, not a substitute for this record.

## Executed builds and checks

Commands used the independent Node toolchain on PATH. Dependencies were installed with `npm ci` and the lock file. Commands:

```sh
npm --workspace @sparkclaw/desktop run dist:mac-arm64
npm run test:desktop
npm run qualify:desktop-r3
npm run qualify:desktop-mac-client
```

| Check | Actual result and scope |
|---|---|
| Baseline and fixed-source ARM64 packaging | PASS; UI build, managed-asset check, afterPack allowlist audit and DMG/ZIP generation succeeded |
| Package contents/architecture | PASS; Mach-O ARM64 executable, actual ASAR with 60 entries and 5 UI files; no Gateway, server data/credentials or unapproved dependency |
| Final DMG | PASS; `hdiutil verify` succeeded |
| Packaged application launch | PASS; normal product entry, without `--qualification`, reached the credential gate both initially and after rebuilding; fresh empty schema 4 store and installation identity preserved across application restart |
| Node 26 desktop suite | PASS, 87 tests executed locally; does not establish full real-environment acceptance |
| Actual Electron Node/SQLite | PASS, 49 store/capability/execution/approval/schedule/mail tests under Electron's embedded Node |
| Mac native Host | PASS (isolated); actual TLS/WSS Broker and WebContents navigation/click/fill/read; two views, 20 A/B switches, same-view release/reacquire, late A results cannot select B; system-browser command rejected, Host popup blocked; lost response after actual write produces unknown fences on both sides without replay |
| Mac secure storage/local persistence | PASS (isolated); three fresh Electron processes, actual safeStorage/Keychain encryption and restart decryption; bad credential rejection, installation binding, logout aborts stream, synthetic 401 revocation clears credentials; Chinese conversation/request ID/file retained, awaiting_runtime not automatically submitted; SQLite WAL/FULL and schema 4; fixture backend receives only identity/installation control, no business content |
| Mac filesystem capacity exhaustion | PASS (isolated); a 32 MiB HFS+ image filled to actual ENOSPC; atomic file write and delivery transaction failed without receipt; after releasing space and reopening, input remained, no orphan files, SQLite integrity_check ok; image detached. Does not establish physical host-disk exhaustion or power-loss behavior |
| Chinese GUI/manual input | Chinese UI and Chinese/emoji paste rendered; user completed system Chinese input of “星爪验收”, then AX confirmed that text. This manual step used the baseline development package; final source changes only qualification tools/npm entries, not product src, and the final package received a separate launch check. External displays were not inferred to pass |

Electron Node command:

```sh
ELECTRON_RUN_AS_NODE=1 node_modules/electron/dist/Electron.app/Contents/MacOS/Electron --test \
  apps/desktop/test/client-store.test.mjs apps/desktop/test/client-store-capability.test.mjs \
  apps/desktop/test/execution-client.test.mjs apps/desktop/test/execution-approval.test.mjs \
  apps/desktop/test/schedule-client.test.mjs apps/desktop/test/mail-sync.test.mjs
```

The product used `SPARKCLAW_DESKTOP_USER_DATA_DIR` for the dedicated profile above, retained for follow-up. Automated fixtures created only fresh synthetic data and removed their temporary profiles, certificates and Broker control directories. Disk-capacity fixtures, logs and build artifacts are excluded from Git.

## Initial failures, repairs and open items

Baseline `qualify:desktop-r3` initially failed with `browser control path is unsafe`, because macOS's default temporary path crosses the `/var` symlink. The runner also hardcoded Linux Xvfb and an Electron executable path. The repair canonicalizes the temporary parent, selects Linux Xvfb or the logged-in Mac desktop, and resolves Electron's executable from its package. Mac keeps native sandbox/GPU behavior rather than Linux launch flags. Product path-security checks were not relaxed.

The baseline desktop suite encountered the same path issue: 73 passed and 13 failed. An authentication test left a stream open after failure; ending that fixture child yielded only 86 reported tests, so the initial run is not a complete pass. Three fixtures that require canonical private paths now use the real temporary parent; all 87 tests subsequently passed. The added Mac secure-storage fixture and stronger native popup/system-browser rejection checks also passed.

Packaging retains `mac.identity=null`. `security find-identity -v -p codesigning` found zero valid identities; the app carries only Electron's inherited ad-hoc/linker signature, without TeamIdentifier or sealed resources. `spctl --assess --type execute` **rejected** the development package with `code has no resources but signature indicates they must be present`. Signing/notarization was not performed; Gatekeeper was not bypassed. Formal distribution acceptance is not passed.

Default Electron icon and UI chunk >500 KiB warnings did not block packaging. `npm ci` reported the existing eight high developer-toolchain vulnerabilities; no dependency upgrade or remediation claim was made. Other CPU architectures, formal installer/upgrade flows, physical power loss and a real 24-hour expiry soak were not executed.

## Actual M01–M12 matrix

`PARTIAL` means only scoped local evidence is available; mandatory evidence for the full case is still missing. `NOT_RUN` is not a pass.

| Case | Status | Obtained evidence | Not executed/still required |
|---|---|---|---|
| M01 | PARTIAL | Mac loopback HTTPS/WSS pin and identity rejection checks | Linux LAN HTTPS, real deployment/client binding and end-to-end connectivity without SSH |
| M02 | PARTIAL | Native process restart retains Chinese conversation/task/file; logout/synthetic revocation retains data; product restart preserves installation ID | macOS reboot, two physical clients' non-mail independence and no copies at the real Linux backend |
| M03 | NOT_RUN | No real paired-client mailbox evidence this session; synthetic mail unit checks are among the 49 tests | Actual paired mail synchronization/deletion/cursor recovery and continuous collection; unit checks do not replace this |
| M04 | PARTIAL | Actual Broker/Host adapter operates real Mac embedded views | Linux embedded client, separate backend acquisition role and deployed pairing |
| M05 | PARTIAL | Mac Host rejects system-browser command, blocks separate popup and runs actual embedded pages | Supported site flows, Profile isolation and full physical observation of no backend substitution |
| M06 | PARTIAL | A/B, 20 switches, same-view reacquire, late-result isolation and conservative Host popup rejection | Authorized child pages, real-site popup compatibility and page crash/restart/generation matrix |
| M07 | PARTIAL | Native lost-write-response followed by suspend/unknown fence; logout abort and synthetic 401 revoke | Real network loss, lid/sleep, GUI quit, lease expiry and actual device-revocation matrix |
| M08 | PARTIAL | Native Electron delivery/ACK/fault tests; actual ENOSPC on isolated volume without receipt, integrity after recovery | Actual backend restart, end-to-end ACK loss, 24-hour expiry, physical power loss/host disk exhaustion |
| M09 | PARTIAL | Packaged GUI, Chinese rendering/paste, manual system Chinese input and screenshot on current display | Unlocked workbench, external/multiple displays, full Retina checks, permissions, enabled microphone and supported sites |
| M10 | PARTIAL | ARM64 DMG/ZIP, audit, native SQLite schema upgrade and fail-closed newer-schema/data-retention checks | Developer ID signing/notarization and Gatekeeper acceptance, real installation/upgrade/rollback; Intel not executed |
| M11 | PARTIAL | Fresh product credential gate; real Mac safeStorage across processes, bad credentials blocked, logout/synthetic revoke preserves synthetic history | Real LAN product login, Keychain recovery and real device-revocation/protected-channel matrix |
| M12 | NOT_RUN | No real backend issuance/device-management evidence; actual package audit passed | Backend bootstrap/settings issuance, one-time display/copy, device reuse, lost-device recovery/revocation and log checks |

## Follow-up manual procedure

1. Prepare a separate non-production Linux R3 backend/client and record exact SHAs. Use a public v2 HTTPS description and separately issued Mac credential; enter tokens only in the application, following the [connection guide](macos-connection-guide.md#51-where-credentials-come-from).
2. Use the retained dedicated Mac profile for login, independent Host authorization and M01/M11/M12, then paired non-mail isolation and mail synchronization. Create new data only; do not import old test history.
3. Maintain an active lease on a harmless fixture page and separately exercise network loss, sleep/lid, quit/restart, revocation and lost-write-response behavior; explicitly reconcile unknown results. Without an actual lease, do not claim full M07.
4. Record display, permission, microphone (only when required) and supported-site results. Formal distribution needs applicable user-provided Developer ID/notarization configuration, followed by separate signing and upgrade/rollback qualification. Bypassing Gatekeeper is not a pass.

## Local artifacts

Directory: `apps/desktop/dist/`. Final build SHA: `57616e26dac0449ee12b67aa848eb82bacc59590`. Artifacts are local only, not published to production or Git.

| File | SHA-256 |
|---|---|
| `SparkClaw-0.1.0-mac-arm64.dmg` | `2e24930cf0a9b3fed8758cf3d8b4fa9fc898223224442f38178da9e134bd454d` |
| `SparkClaw-0.1.0-mac-arm64.zip` | `35cebe96054a003a3749f92e3dd4126b614280ab5913d1642fc2c2aff48a5528` |

Evidence files in the local evidence directory: `build-arm64-baseline.log`, `build-arm64-57616e26.log`, `desktop-node-tests.log`, `desktop-node-tests-fixed.log`, `electron-node-tests.log`, `r3-native-baseline.log`, `r3-native-final.log`, `mac-client-native.log`, `fs-full.log`, `dmg-final-verify.log`, `packaged-first-launch.log`, `packaged-launch-57616e26.log`, `packaged-profile-first.json`, and `packaged-profile-restarted.json`. GUI AX/screenshots and manual input feedback remain in this Mac session's tool history.
