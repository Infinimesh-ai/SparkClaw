# Safe Structured Email Content Design

> Language: English | [简体中文](../zh-cn/docs/email-safe-html-preview-design.md)

Status: on 2026-09-18 the initial sanitized-HTML/iframe implementation was replaced by the
`structured-mail-v3` projection. Gateway extracts meaningful headings, paragraphs, lists, tables, quotes,
code, verified CID images and explicitly clicked HTTP(S) actions into a closed semantic tree. Sender HTML and
CSS never cross the API boundary. WebChat renders the tree through its own React components and theme; it does
not use an iframe. Missing and remote images are omitted completely instead of leaving broken placeholders.

## Decision summary

| Question | Decision |
|---|---|
| When is structured content produced? | During the existing single MIME pass for new mail; the current project data is handled by one operator-run serial migration before cutover. |
| What is rendered? | A closed semantic tree mapped to known React components. No HTML insertion or iframe is used. |
| Are remote images, fonts or CSS fetched? | No. Opening content performs no sender-controlled request; only an explicit user click on a validated HTTP(S) action may navigate externally. |
| What happens to inline images? | Verified CID PNG/JPEG/WebP parts may be embedded under strict count and byte limits; every other image node is omitted without a placeholder. |
| Is the result regenerated after every deployment? | No. It is an immutable projection keyed by source hash and sanitizer version and persisted across restarts. |
| What replaces the old preview? | One structured-content surface. Plain-only mail becomes a text node; both legacy plain text and HTML iframe views are retired. |
| What latency should the user expect? | Every preview is precomputed before it can be opened; click-to-first-render p95 is at most 300 ms. |

## Problem and goal

The first safe preview retained too much sender layout in a fixed-height iframe. Once remote images were
removed, source-less image elements could still display broken-image placeholders, while the nested window
and sender styling made the result difficult to read.

The goal is to extract readable structure from hostile mail and lay it out consistently with SparkClaw while
preserving the current owner boundary and guaranteeing that opening the content does not execute script,
submit a form, navigate the application, contact the sender, or load a tracking pixel. It must be fast after
ingestion, survive a normal redeployment, and be deleted with its conversation. Because the project currently
has no users, it does not retain a general-purpose historical migration or dual-preview compatibility mode.

## Non-goals

- Pixel-perfect reproduction of every Outlook, Gmail or QQ rendering quirk.
- Running scripts, forms, animations, embedded frames, media, plugins or interactive widgets from mail.
- Loading remote images, remote fonts, linked stylesheets or CSS imports while rendering.
- Treating the structured projection as source evidence. The immutable `.eml` remains the source of truth.
- Using a model to reconstruct layout or content.
- Re-fetching mail from the provider when a source is unavailable during the one-time project migration.
- Changing the primary conversation summary, reply composer, send path or conversation membership.

## User experience

The final message exposes two body-related actions:

1. **View full email content** — the only user-visible body view, directly expandable below the summary rather
   than nested inside technical details.
2. **Download `.eml`** — the immutable source bytes when still available.

Opening the content displays it directly in the current message card without changing conversation scroll
position or creating another vertical scrolling window. Headings, paragraphs, lists, quotes, code and tables
use SparkClaw typography, spacing, colors and borders. Only an over-wide table receives horizontal scrolling.
Unsafe or unavailable images are absent, so there are no broken-image icons or replacement placeholders.

The UI labels the result “View full email content”, not “original page”: it is coordinated readable content,
not a promise to reproduce sender styling. Valid absolute HTTP(S) links become explicit user-clicked actions
and show their actual hostname next to the sender label; unsafe, relative and credential-bearing URLs degrade
to plain text. The UI does not repeat a remote-content warning for every message because remote resources are
never loaded while rendering.

The final runtime does not generate a preview on click. If a projection is unexpectedly unavailable,
oversized, corrupt or rejected by the sanitizer, the UI explains that the safe preview is unavailable and,
when possible, still offers the `.eml` download. It never exposes the retired plain-text preview or raw HTML.

## End-to-end flow

