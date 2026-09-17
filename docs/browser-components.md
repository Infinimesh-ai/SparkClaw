# Managed browser components

[简体中文](../zh-cn/docs/browser-components.md)

Tampermonkey and the userscripts listed in `configs/browser-components.json` are maintained SparkClaw product components. AI conversation export uses the SparkClaw RevivalStack fork, including its [timeline batch export](ai-conversation-batch-export.md). The fork exposes only a hidden fixed-command automation bridge and does not add export buttons, outlines, or batch controls to provider pages. Every Local/Remote installation uses the same component manifest and shared setup; no user must manually install these assets or add extension paths. Account logins remain in each owner's dedicated Chromium profile.

## Release ownership

`configs/browser-components.json` pins Tampermonkey upstream/product versions, the official CRX checksum, exact userscript source bytes, and bundled dependencies. `tools/browser-userscripts/` retains source and license notices. The official proprietary extension is downloaded at installation, not copied into this repository. A changed official download fails checksum verification; refresh the reviewed pin instead of accepting unverified latest bytes. Verified downloads are cached under the owner's XDG cache.

The product extension has the stable path `/opt/sparkclaw/tampermonkey` and path-derived ID `dlgaaenljmglaedeiniphopajnkeakkf`. Updates retain this identity. The original owner's earlier unpacked extension and database are preserved for rollback; its private path is not used for other deployments. Deployment backs up profile preferences and the legacy manager database. The product database starts with native system imports; copying ordinary scripts into it would cause duplicate injection. Legacy manager data remains at its original identity.

## Native managed provisioning

The installer supplies a narrowly scoped Chromium third-party policy for this product extension ID under `/etc/chromium/policies/managed/sparkclaw-userscripts.json`. Its `jsonImport` references an embedded public script bundle with Tampermonkey's versioned structural digest. There is no provisioning HTTP service and no file-access grant. Each explicit deployment creates a new import generation; ordinary restarts reuse it. Each script has a stable UUID in the product manifest. A narrowly matched provisioning patch uses that UUID instead of generating another copy, so repeated deployments update the same script and retain its storage. The product reconciliation path also allows reinstalling that same managed UUID at the same source version; upstream normally rejects this case. This exception is limited to the explicit managed provisioning path. Tampermonkey installs/replaces scripts as enabled system scripts and keeps unrelated profile data. Per-script automatic updates are disabled so all deployments use the product's exact release. Required JavaScript libraries are imported from pinned bundled bytes.

Chromium 148 can defer managed-storage callbacks until policy initialization (observed around five seconds). Upstream Tampermonkey 5.5.0 gives up after one second. The product applies an exact-match compatibility patch extending the bounded wait to twenty seconds and waiting for the current deployment hash instead of accepting an empty object or an older cached policy. An unmatched patch aborts the build. The patched service worker uses a filename bound to the product version and deployment hash to invalidate Chromium caches on upgrade.  Its product version is distinct from upstream; full installed file hashes are recorded. This patch is source-controlled deployment logic, not a local launcher modification.

The retired ChatGPT Exporter is removed through Tampermonkey's native import queue only when its exact retired UUID is a system script. Ordinary user copies remain intact. The r8 compatibility patch also handles native first-install inspection occurring after managed import: only scripts matching the manifest UUID, system flag and UTF-8 source SHA-256 are recognized as product-provisioned. Unknown identities and changed source bytes retain native rejection. Upgrades reconcile previously blocked pinned scripts.

Only the dedicated profile's product extension is granted the existing userScripts capability. Profile writes happen with its browser stopped. Readiness reads a temporary private snapshot of the extension's LevelDB, never edits its database, and checks the consumed generation, exact script UUIDs, versions, enabled/system flags, zero native `evilness`, retired system-script absence, disabled independent updates, source/dependency hashes and permission. Snapshot races fail/retry within a bounded window. Installed execution is verified separately; the [acceptance report](ai-conversation-live-eval-20260910.md) distinguishes native installation and Bridge fixtures from live provider coverage.

## Operations

Both `npm run deploy:local` and `npm run deploy:remote` call shared browser setup. To reconcile only the browser components on an existing Remote deployment:

```bash
SPARKCLAW_BROWSER_ENV_FILE="$PWD/.env.remote" bash scripts/setup-browser.sh
npm run start:remote -- --check
```

A start/preflight validates the installed version; a deployment installs/reconciles it. Upgrade the manifest, reviewed compatibility patch and bundled sources together, then run component tests, a fresh-profile real install, a repeated install, and actual readiness. Never bypass a mismatched script receipt. The old per-user `browser-extensions.json` mechanism is not part of this product contract.

## Managed email readers

The component manifest also includes the QQ Mail, Gmail and Outlook Network Readers. Their editable source is in `scripts/email/userscripts/lib/`; `node scripts/email/userscripts/build.mjs` produces the three pinned files in `tools/browser-userscripts/` and updates their manifest hashes. `--check` rejects stale generated files. They expose read operations to the Controller on the matching signed-in provider page and never schedule synchronization independently.

Session headers, provider pagination tokens and learned original-download URLs stay in the owned page's memory. The Gateway persists account-bound receipt-time intervals and source acknowledgements. The Outlook Controller installs a small early Worker observation bridge because the website may bind its transport before Tampermonkey's asynchronous script injection finishes; the installed Reader consumes those observations and performs qualified reads. If a Reader is absent or cannot prove a request contract, the operation fails closed with a typed network capability error. Installation readiness does not certify mailbox-wide folder coverage or successful original capture; see the implementation status in [the email pipeline design](email-pipeline-optimization-design.md).
