# Browser Email Workflow Design

> Language: English | [简体中文](../zh-cn/docs/browser-email-workflow-design.md)

Status: Implemented. QQ Mail, Outlook, and Gmail use deterministic Playwright
CLI handlers through the production SparkClaw Browser Bridge. The Phase 6
cutover removed the former Host-CDP runner without changing the send-only
Workflow, approval, or unknown-outcome semantics.

## Decision

SparkClaw owns browser-backed email under the browser branch:

```text
browser
`- browser.email
   `- browser.email#send
```

`browser.email` has one Workflow Profile at revision 1 and one operation,
`send`. Email reading is not registered. Requests to inspect an inbox, read an
unread message, reply, forward, or manage drafts do not receive email tools.

The model chooses only the supported function and supplies one recipient, an
optional subject, and a plain-text body. Runtime owns provider and account
selection, login admission, Browser control credential generation, provider
script revision, approval, invocation identity, and result validation.

## Supported Surface

The current capability supports:

- one new plain-text message to exactly one recipient;
- an optional single-line subject;
- one effective signed-in account per provider;
- QQ Mail, Outlook, and Gmail;
- manual login in the persistent SparkClaw Browser;
- deterministic read-only login probes and one-attempt send handlers;
- one exact-content approval immediately before the external send effect.

It does not support reading, search, listing, replies, forwarding, deletion,
archive, read-state changes, reusable drafts, multiple recipients, CC/BCC,
attachments, HTML authoring, signatures, or multiple accounts per provider.
It does not use OAuth, IMAP, SMTP, Gmail API, Microsoft Graph, cookie export,
profile copying, container Chromium, or a fallback browser backend.

## Routing And Provider Resolution

The semantic graph contains one `browser.email` candidate for `send`. Hard
negatives include inbox inspection, email reading, attachments, replies, login
checks, and requests that merely open a provider website.

Provider selection is deterministic:

1. If the request names exactly one registered provider alias, Runtime selects
   that provider.
2. Otherwise Runtime selects the single enabled default provider.
3. Missing configuration, multiple named providers, or an ambiguous default
   blocks before Workflow creation.

Provider IDs, aliases, login URLs, allowed origins, handler paths, source
closure hashes, revisions, deadlines, result verifiers, and send-effect
selectors live in the Controller provider registry. Runtime registration maps
only those fixed handlers; callers cannot provide a script path or selector.

QQ Mail is not a generic browser destination. A request that merely opens QQ
Mail does not gain email-send authority.

## Login Configuration

WebChat exposes `Settings > Connections > Browser email` with QQ Mail, Outlook,
and Gmail entries. Each provider can be enabled or disabled, selected as the
single default, opened for manual login, and checked with a read-only probe.
The panel reports bounded readiness metadata and an optional masked account
hint.

The Gateway API surface is:

```text
GET   /api/email/providers
PATCH /api/email/providers/{provider}
POST  /api/email/providers/{provider}/login-browser
POST  /api/email/providers/{provider}/check
```

Fixed login origins are:

| Provider | Login URL |
|---|---|
| `qq_mail` | `https://wx.mail.qq.com/` |
| `outlook` | `https://outlook.live.com/mail/` |
| `gmail` | `https://mail.google.com/` |

`Open login browser` creates a provider task tab and explicitly hands it to the
owner. The owner enters credentials and completes CAPTCHA or 2FA directly in
the browser. SparkClaw never receives those values. Authentication remains in
the owner-only persistent profile and is reused by later task tabs without
state export.

Durable provider settings contain only owner ID, provider ID, enabled/default
flags, fixed `default` account ID, optional masked account hint, readiness,
last-check metadata, bounded error code, version, and update metadata. Memory,
file, and PostgreSQL implement the same repository contract.

## Login Probe Boundary

Login probing is configuration and admission logic, not a Workflow node or
model-visible tool. A probe:

1. acquires a fresh Controller CLI session with the shared Browser control
   credential;
2. creates one allowlisted task-owned provider tab;
3. opens the fixed provider URL;
4. checks deterministic signed-in and signed-out page markers;
5. returns `ready` plus an optional masked account hint;
6. closes only its own tab and detaches without closing Chromium.

The probe cannot list, open, read, compose, or send mail. Conflicting evidence,
unexpected origins, stale task ownership, or incomplete provider page state
fails closed. A stored `ready` state is only history and never bypasses the
fresh pre-send probe.

## Fresh Pre-Workflow Admission

Every clear send request runs a fresh probe before Workflow creation. Successful
admission freezes these Runtime-owned facts:

- provider and fixed account ID;
- optional masked account hint;
- provider-setting version;
- Browser control Vault credential generation;
- probe-handler revision;
- send-handler revision;
- validation time;
- unique send invocation ID.

Login-required, unavailable Controller or Bridge, invalid handler output,
provider ambiguity, configuration conflict, or stale credential state stops
before Workflow creation. No email tool or send approval is exposed.

## One-Node Workflow

