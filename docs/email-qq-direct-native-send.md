# QQ Mail Direct Native Send: Maintained Template and Lightweight Flow

> Language: English | [简体中文](../zh-cn/docs/email-qq-direct-native-send.md)

- Status: **implemented locally, not released** (2026-10-23). Design: second revision, following the owner's ten review points. Implementation: App-CLI branch `claude/mail-qq-direct-send` (`322fd94`, docs `b6f334d`) and SparkClaw branch `claude/mail-qq-direct-send` (Browser Controller `a5135b58`, Gateway `89a30aa6`); see section 17. Nothing was released or deployed and no mail was sent.
- Baseline: App-CLI `d8b2a01` (release 35), SparkClaw `a78433c3`, Tampermonkey 5.5.0.18
- Context: [network-native mail architecture](email-network-native-architecture.md), sections 9.3–9.6 and 9.10; [mail send latency](email-send-latency.md)
- Scope: QQ Mail new-compose sends are the **anchor**. The pattern is fully implemented on QQ first and then extended to Gmail and Outlook (section 15). The first implementation changes behavior only for QQ new-compose sends; Gmail, Outlook, replies, attachments and mark-read stay as they are until each is brought over. The changes touch **App-CLI, the Gateway and the Browser Controller**, and their shared parts must be built as provider-independent interfaces.

## 1. Goal

There are four preconditions: the browser is signed in, the sending account is known, the message is complete and the request template is valid. When they all hold, the browser's `fetch` in the signed-in page **submits once** to QQ's own web-client `/send/sendmail`, and the result is decided from QQ's response. The normal path does none of the following:

- run a separate login probe before sending;
- read the Sent list in advance;
- wait for a fixed time without evidence;
- re-initialize the page or script for every message;
- spend seconds tidying the page after a success.

The request template is no longer learned from a DOM send. It becomes a versioned template maintained in code, `qq_sendmail_v1` (sections 3 and 4). Message content never appears in any process's command-line arguments (section 5).

## 2. Release 35 today

### 2.1 Steps of a warm native send

| # | Step | Location | Cost (work2, release 35, page transport off) |
|---|---|---|---|
| 1 | Gateway `Admit` runs a full `probe` task before every message (the 120 s reuse window did not take effect live) | `emailautomation/controller.go`, `send_admission_cache.go` | executor 1.85–2.0 s, API about 2.7 s |
| 2 | `setSecrets` stores recipient, subject and body in private slots | `handlers.mjs` `prepare()` | 1 page call |
| 3 | `prepareBackgroundPage` | same | 1 page call |
| 4 | `prepareMailRound`: check the document nonce, run `resetRound` | same | 1 page call |
| 5 | `prepareNativeSend`: check the template, read a Sent baseline | `managed-send.mjs`, `qq-send-prototype.mjs` | 1 page call + 1 network read |
| 6 | Release the dispatch, write the `dispatching` journal | executor, `send-journal.mjs` | anchor CAS + fsync |
| 7 | `sendNative`: `fetch`, **fixed 1 s wait**, then poll Sent every 750 ms for at most 12 s | `qq-send-prototype.mjs` | request 518–566 ms; Sent proof 1.7–4.6 s |
| 8 | Write the `sent` journal | `send-journal.mjs` | fsync |
| 9 | `parkSendTemplate`: `prepareMailRound`, a DOM-cleanup eval, `setSecrets({})` | `mail-page.mjs` | 3 page calls |

Measured in [section 9.10](email-network-native-architecture.md): native send API time was 18.6–19.1 s, of which the executor took 6.4–7.0 s. With the page transport off, every page call starts `playwright-cli.js` processes, and each process start alone costs about 0.42 s. Every `runReadCode` is also surrounded by a `tab-list` topology-check process before and after it.

### 2.2 The problem with template generation

Today a template can only be obtained by first completing a DOM send of about 80 s, observing QQ's own `func=1` request inside a learning window, and confirming it with an independent Sent read. The learned template lives in that page's memory for 15 minutes. A reload, a document replacement, expiry, an account change or one unproven replay sends the next message back to the 80 s DOM teaching send.

### 2.3 Message content is exposed through command-line arguments

Send pages are not read-only pages, so `HostPage.runReadCode` runs `playwright-cli run-code <code>` (`tools/browser-controller/src/host-page.mjs`). The native path embeds the recipient, subject and body as JSON in that code, so message content appears in a child process's command-line arguments. work2 does not mount `/proc` with `hidepid`, so any process on the host can read them. Page code is also capped at 64 KiB, which is why release 35 limits native message content to about 48 KB.

## 3. Template feasibility

### 3.1 Evidence

