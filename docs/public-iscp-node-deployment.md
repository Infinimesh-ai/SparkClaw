# Public ISCP Node Deployment Record

> Language: English | [简体中文](../zh-cn/docs/public-iscp-node-deployment.md)
>
> Original rollout: 2026-10-12. Its requested acceptance completed. Services
> and native `16b00f17` are deployed. Exact-Client migration, original data/UI,
> default Main cold launch, E registration/model/server-local ACK/revocation/cold
> denial and test suites pass. The migration exception is removed; final paired
> checkpoints retain Main active and six completed revocations. D's unconfirmed
> qualification request remains preserved without replay.

This is the execution record for the [public Relay/node issuer design](public-iscp-relay-node-issuer-design.md).
The [deployment guide](deployment.md) remains the general environment reference.
The user authorized Codex to inventory, back up, deploy and qualify the complete
connection flow while preserving UI, data and LAN. InfiniCenter is absent at the
prescribed anchors; no central `accepted` decision is claimed.

The 2026-10-13 four-node expansion is tracked separately in section 7. Its native
acceptance is independently recorded there; the original rollout's results do not certify
these additional nodes.

## 1. Recorded topology

| Host | Deployed role | Exposure |
|---|---|---|
| PVE | `pve-iscp-443.service`, script `/usr/local/sbin/pve-iscp-443` | The sole new public application mapping is TCP `443` to TEST1 `10.20.0.20:443`; narrow return/reflection rules support work2 outbound traffic |
| TEST1, VM100 | IP TLS/Nginx, Relay, PostgreSQL 18 and independent control forwarder | [HTTPS origin](https://210.16.177.239), `wss://210.16.177.239/v2/relay/connect`; Relay/db/control internals and management remain private |
| work2, VM104 | Existing Gateway plus independent node issuer/outbound agent | No new guest-network or public application listener; issuer administration uses Unix sockets and control traffic initiates outbound HTTPS/WSS to TEST1 |
| SparkX | Existing UI plus credential/main/helper adapter | Only the TEST1 IP origin for this connection; private device key and local workbench data stay on the desktop |

No domain is used. Existing PVE `8006`, guest SSH `22020`/`22023`, business routes
and LAN capability are preserved. A work2 LAN rollout or physical LAN test is
outside scope. Public `/v2/relay/admin/*` and `/metrics` return 403; an opened
port does not expose a signing key or anonymous registration route.

The domain is `infinimesh-public`, Relay ID `relay-210-16-177-239`, and configured
issuer route `sparkclaw-work2-431c00ce-b780-42e3-9d25-c25c87c8c011`. Routing is
operator-approved and bounded; callers cannot choose an upstream URL.

## 2. Running services and private paths

Paths below describe the recorded rollout, not credentials to copy into a client.
Do not publish raw authority/configuration JSON, environment contents, key files,
database connection secrets or token-bearing receipts.

| Service | Binary/runtime | Configuration and state |
|---|---|---|
| TEST1 `iscp-relay.service` | `/opt/iscp-relay/bin/iscp-relayd` | Environment `/etc/iscp-relay/relay.env`; approved authority configuration `/etc/iscp-relay/node-authority.json`; private adapter socket `/var/lib/iscp-relay/managed.sock`; PostgreSQL 18 database `iscprelay` |
| TEST1 signer/TLS | Durable descriptor signer; IP certificate/Nginx | Signer `/var/lib/iscp-relay/signer.json`; public certificate `/etc/iscp-relay/tls/relay.crt`; private signing/TLS material remains protected on TEST1 |
| TEST1 `iscp-control-forwarder.service` | `/opt/iscp-relay/bin/iscp-control-forwarder` | Private config `/var/lib/iscp-relay/node/authority/forwarder.json`; broker identity, credentials, Grants and receipts under the private node root |
| work2 `iscp-node-issuer.service` | `/opt/sparkclaw-iscp/bin/iscp-node-issuer` | State root `/home/ubuntu/.local/share/sparkclaw/public-iscp-node`; config `authority/issuer.json`; private management `issuer-socket/issuer.sock`; private projection `registry/peers.json`; issuer operation/receipt state in PostgreSQL |
| work2 Gateway | Docker `sparkclaw-gateway-1`, image `sparkclaw-gateway:public-iscp-3a9c2535` | Existing base `work2-mail-eac29b37`; original 146 environment entries, 13 mounts and ports preserved, with six restricted node mounts and two node environment additions |

The Gateway's existing Owner/Client store remains authoritative. The issuer calls
its restricted local management socket to create, inspect or revoke a Client;
it does not add a second Owner database. One stable responder receives the Relay
queue and dispatches isolated peer/session state against the private registry and
live Gateway Client checks.

The recorded server source is `3a9c2535`. Both source archives are named
`source-3a9c2535.tar.gz` under `/opt/iscp-relay/source` and
`/opt/sparkclaw-iscp/source`, with SHA-256
`01ae02012c1cd0c7d74675c79974401752a1edbdc63354a9cd3cb44d985fa212`.
The current native installation `/Applications/SparkX.app` at `16b00f17` includes
fresh-authority coalescing and the same-Grant no-op repair. Package parity covers 58 source files and
five unchanged UI assets; ASAR SHA-256 is
`66336000981bc7fb7e8ea3cc26925224f001d8a22de085a8f86eb91d58e7e3ba`
and helper SHA-256 is
`3348c007fd8c49edbe04e2a47beb7ff158d0717324f1244324328ec4c9fb27ce`.
Desktop/WebChat suites report 289/299 pass, and affected nodeclient/issuer/workbench/
helper race tests pass with isolated PostgreSQL. The supported-platform full suite
for `16b00f17` has 70 passing packages and 10 without tests; real PostgreSQL opt-in
checks pass separately.
Server binaries remain `3a9c2535`: this node client is constructed only for the
initiator/helper, while work2's responder uses live local Gateway authorization.
Main's normal default cold launch, the E native business lifecycle, server/local
ACK parity and the final six-revocation checkpoint pass.

Earlier fixes retain the existing 30-second ISCP protocol budget with one in-flight
identity retry, retain LAN's five-second budget, and reuse the original device UI.

Cold startup retains the four business-request slots. Seven fixed safe GETs
wait on local pre-write capacity rejection, with 25–250 ms backoff inside the
caller's original 30-second deadline. Authenticated `throttled` responses for those
reads can retry at most three times within that same deadline; malformed/unqualified
429s, 503s, mutations and LAN do not receive this retry. Canceling a view's read
does not invalidate an otherwise healthy authenticated transport; real helper
failure/revocation stays authoritative. Authentication/profile changes cancel the
wait. ISCP identity reads use 30 seconds and LAN retains five seconds.
The original device UI's `client-<48hex>` issuance key maps durably in
native encrypted storage to one wire UUID and input fingerprint, so restart or a
lost result replays the same issuer receipt. No credential enters that mapping.
The device component reuses the desktop's already verified current Client ID;
callers without that ID still verify identity before listing devices. This avoids
redundant startup identity requests while preserving the original component/UI.

Private native diagnostics isolated repeated issuer status roundtrips under the
client mutex: concurrent business reads and incoming messages each queued another
fresh check, exhausting the unchanged 30-second read budget. The node client repair
in `8a48daac` (component `65ee9fb0`) coalesces only overlapping checks for one Client into one fresh signed
roundtrip. It has no positive-result cache or time TTL; a later nonoverlapping
caller uses a new operation/proof. Each waiter retains its own deadline, cancels
independently and receives a private scope slice. Grant/recovery/deletion/enrollment
mutations and newly observed signed revocation advance a generation fence, so
late active results cannot cross them. Unknown durable lifecycle requests retain
their exact proof/receipt and pending kind. Default encrypted-file and isolated
PostgreSQL race tests cover concurrency, two distinct peers, restart, cancellation,
denial classification, scope mismatch and delayed active results; workbench/helper
race tests and vet pass. Repaired Main cold start and Main/D identity isolation
pass. A later runtime interaction required one further repair: the ten-second
poll re-accepts the same Grant even when no authority changed. `16b00f17` (component
`ded02fdf`) recognizes only a cloned, fully verified, byte-identical current Grant
as a no-op and revalidates it under the client lock. Actual changes retain the
mutation fence. A private digest only identifies this candidate; it never replaces
a fresh authorization check. A real TLS check held beyond ten seconds completes
inside the original 30-second budget despite same-Grant polls; altered
binding/signature/expiry, aliases and known revocation remain rejected.

D was revoked through Main's original UI and closes live/denies cold start while
Main remains usable. D request `8c735df2-2ca3-4ce7-97df-941e992a4006` remains
`submission_pending` with its explicit submission fence and original input digest.
Eight read-only server observations found no accepted execution fence; absence
alone does not clear the client's fence. Its private qualification profile is
retained and the request is not replayed. No D model result/ACK is claimed.
Independent E subsequently completes actual request
`8962d263-8e43-44a5-b54d-f4acd50e345e`: model marker present, delivered result
sequence 1, input digest `885f0499f2f90682319c2c23b7b43ba57ede03930588931cf29782dfdabcf885`,
result digest `f1f8dce4e1726214d2ec839acdb9d718dc0a849b22174ae131dffa38e34daa3c`
and local ACK true. Main/E identities remain distinct. Main's original UI then
revokes E; live access closes and cold start denies, while Main identity returns
200. The latest correctly awaited default Main launch again passes all four
startup reads, nine settings, its original Client and two conversations. All Main
table/file hashes remain unchanged after E closes. The final server checkpoint
matches both E digests and durable ACK sequence 1 to the local record, before and
after service restart. Main remains the sole active Client in this node's
issuer/qualification registrations; unrelated Gateway Clients remain preserved.
Six qualification revocation tombstones have completed Relay stages. D has no accepted server fence
and its local unknown submission remains preserved without replay.

## 3. Trust roles and expiry

| Material | Authority/location | Purpose |
|---|---|---|
| Relay descriptor signer | TEST1 stable private signer | Signed discovery and Relay identity continuity |
| Invitation signer | work2 private key; public verifier configured on TEST1 | Owner-approved one-use v3 invitations and connection constraints |
| Grant/bundle signer | Separate work2 private key, independently pinned | Workbench permission and sealed node results; no private key on TEST1 |
| Responder, control peers and each SparkX | Separate device-owned keys | Identity proof, sessions and each peer's own Access/Refresh |
| Native bootstrap secret | One-use Owner-issued credential, encrypted pending state | Provisioning authentication; never a device private key or Relay admin token |
| Relay actor/admin token | Protected TEST1 operator/adapter boundary | Approved registration/recovery administration; never SparkX or the work2 issuer |

TLS remains verified against the Owner-delivered IP certificate; certificate time
and IP/hostname checks stay enabled. The recorded certificate expires on
2027-01-10 at 11:49:21 China time. Replacement trust requires verified identity
continuity or a new Owner-issued credential; renewal is an operational follow-up,
not a reason to skip verification.

Current defaults are one-use invitation 15 minutes, workbench Grant **900 seconds**,
Relay Access **15 minutes** and Refresh **24 hours**. Standing business authority
is `until_revoked`; none of these tokens becomes permanent. Read the actual issued
expiry metadata rather than editing timestamps.

The fixed, operator-approved control pair uses only
`sparkclaw.issuer.control.v1`. work2 signs a seven-day control Grant, checks every
five minutes and rotates before less than 24 hours remain. The TEST1 application
control-grant endpoint checks the pinned signer, exact identity pair/permission/
Relay, fresh possession proof, durable nonce receipt, current stored service-peer
status and monotonic revision/time before saving it. Cold and live service-peer
credential recovery uses its separately qualified, sealed TEST1 adapter; it cannot
enroll a new peer or grant workbench access. These application adapters do not
alter standard ISCP Hello/Ready, envelopes, tickets or Grants.

## 4. Durable lifecycle and data continuity

The new application credential prefix is `sparkclaw-iscp-connect-v1.`; the existing
HTTPS format remains supported. SparkX generates its device key locally, persists
the same installation and operation before network mutation, and performs a real
two-process X25519/OOB-secret handshake with role-specific finished MACs. TEST1
forwards opaque provisioning messages over a restricted ISCP control session.
It has no work2 Grant key, bootstrap secret or business-session plaintext.

The issuer reserves the exact authoritative Client/device binding and prepares
its signed ticket/Grant. TEST1 commits ticket consumption, device, credential
lineage and sealed wrap-key-bound receipt in a database transaction. Lost responses
and process restart replay/reconcile that operation. The issuer keeps its channel,
secret and receipt material encrypted at rest; a changed device/key/input under an
operation conflicts. A second device cannot consume an already-used invitation.

Refresh rotation commits successor credentials and its sealed receipt atomically,
then persists them before use. Grant renewal and both-expired recovery retain the
original device, Client, scopes and revocation revision. Recovery does not consume
another invitation. Ordinary client recovery requires current work2 authorization;
service peers use their distinct stored operator authority. Unknown outcomes retain
the exact pending request/proof/wrap key. A signed `applied` acknowledgement follows
the client's durable save; unacknowledged outcomes and revocation tombstones are
not purged merely because their TTL elapsed.

Revocation commits the business revision first, denies new business and renewal,
invalidates only that peer's session, and durably retries Relay revocation until
its receipt is known. A local row deletion or cached Grant does not imply completed
transport revocation, and a delayed result cannot restore revoked authority.
The original UI revoked C with issuer revision 2 and completed its Relay stage;
B stayed active until its separate original UI self-authorization deletion.
Both native cold starts then deny authorization. Revoked ClientStore IPC remains
locked. Offline SQLite inspection after shutdown confirms the delivered
`ceba5796-dafe-4b10-b0fa-f74acdee9199` record and exactly one acknowledged delivery
remain; this is data retention evidence, not an unlocked revoked workbench.

The current Mac's existing workbench remains visible under its **exact original
deployment/Owner/Client and installation binding**. The private migration adapter
allows `existing_client_id` only for an explicitly configured, fixed one-operation
migration and verifies the existing Gateway Client's active state and Owner.
It reuses that Client; it does not retag SQLite rows, clone conversations, create
another Owner or broaden scopes. This exception is not exposed to arbitrary public
credential callers. New installations use separately authorized Clients/devices.

The recorded Main migration retains deployment
`431c00ce-b780-42e3-9d25-c25c87c8c011`, Owner `owner`, Client
`sparkx-iscp-remote-8143225e` and installation
`f0671ef3-354b-4ec4-b5a4-f581c7604359`, with new device
`sparkx-5f2a903f-060e-483e-bc0f-54f770dfde2b`. A normal default launch without
qualification environment overrides exposes the two original conversations and
nine original settings sections. All 14 SQLite tables and all owned files match
their pre-install hashes; the receipt separately identifies 13 protected tables.
Counts remain two conversations, two messages, one task, two drafts and no files.
`config`, readiness, Owner and Clients reads return 200.

The first migration invitation expired unused. With the issuer stopped, the
operator verified `issued`, revision 1, no device, no operations and no bound
operation, backed up its encrypted row as
`/work2state/expired-unused-migration-row.encrypted.json`, and removed only that
unused issuer row. The Gateway Client and user database were unchanged. Migration
then completed through the newly approved fixed operation
`6ffddd0f-58ba-47b7-8e5d-d47ffe332fb3`. This recorded reconciliation is not a general
expired-row cleanup policy. The recorded server checkpoint confirms the temporary
`MigrationClients` map was removed, with every other configuration field unchanged;
ordinary new-device issuance remains enabled. The final qualified checkpoint has
Main active and six durable qualification revocation tombstones with all Relay
stages complete.

## 5. Evidence and acceptance boundaries

Operator-private evidence root:
`/Users/dev/.cache/sparkclaw-public-iscp-8kr3dw8g/evidence`.
Only redacted receipts and hashes belong in public documentation.

| Evidence | Recorded result | Limit |
|---|---|---|
| `public-ingress.json` | Outside-PVE verified TLS, health/discovery, only new application port 443; admin/metrics 403; work2 no new ingress | Health does not qualify the desktop lifecycle |
| `real-peer-A.json`, `real-peer-B-helper.json` | Real public production Relay registration/encrypted connection and identity/config access to work2 | Helper qualification is separate from full native activation |
| `real-peer-A-refresh.json` | Both tokens rotate; device unchanged; old Refresh returns 401; observed lineage has three rotated records and one active record | Original-operation replay and negative vectors have separate isolated tests |
| `real-peer-A-recovery.json` | Original device recovers with Grant and Refresh both expired | Does not reauthorize a revoked device or change its scope |
| `real-peer-A-revocation.json` | A closes after revoke and cold start is denied; B remains usable | Deliberate qualification device only; no real email was sent |
| `native-B.json`, `native-B-fixed-trace.json` and screenshots | Isolated native initial connection, original UI, automatic renewal and repaired cold start pass; 40-second stable connection/readiness/config/Owner/email-sync/notifications/identity checks | Renewal/cold-start evidence precedes deliberate qualification-peer revocation |
| `native-main-migration.json`, `native-main-cold.json`, `original-data-preserved.json` | Exact-Client migration and `16b00f17` correctly awaited normal default cold start pass; nine settings, two conversations, device list, four startup reads at 200; table/file hashes remain unchanged after E closes | Actual current installation/data acceptance; no full-host reboot or physical LAN claim |
| `final-normal-running.json` | Installed App/helper opened normally without test flags or environment; public `210.16.177.239:443` socket established and App left running | Mac then locked; no new GUI screenshot is claimed. The same `16b00f17` default cold-start/UI and four API reads have separate passing evidence |
| `real-execution-ack.json` | Real model request `ceba5796-dafe-4b10-b0fa-f74acdee9199` delivers `PUBLIC-ISCP-OK`; server/local input and result digests match and local ACK is recorded | A separate QA request was delivered but blocked by existing `semantic_coverage_low` policy; this is not a claim that every model request succeeds |
| `native-public-lifecycle.json` and original settings screenshots | Pass: distinct B/C identities, original invitation creation/copy, rename/schedule cancellation, GUI C revoke, B self-deletion and both native cold denials; offline data/one delivered ACK retained | Revoked ClientStore IPC correctly locks; the initial harness expectation to read it while revoked was corrected to offline retention inspection |
| `server-restart-checkpoint.json` | New paired backups; Relay → issuer → forwarder resume in six seconds, stable signer/TLS, Gateway same container/image and uninterrupted | Covers these new services, not a full host/PVE reboot or native current-profile migration |
| `server-final-activation-checkpoint.json` | Historical first final activation checkpoint: paired backup and migration-map removal, original Main active/four qualification revocations complete; Relay/issuer/control resume with stable signer/TLS and unchanged healthy Gateway; PVE read-only rules/persistence audit passes | Only new services are stopped/resumed; no claim of a full host/PVE reboot |
| `server-final-qualified-checkpoint.json`, `server-final-qualified-integrity.json` | Final 13:37:52.760–13:38:07.225 UTC paired checkpoint: Main active/six revocations complete; E input/result/durable ACK match local; stable authority/execution ledger/Gateway/TLS/signer/Nginx; services active/enabled and public control restored; archives/dump catalogs, SHA/size and 0700/0600 modes verified | New services only; D's unconfirmed submission remains fenced, with no accepted server execution |
| `linux-final-receipt.json` | `16b00f17` supported-platform full Gateway suite: 70 packages pass, 10 have no tests; real PostgreSQL opt-in checks pass | External opt-in/central-fixture gates remain separately declared |
| `node-authority-coalescing.json` and Go test receipts | Client/issuer/workbench/helper real PG race, waiter/mutation/revocation/peer/scopes and same-Grant >10-second no-op tests and vet pass | Native acceptance has its own E/Main receipts |
| `native-final-peer-revocation.json`, `disposable-D-preserved-pending.json` | Main original UI issue/copy, D registration, distinct identities, live revoke/cold denial pass; Main remains active; D's unconfirmed task/input digest/submission fence retained without replay | D has no model result/ACK claim; B and E have separate proven deliveries |
| `native-final-E.json`, `final-E-local-result-ack.json` | Latest helper: original Main UI issue/copy, E registration, distinct Main/E identities, actual model marker/result/server-local ACK parity, original UI E revoke/live close/cold denial; Main stays usable | No claim of every model request succeeding; B's earlier proof and D's unknown submission remain separate |
| `iscp-official-conformance.json` | Official local P0: 37 cases, 31 pass, 0 fail, 6 skip; SDK/conformance package tests pass | Runner uses local crypto, not a remote endpoint; six service/CLI cases are unqualified here |

The requested flow is qualified with the installed package, matched server/local
E input/result/ACK and final paired backups. Original UI/data/LAN remain preserved;
the temporary migration exception is removed. Unexecuted crash windows and official
service vectors remain explicit. No physical LAN, whole-host reboot, hosted-service
comparison or central accepted contract is inferred from these tests.

## 6. Matched backups and rollback

| Layer | Private checkpoint |
|---|---|
| PVE | `/root/sparkclaw-public-iscp-pre-20261012-0822`; VM100 and VM104 snapshot `iscp_pre_20261012_0822` |
| TEST1 | Qualified paired checkpoint `/var/backups/iscp-relay/public-node-final-qualified-20261012`; earlier final `/var/backups/iscp-relay/public-node-final-20261012`; post-rollout `/var/backups/iscp-relay/public-node-post-20261012`; `/var/backups/iscp-relay/checkpoint-zzq2wlFM`; activation `/root/sparkclaw-public-iscp-activate-20261012`; older signer/database `/var/backups/iscp-relay/checkpoint-zmIZ9Lf4` retained |
| work2 | Qualified paired checkpoint `/var/backups/sparkclaw-public-node-final-qualified-20261012`; earlier final `/var/backups/sparkclaw-public-node-final-20261012`; post-rollout `/var/backups/sparkclaw-public-node-post-20261012`; `/var/backups/sparkclaw-iscp-pre-20261012-0822`; stopped rollback container `sparkclaw-gateway-pre-node-20261012` |
| Mac | Qualified application/userdata/eight default-config checkpoint `/Users/dev/.cache/sparkclaw-public-iscp-8kr3dw8g/backups/final-qualified-current`; prior `/Users/dev/.cache/sparkclaw-public-iscp-8kr3dw8g/backups/pre-install-current`; original `/Users/dev/.cache/sparkclaw-public-iscp-8kr3dw8g/backups/mac`; retained prior app `/Applications/.SparkX-pre-public-iscp-20261012.app` |

The rollback container has `restart=no` to prevent two Gateways starting after a
host reboot. The active replacement uses `unless-stopped`. Keep their execution/
Client data and runtime configuration paired; do not start both against the same
mutable state. Backups can contain authority, tokens and personal data and require
operator-only access and paired storage/signing keys. Do not copy their contents
into Git or the desktop credential.
The final checkpoint verifies backup directories at `0700` and archives/dumps at
`0600`; signing/storage material and database snapshots remain paired.
Mac native private keys were not exported. Restoring its matched files requires
the original Keychain; a filesystem backup is not a replacement device key.

Rollback is ordered and limited to this rollout:

1. Preserve pending/unknown business operations and take a current paired checkpoint.
   Stop new enrollment/control admission before changing an authority or database.
2. For work2, stop the new issuer and active Gateway, retain their current recovery
   state, and restore the named rollback container/configuration with the matching
   authorization/execution ledger. Keep original environment, ports, LAN and mounts.
   Re-enable the selected Gateway's intended restart policy only after excluding
   the other instance. Do not blindly restore an old ledger over newer side effects.
3. For TEST1, stop this rollout's Relay/forwarder and restore its matching binary,
   database, descriptor signer, TLS and authority configuration together. Recheck
   stable discovery and legacy/management routes before opening admission.
4. For PVE, remove only the named rollout rules/unit or restore its recorded
   configuration; preserve `8006`, both SSH mappings and unrelated forwarding.
5. For SparkX, restore the matched application, launch selection, private profiles,
   native credential state, SQLite and owned files. Keep the original local Relay/
   issuer and profile until the new path passes native acceptance. Review existing
   backend revocation/unknown outcomes before reconnecting; a file restore does
   not revive revoked authority or authorize re-sending an uncertain task/email.

Requested deployment and acceptance are complete with matched server/native
manifests, current-installation data/UI proof and final qualified backups. D's
disposable unconfirmed submission stays preserved; completion does not authorize
its replay or an old-ledger restore over newer effects/revocations.

## 7. Four additional independent nodes, 2026-10-13

The user selected TEST1's public Relay with an independent issuer on each node.
Source was committed and pushed before rollout. TEST1 Relay, its five control peers
and all five issuers now use `ae56b642`; the existing work2 Gateway image and Mac
`16b00f17` package remain unchanged. The four new Gateways use clean
`git archive 7d5838c2` release images. Host browser checks, four
independent native registrations, real model deliveries and natural Grant renewals
pass. All four final native UI reviews and paired-checkpoint checks pass; the
offline-revoked status-label limitation below remains recorded.

| VM / name | Private address | Existing SSH port | Authority route suffix / deployment |
|---|---|---|---|
| 103 / work1 | `10.20.0.22` | `22022` | `aee06778-2010-470c-83e9-f83572831c1a` |
| 105 / work3 | `10.20.0.24` | `22024` | `219ec0dc-4add-41b0-ab1e-7dd02997de59` |
| 106 / work4 | `10.20.0.25` | `22025` | `6c3485b3-81ed-4414-a3c2-b6ce879e9561` |
| 107 / work5 | `10.20.0.26` | `22026` | `c840a3e8-86d2-4fc4-8cb2-75341cd1e333` |

Routes are `sparkclaw-vm<VM>-<deployment>`. Each VM generated its own invitation,
Grant, responder and outbound control private keys locally. Only public pins and
its own Relay credentials are distributed. TEST1 retains the operator token and
four independent broker keys under `/var/lib/iscp-relay/nodes/vm<VM>`; no issuer
private key is sent there. The existing work2 route and keys remain registered.
Each new issuer uses `/home/ubuntu/.local/share/sparkclaw/public-iscp-node`,
`iscp-node-issuer.service`, its own PostgreSQL database/role and private Unix IPC.

Schema-2 Relay configuration isolates authority by the full domain/Relay/signer/
responder binding, namespaced receipts, operations and device ownership. Routing
accepts only the five approved routes, rejects duplicate JSON keys and does not
accept caller-selected URLs. A route's 32 request slots and 600/minute budget are
independent. Five-node native testing exposed HTTP 429 from the shared-source
Relay budget; the deployment raises its per-IP limit from 1,200 to 3,600 requests
per minute within the existing bounded configuration range. The per-route budget
and authentication requirements remain unchanged. The optional
`SPARKCLAW_WORKBENCH_ISCP_NODE_ROOT` setting retains the six confined Gateway
mounts on ordinary remote restarts. Gateway receives its responder key, public
pins, registry, private issuer socket and its issuer-management token; invitation/
Grant signing keys, issuer storage/database secrets and the Relay operator token
stay outside Gateway.

The legacy migration was rehearsed against an isolated restoration of the actual
TEST1 database. Nine original tables retained their counts and content hashes;
a second startup was idempotent. Original Access/Refresh values were preserved.
Unknown historical operator devices remain unowned rather than being assigned
to a new tenant. The live migration and twelve service-peer registrations passed.
Original Main recovered with its two conversations and existing interface.

Control peers now recover from independent issuer or broker restarts using fresh,
verified, bilaterally exchanged ISCP Hello messages. A private durable watermark
rejects stale session replacement; pending operations retain their original IDs
and uncertain mutations are never replayed as new requests. Public requests with
an invalid inner frame reached each of the five issuers before and after a broker
restart; each new issuer also recovered independently within three seconds.
Those rejection probes establish control reachability, not client enrollment.
Real PostgreSQL race tests cover authority isolation, migration, refresh, replay,
revocation and control-session replacement. Public health/discovery return 200;
unknown issuer routes, public admin and metrics remain denied with 403.

No public port or PVE rule was added in this expansion. Existing outbound NAT and
443 reflection already reach TEST1 from all four VMs. Their previous online model
URLs, model names and credentials are retained as private remote overrides. The
ASR root on 103/105/107 is corrected to avoid the existing duplicated
`/v1/v1/audio/transcriptions` path; original environment files remain backed up.
Existing LAN settings and unrelated services/port mappings are retained.

The four release images are built for Linux/AMD64 from tracked committed source;
the Gateway content ID is `sha256:4350dd1c408e27253aef42091889ef0be37db7c9b4d999971c31d1d3548f006b`.
The standard `start:remote -- --no-build` path still validates the private model
profile, credentials, node mounts, host browser readiness and the in-container
Controller smoke. The old Gateway images retain rollback tags. Three older
service images had missing parent content despite running containers; their
merged filesystems and complete inspect records were privately exported and
rollback images imported before replacement. Original PVE/database checkpoints
remain the primary complete rollback source.

Pinned Chrome for Testing uses its actual managed-policy directory and an exact
root-owned AppArmor executable exception. Global unprivileged-userns restrictions
stay enabled; observed renderers retain seccomp, NoNewPrivs and separate user
namespaces. All four managed userscripts and the native provisioning hash were
verified. Repeated setup on VM106 reuses byte-verified components and preserves
worker URL/policy hashes. VM103/105 browser migration used cold backups and an
isolated candidate; original history/cookie rows and old profiles are retained.

On 103/105, all 664/525 original rows across 37 tables retain their original IDs
and applicable column hashes. The existing migration retires `model_calls.fallback`;
its 41/31 historical values were all false and remain in private backup evidence.
This is not a claim that the old column still exists. On 106/107, original owners
and empty message/run history are retained; new smoke and test records are counted
separately. Original environment and credential-key hashes are unchanged.

Each primary native profile received its own deployment/Owner/Client identity,
completed an actual model-marker request with durable delivery and naturally
renewed a 900-second Grant without changing clock or signed Grant files. Some
initial synthetic prompts received a delivered semantic-routing rejection; those
results remain preserved, and a separate completed test proves model delivery.
Unknown submissions were reconciled by original request ID, never blindly replayed.
Disposable QA profiles registered and refreshed through the actual installed
helper. Only their local Access expiry metadata was made due; both real tokens
rotated and the previous Refresh was rejected with 401. All four QA devices then
reached issuer revision 2/revocation-complete, and their latest Refresh also returns
401; the primary devices remain active. A revoked offline profile currently shows
“Connection unavailable” on cold start rather than an explicit revoked-credential
message until it obtains authoritative revocation state. Cold-denial acceptance
records that UI limitation separately from server revocation and denied business
access; it does not claim an immediate terminal UI lock.

Independent launchers are in `/Users/dev/Applications/SparkX Remote`, named
`SparkX-work1-VM103.command`, `SparkX-work3-VM105.command`,
`SparkX-work4-VM106.command` and `SparkX-work5-VM107.command`. Their dedicated data
is under `/Users/dev/Library/Application Support/SparkX Remote Profiles/VM<VM>/userdata`.
Actual launcher-by-launcher UI checks retain the original sidebar, composer,
file/email/browser entries and all nine settings. Each live device list shows its
primary identity active and disposable QA identity revoked; each completed model
marker is visible in its original conversation. The original default Main selection
remains unchanged. Its two original conversations and nine Chinese settings were
reopened, its live device list still marks the original identity active, and all
14 original business tables and owned-file hashes remain unchanged. Main is left
open on its original workbench. Private profile backups still require the original
macOS keychain to restore device private keys. Physical LAN, host reboot, actual
ASR transcription and real email are not newly qualified by this rollout.

Private evidence root:
`/Users/dev/.cache/sparkclaw-four-remote-20261013-z7mvhnm8`.
PVE snapshots: `sparkclaw_remote_pre_20261013` on 103/105/106/107 and
`sparkclaw_multi_node_pre_20261013` on TEST1. Each new VM's file, database and cold
browser checkpoint is `/var/backups/sparkclaw-remote-pre-20261013`. TEST1's paired
activation checkpoint is `/var/backups/iscp-relay/multi-node-activation-pre-20261013`;
work2 issuer checkpoint is `/var/backups/sparkclaw-iscp/multi-node-pre-20261013`.
Mac's original/default profile checkpoint is `backups/mac-before-four-node`
under the evidence root. Final checkpoints after registration/renewal/revocation are
`/var/backups/sparkclaw-iscp/four-node-final-20261013` on each new VM and
`/var/backups/iscp-relay/four-node-final-20261013` on TEST1. All six/three archive
and dump artifacts respectively have hashes and successful resume receipts;
TEST1 includes all five broker roles and the systemd write-path drop-in. Cold
primary Mac profiles are backed up at `backups/mac-four-node-final`; device keys
still require the original keychain. Artifacts are private and are not committed
to Git.
Rollback must pair the Relay database with its authority/configuration/binaries;
a binary-only downgrade cannot undo ownership migration. Preserve newer client
revocations and unknown business outcomes before considering any restoration.


## 8. Revoked credential deletion installed (2026-10-13)

The user authorized updating installed SparkX and its running backends while
preserving data and configuration. `/Applications/SparkX.app` now uses Client
source `3de41ee4`; work2 and VM103/105/106/107 use Gateway Go source `43b5e35e`.
The Gateway binary was built from a clean archive for Linux/AMD64 and layered onto
each node's existing image. Runtime dependencies, environment, mounts, ports,
model settings, TLS and issuer/signing identities are retained. Each responder
adds only `clients.delete` to its prior qualified operation list. The selected
`latest` image also retains this update on the normal no-build restart path.
Relay, control forwarders, issuer binaries and unrelated local labs were not
replaced during this update.

Native verification uncovered the Electron proxy's conversion of a bodyless
DELETE to an empty ArrayBuffer. The Client now sends the closed JSON body `{}`;
wire regression checks cover that exact conversion. WebChat 304, Desktop 289,
strict packaging and 58 source/five UI/helper parity checks pass. The Gateway's
supported Linux suite has 70 passing packages and ten without tests. The real
installed Main and all four independent profiles each pass the original nine
settings, original identity/conversation checks, deletion confirmation/cancel,
actual deletion of an already-revoked disposable record and refresh. Cold helper
restart and repeated deletion remain denied/removed for that record. Original
user device records were not deleted or revoked during verification.

Each of the five Mac workbenches retains every original row hash across its 14
business tables and all owned-file hashes; the default launch selection is
unchanged. Conversation counts at this update are Main 1, VM103 2, VM105 2,
VM106 1 and VM107 2. All 41 pre-update Gateway tables retain their original
business rows. work2's mailbox watcher, sync job/failure timestamps and internal
policy activation metadata advance normally on startup; a separate restoration
of its retained dump verifies unchanged original email content, drafts and send
records. The audit database was removed after verification, without restoring or
resetting the live database. This record does not qualify new real email or model
submissions.

Private local evidence and paired application/five-profile backups are at
`/Users/dev/.cache/sparkclaw-upgrades/20261013-credential-delete-29a75jj4`.
Each node's database/issuer dump, paired state archive, original Docker inspect,
configuration hashes and upgrade receipt are under
`/var/backups/sparkclaw-credential-delete-20261013-43b5e35e`.
Its stopped original container is
`sparkclaw-gateway-before-credential-delete-20261013` with restart disabled;
rollback must use its paired database and configuration checkpoint, not just the
old binary. These artifacts contain private authority and are not committed.
InfiniCenter remains absent; no central accepted decision is claimed.

## 9. Unbounded RPC installed (2026-10-14)

The user authorized upgrading the local Mac and all five Gateways. Installed
`/Applications/SparkX.app` and work1–work5 binaries now use clean `9fc956e2`.
Each Gateway retains its exact base dependencies, Docker HostConfig, environment,
mounts, ports, network address and command. The normal no-build `latest` image
selects the new release; Relay and issuer binaries are unchanged.

All five installed profiles negotiate `limits.concurrent=0`, complete eight
parallel control reads without a local queue and retain the policy after cold
reconnect. Native Main/four-profile identity, conversations, original nine menus,
device controls and schedule create/cancel pass. Installed 58-source/five-UI/helper
parity passes, with byte-identical original UI. No business operation is submitted
in qualification. The other M1 Pro Mac receives the same arm64 installer; the user
limited direct client installation to this Mac.

All 14 local business tables and owned files retain hashes in each of the five
profiles. Current conversation counts are Main 3, work1 2, work3 2, work4 1 and
work5 2. Use the current pre-upgrade snapshot, not yesterday's count. Default
selection, configuration, identity, permissions and trust pins are unchanged.
Normal Grant/Relay credential renewals update signed IDs, expiry, signatures and
tokens with unchanged bindings. Each Gateway retains the original records from
all 41 business tables; internal startup watcher timestamps may advance.

A separate work1 48-request burst admitted all calls locally but had end-to-end
response timeouts; its root cause is unproven. This remains a reliability/performance
limitation, not a successful 48-call production qualification. Do not replay
uncertain mutations or weaken authorization/deadlines to conceal the result.
Mac 308/Linux arm64 194 and affected Go race gates remain applicable; no new
Linux/x64 test-suite qualification is claimed.

Private Mac backup/acceptance root:
`/Users/dev/.cache/sparkclaw-upgrades/20261014-unbounded-rpc-9fc956e2`.
The prior application is `/Applications/.SparkX-before-unbounded-rpc-20261014.app`.
Each node retains Gateway/issuer dumps, roles, paired authority/configuration/state
archive and original Docker inspect at
`/var/backups/sparkclaw-unbounded-rpc-20261014-9fc956e2` (0700/0600).
Rollback container `sparkclaw-gateway-before-unbounded-rpc-20261014` stays stopped
with restart disabled. Review subsequent effects and revocations before restoring
a paired checkpoint; do not overwrite a live ledger merely to roll back a program.
SSH credentials are retained only in an operator-private file outside Git at the
user's request. InfiniCenter remains unavailable.

## 10. Bounded continuous receive

The public Relay accepts `ISCP_RECEIVE_WINDOW`, default `0s` (the original
reference drain). An explicit positive duration up to `55s` keeps an authenticated
receive socket available for later messages, then sends the existing `drained`
frame. Invalid or larger values fail startup. This setting changes server
scheduling only: no descriptor, JSON frame, envelope, Access-PoP, authorization,
administration or rate-limit contract changes. The original client remains
compatible; its two-minute receive deadline is longer than the configured window.

Each recipient has one active receive owner. Replacement closes/cancels the old
socket and waits for its delivery critical section before advertising `ready`.
Standard WebSocket Ping/Pong confirms liveness before claiming one message;
authentication, ping and writes have five-second bounds. Enqueue wakes the
recipient immediately, with a one-second fallback for persisted messages. Live
revocation and service shutdown close receivers and release their work.

In PostgreSQL continuous mode, one message claim, socket write and delivery mark
share a short transaction. A failed write, cancellation or lost database
connection rolls the claim back, without waiting for a one-minute delivery lease.
The memory queue reserves claimed bytes until completion so a failed write can
restore its original message under the existing capacity limit. Single-message
claims preserve priority/queue selection order without relying on multi-row
`UPDATE RETURNING` order. Default drain behavior is unchanged.

Successful writes retain the reference `delivered_to_connection` meaning. They
do not replace the business result's durable ACK or guarantee that a recipient
has committed the result. A lost transaction commit response remains an unknown
outcome handled by the existing original-request recovery mechanisms.

Qualification uses isolated PostgreSQL 18 and the default memory queue: later
messages on one connection, original-client compatibility, default drain,
replacement, revocation, failed writes, process/DB disconnect rollback, claim
ordering, reserved capacity and shutdown.

On 2026-10-14 TEST1 deployed `f19847c3` with `ISCP_RECEIVE_WINDOW=55s`, retaining
its authority, identity, rate limits and paired PostgreSQL/state checkpoint.
Mac and work2 deployed `3ddde02b`, including private timings, wait decoupling,
optional execution progress and cursor recovery. Other Gateways retain their
existing versions. See the [native before/after measurements and acceptance](iscp-latency-optimization.md)
for deployment scope, preserved state, precise timings and sample limitations.

## 11. Receipt lookup index and HTTP 503 recovery (2026-10-16)

The work3 desktop's 30-second RPC failures were traced to the shared TEST1
Relay database. Work1 and work3 use identical Gateway binaries; the work3
handlers complete in milliseconds. The Relay had approximately 1.27 million
retained delivery receipts but no index matching `MarkMessageDelivered`'s
`domain_id`, `message_id`, `status='queued'` predicate. Each delivery scanned the
receipt history before its receive loop could advance. Queue delay accumulated
in both directions until the desktop RPC deadline reset the encrypted session.

The local Relay now prepares a partial index on `(domain_id, message_id)` for
queued receipts after the pinned upstream migrations. Preparation is idempotent
and verifies that the index is valid. This is a SparkClaw deployment index;
upstream ISCP migrations, receipt contents, retention, authorization, wire frames,
transaction boundaries and RPC deadlines are unchanged.

For a large running database, apply the statement from
[`schema.sql`](../services/gateway/internal/iscprelayreference/schema.sql) with
`CREATE INDEX CONCURRENTLY IF NOT EXISTS` before starting the new binary. Execute
it outside a transaction, using the established operator database connection.
Verify `pg_index.indisvalid` and `indisready`, then use `EXPLAIN` on the delivery
UPDATE to confirm the partial index is selected. If a concurrent build is
interrupted and leaves an invalid index, remove only that invalid index with
`DROP INDEX CONCURRENTLY` and retry; do not clear receipts or business state.
Fresh databases use the ordinary startup index creation. An index-only live
repair persists across restarts and does not require replacing client credentials.

On TEST1 the index was built online in 4.54 seconds without restarting Relay.
An equivalent read-only lookup changed from a history scan touching 132,172
blocks in 557 ms to an index lookup touching two blocks in 0.07 ms. All authority
fingerprints remained identical; receipt counts increased only with live traffic.
The affected desktop reconnected automatically. Forty consecutive settings,
mail-status and notification reads returned HTTP 200 in 219–537 ms. These are
measured reads, not a new model or email-send benchmark.

The PostgreSQL 18 regression seeds 20,000 historical receipts and reproduces
the old sequential scan, verifies index selection and idempotent preservation,
and checks domain/status isolation of the original delivery update. Existing
receive rollback, revocation and connection-replacement tests remain applicable.

## 12. Isolate individual RPC deadlines (2026-10-16)

The Workbench endpoint now ends only the affected call when its caller cancels,
its response deadline expires, or its Relay POST times out. A timed-out fresh
response-authorization check still denies that result, without invalidating
other calls. Each subsequent response requires its own fresh authorization.
The authenticated receive owner and peer heartbeat continue to detect actual
connection loss. Signed revocation, expired grants and invalid encrypted frames
retain their existing terminal behavior.

RPC correlation IDs cannot be reused within the same encrypted session, even
after a timeout. Late responses are discarded, and an old submit failure cannot
disconnect a replacement session. Durable operation/request identities remain
unchanged: a timeout proves neither non-delivery nor non-execution and never
automatically replays a mutation. Existing receipt reconciliation is retained.
Ordinary/mail deadlines, wire frames, capability limits, credentials, stored data
and the original desktop UI are unchanged.

Regression coverage uses real encrypted v1/v2 forward and v2 reverse calls,
concurrent deadlines/cancellation, late replies, POST deadlines and sequence gaps,
fresh authority failure, replaced sessions and heartbeat loss. The desktop IPC
test verifies that a timeout or a single 503 leaves the same helper usable for
other requests. Upstream receipt-index work is separately submitted in
[ISCP PR #15](https://github.com/Infinimesh-ai/ISCP/pull/15); an open PR is not an
upstream release or a new Relay deployment.

## 13. TEST1 Relay upgrade (2026-10-18)

At the user's request, TEST1 now runs source
`19408777070067a3641acc873f234936633d59b7`, the verified upstream `main` at
deployment time. The Go 1.25.12 Linux/amd64 artifact and running process both have
SHA-256 `0b4e3a8bcb2279bbae76593fa9388c16b3c6ff987fadf6be5a1a21bc7620a052`.
The previous `f19847c3` binary is retained. Existing 55-second reception, rate
settings, TLS, signer, authorities and forwarder binary are unchanged; the
already-valid receipt index is now also checked by startup.

The full PostgreSQL 18 online backup is 12,291,967,601 compressed bytes and
passed complete decoding, with SHA-256
`504fe976236c50a37864aa990171871e5b11e4f8ae9e7b26313b612ddaf8dc9d`.
The private TEST1 checkpoint is
`/var/backups/iscp-relay/upgrade-20261018-19408777`; it contains the database/roles
backup, cutover configuration/state archive, previous binary and deployment
receipt. The online database and later cutover archive have separate timestamps.
No database restore or cleanup occurred.

All 45 protected file contents, permissions and owners match. Original keys in
25 durable tables remain, including 5,567,588 messages and 5,567,732 receipts;
11 identity/authority/migration tables also retain every original row hash.
The two expiring replay caches are excluded from permanent-key preservation.
Large-history verification compares IDs, not every payload byte. Database column
and index definitions are unchanged, allowing rollback to the retained compatible
program without restoring an older database over newly accepted work.

Relevant PostgreSQL 18 race tests, vet and actual Linux/amd64 fresh/repeated
startup passed. Verified-TLS health/readiness/liveness/discovery return 200,
unauthenticated administration remains denied, the forwarder is active and all
18 previously live devices reconnect with live message delivery. Private build,
preservation and monitoring evidence is under
`/Users/dev/.cache/sparkclaw-relay-upgrade-20261018-19408777`.
No Gateway, installed desktop, UI, external contract or application-stream
proposal changed in this rollout.

The final two-minute observation recorded 1,980 messages received and delivered.
All 103 sampled HTTPS health reads after readiness returned 200; two sampled
requests during the planned cutover returned non-200. Both services remain active,
with zero Relay automatic restarts and no post-readiness error-priority or panic
records in the inspected journals.

## 14. Matched live streaming rollout (2026-10-18)

The user subsequently authorized upgrading the server chain used by the default
desktop connection. Work2 Gateway and node issuer, and TEST1 Relay and control
forwarder, now run `bef007e8627daa484a143d3bdf9e0dcce8595824`. Running process and
container binary hashes match the Linux/amd64 build receipt. Other Gateway nodes
retain their existing versions. The browser Controller and App-CLI processes were
not restarted; their source and release remain unchanged.

The user explicitly waived new database backups during this rollout. The partial
online transfer was stopped and removed. Only old programs, Docker configuration
and private configuration/identity files were retained for rollback; no database
was restored or cleared. TEST1 retains all 5,708,614 original message IDs,
5,708,722 receipt IDs, keys in 23 other durable tables, and row hashes in 11
identity/authority tables. Its schema and 44 protected files are unchanged.
Large-history ID checks do not assert equality of every payload byte.

Work2 retains the original rows in 41 business tables, eight issuer record keys,
44 protected configuration files and 883 workspace files. Existing mailbox
runtime cursors and renewal fields are normalized by the documented preservation
policy; business content, mailbox identity/configuration and draft/send records
remain protected. Docker environment, HostConfig and network addresses match.
Gateway health, issuer status and pinned-TLS Relay connectivity pass. The new
Relay also passed fresh and repeated startup against a disposable PostgreSQL 18
database on the actual Linux/amd64 host.

The freshly packaged ARM64 SparkX application used the original connection and
installation identity with a separate, empty local ClientStore. No original
client database was copied or migrated. A real-model request produced 1,123
final characters through 109 authenticated delta frames and 96 renderer preview
updates. The first renderer text update appeared after 7,138 ms; this is one live observation,
not a percentile benchmark. Exactly one subscription and one durable commit
completed, the normalized visible/final digests match, and both the local receipt
and server fence record delivery. There were no blank, missing, duplicate or
remounted message rows, and no script-assisted scrolling. The original nine
settings entries and schedule-create/cancel interaction also pass.

The first isolated-profile attempt generated a new installation ID and was
correctly rejected by the enrolled connection binding. Preserving the original
installation identity in the empty test store fixed that harness error. Two
inspection-import errors were corrected before any model task was submitted;
failure evidence is retained. All 19 tables across the original client's two
business databases, its authentication and launch selection match their cold
fingerprints. The installed application was not replaced and its normal launch
was restored after testing. This verifies the latest package, not an installation
of it over the user's existing app.

The [sanitized deployment receipt](evidence/live-stream-rollout-2026-10-18.json)
records hashes, counts and scope. Full private evidence and old programs are
retained by the operator. Existing public contracts are unchanged; InfiniCenter
0036 upstream standardization remains proposed.

## 15. Local client installation and live acceptance (2026-10-18)

The user then authorized upgrading the local client while preserving configuration
and chat history. `/Applications/SparkX.app` now runs the packaged macOS ARM64
product revision `bef007e8`. All 277 application files/symlinks and executable
bits match the previously qualified package. The old application and private
configuration are retained; no database was backed up, copied, restored or reset.

Cold read-only checks cover five history profiles, the default selection and
profile launchers: all 20 original messages, original records across 95 tables,
and all 72 configuration/authentication files remain. Only an empty welcome
draft's revision advances during the verification request; original draft content
is unchanged. All ten databases pass integrity checks. The daily ClientStore
migrates from schema 10 to 11 by adding `message_deliveries`, preserving old message
IDs and content. The four unopened profiles remain unchanged at schema 10 and
will migrate when opened. An older schema-10 application must not be substituted
after migration without checking schema compatibility.

The installed application used its original daily identity and existing work2 /
TEST1 connection. Both old conversations and all six daily-profile messages opened
in the original UI. One new verification conversation received 112 delta frames
and 104 renderer preview updates, with the text tail visible in all 104 recorded
animation-frame observations. The 1,126-character result committed once, retained
the same message node, and matched the final stored digest. There were no blank,
missing or duplicate rows, no remounts and no script-assisted scrolling. First
renderer text appeared after 7,095 ms in this single sample; this is not a latency
percentile or continuous video measurement.

The private trace directory initially had mode 0755, so the secure logger correctly
disabled itself and the script's log-based ACK wait timed out. This harness failure
is retained. Independent cold SQLite inspection confirms one acknowledged delivery,
and the server's original request fence is `delivered`. No model request was repeated.
A subsequent restart preserved the final message/digest; the original nine settings
entries and schedule-create/cancel interaction pass. Normal launch is restored,
with diagnostic environment overrides and inspector arguments removed.

The [sanitized installation receipt](evidence/local-client-upgrade-2026-10-18.json)
records these checks. Private evidence and the previous app are under
`/Users/dev/.cache/sparkclaw-upgrades/20261018-bef007e8`. Existing public contracts
and upstream standardization status are unchanged.

### Mail action alignment follow-up

The installed client's mail icon was 5.5 CSS pixels above its neighboring composer
icons. Revision `07002f06` adds the missing `align-self: end` to the existing mail
button. Native checks across 96 combinations of window width, zoom, theme, text
size, welcome/conversation view and one/six-line input report zero icon-center
offset. Mail open/close and focus restoration pass. The installed Mac app also
measures zero offset and retains the original UI and all eight daily messages.

Fresh ARM64 DMG/ZIP packages are in Downloads with `20261018-07002f06` filenames;
source/UI/helper and all 277 application file/symlink comparisons pass. Cold checks
retain all rows in 96 tables and all 22 messages across five profiles, with no
schema change or database copies/backups. Of 72 protected configuration files,
71 are byte-identical; one grant renewed only its ID, validity times and signature,
preserving connection identity and permissions. Normal launch is restored. This
CSS-only source change does not alter the streaming implementation or servers.
See the [sanitized receipt](evidence/composer-mail-alignment-2026-10-18.json).

## 16. Local browser fixes installed (2026-10-20)

The local macOS ARM64 client now runs product revision `f41f9367`, including
`c19a0abf` (replacement-document navigation), `dec4bb28` (workbench reload/crash
cleanup), and `f41f9367` (trusted browser hiding after connection loss). Personal
navigation preserves genuine failures; retained pages require explicit selection
after the workbench releases their presentation. Original menus, layout and
interactions remain, and all five built UI files match the previous installation.

Desktop 479, focused UI 36 and native layout 48 checks pass, including actual
close/reactivation with connection loss, redirect/new-page/popup handling,
renderer reload/crash and retained input. Strict UI build, Go build/vet and all
75 Linux test packages pass. The macOS full suite encounters Linux-only
`/dev/shm` tests; path/socket checks require a canonical, short temporary
directory. Initial environment and cleanup failures remain in private evidence.

Fresh DMG/ZIP packages and SHA256SUMS use `20261020-f41f9367` filenames in
Downloads. Source/UI/helper audits and all 277 application entries match across
the build, mounted DMG, extracted ZIP and `/Applications/SparkX.app`. Cold checks
retain every original row across 97 tables, all 22 messages, draft contents and
stable identity/permission configuration. Of 90 protected files, 85 are identical;
two credential JSON files renew normally and three mutable encrypted transport
journals advance. No database is copied, backed up or reset. The previous program
is retained separately.

The installed app retains the original conversation, nine settings sections,
active device and mail popup/focus behavior. Collapsing a native page and reopening
the window leaves the conversation unobscured. Normal launch is restored without
inspection arguments. No model task or server deployment is performed;
cross-project contracts remain unchanged. Packages remain unsigned and
unnotarized. See the
[sanitized installation receipt](evidence/desktop-browser-fixes-install-2026-10-20.json).

## 17. Receipt retention and automatic browser rollout (2026-10-20)

Product `094deb8f` is published and installed on the current ARM64 Mac, with
`c0d6f5e9`, `0fbbbaa1` and `26a8df49`. ISCP and pinned HTTPS LAN establish the
local browser automatically. The separate authorization button and renderer API
are removed; original browser tabs, panel controls and task permissions remain.
The installed ISCP client connects its Host Agent without a click, reads the
original conversation and opens/closes the browser panel and mail popup. Normal
startup is restored, with no inspector arguments or listening inspection port.

Fresh `20261020-094deb8f` DMG/ZIP and SHA256SUMS are in Downloads. The public
allowlist, 66 source files, five UI resources and helper match the source;
all 277 application entries match the build, mounted DMG, extracted ZIP and
installed app. Packages remain unsigned/unnotarized. Existing source validation
covers Desktop490, WebChat361, Linux75/build/vet and actual native LAN/ISCP,
including seven ISCP capture modes; product source is unchanged since those gates.

The work2 Gateway runs a new image with the seven-day business-result retention,
earliest-expiry index, hourly reconciliation, startup/capacity triggers and
bounded batches. The remote checkout also advances to `094deb8f`. A root-private
recreation recipe pins the image and preserves the original environment, mounts
and network addresses. Its read-only validation passes and recreation refuses an
existing target. A separately prepared `0fbbbaa1` image supports the new journal
format for rollback; never restart the retained `c0d6f5e9` container as a writable
rollback or delete deduplication indexes. Both images pass UID execution and
actual document read checks. Relay/issuer versions and other nodes are unchanged;
the issuer is active and the live ISCP path works.

Preservation checks retain the original records in 41 server business tables,
904 protected configuration/workspace/identity files and all 65,758 original
receipt-directory files. The new catalog exists and new completed receipts carry
authenticated timestamps; no retention warnings occur. Legacy records remain,
and production expiry is not claimed before the seven-day deadline. The Mac's
six profiles retain 22 messages, conversations, drafts and stable identities
across 97 checked tables. One existing event projection advances within its
original scope, and two encrypted control/transport journals advance normally.
No database copies, backups, resets or new model tasks are created. The previous
application and compatible server rollback are retained.

Initial old-Docker `COPY --chmod` rejection and overly broad comparisons of
mutable event/control journals remain in private evidence. The corrected image
build and scoped preservation checks pass. See the
[sanitized rollout receipt](evidence/receipt-browser-rollout-2026-10-20.json).

Remote CI for `094deb8f` is **not green**. Run39380313739 passes Docs, Gateway
race, WebChat and ASR, but fails the Gateway temporary-directory cleanup race,
Electron readiness/SUID sandbox setup and a Compose error-message assertion.
The previous `667566a7` run39370919665 has the same failure signatures (the cleanup
race appears in its race job). These existing CI failures remain unresolved;
local product gates and live deployment checks above do not imply remote CI success.
