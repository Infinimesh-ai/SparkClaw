# Dedicated-browser live evaluation, 2026-09-10

> Language: English | [简体中文](../zh-cn/docs/ai-conversation-live-eval-20260910.md)

The evaluation connected to the running SparkClaw Chromium through its Browser Bridge and used task-owned pages on ChatGPT, Claude, Gemini, and Grok. It reused existing login sessions and conversations. No messages were sent, conversations created, or email tabs changed.

The shared installation was RevivalStack 3.1.0. The candidate was temporarily injected into test pages with the batch and extraction code unchanged, a separate control container, and default GM settings. Initialization called `addExportControls()` instead of the full `init()`. This did not upgrade the shared Tampermonkey installation, restart Chromium, or deploy Gateway.

## Results

| Provider | Visible conversations | Saved | Messages per file | Rerun |
|---|---:|---:|---|---|
| ChatGPT | 2 | 2 | 2, 4 | Both skipped without duplicates |
| Claude | 2 | 2 | 6, 2 | Both skipped without duplicates |
| Gemini | 2 | 2 | 4, 4 | Both skipped without duplicates |
| Grok | 1 | 1 | 2 | Skipped without duplicates |

Seven real conversations containing 24 messages were saved. Each conversation was reopened independently: all seven file hashes matched the ledger, and exported user/AI message counts matched the page. For applicable samples, text from 14 `pre code` nodes and 90 table cells appeared in the exported files. Node counts do not represent distinct code blocks. This is not a character-for-character or visual fidelity certification.

Conversations without update timestamps are revisited and compared by title/message fingerprint. Export timestamp changes alone do not create duplicate files. Each provider exposed at most two conversations during this run, so the qualification sample limit did not exclude any discovered conversation.

## Issues found and fixed

1. Background Bridge pages can suspend animation frames. Pointer-based clicks and default `waitForFunction` polling stalled. The runner now waits for controls to attach, invokes them directly, and uses timer polling.
2. Gemini retains hidden loading indicators. Discovery now considers only visible loading/more controls.
3. Grok uses a `[data-sidebar="sidebar"]` root and can hydrate its history slowly. Discovery now recognizes that root and waits approximately 30 seconds before rejecting a page with neither conversations nor an explicit empty state.
4. The dedicated browser has no ordinary CDP endpoint. `scripts/ai-chat/qualify-bridge.mjs` uses the configured native Bridge launcher and caller-provided browser credential, plus the existing background-rendering marker.

The original regression run passed 14 batch tests and 9 component tests. Isolated Chromium covered all four fixture providers, repeat exports, and preservation of unrelated pages. Those fixtures are separate from the live-site evidence above.

## Reproduction and private evidence

```sh
node scripts/ai-chat/qualify-bridge.mjs \
  --workspace /absolute/private/session/workspace \
  --account account-workspace-label \
  --providers chatgpt,claude,gemini,grok
```

Supply `PLAYWRIGHT_MCP_EXTENSION_TOKEN`, `PLAYWRIGHT_MCP_EXECUTABLE_PATH` (Bridge launcher), and `PLAYWRIGHT_MCP_USER_DATA_DIR` through existing local credential/runtime configuration. Do not put credentials in command arguments, reports, or the repository. `SPARKCLAW_PLAYWRIGHT_CLI_ENTRY` can select an installed CLI.

Original JSON, ledgers, per-run manifests, independent page comparisons, and qualification summaries remain in the ignored private worktree directory `data/workspaces/ai-chat-live-eval/20260910/`. Summaries identify the candidate and temporary injection hashes. Conversation text, account information, and titles are excluded from this report and commits.

## Follow-up scope

The production CLI now defaults to the native Bridge and uses installed controls without candidate injection or a qualification sample limit. Discovery can traverse observed same-provider project/archive/history links, record inaccessible collections, and deduplicate conversations. These additions have separate fixture coverage and must not be inferred to have passed the earlier live run.