The SQL index of the work2 production browser's HTTP cache records the URLs of the QQ scripts the web client loaded. Only that index was scanned, read-only. The scripts with the same names were then downloaded on the Mac, without any cookie, from the public CDN `res.wx.qq.com/t/webmail/webmail/res/js/`. No live page was touched, no debugging port was opened and no mail was sent.

| Script | Last-Modified |
|---|---|
| `home-web.d0d4cbddfb2d7d06.js` | 2026-09-22 |
| `home-web.18ed04099703ed2d.js` | 2026-09-24 |
| `home-web.e91d36e6686b314c.js` | 2026-10-22 (loaded during the release 34/35 live tests) |
| `home-web.2a8c6bb7ea4212ee.js` | 2026-10-23 (current) |
| `chunk/XmailEditor_14e7.73850030e0dda947.js`, `…dd7a932fbf4c0fd5.js` | 2026-09-22, 2026-10-22 (editor, see 4.2) |

### 3.2 How QQ itself builds `/send/sendmail`

The send function `sendMail` calls `post({cgi:"/send/sendmail", params})`. `k(e)` builds the parameters and deletes every key whose value is `null` or `undefined`.

| Field | Value in QQ's source | Plain compose |
|---|---|---|
| `func` | `e.type` | `1` (send; `2` draft; `4` reschedule) |
| `mailid` | `e.mailId` | `''` (accepted in the release 34/35 replays) |
| `time_zone` | `-new Date(2009,0,1).getTimezoneOffset()/60*3600` | computed in the page |
| `time_zone_dst` | `new Date(2009,0,1).getTimezoneOffset()/60*3600-(new Date).getTimezoneOffset()/60*3600` | computed in the page |
| `calendar` | `e.schedule` | `''` |
| `subject`, `content` | subject, body HTML | see 4.2 |
| `is_text`, `is_urgent`, `is_resume`, `need_receipt` | the flags | `false` (whether to use `is_text=true` is to be verified, see 4.2) |
| `from` | `S(sender)`: `"nick"<address>` with a nickname, else the bare address | see 3.4 |
| `save_to_send` | `!isReadReceipt` | `true` |
| `separated_copy` | separate copies | `false` |
| `to`, `cc`, `bcc` | each address formatted by `S()` | one key per array element |
| `send_time`, `manual_save`, `re_mailid`, `fw_mailid`, `sent_mailid`, `is_receipt`, attachment fields | empty for a plain compose | not sent |

The shared request library `post()` adds three common parameters and deletes the same names from the URL:

- `language=window.lang`;
- `r=(1e13*Math.random()).toFixed(0)+Date.now()`;
- `sid=ee()` (see 4.3).

The request is a `POST` with `Content-Type: application/x-www-form-urlencoded`, credentials included, `responseType=json` and a 30 s timeout.

Only `head.ret==="0"` counts as success. A success returns `body.mailid` and `body.messageid`, and `openJustSendMail` opens the message just sent by its `mailid`. The mail model's `id` comes from the list row's `emailid`, so `mailid` is inferred to share the ID space of the Sent row's `emailid`; section 6.4 sets the conditions for using it. QQ's error codes relevant to sending are listed in section 7.2.

### 3.3 Stability

These three code segments are byte-identical in all four versions:

- the parameter builder (1,689 characters, SHA-256 prefix `664a4ef1db37`);
- the sender formatter `S()` (`4de39330d3d5`);
- the request library's common-parameter code (`23e939dfb7bd`).

Between the September and October versions, `sendMail` differs only in minifier variable names. The three live replays in releases 34/35 learned exactly the field set above. QQ accepted `mailid=''` and a `<div>`-per-line body; each replay returned `head.ret=0` with a `messageid` and matched one independent Sent row.

### 3.4 Conclusion and remaining live checks

The template is **feasible**. Every field is a constant, QQ's own formula computed in the page, or a value taken from the page. The first qualified live send must still confirm three things:

1. **The `from` display name.** v1 sends the bare address, a legitimate form QQ sends itself when the sender has no nickname. If recipients see a different name than with a DOM send, v1.1 reads the page's sender nickname and builds `"nick"<address>` with `encodeNick`.
2. **Whether `mailid` equals the Sent row's `emailid`.** This decides the receipt; see 6.4.
3. **Body rendering.** Whitespace must look the same to recipients as with a DOM send; see 4.2.

## 4. Maintained template `qq_sendmail_v1`

The template lives under App-CLI `applications/mail/userscripts/lib/`, is bundled into the Reader, and replaces the learning and confirmation logic in `qq-send-prototype.mjs`.

