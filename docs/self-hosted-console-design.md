# Self-hosted Console Design

> Language: English | [简体中文](../zh-cn/docs/self-hosted-console-design.md)

Date: 2026-11-19. Status: C0–C2 implemented and deployed on TEST1 (section 9); C3 waits for the real console.

This document covers only the console side: domains, device binding, and the issuance of access credentials and Grants. How devices and the backend communicate is in [Client link layer design](client-link-layer-design.md). The two documents are maintained separately.

## 1. Premises

The user confirmed these premises on 2026-11-19.

1. In production, devices and the backend join the same domain on the official console, and the console issues access credentials before they can communicate.
2. The console only binds devices, pairs them and issues credentials. Messages do not pass through it.
3. The self-hosted console is the development environment of that same method. We build it ourselves and run it on our own host. It is not a second connection method.
4. The self-hosted console must match the official console in form, not interface by interface. It exists to make the later connection easier; differences from the real console are adjusted when that connection is made and are not weighed at length now.
5. Only one way to join is built: issuing a SparkClaw access credential. The official console's legacy pairing code and its access tickets for other products are not implemented.
6. The three clients do not use ITES sign-in for now.
7. One backend serves one person, so one domain corresponds to one person and one backend.
8. Credentials follow the console's domain model. The existing node issuer is a different issuance scheme and is retired at the end.

## 2. The official console today

On 2026-11-19 the signed-in console pages and the console's public front-end script were read. No state-changing interface was called.

| Concept | Meaning on the official console |
|---|---|
| Account | Signs in with a phone number and maps to a user ID |
| Domain | The isolation boundary for devices and trust. Personal domains are supported today, with active and suspended states |
| Device | Name, type and role (for example `phone / owner_device`), trust state and Relay state (for example `trusted / active`), public-key fingerprint, bound account |
| Pairing authorization (Trust Grant) | A short-lived runtime credential naming the peer device, the permission and the expiry. It can be renewed by hand or through bounded automatic renewal (a renewal authorization expires after 90 days by default). Each renewal needs proof from the device's original private key, and the console never gives a device a long-lived credential |
| Ways to join | A legacy one-time pairing code `pair_…`; or a one-time access ticket signed by the trust root, pasted or scanned |
| Revocation | The device loses connection and authorization at once and must pair again |
| Audit and usage | A read-only record of management operations in the domain; usage and quota |

The console notes that directory state does not mean a device is online; being online is defined by an end-to-end session reaching ready.

The console calls these interfaces under `/v1`:

- `accounts/login`, `logout`, `me`, `register`
- `domains`, `domains/{domain}`
- `domains/{domain}/devices`, `devices/{device}`, `devices/{device}/revoke`
- `devices/{device}/grant-renewals`, `devices/{device}/renewal-authorizations`
- `domains/{domain}/pairing-tickets`, `domains/{domain}/provisioning-tickets`
- `domains/{domain}/audit`, `usage`, `quota`

Two clients of the real console's interfaces already exist on the device side: `ManagedCloud.java` in JingSi Android and `CloudAPIClient.swift` in JingSi iOS.

## 3. What the self-hosted console does

### 3.1 Responsibilities

| Does | Does not |
|---|---|
| Account sign-in (the simplest workable form in development, without ITES) | Relay or store any message, conversation or attachment |
| Create and show domains | Manage feature availability (the link layer decides from device conditions) |
| Let the backend join a domain | Legacy pairing codes or access tickets for other products |
| Issue SparkClaw access credentials | Usage, quota or billing |
| List, rename and revoke devices | Domains with several people or several backends |
| Issue Grants and renew them automatically | |
| Keep an audit record | |
| Tell devices which Relays to use | |

### 3.2 Device types and roles

This follows the official console's "type / role" notation.

| Device | Type | Role | Count |
|---|---|---|---|
| Backend | `sparkclaw` | `domain_host` | Exactly one per domain |
| Desktop | `desktop` | `owner_device` | Unlimited |
| Phone | `phone` | `owner_device` | Unlimited |
| Watch | `watch` | `owner_device` | Unlimited |

Rules:

