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
- Every model call after a Workflow is selected uses the Deep model lane.
- Conversation context, attachments, ToolResult messages, observation ordering,
  compaction, grounding, and channel delivery continue to use the shared runtime
  pipeline.
- Workflow plans contain no Skill IDs and Workflow execution loads no Skill text.
  The versioned profile, active capability scope, argument bindings, ToolHub
  metadata, and Policy are the complete execution boundary.
- Bound queries, URLs, workspace paths, locations, output paths, and outcome refs
  are materialized from persisted state. A model cannot replace them during a
  later stage.
- A matched Workflow failure is explicit. It never falls back to TaskHint/ReAct
  or a different capability.
- An `unmatched` route terminates as `router.blocked`; it does not expose
  TaskHint candidates, tools, or ReAct.
- A browser, weather, or document request outside the current profile revision
  is changed from `unmatched` to `blocked`; it cannot recover through legacy
  ReAct.

## Current Profiles

| Capability / profile | What it can do | Fixed execution path | Current boundary |
|---|---|---|---|
| `conversation.answer` r1 | Answer greetings, stable common knowledge, and simple explanations from the owner request and conversation context. | One no-tool Deep model answer recorded as `workflow_answer`. | It cannot use current Internet facts, workspace evidence, account data, tools, approvals, or actions. Those requests must match another registered capability. |
| `browser.internet_search` r1 | Search public, current Internet information from a frozen query. Weather warnings, news, official-source discovery, comparison, and air-quality research also use this general search path. | One stage: `web.search`, currently backed by Infinimesh Info when enabled. | It does not open result pages, read a known URL, operate a signed-in browser, or turn a direct weather request into a card. The provider must be configured and return a bounded result. |
| `browser.weather` r1 | Produce one weather PNG for an explicit location. The card uses supported current condition and current temperature, optional same-day low/high temperature, and zero to five available future hourly condition/temperature entries. | `info.query` -> `weather.structure_payload` -> `media.render_weather_card`. Only one stage tool is visible at a time. | Info is the only weather-data source. Every value needs an exact evidence ref and source substring. Missing current condition, current temperature, daily range, or hourly data must be represented in `missing_fields`; no value is inferred. Text-only weather, missing location, alerts/news/source comparison, and AQI are outside this profile. |
| `browser.automation` r1 | Open exactly one explicit HTTP(S) URL or one runtime-registered destination, or focus an existing tab that matches the frozen target. Explicit URLs require normalized equality; registered destinations may also match a registry-bounded host or true subdomain after a site redirect. The initial registry contains QQ Mail (`https://mail.qq.com/`). | `browser.list_tabs` -> `browser.focus` when a qualifying target tab exists, otherwise `browser.open`. Registered names resolve to frozen runtime URLs rather than model-supplied values. | This profile remains open/focus only. Unrelated existing tabs are never reused, and an explicit open remains available after completion. Page interaction belongs to `browser.interaction`; type, select, screenshots, page reading, login/authentication, and multi-URL work remain outside this revision. Unknown names still require an explicit URL. |
| `browser.interaction` r1 | Inspect one managed current tab, one explicit HTTP(S) URL, or a registered destination with a page subgoal, and perform up to three verified clicks. An already open qualifying target tab is focused first; a selected blank tab is only a fallback when no target matches. | `browser.status` -> `browser.list_tabs` -> focus/navigate/open -> structured `browser.snapshot` -> `browser.click` -> post-click snapshot -> `browser.verify`. Verified progress may repeat the click loop. Tool Exposure persists the fixed nine-tool boundary while each model turn sees only the active stage subset. | Every argument is bound to persisted refs. Explicit open/enter/visit results remain open after success; this Workflow never performs automatic `browser.close`. Unrelated existing tabs are not reused. Repeated state fails immediately and a third progress verdict fails with `interaction_attempt_limit`. Type, select, upload/download, login/authentication, credentials, captcha/2FA, payment/purchase, form submission, screenshots, arbitrary script execution, and unsafe consequential actions are outside r1. |
| `document.read` r2 | Read or summarize one exact governed workspace file resolved from the current request or recent document records. Supported detection covers text, DOCX, XLSX, PPTX, PDF, and images. | Recent-document resolution and deterministic path/type preflight -> persisted `confirm_document_target` evidence -> `files.read`, `pdf.extract_text`, or `images.inspect` according to the frozen format. | A unique recent document may satisfy a follow-up without another attachment. Durable identity, source, and activity metadata are required; exact parsed-result persistence is not. The path must resolve inside the configured workspace to one existing regular non-symlink file. File search, multiple-file comparison, mutation, and arbitrary external paths are outside this profile. |
| `document.edit` r4 | Apply one supported bounded edit to one governed text, DOCX, XLSX, PPTX, or PDF file and write one or more traceable output copies. | Deterministic path/type preflight -> `confirm_document_target` -> `document_locate_evidence` -> explicit retry-bounded `select_edit_operation` decision -> `document_edit` with only the persisted format/operation entry materialized. The default bound output is `<name>-sparkclaw-edit.<ext>`. | Multi-candidate operation selection runs on Deep with the located evidence; single-candidate formats are deterministic. Missing or invalid decisions block. The former Fast secondary router is removed, so every other multi-candidate scope also requires an explicit decision node. Reversible writes require approval and every output is linked to its parent and activity. |
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
registered Workflow terminate as `unmatched`. Legacy Skills and ReAct resume
code remain only for persisted-run compatibility. Registered tools outside the
current Workflow matrix remain in ToolHub for later migration but are not
advertised as available features.