```text
POST https://wx.mail.qq.com/send/sendmail        (no query string)
Content-Type: application/x-www-form-urlencoded
credentials: same-origin, redirect: error, cache: no-store

func=1  mailid=  calendar=  is_text=false  is_urgent=false  is_resume=false
need_receipt=false  save_to_send=true  separated_copy=false
time_zone=<computed>  time_zone_dst=<computed>
from=<verified account address>  to=<recipient>
subject=<subject>  content=<body converted per 4.2>
language=<window.lang>  r=<QQ formula>  sid=<per 4.3>
```

### 4.1 Version, preconditions and scope

- **Version and maintenance.** The template carries a `template_version`. When QQ changes fields, a developer updates the template with the method in 3.1 and ships it in a paired release; the runtime never relearns. Regression tests pin the field set and every value rule.
- **Page preconditions.** All of these must hold before a submit is allowed:
  - `location.origin` is `https://wx.mail.qq.com`;
  - the path is `/home/index`;
  - a sid of at most 4,096 bytes is available per 4.3;
  - `window.lang` is a short string;
  - the Reader account is readable and equals the sending account.
- **Drift signal.** Record the page's `__WEB_MANIFEST__.version` and the `home-web` file name, only to compare QQ front-end versions. This is never a gate.
- **v1 scope.** New compose, one recipient, no cc, no attachments. The body limit depends on the Browser Controller change in section 5: once it lands, the existing 200 KiB body limit applies; until then, message content (the JSON of recipient, subject and body) is limited to about 48 KB. Several recipients and cc open only after their own qualification.
- **Retired.** The learning window's `capture_send_template`, `observe()` capture of `/send/sendmail`, `confirm()`, the 15-minute TTL, consume-and-renew, `sendNativeReady` and `parkSendTemplate` are all removed. The learning window itself stays for mark-read and shape evidence.
- **DOM path.** Messages outside v1 (replies, cc, attachments) use the DOM flow as today. Messages inside v1 **never** use a DOM send to generate a template, and **never** fall back to a DOM send when the template fails (section 7).

### 4.2 Body: plain text to HTML

The rule matches the QQ editor's "paste as plain text" path exactly: `gu()` in `XmailEditor`, byte-identical in both editor versions. The body is split on `\n` and each line is processed in this order:

1. remove `\r`;
2. replace one leading space with U+00A0 (no-break space);
3. replace one trailing space with U+00A0;
4. replace every pair of consecutive spaces with "space + U+00A0" (global, non-overlapping), so runs of spaces are kept;
5. expand each tab to the next 6-column stop with U+2002 (EN SPACE), counting columns in UTF-16 length from the line start.

HTML encoding then matches the browser's `innerHTML` serialization of a text node:

- `&` → `&amp;`, `<` → `&lt;`, `>` → `&gt;`, U+00A0 → `&nbsp;`;
- U+2002 stays literal;
- quotes are not escaped.

Each line becomes `<div>…</div>`, and an empty line becomes `<div><br></div>`; this structure was accepted in the release 34/35 tests.

`is_text=true`, which submits plain text directly, is **to be verified**. QQ's front end supports it, but its effect on recipient rendering and on the Sent original has not been tested, so v1 does not use it.

### 4.3 Obtaining the sid

QQ's `ee()` takes the sid in this order:

1. Find the current account uin: first `<head xmail_uin>`; otherwise the entry in cookie `xm_muti_sid` whose sid equals the address-bar sid; otherwise cookie `xm_uin`.
2. Take that uin's sid from cookie `xm_muti_sid` (format `uin&sid|…`). Without that cookie, use `xm_uin` + `xm_sid`.
3. Only when neither yields a sid, use the address-bar `sid`.

When the cookie sid differs from the address bar, QQ rewrites the address bar with `history.replaceState` and notifies other tabs through `BroadcastChannel("updateUrlSid")`.

**Our method:** read the sid inside the page with QQ's own priority, cookie first and address bar as fallback. The sid is used only in this request's body. It is not returned to Node, not logged, the address bar is not rewritten, and no other cookie is read. When the two disagree, the cookie wins, and the disagreement is recorded only as a drift signal.

**Trade-off:** we depend on three cookie names and one `<head>` attribute. They are maintained together with the template and checked with the method in 3.1. In return, if another tab has refreshed the sid and this page's address bar has not caught up yet, a stale sid does not produce `-20002`, so the send is not misclassified as an invalid login. If those cookies ever become HttpOnly and unreadable to page scripts, we fall back to the address-bar sid and accept that risk. The Reader's existing list reads keep using the address-bar sid; this design does not change them.

## 5. Content transfer: Browser Controller private request files

**Principle:** native sending never puts recipient, subject or body into any process's command-line arguments, and message content is transferred only once per send.

