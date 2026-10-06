# Local WebChat Access Design

> Language: English | [简体中文](../zh-cn/docs/local-webchat-access-design.md)
>
> Date: 2026-10-06 (Asia/Shanghai). Status: **D01–D04 confirmed; user authorized design completion and implementation; implementation and isolated qualification completed; physical deployment qualification pending, not deployed**. This review resolves framing, stale-credential recovery, speech tickets and staged acceptance. Section 6.4 fixes the implementation contract.

## 1. User Requirement And Scope

Opening WebChat on the backend machine through a local address should not require manually entering a credential. LAN browsers and independent clients still require their own credentials. “No credential entry” removes a user interaction; business requests still receive a verified, owner-scoped identity.

This proposal applies to the product WebChat entrance in both Local and Remote model modes. Model placement does not determine whether a browser is local. It does not change tool approvals, client browser-host grants, MCP/ISCP authentication, JingSi interfaces, or mail authorization.

The current [shared-backend baseline](local-shared-backend-design.md) requires browser tokens even over loopback. The [R3 target](client-backend-architecture-design.md#31-first-login-and-service-unlock) requires installation credentials, including colocated desktop installations. This implementation introduces a narrowly scoped local **WebChat** exception, now recorded in both baseline documents. Independent desktop installations keep their first-login requirement.

Authentication changes do not change R3 data ownership: browser installations retain their own non-mail data; the backend owns mail. Sharing a local Owner principal must not merge browser profiles, installation IDs, or conversation stores. The currently deployed server-backed Web UI and the future R3 Web storage adapter remain distinct implementation stages.

## 2. Current Evidence

At the inspected source revision `2526240e`, the following behavior is implemented:

| Area | Current behavior / source |
|---|---|
| Local request | On 2026-10-06, the Linux test host returned `200` for the loopback page, `auth_required: true` from readiness, and `401` for a credential-free `/api/sessions` request |
| Gateway | [Authentication middleware](../services/gateway/internal/gateway/middleware.go) validates protected routes using bearer credentials; loopback does not grant normal workbench authority |
| Web entrance | [Compose](../docker/compose.yaml) publishes WebChat through a Docker bridge; Gateway remains internal. The [TLS overlay](../docker/compose.desktop-tls.yaml) enables HTTPS |
| Browser | [API transport](../apps/webchat/src/api/client.ts) loads a token from browser storage or a build-time override; [App](../apps/webchat/src/App.tsx) also gates workbench events on token availability |
| Host recovery | [Private management handler](../services/gateway/internal/gateway/local_management.go) exposes only identity/device-management operations over an owner-only Unix socket, using a credential with no authority on network APIs |

The previous exploratory changes were withdrawn. This section records the pre-implementation baseline; section 10 records this implementation and its qualification, without equating source completion with deployment.

## 3. Proposed Access Rules

| Caller and address | Proposed result |
|---|---|
| Backend-host browser, `localhost`, `127.0.0.1`, or `::1`, configured WebChat port | Automatic local identity; no token form |
| Same machine, but using its LAN IP or a LAN domain | Ordinary credential login; location alone does not change the selected entrance |
| Another machine's browser using the LAN entrance | Independent browser credential |
| Another device using an authorized host user's tunnel to the loopback entrance | May inherit local access as intentional delegation under D04; configured Host/Origin/port checks still apply |
| SparkX / another independent client, even when colocated | Existing installation credential and backend identity verification |
| Unrelated website trying to call local WebChat | No local authority; reject before protected data access or effects |
| Remote caller forging Host, forwarding headers, or an Electron/browser marker | No local authority |
| Gateway TCP endpoint, MCP, ISCP, browser-host control, or JingSi | Existing independent admission rules |

Automatic admission requires the conjunction of a trusted transport source, an explicitly configured local destination, an allowed browser request context, and a valid deployment-to-Owner binding. No single request header proves locality or identity. Hostname resolution to a local IP is insufficient; arbitrary names such as `example.localhost` are not automatically trusted.

## 4. Product Decisions

| ID | Question | Decision and status | Consequence |
|---|---|---|---|
| D01 | Does local access trust the whole machine, or only the deployment OS user? | **Confirmed: trust the whole machine** | Every local OS user's process can reach loopback. No per-OS-user isolation is promised; TCP loopback cannot identify the browser's OS user |
| D02 | Does local WebChat receive full Owner privileges, including device issuance and revocation? | **Confirmed: full Owner privileges** | Local device management needs no additional credential prompt; ordinary business/tool approvals remain in force |
| D03 | May the local browser entrance use HTTP while LAN uses HTTPS? | **Confirmed: loopback HTTP is sufficient; LAN uses verified HTTPS** | HTTP and HTTPS routing/bindings must be qualified before selecting a port layout; users need not configure local browser certificate trust for the HTTP entrance |
| D04 | May an authorized host user's SSH/locally terminating relay delegate local access to another device? | **Confirmed: accept it as the host user's intentional delegation** | The ingress sees a local socket peer, not the remote endpoint behind that relay; this is an accepted limit of whole-machine trust |

D01–D04 record explicit user answers, not inferred defaults. No product question remains unanswered at this revision. The user subsequently authorized completing the design and implementing it; deployment completion requires runtime evidence.

Rules adopted for implementation:

- Preserve the ordinary credential path if a caller explicitly supplies credentials. Reject invalid/revoked credentials instead of silently falling back to the local Owner. Offer an explicit “Clear credential and use local access” action that clears only the current service credential and revalidates local admission without deleting history. Explain when a fixed build-time token prevents this recovery.
- Keep host-local authority separate from revocable remote-device identities. Revoking every user device should leave the intentional host administration/recovery path available, consistent with D02.
- Display “Local access” rather than a normal credential logout action. Closing a page ends that page's activity; reopening an enabled local entrance can authenticate again. Turning off local access is a deployment policy operation, not a browser logout.
- An SSH tunnel or another locally terminating relay can appear as a loopback peer. D04 explicitly accepts the resulting delegation under D01's whole-machine model. The product must document this limit and must not claim remote endpoint identification through arbitrary tunnels. Forwarding still has to preserve an admitted Host/Origin/port; arbitrary port mappings are not automatically trusted.

## 5. Identity And Authority

The recommended design separates three concepts:

| Concept | Lifetime and scope |
|---|---|
| Deployment / Owner | Verified against private local provisioning and backend state; never selected from URL/body parameters |
| Host-local workbench authority | Scoped to the controlled local WebChat entrance; not a portable LAN bearer and not an independent desktop's credential |
| Browser installation / session | Identifies a particular browser's lifecycle and R3 local data; does not imply a shared conversation database |

The public identity response exposes `access_mode=local`, `local_access_id`, `owner_id` and `actor_id`, with an empty `client_id` and no secret. Local mutations record an admission audit with the registered method/route, Actor, `auth_method=local`, `local_access_id` and `owner_id`; request bodies, queries and secrets are excluded. Existing business audits continue to record outcomes. The admission event records authorization to dispatch, not successful completion.

Do not copy `desktop-client.json` into browser storage, reuse a revocable desktop identity as the permanent host authority, or turn the existing management credential into a network API token. The current management socket stays restricted to its existing route set. A new local workbench transport must have its own explicit boundary and route policy.

The pre-implementation identity and event handlers require `client_id`, and connection cancellation checks a persisted Client. Implementation must deliberately support the chosen local principal and session lifecycle across those paths. The implementation uses a distinct local identity and shared Owner-scope checks; an empty Client ID must never inherit legacy global-debug access. Sessions, profiles, documents and approvals remain scoped to the provisioned Owner.

Full Owner authority still follows existing business authorization and tool approvals. Device credential issuance uses the existing idempotent, one-time disclosure flow; the local session itself cannot be exported as a remote credential.

## 6. Transport And Browser Boundary

### 6.1 Reliable locality

Docker's published-port path may translate connection addresses. Do not treat the Docker gateway address, a private subnet, Host, `X-Forwarded-For`, or User-Agent as proof of a local browser.

Selected approach (details in 6.4): a host-side WebChat ingress observes the real socket peer and destination, serves the browser assets, and connects to Gateway through a private authenticated or OS-protected channel. A The selected Unix-domain socket is independent of the existing credential-management socket. Gateway need not be published on a host TCP port.

An alternative is a separately bound loopback-only entrance with an equally private upstream. It must remain unreachable through the LAN entrance and other containers. Section 6.4 fixes the separate host ingress/private upstream selected here and preserves existing integration routing and qualified desktop HTTPS/WSS semantics.

D01 does not require per-user isolation. A future move to per-user trust would require OS-user verification before establishing local workbench authority. Merely making an upstream socket owner-only does not distinguish users of a public loopback HTTP listener.

### 6.2 Browser source checks

Removing manual token entry must not let an unrelated website use the browser's access to localhost. Before granting local authority:

1. Verify the trusted transport and exact configured local Host/port; reject unexpected hostnames and ambiguous authority parsing.
2. Validate Origin against the exact expected scheme/host/port. Reject `null`, foreign, and malformed origins where a browser origin is required. Validate WebSocket origins as well.
3. Use Fetch Metadata and a same-origin-only request proof/session mechanism for requests without Origin. A simple GET, image, form submission, or permissive CORS response must not independently establish local authority.
4. Constrain local bootstrap and protected cookie/session use with CSRF protection. If cookies are chosen, use host-only, HttpOnly, same-site restrictions, appropriate Secure handling, finite server lifetime, and entrance-bound validation. Cookie Domain/path flags alone are insufficient: cookies are not port-isolated.
5. Apply the boundary to JSON, uploads/downloads, SSE, speech ticket creation and WebSockets, including retry/reconnect paths. The private ingress secret never enters HTML, bundles, URLs, logs or browser storage. Retain the existing short-lived, single-use speech URL ticket as the sole controlled exception: bind it to its issuing entrance and local principal, validate before consumption, disable access logging/cache/referrer disclosure, and reject redemption at the LAN entrance. Device credentials are temporarily displayed only after explicit issuance.
6. Workbench HTML and error responses send `Content-Security-Policy: frame-ancestors 'none'`; reject iframe/object/embed entrances. Same-origin API checks do not authenticate the top-level page and cannot replace clickjacking protection.

Reject disallowed protected requests before processing them; CORS headers alone only constrain browser response access. Check raw requests and resource embedding as well as `fetch`.

The local session proof must have no authority on the LAN/client entrance, even if copied. Define and test that binding explicitly. An ordinary client cannot obtain R3 installation or executable-host grants just by claiming a browser User-Agent.

Under whole-machine trust, a native process on the machine can imitate browser requests. HTTP headers cannot prove which executable sent them. The product can keep the independent-client enrollment flow credential-based, but it cannot claim isolation from all trusted local programs without a stronger OS boundary.

### 6.3 Local HTTP and LAN HTTPS

Under D03, bind the automatic-authentication HTTP entrance to loopback only. Never send a local host credential through a plaintext LAN entrance or redirect it to a remote origin. Scheme and port must be included in session/source checks.

The pre-implementation test deployment serves HTTPS on the workbench port, including loopback. This implementation adds an independent local port while retaining the existing LAN/desktop port and TLS overlay. Do not promise that a wildcard listener and a second listener can bind the same port without conflict. Preserve LAN certificate verification and native Gateway TLS requirements for R3 host control.

### 6.4 Selected Implementation Contract

- A separate `local-webchat` process binds host loopback `127.0.0.1:18794` and, when available, `[::1]:18794`. `SPARKCLAW_LOCAL_WEBCHAT_PORT` configures the port. Only `localhost`, `127.0.0.1` and `[::1]` Host authorities on that port are admitted. Existing LAN/desktop bindings remain configured independently. Product containers use Linux host networking; never infer locality from Docker bridge peers. Other platforms require qualified host-process operation.
- Serve independently built WebChat static assets, prohibit framing and send no cross-origin API permission. Ordinary APIs require the non-secret `X-SparkClaw-Local-WebChat: 1` marker and `Sec-Fetch-Site: same-origin`; any Origin must exactly equal `http://Host`. Missing Fetch Metadata is rejected without widening admission. Top-level assets and non-secret health checks may load anonymously but grant no business authority.
- There is no cookie session or browser bearer. Recheck authority per request; connection lifetime belongs to the service. WebSockets use exact same-origin Origin/Fetch Metadata and an entrance-bound, single-use short-lived ticket, without requiring a custom header the browser cannot set.
- Gateway adds a separate `local-webchat/workbench.sock` (0600) in a private 0700 directory. `local-webchat.json` (0600) carries deployment/Owner binding and an independent random upstream secret. The ingress validates permissions and sends that secret in a private header accepted only on this socket. Neither the management socket nor a portable ordinary device identity is reused.
- A private identity-check route exists only on the new socket. The ingress verifies deployment, Owner and independent local identity before forwarding business requests, follows no redirects and strips caller-injected forwarding identity headers. Missing/unsafe provisioning or identity mismatch disables local authority while independent clients remain usable.
- Gateway route registration explicitly enrolls workbench business routes while sharing handlers. The local table excludes `/mcp`, `/api/bridge/`, `/api/jingsi/`, `/api/r3/`, pairing and executable browser-host control. New routes do not acquire local authority simply by being under `/api/`. Owner device management and integration settings are workbench business routes.
- Local principals use a distinct `local_access_id` and `access_mode=local`; empty `client_id` never impersonates a persisted Client. Identity, events, audit and speech deliberately support that principal type; ordinary revocation still checks persisted Clients. An explicit bearer has priority and rejection never falls back. Enabling local access also requires ordinary Gateway authentication.
- This phase changes the existing server-backed Web UI only. Browser R3 installation/local storage is separate. Initial connection verifies identity before private reads; recovery rereads state without replaying messages, approvals or issuance.

## 7. Frontend And Failure Behavior

The UI should use the backend-confirmed access state rather than infer authorization from whether localStorage contains a token. This applies to initial private-data loading, background refresh, workbench SSE, binary fetches, speech, and reconnects.

| State / event | Proposed behavior |
|---|---|
| First local visit | Verify local access automatically, then load authorized data; no credential prompt or unintended conversation creation |
| First LAN visit | Existing credential login |
| Supplied credential rejected | Show invalid/revoked authentication; no local-authority fallback |
| Backend unavailable | Connection failure with retry; no new identity or replay of writes |
| Local provisioning missing or mismatched | Explain local-access setup failure; preserve independent clients and explicit host recovery |
| Local access disabled or session invalidated | Stop local protected streams/actions; no broader anonymous fallback |
| Page/reload/backend restart | Revalidate local authority and refresh state; do not resubmit messages, sends, approvals, or issuance |
| Every ordinary device revoked | Host-local entry remains available under D02; revoked devices remain revoked |

The single switch is `SPARKCLAW_LOCAL_WEBCHAT_ENABLED`: `true` in the product profile, `false` in standalone Gateway debugging; invalid values fail startup. Reconcile/restart services after changing it. Disabling stops the loopback entrance and private workbench socket, closes active connections and makes old local speech tickets unusable. Switching authentication does not delete browser history or change data ownership.

## 8. Acceptance Plan

These are acceptance requirements; completed evidence and remaining deployment gates are recorded in section 10:

| ID | Required evidence |
|---|---|
| A01 | Clean browser profiles on configured IPv4/IPv6/localhost entrances can open the workbench and use authorized APIs without entering or persisting a device bearer |
| A02 | A separate physical LAN machine gets `401` without credentials and succeeds with its own valid credential |
| A03 | Same-host access via LAN IP/domain still uses ordinary credential login |
| A04 | Forged Host, Origin, forwarding headers, browser markers and Docker-network callers cannot acquire local authority |
| A05 | Foreign/null origins, embedded resources, forms, CORS preflights, redirects and WebSocket attempts cannot read protected data or trigger mutations |
| A06 | Local sessions/proofs copied directly to the LAN entrance are rejected; explicit invalid/revoked device credentials never fall back |
| A07 | JSON, files, streaming chat, workbench events and speech work locally; missing browser tokens no longer suppress valid subscriptions |
| A08 | Device revocation closes that device's channels; local authority and other devices follow D02's approved independence rule |
| A09 (future R3 stage) | Once the browser R3 storage adapter exists, profiles retain distinct installation/data ownership. This phase does not migrate storage or impersonate an R3 installation; A09 does not block the authentication change |
| A10 | Missing private files, unsafe permissions, backend restart and connection loss fail visibly without creating authority or replaying writes |
| A11 | Tests cover whole-machine trust and an authorized SSH relay under D04, including rejection of mismatched Host/Origin; TLS and port-binding behavior matches D03 |
| A12 | Existing desktop credential login, verified HTTPS/WSS, MCP/ISCP, JingSi and browser-host grants remain unchanged |
| A13 | iframe/object/embed cannot embed the automatically authorized workbench; responses include frame protection |
| A14 | A browser retaining a revoked token can explicitly recover into local access; failed requests never automatically downgrade |
| A15 | A local speech ticket copied to LAN or another local Origin is rejected without consumption; disable/restart invalidates it |
| A16 | Local principals cannot access another Owner's sessions, profiles, files or approvals; mutation audits attribute the local identity without retaining private request inputs |

Use unit/contract tests for admission and lifecycle, real browser tests for CSRF/cookie/stream behavior, and the Linux test host plus a separate LAN client for transport proof. A curl call from the backend alone does not prove remote rejection. Include the selected memory/file/PostgreSQL paths if identity persistence changes.

## 9. Review And Implementation Handoff

1. Preserve the confirmed D01–D04 policy when selecting the final transport/session implementation. Escalate a newly discovered conflict rather than silently changing these decisions.
2. Review the additional proposals, source checks, lifecycle, exact affected routes and R3 identity implications; mark unresolved items explicitly.
3. Implementation is authorized. Implement section 6.4 and run the current-stage matrix; record outstanding physical Linux/LAN evidence without substituting unit tests for rollout validation.
4. Update current-state architecture, WebChat/deployment guidance and their Chinese mirrors only when the behavior is actually implemented. Remove contradictory current guidance at that point.
5. Keep the existing authenticated deployment available for rollback; do not revive revoked credentials or delete user data as recovery.

InfiniCenter was not found at the configured or ancestor locations during this session. Consequently no cluster decision or contract review is claimed. This implementation changes no external integration contract. If implementation would affect JingSi, another project, or a shared contract, resolve that coordination before making the external change. Record externally visible completion in the cluster status when the center is available.

## 10. Implementation And Qualification Ledger

Source implements the separate ingress, private Gateway transport, explicit principal/routes, Owner isolation, mutation audit attribution, bound speech tickets, frontend identity gate/recovery and both product startup paths. No production rollout is claimed.

Evidence recorded on 2026-10-06:

- WebChat: 205 tests across 48 files and a production build passed.
- Provisioning and Compose: 38 tests passed, including disabled-to-enabled reconciliation, Local/Remote startup and combined TLS/JingSi overlays. Another 19 deployment, autostart and email-deployment tests passed on Ubuntu 24 as a non-root user; the remote-deployment fixture now includes the provisioning module it imports.
- The default file backend was exercised through real Gateway and ingress processes. Eight Chromium checks passed: clean credential-free entry, ordinary network rejection, last-device revocation with continued local access, explicit revoked-token recovery with draft preservation, desktop/mobile layout, foreign-origin rejection, framing protection and no runtime JavaScript errors.
- Go admission and lifecycle tests cover private authority, route exclusion, explicit bearer precedence, Owner isolation, audit input redaction, credential revocation, streaming, ticket entrance binding, unsafe provisioning, and cancellation. The final Linux full build/vet/test and focused race gates passed against the final runtime-source snapshot. The new product ingress image built successfully for Linux/arm64; isolated read-only smoke checks passed for disabled mode and missing-backend rejection.
- The authoritative bilingual docs check passed for 103 project Markdown files; `git diff --check` passed.

Remaining deployment gates: qualify the actual native Linux host network and IPv4/IPv6 bindings with an independent physical LAN client, verify the deployed HTTPS/WSS and authorized SSH relay, and exercise speech with a real microphone. The isolated process/browser tests and Linux container suite do not replace that physical evidence. A09 remains a future R3 storage milestone.
