# Desktop experience and ISCP stream joint validation

Language: English | [简体中文](../zh-cn/docs/desktop-stream-integration-validation.md)

Date: 2026-10-18. The user authorized integration and local `main` after validation, with no push or deployment. This report supersedes the separate branches' pending integration gates in [desktop validation](desktop-experience-validation.md) and [stream implementation](iscp-application-stream-implementation.md). All new runs use disposable identities, profiles and PostgreSQL data. The installed client and live services are unchanged.

## Integrated source

The common bilingual design baseline is `cd7d5ccc`. The isolated integration branch first merged desktop `2e5e96cb` and then stream `071684d4`, producing `63559c91`. The shared message conflict retains both the persistent `data-message-id` and stable `execution:<request_id>` scroll anchor. The original workbench, browser, menus and composer remain in use. Post-save content reads retain the streaming publication-version guard.

Cross-review found and fixed three behaviors before acceptance:

- A successful durable upload followed by a read failure no longer offers to upload the same file again. A failed first upload on the welcome screen belongs to its actual new conversation and retains its file/draft through navigation. Desktop commit `74103b2f` was integrated as `4c7c526f` with `readContent(id).catch(surfaceError)`.
- Appending to a selected native text node collapsed its selection even though the article and node were retained. `RenderedMessageText` now snapshots the native selection before DOM mutation and restores it only while the selected rendered prefix remains identical. It preserves direction and does not restore stale text after a replacement or interfere with another answer's selection.
- Selection endpoints at a line or paragraph start must retain their exact DOM boundary, rather than the preceding node's equal text offset. The follow-up preserves connected original endpoints when they still identify the same text. Five regressions cover forward/backward selection, inline nodes, another answer, changed text, and line/paragraph boundaries.

The final product and fixture revision is `6608a8fe`. Ancestry merge `ef0dbfa5` includes the reviewed desktop follow-up and has a byte-identical Git tree. Subsequent changes only record this report and its evidence. No Gateway source changed after the combined Go suite was captured at `63559c91`.

## Joint acceptance

The [sanitized receipt](evidence/desktop-stream-integration-2026-10-18.json) records exact revisions, build/module hashes, log and raw-receipt fingerprints, counts and timings. Final native runs start from a clean tracked tree and use production automatic scrolling; harness scroll count is zero. The original `--long-text` assisted mode remains only for historical comparison and is excluded from these acceptance results.

| Check | Result |
| --- | --- |
| WebChat / Desktop | 351 / 427 tests passed; no desktop skips |
| Linux Gateway | 73 test packages passed with cache disabled; 11 packages without tests; build and vet passed |
| Frontend | Strict TypeScript and production build passed |
| Native browser panel | 48 window/text/sidebar/zoom cases, native-view bounds, modal occlusion, retained page, focus layout and keyboard divider behavior passed |
| Native visual matrix | 432 combinations, 88 retained screenshots, no layout assertion failures; representative light/large and dark/large screenshots inspected |
| Existing scroll/input interaction | Async conversation reading-position restoration, selected text, return-to-latest and 31-line textarea passed; height 168 px, input feedback 12 ms over two frames |
| 20 controlled native streams | 2,000 batches, 20 durable deliveries; P95 109.18 ms and P99 116.39 ms conservative upper bounds; no blank, missing, duplicated or remounted answer rows |
| 6,000-character automatic follow | Complete source/commit/render digests agree; tail remains visible with zero harness scrolling; same row through final delivery |
| Two 6,000-character reading cases | Upward scrolling and selection remain stable during growth; one case holds selection through final delivery; original return-to-latest resumes following |
| Helper interruption | Original request resumes in 1326.90 ms; one submit, two subscriptions and one durable final ACK; required authority rebind hides the preview once |

Healthy controlled production is 100 batches at 100 ms intervals, through PostgreSQL 18, continuous encrypted Relay/Endpoint, the actual helper private pipe, Electron main/ClientStore and the original renderer. No direct business HTTP or progress polling is used. The repeated streams and long answers all execute once and acknowledge durable final results. The timing method observes DOM visibility followed by the next animation frame, a compositor-paint proxy with measured cross-process clock bounds. Reading cases deliberately move the tail offscreen: their timing fields qualify rendering, not visible latency.

The failed native selection run is retained, as is a subsequent harness failure in which a queued observation captured the following phase before intentional upward scrolling. That harness now samples phase at measurement time; product thresholds were not loosened. The Linux setup script's `--user` option was rejected by the prepared image's Python virtualenv. All required Node/Python document modules were verified in that same image before the successful full suite. The earlier four macOS full-Go failures remain baseline platform limitations, not claimed passes.

## Reproduction and limits

Use Node 26, npm 11, Go 1.25 and the independent worktree dependencies. Run the native commands serially because they create actual Electron windows and rebuild the UI:

```sh
npm run test:webchat
npm run test:desktop
npm run build:desktop-ui
node apps/desktop/test/run-browser-panel-layout.mjs
node apps/desktop/test/run-experience-interaction.mjs
node apps/desktop/test/experience-visual-run.mjs
node apps/desktop/test/run-stream-native-qualification.mjs 20
node apps/desktop/test/run-stream-native-qualification.mjs 1 --auto-follow
node apps/desktop/test/run-stream-native-qualification.mjs 2 --joint-ui
node apps/desktop/test/run-stream-native-qualification.mjs 1 --recovery
```

Linux used the existing prepared test image `sparkclaw-public-iscp-linux-suite:s1lz19o5` (digest `865ab6631f4b8994769d43966141a539b9d1ca1a77bf60b9a639b4324bc946e0`), replacing source only inside a disposable container while preserving its Linux dependencies. The full command was `go test -count=1 ./services/gateway/...`, followed by build and vet. No service was deployed.

Native results cover macOS Apple Silicon. System IME, screen-reader behavior, other-platform font rendering and x64 native runs remain outside this acceptance. The earlier production-Agent real-model result is retained in the stream report; integration did not repeat that model request or modify the model environment. ClientStore schema 11 and matched Gateway/Relay/helper/Electron capabilities remain requirements for a future release. ISCP upstream extension acceptance stays proposed; existing public contracts and the SDK pin are unchanged.
