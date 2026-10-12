# Public ISCP Node Deployment Record

> Language: English | [简体中文](../zh-cn/docs/public-iscp-node-deployment.md)
>
> Date: 2026-10-12. Status: server rollout recorded; desktop qualification is
> in progress. The repaired isolated native relaunch passes; the current Mac
> installation/migration and B/C business/UI acceptance are not complete. This record must not be read as installation success.

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
The isolated desktop retry candidate is `946c2345`: it reuses the existing
30-second ISCP protocol budget and allows one in-flight retry. The LAN budget
remains five seconds and the repair changes no UI. Its recorded package audit
matches 57 source files and five UI assets; ASAR SHA-256 is
`cdddbc116adcb0e8285d455109b261322d29d7c419e1d34a43dd3580b05fca73`,
helper SHA-256 is
`b87cd59bc6578ce9b1a7828e08a2cb4a46c1e4c9d54584034d93dc16a8ba6264`.
Desktop tests report 275 pass. These candidate checks do not attest to a final
current-Mac installation or migrated business-data acceptance.

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

The current Mac's existing workbench must remain visible under its **exact original
deployment/Owner/Client and installation binding**. The private migration adapter
allows `existing_client_id` only for an explicitly configured, fixed one-operation
migration and verifies the existing Gateway Client's active state and Owner.
It reuses that Client; it does not retag SQLite rows, clone conversations, create
another Owner or broaden scopes. This exception is not exposed to arbitrary public
credential callers. New installations use separately authorized Clients/devices.
Final native migration, selection, secure-storage and data parity remain pending.

## 5. Evidence and remaining gates

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
| `native-B.json`, `native-B-fixed-trace.json` and screenshots | Isolated native initial connection, original UI, automatic renewal and repaired cold start pass; 40-second stable connection/readiness/config/Owner/email-sync/notifications/identity checks | Current Mac installation/migration and B/C model/Owner-create-copy/revoke gates are still in progress |
| `server-restart-checkpoint.json` | New paired backups; Relay → issuer → forwarder resume in six seconds, stable signer/TLS, Gateway same container/image and uninterrupted | Covers these new services, not a full host/PVE reboot or native current-profile migration |
| `linux-final-receipt.json` | Supported-platform full Gateway suite: 70 packages pass, 10 have no tests | External opt-in/central-fixture gates remain separately declared |
| `iscp-official-conformance.json` | Official local P0: 37 cases, 31 pass, 0 fail, 6 skip; SDK/conformance package tests pass | Runner uses local crypto, not a remote endpoint; six service/CLI cases are unqualified here |

Before completion, qualify B/C model request/result/ACK, native Owner invitation
creation/copy, one-device revoke and current-installation migration with original
UI/data/LAN preserved. Keep the recorded source/package/helper hashes matched to
any subsequent candidate. Unexecuted crash windows and official service vectors
stay explicit. No hosted-service comparison or central accepted contract is
inferred from local protocol tests.

## 6. Matched backups and rollback

| Layer | Private checkpoint |
|---|---|
| PVE | `/root/sparkclaw-public-iscp-pre-20261012-0822`; VM100 and VM104 snapshot `iscp_pre_20261012_0822` |
| TEST1 | Post-rollout paired checkpoint `/var/backups/iscp-relay/public-node-post-20261012`; `/var/backups/iscp-relay/checkpoint-zzq2wlFM`; activation backup `/root/sparkclaw-public-iscp-activate-20261012`; older signer/database checkpoint `/var/backups/iscp-relay/checkpoint-zmIZ9Lf4` retained |
| work2 | Post-rollout paired checkpoint `/var/backups/sparkclaw-public-node-post-20261012`; `/var/backups/sparkclaw-iscp-pre-20261012-0822`; stopped rollback container `sparkclaw-gateway-pre-node-20261012` |
| Mac | `/Users/dev/.cache/sparkclaw-public-iscp-8kr3dw8g/backups/mac`; an additional current pre-install application/profile/data checkpoint is required before installation |

The rollback container has `restart=no` to prevent two Gateways starting after a
host reboot. The active replacement uses `unless-stopped`. Keep their execution/
Client data and runtime configuration paired; do not start both against the same
mutable state. Backups can contain authority, tokens and personal data and require
operator-only access and paired storage/signing keys. Do not copy their contents
into Git or the desktop credential.

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

Current server deployment and qualification do not close these desktop gates.
The paired post-rollout service checkpoint is recorded. Final current-installation
migration and native business/UI evidence must be added before this record can
state that the whole rollout is accepted.
