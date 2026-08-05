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
| `browser.automation` r2 | Acquire exactly one explicit HTTP(S) URL, runtime-registered destination, or named public website, prove it with hidden and visible snapshots, and leave the verified visible result open. The existing destination registry remains the first lookup path; a named registry miss uses the first safety-valid URL in Info's ordered structured results. | Optional hidden `web.search` -> `browser.identify_public_target` for a named miss, then passive `browser.status` -> `browser.list_tabs` -> focus/navigate/open -> settle -> hidden snapshot and route validation -> optional owner-requested visual inspection -> visible open/focus -> settle -> visible snapshot and route validation. | Info relevance ordering is trusted, but every selected URL and redirect must remain public HTTPS. Unrelated tabs are never reused. The run cannot succeed before visible validation, and production never closes the result tab. Clicks, typing, forms, arbitrary page reads, and multi-URL work remain outside this profile. Browser r1 is retired. |
| `browser.page_read` r1 | Read, summarize, or extract bounded content from exactly one explicit URL or named public page in managed headless Chromium. | Optional Info target identification -> hidden `browser.status` -> hidden `browser.open` for the frozen URL -> hidden `browser.read` with `require_browser_session=true` and active-page reuse -> final URL and content validation. | The fixed health/open/read chain cannot skip browser acquisition, use a visible presentation stage, or fall back to direct HTTP. Login or human verification pauses in the shared owner handoff. Open-ended research remains `browser.internet_search`; page mutation and multi-URL work are outside this profile. If final model generation is unavailable after a successful read, Runtime returns bounded extracted page content as untrusted evidence. |
| `browser.interaction` r2 | Inspect one managed current tab, explicit HTTP(S) URL, registered destination, or Info-identified public site with a page subgoal, then perform up to three independently verified, ref-bound clicks and leave the verified visible result open. | Target identification when needed -> automation r2 acquisition -> hidden `browser.assess_goal` -> bounded `browser.click` -> settle -> fresh snapshot -> `browser.validate_transition` -> `browser.assess_goal`; progress may repeat the action loop. Completion may add one owner-requested visual inspection, then runs visible open/focus -> settle -> fresh snapshot -> a second `browser.assess_goal`. | Every argument is bound to persisted session/page generations and fresh refs. Stale evidence, repeated state, route divergence, transition failure, or a third progress verdict fails closed. Login and human verification use a durable owner handoff. Type, select, upload/download, credentials, captcha/2FA, payment/purchase, form submission, arbitrary scripts, and unsafe consequential actions remain outside r2. |
| `browser.form_draft` r1 | Type or select up to five exact owner-supplied values in ordinary reversible fields without committing the form. It can use the current managed tab, an explicit URL, a registered destination, or an Info-identified public site. | Interaction-style acquisition -> fresh structured snapshot -> `browser.assess_goal` -> one independently approved `browser.type` or `browser.select` -> settle -> fresh higher page-generation snapshot -> draft verification; repeat within the five-action bound, optionally inspect owner-requested visual state, then visibly present and verify the draft. | The draft stage exposes no click or submit-capable tool. Each approval binds one field, operation, exact value, snapshot digest, page ID, and session/page generation; freshness and field safety are checked before approval and again at execution. Password, passkey, token, OTP, captcha, payment, purchase, delete, upload, submit, send, and publish controls are rejected. Values are redacted in approval summaries and persisted browser projections. |
| `document.read` r4 | Read, summarize, or extract verbatim in-image text from one exact governed workspace file resolved from the current request or recent document records. Supported detection covers text, DOCX, XLSX, PPTX, PDF, and images. | Recent-document resolution and deterministic path/type preflight -> persisted `confirm_document_target` evidence -> one Runtime-invoked `files.read`, `pdf.extract_text`, or `images.inspect` call according to the frozen format -> Fast finalization over completed evidence. `images.inspect` runs optional OCR beside Fast visual semantics and classifies text/no-text explicitly; scanned PDF pages use the same bounded OCR adapter. | The single format-qualified reader is `direct_once`; the model does not decide whether to call it. OCR remains untrusted evidence, not a model lane or separate Workflow. Disabled or failed OCR degrades explicitly to Fast visual evidence, and text-free images omit OCR field noise. A unique recent document may satisfy a follow-up without another attachment. The path must resolve inside the workspace to one regular non-symlink file. File search, multiple-file comparison, mutation, and arbitrary external paths are outside this profile. |
| `document.edit` r5 | Apply one supported bounded edit to one governed text, DOCX, XLSX, PPTX, or PDF file and write one or more traceable output copies. | Deterministic path/type preflight -> `confirm_document_target` -> one Runtime-invoked format-qualified read in `document_locate_evidence` -> explicit retry-bounded `select_edit_operation` decision -> `document_edit` with only the persisted format/operation entry materialized. The default bound output is `<name>-sparkclaw-edit.<ext>`; existing and follow-up edited copies advance the suffix to `-2`, `-3`, and so on. | All model-driven document stages use Fast. Localization never asks a model whether to call the reader and never repeats the read. Multi-candidate operation selection runs only after evidence exists; single-candidate formats are deterministic. Missing or invalid decisions block. Reversible writes require approval and every output is linked to its parent and activity. A unique latest output is only bound when the current request selects a document Workflow; unrelated turns do not inherit it. |
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
