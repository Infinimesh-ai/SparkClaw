# Browser Email Workflow Design

> Language: English | [简体中文](../zh-cn/docs/browser-email-workflow-design.md)

The [management implementation](email-management-implementation.md) adds a
separate default-off unified popup, cross-account topics and background thread
synchronization. This page governs the explicit human Workflow entry.

Status: Sending is implemented. The human reading entry implements the authorized
[source foundation](email-read-design.md): one unread message per script invocation,
workspace source files and a validated manifest. This human entry does not perform Gateway mail Store publication or model analysis. Fresh unread reading is not
fully qualified; provider limitations and live evidence are recorded in the source
design. Script completion does not
establish those outcomes.
QQ Mail, Outlook, and Gmail use deterministic Playwright
CLI handlers through the production SparkClaw Browser Bridge. The Phase 6
cutover removed the former Host-CDP runner. Revision 2 adds scripted inbox
reading alongside sending, preserving send approval and unknown-outcome semantics.

Background discovery, identity-based capture and independent model analysis now
run under the dedicated `emailmanagement` service, with durable Store jobs and
separate lifecycle. The human read Workflow still returns its source manifest;
it does not accept an arbitrary target or automatically import historical captures.
Provider qualification and remaining coverage gaps are recorded in the
[implementation report](email-management-implementation.md).

## Decision

SparkClaw owns browser-backed email under the browser branch:

```text
browser
`- browser.email
   |- browser.email#read
   `- browser.email#send
```

`browser.email` has one Workflow Profile at revision 2 with `read` and `send`
operations. Inbox and unread-mail requests route to `read`. Reply, forwarding,
and draft management remain unavailable.

The model chooses only the supported function. Reading takes no model parameters;
sending takes one recipient, an optional subject and a plain-text body. Runtime owns provider and account
selection, login admission, Browser control credential generation, provider
script revision, approval, invocation identity, and result validation.

## Supported Surface

The current capability supports:

- one new plain-text message to exactly one recipient;
- scripted capture of one unread message and available in-scope binary parts;
- an optional single-line subject;
- one effective signed-in account per provider;
- QQ Mail, Outlook, and Gmail;
- manual login in the persistent SparkClaw Browser;
- deterministic read-only login probes and one-attempt send handlers;
- one exact-content approval immediately before the external send effect.

It does not support mailbox search, replies, forwarding, deletion,
archive, explicit read-state management, reusable drafts, multiple recipients, CC/BCC,
outgoing attachments, HTML authoring, signatures, or multiple accounts per provider.
It does not use OAuth, IMAP, SMTP, Gmail API, Microsoft Graph, cookie export,
profile copying, container Chromium, or a fallback browser backend.

## Routing And Provider Resolution

The semantic graph contains `browser.email` candidates for `read` and `send`.
Unsupported requests include sending attachments, replies, login checks, and requests
that merely open a provider website.

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
The Gateway binds to that registry through a generated projection,
`services/gateway/internal/emailautomation/provider_scripts.json`, that
carries each provider's probe, read, and send script ID, revision, and budget.
`npm run sync:provider-contract --prefix tools/browser-controller` regenerates
it and the Controller test suite fails when it drifts; the Gateway never
restates login URLs or origins.

QQ Mail is not a generic browser destination. A request that merely opens QQ
Mail does not gain email-send authority.

## Inbox Reading

The fixed `qqmail.read`, `outlook.read`, and `gmail.read` scripts acquire one
unread message under the existing configured account and fresh admission guards.
The capture takes no model parameters. No send approval or send authority is involved.

The script persists actual source material under the configured workspace and
returns a bounded relative manifest reference, capture count and status. The
manifest records immutable IDs, file sizes/hashes, coverage and missing material.
Acquisition limits are 2 MiB of body, 20 binary parts, 25 MiB per part and 100 MiB
of parts in total. Files are not inlined into the script result. Unsupported
parts and incomplete capture remain explicit. A `script_capture` outcome is
distinct from a Gateway Store-committed email or completed model analysis.

Read scripts use background task tabs with ownership/origin guards; callers
cannot supply arbitrary script paths or selectors. Mark-read is authorized only
after complete durable capture, with its outcome tracked separately. Provider
automatic marking on open remains observable and does not prove capture success.
See the [source design](email-read-design.md) for the current implementation
boundary. Reply, draft management and general mailbox search remain out of scope.

## Login Configuration

WebChat exposes `Settings > Connections > Browser email` with QQ Mail, Outlook,
and Gmail entries. Each provider can be enabled or disabled, selected as the
single default, opened for manual login, and checked with a read-only probe.
The panel reports bounded readiness metadata and an optional masked account
hint.

The QQ Mail login probe reads two page markers in one batch: a visible login
region means login is required; otherwise a visible, valid account address in
the mailbox header on `/home/index` means ready. If neither state is recognizable,
the probe reports a page-contract error. It does not require the compose button,
profile container, or individual login methods, or repeat the same checks.
Task-tab ownership and allowed-origin validation remain browser-runtime checks.

