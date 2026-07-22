# Workflow Capability Matrix

> Language: English | [简体中文](../zh-cn/docs/workflow-capabilities.md)

This document is the user-visible capability inventory for the Workflow runtime.
It describes only registered Workflow Profiles that can currently be selected
and completed. A ToolHub tool being registered does not by itself make a feature
available.

## Shared Execution Contract

- Every owner request is normalized and routed on the Fast model lane.
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
- A browser, weather, or document request outside the current profile revision
  is changed from `unmatched` to `blocked`; it cannot recover through legacy
  ReAct. Shared file-read tools remain available only to genuinely unmigrated
  domains such as code inspection.

## Current Profiles

| Capability / profile | What it can do | Fixed execution path | Current boundary |
|---|---|---|---|
| `browser.internet_search` r1 | Search public, current Internet information from a frozen query. Weather warnings, news, official-source discovery, comparison, and air-quality research also use this general search path. | One stage: `web.search`, currently backed by Infinimesh Info when enabled. | It does not open result pages, read a known URL, operate a signed-in browser, or turn a direct weather request into a card. The provider must be configured and return a bounded result. |
| `browser.weather` r1 | Produce one weather PNG for an explicit location. The card uses supported current condition and current temperature, optional same-day low/high temperature, and zero to five available future hourly condition/temperature entries. | `info.query` -> `weather.structure_payload` -> `media.render_weather_card`. Only one stage tool is visible at a time. | Info is the only weather-data source. Every value needs an exact evidence ref and source substring. Missing current condition, current temperature, daily range, or hourly data must be represented in `missing_fields`; no value is inferred. Text-only weather, missing location, alerts/news/source comparison, and AQI are outside this profile. |
| `browser.automation` r1 | Open exactly one explicit HTTP(S) URL or one runtime-registered destination, or focus an existing tab whose normalized URL is an exact match. The initial registry contains QQ Mail (`https://mail.qq.com/`). | `browser.list_tabs` -> `browser.focus` when an exact tab exists, otherwise `browser.open`. Registered names resolve to frozen runtime URLs rather than model-supplied values. | This profile remains open/focus only. Page interaction belongs to `browser.interaction`; type, select, screenshots, page reading, login/authentication, and multi-URL work remain outside this revision. Unknown names still require an explicit URL. |
| `browser.interaction` r1 | Inspect one managed current tab, one explicit HTTP(S) URL, or a registered destination with a page subgoal, and perform up to three verified clicks. A usable current/exact/blank tab is reused before opening a new tab. | `browser.status` -> `browser.list_tabs` -> focus/navigate/open -> structured `browser.snapshot` -> `browser.click` -> post-click snapshot -> `browser.verify`; when this Workflow opened a new tab, success continues to `browser.close`. Verified progress may repeat the click loop. Tool Exposure persists the fixed ten-tool boundary while each model turn sees only the active stage subset. | Clicks and cleanup of the Workflow-owned tab do not require approval, but every argument is bound to persisted refs. Reused user tabs remain open. Repeated state fails immediately and a third progress verdict fails with `interaction_attempt_limit`. Type, select, upload/download, login/authentication, credentials, captcha/2FA, payment/purchase, form submission, screenshots, arbitrary script execution, and unsafe consequential actions are outside r1. |
| `document.read` r1 | Read or summarize one exact governed workspace file. Supported detection covers text, DOCX, XLSX, PPTX, and PDF. | Deterministic path/type preflight, then `files.read` for text/Office or `pdf.extract_text` for PDF. | The path must resolve inside the configured workspace to one existing regular non-symlink file. Extension and file signature/package type must agree. File search, multiple-file comparison, mutation, and arbitrary external paths are not part of this profile. |
| `document.edit` r2 | Apply one supported bounded edit to one governed DOCX, XLSX, PPTX, or PDF and write a new sibling output copy. | Deterministic path/type preflight and structured read, then exactly one format- and operation-qualified editor. The bound output is `<name>-sparkclaw-edit.<ext>`. | The input must pass the same workspace checks as document read, the output must not already exist, and the reversible tool action requires approval. New document creation, generic file deletion, multi-file edits, and unsupported operations do not enter this profile. |
| `schedule.manage` r1 | Create, list, update, or cancel scheduled tasks from conversation. | The route operation materializes exactly one existing Reminder tool. Persistence remains `ScheduleSpec` -> `ScheduleRegistry`; execution remains Timer workers -> Delivery Gateway. | Management requires an explicit scheduled-task request. The WebChat status bar is read-only and returns only the current principal's active `pending`/`sending` schedules. |

## Document Edit Operations

| Format | Supported revision 1 operations |
|---|---|
| DOCX | `replace_paragraph`, `insert_paragraph`, `delete_paragraph`, `set_text_style` |
| XLSX | `update_cell`, `insert_row`, `delete_row`, `update_row`, `append_row` |
| PPTX | `add_slide`, `duplicate_slide`, `delete_slide`, `replace_text` |
| PDF | `extract_pages`, `delete_pages`, `rotate_pages`, `split` |

## Transitional Features

Code/command assistance, image inspection, memory, and other
unmigrated domains still use the transitional TaskHint/ReAct path. Their legacy
Skills remain temporarily. The migrated browser, public search, weather, and
document Skill packages have been removed, and those capabilities cannot be
recreated by legacy Skill candidates. Registered tools that are not listed in a
current Workflow remain in ToolHub for later migration but are not advertised as
available Workflow features. Scheduled-task management has migrated to
`schedule.manage`; it no longer relies on the transitional route.
