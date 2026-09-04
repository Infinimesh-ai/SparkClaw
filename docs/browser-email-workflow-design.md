# Browser Email Workflow Design

> Language: English | [简体中文](../zh-cn/docs/browser-email-workflow-design.md)

Status: Implemented on 2026-09-03 as a send-only Host-CDP browser capability.
Its transport-specific design was superseded for future implementation on
2026-09-04 by the
[Playwright Extension browser migration design](playwright-extension-browser-design.md).
This document remains the record of current code until the atomic cutover, but
must not be used as the target contract for new browser transport work.

## Decision

SparkClaw owns browser-backed email under the existing browser branch:

```text
browser
`- browser.email
   `- browser.email#send
```

`browser.email` has one Workflow Profile at revision 1 and one operation,
`send`. Email reading is not registered. Requests to inspect an inbox, read an
unread message, reply, forward, or manage drafts do not receive email tools.

QQ Mail, Outlook, and Gmail use first-party provider scripts against the
dedicated SparkClaw Chromium profile. The model chooses only the supported
function and supplies the recipient, optional subject, and plain-text body.
Runtime owns provider selection, account selection, login admission, browser
identity, script revision, approval, invocation, and result validation.

This capability does not restore the retired generic personal-data email
connector. It is a bounded Host-CDP browser workflow with a new execution and
failure contract.

## Supported Surface

The current slice supports:

- one new plain-text message;
- exactly one recipient;
- an optional single-line subject;
- one effective signed-in account per provider;
- QQ Mail, Outlook, and Gmail;
- manual login in headed SparkClaw Chromium;
- login probes and sends in headless SparkClaw Chromium;
- one exact-content approval immediately before the external send effect.

It does not support:

- reading, searching, listing, replying to, forwarding, deleting, archiving,
  or marking mail;
- draft-only requests or reuse of an existing provider draft;
- multiple recipients, CC/BCC, attachments, HTML authoring, or signatures;
- OAuth, IMAP, SMTP, Gmail API, Microsoft Graph, or provider access tokens;
- multiple accounts within one provider;
- cookie export, profile copying, Playwright, container Chromium, or fallback
  to another browser runtime.

### Qualification-Only Read Script

The repository retains `scripts/email/qqmail-read-unread.mjs` as a deterministic
QQ Mail qualification utility imported from the provider worktree. It is not
registered in the Capability Catalog, Workflow Registry, ToolHub, Gateway API,
or WebChat, so its presence does not enable email reading in SparkClaw.

The utility accepts only the versioned `read_first_unread` JSON stdin contract,
uses a unique task-owned Host-CDP tab, opens the first unread message once, and
returns its sender, subject, displayed times, body, and bounded attachment
metadata. Opening the message marks it read. Any browser failure after that
click returns `read_outcome_unknown`; callers must not retry automatically.

## Routing And Provider Resolution

The semantic graph contains one `browser.email` candidate for the `send`
operation. Its hard negatives include email reading, inbox inspection,
attachments, replies, login checks, and requests that merely open a provider
website.

Provider selection is deterministic and is never delegated to the model:

1. If the owner request names exactly one registered provider alias, Runtime
   selects that provider.
2. Otherwise Runtime selects the single enabled default provider.
3. Missing configuration, multiple named providers, or an ambiguous default
   blocks before Workflow creation.

Provider IDs, aliases, login URLs, origins, script commands, revisions, and
timeouts live in `internal/emailautomation.Registry`. They are not copied into
parallel routing or dispatch switches.

QQ Mail is not a generic registered browser destination. A request that merely
opens QQ Mail remains a normal public named-target browser request and does not
gain email-send authority.

## Login Configuration

WebChat exposes `Settings > Connections > Browser email` with entries for QQ
Mail, Outlook, and Gmail. Each entry supports:

- enable or disable;
- select as the one default provider;
- open the dedicated login browser;
- run a login-status check;
- display the current readiness state, last check time, bounded error code,
  version, and optional masked account hint.

The Gateway API surface is:

```text
GET  /api/email/providers
PATCH /api/email/providers/{provider}
POST /api/email/providers/{provider}/login-browser
POST /api/email/providers/{provider}/check
```

The three login origins are fixed by the provider registry:

| Provider | Login URL |
|---|---|
| `qq_mail` | `https://wx.mail.qq.com/` |
| `outlook` | `https://outlook.live.com/mail/` |
| `gmail` | `https://mail.google.com/` |

`Open login browser` asks browserd for headed presentation and opens the fixed
provider URL. The owner enters credentials and completes CAPTCHA or 2FA
manually, then closes the headed browser. SparkClaw never receives those
credentials.

The next check asks browserd for headless presentation against the same
dedicated profile. Headed and headless Chromium use that profile sequentially,
never concurrently. Authentication remains in Chromium's profile; SparkClaw
does not copy cookies or tokens into Gateway state.

Durable provider settings contain only owner ID, provider ID, enabled/default
flags, the fixed `default` account ID, optional masked account hint, readiness
state, last-check metadata, error code, version, and update metadata. Memory,
file, and PostgreSQL backends implement the same repository contract.

## Login Probe Boundary

Login probing is configuration and admission logic, not a Workflow node or a
model-visible tool. A probe:

1. requires browserd to report actual `headless` presentation;
2. creates a new task-owned provider tab;
3. opens the registered provider URL;
4. checks deterministic signed-in and signed-out page markers;
5. returns `ready` plus an optional masked account hint;
6. closes only its own tab.

The probe cannot list, open, read, compose, or send mail. Conflicting login
evidence or an incomplete provider page contract fails closed. A previous
`ready` setting is only status history and never bypasses the fresh probe.

