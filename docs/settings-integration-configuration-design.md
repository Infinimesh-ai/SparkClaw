# Settings And Integration Configuration

> Language: English | [简体中文](../zh-cn/docs/settings-integration-configuration-design.md)

Status: Accepted and implemented on `main`.

## Scope

This design replaces WebChat's single long settings stack with a category
directory and adds live household configuration for Infinimesh Info and the
outbound LocalMind MCP integration.

SparkClaw is deployed as a trusted household service. Conversation records
may still have an owner, but service integrations are household-global:

- every authenticated household control client can list and change service
  credentials;
- Info and LocalMind credentials are not partitioned by owner;
- external MCP principals and Bridge peers do not receive a separate settings
  surface;
- the Gateway's normal control authentication remains the authorization
  boundary.

Credential-count limits and concurrent-editor revision conflicts are outside
the current scope.

## Settings Navigation

The settings inspector uses four categories and drill-in detail views:

```text
Settings
|- Account
|  |- Owner profile
|  `- Paired clients
|- Connections
|  |- Messaging: Telegram and Weixin
|  |- Data provider: Infinimesh Info
|  |- Outbound MCP: LocalMind
|  `- Inbound MCP: External MCP access
|- Agent
|  |- Tool policy
|  `- Model profiles
`- System
   `- Runtime boundaries
```

The directory shows only an icon, label, and bounded status. Selecting a row
replaces the directory with its detail view; Back returns to the current
category. Existing connector, policy, model, owner, client, and External MCP
controls remain reachable.

LocalMind and External MCP remain separate because they have opposite trust
directions. LocalMind is a fixed outbound task client. External MCP is the
inbound surface through which another AI invokes SparkClaw.

## Household Credential Model

Each integration stores one encrypted, versioned bundle in the existing
credential Vault:

```text
integration:infinimesh-info
  kind: infinimesh-info-credential-bundle-v1

integration:localmind
  kind: localmind-credential-bundle-v1