- **Reuse the read-only page method.** For read-only pages, `runReadCode` already writes the code to `read-batch-<uuid>.json` in the task state directory (opened with `wx`, mode `0600`). The arguments carry only `--batch-request <path>`. `cli-read-batch.mjs` then runs the topology check, `run-code` and the re-check in one process, and the file is deleted in `finally`.
- **New: guarded code request for mutation pages.** The Browser Controller adds the same kind of request for pages that declare mutation activity. The request file has two fields:
  - `code`: a small fixed page function with no message content, still capped at 72 KiB;
  - `argument`: the message content JSON, at most 512 KiB.

  `argument` is combined with the code only inside the guarded process and reaches the browser through the daemon's Session socket, never through any argument list. There is no read-only wrapper, but the topology and origin checks stay. Each request file is used once and deleted immediately. Leftover request files are removed when the page starts.
- **With the resident transport on.** The same request travels over `cli-page-transport.mjs` stdin as `{"code_request":…}`, with no file. When the transport is unavailable, the request falls back to a private request file, never to command-line arguments.
- **Once only.** `preflight` carries no message content. The content travels only with `dispatch` (section 6).
- **Limits.** Once this change lands, native sending is no longer bound by the 64 KiB page-code limit, and v1 keeps the 200 KiB body limit. Until then, the limit in 4.1 (about 48 KB) applies.
- **To verify.** Check that the Playwright daemon does not write `run-code` text to logs or traces. If it does, that logging must be disabled for task pages.

## 6. Normal send flow

### 6.1 Check the account inside the page

- **Gateway.** QQ's send script declares `send_admission: in_page`. `Admit` no longer calls `probe`. It only checks persisted state:
  - the setting is `ready` with no error code;
  - the setting version and account match;
  - the browser credential generation matches;
  - the mailbox observer reports neither `login_required` nor an error.

  If any check fails, the full probe runs as today. `SendForOwner` still rechecks the setting version and credential generation.
- **Page.** One page call, `preflight`, carries no message content. It:
  1. checks the document nonce;
  2. on a cold page, waits inside the call, bounded to 5 s, for the Reader and the account;
  3. compares the signed-in account with the sending account (case-insensitive);
  4. checks the preconditions in 4.1 and that a sid is available per 4.3.
- **Outcomes:**
  - match: a one-use ticket `{token, nonce, account, expires_at}`, valid for at most 60 s and holding no message content;
  - different account: `email_account_identity_mismatch`;
  - visible login page: `email_login_required`;
  - account unreadable within the bound: `email_account_identity_unavailable` ("account cannot be determined, please check").

  All of these failures happen before the dispatch is released, so nothing was sent.

### 6.2 Assemble the request locally

- App-CLI performs every deterministic check on the Node side: addresses, subject, body, size and scope, with the existing `validateManagedSend` rules. The dispatch is released only after they pass.
- The `dispatch` call passes the ticket and the message content through the private request in section 5. The page assembles the form in local variables per section 4: body conversion, time zones, `language`, `r` and the sid. The browser attaches login credentials by its same-origin rules.
- The ready page and Reader are reused. There is no per-message `setSecrets`, `resetRound`, Sent baseline or DOM tidying. The native path writes no message content into the DOM.

### 6.3 Submit once

The executor releases the dispatch and writes the `dispatching` journal entry, including its timestamp. It then makes the `dispatch` page call:

1. The page checks the ticket first: token, nonce, account and expiry. If the ticket became invalid only after the release (extremely rare), the page does not submit and returns `refused_before_request`. This is handled as "send result pending confirmation", with no new proof kind, and the existing resolve entry confirms that nothing was sent.
2. The page performs one `fetch` with a timeout of about **25 s**, below the 30 s page-call budget.
3. There is no login probe, no Sent read, no fixed wait and never an automatic resend.

### 6.4 Decide from the response

The result is **accepted by QQ** only when all of these hold:

1. HTTP 200, and `response.url` is the request URL (same origin, no redirect);
2. the body is JSON of at most 1 MiB with `head.ret === 0`;
3. `body.messageid` and `body.mailid` are valid provider IDs;
4. after the response, the page account is still the sending account.

Then:

- **Receipt and ID use.** Until a live check confirms that `mailid` equals the Sent row's `emailid`, the receipt is `{status:"sent"}` **without** `provider_message_id`, and the draft shows the existing "waiting to check the sent original". The private journal keeps `mailid`, `messageid`, `template_version` and timings. After confirmation, a release configuration switch enables `provider_message_id = body.mailid`.
- **Wording.** The existing "服务商已确认发送" (provider confirmed the send) is kept. It means QQ accepted the send, never that the recipient received it. The UI does not change.
- An HTTP 200 alone is not success. `ret=0` without IDs is handled as pending confirmation (section 7).

