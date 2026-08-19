# WebChat Voice Phase 2: Native Realtime ASR And Silence Stop

> Language: English | [简体中文](../zh-cn/docs/webchat-voice-phase2-design.md)

> Status: implemented in the active worktree and candidate-qualified on
> 2026-08-19. Production services have not been cut over; physical desktop and
> mobile microphone acceptance remains pending. This document expands Phase 2
> of the [WebChat voice input loop design](webchat-voice-input-design.md).
> Phase 1 remains the batch fallback and LLM polishing remains Phase 3.

This design requires genuine transcription while recording. A completed WAV
upload whose decoder output is streamed afterward is explicitly out of scope.

## Confirmed Decisions

- Initial qualification uses the current `Qwen/Qwen3-ASR-0.6B` weights. The
  serving runtime may change to expose the official native streaming API, but
  SparkClaw does not switch models before measured evidence from this model.
- Silence auto-stop is browser-local and **Off by default**. Manual stop remains
  the default interaction.
- Once realtime capture has started, any transport, protocol, backpressure, or
  backend failure immediately stops capture. WebChat flushes PCM already
  accepted at that boundary, automatically batch-transcribes one complete WAV,
  and then ends the recording operation. It never continues recording after a
  mid-stream failure.
- LLM polishing remains outside Phase 2.

## Implementation And Qualification Status

The active worktree implements the full Phase 2 path across the ASR runtime,
Gateway, Nginx/Vite proxy, and WebChat. The candidate image uses one
`Qwen3ASRModel.LLM` instance for batch and native realtime calls, keeps every
model operation on one owner thread, and performs a 300 ms silent inference
before opening its HTTP listener. Consequently, `ready=true` no longer leaves
the first owner request paying the backend's inference cold start.

Qualification on the local DGX Spark used image
`sha256:3f20b317af0332062923bd6fb8176cee95ac0ac1e52221a53927c761de7c08f3`
and produced the following evidence:

- Cold start reached ready in 121 seconds, including a 48.1-second first
  inference warm-up. The first real batch request after ready completed in
  183 ms and exactly matched the production transcript: `Front, left, front,
  center, front, right.`
- A 4.439-second record-paced stream emitted its first model partial after
  about 1.1 seconds, revised it on each completed model chunk, and returned the
  same authoritative final with 470 ms cumulative model inference.
- A 60-second record-paced repeated-speech fixture completed in 60.7 seconds,
  produced 43 revisions, accumulated 17.8 seconds of model inference, and
  peaked at 3.3 seconds of unacknowledged audio, below the 5-second hard bound.
- A held realtime session made a concurrent batch request fail with `429`.
  Closing that session released capacity before the next batch inference,
  which completed in 161 ms.
- Desktop and 390 px mobile Chromium fake-microphone passes crossed the real
  AudioWorklet, resampler, Vite proxy, authenticated Gateway ticket, and ASR
  session. A model partial was visible before stop, the final entered the draft
  without sending it, and the healthy path made no batch transcription request.
- The ASR runtime protocol and owner-thread warm-up suite passes 6 tests. The
  focused Gateway race suite and WebChat unit/build gates are also green.

This is candidate qualification, not production cutover evidence. Physical
microphone interaction, device/noise corpus validation for silence stop, and a
live failure-to-complete-WAV recovery drill remain release checks.

## Non-Negotiable Realtime Definition

Phase 2 means **transcription while recording**, not a streamed response after
an upload has completed. A path is realtime only when all of these are true:

1. WebChat continuously sends PCM from the active microphone before stop.
2. One stateful ASR session consumes that PCM and runs inference while capture
   is still active.
3. WebChat receives at least one model-produced partial snapshot before sending
   `finish` for a normal utterance long enough to fill one inference chunk.
4. `finish` flushes the same ASR state and returns one authoritative final
   snapshot.

The vLLM OpenAI-compatible `POST /v1/audio/transcriptions` request with
`stream=true` does not meet this definition. It still requires the complete
audio file before inference and only streams decoder output afterward. Phase 2
must never split or repeatedly upload WAV files to imitate realtime behavior.
On a healthy realtime operation, no batch transcription request is made during
recording.

## Verified Local-Engine Boundary

