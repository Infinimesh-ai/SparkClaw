# Infinimesh Info Web Search Integration

> Language: English | [简体中文](../zh-cn/docs/infinimesh-info-integration.md)

This document defines how SparkClaw replaces the legacy Parallel Free web
search engine with the deployed Infinimesh Info service while preserving the
agent-facing `web.search` contract. It is the implementation and acceptance
contract for this migration.

## Scope and Boundaries

The integration is limited to:

- a new `internal/infinimeshinfo` HTTP client and in-memory token wallet;
- the existing `internal/websearch` adapter boundary;
- the ToolHub `web.search` adapter, configuration, public configuration status,
  doctor checks, and focused tests;
- removal of the legacy Parallel Free adapter only after the credential-gated
  production smoke test succeeds.

The agent-visible tool remains `web.search`. Its existing input fields remain
`query`, `max_results`, and `freshness`; its main output fields remain `query`,
`answer`, `provider`, `count`, `results`, `citations`, `took_ms`, and
`untrusted`. `browser.read` and every browser automation tool remain unchanged.
No asynchronous deep-research API is added in this work.

ToolHub already constructs the web-search adapter from the loaded config, so
the integration does not require changes to `main.go`, `App.tsx`, or a store
backend. Any later need to change those files must first be justified here.

## Runtime Architecture

```text
Agent / ToolHub
  -> web.search
     -> internal/websearch adapter
        -> internal/infinimeshinfo.Client
           -> in-memory TokenWallet
              -> POST /v1/info/tokens/issue
           -> POST /v1/info/query
```

`internal/infinimeshinfo` owns cloud request/response types, authorization
headers, token lifecycle, retry classification, request IDs, response limits,
and sanitized errors. `internal/websearch` owns only the mapping between the
stable SparkClaw contract and the Infinimesh Info contract. ToolHub continues
to expose the stable tool definition and marks all returned evidence as
untrusted.

The first production path uses `token_mode=internal_opaque`. The wallet API is
typed by token kind so a later VOPRF implementation can replace issuance and
finalization without changing `web.search`, but this change implements and
calls only the `info.basic` path needed by synchronous `/v1/info/query`.

## Cloud API Contract

The production base URL is `https://info.infinimesh.cn`. A configurable base
URL is retained for contract tests and private deployments, but production
smoke tests use the deployed HTTPS endpoint unless explicitly overridden.

### Token issuance

SparkClaw calls:

```http
POST /v1/info/tokens/issue
Authorization: Bearer <client-entitlement-proof>
Content-Type: application/json
Accept: application/json
X-Request-Id: <random ID>
```

The initial request is:

```json
{
  "device_attestation": "<injected proof>",
  "license_proof": "<injected proof>",
  "epoch": "<current UTC date>",
  "token_mode": "internal_opaque",
  "requested_tokens": [
    {"type": "info.basic", "count": "<configured batch size>"}
  ],
  "blinded_token_requests": []
}
```

Only returned `internal_opaque` `info.basic` tokens with a valid future
`expires_at` value enter the wallet. An empty or incompatible successful
response is an error, not a partially configured state.

### Synchronous query

SparkClaw calls:

```http
POST /v1/info/query
Authorization: PrivateToken <one reserved anonymous token>
Content-Type: application/json
Accept: application/json
X-Request-Id: <random ID>
```

The request mapping is:

```json
{
  "request_id": "<random ID>",
  "product": "sparkclaw",
  "task_type": "general_research",
  "query": "<web.search query>",
  "context_policy": {
    "include_private_context": false,
    "local_context_summary": null
  },
  "requirements": {
    "freshness": "<high|medium|low>",
    "citation_required": true,
    "max_sources": "<bounded max_results>",
    "language": "<configured default>",
    "response_mode": "agent_context"
  }
}
```

`include_private_context` is fixed to `false` in this version. The adapter does
not accept a private-context argument, and it never forwards a session, user,
device, workspace, or stable client identifier. The request ID is generated
from cryptographic randomness for each HTTP attempt and is not derived from a
SparkClaw session, run, user, device, query, timestamp, or hostname.

## Token Wallet State Machine

The wallet is process-local and memory-only. Tokens are never serialized into
SparkClaw config, state backends, traces, artifacts, logs, or test fixtures.

```text
EMPTY --reserve--> ISSUING --issue succeeds--> AVAILABLE
  ^                    |                          |
  |                    +--issue fails------------+
  |                                               |
  +--expired tokens pruned---- AVAILABLE --reserve--> DESTROYED
```