### 6.5 Lightweight finish

After a success, only three things happen: the `sent` journal entry is written (one fsync for the file, one for the directory), the page ticket is cleared, and the page is returned parked (`retain:true`). There are no `parkSendTemplate` calls, no navigation and no waiting for the page. The private request file was already deleted inside the guarded process.

## 7. Special cases

### 7.1 Classification and handling

| Case | Detection (explicit signals only) | Handling |
|---|---|---|
| Login clearly invalid | Before sending: login page visible. After sending: a login code `-5012`, `-20002`, `-20062`, `-20071` or `-21021` | Before: `email_login_required`, nothing sent. After: `not_sent` with `kind:"provider_rejected"` and a login reason. The Gateway sets the setting to `login_required`, and the existing UI entry asks the user to sign in again. No automatic sid refresh, no resend. |
| Account cannot be determined | Account unreadable within the bound (page not rendered, DOM change, …) | `email_account_identity_unavailable`, nothing sent; the user is asked to check ("account cannot be determined"). Never classified as an invalid login. |
| Account differs | `preflight` comparison fails | `email_account_identity_mismatch`, nothing sent; the user confirms or switches the account. |
| Parameter error | Before sending: local validation fails. After sending: a parameter code `-20011`, `-21101`, `-5151`, `-21501` or `-21132` | Before: `email_send_invalid_input` naming the exact field. After: `not_sent`/`provider_rejected` with the specific code. `-20011` suggests template drift: report `template_drift` and fix the script. |
| Temporarily refused | `-21519` (draft saving), `-20003` (too frequent) | `not_sent`/`provider_rejected` with a temporary reason; the user resends later. |
| Any other nonzero code | Well-formed JSON whose `head.ret` is a nonzero integer outside the lists above | Always "send result pending confirmation" (7.3). |
| Page, script or template unavailable | Reader missing or wrong version, nonce changed, a precondition fails | Explicit recovery before any dispatch is released: navigate back to the mailbox once and run `preflight` once more. If that still fails, return `email_native_send_unavailable` with a reason such as `reader_unavailable`, `sid_missing` or `template_drift`. **No fallback to a DOM send, and no entry for the user to switch to page sending.** |
| Submitted but undecidable | Network error, timeout, non-200, non-JSON, `ret=0` without IDs, account changed after the response, another nonzero code, `refused_before_request` | Mark "send result pending confirmation" (existing `unknown` semantics and wording), then one separate Sent lookup (7.3). |

**v1 never retries automatically for any error code**, including `-21519`.

### 7.2 Basis for `provider_rejected`

Only these 12 listed codes mean "not sent":

- login (5): `-5012`, `-20002`, `-20062`, `-20071`, `-21021`;
- parameter (5): `-20011`, `-21101`, `-5151`, `-21501`, `-21132`;
- temporary (2): `-21519`, `-20003`.

The basis is QQ's own client behavior: it treats `ret≠0` as failure and keeps the composed message, and for login codes it refreshes the session and sends again. That shows the server did not accept the message. This is an inference from client behavior, so it applies only to the listed codes; every other nonzero code is pending confirmation.

### 7.3 Sent lookup after "pending confirmation"

- **When:** after `dispatch` returns, in one separate page call. It polls within the page-call budget for at most 12 s (QQ was measured listing a send 1.5–4.6 s after accepting it). The lookup runs only on the pending path, never on the normal path.
- **With a `messageid`:** match it exactly, and also require `dirid=3` and the same subject, sender and sole recipient.
- **Without a `messageid`:**
  - QQ server time is authoritative: estimate the local-to-server clock offset from the `Date` header of the lookup's own `/list/maillist` response;
  - the lower time bound is the `dispatching` write time, converted to server time, minus the allowed clock skew (120 s for now);
  - only rows whose `fromtime` is not earlier than that bound are matched, by subject, sender and sole recipient.
- **Result:** only a unique match becomes `sent`, with `qq_sent_list_v1` evidence. Zero or several matches stay pending. There is never an automatic resend; after checking, the user decides through the existing "check send result" and resolve entries.

## 8. Executor review: returning `not_sent` after dispatch