```text
Immutable message.eml
  -> existing bounded MIME walk (one pass for new mail)
  -> select the main multipart/alternative HTML part
  -> resolve only verified, bounded CID raster resources
  -> extract a closed semantic tree, discard HTML/CSS and retain only validated click actions
  -> atomic immutable JSON content artifact
  -> Store preview metadata bound to owner/mail/representation/source hash/version
  -> authenticated JSON fetch
  -> fixed semantic-node-to-React mapping in WebChat
```

The summary and list paths never load this artifact. Work occurs during the normal parse of new mail or the
explicit one-time project migration before cutover. No model call and no provider network request are involved.

## MIME selection and fidelity

The parser must preserve the existing one-full-parse invariant for newly captured mail. The MIME walk returns
the existing `EmailRepresentation` plus one bounded render candidate in memory:

- For `multipart/alternative`, select the last supported `text/html` alternative associated with the main
  body; do not concatenate plain and HTML alternatives.
- For `multipart/related`, map `cid:` references only to parts in the same verified message tree.
- If the message has no HTML body, preserve `text/plain` as a preformatted text node.
- Nested `message/rfc822` content is rendered as a quoted section with an explicit boundary.
- Ordinary attachments are not inserted into the preview. They remain in the existing attachment surface.
- Sender-supplied `data:` and `blob:` URLs are rejected. Only SparkClaw-generated bounded data URLs for
  verified inline raster parts are permitted.

The extractor preserves readable semantics: paragraphs, headings, tables, lists, quotes, emphasis, code,
line breaks and dividers. Sender fonts, colors, positioning, dimensions, classes, IDs and all CSS are dropped;
SparkClaw controls the final presentation. A real HTML5 parser is required; regex extraction is insufficient.

## Security boundary

Mail is hostile input. The design uses independent server and browser fences so one missed sanitizer rule is
not sufficient to compromise WebChat.

### Structural extraction

The Gateway removes at least `script`, `iframe`, `frame`, `frameset`, `object`, `embed`, `applet`, `form`,
`input`, `button`, `select`, `textarea`, `video`, `audio`, `canvas`, `portal`, `base`, active `meta`, linked
stylesheets and SVG/MathML. It removes event attributes, `srcdoc`, scripting URLs, refresh/navigation
directives and sender-controlled `data:`/`blob:` content. Anchor destinations survive only when they are
absolute HTTP(S), have no embedded username/password, contain no control characters and fit the 8 KiB bound.
Invalid destinations lose navigation while their readable child text remains.

No CSS or generic attribute is emitted, so sender `@import`, `url()`, fixed/sticky positioning and z-index do
not need to be repaired. Hidden nodes (`hidden`, `aria-hidden=true`, `display:none`, `visibility:hidden`, and
`opacity:0`) are excluded. Unknown layout wrappers degrade to attribute-free sections. A remote image inside a
valid action may contribute only its bounded alt text as the action label; the image itself is still omitted.

### Resource policy

- Remote HTTP(S) images and resources are never requested while content is rendered. A validated action URL is
  requested only after the user explicitly clicks it.
- CID resolution accepts only verified PNG, JPEG or WebP payloads whose declared and detected types agree.
- SVG, HTML, PDF and other active or document formats are never embedded as images.
- At most 20 inline resources and 5 MiB of decoded inline bytes are embedded. Excess or unavailable resources
  are omitted instead of becoming placeholders.
- No cookies, bearer tokens, provider credentials or local file paths enter the preview document.
- A mail-provided action URL may itself contain a one-time token. It stays inside the owner-scoped artifact and
  authenticated response, is never logged or expanded as visible text for an HTML action, and only its hostname
  is added to the label. Plain-text mail continues to show its source URL verbatim.

### Browser rendering boundary

WebChat fetches the closed tree through the existing authenticated API and creates elements only through a
fixed `kind` switch. Unknown kinds render nothing, image sources are checked again for PNG/JPEG/WebP data URLs,
and link destinations are independently revalidated as absolute HTTP(S) without URL credentials. There is no
`dangerouslySetInnerHTML`, `srcdoc`, iframe or arbitrary tag creation. Action links open only on user click in a
new tab with `noopener noreferrer nofollow` and `referrerpolicy=no-referrer`; the visible hostname exposes the
actual request host to sighted and assistive-technology users. Mail text reaches the DOM only as React-escaped
text and the application owns every layout style.