```

The decrypted logical shape is:

```json
{
  "version": 1,
  "active_credential_id": "info_cred_b",
  "credentials": [
    {
      "id": "info_cred_a",
      "label": "Family account",
      "validated_at": "2026-08-27T10:00:00Z",
      "last_checked_at": "2026-08-27T10:00:00Z",
      "state": "ready",
      "payload": {
        "license_id": "...",
        "license_key": "..."
      }
    }
  ]
}
```

The complete bundle, including labels, endpoint, identifiers, and secrets, is
encrypted as one authenticated value. The repository sees only the opaque
Vault ref, credential kind, and AES-256-GCM envelope. The Vault uses the
repository's compare-and-swap replace command, never delete-then-create, and
reconciles unknown create, replace, and delete outcomes before another
mutation is accepted.

Public responses include only:

- an opaque credential ID;
- the user-entered label;
- validation/check timestamps;
- bounded state and error code;
- whether the credential is active.

They never include a license ID/key, LocalMind endpoint/token, Vault ref,
encrypted envelope, environment variable name, or file path. Secret form
fields are never prefilled and are cleared after every terminal save attempt.

## Save, Select, And Delete Semantics

Saving and selecting are separate operations:

1. The Gateway validates the typed local format.
2. It performs the integration's real online validation.
3. Only a credential that passes validation is appended to the encrypted
   bundle.
4. The active credential is unchanged.
5. The user explicitly selects a saved credential or the virtual operator
   configuration.

The effective source is:

```text
selected household Vault credential > selected operator configuration > none
```

This is selection precedence, not fallback logic. Once a household credential
is selected, a later authentication or availability failure leaves it
selected and reports the failure. SparkClaw never automatically chooses a
different saved credential or the operator configuration.

A validation failure occurs before persistence and therefore does not change
the existing active source. Deleting the active credential is rejected until
the user explicitly selects another saved credential or the available
operator configuration. Inactive credentials may be deleted directly.

## Runtime Switching

Each live integration runtime has a monotonically increasing credential
generation. An agent run records the generation on first use. A committed
selection change:

1. marks the integration as updating;
2. cancels direct calls and full agent runs that used the old generation;
3. returns `info_credentials_changed` or
   `localmind_credentials_changed` for the interrupted call;
4. publishes only clients built for the newly selected source;
5. increments the generation and leaves the new selection committed even if
   its subsequent runtime refresh fails.

New calls during the publication window receive `info_updating` or
`localmind_updating`. A paused or resumed run cannot cross generations.

There is no compatibility drain, dual-client transition, or runtime rollback.

## Infinimesh Info Contract

An Info credential contains a bounded License ID and License Key. The key must
use the `ilk_v1.<license-id>.<secret>` wire shape and embed the same License ID.

Before saving or on an explicit check, SparkClaw creates a temporary client and
runs exactly one fixed low-cost query:

```text
query: SparkClaw connection check
max_sources: 1
token batch: 1
private context: false
```

Response content is discarded. Only typed success, authentication failure, or
temporary unavailability reaches the settings API.

`web.search` remains controlled by the explicit Web Search tool-policy/config
switch. `weather.lookup` is registered regardless of credential presence.
Credentials never decide whether an Info tool exists. Invocation without a
selected source fails with `info_not_configured`; explicit tool policy may
still deny a registered tool.

## LocalMind Contract

A LocalMind credential contains a bounded workspace MCP endpoint and bearer
token. The endpoint must be absolute HTTP(S), contain no user info, query, or
fragment, and end in `/api/workspaces/<workspace-id>/mcp`. Plain HTTP is
accepted only when the operator has enabled private HTTP and the host is
loopback, private, or container-local.

The online validation reuses the full fixed LocalMind task contract:

1. negotiate MCP `2025-06-18`;
2. require server name `localmind-ai`;
3. reject Resources;
4. require the exact three remote task tools and schemas;
5. prepare the four governed local tool registrations.

Credential validation does not publish tools. Activation refreshes and
atomically publishes the four registrations. A refresh failure removes stale
LocalMind tools and leaves the newly selected credential selected.

The LocalMind manager is constructed when its fixed server block exists even
if operator environment variables are absent, allowing a household credential
to activate it later. Periodic refresh is idle while no source is selected.

## Gateway API

The authenticated control API is typed per provider:

```text
GET    /api/integrations
GET    /api/integrations/{id}
POST   /api/integrations/infinimesh-info/credentials
POST   /api/integrations/localmind/credentials
PUT    /api/integrations/{id}/active-credential
POST   /api/integrations/{id}/credentials/{credential_id}/check
DELETE /api/integrations/{id}/credentials/{credential_id}
```

Credential request bodies are limited to 16 KiB, reject unknown fields, and
accept one JSON value. Errors return only stable codes and bounded messages.
Mutations are serialized inside each controller and audit only integration ID,
operation, source, state, and bounded error code. Multi-editor optimistic
revision fields are intentionally not part of this household API.

## Public State Vocabulary

| State | Meaning |
|---|---|
| `not_configured` | No source is selected and no operator source is available |
| `configured` | A source is selected and locally usable; no fresh live success is claimed |
| `checking` | An explicit online check is in progress |
| `ready` | The required query or handshake succeeded |
| `needs_attention` | Authentication, identity, contract, or permanent validation failed |
| `temporarily_unavailable` | A bounded retryable external check failed |
| `vault_unavailable` | The encrypted bundle cannot be read or changed safely |

Operator configuration is exposed only as an availability flag and a virtual
selectable row. Its values and source locations remain private.

## Verification Requirements

Tests cover Vault CAS replacement and unknown outcomes, ciphertext-only
persistence, multiple retained credentials, failed-validation non-persistence,
explicit activation, active-delete rejection, no fallback after failure,
generation cancellation, credential-independent Info registration, the fixed
Info query, LocalMind fixed-contract validation, redacted API responses, and
WebChat navigation/secret clearing/selection confirmation.