- **State after release.** After `releaseDispatch()`, the ledger row has `effect=1` and the dispatch is `released`. From then on, a thrown error or a returned `blocked`/`waiting_confirmation` becomes `uncertain` (the `finish` branches in `executor.mjs`). `provider_rejected` must therefore be returned as `status:"completed"` with data `{status:"not_sent", not_sent:{kind:"provider_rejected",…}}`.
- **Dispatch phase.** A completed external write is recorded as `dispatch:"sent"`. That value means "the released dispatch has a terminal business result", not "mail was sent". The ledger schema does not change, but every reader of `dispatch` must be audited (lifecycle lookup, anchor, GC, diagnostics) to confirm none treats it as the send result. The business result is in `data`.
- **Output validation.** `spec.output` validates `data`, so the output schema's `not_sent.kind` must allow `provider_rejected`, with reasons limited to the list in 7.2. `not_sent` still forbids `provider_message_id`. The proof's `executionEvidence` (task_id, digests, ledger_epoch) is available in normal mode.
- **ACK.** The Gateway currently sets `SendResult.Lifecycle`, and therefore ACKs, only when `output.Status=="sent"` (`playwright_runner.go`). An un-ACKed `not_sent` terminal result would stay in the executor until retention expires, so this must be extended to set and ACK `not_sent` as well.
- **Journal and reconcile.** The journal allows an atomic `dispatching → not_sent` replacement. A later reconcile returns the saved `not_sent` receipt, which existing logic already supports.
- **Cleanup.** A login rejection returns `retain:false` and invalidates the page, so the next send starts cold. Other rejections park the page as usual. `reclaim` journal removal is unchanged.
- **Crash.** A crash after the response but before the journal write leaves the row `uncertain` and the journal at `dispatching`. It is pending confirmation and never resent, as today.
- **Epochs.** Epoch drain and seal are unaffected.

## 9. Earlier send optimizations

| Earlier design | Status | In the new flow |
|---|---|---|
| Resident page transport `cli-page-transport.mjs` ([send latency](email-send-latency.md) 2.1) | deployed in release 35, switched off on work2 | **Usable; recommended as an accelerator.** Requests go over stdin, never into arguments, and one helper runs the topology checks and `run-code` without spawning a process per page call. It was switched off because the QQ **DOM** send clicked the Sent folder before it rendered (the `sent_baseline` race). The new native path has no DOM clicks and is unaffected. However, the same QQ send page still serves DOM sends outside v1, so re-enabling the transport requires first fixing page readiness in the DOM flow (wait for the folder before clicking). Until then, the native path works with private request files. |
| Read-only batch `cli-read-batch.mjs` | deployed | **Reused directly** as the base of the guarded code request in section 5: topology check, `run-code` and re-check in one process, with the request in a 0600 file. It needs a non-read-only variant and a separate `argument` field. |
| Gateway login-check reuse (120 s) | deployed, ineffective live | **No longer needed** for QQ sends, since the in-page check replaces it. Kept for Gmail and Outlook only until they move to the in-page check, then retired once all three have switched. The cause of its ineffectiveness is still open, but it is not on the new path. |
| Mutation lanes ([architecture](email-network-native-architecture.md) 9.4) | deployed | **Kept**, as a prerequisite: sends do not block inbound mail. |
| Warm send page reuse (`reuse_idle_ms` 15 min) | deployed | **Kept.** Cold pages are now cheap; whether to extend the idle time depends on measurements. |
| Batching a send's guarded steps into one process (9.4 follow-up) | not implemented separately | Covered by the resident transport plus the guarded code request; **not pursued separately**. |

## 10. Expected effect (estimates, to be replaced by measurements)

| Item | Release 35 warm native | New flow |
|---|---|---|
| Probe before sending | 1 per message (about 2.7 s) | 0 (only when the state is doubtful) |
| Page calls | about 8, each with topology-check processes before and after | 2 (`preflight`, `dispatch`); 1 more lookup only when pending |
| Sent reads | baseline + polls | 0 (only when pending) |
| Fixed waits | 1 s + poll intervals | 0 |
| First message on a cold page | DOM teaching, about 80 s | navigation + 2 page calls |

- **Executor about 2–3 s, assuming:** each page call starts a single guarded process that performs its topology checks internally (section 5), or the resident transport is on. On work2, a page call takes about 0.5–1 s and the request itself about 0.5 s. If page calls still run command by command, each also starts a `tab-list` topology-check process before and after it, adding roughly 1–2 s to the executor.
- **API time:** the release 35 warm API took 18.6 s, of which the executor took 6.4 s. The roughly 12 s outside the executor is Gateway preparation, approval binding, scheduling, journaling and ACK, including the probe of about 2.7 s. This design removes only the probe; the rest of those roughly 12 s is **outside its scope**. On that breakdown, the warm-page API time is expected to be about 10 s (roughly 10–12 s).
- These are estimates, not results, and must be replaced by measurements.

## 11. Component changes

In the table below, the admission declaration, the guarded code request, the `provider_rejected` proof, ACK and the journal transition are shared interfaces, with QQ as their first user. The template, the rejection-code list and the body rule live in QQ-specific modules (section 15).