`Qwen/Qwen3-ASR-0.6B` supports the official Qwen streaming API through the vLLM
backend. Its native API owns an `ASRStreamingState`, accepts arbitrary 16 kHz
PCM increments, decodes whenever a configured model chunk is complete, revises
the whole transcript snapshot using prefix rollback, and explicitly flushes the
tail at finish. The current official implementation re-feeds accumulated audio
and revises unstable suffix tokens; it is still genuine record-time inference,
but it is not a timestamped delta protocol or a cached recurrent encoder.

The deployed SparkClaw ASR service cannot expose that API today:

- its OpenAPI surface has no realtime/session endpoint;
- its `stream=true` field belongs to the completed-file endpoint; and
- the running image does not contain the `qwen_asr` inference package that owns
  `init_streaming_state`, `streaming_transcribe`, and
  `finish_streaming_transcribe`.

The official `qwen-asr` package currently pins a different vLLM release from
the deployed image. Phase 2 therefore starts with a compatibility qualification,
not an in-place `pip install`. The candidate image must prove model startup,
streaming state correctness, batch quality parity, memory bounds, and cold-boot
recovery on the deployed DGX Spark before the current ASR image is replaced.

## Target Topology

```text
WebChat AudioWorklet
  -> continuous native mono PCM capture
  -> stateful resample to 16 kHz PCM16
  -> authenticated Gateway WebSocket
      -> sequence, size, duration, owner/session, and backpressure checks
      -> one upstream realtime ASR session
          -> Qwen3ASRModel.LLM singleton
          -> native ASRStreamingState
          -> revisioned partial snapshots while recording
      <- revisioned partial/final snapshots
  -> partial preview separate from the editable draft
  -> one final reconciliation against the Phase 1 draft anchor

realtime failure after capture starts
  -> atomically freeze partial state and close the capture boundary
  -> stop microphone/worklet and flush the resampler immediately
  -> encode all PCM captured through that boundary as canonical WAV
  -> automatically invoke one complete-WAV batch transcription
  -> replace the partial and reconcile through the Phase 1 draft anchor
```

WebChat never calls the model service directly. Gateway owns public
authentication, session authorization, admission, cancellation, bounds,
protocol normalization, and audit. The ASR runtime owns model state and no
SparkClaw user/session authority.

## One ASR Process, Not Two Model Copies

The `sparkclaw-asr` container becomes a small SparkClaw-owned runtime around one
`Qwen3ASRModel.LLM` singleton. That process exposes:

- `/health`, `/version`, and `/v1/models` for existing operations;
- `/v1/audio/transcriptions` for the Phase 1 batch contract and Telegram voice
  notes; and
- one internal realtime WebSocket endpoint for stateful PCM/partial/final
  exchange.

Batch and realtime admission share the same model worker and configured
capacity. The initial release retains one active inference operation because
the official streaming API is single-stream and non-batched. It must not run a
stock vLLM OpenAI server beside a second embedded Qwen engine; that would load
the model twice, split readiness, and make memory behavior unpredictable.

The ASR runtime creates state only after Gateway admission, serializes calls
into the model singleton, bounds pending PCM independently from network
buffers, expires abandoned sessions, and destroys audio/state at every terminal
path. It never writes PCM, WAV, partial text, or final text to disk or logs.
Realtime-to-batch recovery idempotently destroys the realtime state and releases
its admission slot before the fallback request enters batch admission; the two
inference operations never overlap or wait on a slot still owned by the same
voice operation.

## Browser-to-Gateway Session Bootstrap

The browser WebSocket API cannot attach SparkClaw's normal bearer header.
WebChat therefore first makes an authenticated request:

```text
POST /api/speech/realtime-sessions
```

The body carries `session_id`, `request_id`, language, and the fixed audio
format. Gateway validates the authenticated owner/session relationship and
reserves realtime capacity. The response contains an opaque, random,
single-use WebSocket ticket with a 30-second expiry and the exact same-origin
WebSocket URL. Gateway keeps only the ticket hash and bound principal in memory.
The upgrade consumes it atomically; replay, expiry, wrong route, or a second
connection fails closed. Access logs must redact the ticket query value.

If authentication is disabled, the ticket is still required and is bound to
the default local owner. This keeps one protocol in both deployment postures.
The Nginx configuration gets an exact WebSocket location with `Upgrade` and
`Connection` headers; the generic `/api/` proxy must not accidentally expose an
upgrade route.

