# Third-Party Connector Registry Refactor Plan

> Language: English | [简体中文](../zh-cn/docs/connector-registry-refactor-plan.md)

Status as of 2026-07-15: implemented and verified in the isolated
`codex/third-party-integration-architecture` worktree.

## Objective

Make third-party messaging integration extensible without making Gateway,
reminder delivery, or process startup depend on a particular product.
Existing Telegram and Weixin behavior must remain available through the same
HTTP, store, policy, and Agent Runtime paths.

This is an in-process refactor. SparkClaw remains one Gateway binary and keeps
the existing packages, APIs, configuration files, state backends, approval
model, and connector implementations.

## Baseline

The isolated worktree starts at `1578785` and passed:

- `npm run setup:document-tools`;
- `go build ./...`, `go vet ./...`, and `go test ./...` under
  `services/gateway`;
- `npm run build` under `apps/webchat`;
- `npm run test:voice` (6 tests).

The WebChat bundle baseline is 298.91 kB JavaScript and 35.20 kB CSS before
gzip.

## Current Coupling

Adding a messaging connector currently requires synchronized edits in several
unrelated locations:

- `binding.NewRouter` selects Telegram and Weixin binding adapters;
- `notification.NewRouter` selects only Weixin outbound delivery while
  `main.go` separately adds Telegram;
- Gateway applies Telegram-only capability and activation-URL rules;
- `main.go` starts Telegram and Weixin background work separately and wires one
  connector-specific cancellation callback;
- reminder delivery constructs an independent notification router.

The existing domain contracts are already useful: `binding.Adapter`,
`notification.Adapter`, `websearch.Adapter`, and `speech.Transcriber`. The
problem is repeated construction and registration, not the absence of a single
universal adapter interface.

## Selected Design

Add `internal/connector.Registry` as the messaging-connector composition
boundary.

```text
provider implementation
  binding + notification + polling runtime + protocol delivery
                              |
                              v
                   connector.Registry.Register
                              |
       +----------------------+----------------------+
       |                      |                      |
 binding/notification connectorruntime.Runtime connectorruntime.AgentBridge
       |                      |                      |
 Gateway/reminders       process context       shared Agent path
```

Each registration contains only capability contracts:

- normalized channel name;
- binding adapter, when the connector supports owner binding;
- notification adapter, when it supports outbound reminders;
- polling runtime, when it consumes inbound events;
- binding cancellation function, when active work must be stopped.

The registry reads the existing channel configuration to decide whether
outbound delivery and background work are enabled. Binding adapters remain
registered while operator-disabled so Gateway can report the difference
between `connector_unavailable` and `operator_disabled`.

Registration is channel-keyed and rejects empty or duplicate channels. The
registry does not import Telegram, Weixin, their protocols, or their config
fields. Concrete construction stays in the executable composition root, which
is the one place allowed to know which implementation satisfies each
capability.

## Domain Boundaries

The refactor intentionally keeps separate interfaces for separate semantics:

| Capability | Existing contract | Registry role |
|---|---|---|
| Owner binding | `binding.Adapter` | Register and build Gateway router |
| Outbound messaging | `notification.Adapter` | Register and share one router |
| Inbound connector work | `connectorruntime.Runtime` | Run under server context |
| Normalized Agent call | `connectorruntime.AgentBridge` | Share idempotent Agent invocation |
| Reminder target | `remindertarget.Resolver` | Resolve normalized binding/session fields without provider branches |
| Search | `websearch.Adapter` | No change |
| Speech-to-text | `speech.Transcriber` | No change |

`connectorruntime.Runtime` models the shared long-lived shape: receive provider
events, normalize them, call the Agent bridge, and deliver the result through
the provider protocol. Telegram and Weixin keep their own polling algorithms:
Telegram has durable inbox/offset semantics and long polling, while Weixin has
cursor batches and CDN media. The shared runtime contract does not weaken
those delivery guarantees.

`connectorruntime.AgentBridge` owns the common Agent invocation and idempotency
selection. Provider dispatchers continue to own authorization, commands,
approval presentation, session identity, media decoding, localized copy, and
protocol sends. Search and speech are provider-neutral at their consumers
already; combining them with chat connector lifecycle would create a weak
abstraction because they do not bind accounts, poll messages, send reminders,
or cancel binding work.

