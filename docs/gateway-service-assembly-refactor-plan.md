# Gateway Service Assembly Refactor Plan

> Language: English | [简体中文](../zh-cn/docs/gateway-service-assembly-refactor-plan.md)

Status as of 2026-07-15: the `gatewayServices` composition wrapper is retained,
while its connector-specific assembly was superseded by the provider-neutral
[Connector Registry Refactor Plan](connector-registry-refactor-plan.md).

## Objective

Extract the optional-feature assembly in `cmd/sparkclaw` into one same-package
composition abstraction so that speech, connector credentials, Telegram,
Weixin, reminders, and Gateway options are wired through a single production
path.

The change must preserve all externally visible behavior and the current
architecture. It does not create a new service, package dependency, protocol,
configuration field, or runtime capability.

## Baseline

The following checks passed before implementation:

- `npm run setup:document-tools`
- `go build ./...` in `services/gateway`
- `go vet ./...` in `services/gateway`
- `go test ./...` in `services/gateway`
- `npm run build` in `apps/webchat`
- `npm run test:voice` in `apps/webchat` (6 tests)

The WebChat production bundle baseline is 298.91 kB JavaScript and 35.20 kB
CSS before gzip.

## Problem Statement

The composition root currently assembles related optional features directly in
[`main.go`](../services/gateway/cmd/sparkclaw/main.go):

- speech is adapted for Telegram voice messages;
- the connector credential vault is created and checked;
- the Telegram service supplies Gateway binding cancellation;
- the Telegram notification adapter is conditionally added to reminders;
- the Weixin syncer and reminder scheduler are started independently.

The all-features integration test repeats the critical vault, Telegram, and
Gateway wiring instead of invoking the production assembly path. This creates a
single-source-of-truth violation: adding or changing a connector requires
keeping production bootstrap and test bootstrap synchronized manually.

The problem belongs to the executable composition root. Moving connector logic
into Gateway, Agent Runtime, Store, or a new shared package would broaden the
architecture without improving the functional boundary.

## Selected Design

Add a same-package `gatewayServices` composition object under
`services/gateway/cmd/sparkclaw`.

```text
main-owned dependencies
  config + store + toolhub + agent runtime + traces + speech
                              |
                              v
                    newGatewayServices(...)
                              |
          +-------------------+-------------------+
          |                   |                   |
      Gateway HTTP       background jobs     connector wiring
                         reminders/weixin     Telegram/vault
```

The constructor will own only the assembly decisions that are currently spread
across `main.go`:

1. Read the Telegram channel configuration.
2. Create the credential vault with the existing auto-create rule.
3. Preserve the existing non-fatal readiness warning when Telegram is enabled.
4. Create the Telegram dispatcher/service and speech adapter.
5. Build the Gateway with the same speech, vault, and binding-cancellation
   options.
6. Build the reminder router and optional Telegram notification adapter only
   when reminders are enabled.
7. Build the Weixin syncer with the existing dispatcher and configuration.

`gatewayServices.Start(ctx)` will start the same background work under the
existing server context. Existing intervals and enablement rules remain
unchanged:

- Weixin sync: immediate tick, then every 15 seconds;
- reminder delivery: enabled only by `Tools.Reminders.Enabled`, immediate tick,
  then every 10 seconds;
- Telegram service: started only when the Telegram channel is enabled.

The abstraction remains in package `main` because it composes concrete internal
packages and is not a reusable domain contract.

## File Scope

Implementation is limited to the functional files involved in executable
assembly:

- `services/gateway/cmd/sparkclaw/main.go`
- `services/gateway/cmd/sparkclaw/bootstrap.go` (new)
- `services/gateway/cmd/sparkclaw/main_test.go`

This plan and its Chinese mirror are the only documentation additions. No
configuration, API, frontend, store backend, connector package, or architecture
document changes are required.

## Behavior Invariants

The refactor must preserve:

- default file-backend startup;
- optional connectors disabled by default;
- speech initialization failure remaining fatal;
- Telegram credential-vault readiness failure remaining a warning;
- Telegram binding revocation canceling active Telegram work;
- Telegram reminders using the bound encrypted credential path;
- Weixin polling continuing even when Telegram is disabled;
- HTTP routes, public config, readiness output, status codes, and payloads;
- shutdown cancellation through the existing server context;
- all polling intervals, retry behavior, and worker limits.

## Test Strategy

1. Change `TestAllOptionalFeaturesComposeWithFileBackend` to construct the
   Gateway through `newGatewayServices`, proving the integration test exercises
   the production feature assembly path.
2. Keep the recording speech backend injection so no external ASR service is
   required.
3. Run focused tests for `cmd/sparkclaw` after the mechanical move.
4. Run `gofmt`, `go build ./...`, `go vet ./...`, and `go test ./...` for the
   Gateway.
5. Re-run WebChat build and voice tests to confirm the repository-wide baseline
   remains unchanged.
6. Run the bilingual documentation mirror/link check and `git diff --check`.

## Rejected Alternatives

- **New cross-package connector interface:** rejected because Telegram and
  Weixin have different polling, persistence, and lifecycle semantics. A common
  interface would hide meaningful differences and change package boundaries.
- **Move connector assembly into Gateway:** rejected because Gateway is the HTTP
  layer and should not own process startup or background-service lifecycle.
- **Generic dependency-injection container:** rejected because the current
  dependency graph is explicit and small enough; a container would weaken type
  clarity without reducing functional risk.
- **Refactor Store or connector internals in the same pass:** rejected to keep
  the change behavior-preserving and independently reversible.

## Completion Criteria

- Production startup and the all-features integration test use the same
  optional-feature assembly constructor.
- `main.go` retains process concerns only: config, base dependencies, HTTP
  server, signals, and shutdown.
- No new imports violate the documented package dependency direction.
- No external behavior or configuration changes.
- All baseline checks pass with no new generated or runtime artifacts tracked.

## Rollback

The change is confined to package `main`. Rollback consists of inlining the
constructor and `Start` calls back into `main.go` and restoring the previous
integration-test setup; no persisted data or API migration is involved.
