# Workflow Capability Matrix

> Language: English | [简体中文](../zh-cn/docs/workflow-capabilities.md)

This document is the user-visible capability inventory for the Workflow runtime.
It describes only registered Workflow Profiles that can currently be selected
and completed. A ToolHub tool being registered does not by itself make a feature
available.

## Shared Execution Contract

- Every natural-language owner question enters the same semantic graph through
  asymmetric channels. Embedding receives only that question. Fast/Tree receives
  the same question plus bounded typed context and scores candidates without
  rewriting the request or selecting resources. Candidate-neutral grounding,
  weighted fusion, and the final Top-2 decision select at most one
  Catalog-validated leaf.
- Each Workflow profile selects its execution lane. Document read/edit model
  calls currently use Fast; other profiles retain the Deep default.
- Conversation context, attachments, ToolResult messages, observation ordering,
  compaction, grounding, and channel delivery continue to use the shared runtime
  pipeline.
- Workflow plans contain no Skill IDs and Workflow execution loads no Skill text.
  The versioned profile, active capability scope, argument bindings, ToolHub
  metadata, and Policy are the complete execution boundary.
- Bound queries, URLs, workspace paths, locations, output paths, and outcome refs
  are materialized from persisted state. A model cannot replace them during a
  later stage.
- A matched Workflow failure is explicit. It never falls back to a generic
  executor or a different capability.
- An `unmatched` route terminates as `router.blocked`; it does not expose
  tools or a fallback executor.
- A browser, weather, or document request outside the current profile revision
  is changed from `unmatched` to `blocked`; there is no legacy fallback to
  recover through.

## Current Profiles