## Core Changes

1. Add a provider-neutral connector registry with focused unit tests using
   fictitious channel names.
2. Add base-router constructors to binding and notification packages so the
   registry can populate adapters without provider switches.
3. Move binding dependency readiness behind the binding adapter contract;
   Gateway capability calculation no longer checks a Telegram channel name.
4. Add Gateway injection for the registry-built binding router.
5. Construct Telegram and Weixin registrations once in `cmd/sparkclaw` and use
   the registry for Gateway binding, reminder delivery, background startup,
   and binding cancellation.
6. Add a shared `connectorruntime.Runtime` lifecycle and make Telegram Service and
   Weixin Syncer satisfy it; remove product-specific start goroutines from
   `main.go`.
7. Route both dispatchers through `connectorruntime.AgentBridge` for normal and
   idempotent Agent execution while keeping protocol processing in the
   provider packages.
8. Replace Telegram-only Gateway branching with state/capability semantics:
   all channels use server-computed startability, and activation links are
   hidden according to the binding state rather than the product name.
9. Move reminder recipient selection out of ToolHub product switches into
   `remindertarget.Resolver`; delivery adapters retain protocol-specific final
   validation.

Compatibility constructors may remain for package-level tests during this
pass, but the production path must use the registry and must not rely on their
provider switches.

## Behavior Invariants

The refactor must preserve:

- optional connectors disabled by default;
- Telegram token verification, encrypted credential storage, activation,
  long polling, voice transcription, reminders, and revocation cancellation;
- multiple Telegram Bot bindings may coexist for different external Telegram
  users; each binding keeps a distinct encrypted credential, activation
  challenge, cursor, inbox identity, and private-chat authorization boundary;
- Weixin QR/manual binding, polling, inbound dispatch, media, reminders, and
  notification delivery;
- one shared Speech transcriber for WebChat and Telegram;
- Infinimesh Info as the configured opt-in `web.search` provider;
- default file-backend behavior and all three Store implementations;
- HTTP routes, approval rules, retry limits, worker bounds, polling intervals,
  and shutdown cancellation;
- explicit unavailable/disabled states instead of silent fallback.

Applying the operator-enabled check consistently to every registered connector
is allowed as a defect correction: an operator-disabled connector must not
start a new binding or background worker. Any such observable correction will
be listed separately in the final report.

## Non-Goals

- No plugin loading, dynamic Go modules, or runtime code download.
- No service split or new process.
- No replacement of provider-specific protocol clients.
- No generic untyped bag replacing the current configuration schema.
- No new Telegram or Weixin protocol, WebChat, search, speech, or TTS feature
  beyond enabling the explicitly requested multiple Telegram bindings.
- No forced common cursor/offset algorithm: delivery acknowledgement remains a
  provider runtime responsibility until both implementations use the same
  durable inbox contract.
- No claim that Weixin voice input is implemented; that requires a separate
  typed media and transcription design.

## Verification

Focused checks:

- registry duplicate/unknown/disabled/runtime/cancellation tests;
- Agent bridge normal/idempotent execution tests shared by both providers;
- binding capability tests with a fictitious adapter readiness failure;
- Gateway binding tests for Telegram and Weixin through injected routers,
  including two Telegram Bot credentials created under one running Gateway;
- reminder delivery tests through the shared notification router;
- reminder target tests with a fictitious channel and multiple bindings;
- `cmd/sparkclaw` all-features/default-file-backend composition tests.

Final checks:

- `gofmt` and `git diff --check`;
- Gateway `go build ./...`, `go vet ./...`, and `go test ./...`;
- WebChat build and voice tests with bundle-size comparison;
- `scripts/doctor.sh`;
- mock golden eval, with any pre-existing validation drift recorded separately;
- bilingual Markdown mirror/link validation;
- tracked-diff secret and generated-artifact scan.

## Completion Criteria

- Production code registers each messaging connector once.
- Gateway, reminders, and lifecycle consume registry products rather
  than selecting product names.
- Registry core tests use no Telegram or Weixin identifiers.
- Supported connector behavior remains green on the default file backend.
- No architecture layer outside the existing Gateway binary is introduced.

## Rollback

The change is code-only composition. Rollback restores the previous router
construction and direct background starts; no persisted schema, credential
format, API migration, or data rewrite is involved.
