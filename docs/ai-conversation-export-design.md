# Export AI platform conversations: login, script control and workspace persistence

> 2026-09-10 update: see [RevivalStack timeline batch export](ai-conversation-batch-export.md) for the four-platform visible-history userscript fork and separate automation runner. Account-wide completeness and Gateway batch routing remain unqualified; earlier single-conversation scope below is historical for that workflow.


> Language: English | [简体中文](../zh-cn/docs/ai-conversation-export-design.md)

Date: 2026-09-10. Status: **Four capability branches and single-JSON export/workspace persistence implemented in code, not deployed; live four-platform qualification remains incomplete**. Summarization, memory extraction, embeddings, and memory-service integration are out of scope. This scope supersedes the earlier batch/memory pipeline. Batch work can extend a stable single-conversation workflow later; it is not a prerequisite now.

## 1. Current deliverable

SparkClaw controls the RevivalStack userscript on a signed-in conversation page in dedicated Chromium, triggers export, verifies receipt of a real file, saves its original bytes into the **current SparkClaw session workspace**, and returns a path usable by subsequent tools.

The complete workflow is `locate conversation → check script → trigger JSON export → await completed download → validate file → save into workspace → return file receipt`.

Save original JSON by default, with original Markdown optionally added. Persistence neither rewrites text nor generates summaries. Preserve script output unchanged and record provenance, checksums, and gaps in a separate manifest. Quality concerns text, roles, sequence, and conversational context rather than identical layout; available code/table text must not be deliberately removed.

## 2. Selected approach and evidence

