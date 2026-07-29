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
- A matched Workflow failure is explicit. It never falls back to TaskHint, a
  generic fallback loop, or a different capability.
- An `unmatched` route terminates as `router.blocked`; it does not expose
  TaskHint candidates, tools, or a fallback executor.
- A browser, weather, or document request outside the current profile revision
  is changed from `unmatched` to `blocked`; there is no legacy fallback to
  recover through.

## Current Profiles

| Capability / profile | What it can do | Fixed execution path | Current boundary |
|---|---|---|---|
| `conversation.answer` r1 | Answer greetings, stable common knowledge, and simple explanations from the owner request and conversation context. | One no-tool Deep model answer recorded as `workflow_answer`. | It cannot use current Internet facts, workspace evidence, account data, tools, approvals, or actions. Those requests must match another registered capability. |
| `browser.internet_search` r1 | Search public, current Internet information from a frozen query. Weather warnings, news, official-source discovery, comparison, and air-quality research also use this general search path. | One stage: `web.search`, currently backed by Infinimesh Info when enabled. | It does not open result pages, read a known URL, operate a signed-in browser, or turn a direct weather request into a card. The provider must be configured and return a bounded result. |
| `browser.weather` r1 | Produce one weather PNG for an explicit location. The card uses supported current condition and current temperature, optional same-day low/high temperature, and zero to five available future hourly condition/temperature entries. | `info.query` -> `weather.structure_payload` -> `media.render_weather_card`. Only one stage tool is visible at a time. | Info is the only weather-data source. Every value needs an exact evidence ref and source substring. Missing current condition, current temperature, daily range, or hourly data must be represented in `missing_fields`; no value is inferred. Text-only weather, missing location, alerts/news/source comparison, and AQI are outside this profile. |
| `browser.automation` r2 | Acquire exactly one explicit HTTP(S) URL or runtime-registered destination, prove it with hidden and visible snapshots, and leave the verified visible result open. Explicit URLs require normalized equality; registered destinations may also match a registry-bounded host or true subdomain after a site redirect. The initial registry contains QQ Mail (`https://mail.qq.com/`). | Passive `browser.status` -> `browser.list_tabs` -> focus/navigate/open -> settle -> hidden snapshot and route validation -> visible open/focus -> settle -> visible snapshot and route validation. Structural stages are Runtime-invoked and every navigation receives a new settled snapshot. | Unrelated tabs are never reused. A reusable initialized profile opens the target directly without exposing `about:blank` or asking for login again. The run cannot succeed before visible validation, and production never closes the result tab. Clicks, typing, forms, screenshots, arbitrary page reads, and multi-URL work remain outside this profile. Browser r1 is retired. |
| `browser.interaction` r2 | Inspect one managed current tab, explicit HTTP(S) URL, or registered destination with a page subgoal, then perform up to three independently verified, ref-bound clicks and leave the verified visible result open. A qualifying target tab is focused first; one selected blank tab is only a fallback when no target matches. | The automation r2 acquisition chain -> hidden `browser.assess_goal` -> bounded `browser.click` -> settle -> fresh snapshot -> `browser.validate_transition` -> `browser.assess_goal`; progress may repeat the action loop. Completion then runs visible open/focus -> settle -> fresh snapshot -> a second `browser.assess_goal`. The persisted ten-capability boundary is projected to the active stage. | Every argument is bound to persisted generation-scoped refs. Stale refs/generations, repeated state, route divergence, transition failure, or a third progress verdict fail closed. Login and human verification pause in a durable owner handoff; ambiguous replies do no browser work, and resume requires matching visible authentication/task evidence followed by fresh hidden evidence. Type, select, upload/download, credentials, captcha/2FA, payment/purchase, form submission, screenshots, arbitrary scripts, and unsafe consequential actions remain outside r2. |
| `document.read` r2 | Read or summarize one exact governed workspace file resolved from the current request or recent document records. Supported detection covers text, DOCX, XLSX, PPTX, PDF, and images. | Recent-document resolution and deterministic path/type preflight -> persisted `confirm_document_target` evidence -> `files.read`, `pdf.extract_text`, or `images.inspect` according to the frozen format. | A unique recent document may satisfy a follow-up without another attachment. Durable identity, source, and activity metadata are required; exact parsed-result persistence is not. The path must resolve inside the configured workspace to one existing regular non-symlink file. File search, multiple-file comparison, mutation, and arbitrary external paths are outside this profile. |
| `document.edit` r5 | Apply one supported bounded edit to one governed text, DOCX, XLSX, PPTX, or PDF file and write one or more traceable output copies. | Deterministic path/type preflight -> `confirm_document_target` -> one Runtime-invoked format-qualified read in `document_locate_evidence` -> explicit retry-bounded `select_edit_operation` decision -> `document_edit` with only the persisted format/operation entry materialized. The default bound output is `<name>-sparkclaw-edit.<ext>`; existing and follow-up edited copies advance the suffix to `-2`, `-3`, and so on. | Localization never asks a model whether to call the reader and never repeats the read. Multi-candidate operation selection runs on Deep only after evidence exists; single-candidate formats are deterministic. Missing or invalid decisions block. Reversible writes require approval and every output is linked to its parent and activity. A unique latest output is only bound when the current request selects a document Workflow; unrelated turns do not inherit it. |
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