## Wire Contract

The protocol is versioned as `sparkclaw.speech.realtime.v1`.

Client binary audio frames contain a fixed header followed by PCM:

```text
u32 sequence, big endian
u32 sample_count, big endian
sample_count * i16 little-endian mono PCM samples
```

WebChat emits 100 ms frames: 1,600 samples and 3,200 PCM bytes at 16 kHz.
Sequence starts at zero and must be contiguous. Gateway rejects duplicates,
gaps, malformed lengths, non-canonical format, bytes beyond the configured
upload bound, or audio beyond `max_audio_seconds`; it never fills a gap with
silence. The ASR runtime groups transport frames into a server-owned initial
1.0-second model chunk. `chunk_size_sec`, prefix rollback, and generation-token
limits are runtime policy, not browser-provided knobs.

Client control frames are JSON:

| Event | Required fields | Meaning |
|---|---|---|
| `finish` | `last_sequence`, `captured_ms`, `reason` | Flush all accepted PCM and request final output |
| `cancel` | `last_sequence` | Abort and discard the operation without a draft change |

`reason` is one of `manual_stop`, `silence_stop`, or `max_duration`. Server
events are complete JSON snapshots:

| Event | Important fields | Semantics |
|---|---|---|
| `ready` | protocol, format, limits | Model state exists and audio may flow |
| `ack` | accepted sequence, received audio ms | Highest contiguous audio durably admitted to the in-memory session |
| `partial` | revision, text, language, audio end ms | Replace the prior preview; never append as a delta |
| `final` | revision, text, language, duration/inference ms, stop reason | Authoritative result from the same streaming state |
| `fallback` | code, retryable | Realtime cannot continue; stop active capture and automatically batch the locally retained PCM |
| `error` | code, retryable | Operation cannot continue safely |

Revisions start at one and increase only when normalized text or language
changes. Partial and final payloads are bounded. A final revision must be newer
than every partial, even when its text is byte-identical. No event contains a
draft, bearer credential, device ID, raw audio, or internal exception.

## Browser Audio Ownership And Backpressure

`PCMInputCapture` continues retaining the complete operation in memory for the
Phase 1 fallback. A stateful resampler preserves fractional position across
AudioWorklet callbacks, emits canonical 16 kHz PCM16 once, and feeds both the
WebSocket frame buffer and final WAV wrapper. Resampling each callback in
isolation is forbidden because it creates gaps or duplicated boundary samples.

The browser keeps a small bounded unsent queue and monitors
`WebSocket.bufferedAmount`. Gateway and the ASR runtime each keep an independent
bounded queue measured in audio milliseconds. The initial target is at most
five seconds at every hop. Audio is never silently dropped to catch up. If any
queue reaches its bound, the realtime session emits `speech_stream_overrun`,
closes, and WebChat immediately stops capture and automatically batch-transcribes
the PCM retained through the failure boundary.

WebChat acquires microphone permission and the selected device track before it
reserves realtime capacity, so a pending permission prompt cannot pin the one
ASR slot. It does not start the AudioWorklet or accept PCM until the WebSocket
has emitted `ready`. If ticket admission, connection, or readiness fails before
capture starts while the batch endpoint remains ready, WebChat starts a clearly
labeled Phase 1 batch-only recording and does not claim live transcription. If
the speech service is unavailable for both modes, WebChat releases the track
and reports the error without starting capture.

After `ready`, every realtime failure dispatches one idempotent recovery action.
That action closes the operation's input boundary so later worklet callbacks are
ignored, flushes samples already accepted by the capture/resampler, stops the
track and audio context, freezes the partial as non-authoritative, closes the
realtime session, waits for its bounded idempotent capacity release, wraps the
complete local PCM once, and automatically invokes the Phase 1 batch endpoint.
No recovery path returns to a recording state.

## Partial And Final Draft Reconciliation

Qwen streaming output is a revision of the whole recognized prefix, not an
append-only token stream. WebChat therefore renders one visually secondary
partial preview near the composer and replaces it atomically for each revision.
Partial text never enters the textarea, never changes the selection, never
becomes a message, and is never persisted.