```text
owner request
  -> semantic route: browser.email#send
  -> deterministic provider resolution
  -> fresh read-only login admission outside Workflow
  -> browser.email r1 / email_send
       -> model supplies recipient, optional subject, and body
       -> Runtime restores all frozen admission bindings
       -> exact-content owner approval
       -> email.send
  -> grounded send receipt
```

The `email_send` node has one attempt, dangerous risk, evidence completion, and
only `browser.email.send` in scope. The model-visible schema excludes provider,
account, setting version, credential generation, script revisions, validation
time, and invocation ID. Runtime restores them before ToolHub validation and
Policy.

The model cannot choose a provider, account, executable, URL, tab, browser
action, retry, or alternate tool. The Workflow contains no login, probe,
re-login, or generic browser node.

## Approval And Send Semantics

`email.send` is dangerous, non-idempotent, approval-required, and bounded by a
90-second tool deadline. Approval presents the provider, masked account hint,
recipient, subject, and full body. It binds the complete argument object,
including every Runtime-owned admission fact. Any post-approval change blocks
execution.

Immediately before script invocation, Runtime verifies that the provider is
still enabled and ready and that account, setting version, and Browser control
credential generation still match the approval. Drift requires a new request
and approval.

Each send handler verifies recipient, subject, body, and the unique Send control
before the effect. It may attempt the Send action once. Any timeout, target
loss, invalid result, context loss, or cleanup failure after the registered
effect selector may have been activated becomes `email_send_outcome_unknown`.
That result is terminal and non-retryable because the provider may already have
sent the message.

Success returns no subject or body. It contains provider, `sent` status, a
SHA-256 digest of the exact recipient, optional opaque provider message ID,
Browser credential generation, and handler revision.

## Script Registry And Isolation

The fixed registry contains one probe and one send revision for each provider.
All six real handlers run through an injected Playwright task runtime. There is
no standalone stdin entrypoint or process/CDP fallback inside a handler.

The Controller sends message values through an owner-only `0600` ephemeral
input file. Recipient, subject, body, and extension credential are absent from
argv, logs, artifacts, and model output. Inputs and results use strict bounded
JSON contracts; unknown fields and malformed output are rejected.

Every invocation owns one task tab. Handler actions use only page operations
and locators exposed by the injected runtime. Existing owner tabs and former
task tabs are never reused. Completion and cancellation detach the CLI session,
reap its subprocess, and remove its private runtime directory.

Probe request:

```json
{
  "schema_version": 1,
  "operation": "probe",
  "invocation_id": "opaque-runtime-id",
  "provider": "gmail",
  "account": "default"
}
```

Send request:

```json
{
  "schema_version": 1,
  "operation": "send",
  "invocation_id": "opaque-runtime-id",
  "provider": "gmail",
  "account": "default",
  "message": {
    "recipient": "recipient@example.com",
    "subject": "Optional subject",
    "body": {"format": "text", "content": "Message body"}
  }
}
```

Send success:

```json
{
  "schema_version": 1,
  "status": "sent",
  "provider": "gmail",
  "recipient_digest": "sha256:...",
  "provider_message_id": "optional-provider-opaque-id"
}
```

## Failure Contract

| Code | Meaning |
|---|---|
| `email_not_configured` | The requested/default provider is not enabled. |
| `email_login_required` | Manual login is required. |
| `email_account_ambiguous` | Provider or default selection is ambiguous. |
| `email_provider_unavailable` | The Controller, Bridge, or script runtime is unavailable. |
| `email_page_contract_changed` | Deterministic provider evidence no longer matches the handler contract. |
| `email_invalid_input` | Recipient, subject, body, or Runtime binding is invalid. |
| `email_draft_conflict` | Existing provider draft state makes a new send unsafe. |
| `email_draft_verification_failed` | Composed values could not be verified; Send is not activated. |
| `email_send_control_unverified` | The Send control was not uniquely verified. |
| `email_send_outcome_unknown` | The message may have been sent; never retry automatically. |
| `email_script_timeout` | A probe timed out before any send effect. |
| `email_script_invalid_output` | A provider handler violated its strict result contract. |
| `email_admission_stale` | Configuration or credential generation changed after admission. |

Selectors, page text, raw diagnostics, profile and socket paths, page IDs,
task identities, tokens, and cookies are absent from public settings and error
payloads.

## Acceptance Boundary

- Catalog and semantic routing expose send only; email reading stays unavailable.
- Provider resolution and admission facts remain Runtime-owned.
- A fresh read-only probe succeeds before Workflow creation.
- Workflow exposes one email tool and no generic browser tools.
- Approval binds exact content and every frozen admission fact.
- Provider setting and credential generation are rechecked after approval.
- Send is attempted at most once and unknown outcome is never retried.
- Every provider operation uses one Bridge-allowlisted task tab.
- Existing owner tabs are never selected, read, changed, or closed.
- No container Chromium, cookie/profile copy, CDP path, or browser fallback exists.
- QQ Mail remains absent from the generic destination registry.
- Provider settings behave identically in memory, file, and PostgreSQL stores.

Email reading, attachments, replies, drafts, or multi-account support requires a
separate capability and product contract. It must not be added as an unreviewed
mode under `browser.email` r1.