- A Grant is issued only between an `owner_device` and the `domain_host`. There are no Grants between `owner_device`s, so the three clients are not connected to each other.
- The peer of every Grant is the domain's `domain_host`.
- There is one Grant permission name: `sparkclaw.link`. Feature availability is not expressed through permissions.
- The link layer uses the type as the device class, which decides projections and available features.

`phone / owner_device` matches what the official console already uses. `sparkclaw / domain_host` is defined by this design; if the real console uses other names, only the adapter maps them.

### 3.3 Data

| Record | Content |
|---|---|
| Account | User ID, sign-in method |
| Domain | Domain ID, owning account, state (active or suspended) |
| Device | Device ID, domain, name, type, role, public-key fingerprint, state (trusted or revoked), created and updated times |
| Access credential | One-time; domain, target device type, expiry, whether it has been used |
| Grant | Grant ID, device, peer, expiry, state; expiry of the renewal authorization |
| Audit | Who performed which management operation and when |

A device's private key stays on the device. The backend's private key stays on the backend. The self-hosted console stores only public keys and its own trust-root signing key.

### 3.4 Three flows

**The backend joins a domain**

1. The user creates a domain on the self-hosted console.
2. The user issues an access credential for the backend with target type `sparkclaw`.
3. The backend's ISCP connector registers with it: it submits the backend public key and becomes the domain's `domain_host`.
4. A domain allows one `domain_host`. A second registration is refused.

**A device joins a domain**

1. The user issues a SparkClaw access credential on the self-hosted console and picks the device type.
2. The page shows the credential as a QR code and as copyable text. It is valid once, is shown only this one time, and has an expiry.
3. The device scans or pastes it, generates its own key, registers with the credential and submits its public key.
4. The self-hosted console records the device and issues the Grant between this device and the `domain_host`.
5. The device also receives the domain ID, the `domain_host`'s device ID and public key, and the Relay node list. After this the device no longer needs the access credential.

A device may join before the domain has a `domain_host`; its Grant is issued once the backend joins.

**Renewal and revocation**

- A Grant is short-lived. Before expiry the device proves its identity with its private key and receives a new Grant. The binding, the key and the peer do not change.
- Automatic renewal has an absolute limit, 90 days by default as on the official console. After that the user authorizes again on the console.
- Revocation takes effect at once: the Grant is void, and the backend rejects the device after syncing the revocation. The device must go through "a device joins a domain" again.

### 3.5 Pages

The layout follows the official console and keeps only what is needed:

- Domain list and domain overview
- Device list and device detail (name, type and role, state, public-key fingerprint, Grant state and expiry, revoke)
- Issue an access credential
- Audit

### 3.6 Interface to the link layer

The link layer reaches the console only through the console adapter, which exposes four operations:

| Operation | Caller | Purpose |
|---|---|---|
| Join the domain | Devices, backend | Register with an access credential, submit the public key, receive the identity in the domain and the connection information |
| Obtain and renew a Grant | Devices, backend | Get and replace the short-lived Grant |
| Sync the device directory | Backend | Get the domain's device list and revocation state and keep a local copy, so devices can still be verified on the LAN without internet access |
| Leave the domain | Devices | Remove the binding |

Connecting the real console changes only the adapter. The existing console clients in JingSi Android and iOS can be the starting point for the real-console side of the adapter.

## 4. Implementation

- **Starting point**: the reference trust root in the ISCP repository (`services/trust-root-reference`). It already has the basics of device registration, Grant issuance and pairing tickets.
- **To change**: the reference implementation caps a `text` Grant at one hour and does not support renewal. The self-hosted console follows the official console: Grants last days, with bounded automatic renewal.
- **To add**: domains and accounts, issuing and one-time consumption of access credentials, device types and roles, device-directory queries, audit, and pages.
- **Code location**: in this repository, marked as development only (working name `cmd/dev-console`).
- **Deployment**: TEST1, on a new port, beside the existing Relay. The Relay stays in use with the same role.

## 5. Relation to existing parts

| Part | Today | Afterwards |
|---|---|---|
| Relay on TEST1 | Forwards ISCP traffic | Unchanged |
| Control forwarder on TEST1 | Forwards issuance requests to the node issuer on work2 | Retired together with the node issuer |
| Node issuer on work2 | Issues 900-second Grants to desktop and phone | Kept for installed clients during the migration; retired once every client is on the new link layer |
| Desktop settings device page and one-time credential QR code | Issues node-issuer invitations | The entry stays; any change follows the project's UI change rules and is confirmed separately |