## Persistence and versioning

Structured content is a derived, language-independent projection, not a new source. Do not add mutable body data to the
existing immutable `EmailRepresentation`. Add a separate record with fields equivalent to:

```text
EmailRenderPreview
  id, owner_id, mail_id, capture_id, representation_id
  source_sha256, sanitizer_version, state
  artifact_path, artifact_sha256, artifact_bytes
  embedded_resource_count, embedded_resource_bytes
  failure_code, created_at
```

The artifact lives under the owner-scoped normalized tree, for example:

```text
email/<owner-scope>/normalized/<representation-id>/render/<sanitizer-version>/
  content.json
  manifest.json
```

Both files are bounded, written with service-only permissions and published immutably before Store admission. The Store record is
published only after hashes and sizes are final. Identity is derived from owner, representation ID, source
SHA-256 and sanitizer version, making replay idempotent.

A redeployment with the same sanitizer version reuses the artifact. A new version produces a new artifact;
the old one is not served once that version is retired. A security retirement never serves stale HTML projection merely
because the original has been purged: if regeneration is impossible, the safe preview is unavailable.

Manual original-source cleanup removes `.eml` source bytes but may retain the already-sanitized projection,
matching the existing behavior of retained parsed text. Whole-conversation deletion removes preview records,
and files together with all other mail data. Orphan recovery may delete an unreferenced derived artifact but
must never scan or alter provider source state.

## One-time project migration and cutover

New mail creates the preview candidate in the same MIME pass that already produces `BodyText`; sanitization or
preview publication failure does not fail the text representation. It records a separate preview state and
the message summary remains readable. `BodyText` remains an internal parsing, summary, classification and
search input; removing the old preview does not remove that internal evidence.

There is no production backfill cursor, compatibility worker, on-click generation job or dual-preview feature
flag. Before final deployment, an operator-only migration command enumerates the current project Store in
bounded pages and processes one retained mail at a time from its local verified `.eml`. It never contacts QQ,
Gmail or Outlook, calls a model, changes read state or sends mail. It records exact ready/unavailable/failed
coverage and is idempotent on owner, representation, source hash and sanitizer version.

The implemented command is:

```bash
go run ./services/gateway/cmd/sparkclaw \
  -config configs/sparkclaw.default.json \
  -migrate-email-render-previews <owner-id>
```

It prints a JSON coverage report and exits non-zero if any mail fails validation or generation.

Cutover requires every retained current mail to have a verified ready projection. A missing, corrupt,
hash-mismatched, oversized or rejected source stops the cutover and reports the exact mail; it is not silently
deleted and does not trigger a provider refetch. After migration coverage, security, visual and performance
checks pass, the same implementation change removes the legacy plain-text preview component, label, API
client, `GET /api/email/messages/{mail}/preview` route, obsolete CSS and legacy UI tests. Replacement tests
must cover the new surface. The final deployed build therefore never exposes both preview modes.

## API contract

One owner-authenticated internal endpoint is sufficient:

```text
GET  /api/email/messages/{mail}/render-preview
```

`GET` is read-only and returns `ready`, `unavailable` or `failed`. A ready response includes the
closed semantic nodes, projection version, representation revision and resource counts. Responses are
`Cache-Control: no-store` and `X-Content-Type-Options: nosniff`.

The endpoint repeats mail-owner, capture-owner and representation binding checks and rejects a stale
representation revision rather than displaying content for a replaced source. Structured body content is not added
to conversation list responses, search indexes, model inputs, logs, telemetry or error messages.

## Size and resource limits

Initial implementation limits are intentionally conservative:

| Item | Limit | Result when exceeded |
|---|---:|---|
| Selected input HTML part | 2 MiB decoded | Preview unavailable; message summary and `.eml` remain |
| Parsed DOM nodes | 50,000 | Preview unavailable; message summary and `.eml` remain |
| Structured JSON artifact | 2 MiB | Content unavailable; message summary and `.eml` remain |
| Inline resources | 20 files / 5 MiB decoded total | Omit excess resources |
| One-time project migration | One serial process | No product runtime worker or user-visible contention |

