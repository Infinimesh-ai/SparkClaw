# Intent Routing Workflow Profile Catalog

> Language: English | [简体中文](../zh-cn/docs/intent-routing-workflow-domain-profiles.md)

Design status as of 2026-07-20: the current-stage target registers five leaves
under two branches: browser Internet search, single-location weather card,
browser automation, document read, and document edit. This is a registration
snapshot, not a fixed taxonomy.
Profiles and whole branches may be added later without changing Router control
flow. Earlier Web/workspace entries below are retained only as future or
transitional examples, not as the current target tree. The main plan remains
authoritative for intent, Policy, and persistence; the
[Tool Exposure Contract](intent-routing-tool-exposure-contract.md) is
authoritative for per-stage directory search and schema materialization.

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

## Current-stage Registered Tree

```text
capability
  browser
    internet_search -> browser.internet_search r1
    weather         -> browser.weather r1
    automation      -> browser.automation r1
  document
    read            -> document.read r1
    edit            -> document.edit r2
```

The tree is assembled through node and Workflow registrations. Tests validate
identity, parent-child edges, cycles, and leaf Workflow references; they do not
assert that these five leaves are the only legal registrations forever. Adding a branch changes
the registered catalog and decision corpus, not a Router switch.

At any instant, only one Workflow stage is active. Tool Exposure receives that
stage's scope and materializes only matching definitions. A transition clears
the prior Exposure view, advances `ScopeRevision`, and rejects calls from the
old view. Stage scopes never accumulate.

### Browser Internet Search: `browser.internet_search` r1

Intent: retrieve a read-only fact whose correct answer depends on current
Internet state and return the search result. This includes current gold prices,
exchange rates, stock or index quotes, immediate news, current match results,
and currently published schedules. These are examples inside one semantic leaf,
not separate vertical capabilities.

```text
stage search_info
  expose only: web.discovery
  materialize: web.search using the configured Infinimesh Info provider
  execute with the frozen query

results_available OR no_results
  -> return the typed search result
  -> complete

provider unavailable, timeout, invalid result
  -> blocked/failed with a typed reason
```

Revision 1 does not transition into `browser.read`, open a visible tab, or
perform browser automation. Those would require a later registered Workflow or
explicit composition.

Stable common knowledge that does not depend on current external state remains
`unmatched`; Fast must not force it online. Weather alerts, weather news,
historical weather research, and multi-location comparisons use this search
leaf. Search binds `fact_scope=current_internet_state` and freezes the current
owner message as the query before Workflow dispatch.

### Browser Weather Card: `browser.weather` r1

Intent: render current conditions or a short forecast for one explicit
location as the dedicated weather card.

```text
precondition
  fact_scope = weather_snapshot
  exactly one location is copied from and grounded in the current owner message

stage render_weather_card
  expose only: weather.card.render
  materialize: media.render_weather_card
  bind the exact frozen location

artifact_available
  -> return the PNG weather card
  -> complete

missing location, lookup failure, or missing artifact
  -> clarify or blocked/failed with a typed reason
```

This leaf does not cover weather alerts, news, historical research, or
multi-location comparisons; those depend on broader current Internet evidence
and route to `browser.internet_search`. It also does not expose `web.search`,
page reading, or browser interaction.

### Browser Automation: `browser.automation` r1

Intent: open/focus an explicitly known target URL in the managed browser.

```text
precondition
  target URL is deterministic and frozen; otherwise clarify

stage scan_tabs
  expose only: browser.list_tabs
  persist typed page IDs and normalized URLs

exact target URL exists
  -> stage focus_existing
  -> expose only: browser.focus bound to the matched page ID

exact target URL does not exist
  -> stage open_new
  -> expose only: browser.open bound to the frozen target URL

focus_completed OR open_completed
  -> return the browser result
  -> complete
```

Revision 1 does not expose navigate, click, type, select, page read, or Web
search. Later browser branches or Workflow revisions can add those behaviors
through registration without modifying the current Router algorithm.

### Document Read: `document.read` r1

Intent: inspect the contents of one governed document or image and return the
result.

```text
preflight inspect_type
  resolve the exact governed path
  detect extension/MIME/signature and reject mismatches
  record source size and reject/defer unavailable strategies explicitly
  expose no Agent tool (or only a future registered type-inspector)

stage read_by_type
  expose only the reader compatible with the detected type and exact path
  plain text/code -> bounded file reader
  DOCX/XLSX/PPTX -> format-compatible document reader backend
  PDF -> PDF text reader
  PNG/JPEG/GIF/WebP -> Fast multimodal image reader
  document reader -> complete parse -> stable structured_document_v1 locations
  image reader -> bounded semantic summary with dimensions and model provenance

content_available
  -> return typed content and references
  -> complete
```

