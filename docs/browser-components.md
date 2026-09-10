# Managed browser components

[简体中文](../zh-cn/docs/browser-components.md)

Tampermonkey, RevivalStack AI Chat Exporter, and ChatGPT Exporter are maintained SparkClaw product components. Every Local/Remote installation uses the same component manifest and shared setup; no user must manually install these assets or add extension paths. Account logins remain in each owner's dedicated Chromium profile.

## Release ownership

`configs/browser-components.json` pins Tampermonkey upstream/product versions, the official CRX checksum, exact exporter source bytes, and bundled dependencies. `tools/browser-userscripts/` retains source and license notices. The official proprietary extension is downloaded at installation, not copied into this repository. A changed official download fails checksum verification; refresh the reviewed pin instead of accepting unverified latest bytes. Verified downloads are cached under the owner's XDG cache.

The product extension has the stable path `/opt/sparkclaw/tampermonkey` and path-derived ID `dlgaaenljmglaedeiniphopajnkeakkf`. Updates retain this identity. The original owner's earlier unpacked extension and database are preserved for rollback; its private path is not used for other deployments. Deployment backs up profile preferences and the legacy manager database. The product database starts with native system imports; copying ordinary scripts into it would cause duplicate injection. Legacy manager data remains at its original identity.

## Native managed provisioning

The installer supplies a narrowly scoped Chromium third-party policy for this product extension ID under `/etc/chromium/policies/managed/sparkclaw-userscripts.json`. Its `jsonImport` references an embedded public script bundle with Tampermonkey's versioned structural digest. There is no provisioning HTTP service and no file-access grant. Each explicit deployment creates a new import generation; ordinary restarts reuse it. Each script has a stable UUID in the product manifest. A narrowly matched provisioning patch uses that UUID instead of generating another copy, so repeated deployments update the same script and retain its storage. The product reconciliation path also allows reinstalling that same managed UUID at the same source version; upstream normally rejects this case. This exception is limited to the explicit managed provisioning path. Tampermonkey installs/replaces scripts as enabled system scripts and keeps unrelated profile data. Per-script automatic updates are disabled so all deployments use the product's exact release. Required JavaScript libraries are imported from pinned bundled bytes.

Chromium 148 can defer managed-storage callbacks until policy initialization (observed around five seconds). Upstream Tampermonkey 5.5.0 gives up after one second. The product applies an exact-match compatibility patch extending the bounded wait to twenty seconds and waiting for the current deployment hash instead of accepting an empty object or an older cached policy. An unmatched patch aborts the build. The patched service worker uses a filename bound to the product version and deployment hash to invalidate Chromium caches on upgrade.  Its product version is distinct from upstream; full installed file hashes are recorded. This patch is source-controlled deployment logic, not a local launcher modification.

Only the dedicated profile's product extension is granted the existing userScripts capability. Profile writes happen with its browser stopped. Readiness reads a temporary private snapshot of the extension's LevelDB, never edits its database, and checks the consumed generation, unique script membership, versions, enabled/system flags, disabled independent updates, source/dependency hashes and permission. Snapshot races fail/retry within a bounded window. This proves provisioning, not successful export on every provider website.

## Operations

Both `npm run deploy:local` and `npm run deploy:remote` call shared browser setup. To reconcile only the browser components on an existing Remote deployment:

```bash
SPARKCLAW_BROWSER_ENV_FILE="$PWD/.env.remote" bash scripts/setup-browser.sh
npm run start:remote -- --check
```

A start/preflight validates the installed version; a deployment installs/reconciles it. Upgrade the manifest, reviewed compatibility patch and bundled sources together, then run component tests, a fresh-profile real install, a repeated install, and actual readiness. Never bypass a mismatched script receipt. The old per-user `browser-extensions.json` mechanism is not part of this product contract.