These are preview limits only. They do not change the existing 110 MiB source envelope or attachment
retention contract.

## Performance contract

Performance is measured click-to-first-preview-paint on the Remote deployment reference host, with browser
network instrumentation confirming zero automatic sender-controlled requests before an action is clicked:

| Case | Acceptance target |
|---|---|
| UI acknowledges click | p95 <= 100 ms |
| Current cached projection | p95 <= 300 ms, p99 <= 750 ms |
| Added new-mail parse overhead, HTML <= 2 MiB | p95 <= 150 ms, measured separately from source download |
| Reopen after restart/redeploy with the same version | Same cached target; no regeneration |
| One-time migration | Measured offline and completed before cutover; it adds no click-path work |

These are release gates, not claims about the current build. Remote images are blocked, so their unpredictable
latency is excluded. The conversation list and primary summaries must show no measurable regression.

## Failure and deletion semantics

- Extraction failure: record a bounded stable code; show content unavailable; never expose rejected HTML or
  revive the legacy plain-text view.
- Preview file missing or hash mismatch: stop serving it and mark repairable if source remains; only an exact
  operator repair may rebuild it, not a user click or background compatibility worker.
- Original missing before first generation: structured content is terminally unavailable; no provider refetch.
- Service restart during new-mail parsing follows the existing durable parse-job rules. A migration
  interruption is rerun explicitly; immutable finished artifacts are reused only after hash and owner checks.
- Whole-conversation delete: fail closed under the existing active/unknown-send rules, then remove preview
  records and files in the same conversation cleanup boundary.
- Deployment rollback: `BodyText` remains in the representation for internal processing, so a coordinated
  rollback image can still operate; the final forward version does not expose the legacy preview route.

## Validation and release gates

Implementation is not deployable until all of the following pass:

1. A hostile corpus covering script/event handlers, form submission, meta refresh, CSS imports/URLs,
   navigation, SVG, nested frames, malformed HTML, MIME confusion and oversized inputs produces no execution,
   automatic navigation or network request; unsafe action URLs must degrade to text.
2. Chromium request logs prove zero remote requests when previews from QQ, Gmail, Outlook and synthetic
   marketing mail are opened without clicking, while a user-click test confirms the validated destination,
   visible hostname, new-tab isolation and no-referrer policy.
3. Golden visual fixtures cover table newsletters, transaction receipts, verification mail, quoted replies,
   plain-only mail rendered inside the new safe surface, CID images, dark mail and narrow/mobile layouts.
4. Owner-scope, stale-revision, source-hash, direct-navigation and cross-owner API tests fail closed.
5. Restart/redeploy tests prove a same-version projection is reused and not regenerated.
6. The one-time operator migration is idempotent, serial and bounded, triggers no provider synchronization or
   model calls, and produces exact coverage. Cutover stops on any retained mail without a ready projection.
7. Source cleanup retains a valid current projection, while whole-conversation deletion removes it without
   leaving files.
8. The performance table is measured rather than inferred; results are recorded separately for ordinary and
   large mail.
9. Full Gateway tests/build/vet, scoped race, WebChat tests/build/i18n and bilingual documentation checks pass.
10. Live validation uses non-sensitive test mail first. Production validation opens only owner-selected mail
    and confirms that no message is sent, marked, synchronized or remotely fetched before an explicit action click.

### Clickable-action deployment evidence (structured-mail-v3, 2026-09-18)

- All 8 retained mails have `structured-mail-v3` records and `content.json` artifacts. Migration reported
  `migrated=8,failed=0`; its deployed rerun reported `reused=8,failed=0`.
- The eight artifacts contain 1,086 semantic nodes and 52 HTTP(S) action nodes across 26 visible hostnames.
  A structural scan found zero invalid action URL and zero remote image source; the current sample has no CID
  image node.
- Live inspection of a historical workspace invitation displayed “Join workspace” and the actual hostname as
  one coordinated action. The DOM confirms `target=_blank`, `rel="noopener noreferrer nofollow"` and
  `referrerpolicy=no-referrer`. The authentication link was inspected but not clicked.
