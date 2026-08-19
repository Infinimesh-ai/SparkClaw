# WebChat Voice Input Loop Design

> Language: English | [简体中文](../zh-cn/docs/webchat-voice-input-design.md)

> Status: Phase 1 is implemented in the active worktree; Phase 2 is a detailed
> design, updated 2026-08-19. LLM polishing remains deferred until the native
> realtime voice loop meets the acceptance gates below.

## Decision Summary

The first objective is a reliable WebChat-only dictation loop:

```text
click microphone
  -> acquire the selected browser microphone
  -> record with visible level and elapsed time
  -> explicit stop or cancel
  -> encode bounded 16 kHz mono PCM16 WAV
  -> transcribe through the existing speech adapter
  -> insert into the current draft without overwriting newer edits
  -> owner reviews/edits and explicitly sends
```

Phase 2 extends that loop with native record-time inference: WebChat sends
continuous PCM through one authenticated stateful session, displays revisioned
partial snapshots while the microphone remains active, and reconciles one final
snapshot after manual or optional silence stop. The complete in-memory WAV is
retained only as a failure fallback; it is not sliced into pseudo-streaming
requests.

Initial Phase 2 qualification keeps the current `Qwen/Qwen3-ASR-0.6B` model and
changes only the serving runtime needed to expose its native streaming state.
Silence auto-stop is Off by default. If realtime fails after capture starts,
WebChat immediately stops and flushes capture, automatically batch-transcribes
all PCM recorded through that boundary, and ends the operation; it never keeps
recording after a mid-stream failure.

LLM polishing is not part of this critical path. It becomes an optional
post-transcription draft enhancement only after recording, transcription,
retry, cancellation, resource cleanup, and draft insertion are stable.

The interaction remains inside the current WebChat composer. Phase 1 adds no
global hotkey, wake word, tray process, background listener, system-wide
push-to-talk mode, or cross-application cursor insertion. The owner starts and
stops recording with the visible microphone control, and SparkClaw never sends
the resulting text automatically.

The existing implementation already covers much of the happy path. The main
stabilization gaps are microphone selection and fallback, runtime device-loss
detection, retrying transcription without repeating the recording, explicit
handling of stale draft anchors, focused state-machine tests, and an end-to-end
deadline covering both queueing and ASR inference.

## Reference Scope