On `final`, WebChat constructs the existing `SpeechTranscriptionResult` and
applies it exactly once through the Phase 1 anchor rules:

- unchanged session and draft snapshot: replace the captured selection;
- changed draft: keep one explicit pending-insert candidate;
- cancelled, replaced, or changed session: ignore the late result.

The final event is authoritative for a healthy stream. SparkClaw does not run a
second batch transcription merely to compare wording. If the stream fails or
final does not arrive within the bounded finalization deadline, capture is
already stopped or is stopped immediately, and the complete in-memory WAV is
automatically sent once through the batch path. A successful batch result
replaces the frozen partial preview and uses the same anchor rules. If batch
also fails, the existing five-minute same-WAV explicit retry remains available;
an unstable partial is never inserted as a substitute final.

## Silence Auto-Stop

Silence auto-stop is a browser-local recording controller, independent of ASR
partial text. It observes the exact PCM sample clock used for capture and never
gates, trims, or withholds audio from the realtime model. The trailing silence
which triggers stop has therefore already reached the ASR session before
`finish`.

The microphone menu exposes an option set rather than another toolbar button:

| Mode | Trailing silence | Initial default |
|---|---:|---|
| Off | none | selected for the first Phase 2 release |
| Standard | 1.2 seconds | owner-selectable |
| Patient | 2.0 seconds | owner-selectable for deliberate speech |

The preference is browser-local. It does not become owner memory or Gateway
configuration.

The initial detector follows the useful OpenLess one-shot contract while
adapting its threshold to each device:

1. Track a rolling low-percentile noise floor before confirmed speech.
2. Enter `speech_active` only after at least 160 ms of sustained activity above
   a start threshold derived from the noise floor.
3. Use a lower end threshold for hysteresis; short pauses reset no state.
4. After confirmed speech, emit exactly one `auto_stop` only when continuous
   below-threshold audio reaches the selected trailing-silence duration.
5. If no speech is confirmed within ten seconds, emit `no_speech_cancel` and
   discard the empty operation.

All durations use accumulated audio samples, not throttled page timers. A loud
single transient cannot arm the detector. Speech resuming before the deadline
cancels the trailing-silence countdown. In persistent noise, the safe failure
mode is to remain recording until manual stop or maximum duration; the detector
must prefer missed auto-stop over truncating speech. Manual stop, cancel,
device loss, and maximum duration always take precedence over a later VAD
decision.

The threshold algorithm remains isolated behind a pure `SilenceDetector`
contract with recorded-fixture tests. A model-based VAD is considered only if
the device/noise corpus cannot meet the false-stop gate; it must consume the
existing PCM stream and may not open a second microphone or require a remote
service.

## State Ownership

The main voice reducer gains these phases:

```text
acquiring_microphone
  -> connecting_realtime
      -> starting_capture -> recording_realtime -> finalizing_realtime
      -> starting_batch_capture -> recording_batch_only

recording_realtime | finalizing_realtime
  -> recovering_batch -> encoding -> transcribing

recording_batch_only
  -> encoding -> transcribing

finalizing_realtime | transcribing
  -> idle | pending_insert | retryable_error
```

Silence detection is a separate deterministic sub-state:

```text
disabled | waiting_for_speech -> speech_active -> trailing_silence -> decided
```

It may emit only `auto_stop` or `no_speech_cancel` into the main reducer. This
avoids a Cartesian product of transport and VAD states. One operation generation
owns capture, resampler, VAD, WebSocket, sequence counters, queues, full PCM/WAV,
draft anchor, timers, and abort handles. Every callback checks generation and
session before mutation.

## Failure And Fallback Matrix

| Failure point | Required behavior |
|---|---|
| Microphone permission/device acquisition before capture | Start no recorder, release acquired resources, and report the Phase 1 device error |
| Ticket/admission or connect/ready failure before capture | If batch ASR is ready, start a visibly batch-only Phase 1 recording; otherwise release the track and report busy/unavailable |
| Sequence gap or malformed frame after capture starts | Fail realtime closed, immediately stop/flush capture, and automatically batch exact local PCM; never infer over fabricated silence |
| Network/backend loss while recording | Freeze the partial, immediately stop/flush capture, and automatically batch exact local PCM |
| Backpressure bound exceeded | Drop no audio; immediately stop/flush capture and automatically batch exact local PCM |
| First-sample timeout or device loss | Stop at the last locally accepted sample and automatically batch when usable PCM exists; never switch devices mid-recording |
| Manual cancel | Abort both sockets/model state, discard PCM/WAV/partial, leave draft unchanged |
| Silence auto-stop | Flush capture/resampler, send all frames, then `finish` with `silence_stop` |
| Final timeout or invalid final | Capture is already stopped; close realtime and automatically invoke one complete-WAV batch transcription |
| Batch fallback retryable failure | Keep the same WAV/request ID under the existing five-minute retry contract |
| Session switch/unmount | Cancel all resources and ignore every late partial/final |