- Remote Gateway image `sha256:de17b2ba8b085aa4b22c760535ec4167ee3f20f072669b7c242f97ae280f69c4`
  and WebChat image `sha256:ead37ba2ae0de2a7d12a1270b266bd5fa167c4bb5badef0c8f602cd4dbe52fc1`
  are healthy; WebChat returns HTTP 200.
- Migration made no provider or model call. Deployment and validation did not send, delete or manually sync
  mail and did not navigate to an extracted action. Inventory and receiving settings are unchanged.

### Initial structured-content evidence (structured-mail-v2, superseded by v3)

- All 8 retained mails for the current owner have `structured-mail-v2` records and `content.json` artifacts.
  The first migration reported `migrated=8,failed=0`; the deployed rerun reported `reused=8,failed=0`.
- The eight artifacts contain 982 semantic nodes. A structural scan found no unknown kind, remote source or
  invalid image source. The current sample has no usable CID image, so the resulting trees have no image node.
- WebChat contains no iframe, `srcdoc` or arbitrary HTML insertion. Live validation of a historical invitation
  kept the primary summary and exposed “View full email content” directly below it. The sender's one-column
  layout table was flattened into ordinary sections, while real multi-column tables retain bounded horizontal
  scrolling. No broken-image placeholder was present.
- Remote Gateway image `sha256:08056e1767eed103b049b2bbe85b6c0e8b4256a3d745751d0c6dbdfbf0dbad15`
  and WebChat image `sha256:56ce211ebe7d5eaf525331d8c5d4c3b43186be1faba2dacddcc0348a7e248268`
  are healthy; WebChat returns HTTP 200.
- Migration made no provider or model call. Deployment and validation did not send, delete or manually sync
  mail. Inventory remains 8 mails; QQ intake is on and Gmail/Outlook intake is off.

### Initial HTML-preview evidence (2026-09-17, superseded by v2)

- PostgreSQL migration `0016_email_render_preview.sql` is applied. The current owner has eight mail records and
  eight `ready` render-preview records; the operator migration reported `migrated=8, failed=0`, then
  `reused=8, failed=0` from the deployed image.
- The workspace contains eight bounded `safe-mail-v1` HTML artifacts (97,859 bytes total). All eight contain
  the restrictive CSP, and a structural scan found no active elements, event handlers, link destinations or
  remote source attributes.
- The deployed authenticated WebChat opened a real migrated verification email, preserved its table layout and
  visible verification code, and exposed the preview through an iframe with `sandbox=""`,
  `referrerpolicy="no-referrer"` and an accessible title. The primary conversation surface remained the
  localized summary.
- Remote Gateway image `sha256:a8bd27df487421fbddae1622b12cdf15919e8af6ccba50b1b2463cb3ef6ea651`
  is healthy; WebChat image `sha256:f55fde5a1ed921d8215ef8ab6e151b393e11334a419e33b567ab8b8db5f034a6`
  is running and HTTP 200.
- The legacy `/api/email/messages/{mail}/preview` route, client method, component and CSS are absent from the
  deployed build. The only user-visible body path is the safe preview; `.eml` download remains separate.
- No send action or receiving-switch mutation was performed by migration or validation. Percentile latency,
  multi-provider golden screenshots and browser-level request tracing remain unclaimed follow-up evidence;
  the security boundary does not depend on those measurements.

## Delivery sequence

1. Add preview record/store contracts, schema migration, owner-scoped artifact paths and deletion/recovery semantics.
2. Extend the existing MIME walk to emit one bounded candidate; use an HTML5 parser to extract the closed
   semantic tree and test hostile nodes, complete removal of remote/missing images, and size/depth limits.
3. Add the read endpoint, operator-only one-time migration command and exact coverage report.
4. Add the fixed WebChat semantic-node-to-React mapping and localized states, with no iframe or arbitrary HTML
   insertion.
5. Run the serial migration over all current project mail, then pass malicious-corpus, ownership, persistence,
   deletion, visual and performance gates.
6. Remove the legacy plain-text preview component, route, client method, labels, CSS and obsolete tests; rerun
   the full gates and confirm the final build exposes only structured email content plus `.eml` download.
7. Deploy without changing receiving switches or triggering mailbox synchronization, validate the migrated
   project data, and publish measured results.
