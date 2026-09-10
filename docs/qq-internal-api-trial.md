# QQ Web Session API Trial

> Language: English | [简体中文](../zh-cn/docs/qq-internal-api-trial.md)

Status: 2026-09-10. The owner authorized a Tampermonkey userscript and a real QQ speed trial. The [userscript](../scripts/email/userscripts/qq-internal-reader.user.js) is implemented and its exact source ran successfully in a logged-in QQ task page through SparkClaw's existing CLI test runtime. Persistent installation and execution **through Tampermonkey remain unverified**: the browser has an unpacked Tampermonkey 5.5.0, but Codex has no UI connection to that browser, and the email script runtime rejects extension-management origins. No security gate was relaxed.

## Verified behavior

The current page's observed `/list/maillist` request supplies the in-memory session parameter. The script fetches the current ordinary folder's first page (50 rows) and retrieves individual original EMLs using `/read/readmail?func=5&mailid=…`, matching the export operation found in QQ's current public application bundle. Credentials remain in the page session; no Cookie, SID, email address, subject or body is exported in timing reports. Raw EMLs remain in page memory until explicitly downloaded. No Gateway integration, periodic job, remote upload, sending or mailbox-management operation was added.

Buttons fetch three originals, run three repetitions, download the timing report, download each EML, or stop and clear. Samples default to at most 1 MiB per mail; the underlying response reader caps each response at 25 MiB and 15 seconds. Requests are same-origin GET, use `credentials: same-origin`, reject redirects and use `cache: no-store`. Login/error envelopes and non-EML responses fail visibly. Initial scope excludes pinned and conversation/group rows; there is no complete-folder or complete-history claim. To install manually, create a Tampermonkey script, replace the template with the linked source, save, and reload a logged-in `wx.mail.qq.com` folder. If the browser asks to enable userscripts, complete that setting in the browser before checking the panel.

## Measurements

| Stage | Observed time |
| --- | --- |
| List 50 mails, three runs | 217.6 / 144.4 / 146.5 ms |
| Direct original EML, nine reads of three mails | Mean 152.3 ms; range 118.5–203.0 ms |
| Existing `collectUnread` + native export, three mails | 10,452.1 / 10,974.0 / 10,510.8 ms |
| Three complete direct rounds, each list + three originals | 1,881 ms total |

The observed mean per-original ratio is about 69.9×. This is a practical pipeline comparison: the UI path includes existing identity inspections, Controller/CLI round trips and native disk download; the direct path ends with bytes and a hash in page memory. Both begin with an authenticated browser already loaded. It does not isolate server latency or establish general throughput. `no-store` does not rule out provider-side caching. Only three small messages (16,106 / 7,176 / 15,068 bytes) were tested; large attachments and continuous synchronization remain unqualified.

All nine direct downloads matched declared sizes and retained identical per-mail SHA-256 hashes across rounds. The three corresponding native exports contain exactly the same bytes plus one trailing CRLF (two bytes); raw file hashes therefore differ. This is recorded explicitly, not treated as byte-identical files.

Evidence: [direct execution](evaluation/qq-internal-api/direct-script.json), [UI baseline](evaluation/qq-internal-api/ui-baseline.json), [byte comparison](evaluation/qq-internal-api/source-comparison.json), [metadata and limits](evaluation/qq-internal-api/metadata.json). Five focused Node tests cover request scope, credential-free reports, missing login, error/non-EML rejection and bounds/disposal. Syntax and bilingual links pass. Initial login was absent; the owner logged in before successful trials. Failed preliminary UI timing attempts are described in metadata; successful retries used explicit QQ navigation and retained credential-output guards.

The application containers were stopped at trial start. Only PostgreSQL was temporarily started to let the existing Vault-backed test harness obtain the browser control credential, and it was stopped again afterward. Browser/login state was preserved; normal receiving was not started. Temporary Go/shell harnesses and downloaded trial EMLs were removed; retained reports contain timing and integrity metadata only.

## Tuesday Correspondence Follow-up — 2026-09-10

The owner selected Tuesday's reply mails to test full correspondence retrieval. The September 8 `测试` / `Re: 测试` sample contains **two separate RFC reply chains**, not one event implied by a common subject:

| Chain, local time (Asia/Shanghai) | Delivered originals | Directions |
| --- | --- | --- |
| 15:11:01–15:21:54 | 4 | Sent → inbox → Sent → inbox |
| 15:29:51–15:31:13 | 3 | Sent → inbox → Sent |

The observed-session `/list/search` POST was qualified against the current public QQ app implementation. A page size of two exercised eight actual pages, totaling all 16 reported results with no locked matches or repeated IDs. Seven were this exact subject's delivered originals; one matching unsent draft and eight other subjects were excluded. A second complete census confirmed the same seven target IDs and no additional exact-subject delivered matches in other folders. The originals were fetched via `/read/readmail?func=5` without opening individual detail pages.

All seven EMLs match their declared sizes (47,764 bytes total), parse without MIME defects, and carry unique Message-IDs. Every In-Reply-To/References edge resolves within the seven originals. Neither chain has a missing referenced predecessor, and both include the owner's Sent messages. Search plus seven original downloads took **2,490 ms**, including 1,456 ms for eight search pages, on an already-loaded authenticated page. These originals contain no attachment parts, so this sample does not qualify attachment retrieval.

The private local deliverable `data/qq-thread-trial/qq-tuesday-conversations.zip` contains seven EMLs ordered into the two chains plus integrity metadata. Only redacted metadata is stored in repository evidence: [capture](evaluation/qq-internal-api/thread-sample/capture.json), [reference graph](evaluation/qq-internal-api/thread-sample/integrity.json), [scope and limits](evaluation/qq-internal-api/thread-sample/metadata.json). Browser control returned to idle; PostgreSQL was restored to its previously stopped state after the temporary Vault-backed test.

This verifies this sample's enumerated search results and closed reply references, not the absence of deleted, inaccessible or subject-changed unseen descendants. The mailbox currently exposes individual rows. `/list/collo_maillist` and its native aggregate-ID parsing were found in public QQ source, but that grouped-member endpoint was not live-qualified. This was a targeted search-and-originals network trial; a general thread reader in the userscript and Tampermonkey installation remain separate work.