The sole exporter base is [RevivalStack / ai-chat-exporter](https://github.com/revivalstack/ai-chat-exporter). The reviewed baseline is v3.1.0, commit [`4ce218d1290956703489870aafa2adfdf4448f38`](https://github.com/revivalstack/ai-chat-exporter/tree/4ce218d1290956703489870aafa2adfdf4448f38). MIT permits modification/distribution with notices retained. Start by controlling the installed script. If automation entry points/status are insufficient, maintain a derived script with a distinct name/version and upstream provenance; building a complete new script is not the first step.

Real clicks on RevivalStack Markdown/JSON controls in dedicated Chromium downloaded two files for a short ChatGPT conversation: Markdown 1232 bytes and JSON 1174 bytes. They retain the prompt, answer, three code-block bodies, and table text. JSON `count=1` means one question/answer pair with two messages. Inline-code styling in the table and one code-block language label were lost, without losing the sample's main text. Long conversations, other platforms, and batch completeness remain unverified.

Two exporters were enabled during that test. Some subsequent clicks produced no new file; fresh task pages succeeded, then Controller returned `browser_extension_unavailable`. The cause is unresolved. One successful download does not establish stable automation.

This revision copies the previously downloaded RevivalStack JSON/Markdown byte-for-byte into this project's development workspace at `data/workspaces/ai-chat-exports/chatgpt/6aa22545-5ecc-83e8-ae2e-76c55897cf15/20260910-live-sample/`, with a manifest and explanation. This archives existing evidence; it is neither a fresh export nor proof of a connected production persistence workflow.

## 3. Control and component responsibilities

Follow task isolation in the [browser runtime](browser-runtime.md) and [browser control design](playwright-extension-browser-design.md). Tampermonkey runs the script; the script reads the page and generates files; SparkClaw owns page operations, download receipt, workspace persistence, and result receipts.

Use the selected browser profile's login state to open the user-specified conversation URL in a task-owned tab. A user's current tab can identify the source, but the task does not take over, navigate, or close that original tab. No extra platform API Key or copied cookies/tokens are required.

Initially use existing snapshot/click and native-download facilities to identify `↓ JSON`/`↓ Export MD` controls. Obtain fresh snapshots before actions, never reuse stale refs. Enable only the exporter under test during automation qualification. Verify source platform/ID, loading/generation state, and message selection scope to prevent manual filters from silently limiting export.

If the installed script cannot reliably expose scope/status, the minimal adaptation is a fixed “export all loaded messages in this conversation” action plus export-operation identifiers and status/errors. “All loaded” must not imply “all historical.” Current Controller has no arbitrary-script execution or Tampermonkey management interface; do not assume either exists. Any later fixed integration must preserve task ownership.

Treat script output and website content as data. This feature does not edit, delete, archive, or share website conversations, connect memory services, or introduce cross-project interfaces.

## 4. Single-conversation workflow

1. **Locate**: resolve the current session's workspace root and target URL, establish an owned task page, and verify platform/conversation identity.
2. **Check**: confirm login, script controls, selection scope, and loaded content. Missing controls mean script-not-ready, not a successful empty export.
3. **Prepare receipt**: register the task's download wait before clicking. Bind the download to its task page and operation rather than scanning global Downloads for unrelated files.
4. **Export**: trigger JSON by default; Markdown is a separate optional action. Navigation or concurrent tasks must not mix receipts.
5. **Validate**: require completed download and a readable regular file, parse JSON, validate message structure and matching source URL/ID, and record message count/scope. Parsing alone does not establish completeness.
6. **Save**: create a distinct capture directory inside workspace, write original bytes to a temporary file, verify size/SHA-256, commit atomically, and write the manifest. Complete persistence before releasing the browser session.
7. **Return receipt**: provide workspace-relative path, format, bytes, hash, message count, source, and `saved / partial / failed` status. Success requires an actual file.

Saved files are available to later read/search/processing tools. Later processing is outside this phase and is not automatically triggered by saving.

## 5. Workspace layout and receipts

Production code resolves the session's `WorkspaceRoot`; do not hardcode a development repository, browser download directory, or model temporary-artifact directory. All AI platform exports live in a dedicated `ai-chat-exports/` workspace directory, separate from `uploads/` and `media/`; JSON files are not scattered at workspace root. Suggested layout:

```text
<WorkspaceRoot>/ai-chat-exports/<platform>/<conversation-key>/<capture-id>/
  <conversation-title>-<platform>.json
  <conversation-title>-<platform>.md  # later option
  manifest.json
```

Derive the JSON filename from the original exported `title`, for example `Travel plans-ChatGPT.json`. Platform labels are ChatGPT, Claude, Gemini and Grok. Missing/blank titles use `未命名对话`. Replace path separators, control/format characters and nonportable filename characters, and bound title length to 180 UTF-8 bytes without splitting characters. Preserve the original JSON bytes. Receipts/manifests reference the actual filename. Separate capture directories isolate duplicate titles and repeat exports without overwrites.

Use a path-safe conversation identifier for `conversation-key`. Isolate accounts where relevant; titles/profile names do not prove identity. A unique `capture-id` prevents repeated exports or duplicate titles from overwriting prior results. Limit filename length and reject traversal, symlink escape, and writes outside workspace using existing workspace file-safety mechanisms.

Manifest fields include schema version, platform, conversation ID, source URL, known account scope, script version, export/save times, task identifier, relative file paths, byte counts, SHA-256, message count, coverage, and warnings. Unknown versions/source times remain unknown; export time is not message creation time.

JSON/Markdown are **original script outputs**, not lossless webpage snapshots. Future normalization must create separate files rather than overwrite originals. Distinct capture directories provide overwrite protection now; semantic incremental deduplication is not required in this phase.

Persistence and completeness are independent: a saved file can have `partial / unknown` body coverage. Incomplete downloads, invalid files, or failed workspace writes must not return `saved`. Partial files may remain explicitly labeled failure evidence, not published as successful results.

## 6. Errors and lifecycle

States are `pending → page_ready → exporting → downloaded → validating → saving → saved`, with `partial / failed / canceled` outcomes. Record per-operation state, errors, and artifact references without logging conversation bodies or credentials.

- Expired login, account change, or access denial: pause affected work and revalidate source before continuing.
- Script not ready or changed page structure: report an explicit error rather than substitute whole-page text.
- No download after clicking: time out with `download_timeout`, diagnose download restrictions/script state, and avoid unlimited clicks.
- Lost control connection: report `browser_extension_unavailable`; obtain a fresh task page/snapshot before bounded retries.
- Download succeeds but saving fails: preserve usable staged files and retry persistence; do not release the session before accessing files its cleanup removes.
- Save succeeds but receipt fails: locate/validate the manifest by capture ID and return the existing result rather than overwrite it.
- Cancellation: stop subsequent operations, clean unfinished temporary files, and retain successfully saved results.

Configure download wait, file-size, and retry limits, reporting any exceeded limit. Controller session staging can disappear upon release; it is not delivery until workspace persistence completes.

## 7. Current acceptance and later work

| Current acceptance item | Requirement |
|---|---|
| Script control | Trigger RevivalStack in dedicated Chromium with correct target/scope, preserving the user's original tab |
| Real file | Completed native download, parseable original JSON, matching source, correct short-sample roles/order/text |
| Workspace persistence | Readable session-workspace file, identical original bytes/hash, still present after browser-session release |
| Reliable receipt | Actual file paths; explicit failure without files; duplicate titles/repeated operations do not overwrite results |
| Recovery | Explicit outcomes for missing script, timeout, lost connection, write failure, and cancellation; no silent missing files |
| Repetition | At least three consecutive fixed-sample runs with outcomes/timing, followed by an interruption/recovery test |

First complete ChatGPT's single-conversation control/download/workspace-receipt workflow. Other platforms share persistence but require independent page/script compatibility checks. Long-history completeness, history discovery, batch scheduling, and incremental deduplication are later extensions. Memory processing is outside current implementation and acceptance.

Section 8 describes the implementation. The workspace sample came from earlier real downloads; this revision uses synthetic pages for native Chromium download tests, not live four-platform qualification. No deployment or global download-directory change was performed.

## 8. Implemented four-branch capability

The product capability name is **Export AI platform conversations**, alongside Browser and Documents, with four platform branches. The code description now uses “Export AI platform conversations.” Internal `ai_chat` and existing capability/workflow IDs remain:

| Branch | Capability/workflow ID | Initial accepted source |
|---|---|---|
| ChatGPT | `ai_chat.chatgpt` | `https://chatgpt.com/c/<id>` |
| Claude | `ai_chat.claude` | `https://claude.ai/chat/<id>` |
| Gemini | `ai_chat.gemini` | `https://gemini.google.com/app/<id>` |
| Grok | `ai_chat.grok` | `https://grok.com/c/<id>` |

Example: “Export this ChatGPT conversation into workspace: https://chatgpt.com/c/<id>”. An explicit conversation link is required; missing links need clarification. Opening AI websites, sending prompts, and generic webpage reading are different capabilities. Each branch binds its provider and rejects other providers' links.

Execution follows catalog → fixed direct workflow → `ai_chat.export` tool → BrowserControl task session → fixed Controller `ai_chat.export` operation → native download listener → Gateway validation → session-workspace receipt. The tool accepts no model-selected workspace, arbitrary file path, script code, or selector.

Controller checks fixed RevivalStack controls, select-all state, and an empty search field. Filters, recognized generation-in-progress controls, or missing controls prevent export. It does not alter manual selection or bypass login. Fixed bundled Playwright code registers the download listener before clicking JSON, saves the completed native download into task staging with `saveAs`, reads it, and removes staging. Neither global download settings nor global Downloads scanning is used.

Controller and Gateway may have different filesystems. The authenticated local control channel carries at most **4 MiB** of original JSON as Base64 with SHA-256 rather than exposing a host path to a container. Conversation text never enters the tool response/model context; the Gateway returns only a file receipt. Download wait is 25 seconds, script-control wait 15 seconds, fixed MCP-operation timeout 60 seconds, and export context 90 seconds. Errors propagate; the initial workflow does not blindly retry, and users can rerun it.

Gateway revalidates provider, source URL, nonempty message structure, and checksum. Session `WorkspaceRoot` and `os.Root` confine filesystem access. A unique capture directory first writes `<conversation-title>-<platform>.json` and `manifest.json` under `.pending-*`, syncs files, then renames to publish. Failure cleans incomplete staging; repeat exports use new IDs without overwriting results. Only JSON is automated initially; Markdown remains a later option.

Receipts include `status`, `path`, `manifest_path`, `sha256`, `bytes`, `message_count`, `provider`, `source_url`, and `coverage`. Paths are workspace-relative. Body coverage remains `unknown`: obtaining script output does not prove full long-history capture. Account-history discovery, batch export, takeover of user tabs, summaries, and memory processing are not implemented.

Implementation: `internal/capability/catalog.go` registers branches; `internal/agent/ai_chat_workflow.go` binds routes/direct execution; `internal/toolhub/ai_chat.go` binds sessions; `internal/aichatexport/` validates/persists; `tools/browser-controller/src/ai-chat-export.mjs` listens for script downloads. Deployment requires updating/restarting Gateway and its Controller; no deployment was performed here.

Validation covers four-branch routing, cross-provider URL rejection, session workspace binding, caller-path rejection, original bytes/hashes, no overwrite on repeat exports, symlink escape, cancellation cleanup, listener-before-click, and rejection of filtered selection. Three consecutive real Blob downloads passed in isolated Chromium using a synthetic page. This verifies transport mechanics, not live websites or Tampermonkey installation. Each real platform still needs qualification with its script installed and account signed in.

## 9. Product name and intent descriptions

Use **Export AI platform conversations** as the top-level name, rather than the broad “AI conversations.” Hierarchy:

```text
Capabilities
├─ Browser
├─ Documents
└─ Export AI platform conversations
   ├─ ChatGPT
   ├─ Claude
   ├─ Gemini
   └─ Grok
```

Top-level description: **Export existing conversations from ChatGPT, Claude, Gemini and Grok websites through RevivalStack, save original JSON into the current workspace, and return file paths.**

| Branch | Catalog and intent description |
|---|---|
| ChatGPT | Export one specified existing ChatGPT website conversation as original JSON into the current workspace. |
| Claude | Export one specified existing Claude website conversation as original JSON into the current workspace. |
| Gemini | Export one specified existing Gemini website conversation as original JSON into the current workspace. |
| Grok | Export one specified existing Grok website conversation as original JSON into the current workspace. |

Routing considers the export-existing-conversation action, platform, and target together. Examples include “Export this ChatGPT conversation,” “Download this Claude conversation into workspace,” “Save the conversation at this Gemini chat link,” and “Export this Grok conversation as JSON.” Resolve the platform from an explicit name or canonical conversation URL; clarify contradictions. Ask only for missing platform/link information rather than requiring the whole request again.

“Discuss AI with me” is ordinary conversation; “Open ChatGPT” is browser navigation; “Ask Claude a question” is not export; “Summarize the existing chat JSON in workspace” is document reading/later processing; “Log in to Gemini/check Grok login” is a platform-login settings action. A platform name alone must not select export. The catalog description, routing examples and bilingual settings copy are synchronized in this revision. The catalog revision was advanced for classification; internal IDs remain unchanged.

## 10. Four-platform login and detection settings

Add **Configuration settings → Connections → AI platform login** beside browser-control and browser-email settings. Description: **Reuse the dedicated browser login to export existing AI platform conversations into workspace.** ChatGPT, Claude, Gemini and Grok cards provide “Open login page” and “Check status,” with shared “Check all” and “Refresh status” actions.

| Platform | Fixed official login entry |
|---|---|
| ChatGPT | `https://chatgpt.com/` |
| Claude | `https://claude.ai/` |
| Gemini | `https://gemini.google.com/` |
| Grok | `https://grok.com/` |

Login, detection and export reuse the same persistent SparkClaw dedicated Chromium profile. Existing login is reused without an incognito profile or copied credentials. Settings neither accept nor store/display platform passwords, cookies, session tokens or API Keys.

“Open login page” reuses Controller's explicit browser-login mechanism with the same user-data directory and a fixed official URL. The user completes login and then checks status. Existing user pages are neither navigated nor closed. An opened page is not evidence of successful login. CAPTCHA, account selection and completion remain user actions.

“Check status” uses fixed `ai_platform.check` in an isolated task page, inspecting only visible authentication UI such as login buttons, account menus and the composer. Release its page afterward and allow a short bounded wait for UI readiness. A reachable URL is insufficient evidence. Detection sends no messages, exports no conversation, changes no account settings, and never checks Tampermonkey or export buttons. “Check all” runs sequentially, continues after individual failures, and reports errors.

Display only shared **browser-control state** and per-platform **login state**. Login states are unchecked/checking/signed in/signed out/user action required/unconfirmed, with last-check time and fixed error codes. **There is no script-readiness state, script-check action or script diagnostic in settings.** Export execution still needs to locate the JSON button; this is part of exporting, not settings detection.

Probes are conservative: explicit login pages/buttons mean signed out; both an explicit account menu and a chat composer are required for signed in. Supported identity-provider redirects or visible challenges require user action. Insufficient evidence is unconfirmed, not signed out. Platform DOM changes may require probe updates. Account display names are not extracted or saved in this version.

Gateway retains results only in memory for five minutes; restart resets them. Credential generation, profile or Controller-generation changes and non-ready browser state clear cached checks. Opening a platform login page invalidates its previous check. Cached signed-in state is never permanent export permission; every export validates its actual target/source. An unobserved account logout/switch can leave a historical timestamped result until expiry or a fresh check.

Three endpoints reuse the browser authentication boundary: `GET /api/browser/extension/ai-platforms` lists status; `POST /api/browser/extension/ai-platforms/{provider}/login` opens login; `POST /api/browser/extension/ai-platforms/{provider}/check` detects login. Unknown providers, query parameters and extra fields are rejected. Callers cannot supply arbitrary URLs/profiles. Busy protection serializes login/check actions without taking over export tasks.

Implementation: `internal/aichatexport/login.go` manages probes/cache, `internal/gateway/ai_platform_endpoints.go` exposes authenticated APIs, `tools/browser-controller/src/ai-platform-login.mjs` provides fixed URLs/read-only probes, and WebChat `settingsAIPlatforms.tsx` provides bilingual platform cards. Code is connected but not deployed or qualified against all four real login sessions. Acceptance covers absence of script checks, independent states, login reuse, opening-not-signed-in, cache invalidation, conservative probes, continuing check-all after failure, and preservation of original tabs.

Tampermonkey and both export userscripts are now managed product components: every Local/Remote deployment installs and reconciles pinned versions through [browser component management](browser-components.md). They are not per-user extension configuration.
