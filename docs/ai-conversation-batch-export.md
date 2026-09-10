# RevivalStack timeline batch export

The SparkClaw fork (`3.1.0-sparkclaw.1`) adds batch orchestration for ChatGPT, Claude, Gemini, and Grok. The retired pionxzh ChatGPT Exporter is removed from the repository and managed-script manifest. Original RevivalStack MIT notices remain. Upstream automatic updates are disabled so they cannot overwrite the fork.

## Use in the browser

Install the bundled `tools/browser-userscripts/revivalstack.user.js`. Open the signed-in platform's expanded history list: ChatGPT sidebar, Claude `/recents`, Gemini `/app`, or Grok `/history`. Click **批量导出到文件夹**, supply an account/workspace label, and choose an output directory. Allow the task popup. Keep the source page and popup open until completion. **停止批量** stops subsequent work; successfully saved exports remain.

The script scans lazy-loaded and virtualized visible history, deduplicates conversation IDs, and processes oldest first. Exact update timestamps take precedence where the UI provides them; otherwise the reverse of the displayed newest-first list is used. Pinned/grouped lists can therefore affect order. The source page stays on its URL; conversation capture uses a separate popup. Existing manual message filters do not restrict batch capture.

Each JSON file retains the fork's original serialized conversation output. SHA-256 read-back verification precedes the account-scoped ledger update. A failed save/checkpoint is retried on the next run. Existing records are skipped only if their saved file still verifies; missing/corrupt files are recaptured. If history exposes no update timestamp, the conversation is revisited and title/message content hashes decide whether another file is needed. Changes only to export timestamps do not create duplicate files. Files use provider/conversation/content-hash names to prevent title collisions. Batch reports list exports, skips and failures.

Account labels identify **local checkpoints**, not verified platform identities. Use a different label after changing accounts or platform workspaces, and do not switch login during a run. No account password, token or cookie is copied. Browser UI mode uses the File System Access API and a same-origin task popup, so it requires desktop Chromium and popup/directory permission. It does not mark a download click as a successful save.

## SparkClaw automation

A bounded entry point accepts an authorized Playwright BrowserContext and the current session's workspace:

```js
import { exportTimeline } from './scripts/ai-chat/batch-export.mjs';
const receipt = await exportTimeline({
  context, provider: 'claude', workspaceRoot: sessionWorkspace,
  accountScope: 'my-account-workspace', signal,
});
```

The callable creates/closes only its own task page. It consumes fixed userscript controls and saves original JSON, ledger and per-run manifest under `ai-chat-exports/<provider>-<account-hash>/`. Each file is atomically published and verified before checkpointing. A per-directory lock prevents concurrent exporters; a stale lock after a process crash must be removed only after verifying no exporter owns it. Other browser tabs and email files are untouched. Cancellation/timeout closes the owned page; already saved receipts survive.

For local orchestration with an explicitly supplied existing local CDP endpoint:

```sh
node scripts/ai-chat/export-batch.mjs --provider chatgpt \
  --workspace /absolute/session/workspace --account my-account-workspace \
  --cdp http://127.0.0.1:PORT
```

Run separately for each provider (or call the function sequentially). This command does not install/update the script, start a browser, change a browser profile, or restart services. It requires the browser-controller Playwright dependency. Its fixed DOM control IDs are `sparkclaw-batch-scan`, `sparkclaw-batch-capture`, `sparkclaw-batch-status` and `sparkclaw-batch-output`. These are data/capture controls, not an arbitrary-code bridge.

For the native SparkClaw Browser Bridge (which does not expose a normal CDP port), use `scripts/ai-chat/qualify-bridge.mjs --help`. This qualification entry temporarily loads the candidate into owned pages, samples at most two conversations per provider, and uses the existing background-rendering marker. Supply its browser-control credential through the configured environment, never a command argument. It does not update the installed userscript.

The existing `ai_chat.export` Gateway workflow remains the single-conversation native-download workflow. This batch runner is an additional callable/CLI, **not yet routed from the Gateway's natural-language capability**. Integrating that workflow is separate from the userscript and must retain session workspace and task ownership.

## Coverage and qualification

**This implements visible-history batch export; it does not yet establish retrieval of every conversation in an account.** Scrolling to a stable end is not proof that archived, project-only or hidden conversations were included. Reports retain `complete:false`, `coverage:unknown` and `saved_visible_history`/`partial`, never a claim of account-wide completeness. Empty/blocked lists and scan limits fail explicitly. The UI selectors must be qualified against the current signed-in websites; a collapsed sidebar, changed DOM or login redirect can prevent enumeration. Conversation-body stability likewise does not prove full historical body coverage.

Offline tests cover four-provider URL isolation, virtualized enumeration, chronological ordering, failed-save/checkpoint recovery, unchanged/updated content, missing files and cancellation. Isolated Chromium fixtures exercise the full bundled userscript, filesystem runner, rerun and preservation of an unrelated existing tab. These fixtures remain synthetic. Additional [real dedicated-browser evaluation on 2026-09-10](ai-conversation-live-eval-20260910.md) exported seven existing conversations across all four platforms and verified saved hashes and source role counts. Account-wide and long-history coverage remain unqualified. No shared-browser deployment was performed.

## Maintenance and integration

Edit `tools/browser-userscripts/src/timeline-batch.js`, then run:

```sh
node tools/browser-userscripts/build-batch.mjs
node tools/browser-userscripts/build-batch.mjs --check
node --test tools/browser-userscripts/test/*.test.mjs
python3 -m unittest discover -s scripts -p test_browser_components.py
```

The build embeds the source into the self-contained userscript and generates `timeline-core.mjs`. The build also updates the fork SHA-256 in `configs/browser-components.json`; `--check` verifies it. The browser integration test requires `SPARKCLAW_TEST_PLAYWRIGHT` (absolute Playwright module path) and `SPARKCLAW_TEST_CHROMIUM`.

Merge this worktree's commit via the original integration task. Pay attention to the managed script manifest and installer tests if the email worktree edits them. Retain the existing RevivalStack UUID. The importer should reconcile removed managed scripts; check that an already-installed ChatGPT Exporter is disabled/removed when deployment is eventually scheduled. A manually installed copy may require manual removal. No email provider/controller registrations are changed here.