The read stage never exposes editors or readers for unrelated formats. Direct
image analysis stays in `document.read` r1: image signature preflight selects
only `images.inspect`, which retains its 12 MiB source limit and Fast-only
model policy. The 8 MiB/200,000-byte `small_file_v1` limits continue to apply
to structured document readers.

No search, edit, image inspection, or unrelated format tool is visible in the
read stage. A missing path or unsupported type clarifies or blocks; it does not
widen exposure. The current small-file strategy accepts at most 8 MiB of source
data and 200,000 bytes of complete extracted content. Larger input returns
typed `strategy_deferred`, never a truncated success.

### Document Edit: `document.edit` r2

Intent: edit one governed document and return the new result.
All detected mutations of an existing document's content enter this profile;
explicit reads and summaries remain in `document.read`. Routing does not map
natural-language edit phrases to concrete operations. If the requested
format-specific operation has no registered editor, r2 still performs its
governed structured read and then blocks explicitly instead of returning a
read-only success or selecting an unrelated editor.

```text
deterministic preflight
  resolve one authoritative governed attachment path and output-copy path
  detect extension/MIME/signature and reject mismatches

stage read_for_edit
  expose only the reader matching the detected format and frozen input path
  parse the complete small document into structured_document_v1
  preserve stable locators such as document.p[25], sheet/cell, slide, and page

document_evidence_available
  replace the read scope with the detected-format edit scope
  select one exact operation-qualified editor entry from the request and evidence
  form bounded edit arguments from the structured observation

stage edit_by_type
  materialize only the selected editor matching the detected format and requested operation
  text/Markdown -> bounded text replacement entry
  DOCX -> compatible DOCX editor entries
  XLSX -> compatible XLSX editor entries
  PPTX -> compatible PPTX editor entries
  PDF -> compatible PDF transform entry
  Policy -> persist recoverable approval before reversible execution
  editor re-inspects -> reads -> structures -> locates -> constrains
  structural row/slide targets -> one stable entity, not all child blocks
  apply only to non-existing output copies
  validate each typed output -> re-hash the frozen input -> clean up on failure

edit_completed
  -> return every typed output artifact and operation result
  -> complete
```

Revision 2 returns an auditable `change_summary`, writes one or more output
copies, and stops only after every typed output exists and the input hash is
unchanged. The unified `WorkflowResult` carries both `change_summary` and the
new file. Missing, ambiguous, and unexpected target counts block before
approval or mutation. A separate
verification stage may be registered in a later revision; it is not silently
inserted now. Tools for other formats or operations remain invisible.

## Legacy Context Assembly Boundary

These Workflows reuse the existing context assembler for conversation history,
owner context, current user text, attachments, and compact context formatting.
This phase does not introduce a new context graph, reducer, or per-Workflow
prompt-assembly system. Route identity, active stage, frozen resources, and
Exposure bindings are added around the legacy assembled context.

Legacy context may supply evidence, but its candidate-tool and Skill lists are
not a visibility authority. Only the active stage's Tool Exposure view reaches
the Agent.

## Future Expansion Matrix (Not Current Registrations)

| Domain | Representative goal | Adaptive state |
|---|---|---|
| Conversation | direct answer | no tool directory |
| Web | discover, verify, answer | evidence depth, source URLs, citations |
| Browser | read, open, inspect, interact | presentation, tab, structure, login handoff |
| Files | find, read, answer | workspace root and governed paths |
| Document | read, locate, edit, verify | format, anchors, output copy, coverage |
| Image | inspect or transform | media source, artifact, output modality |
| Memory | search or propose | sensitivity, candidate review |
| Reminder | create/list/update/cancel | due time, timezone, channel, binding |
| Code/Command | inspect, patch, execute | evidence, sandbox, rollback, approval |

## Future Or Transitional Profile Examples

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
  -> add document.modify with the exact format qualifier
  -> directory selection chooses one operation-qualified edit description

edit_completed
  -> add document.read for the output copy

output_verified
  -> complete
```

An XLSX cell edit cannot retrieve DOCX, PPTX, or PDF mutation entries. Insert,
delete, append, row, and cell requests share this same profile and differ only
at post-read directory selection. Uploaded originals remain immutable.


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