The manual flow now uses separate clicks for choosing a directory and opening the export popup, avoiding competing transient-activation requirements. The managed upgrade retires only the old ChatGPT Exporter's exact system UUID and preserves ordinary user copies. The final isolated suite passed 18 tests, including native Tampermonkey fresh import, upgrade retirement, user-copy preservation and redeployment. Only the fixture's managed-policy transport was substituted; native download, hash verification and installation ran intact. Component checks passed 10 tests and the authoritative documentation check covered 77 Markdown files. A strengthened Bridge ownership regression passed after the suite: a later popup and a missing target cannot redirect navigation or cleanup to another page.

The manual-path fixture exercised the full bundled initialization, a real popup, and actual OPFS file writes; directory-picker cancellation and popup blocking were simulated. An initial native-installed control probe timed out despite successful import and bootstrap registration. The follow-up below identifies and fixes that installation bug; injected initialization alone was not accepted as evidence.

A subsequent shared-browser connection succeeded, but Chromium rejected Bridge navigation to Tampermonkey's options page and the extension manager. The computer-use tool exposed only the Codex in-app browser, not the dedicated browser or native directory dialog. Consequently the shared installation was not changed or restarted, and its coordination window was released to the email task.

**Live evidence establishes short-conversation extraction and visible-history reruns only.** The seven-conversation live run does not establish shared upgrade, native directory-picker success/cancellation, popup interaction, newly added collection traversal, long virtualized transcripts, rate limits, large accounts, or Gateway natural-language batch routing. Installed-script execution has separate isolated acceptance evidence below. Results retain `saved_visible_history` and unknown account-wide coverage.

## Installed-script execution acceptance

The missing controls were a product initialization-order bug. On a fresh unpacked-extension installation, Tampermonkey's `onInstalled` unfamiliar-script inspection runs after native managed import. It marked the already-imported script with `evilness=12` (foisted origin), leaving it enabled in storage but excluded from execution. Native `userScripts` permission and registration were working. Both intercepted fixture pages and a local HTTPS fixture reproduced the failure.

Component patch r8 recognizes an already-provisioned script during this first-install inspection only when its UUID, `system=true`, and UTF-8 source SHA-256 all match the pinned manifest. Other scripts retain native unfamiliar-origin inspection, and other blacklist checks remain active. Profile verification now rejects nonzero `evilness`; enabled/version/hash metadata alone is insufficient.

The revised lifecycle test retains the same installed extension path and browser profile across upgrades. It requires real controls on a newly navigated page after each import, verifies recovery from the older foisted flag, retires the legacy managed UUID, preserves an ordinary user copy, and repeats deployment.

`installed-bridge.test.mjs` starts an isolated Chromium with the actual staged Tampermonkey, the actual SparkClaw Browser Bridge, and the actual native messaging host/launcher. It creates its own profile, credential and Unix socket. The production `connectBatchBridge()` and `exportTimeline()` export a short ChatGPT fixture containing two messages, verify the file SHA-256 and exact message text, skip unchanged content on rerun, and preserve an existing owner page. It does not inject the userscript or replace `UIManager.init()`/GM APIs. The fixture also supplies an unrecognized system script and requires native `evilness=12` protection to remain effective. A second native negative case uses the recognized managed UUID with changed source bytes and also requires rejection. The lifecycle assertions derive the complete managed UUID set from the current manifest, so additional email scripts do not invalidate the test.

Only the isolated managed-policy transport and website content are fixtures. This closes the installed-execution acceptance gap without modifying the shared browser or email scripts; it is not an additional signed-in live-site conversation result.

```sh
SPARKCLAW_TEST_PLAYWRIGHT=/absolute/playwright/index.mjs \
SPARKCLAW_TEST_CHROMIUM=/absolute/chromium/chrome \
SPARKCLAW_TEST_CLI=/absolute/@playwright/cli/playwright-cli.js \
SPARKCLAW_TEST_COMPONENT_LIFECYCLE=1 \
SPARKCLAW_TEST_NATIVE_BRIDGE=1 \
node --test tools/browser-userscripts/test/component-lifecycle.test.mjs \
  tools/browser-userscripts/test/installed-bridge.test.mjs
```

The test registers its native host only inside its temporary profile. It does not require changing the user's native-host registration, browser-control credential or Controller socket.

Final targeted acceptance passed 3/3 native tests, 10/10 component checks, and the authoritative 77-file documentation check.
