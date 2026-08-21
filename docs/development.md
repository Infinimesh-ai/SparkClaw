# SparkClaw Development

> Language: English | [简体中文](../zh-cn/docs/development.md)

This guide is the contributor entry point. Read [Architecture](architecture.md)
for system ownership, [Workflow capabilities](workflow-capabilities.md) for the
shipped user surface, and the relevant component guide from the
[documentation index](index.md) before changing behavior.

## Repository Map

```text
apps/webchat/              React/Vite owner workbench
services/gateway/          Go Gateway and runtime packages
configs/                   Runtime and model configuration
docker/                    Compose files, images, and environment templates
scripts/                   Setup, doctor, eval, and model helpers
eval/golden/               Golden cases and fixtures
benchmarks/                Model endpoint evidence
packages/                  Portable protocol/policy/schema notes
tools/document-runtime/    Declared document adapter dependencies
docs/                      Current English documentation
zh-cn/                     Simplified Chinese documentation mirror
```

Inside `services/gateway/internal`, dependency direction is generally:

```text
app contracts
  <- capability / semanticrouting / messagecontrol / delivery / document
  <- modelrouter / toolhub / policy / store / adapters
  <- agent Workflow runtime
  <- gateway HTTP and cmd/sparkclaw assembly
```

Owner packages must not import Gateway handlers or WebChat concerns. Adapters
implement typed interfaces; they do not redefine domain records.

## Setup

The validated host baseline is Ubuntu ARM64 with Go 1.25, Node.js 26, npm 11,
Python 3.12, and Docker. `.nvmrc`, CI, and Docker builds use Node 26 so host and
container validation stay on the same major version.

Install Go, Node.js, npm, Python, and pip as host tools available on `PATH`.
The root npm workspace owns all Node dependencies, while
`setup:document-tools` installs Python document libraries into the host user's
standard site-packages directory. The repository does not use a `.tools`
runtime or platform-specific toolchain directory.

```bash
npm run setup:host
```

Rebuild and restart the current external-model/OCR/PostgreSQL development runtime:

```bash
npm run dev
```

To rebuild only one application container, use `npm run dev:gateway` or
`npm run dev:webchat`. These commands preserve the external/OCR/PostgreSQL
environment and verify Gateway readiness.

Direct host processes are retained only for isolated mock/file and Vite
debugging as `npm run dev:gateway:host` and `npm run dev:webchat:host`.
See [Deployment](deployment.md) for Compose, auth, state backends, DGX Spark
models, and operational environment variables.

## Standard Verification

Run checks proportional to the change. The normal full local gate is:

```bash
cd services/gateway && go test ./...
cd services/gateway && go vet ./...
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build
bash scripts/doctor.sh
```

Also run:

- `bash scripts/run-eval.sh` for routing, Workflow, model, tool, policy,
  delivery, or user-flow changes;
- `docker compose --env-file .env -f docker/compose.yaml config --quiet` for
  Compose/config changes;
- `npm run setup:browser` plus browser-focused tests/eval for browser transport
  or profile changes;
- the CI `docs` job rules locally or in CI for Markdown changes: every English
  document needs a `zh-cn/` mirror and all local links must resolve.

Use credential-gated live services only as additional evidence. Deterministic
tests must still cover unavailable, timeout, malformed, and authorization
failure paths.

## Change Workflow

1. Establish a clean behavioral baseline for the touched area.
2. Read the owning package, tests, config, and current component guide.
3. Identify one source of truth for every new fact or registry entry.
4. Change the smallest coherent owner boundary; avoid parallel compatibility
   paths unless persisted data explicitly requires one.
5. Add focused tests for success, failure, and contract boundaries.
6. Run the proportional verification matrix.
7. Update English and Chinese docs in the same change.
8. Inspect `git diff` for unrelated edits, generated churn, secrets, and stale
   names before handoff.

Follow the [Engineering baseline](engineering-baseline.md) for all work and the
[Refactoring playbook](refactor-playbook.md) for architecture cleanup.

## Capability And Workflow Changes

The current natural-language path is semantic graph -> embedding and Fast/Tree
scores for every eligible candidate -> weighted fusion -> Top-2 decision ->
deterministic route assembly. Do not add keyword fallback, a second capability
map, or a model-owned `RouteDecision`. See [Intent routing](intent-routing.md).

To add a user-facing capability:

1. Register its branch/leaf, route contract, operation, targets, and Workflow
   reference in `internal/capability`.
2. Implement exactly one versioned Workflow Profile for the leaf.
3. Register semantic variants with realistic examples, Tree distinctions, hard
   negatives, and source eligibility.
4. Add candidate-neutral deterministic grounding for required resources.
5. Register exact ToolHub capabilities with schemas, risk/effects, and outcome
   adapters.
6. Define Workflow nodes, transitions, argument bindings, completion evidence,
   retry bounds, and final projection.
7. Verify Catalog/profile/graph consistency, tool exposure, Policy/Approval,
   terminal failures, semantic confusion cases, and end-to-end delivery.
8. Update [Workflow capabilities](workflow-capabilities.md).

