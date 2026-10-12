# Public ISCP Node Deployment Record

> Language: English | [简体中文](../zh-cn/docs/public-iscp-node-deployment.md)
>
> Date: 2026-10-12. Status: requested rollout and acceptance completed. Services
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