## 6. Phases

| Phase | Content |
|---|---|
| C0 | Finalize the data and the three flows, the access-credential format, and the four adapter operations |
| C1 | Trust-root changes: Grants lasting days, automatic renewal, one-time access credentials, types and roles; run "backend joins, device joins, renewal, revocation" end to end from the command line |
| C2 | Pages; deployment to TEST1 |
| C3 | When connecting the real console: write the real-console side of the adapter and reconcile differences |

Link-layer integration can start once C1 is complete, without waiting for the pages.

## 7. Deferred until the real console is connected

These are not resolved now and are adjusted in C3 according to the real console:

- Which type and role the backend should use on the real console. This design defines `sparkclaw / domain_host`.
- Whether the real console's current flow directly supports several devices each holding a Grant with the same backend. The ticket flow on the console pages is designed for one agent endpoint and one controlling phone.
- Differences between the access-credential format and the console's current access ticket.
- How far the reference trust root is from the target. Only the one-hour cap and the lack of renewal have been confirmed.

## 8. Items that need separate user approval

- Opening a new external port on TEST1 is a public-ingress change and needs approval before it is done.
- When the node issuer and the control forwarder are switched off.

## 9. Implementation (2026-11-19)

| Phase | State |
|---|---|
| C0 | Done. The access credential, the device-facing interface and the four adapter operations are fixed in `services/link/consoleapi` |
| C1 | Done. Implemented in `services/link/devconsole` rather than by changing the ISCP reference trust root: it reuses the ISCP identity and Grant primitives, and its signer key is persistent |
| C2 | Done. Pages and deployment on TEST1 |
| C3 | Not started: it needs the real console |

**Code and commands**
- `services/link/devconsole` and `cmd/dev-console`: the console.
- `services/link/consoleapi`: the adapter devices and the backend use.
- `services/link/cmd/link-connector`: the backend side. `join` and `run`.

**Access credential**
- One-time, with a 12-digit code shown as `1234 5678 9012`.
- Only the code's digest is stored.
- The full text, `sparkclaw-link-v1.<base64url JSON>`, carries the console address, its TLS pin, the code, the target device type and the expiry.
- A device whose trust configuration already names the console may enter the code alone; the watch does.
- Valid 10, 15 or 30 minutes.

**Grants**
- Permission `sparkclaw.link`, valid 72 hours, renewed when a third of the lifetime (at most 24 hours) remains.
- Renewal authorization runs 90 days from joining, and can be re-authorized on the device page.
- Each renewal is proven with the device's own key.
- Revoking a device raises its revocation epoch, so older Grants stop working at once.

**Relay registration**
- The console registers each joining device at the TEST1 Relay with the Relay's operator token.
- This needed one Relay change, ISCP `59057fe`, so that such devices get duplex-capable, refreshable credentials.
- The node-authority file is unchanged.
- When a device's Relay refresh credential has lapsed, the console registers it again on request (`/v1/link/relay-access`).

**Sign-in**
- A single administrator password.
- Operator scripts use an API token, of which only the hash is stored.
- No ITES.

**Pages**
- Rendered by the server with the official console's stylesheet and wording.
- Domains, overview, devices, device detail (Grant, renewal, revoke), 配对 (issue a credential, QR code and code shown once), and audit.

**Deployment**

| Item | Value |
|---|---|
| Service | `sparkclaw-dev-console` on TEST1 |
| User | `sparkclaw-console` |
| Data directory | `/var/lib/sparkclaw-dev-console` |
| Listens on | `10.20.0.20:18450` |
| Public address | `https://210.16.177.239:18450`, through the PVE forwarding unit `pve-sparkclaw-console-18450` |
| TLS pin | `wASUGRi9ViqrMbUgnpOLj3ZyQ87VtzHiDn6Rah2Ofg8` |

The backend `SparkClaw work2` is the `domain_host` of the domain 我的 SparkClaw.

**Differences from the design**
- The console stores its state in a JSON file rather than PostgreSQL, because it is single-user and development-only.
- The admin JSON interface is shaped like the official `/v1` interface but adds `PATCH /v1/domains/{d}` for renaming and suspending.