## Fresh Pre-Workflow Admission

Every clear send request runs a fresh probe before the Workflow Registry creates
the plan. Successful admission freezes these Runtime-owned route facts:

- provider and fixed account ID;
- optional masked account hint;
- provider-setting version;
- headless browser generation;
- probe-script revision;
- send-script revision;
- validation time;
- unique send invocation ID.

If probing reports login required, unavailable browser control, invalid script
output, provider ambiguity, or configuration conflict, the request terminates
before Workflow creation. No email tool and no send approval are exposed.

## One-Node Workflow

`browser.email` r1 creates one node named `email_send`:

```text
owner request
  -> semantic route: browser.email#send
  -> deterministic provider resolution
  -> fresh headless login admission outside Workflow
  -> browser.email r1 / email_send
       -> model supplies recipient, optional subject, and body
       -> Runtime restores all frozen admission bindings
       -> exact-content owner approval
       -> email.send
  -> grounded send receipt
```

The node has one attempt, dangerous risk, evidence completion, and only the
`browser.email.send` capability in scope. The model-visible schema excludes
provider, account, setting version, browser generation, script revisions,
validation time, and invocation ID. Runtime restores those values before
ToolHub validation and Policy.

The model cannot choose a provider, account, executable, URL, tab, browser
action, retry, or alternate tool. The Workflow does not contain login, probe,
re-login, or generic browser nodes.

## Approval And Send Semantics

`email.send` is dangerous, non-idempotent, approval-required, and has a 90
second tool deadline. The approval presents the provider, masked account hint,
recipient, subject, and full body. Approval binds the complete argument object,
including Runtime-owned admission facts. If any argument changes after approval
was created, execution fails with a policy block.

Immediately before invoking the script, the controller verifies that the
provider remains enabled and ready and that its account and setting version
still match the approved admission. The runner then requires browserd to return
the same headless browser generation. Configuration or browser drift requires a
fresh owner request and approval.

Each provider script verifies the composed recipient, subject, body, and Send
control before the effect. It may attempt the Send click at most once. A timeout,
invalid error envelope, lost target, cleanup failure after the click, or missing
positive send confirmation becomes `email_send_outcome_unknown`. That outcome
is terminal and non-retryable because the provider may already have sent the
message.

Successful output contains no subject or body. It returns the provider,
`sent` status, a SHA-256 digest of the exact recipient, optional opaque provider
message ID, browser generation, and send-script revision.

## Script Registry And Isolation

The provider registry owns one probe and one send script for each provider.
Scripts use a strict stdin/stdout JSON protocol and run as bounded subprocesses.
Message content is passed on stdin, never in argv. Unknown input or output
fields are rejected, and stdout/stderr are size-limited.

Runner-owned environment contains only the private Host-CDP attachment needed
for the invocation, a derived unique agent-browser session, browser generation,
and the pinned agent-browser executable. Inherited agent-browser profile,
restore, state, auto-connect, headed, extension, and init-script settings are
removed or rejected.

Every invocation creates a unique task-owned tab and binds all later actions to
it. Scripts may close only that tab. Existing owner tabs, former login tabs, and
idle tabs are never reused; user inactivity does not transfer tab ownership.

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

Probe success:

```json
{
  "schema_version": 1,
  "status": "ready",
  "provider": "gmail",
  "account_hint": "us***@gmail.com"
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

Stable public error codes include:

| Code | Meaning |
|---|---|
| `email_not_configured` | The requested/default provider is not enabled. |
| `email_login_required` | Manual login is required. |
| `email_account_ambiguous` | Provider or default selection is ambiguous. |
| `email_provider_unavailable` | browserd, Host-CDP, or the script runtime is unavailable. |
| `email_page_contract_changed` | Deterministic provider page evidence no longer matches the script contract. |
| `email_invalid_input` | Recipient, subject, body, or Runtime binding is invalid. |
| `email_draft_conflict` | A provider draft state makes a new send unsafe. |
| `email_draft_verification_failed` | Composed values could not be verified; Send is not clicked. |
| `email_send_control_unverified` | The Send control was not uniquely verified. |
| `email_send_outcome_unknown` | The message may have been sent; never retry automatically. |
| `email_script_timeout` | A probe timed out before any send effect. |
| `email_script_invalid_output` | The provider script violated its strict result contract. |
| `email_admission_stale` | Configuration or browser generation changed after admission. |

Selectors, page text, raw script diagnostics, profile paths, CDP ports,
capability WebSocket URLs, target IDs, and cookies are not part of public
settings or user-facing error payloads.

## Current Acceptance Boundary

The implementation is complete only while all of these invariants remain true:

- the Catalog and semantic graph expose send only;
- email reading remains unavailable;
- provider resolution and all admission facts remain Runtime-owned;
- a fresh headless probe succeeds before Workflow creation;
- the Workflow exposes one email tool and no generic browser tools;
- approval binds the exact provider, account, recipient, subject, body, setting
  version, browser generation, script revisions, validation time, and invocation
  ID;
- the provider setting and browser generation are rechecked after approval;
- Send is attempted at most once and an unknown result is never retried;
- all provider operations use task-owned headless tabs;
- headed Chromium is used only for manual configuration login;
- no container Chromium, Playwright, cookie copy, profile copy, or browser
  fallback exists;
- QQ Mail is absent from the generic browser destination registry;
- provider settings behave identically in memory, file, and PostgreSQL stores.

Future email reading, attachments, replies, drafts, or multi-account support
requires a separate capability and product contract. It must not be added as an
unreviewed script mode under `browser.email` r1.