[OpenLess](https://github.com/Open-Less/openless/tree/9f360e20) is a behavioral
reference, not a dependency. Its useful voice-input lessons are:

- one explicit dictation state machine and a unique generation/session ID for
  rejecting late callbacks;
- separate startup, recording, processing, cancellation, and terminal states;
- visible input level and elapsed time while recording;
- preferred microphone selection with fallback to the system default;
- explicit startup and runtime capture errors, including a recorder liveness
  watchdog;
- cancellation that owns recorder and downstream request cleanup;
- optional silence auto-stop only as a user-selected recording mode; and
- recovery paths which avoid losing spoken content.

Relevant OpenLess references include its
[dictation state machine](https://github.com/Open-Less/openless/blob/9f360e20/openless-all/app/src-tauri/src/coordinator_state.rs),
[recorder and device handling](https://github.com/Open-Less/openless/blob/9f360e20/openless-all/app/src-tauri/src/recorder.rs),
[silence auto-stop](https://github.com/Open-Less/openless/blob/9f360e20/openless-all/app/src-tauri/src/coordinator/silence_auto_stop.rs),
and
[microphone selection UI](https://github.com/Open-Less/openless/blob/9f360e20/openless-all/app/src/pages/settings/MicrophoneSelect.tsx).

SparkClaw should not copy OpenLess's Tauri/Rust runtime, global hotkeys,
accessibility permissions, clipboard fallback, arbitrary-application insertion,
tray/capsule UI, audio archive, provider matrix, or persistent dictation
history. Those solve a system-wide input-method problem. SparkClaw currently
needs a bounded voice control inside one authenticated WebChat composer.

## Scope Decisions

| Capability | Phase 1 decision | Reason |
|---|---|---|
| Composer microphone button | Keep and harden | It is the only supported activation surface |
| Manual click-to-start/click-to-stop | Keep | Predictable in desktop and mobile browsers |
| Escape/cancel during any active phase | Keep and test | Required for owned cleanup |
| Live input level and elapsed time | Keep and harden | Confirms that capture is active and using the expected device |
| Microphone selection and default fallback | Add | Essential on multi-device desktops and after device changes |
| Same-recording transcription retry | Add with an in-memory buffer | Prevents a transient ASR failure from losing the utterance |
| Maximum-duration auto-stop | Keep | Enforces the existing upload and inference bounds |
| Silence/VAD auto-stop | Defer, default off if later added | Threshold errors can truncate speech; it is ergonomics, not base reliability |
| Streaming/partial ASR | Defer | Batch transcription is simpler to validate and recover |
| Persistent voice/audio history | Do not add | Final sent messages already provide useful history; audio privacy cost is high |
| LLM polishing and style modes | Defer | Optional post-transcription enhancement, not a Phase 1 dependency |
| Global hotkey, wake word, push-to-talk | Out of scope | The user explicitly activates voice inside WebChat |
| Cross-application insertion or clipboard fallback | Out of scope | WebChat owns its composer and draft state directly |

## Goals

1. Make one recording attempt have a deterministic lifecycle with no stuck UI,
   leaked microphone track, leaked `AudioContext`, or late callback affecting a
   newer attempt.
2. Preserve spoken content across retryable network, capacity, timeout, and ASR
   failures without persisting audio.
3. Insert a successful transcript at the captured composer selection when it is
   still valid, and never overwrite text edited while transcription was in
   flight.
4. Give owners clear status and recovery actions for permission, device,
   recording, encoding, service readiness, busy, timeout, and empty-transcript
   failures.
5. Preserve the current privacy boundary: transcription creates no message,
   Agent run, Tool Call, approval, or artifact, and raw audio is not retained by
   Gateway.
6. Keep the final send explicit and preserve the sent owner text unchanged.

## Non-Goals

- Always-listening or wake-word behavior.
- Desktop-global shortcuts or recording while WebChat is not active.
- Push-to-talk keyboard bindings, earbud media-button triggers, or OS-level
  microphone controls.
- Replacing the current OpenAI-compatible speech provider contract or adding a
  broad ASR provider marketplace.
- Automatically sending a transcript to the Agent.
- Automatically rewriting, summarizing, or interpreting a transcript.
- Storing audio in local storage, IndexedDB, Gateway Store, artifacts, traces,
  or audit.
- Applying this WebChat flow to Telegram or Weixin voice notes in Phase 1.

## Current Baseline And Gaps

SparkClaw already provides:

- browser `getUserMedia` capture through an `AudioWorklet`;
- mono capture, level reporting, resampling, and canonical 16 kHz PCM16 WAV;
- minimum duration, maximum duration, and maximum upload checks;
- visible permission, recording, encoding, transcription, disabled, and error
  states;
- Escape and session-switch cancellation through an operation generation;
- a bounded Gateway upload and OpenAI-compatible transcription adapter;
- explicit busy, timeout, unavailable, cancelled, and inference error codes;
- bounded ASR concurrency and pending admission;
- insertion into an unsent per-session draft; and
- metadata-only speech audit with `audio_retained: false`.

The design addresses these gaps:

1. Capture always uses the browser default device; there is no preferred-device
   picker, live preview, or saved client-local device choice.
2. Capture does not project `MediaStreamTrack.onended`, audio-context failure,
   or a first/last-sample liveness deadline into the voice state.
3. The encoded WAV is discarded after a failed transcription, so retry means
   repeating the recording.
4. If the composer draft changes while ASR is running, current insertion falls
   back to appending at the end. That avoids overwrite but can silently put text
   in the wrong place.
5. WAV and insertion helpers have unit tests, but the complete voice operation
   state and browser-resource cleanup paths do not have focused tests.
6. The ASR HTTP timeout does not by itself define one complete queue-plus-
   inference deadline from Gateway admission to response.
7. Gateway currently performs a remote speech health check before each
   transcription and only checks that `session_id` exists. The final contract
   should avoid a redundant health prerequisite and enforce authenticated
   owner/session scope.

## Product Experience

### Normal Flow

1. The owner places the caret or selects text in the current composer and clicks
   the microphone button.
2. WebChat requests permission when needed, resolves the selected device, and
   starts recording only after the audio graph is active and PCM callbacks have
   begun.
3. The composer displays recording state, elapsed time, an input-level meter,
   and a stop control. The existing maximum duration remains visible as a
   limit; reaching it performs a normal stop, not an error.
4. Clicking stop flushes the final worklet samples, closes microphone resources,
   encodes the WAV, and starts transcription.
5. On success, WebChat inserts the transcript at the captured selection if the
   draft snapshot is unchanged and restores the caret after the inserted text.
6. The transcript remains an ordinary editable draft. Only the normal send
   action creates a message and starts routing.

No extra review modal is required for a successful raw transcript. The owner
can read and edit it directly in the composer.

### Device Selection

After microphone permission is available, WebChat may call
`enumerateDevices()` and show a compact device menu adjacent to the microphone.
The selected `deviceId` is origin- and browser-profile-specific, so it belongs
in client-local preferences, not the owner profile or Gateway Store.

Opening the device menu may show a live level preview for the selected device.
That preview owns a separate short-lived stream and must stop every track and
close its audio context when the menu closes. It cannot run concurrently with
an active recording.

At recording start:

1. Try the saved device when it is still enumerated.
2. If it disappeared or `getUserMedia` rejects that exact device as unavailable,
   retry once with the system default.
3. Show that fallback occurred and update the effective selection only after
   capture succeeds.
4. Do not automatically fall back when permission is denied or a general
   security error occurs.

### Retry Without Re-Recording

After recording stops, the encoded WAV remains only in the active browser
operation. A retryable `speech_busy`, `speech_timeout`,
`speech_model_unavailable`, transient network failure, or retryable upstream
failure exposes **Retry transcription**. Retry sends the same WAV and does not
reopen the microphone.

The retry buffer is discarded on success, explicit cancel/dismiss, session
change, a new recording, component unmount, or a short expiry. Five minutes is
the initial proposed expiry. It is never written to browser persistence. A
non-retryable invalid-audio or no-speech outcome requires a new recording.

Phase 1 should prefer explicit retry over automatic retries. This keeps latency
and ASR load visible and avoids repeated work during an outage.

### Draft Conflict Recovery

Every operation captures:

```text
operation_generation
session_id
draft_snapshot
selection_start
selection_end
```

When transcription completes:

- if the same generation and session are active and the draft still matches the
  snapshot, replace the captured selection;
- if the draft changed, preserve the transcript as a pending in-memory
  candidate with **Insert at cursor** and **Dismiss** actions;
- never silently append, replace, or merge into a changed draft; and
- if the session changed or the operation was cancelled, discard the late
  result.

This is the WebChat equivalent of OpenLess's insertion fallback: no spoken text
is lost, but SparkClaw does not need the system clipboard because it directly
owns the destination composer.

### Error Recovery

| Failure | User-visible recovery |
|---|---|
| Permission denied | Keep draft unchanged; allow retry after browser permission changes |
| No device / saved device removed | Refresh device list and offer system default |
| Capture failed before PCM begins | Close all resources and return to record-ready error state |
| Device disconnected while recording | Stop capture, report the selected device loss, and offer a new recording |
| Recording too short | Discard audio and offer a new recording |
| Maximum duration reached | Perform a normal stop and transcribe |
| ASR busy, timeout, unavailable, or transient network error | Keep WAV in memory and offer retry |
| Empty transcript / no speech | Discard retry buffer and offer a new recording |
| Draft changed before result | Keep transcript as a pending insert candidate |
| Session switched or operation cancelled | Abort requests, release resources, discard operation-local data |

Errors should appear next to the composer with direct actions. They should not
use a blocking modal or replace the global application error surface.

## State And Ownership Model

The WebChat hook should expose an explicit state machine rather than infer
behavior from several booleans:

```text
disabled
idle
  -> requesting_permission
  -> starting_capture
  -> recording
  -> encoding
  -> transcribing
  -> applied

transcribing -> retryable_error -> transcribing
active state -> cancelled -> idle
active state -> error -> idle or new recording
transcribing -> pending_insert when the draft anchor is stale
```

Only one operation may own microphone, timer, WAV, abort controller, and pending
transcript state. Starting a new generation cancels and cleans the old one
before allocating new resources. Every asynchronous continuation checks both
generation and `session_id` before changing UI or draft state.

Stop and cancel are distinct:

- **stop** flushes captured samples and advances toward transcription;
- **cancel** stops tracks, discards samples/WAV/pending transcript, aborts the
  HTTP request, and returns to idle without changing the draft.

Cancel remains available during permission, capture startup, recording,
encoding, transcription, retryable error, and pending insertion. If an
irreversible draft insertion has already completed, cancel does not attempt to
undo normal owner-editable text.

## Browser Capture Reliability

`PCMInputCapture` should own and report the complete capture lifecycle:

- selected and effective device identity;
- first PCM callback deadline;
- timestamp of the most recent PCM callback;
- `MediaStreamTrack` ended events;
- audio context state changes that make capture unusable;
- idempotent stop and cancel cleanup; and
- the final flushed sample set.

A liveness watchdog checks that the worklet produces callbacks after startup
and continues producing them while the track is live. It detects capture
plumbing failure, not whether the owner is speaking. Phase 1 should not reject
quiet speech using an RMS/VAD threshold. No-speech remains an ASR outcome until
a separately evaluated optional VAD mode exists.

Every terminal path must stop every media track, disconnect audio nodes, clear
timers and listeners, remove worklet handlers, and close the `AudioContext`.
Cleanup must be safe when called more than once or while startup is still in
flight.

## Gateway And Speech Contract

The existing public operations remain sufficient:

```text
GET  /api/speech/status
POST /api/speech/transcriptions
```

The multipart request and successful response do not need a new version for the
base loop. `request_id` continues to correlate one captured-audio operation;
retries may reuse it because transcription has no SparkClaw-side effect.

Gateway hardening should include:

1. Verify the referenced session belongs to the authenticated owner, not only
   that it exists.
2. Apply one end-to-end deadline before admission so pending wait plus inference
   cannot exceed the configured speech budget.
3. Preserve immediate bounded `speech_busy` admission when the pending capacity
   is full.
4. Treat the transcription request itself as the readiness authority. Do not
   require a second remote `/health` request immediately before every valid
   transcription; cache/project health for status and readiness surfaces.
5. Keep response body, upload, duration, content type, language, redirect,
   allowlist, concurrency, and pending bounds unchanged.
6. Keep audit metadata-only and preserve the invariant that transcription
   creates no message, run, tool call, approval, or artifact.

The Gateway remains the only browser-facing ASR boundary. WebChat does not call
the model endpoint directly.

## Privacy And Persistence

- Gateway and the ASR adapter do not retain audio after the request.
- WebChat may retain the encoded WAV only in memory for the active retry window.
- Audio is never stored in `localStorage`, IndexedDB, Service Worker caches,
  Store, audit, trace, artifact, or message attachment.
- A raw transcript is inserted only into the client draft. It is persisted only
  if and when the owner sends the ordinary message.
- Speech audit contains request ID, duration, bytes, language, model, queue and
  inference timing, outcome, and bounded error code, but no audio or transcript.
- Device preference stores only the browser device ID/label needed by that
  client. It is not synchronized as owner memory.

## Observability

Reliability should be measured by stage rather than one aggregate success flag:

- permission and capture-start outcome;
- time to first PCM callback;
- recording duration and stop/cancel cause;
- encode duration and WAV bytes;
- Gateway admission wait, ASR inference, and total stop-to-draft latency;
- retry count and final result;
- stale-anchor pending insertion;
- device fallback or runtime device loss; and
- resource-cleanup completion in tests.

No external telemetry service is required. Server audit remains metadata-only;
client-only capture diagnostics may be exposed in development logs without
audio samples, device IDs, or transcript content.

## Verification Matrix

### Browser And State Tests

- First-use permission grant, denial, revocation, and cancellation while the
  permission prompt is pending.
- Default microphone, saved microphone, saved device removed, fallback success,
  fallback failure, and device disconnection during recording.
- First PCM timeout, callback liveness failure, worklet load failure, audio
  context failure, and idempotent cleanup after each failure.
- Very quick stop, normal stop, maximum-duration stop, repeated stop/cancel, and
  final worklet flush preservation.
- Cancel during startup, recording, encoding, transcription, retryable error,
  pending insertion, session switch, and unmount.
- Busy, timeout, unavailable, network failure, invalid JSON, empty transcript,
  and successful same-WAV retry.
- Unchanged draft insertion, selected-text replacement, changed-draft pending
  candidate, dismiss, and explicit insert-at-cursor.
- No late operation may change a newer session, state, or draft.

The state transition logic should be extracted into a pure reducer or
equivalent deterministic owner so these cases do not depend only on component
timing tests.

### Gateway And Adapter Tests

- Owner/session authorization and no cross-owner session use.
- Canonical WAV, duration, upload, language, request ID, and unexpected-file
  rejection.
- End-to-end deadline includes admission wait.
- Full queue returns retryable busy without starting inference.
- Cancellation reaches the pending wait or outbound request.
- Redirects remain rejected and upstream bodies remain bounded.
- Audit and errors contain no transcript or audio.
- Success and every failure create no message, Agent run, Tool Call, approval,
  or artifact.

### Live Acceptance

- Desktop Chromium and a mobile viewport with a real or deterministic fake
  microphone.
- At least two physical input devices, including unplugging the selected device.
- Chinese, English, and mixed technical dictation against the deployed ASR.
- Repeated start, stop, cancel, retry, and session-switch cycles without a
  persistent browser microphone indicator or stuck WebChat state.
- Stop-to-draft latency measured separately from recording duration; initial
  SLOs are set only after this baseline is recorded on the deployed ASR.

Phase 1 acceptance requires:

- no automatic sends;
- no changed-draft overwrite in the full test matrix;
- byte-identical same-WAV reuse across transcription retry;
- no audio persistence beyond the in-memory retry window;
- all media tracks and audio contexts closed on every terminal path; and
- no stuck state or late-generation mutation in repeated lifecycle tests.

## Phase 2 Detailed Design

The normative runtime, transport, partial/final reconciliation, silence-stop,
fallback, and acceptance contract is in [WebChat voice Phase 2: native
realtime ASR and silence stop](webchat-voice-phase2-design.md). It requires
model-produced partial text while the microphone remains active and explicitly
rejects completed-file response streaming as realtime.

## Delivery Phases

### Phase 0: Baseline And State Contract

- Record current desktop/mobile and deployed-ASR behavior.
- Extract and test the explicit voice-operation state transition contract.
- Add deterministic browser-media fakes for capture, device, failure, and
  cleanup tests.
- Measure stop-to-draft latency and current failure categories.

### Phase 1: Stable WebChat Voice Input

- Add microphone selection, preview, and system-default fallback.
- Add first-sample and runtime capture failure reporting.
- Add the in-memory WAV retry window and retry UI.
- Replace silent append-on-stale-draft with a pending insert candidate.
- Enforce owner/session scope and one queue-plus-inference deadline in Gateway.
- Remove the redundant per-transcription health prerequisite while preserving
  status readiness.
- Complete focused state, resource, API, desktop, and mobile verification.

### Phase 2: Native Realtime Voice Input

- Qualify and replace the current ASR serving process with one pinned runtime
  that exposes Qwen's native stateful streaming API and preserves batch.
- Stream continuous canonical PCM through authenticated, bounded WebSockets and
  show revisioned partial snapshots while recording.
- Reconcile one final snapshot against the existing draft anchor and use the
  complete in-memory WAV only as a failure fallback. A mid-stream failure stops
  capture immediately and automatically batch-transcribes audio through that
  boundary; it never downgrades an active operation into continued recording.
- Add browser-local, default-off silence auto-stop with deterministic one-shot
  semantics and no audio gating.
- Keep the 60-second bound; longer dictation segmentation remains deferred.

### Phase 3: Rich Voice Features

- Add optional LLM polishing, fixed styles, original/polished comparison, and
  raw fallback as a separate post-transcription layer.
- Evaluate owner-confirmed terminology correction and provider-supported ASR
  hotwords without retaining full dictation history.
- Keep raw transcription available whenever an enhancement is disabled or
  fails.

After each implementation phase stabilizes, durable behavior belongs in
[Architecture](architecture.md), [External integrations](integrations.md), and
[WebChat](webchat.md). This design record must remain until every later phase is
complete or its remaining decisions and acceptance criteria have been
explicitly migrated; completing one phase is not sufficient reason to delete it.

## Open Questions

| Question | Initial recommendation | Consequence |
|---|---|---|
| Device selector location | Compact menu next to the composer microphone | The owner can verify/change input without leaving the task |
| Device preference scope | Browser-local only | Avoids syncing origin-specific device IDs through Gateway |
| Device loss fallback | Fall back on the next recording, not mid-recording | Avoids merging audio streams with ambiguous timing |
| Retry audio expiry | Five minutes, memory only | Enough for transient recovery without becoming history |
| Retry policy | Explicit owner retry | Predictable ASR load and visible failure |
| Changed draft result | Pending insert candidate | Spoken content is recoverable without overwriting or silent append |
| Silence auto-stop | Browser-local Off / Standard / Patient; initially Off | False-stop risk stays owner-controlled and cannot block manual recording |
| Streaming ASR | Native Qwen state over bounded WebSockets | Partial text is produced while the microphone remains active; batch is failure fallback only |
| Mid-stream realtime failure | Stop capture and automatically batch the accumulated complete WAV | Preserves speech through the failure boundary without silently continuing the recording |
| LLM polishing | Phase 3 optional enhancement | Voice input remains useful and reliable without a chat model |
| Activation scope | Visible WebChat microphone only | No global listener, wake word, or background capture |