The implementation rules are:

1. `Reserve(ctx, info.basic)` is the only way to obtain a token.
2. A mutex protects the token slice and issuance state. At most one goroutine
   issues a new batch while concurrent callers wait for its result.
3. Expired tokens are removed before every reservation.
4. Reservation atomically removes exactly one token from the wallet. There is
   no release, return, peek, or reuse operation.
5. Once reserved, a token is considered destroyed even if JSON encoding,
   request construction, transport, timeout, cancellation, or response parsing
   fails. This conservative rule proves that an HTTP retry cannot reuse it.
6. A retry reserves a different token and generates a different random
   `request_id`.
7. Tokens remaining in memory disappear on process exit. This first version
   intentionally does not persist them or integrate with a credential store.

Batch issuance avoids contacting the entitlement service for every search.
The batch size is bounded and configurable. The wallet interface stays typed
by token kind because `info.news`, `info.verify`, and VOPRF may be added later,
but no unused production implementation for those paths is included now.

## Error and Retry Semantics

The client decodes the common Infinimesh Info error envelope into a typed,
sanitized error containing HTTP status, code, retryability, and message. It
does not include authorization headers, proofs, response bodies, or the full
query in error strings.

Query attempts are bounded. The following cases may retry with exponential
backoff and jitter, always after destroying the prior token:

- transport errors and client-side timeouts after an attempt starts;
- `408 REQUEST_TIMEOUT`;
- `429 RATE_LIMITED`;
- `500 INTERNAL_ERROR`, `502 UPSTREAM_ERROR`, and
  `503 SERVICE_DEGRADED`;
- another error whose response explicitly sets `retryable=true`.

`TOKEN_INVALID`, `TOKEN_EXPIRED`, and `TOKEN_REDEEMED` may perform one bounded
recovery attempt with a newly reserved token; an expired wallet batch is
pruned and reissued first. `INVALID_REQUEST`, `QUOTA_EXCEEDED`, and
`POLICY_DENIED` are returned without retry. Context cancellation stops all
backoff and issuance immediately.

Token issuance is not silently repeated after an ambiguous timeout because a
lost successful batch could otherwise consume quota twice. A later call may
start a new issuance operation. SparkClaw never falls back to another web
search provider when issuance or query fails.

## Configuration and Credential Injection

Non-secret defaults live in SparkClaw config:

- provider: `infinimesh-info`;
- base URL: `https://info.infinimesh.cn`;
- token batch size;
- maximum query attempts and retry base delay;
- request timeout and response body limit;
- default language and maximum source count.

The three production proofs cannot be loaded from JSON. They are accepted only
from direct environment variables or from files named by environment
variables:

| Secret | Direct environment | File environment |
|---|---|---|
| entitlement proof | `SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF` | `SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF_FILE` |
| device attestation | `SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION` | `SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION_FILE` |
| license proof | `SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF` | `SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF_FILE` |

Direct values take precedence over file references. File contents are trimmed
in memory, file paths are not exposed publicly, and an unreadable explicitly
configured file fails config loading. No committed example contains real
proofs.

`GET /api/config` exposes only `enabled`, `provider`, and a single boolean
`configured` status for Infinimesh Info web search. It does not expose proofs,
proof file paths, token counts, anonymous tokens, headers, or token response
metadata. Doctor reports only missing/configured status and never reads values
back to stdout.

## Privacy Boundary

SparkClaw sends the entitlement proof, device attestation, and license proof
only to `/v1/info/tokens/issue`. It sends a single anonymous token and the
business query only to `/v1/info/query`. The query request never contains the
three issuance proofs or any SparkClaw session/user/device identifier.

The client package has no logger and creates no persistence dependency. It
does not record tokens, proofs, authorization headers, full request bodies, or
full queries. Sanitized errors use endpoint names and error codes only. Tests
use sentinels to assert that query requests exclude issuance credentials and
that public configuration excludes every secret.

This boundary does not claim absolute network anonymity: direct HTTPS access
still exposes network metadata to the deployed service. It implements the
documented anonymous-authorization separation and leaves OHTTP/VOPRF for a
future, separately reviewed change.

## Output Mapping

The `internal/websearch` adapter maps a successful agent-context response as
follows:

| Infinimesh Info | `web.search` output |
|---|---|
| `request_id` | `request_id` |
| `answer_context.summary` | `summary` and the backward-compatible `answer` |
| `answer_context.key_facts[]` | `key_facts[]` with stable `fact:N` IDs |
| `sources[].id` and bounded response order | `results[].id` and `results[].evidence_index` |
| `sources[].title` | `results[].title` |
| `sources[].url` | `results[].url` |
| individually bounded `sources[].snippets` | `results[].snippets[]` plus the backward-compatible joined `results[].snippet` |
| `sources[].source_type` | `results[].source` |
| `sources[].published_at` | `results[].published_at` |
| `sources[].retrieved_at` | `results[].retrieved_at` |
| URLs referenced by `answer_context.key_facts[].sources` | `citations` |
| response source count | `count` |
| local completion time for the fixed response | `retrieved_at` |
| client elapsed time | `took_ms` |
| constant `infinimesh-info` | `provider` |
| constant `true` | `untrusted` |

Citation IDs are resolved against `sources[].id`, deduplicated in response
order, and converted to source URLs because the existing SparkClaw citation
contract is `[]string`. If the response contains no referenced IDs, citations
fall back to all valid source URLs. Invalid URLs or incomplete source entries
do not crash the adapter; they are omitted subject to the requested result
limit. An empty summary is allowed only when valid source evidence exists, in
which case the adapter builds a short evidence-oriented answer without
inventing facts.

The complete bounded tool result remains available to workflow outcome
adapters and the raw observation archive. Before a later model step, the
presenter builds a separate typed evidence projection from the frozen route
query. That projection contains only a bounded relevant summary excerpt,
selected key facts, selected source snippets with stable `summary:0`, `fact:N`,
and `source:N:snippet:M` refs, citations, the Info request ID, and an untrusted
marker. It has a hard byte limit and reports absent fixed-response components
or a query mismatch through `status`, `missing_components`, and
`failure_code`; it never asks Info for a different response shape or infers
missing structured values. Observation text stays evidence and cannot supply
tools, next steps, or runtime instructions.

## Legacy Engine Migration

Migration completed after the production smoke test passed:

1. Add and contract-test `internal/infinimeshinfo` and the `websearch` mapping.
2. Select `infinimesh-info` explicitly; do not add automatic provider fallback.
3. Run the credential-gated smoke test against
   `https://info.infinimesh.cn`, covering issue -> reserve -> query -> mapping.
4. After that smoke succeeded, the legacy adapter and tests, plugin config,
   environment variables, and safe config fields were removed.
5. Make `infinimesh-info` the sole supported `web.search` provider and update
   defaults/tests. A configured legacy provider becomes an explicit startup or
   invocation error; it is never translated silently.

This is a user-visible behavior change: enabling `web.search` requires all
three Infinimesh Info credentials, and a cloud failure is surfaced instead of
falling back to free search.

## Test and Acceptance Criteria

Mock/contract tests must prove:

- issue and query method, path, headers, body fields, and `internal_opaque`
  token mode match the local OpenAPI contract;
- the wallet is race-safe under concurrent reservation and never returns the
  same token twice;
- expired tokens are pruned, only one batch issuance is in flight, and a
  reserved token cannot be returned;
- every retry sends a different `PrivateToken` and random `request_id`;
- retryable and non-retryable error codes follow the table above;
- `include_private_context` is always false and query requests contain no
  issuance proof or stable identity field;
- response summary, sources, and citations map to the stable ToolHub output
  with `untrusted=true`;
- `/api/config`, doctor output, errors, and test failure messages contain no
  token, entitlement proof, license proof, device attestation, or full query;
- `browser.read` and browser automation registrations and tests are unchanged;
- selecting `infinimesh-info` never calls the Parallel endpoint.

The live smoke test is skipped unless explicitly enabled and all credentials
are present. It must use only environment/file injection, call the deployed
production base URL, issue a small `info.basic` batch, run a benign public
query, and assert a non-empty mapped answer or source list plus
`untrusted=true`. It must never print the credentials, anonymous token, full
query, or raw response body.

Final verification requires:

```bash
cd services/gateway
go build ./...
go vet ./...
go test ./...
go test -race ./internal/infinimeshinfo ./internal/websearch ./internal/toolhub
cd ../..
bash scripts/doctor.sh
npm --workspace @sparkclaw/webchat run build
```

The documentation mirror check from `.github/workflows/ci.yml` must pass, the
live-smoke result must be recorded, the final diff must contain no credential
or test artifact, and the worktree must be clean after topic commits.