| Component | Change |
|---|---|
| App-CLI Reader (maintained template module) | Remove learning, confirmation, TTL and baseline logic. Add `preflight`, `dispatch` and the pending-path lookup. Implement the 4.2 body rule and the 4.3 sid method, and record the drift signal. |
| App-CLI `managed-send.mjs`, `handlers.mjs`, `mail-page.mjs` | The native branch uses two page calls, and content travels only with `dispatch`. For v1 requests, skip `setSecrets`, `prepareMailRound` and `parkSendTemplate`. Cold pages also take the native branch. A failed template is reported explicitly, with no DOM fallback. A release configuration switch replaces `APP_CLI_MAIL_NATIVE_SEND`, and a separate switch controls the `mailid` receipt. |
| App-CLI `send-journal.mjs`, output schema, `send-recovery.mjs` | The journal supports `dispatching → not_sent(provider_rejected)`; a new proof kind `provider_rejected` has a fixed reason list. |
| Browser Controller `host-page.mjs`, `cli-read-batch.mjs`, `cli-page-transport.mjs` | Add the guarded code request for mutation pages (`code` + `argument`, through a one-use 0600 file or transport stdin), and remove leftover files at page start. Message content never enters any argument list. |
| Gateway `app/email_send_proof.go`, `playwright_runner.go` | In normal mode, accept `not_sent` + `provider_rejected` bound to the original invocation; set `Lifecycle` and ACK for `not_sent` too. |
| Gateway `emailmanagement/compose.go` | Map `provider_rejected` to draft `failed` with the specific code; for login reasons, also set the setting to `login_required`. |
| Gateway `Admit` | Skip the probe for providers that declare `send_admission: in_page`. |
| UI | No change. |

## 12. Unchanged safety invariants

- At most one `func=1` request per send. An unclear result is never resent and never falls back to another path. v1 never retries automatically for any code.
- The dispatch release and the `dispatching` journal entry both precede the request. Any failure before them proves nothing was sent.
- Requests are sent by the browser, only from a signed-in, same-origin page whose account has been checked. No IMAP, SMTP, OAuth or client credentials.
- Message content never enters command-line arguments. No sid, body, raw recipient or raw response is recorded; evidence keeps only hashes, codes and timings.
- Historical `unknown` sends are never resent.

## 13. Implementation and validation plan

1. **Local implementation** (code changes only after owner approval): make the changes in section 11 and add regression tests. At a minimum the tests cover:
   - the field set and every value rule;
   - time zones, including a DST zone;
   - body conversion: leading and trailing spaces, runs of spaces, tab stops, `&<>`, empty lines;
   - sid priority and a disagreement between the two sources;
   - a counterexample for every success condition;
   - the 12-code list and codes outside it;
   - an unreadable account versus a visible login page;
   - `refused_before_request`;
   - the pending lookup's time bound and uniqueness;
   - no DOM fallback;
   - no message content in any argument list (checked through `/proc/<pid>/cmdline`);
   - request-file deletion on success and failure;
   - ACK of `not_sent`;
   - every invalidation signal of probe-free admission.
2. **Paired release:** App-CLI, SparkClaw (including the Browser Controller) and a new Tampermonkey version, released together.
3. **Live qualification** (each message needs explicit owner approval; only new, brief test messages):
   - **First message on a cold page:** confirm no probe, two page calls, `ret=0` and no message content in arguments. Check `mailid == emailid`, the `from` display name and body whitespace with one independent Sent read.
   - **Second message on the warm page:** record API, executor and page-call times.
   - **Third message after a page reload:** confirm no DOM teaching is needed.
   - **At every step:** confirm the number of historical `unknown` rows is unchanged and no message was sent twice.
4. **Enable and record:** after the `mailid` check passes, enable the `mailid` receipt switch. Update this document, architecture section 9 and the cluster status with the measurements.

## 14. Cross-project impact

Only SparkClaw (the Gateway and the Browser Controller) and its own App-CLI fork are involved; no ISCP or other project interface changes. The implementation is recorded in the review notes of InfiniCenter decision 0049.

## 15. Extending to Gmail and Outlook

QQ is the anchor of this pattern, not its end. The implementation must keep shared parts and QQ-specific parts apart, so that QQ details are never hard-coded into the shared path.

### 15.1 Shared parts (all three providers; QQ implements them first)

