# Issue #20 God-File Split Design

> Language: English | [简体中文](../zh-cn/docs/issue-20-god-file-split-design.md)

> Status: implemented on local `main` and locally validated for
> [issue #20](https://github.com/Infinimesh-ai/SparkClaw/issues/20), based on
> the `70e30f0` baseline. The issue has no comments. On 2026-08-18 the owner
> accepted the full CSS split, two-batch delivery, and default-test i18n lint.

## Decision Summary

Issue #20 was implemented as five behavior-preserving, independently
reviewable refactors. The public React import path, rendered markup, CSS cascade,
translation lookup API, Go test names, ToolHub adapter request/result contract,
and Python error projection remain unchanged.

The WebChat panels become focused feature modules behind the existing
`components/panels` import path. Binding polling and repeated async busy guards
move into tested hooks without merging currently independent concurrency
domains. CSS is split into ordered area files, with all responsive overrides
remaining last. English becomes the compile-time translation shape authority;
Chinese must satisfy that exact shape, and a TypeScript-AST lint rejects unused
production keys.

The Go schema test file is split mechanically by feature and document format.
The PPTX slide adapter becomes a real embedded Python package. Go materializes
that trusted package into an invocation-scoped temporary directory, executes it
through the existing bounded subprocess path, and removes it after the call.
Python standard-library unit tests cover extracted pure behavior while the
existing Go integration tests continue to cover the executable adapter contract.

## Pre-Implementation Baseline

The target-file line counts had already drifted upward after the issue was filed:

| File | Issue count | Current count | Main concern |
|---|---:|---:|---|
| `apps/webchat/src/components/panels.tsx` | 1,447 | 1,524 | 15 exported components; settings owns polling and multiple action domains |
| `apps/webchat/src/styles/app.css` | 3,532 | 3,623 | global cascade, no ownership sections, responsive overrides concentrated at the end |
| `apps/webchat/src/i18n.ts` | 878 | 900 | two manually mirrored dictionaries separated by about 440 lines |
| `services/gateway/internal/toolhub/schema_test.go` | 2,248 | 2,278 | unrelated schema, image, weather, DOCX, XLSX, PPTX, and PDF tests share one file |
| `services/gateway/internal/toolhub/scripts/pptx_slide.py` | 905 | 905 | layout, text mutation, slide cloning, dispatch, and CLI error projection share one module |

Existing regression anchors include `panels.test.tsx`,
`panels.polling.test.tsx`, `externalMCPSettings.test.tsx`, the full WebChat test
suite, and extensive PPTX mutation tests currently located in `schema_test.go`.
The production PPTX adapter is presently embedded as one string and executed as
`python3 -c`, so a package split requires a new multi-file execution path rather
than only moving Python functions.

## Goals

- Keep each production source file comfortably below the 800-line soft limit.
- Make component, style, translation, test, and Python-module ownership obvious.
- Preserve all current public and runtime behavior.
- Preserve current UI action concurrency and error-display behavior.
- Enforce exact English/Chinese translation parity at compile time.
- Make unused translation keys fail the normal WebChat validation path.
- Run focused Python unit tests through a command that CI already executes.
- Keep PPTX subprocess timeout, stdin/stdout JSON, error codes, output-copy
  behavior, and cleanup guarantees unchanged.
- Make every sub-refactor independently reviewable and revertible.

## Non-Goals

- Redesigning the inspector, settings UI, or responsive layout.
- Renaming CSS classes, component exports, translation keys, test functions, or
  ToolHub operations.
- Consolidating repeated CSS selectors during the mechanical split.
- Changing connector polling intervals, cancellation, retry, or error semantics.
- Adding a new frontend state-management, CSS-module, CSS-in-JS, Python test, or
  Python packaging dependency.
- Changing PPTX layout heuristics or adding PPTX features.
- Changing Gateway APIs, Store contracts, Workflow behavior, or user-visible copy.

## Invariants

1. `import { SettingsPanel, ... } from "./panels"` remains valid.
2. Component props and exported symbol names remain unchanged.
3. Existing class names and the final CSS rule order remain unchanged.
4. `dictionaries[language]`, `Copy`, `Language`, `initialLanguage`, and
   `LANGUAGE_STORAGE_KEY` remain the public i18n API.
5. A busy domain blocks exactly the actions it blocks today; independent domains
   remain independently runnable.
6. Binding polling starts after 1 second, repeats after 2 seconds while pending,
   backs off for 4 seconds after failure, survives parent callback identity
   changes, and aborts its active request on cleanup.
7. PPTX adapter input remains one JSON object on stdin and output remains one JSON
   object on stdout. Adapter-reported business failures continue to exit through
   JSON so Go can retain typed `pptx_layout_fit_conflict` mapping.
8. The existing 60-second fallback timeout and caller cancellation still own
   every Python subprocess.

## WebChat Panel Modules

`components/panels.tsx` becomes a small compatibility barrel. Implementation
files live below `components/panels/`:

```text
components/
  panels.ts
  panels/
    approvals.tsx
    memory.tsx
    primitives.tsx
    settings.tsx
    status.tsx
    timeline.tsx
    trace.tsx
```

The barrel re-exports the same 15 public components. `primitives.tsx` owns only
`SectionHeader`, `JsonBlock`, and `RiskPill`. `status.tsx` groups Status,
Artifact, Episode, and Eval because they are assembled as one current status
stack. Workspace approval helpers remain private to `approvals.tsx`; connector
status rendering remains private to `settings.tsx`.

The split is first performed as exact moves with imports adjusted by TypeScript.
No JSX, copy, class, ordering, or condition changes are mixed into that move.
Existing consumer imports in `inspector.tsx` and tests do not change.

### `useBindingPolling`

`hooks/useBindingPolling.ts` owns the complete timer/ref/abort lifecycle. Its
input contract is the pending binding key, the refresh callback, the localized
fallback error, and an error sink. It continues to use the stable serialized ID
set from `pendingBindingPollKey`; the latest refresh callback is held in a ref so
normal parent rerenders do not restart a long poll.

The existing `panels.polling.test.tsx` retains the Settings integration case for
rerender and abort behavior. `useBindingPolling.test.tsx` adds direct timing
cases for successful continuation, terminal completion, and rejected-refresh
backoff. The hook does not fetch connector or binding lists itself and therefore
does not create a second state authority.

### `useAsyncAction`

`hooks/useAsyncAction.ts` extracts the `externalMCPSettings.run` contract:

- one hook instance owns one single-flight domain;
- `run(action, task)` records the current action token, clears the configured
  local error, catches through the configured error mapper/sink, and always
  clears busy state while mounted;
- the returned action token supports both boolean disabled states and exact row
  spinners; and
- unmount prevents late state writes but cancellation remains the task owner's
  responsibility.

Settings keeps separate hook instances for owner save, policy save, client
revoke, binding actions, and connector toggle. This replaces the eight repeated
handlers without turning those five current domains into one global lock.
`ExternalMCPSettings` adopts the same hook. Callers that intentionally surface
errors elsewhere may use a no-op local error sink; errors are not silently
reclassified.

Focused hook tests cover success, rejection mapping, duplicate invocation,
action-token identity, and unmount. Existing component tests remain as contract
tests for rendered disabled/spinner and error states.

## CSS Split

`styles/app.css` becomes an ordered import manifest. The exact names may be
adjusted during the mechanical move, but the ownership is:

```text
styles/
  app.css
  foundation.css
  shell.css
  notifications.css
  schedules.css
  conversation.css
  composer.css
  inspector.css
  settings.css
  responsive.css
```

Rules move in contiguous original-order blocks. `responsive.css` remains the
last import and retains the current `1280px`, `900px`, and `480px` blocks in
their existing internal order. Cross-area grouped selectors stay together in
the file of their first/base declaration for this pass. No selector
deduplication, specificity change, shorthand rewrite, or media-rule relocation
is combined with the split.

Validation compares the pre/post Vite CSS bundle rule order and size, then uses
desktop and mobile screenshots for the main chat, inspector, settings,
approval, connector-binding, and External MCP states. A rule-order comparison is
the primary no-behavior-change proof; equal source line counts alone are not.

## Translation Modules And Lint

The public facade stays at `src/i18n.ts`, backed by:

```text
src/i18n.ts
src/i18n/en.ts
src/i18n/zh.ts
```

`en.ts` exports the English object with normal widened string values. `zh.ts`
declares its object with `satisfies typeof en`, making missing, extra, or wrongly
shaped keys TypeScript errors without requiring Chinese strings to equal English
literal values. The facade exports `Copy = typeof en`, builds the typed
`dictionaries`, and retains language persistence helpers.

`scripts/check-i18n-usage.mjs` uses the already-declared TypeScript compiler API
and the WebChat tsconfig. It resolves property symbols back to English dictionary
leaf declarations instead of searching source text. Production `.ts`/`.tsx`
files are scanned; translation declarations and test files are excluded.
Computed access to a typed subtree, such as `text.risk[key]`, marks that subtree
as intentionally consumed. Every unused English leaf is printed as a stable
dotted path and causes a nonzero exit.

`npm run lint:i18n` exposes the check, and the default WebChat `test` command
runs it before Vitest so existing CI cannot bypass it. Its first run found 12
English leaves with matching unused Chinese leaves; those dead pairs were
deleted without changing any production-consumed copy.

## Go Test Split

`schema_test.go` is split by moving complete test functions and helpers without
editing assertions:

```text
schema_test.go
image_schema_test.go
weather_schema_test.go
files_read_schema_test.go
document_docx_schema_test.go
document_pptx_schema_test.go
document_xlsx_schema_test.go
document_pdf_schema_test.go
document_schema_helpers_test.go
```

The exact helper file count may shrink if a helper has only one format caller.
Shared fixture helpers stay in the same `toolhub` test package and use format
names rather than a generic dumping ground. Existing `document_*_test.go` files
are not enlarged past the soft limit merely to reduce file count.

Test names, package name, fixtures, subprocess calls, and assertions remain
unchanged except for `gofmt` import grouping. The sorted test-name sets from
`go test -list . ./internal/toolhub` must match exactly; raw order changes when
Go discovers declarations in the new file names.

## Embedded PPTX Package

The single script becomes:

```text
scripts/pptx_slide/
  __init__.py
  __main__.py
  clone.py
  constants.py
  errors.py
  layout.py
  slides.py
  text.py
  text_edit.py
  update.py
  tests/
    test_layout.py
    test_slides.py
    test_text.py
```

`text.py` owns measurement and normalized text, while `text_edit.py` owns
run-property copying, weighted replacement, exact-span replacement, and shape
rewriting. `layout.py` owns collision, band/card grouping, coordinated layout,
and post-layout checks. `slides.py` owns basic index and add/delete helpers;
`clone.py` owns move/clone/ref helpers and relationship copy. `update.py` owns
one-slide mutation orchestration. `__main__.py` retains the original operation
dispatch, dependency failure projection, stdin decoding, save, error mapping,
and stdout encoding.

Production files are embedded with `embed.FS`; test files are not embedded.
`runPythonPackageAdapter` creates an invocation-scoped temporary root, writes
only the trusted embedded package with restrictive file modes, runs
`python3 -m pptx_slide` from that root through `runSubprocessAdapter`, and
removes the root on success, adapter error, process error, or cancellation.
Concurrent calls never share a writable package directory.

The standard-library `unittest` suite directly exercises pure functions and
small fake shape/slide objects. A Go test invokes unittest discovery so
`go test ./internal/toolhub` remains the single CI gate after the documented
document-tool setup. Existing end-to-end PPTX tests are moved out of
`schema_test.go` but otherwise retained; they prove package materialization,
imports, JSON framing, python-pptx integration, file output, and typed errors.

## Delivery Sequence

1. Record WebChat tests/build artifacts and ToolHub test-name/package baseline.
2. Split panel modules only; verify WebChat tests/build and bundle size.
3. Extract and test `useBindingPolling`.
4. Extract and test `useAsyncAction`, preserving each busy domain.
5. Split CSS mechanically and verify compiled rule order plus desktop/mobile
   rendering.
6. Split i18n data, add parity typing, then add and gate the unused-key lint.
7. Split `schema_test.go` mechanically and prove the test list is unchanged.
8. Add the package runner, split `pptx_slide.py`, add Python unit tests, and run
   all focused PPTX/ToolHub tests.
9. Run full WebChat and Gateway validation, inspect the diff and worktree, and
   report bundle/test-list comparisons.

Each numbered behavior-neutral topic is a separate commit. Mechanical moves are
not combined with hook behavior or execution-runner changes.

## Validation

Before ToolHub baseline or final tests, run `npm run setup:document-tools`.
Required evidence is:

```bash
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build

cd services/gateway
go test ./internal/toolhub
go test ./...
go vet ./...
go build ./...
```

Also required:

- pre/post WebChat production CSS bundle size and ordered-rule comparison;
- focused hook timing/cancellation tests;
- exact pre/post sorted `go test -list . ./internal/toolhub` name-set comparison;
- Python unit-test execution through the ToolHub Go test;
- default file-backend validation because it is the product default;
- desktop/mobile screenshots for affected WebChat states;
- bilingual document mirror/link checks; and
- final `git status` inspection for document fixtures, temporary Python package
  files, screenshots, or other test artifacts.

The eval suite is not required because routing, Workflow, model, tool schema,
Policy, and user-visible behavior do not change. Any unexpected behavioral diff
stops the refactor and is handled as a separate defect change.

## Acceptance Criteria

- No touched production source file exceeds 800 lines without a new written
  justification.
- All existing component exports/imports and i18n public exports remain valid.
- Existing WebChat tests pass, and the new hooks/lint are covered and gated.
- English/Chinese parity and zero unused production translation leaves are
  mechanically enforced.
- Compiled CSS has equivalent ordered rules and affected desktop/mobile states
  have no visual diff beyond nondeterministic rendering noise.
- ToolHub exposes the exact same Go test-name set before and after the test split.
- PPTX adapter success and failure JSON, output files, layout checks, error code,
  cancellation, timeout, and cleanup remain unchanged.
- Python unit tests and all existing Go PPTX integration tests pass.
- Full Go build/test/vet and WebChat test/build are green.
- There are no unrelated changes or generated runtime artifacts.

## Owner Decisions

On 2026-08-18 the owner selected the recommended options:

1. CSS is split into ordered area files rather than receiving banners only.
2. WebChat and ToolHub/Python are validated as two batches within issue #20.
3. `lint:i18n` runs inside the default WebChat test gate.
