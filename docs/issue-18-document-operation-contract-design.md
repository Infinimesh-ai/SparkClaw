# Issue #18 Document Operation Contract Design

> Language: English | [简体中文](../zh-cn/docs/issue-18-document-operation-contract-design.md)

> Status: implemented and validated for
> [issue #18](https://github.com/Infinimesh-ai/SparkClaw/issues/18), based on
> `main` at `74f561c`. The issue has no comments. Its timeline contains one
> reference from `096f1cf`, and the later `d3e4245` commit already fixes the
> alias-aware deterministic error-wrapper lookup. On 2026-08-18 the owner
> confirmed all product boundaries and confirmed that no non-terminal document
> edit using the old argument contract exists in the current deployment.

## Decision Summary

SparkClaw will own one ordered document format-to-operation catalog in
`internal/app`. ToolHub providers, document lifecycle policies, and Agent
routing policies will derive their operation key sets and order from that
catalog while retaining their package-specific behavior. Each local registry
constructor must reject a missing implementation, an extra implementation, an
unknown format, or a duplicate operation. Tests will compare exact sets rather
than checking only that one registry is a subset of another.

`document.EditRequest.SourceSHA256` will be the only typed whole-source hash
hook, and `document.Pipeline.Edit` will be the only component that compares it
with freshly inspected source metadata. ToolHub format providers will stop
performing their own whole-source hash comparisons. Agent remains responsible
for proving that a runtime-bound hash came from the current Workflow
localization observation; that provenance check is distinct from filesystem
freshness enforcement.

The public argument name is `source_sha256`, defined once in `internal/app`.
`source_evidence` and `evidence_targets` are Agent-runtime-only provenance, not
advertised ToolHub input fields. Source SHA-256 is required for DOCX, XLSX, and
PPTX operations only. The update is a hard contract cutover with no legacy
decoder or persisted-state migration.

## Pre-Implementation Gap

The same executable operation matrix is currently repeated in four places:

- ToolHub providers in `document_{docx,xlsx,pptx,pdf}_registry.go` and the text
  provider in `document_format_registry.go`;
- document lifecycle policies in `document/format_policy.go`;
- Agent routing policies in `agent/workflow_document_format_policy.go`; and
- literal expected maps in both the document and Agent registry tests.

The current one-way parity test proves only that every ToolHub provider
operation has a document preservation policy. It does not catch extra document
policies, Agent drift, ordering drift, or a format present in only one registry.
Runtime now fails closed for a missing document lifecycle policy because of
`096f1cf`, but drift can still reach runtime before being discovered.

Whole-source hash behavior is also inconsistent:

| Format/path | Argument | Current enforcement |
|---|---|---|
| DOCX | `source_document_sha256` | ToolHub validator compares inspected metadata; the provider does not populate `EditRequest.SourceSHA256` |
| XLSX | `source_sha256` | ToolHub checks presence, then `Pipeline.Edit` compares the provider-extracted hash |
| PPTX | `source_document_sha256` | ToolHub compares inspected metadata, then `Pipeline.Edit` compares it again |

The shared `office.replace_text` schema advertises both hash names. It also
advertises `source_evidence` and `evidence_targets`, although those fields are
interpreted only by Agent's Workflow binding policy. A direct ToolHub caller can
supply them, but ToolHub neither establishes nor validates their provenance.

Two related defects are already fixed and are not reimplemented here:

- `096f1cf` makes lifecycle policy misses fail closed and adds the existing
  ToolHub-to-document coverage test; and
- `d3e4245` makes `errorWrapper` honor `ToolAliases`, traverse formats in
  deterministic order, and avoid a second DOCX source-file inspection.

## Goals

- Define every supported editable `(format, operation)` pair exactly once.
- Preserve deterministic operation order for directory materialization and
  tests.
- Make all three registries fail during construction or tests when package
  behavior does not exactly cover the canonical catalog.
- Use one whole-source hash argument and one typed pipeline field.
- Check whole-source freshness once in `Pipeline.Edit` for every operation that
  requires localization evidence.
- Ensure every advertised ToolHub input has ToolHub-owned semantics and
  validation.
- Preserve existing operation selection, approval, output-copy, preservation,
  error mapping, and post-approval safety behavior.

## Non-Goals

- Adding document formats or edit operations.
- Combining the three behavior registries into a cross-package mega-registry.
- Moving parsers, editors, preservation hooks, routing prompts, or evidence
  projectors into `internal/app`.
- Changing document mutation approval policy.
- Extending source-hash requirements to plain-text replacement or PDF
  transforms unless separately approved.
- Removing target-level evidence such as paragraph, cell, row, sheet, shape, or
  page hashes.

## Canonical Catalog

### App Contract

`internal/app` will expose an immutable-by-copy, deterministically ordered
catalog. The concrete API may use equivalent names, but its contract is:

```go
type DocumentOperationSpec struct {
    Name                 string
    RequiresSourceSHA256 bool
}

type DocumentFormatOperationSpec struct {
    Format     string
    Operations []DocumentOperationSpec
}

func DocumentFormatOperationSpecs() []DocumentFormatOperationSpec
func DocumentOperationsForFormat(format string) ([]DocumentOperationSpec, bool)
func DocumentOperationFor(format, operation string) (DocumentOperationSpec, bool)
```

Returned slices must be copies so a consumer cannot mutate global authority.
Format and operation names are canonical lowercase wire values. Operation-name
constants live beside the catalog so consumers do not restate literals merely
to switch on behavior.

The initial catalog contains the current five executable pipeline formats:

| Format | Ordered operations | Source SHA-256 required |
|---|---|---|
| `text` | `replace_text` | No |
| `docx` | `replace_text`, `replace_paragraph`, `insert_paragraph`, `delete_paragraph`, `set_text_style` | Yes for every operation |
| `xlsx` | `replace_text`, `update_cell`, `insert_row`, `delete_row`, `update_row`, `append_row` | Yes for every operation |
| `pptx` | `replace_text`, `add_slide`, `update_slide`, `update_deck`, `duplicate_slide`, `delete_slide` | Yes for every operation |
| `pdf` | `extract_pages`, `delete_pages`, `rotate_pages`, `split` | No |

`image` remains an Agent routing format with no executable document edit
operation and is therefore outside this catalog. This avoids claiming that
ToolHub or the document lifecycle registry has an image editor.

### Consumer Construction

Each package retains ownership of its behavior and joins it to the app catalog
by canonical `(format, operation)` keys:

- ToolHub supplies parser, editor, target builder, tool name/aliases, result
  projection, and error mapping. `OperationOrder` is copied directly from the
  canonical catalog.
- `document` supplies normalization, lifecycle, preservation, and package
  verification hooks.
- Agent supplies route grounding, schema projection, evidence slicing,
  argument binding, approval revalidation, and result projection.

Each registry constructor performs an exact join:

1. Iterate the canonical formats and operations in order.
2. Resolve the package-owned behavior for each key.
3. Panic with the package and missing key if behavior is absent.
4. Reject local behavior keyed by a pair absent from the catalog.
5. Reject duplicate formats, duplicate operations, and empty normalized keys.

This keeps facts single-sourced without making `app` depend on implementation
packages or storing function hooks in shared contracts.

## Whole-Source Hash Contract

### Ownership

`internal/app` will define one public ToolHub argument-name constant. The
recommended value is:

```go
const DocumentSourceSHA256Argument = "source_sha256"
```

Tool definitions, Agent runtime-bound argument lists, binding code, approval
arguments, tests, and documentation use this constant or its wire value. A
ToolHub provider no longer owns a per-format `SourceSHA256 func(map[string]any)
string` extractor.

`executeDocumentOperation` copies the canonical argument into
`EditRequest.SourceSHA256` for every operation. `Pipeline.Edit` resolves the
canonical operation spec after inspecting the format, then:

1. rejects a missing hash when `RequiresSourceSHA256` is true;
2. rejects a non-empty mismatch with `CodeResourceInvalid` at
   `StageConstrain`; and
3. continues through the existing read, target localization, output-path
   validation, and apply stages.

The later re-inspection immediately before apply remains. It is a time-of-check
to time-of-use guard against a file changing during the current Pipeline call,
not a second validation of the Workflow-provided hash.

### Agent Provenance

Agent binds the canonical hash from the single completed localization read and
rejects a caller/model value that conflicts with that persisted observation.
This check proves provenance and freezes approval arguments; it does not inspect
the filesystem or replace `Pipeline.Edit` freshness enforcement.

Post-approval revalidation still checks exact approval arguments and any
target-level evidence needed to resume safely. A source file changed while
approval was pending must ultimately fail through the canonical pipeline hash
check before mutation. Format-specific code must not add another whole-file
hash comparison.

## Workflow-Only Evidence

`source_evidence` identifies the exact localization call, run, node, scope
revision, path, and operation. `evidence_targets` binds an Office text
replacement to target blocks. These are Agent provenance records, not inputs a
direct ToolHub caller can establish.

The recommended design is therefore:

- remove both fields from advertised ToolHub input-schema properties and
  required lists;
- keep them out of model-visible schema projections and directory contracts;
- let Agent derive or overwrite them from persisted Workflow state before
  approval;
- validate them only as Agent-owned provenance before execution/resume; and
- ensure ToolHub never grants extra authority or skips target validation based
  on either field.

Direct ToolHub and manual invocation then rely on self-defending ToolHub
contracts: canonical source hash, operation-specific target fields, target-level
hashes where required, exact locator resolution, output-copy boundaries, and
Pipeline preservation checks. Supplying an unknown provenance-shaped field has
no effect.

If the owner instead requires these fields to remain public, ToolHub must define
their complete schema, reconstruct their authoritative source from Store, and
reject forged, stale, cross-run, or incomplete values on every invocation. That
would couple ToolHub to Agent-owned Workflow provenance and is not recommended.

## Alias And Error Resolution

The `d3e4245` behavior remains the baseline: error lookup uses both primary tool
names and `ToolAliases` and traverses formats in stable order. Catalog
construction additionally rejects a duplicate `(accepted tool name, operation)`
error-wrapper match, so deterministic traversal is not used to conceal an
ambiguous registration.

`office.replace_text` remains a shared public tool for DOCX and XLSX and an
accepted PPTX alias. This issue does not rename tools or change capability
qualifiers.

## Failure Semantics

- Unknown catalog pair: fail at registry construction; persisted unknown pairs
  fail with `CodeMutationUnsupported` at runtime.
- Missing required source hash: `CodeResourceInvalid`, `StageConstrain`, before
  read/apply.
- Source hash mismatch: `CodeResourceInvalid`, `StageConstrain`, before
  read/apply.
- Workflow provenance mismatch: Agent blocks before approval or resume.
- Target evidence mismatch: existing format-specific typed failure remains.
- Ambiguous alias registration: fail during ToolHub registry construction.

Raw internal diagnostics remain in audit; existing stable ToolHub error mapping
continues to control public failures.

## Compatibility And Rollout

The owner confirmed that the current deployment contains no non-terminal
document edit, pending document approval, or resumable document ToolCall using
the old argument contract. Completed messages, runs, approvals, audit records,
artifacts, document records, and output files do not require migration.

The rollout is therefore a hard contract cutover:

1. Tool schemas, Agent binding, approval arguments, and direct ToolHub calls
   advertise, generate, and accept only `source_sha256`.
2. No `source_document_sha256` decoder or store migration is added.
3. If an unexpected non-terminal legacy document operation is encountered, it
   terminates with an explicit retired/unsupported-contract failure. It is not
   reinterpreted as a new operation.
4. Failure retains the existing run, approval, ToolCall, message, and audit
   history and never deletes or modifies a source or output document.
5. Completed historical records remain readable as history; they are not
   resumable execution input and are not rewritten.

## Verification

Focused tests must prove:

- the app catalog has stable order, returns defensive copies, and rejects
  duplicate/empty entries in its constructor test;
- ToolHub, document, and Agent operation sets each exactly equal the canonical
  catalog, including no extras;
- synthetic missing and extra package behavior panic with the exact key;
- `OperationOrder`, directory capabilities, definitions, editors, preservation
  policies, and Agent operation policies cover the same pairs;
- every required Office edit rejects a missing or stale canonical source hash
  through `Pipeline.Edit`;
- DOCX, XLSX, and PPTX no longer perform provider-owned whole-source hash
  comparisons;
- the shared `office.replace_text` schema advertises exactly one hash name and
  no Workflow-only provenance fields;
- direct ToolHub invocation cannot gain behavior by supplying
  `source_evidence` or `evidence_targets`;
- Workflow binding, approval persistence, rejection of conflicting model
  arguments, and post-approval resume remain correct for all three Office
  formats;
- PPTX-via-`office.replace_text` retains typed error mapping; and
- a synthetic non-terminal legacy operation fails consistently in memory,
  file, and PostgreSQL stores without deleting its history or touching files.

After focused tests, run the full Gateway build/test/vet gate, document-tool
setup and tests, golden evals for document routing/editing, default file-backend
validation, and bilingual docs checks described in the project SOP.

## Implementation Slices

1. Add the ordered app catalog, operation constants, lookup helpers, and tests.
2. Convert document lifecycle policy construction and its tests to exact
   catalog joins.
3. Convert ToolHub providers/order and parity tests, preserving aliases and
   directory metadata.
4. Convert Agent operation policies and exact-set tests.
5. Introduce the canonical hash argument, move required/mismatch enforcement to
   `Pipeline.Edit`, and delete provider-owned whole-source checks.
6. Apply the selected evidence-field and compatibility decisions.
7. Update current-state architecture/document workflow docs and run the full
   verification matrix.

Mechanical catalog consumption should be separate from hash/evidence behavior
changes in commit history.

## Confirmed Owner Decisions

The owner confirmed on 2026-08-18:

- the canonical public hash argument is `source_sha256`;
- `source_evidence` and `evidence_targets` are removed from public ToolHub
  schemas and remain Agent-runtime-only provenance; and
- source SHA-256 is required for DOCX, XLSX, and PPTX operations only; text and
  PDF behavior is unchanged by this issue; and
- the update uses a hard contract cutover with no legacy decoder because the
  current deployment has no non-terminal document edit using the old contract;
  unexpected legacy work fails explicitly while history and files are retained.

## Implementation Status

Implemented and validated on 2026-08-18. The canonical catalog, exact registry
joins, unified source-hash enforcement, Runtime-only provenance boundary, and
hard cutover are reflected in the current-state architecture and document
workflow guides. Full Gateway build/test/vet, WebChat tests/build, bilingual
docs checks, the default file backend, and 47 isolated golden cases passed.

## Open Decisions

None.