- **Admission.** The account is checked in the page, and the pre-send probe is skipped for providers that declare `send_admission: in_page` (6.1).
- **Content transfer.** Message content goes through a private request file or transport stdin, is transferred once, and never enters command-line arguments (section 5). The Browser Controller's guarded code request serves every mutation page.
- **Call structure.** Two page calls (`preflight`, `dispatch`) with a one-use ticket; the dispatch is released and the journal written before the request.
- **Result decision.** Only the provider's response decides the result. Success needs the provider's own success flag and a valid message identifier; HTTP 200 alone is not enough. Listed rejection codes become `provider_rejected`; every other code is pending confirmation (section 7).
- **Pending handling.** One separate bounded Sent lookup that must find a unique match, and never an automatic resend.
- **Executor, ACK, journal and cleanup** (section 8).
- **Failure handling.** Reasons are reported explicitly, with no fallback to a DOM send.
- **Template management.** Maintained templates carry versions and drift signals; a new receipt ID stays behind a switch until checked live.

### 15.2 Provider-specific parts (maintained per provider)

| Item | Decided separately for each provider |
|---|---|
| Maintained template | send endpoint, fields, encoding, and session parameters (QQ's sid) |
| Account source | a trustworthy current account and sending account in the page |
| Success decision | the success flag and message-identifier fields |
| Explicit rejection codes | lists of login, parameter and temporary codes |
| Sent lookup | the lookup request, the match fields and the server-time source |
| Body conversion | a plain-text-to-HTML rule identical to that provider's editor |

The `provider_rejected` reason lists and the output schema are separated per provider, and the Gateway mapping is configured per provider.

### 15.3 Feasibility check before each extension

Use the same method as section 3:

1. From the provider's public front-end scripts, check how the send request is built, how the response is interpreted, and the error codes. Evidence comes only from public static assets and read-only cache indexes.
2. Compare with the request shapes recorded by the learning window and with existing live DOM-send results.
3. Write the maintained template and the explicit rejection-code list.
4. Qualify each provider live, with owner approval for every message.

Known starting points: some Outlook writes go through a Web Worker and MessagePort channel, which the learning window can already observe ([architecture](email-network-native-architecture.md) 9.3). Gmail and Outlook both send through the DOM flow today, and neither has evidence yet for a send-request template. No feasibility conclusion is drawn for them before that check.

## 16. Open questions

- Whether `from` should carry the nickname (3.4, item 1).
- Whether `is_text=true` can replace the HTML body (4.2).
- Whether to add an offline pre-release check that applies the method in 3.1 to new `home-web` and `XmailEditor` bundles on the public CDN and compares the parameter builder, body rule and sid method.
- When to open several recipients and cc.
- When to re-enable the resident page transport, which depends on fixing DOM-send page readiness (section 9).
- The order of the Gmail and Outlook extensions, and whether replies and attachments are brought over at the same time (section 15).

## 17. Implementation status (2026-10-23, local)

| Component | Implemented as |
|---|---|
| Shared native send | App-CLI `lib/native-send.mjs`: preflight with one recovery navigation, release + `dispatching` journal, one dispatch, response decision, one bounded lookup; no DOM fallback, no resend. |
| Release switches | `lib/native-send-config.mjs` (`enabled`, `receipt_provider_message_id`); `APP_CLI_MAIL_NATIVE_SEND` is retired. QQ is enabled; the `mailid` receipt is off. |
| QQ modules | `lib/qq-native-send.mjs` (scope, recovery page, 12 codes) and `userscripts/lib/qq-sendmail-v1.mjs` (fields, time zones, `r`, body rule, sid, lookup). `qq-send-prototype.mjs` is removed. |
| Journal, schema, recovery | `dispatching → not_sent(provider_rejected)` replaces atomically; `provider_rejected` carries a category `reason` and a listed `provider_code`. |
| Handlers | Native sends skip `setSecrets`, background preparation, `prepareMailRound` and `parkSendTemplate`; decided pages stay parked, a login rejection invalidates the page. |
| Browser Controller | `HostPage.runCodeRequest` (code ≤ 72 KiB, argument ≤ 512 KiB) through transport stdin (`code_request`) or a one-use 0600 `code-request-*.json` file that the guarded process deletes first; leftover files are removed at page start. Refused without the Linux `/proc` proof. |
| Gateway | `provider_rejected` proof validation, `not_sent` accepted and ACKed in normal mode, draft codes `email_send_rejected_login_required` / `_invalid_parameter` / `_temporarily` and `email_send_template_drift`, login rejection → setting `login_required`, `send_admission: in_page` → no pre-send probe. |
| UI | Unchanged. New draft codes show the existing generic send-failure text. |

Not done yet: the paired release (App-CLI build, SparkClaw pin and `provider_scripts.json` sync, new Tampermonkey version), the daemon log/trace check in section 5, and the live qualification in 13.3. A login or account failure in preflight still reaches the draft as `email_send_not_dispatched`, because the runtime reports an undispatched send that way; the setting moves to `login_required` only on a post-send login rejection or the next probe.