The UI distinguishes live transcription, batch-only capture, and automatic
recovery transcription. A partial that can no longer be finalized is frozen and
marked as recovering, not left looking authoritative. Recovery success ends the
operation through the normal draft-anchor result; it never resumes recording.

## Bounds, Privacy, And Audit

- The existing 60-second and upload-size bounds remain; Phase 2 does not yet
  introduce long-dictation segmentation.
- Initial transport frames are 100 ms; model chunks start at 1.0 second and are
  changed only after measured accuracy/latency evidence.
- Connect/ready has a five-second bound; final flush has an initial twelve-second
  bound inside the Gateway operation deadline; idle/abandoned sessions expire.
- Realtime and batch share the configured concurrency and pending budget.
- Raw PCM/WAV and transcript text stay out of Store, artifacts, audit, traces,
  access logs, and server logs.
- Audit records request/session identity, transport outcome, stop reason,
  duration, frame/revision counts, first-partial/final latency, fallback code,
  model, and bounded error code only.

`GET /api/speech/status` keeps the backward-compatible
`supports_streaming` flag but sets it true only when the native realtime runtime
passes readiness. A structured realtime projection may additionally expose the
protocol version, fixed format, frame duration, and limits; it must not expose
the internal endpoint. WebChat never infers capability from a model name.

## Phase 2 Qualification And Acceptance

Implementation is split into evidence-bearing gates:

1. **Runtime qualification**: build the pinned ASR runtime image; prove one
   `Qwen/Qwen3-ASR-0.6B` instance serves native streaming and batch; compare
   batch output with the current service; measure partial quality/latency, GPU
   memory, cold start, and 60-second growth. Consider another model only after
   this evidence is recorded.
2. **Transport**: add ticket bootstrap, WebSocket proxying, sequence/queue
   bounds, partial/final normalization, cancellation, and batch fallback.
3. **WebChat reconciliation**: add stateful resampling, partial preview, final
   insertion, stale-anchor behavior, and loss/reconnect tests.
4. **Silence stop**: add the browser-local setting and pure detector only after
   recorded quiet, noisy, soft-speech, long-pause, keyboard, and mixed-language
   fixtures pass.

Release evidence must prove:

- a timestamped partial is visible before manual/automatic stop in desktop and
  mobile fake-microphone E2E;
- a healthy realtime recording makes zero
  `/v1/audio/transcriptions` calls and does not upload WAV slices;
- partial revisions may replace text, while final applies to the draft exactly
  once;
- every transport failure after capture starts closes the capture boundary,
  accepts no later microphone samples, and automatically submits exactly one
  complete WAV containing all PCM through that boundary;
- a pre-capture realtime failure either starts an explicitly labeled batch-only
  recording or starts no recording when batch ASR is unavailable;
- no frame is silently dropped, reordered, duplicated, or accepted beyond the
  configured bounds;
- silence mode never stops before confirmed speech, resumes correctly across
  short pauses, and stays manual in unsupported/noisy cases;
- silence auto-stop is Off on first use and remains inactive until the owner
  explicitly selects Standard or Patient;
- cancellation, session switch, unmount, device loss, and Gateway shutdown
  release browser, Gateway, and ASR runtime state; and
- no audio or transcript appears in persistence, artifacts, audit, or logs.

Initial latency targets on the deployed 0.6B model are first partial within the
first 1.0-second model chunk plus 1.0 second of inference, subsequent visible
revision gaps below 1.5 seconds at p95, and final within 2.0 seconds at p95.
These are release targets, not claims; the runtime qualification records the
baseline and either meets them or updates the product decision with evidence.