Only the active Workflow node exposes tools. A matched Workflow does not fall
back to a generic loop; the workflow step loop (see
[Workflow execution](workflow-execution.md)) is the only execution primitive.
Tools outside the matrix may remain registered for
future migration but are not advertised as current user features.

## Message, Schedule, And Delivery Changes

Use the shared contracts in `internal/app`, registries in
`internal/messagecontrol`, and Provider/Gateway code in `internal/delivery`.
Web is a registered delivery port, not a separate result path. Timer republishes
due content through Message Runtime and does not send directly.

Schedule edits/deletes must list pending owner-scoped records, resolve one
target, bind its current version, and mutate with compare-and-swap. Typed UI IDs
are hints, not permission to skip the fresh lookup. See
[Messaging and scheduling](messaging-and-scheduling.md).

A new connector must register its binding, delivery provider, optional inbound
runtime, capabilities, and shutdown. Gateway/Agent code must not branch on the
provider name.

## Browser Changes

The only execution backend is pinned agent-browser with system Chromium and a
SparkClaw-owned profile. Keep ToolHub contracts provider-neutral, process/profile
ownership bounded, page evidence untrusted, and click refs tied to fresh
snapshots. Do not restore Playwright, personal Chrome attachment, cookie export,
or a second DOM collector. See [Browser runtime](browser-runtime.md).

## Document Changes

Keep format inspection, high-level parsing, normalized evidence, context
projection, exact editor registration, approval, output-copy writes, and
post-edit preservation in one staged pipeline. New editors require exact
format/operation schemas and delta verification. See
[Document workflows](document-workflows.md).

Register each format or operation in the package that owns the fact:

1. Add ToolHub parser/editor execution, validation, locator construction,
   directory boundaries, and output projection to that format provider.
2. Add normalization, lifecycle, package verification, and preservation deltas
   to the `document` format policy.
3. Add route grounding, decision evidence/rules, argument binding/validation,
   schema materialization, and result evidence projection to the Agent format
   policy when orchestration differs.
4. Keep signature inspection centralized and use frozen capability `format` and
   `operation` qualifiers as the join. Do not import ToolHub implementation
   types into Agent or create a shared mega-registry.

Registration constructors must reject duplicate formats and operations. Add a
synthetic registration test and a shared-dispatch source guard when extending
the abstraction; prompt moves require an exact ordered snapshot before any
separate calibration change.

## WebChat Changes

Keep API transport in `apps/webchat/src/api/client.ts` and shared response/action
types in `apps/webchat/src/api/types.ts`. Gateway owns validation and public
projections. Add focused component/library tests for state logic and build the
full app. Inspect changed views at desktop and mobile sizes against a running
Gateway. See [WebChat](webchat.md).

## Store Changes

Use the typed interfaces in `internal/store/store.go`; do not recreate a broad
Store dependency. Runtime is limited to `cmd/sparkclaw` assembly and lifecycle.
Consumers receive only their required repositories.

Keep shared normalization, command, replay, and reconciliation semantics in
`<repository>_contract.go`. Implement storage in matching
`<repository>_memory.go`, `<repository>_file.go`, and
`<repository>_postgres.go` files. Update the File `Snapshot` for every new
snapshot-backed record. Backend construction and generic durability primitives
stay in the small root backend files.

Classify each operation P0, P1, or P2 before choosing verification. Every change
needs three-backend parity and explicit context/error behavior. Add transaction,
idempotency, unknown-outcome reconciliation, deterministic failure injection,
real PostgreSQL, and race evidence only when the operation's risk or aggregate
invariant requires it. The exact policy, lifecycle, and test commands are in
[Store](store.md).

## Models And Prompts

- Gateway selects model lanes; prompts do not self-route.
- Semantic routing weights and thresholds are an embedded calibration artifact.
  Change them only with labeled calibration/holdout evidence.
- Prompt changes need malformed-output, repair, injection, multilingual, and
  ambiguity coverage appropriate to their contract.
- Do not claim model quality from a smoke call. Record repeatable measurements
  in [Model baseline](../benchmarks/model_baseline.md).
- Loading/capacity changes follow [Model loading](model-loading.md).

## Configuration And Data Hygiene

- Add every setting to the typed config, defaults, environment override if
  needed, example env, public redacted projection, and tests.
- Never expose credentials, native recipient IDs, raw audio, sensitive file
  contents, or unredacted provider payloads in public config/logs/traces.
- Keep all subprocess and outbound calls bounded by timeout, size, concurrency,
  and owned cleanup.
- Preserve file and PostgreSQL Store parity for durable records.
- Use governed workspace/artifact refs instead of arbitrary host paths.

## Documentation Maintenance

Write current-state documentation, not a new permanent plan for every feature.
During implementation a design record may exist, but when work completes:

1. merge durable architecture decisions into `architecture.md` or the owning
   component guide;
2. merge commands into `deployment.md` and contributor steps here;
3. update the capability matrix and changelog where appropriate;
4. delete the completed plan and its Chinese mirror;
5. repair all inbound links and run the docs check.

This keeps the active docs small enough to be authoritative.