| Capability / profile | What it can do | Fixed execution path | Current boundary |
|---|---|---|---|
| `conversation.answer` r2 | The `answer` variant answers greetings, stable common knowledge, and simple explanations. The `publish` variant returns the current ordered text, image, audio, and file parts as one ordinary message. | `answer` performs one no-tool Deep model answer. `publish` makes no model or tool call; it governs workspace attachments and freezes the normalized request `MessageContent` as the Workflow result. A Web request with only media parts selects `publish` deterministically without synthetic text or semantic model calls. When media exists, command text is removed and only image/audio/file parts remain. | `answer` cannot use current facts, governed resources, or actions. `publish` cannot inspect, transform, edit, or schedule message parts. The delivery target remains solely in `ReturnRoute`. A pure-media publication to a selected third-party endpoint is delivered without approval or a source-WebChat assistant result; text-only external results retain send approval. |
| `browser.internet_search` r1 | Search public, current Internet information from a frozen query. Weather warnings, news, official-source discovery, comparison, and air-quality research also use this general search path. | One stage: `web.search`, currently backed by Infinimesh Info when enabled. | It does not open result pages, read a known URL, operate a signed-in browser, or turn a direct weather request into a card. The provider must be configured and return a bounded result. |
| `browser.weather` r1 | Produce one weather PNG for an explicit location. The card uses normalized current conditions and temperature, the matching daily low/high range, and up to five available future hourly entries. | `weather.lookup` -> `media.render_weather_card`. The frozen route location is bound to a typed `POST /v1/info/weather` request; the render stage receives only the completed lookup ref. | Infinimesh Info is the only weather source and fixed metric units are mandatory. Malformed, incomplete, unsupported-unit, or unauthorized responses fail without generic query/search or parsing fallback. Coordinates, AQI, and alerts are not projected into the card payload. Text-only weather, missing location, warnings/news/source comparison, and air-quality research are outside this profile. |
| `browser.automation` r2 | Acquire exactly one explicit HTTP(S) URL or runtime-registered destination, prove it with hidden and visible snapshots, and leave the verified visible result open. Explicit URLs require normalized equality; registered destinations may also match a registry-bounded host or true subdomain after a site redirect. The initial registry contains QQ Mail (`https://mail.qq.com/`). | Passive `browser.status` -> `browser.list_tabs` -> focus/navigate/open -> settle -> hidden snapshot and route validation -> visible open/focus -> settle -> visible snapshot and route validation. Structural stages are Runtime-invoked and every navigation receives a new settled snapshot. | Unrelated tabs are never reused. A reusable initialized profile opens the target directly without exposing `about:blank` or asking for login again. The run cannot succeed before visible validation, and production never closes the result tab. Clicks, typing, forms, screenshots, arbitrary page reads, and multi-URL work remain outside this profile. Browser r1 is retired. |
| `browser.interaction` r2 | Inspect one managed current tab, explicit HTTP(S) URL, or registered destination with a page subgoal, then perform up to three independently verified, ref-bound clicks and leave the verified visible result open. A qualifying target tab is focused first; one selected blank tab is only a fallback when no target matches. | The automation r2 acquisition chain -> hidden `browser.assess_goal` -> bounded `browser.click` -> settle -> fresh snapshot -> `browser.validate_transition` -> `browser.assess_goal`; progress may repeat the action loop. Completion then runs visible open/focus -> settle -> fresh snapshot -> a second `browser.assess_goal`. The persisted ten-capability boundary is projected to the active stage. | Every argument is bound to persisted generation-scoped refs. Stale refs/generations, repeated state, route divergence, transition failure, or a third progress verdict fail closed. Login and human verification pause in a durable owner handoff; ambiguous replies do no browser work, and resume requires matching visible authentication/task evidence followed by fresh hidden evidence. Type, select, upload/download, credentials, captcha/2FA, payment/purchase, form submission, screenshots, arbitrary scripts, and unsafe consequential actions remain outside r2. |
| `document.read` r4 | Read, summarize, or extract verbatim in-image text from one exact governed workspace file resolved from the current request or recent document records. Supported detection covers text, DOCX, XLSX, PPTX, PDF, and images. | Recent-document resolution and deterministic path/type preflight -> persisted `confirm_document_target` evidence -> one Runtime-invoked `files.read`, `pdf.extract_text`, or `images.inspect` call according to the frozen format -> Fast finalization over completed evidence. `images.inspect` runs optional OCR beside Fast visual semantics and classifies text/no-text explicitly; scanned PDF pages use the same bounded OCR adapter. | The single format-qualified reader is `direct_once`; the model does not decide whether to call it. OCR remains untrusted evidence, not a model lane or separate Workflow. Disabled or failed OCR degrades explicitly to Fast visual evidence, and text-free images omit OCR field noise. A unique recent document may satisfy a follow-up without another attachment. The path must resolve inside the workspace to one regular non-symlink file. File search, multiple-file comparison, mutation, and arbitrary external paths are outside this profile. |
| `document.edit` r6 | Apply one supported bounded edit to one governed text, DOCX, XLSX, PPTX, or PDF file and write one or more traceable output copies. XLSX supports typed cell/row evidence, prefix-only row updates, and verified OOXML package preservation. | Deterministic path/type preflight -> `confirm_document_target` -> one Runtime-invoked format-qualified read in `document_locate_evidence` -> explicit retry-bounded `select_edit_operation` decision -> `document_edit` with only the persisted format/operation entry materialized. XLSX selection and execution share bounded `xlsx_sheet_evidence_v1`; Runtime binds workbook plus target hashes before approval. The default output is `<name>-sparkclaw-edit.<ext>` and subsequent copies advance to `-2`, `-3`, and so on. | All model-driven document stages use Fast. Localization never asks a model whether to call the reader or repeats the read. Missing, invalid, unsupported, stale, or ambiguous operation/target evidence blocks before mutation. XLSX packages with unverified features block before approval; undeclared post-write part drift deletes the output, while success returns `package_preservation=verified`. Reversible writes require approval, and every output is linked to its parent and activity. Unrelated turns do not inherit the latest document. |
| `schedule.manage` r2 | Create, list, update, or cancel scheduled tasks from conversation or typed WebChat toolbar actions. | Create/read are single-stage. Edit/delete run `reminders.list` -> unique pending target + frozen version -> `reminders.update` or `reminders.cancel`; all writes use `ScheduleRegistry` compare-and-swap. | Toolbar IDs are hints that must be found in a fresh owner-scoped list. Due content re-enters ordinary Message Runtime; the Timer does not select capabilities or send directly. The reminder endpoint is preserved during edit. |

## Document Edit Operations

| Format | Supported edit operations |
|---|---|
| Text | `replace_text` |
| DOCX | `replace_text`, `replace_paragraph`, `insert_paragraph`, `delete_paragraph`, `set_text_style` |
| XLSX | `replace_text`, `update_cell`, `insert_row`, `delete_row`, `update_row`, `append_row` |
| PPTX | `replace_text`, `add_slide`, `update_slide`, `duplicate_slide`, `delete_slide` |
| PDF | `extract_pages`, `delete_pages`, `rotate_pages`, `split` |

## Unmigrated Features

Code/command assistance, image inspection, memory, and other domains without a
registered Workflow terminate as `unmatched`. The legacy generic loop has been
removed; resuming a persisted pre-workflow run now terminates with an explicit
retired-runtime message. Registered tools outside the
current Workflow matrix remain in ToolHub for later migration but are not
advertised as available features.
