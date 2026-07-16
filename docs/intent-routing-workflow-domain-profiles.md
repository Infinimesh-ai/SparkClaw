# Intent Routing Workflow Profile Catalog

> Language: English | [简体中文](../zh-cn/docs/intent-routing-workflow-domain-profiles.md)

Status as of 2026-07-16: `web.public_research`, `web.explicit_url_read`,
`workspace.file_search`, and `workspace.file_read` revision 1 are implemented.
Remaining entries are the ordered migration catalog for the architecture in
the [Intent Routing and Workflow Tool Exposure Refactor Plan](intent-routing-workflow-refactor-plan.md).
Profiles may be added or refined as decision-corpus and migration evidence
lands. The main plan remains authoritative for intent, Policy, and persistence;
the [Tool Exposure Contract](intent-routing-tool-exposure-contract.md) is
authoritative for directory search and schema materialization.

## Profile Registry And Composition

Each profile registers one supported objective pattern or one intentional
multi-objective composition. Registration fails tests when two profiles match
the same normalized intent at the same priority. Unsupported or ambiguous
combinations return typed clarification instead of being merged heuristically.
The registry routes the normalized Fast semantic envelope, then validates the
resolved plan identity, node graph, scopes, transitions, dependencies, and
argument bindings before persistence.

Composition is closed, not a runtime union. A composite profile must register
the exact objective pattern, assign every objective to one node, declare all
dependencies, and compute a single frozen transition closure. Generic fragments
may be reused in code only when their risk, actor, data scope, and completion
contracts agree. Otherwise a named composite profile or clarification is
required. Tool scopes from independently matched profiles are never unioned.

A profile does not prescribe concrete tool order. Every profile entry declares:

- the normalized intent pattern and node goal;
- the initial CapabilityScope searched by Tool Exposure;
- bounded ScopeTransitions activated by typed ToolOutcomes;
- allowed risks and denied effects;
- semantic completion rules and decision-corpus cases.

Tool Exposure searches the logical directory inside the current scope. One
clear entry may be materialized automatically; multiple plausible entries are
shown as compact descriptions for bounded selection. Complete ToolDefinitions
appear only after selection.

## Domain Matrix

| Domain | Representative goal | Adaptive state |
|---|---|---|
| Conversation | direct answer | no tool directory |
| Web | discover, verify, answer | evidence depth, source URLs, citations |
| Browser | read, open, inspect, interact | presentation, tab, structure, login handoff |
| Files | find, read, answer | workspace root and governed paths |
| Document | read, locate, edit, verify | format, anchors, output copy, coverage |
| Image/Weather | inspect or render | media source, artifact, output modality |
| Memory | search or propose | sensitivity, candidate review |
| Reminder | create/list/update/cancel | due time, timezone, channel, binding |
| Code/Command | inspect, patch, execute | evidence, sandbox, rollback, approval |

## Representative Profiles

### Public Web research

Intent pattern: `domain=web`, `operation=search`, no explicit URL.

```text
node goal: answer with sufficient public evidence
initial scope: web.discovery

directory search
  -> materialize web.discovery implementation
  -> ToolOutcome

evidence_sufficient
  -> complete

source evidence requested AND results contain a typed URL reference
  -> replace scope with web.page.read once
  -> search directory again
  -> materialize a page-read implementation

no results, missing typed URL, or capability unavailable
  -> explicit limitation or blocked
```

The profile does not always read a result page and does not forbid page reading.
It starts with discovery and permits page evidence only when the normalized
intent requested source depth. The page URL must exactly match a governed URL
reference persisted from discovery; arbitrary model-supplied URLs are blocked
before ToolHub execution. Live tab and interaction effects remain denied.

### Explicit URL reading

Intent pattern: `domain=web`, `operation=read`, target kind `explicit_url`.

```text
node goal: answer from the supplied page
initial scope: web.page.read

content_available
  -> complete

structure_required
  -> blocked in revision 1; a later profile revision may declare inspect/wait

authentication_required
  -> blocked
```

Because the URL is already known, `web.discovery` is not in the initial scope.
The execution URL must equal the deterministic explicit target in the frozen
intent. Page reading does not imply visible tab opening or page interaction.

### Workspace file search

Intent pattern: `domain=workspace`, `operation=search`, no explicit path.

```text
initial scope: workspace.file.search
results_available OR no_results -> complete
mutation, image, or code-specialized request -> no match
```

This profile covers explicit workspace searches and ordinary local find/search
requests that have no stronger public-Web or specialized-domain signal. It
materializes `files.search` through Tool Exposure and does not expose
`files.read` speculatively.

### Explicit workspace file read

Intent pattern: `domain=workspace`, `operation=read`, target kind
`workspace_path`.

```text
initial scope: workspace.file.read
content_available -> complete
missing or failed content -> blocked
```

The execution path must equal the deterministic target stored in the intent.
Image inspection and mutation requests do not match this profile. A successful
read cannot expand into editing without a separately registered, explicit
mutation profile.

### Browser open

Known URL:

```text
initial scope: browser.tab.open
open_completed -> complete
```

Named target without URL:

```text
initial scope: web.discovery
target_url_resolved AND original objective explicitly requested open
  -> add browser.tab.open
  -> search directory again
open_completed -> complete
```

Successful open does not enable redundant navigate, click, type, or select.
Browser status and tab listing remain Runtime preflight operations unless the
owner explicitly asked to inspect them.

### Browser interaction

Intent pattern requires an explicit interaction objective.

```text
initial scope: browser.page.inspect
target_ref_resolved
  -> add only the requested browser.interact qualifier
page_changed OR stale_ref
  -> return to browser.page.inspect within retry budget
authentication_required
  -> waiting_human
```

A page-read or search objective cannot transition into interaction merely
because a clickable element appears in a ToolOutcome.

### Document edit

```text
node goal: modify a copy and verify the requested change
initial scope: document.read

format_and_anchor_resolved
  -> add document.modify with exact format and operation qualifiers
  -> directory selection chooses among only matching edit descriptions

edit_completed
  -> add document.read for the output copy

output_verified
  -> complete
```

An XLSX cell edit cannot retrieve DOCX, PPTX, or PDF mutation entries. Uploaded
originals remain immutable.


### Reminder

After required slots are resolved, the initial scope contains exactly one
semantic reminder effect: create, list, update, or cancel. Tool Directory
descriptions resolve the available implementation without exposing the other
CRUD operations. Low-level connector delivery remains internal.

### Code and command

Code inspection starts with workspace evidence capabilities. An explicit patch
objective may add `code.patch`; an explicit test or command objective may add
`command.sandbox.execute`. Loading `coding_helper` cannot add either directory
entry, and every materialized mutation remains approval-gated.