Outlook accepts a visible new-mail or mail-navigation entry on its mailbox route;
Gmail accepts a visible compose entry on its mailbox route. Each collects its
login state in one DOM inspection. Account labels only supply optional masked
hints. A Microsoft or Google login origin means login is required without
enumerating password, account-picker, or verification forms. Missing mailbox
controls get up to eight seconds to render within the inspection, then produce
a page-contract error; recognized states return immediately.

For login probes, the Controller returns URL and page evidence from the same
CLI evaluation. It checks task-tab ownership and the observed provider URL
before evaluation, checks the live origin before DOM access, and validates the
returned URLs. Context loss still retries only after origin revalidation.

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
  -> browser.email r2 / email_send
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
execution. The tool definition declares `arguments_immutable`; the approval
payload carries the same flag, the modify endpoint rejects such approvals with
`409`, and WebChat hides argument editing based on the flag rather than the
tool name.

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

QQ Mail verifies the full address before moving DOM focus to the subject field.
An outside mousedown releases the recipient editor's focus trap; focus and
mouseup then complete the transition within the background page. The script
waits for a valid recipient chip before filling the remaining fields. Chip captions may
contain only a nickname. Plain-text editor verification reconstructs its DIV/BR
lines, preserving blank lines instead of comparing layout-dependent `innerText`.
Unsupported editor structure fails before Send.
QQ opens its Sent folder (`#/list/3`) before composing and records existing
message IDs. After Send it checks that folder for a new first row with the
requested subject digest immediately ahead of the previous first row. Returning
to an arbitrary mail list or finding an older matching message is insufficient.

All three providers' sends and login probes use background task tabs in the
existing browser window. Email scripts never request a window handoff or bring
their task tab to the foreground. Before composing, the Controller sends a fixed
background-input marker through the existing Bridge session. The Bridge enables
focus emulation only for that attached task renderer, allowing editor layout and
native Playwright input to run while the real tab stays in the background. This
marker never grants a handoff or activates a tab/window; probes do not use it.
Field focus is handled inside the page without changing browser-window focus.
Waits require a visible control, not merely an existing DOM node. Outlook accepts
the contenteditable To field and verifies its committed address chip with no
remaining input or additional recipients. Gmail uses the current role=option
recipient chips when available, excluding duplicate collapsed address summaries;
legacy editors use `email` chips. Gmail requires a sent receipt and a
closed compose view. Outlook starts in Sent Items and records existing row IDs;
success requires compose to close and a new first row matching recipient and
subject digests, immediately preceding the former first row. An old matching
message or disappearance alone is not success. Unrecognized list layouts fail
closed; a missing post-send record remains an unknown outcome without retry.

Read operations combine the DOM result and before/after origin validation in
one browser evaluation. Each evaluation still checks task-tab ownership; string
reads retain digest verification. Fill and keypress validate the fresh tab-list
origin before acting and the document URL afterwards, avoiding a redundant URL
evaluation before each action. Browser loading and provider response times still
contribute to total latency.

Send handlers fill fields consecutively and batch the final draft and Send-control
reads. One browser evaluation captures the fields synchronously, then hashes the
string values before returning them. Batches accept only bounded read operations;
clicks and fills cannot enter this API. QQ also batches its preflight and final
result reads. Explicit waits remain for provider state transitions and send
receipts; native Playwright clicks and fills supply their own actionability waits. QQ's
fixed 500 ms and 1500 ms sleeps are removed. Every operation still checks task-tab
ownership, and an uncertain send is never retried.

Success returns no subject or body. It contains provider, `sent` status, a
SHA-256 digest of the exact recipient, optional opaque provider message ID,
Browser credential generation, and handler revision.

## Script Registry And Isolation

The fixed registry contains one probe and one send revision for each provider.
All six real handlers run through an injected Playwright task runtime. There is
no standalone stdin entrypoint or process/CDP fallback inside a handler.

The Controller sends message values through an owner-only `0600` ephemeral
JSON configuration file so quotes, literal escapes, and newlines round-trip
exactly. Playwright redacts secret values in evaluation output too; field reads
include a browser-computed SHA-256 digest, and the Controller recovers a known
input only when that digest matches. Redaction markers alone never establish a
match. Wrapped QQ runtime failures retain their typed cause for diagnostics.
Recipient, subject, body, and extension credential are absent from
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

- Catalog and semantic routing expose separate read and send operations.
- Provider resolution and admission facts remain Runtime-owned.
- A fresh read-only probe succeeds before Workflow creation.
- Workflow exposes one email tool and no generic browser tools.
- Send approval binds exact content and every frozen admission fact.
- Read persists one message's source files and returns a bounded capture receipt without send approval.
- Provider setting and credential generation are rechecked after approval.
- Send is attempted at most once and unknown outcome is never retried.
- Every provider operation uses one Bridge-allowlisted task tab.
- Existing owner tabs are never selected, read, changed, or closed.
- No container Chromium, cookie/profile copy, CDP path, or browser fallback exists.
- QQ Mail remains absent from the generic destination registry.
- Provider settings behave identically in memory, file, and PostgreSQL stores.

Outgoing attachments, replies, drafts, or multi-account support requires a separate
capability and product contract. It must not be added as an unreviewed mode
under `browser.email` r2.
