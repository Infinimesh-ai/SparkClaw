# Local Speech Input Design

> Language: English | [简体中文](../zh-cn/docs/local-speech-input-design.md)

> Status: Authoritative implementation contract for `codex/voice-complete`, verified against the live ASR service on 2026-07-14.

## 1. Decision Summary

SparkClaw WebChat adds one microphone control to the existing composer. The browser records a bounded mono PCM stream, encodes it as 16 kHz PCM16 WAV, and sends it to Gateway. Gateway validates the request and calls the project-owned ASR service at `https://sparkclaw.infinimesh.cloud/asr`. The returned transcript is inserted into the current draft and is never sent automatically.

The production path is:

```text
WebChat microphone
  -> in-memory browser PCM capture
  -> 16 kHz mono PCM16 WAV
  -> authenticated Gateway speech API
  -> bounded OpenAI-compatible ASR adapter
  -> sparkclaw.infinimesh.cloud/asr
  -> Qwen/Qwen3-ASR-1.7B served as sparkclaw-asr
  -> transcript inserted into the existing draft
```

Speech transcription ends before message creation. It must not create a `Message`, `AgentRun`, `ToolCall`, approval, trace observation, or artifact. The normal agent lifecycle begins only when the owner reviews the draft and explicitly uses the existing send action.

## 2. Scope

Included:

- A single stateful microphone button in the WebChat composer.
- Browser permission, recording, cancellation, level feedback, elapsed time, resampling, and WAV encoding.
- Gateway status and batch-transcription APIs.
- A real OpenAI-compatible HTTP adapter for the configured SparkClaw ASR endpoint.
- Strict audio, duration, body-size, session, language, and request-ID validation.
- Bounded HTTP execution, concurrency, queueing, cancellation, and shutdown.
- Metadata-only speech audit events.
- Focused backend/frontend tests, browser desktop and narrow-layout checks, and live-model smoke evidence.

Excluded:

- Automatic message sending.
- Streaming partial transcripts, VAD, diarization, subtitles, or uploaded-file transcription.
- OS-global shortcuts or text injection into other applications.
- Persisting raw audio, transcript history, or reusable voice artifacts.
- Calling ASR directly from WebChat or exposing the upstream ASR URL to the browser.
- Cloud-provider fallback or choosing a destination URL from request data.
- Telegram, notification binding, credential-store, Infinimesh Info, and `web.search` changes.

## 3. ASR Service And Model Selection

The implementation uses the operator-provided endpoint rather than packaging another model runtime in this branch:

| Property | Verified value |
|---|---|
| Base URL | `https://sparkclaw.infinimesh.cloud/asr` |
| Health | `GET /health` -> HTTP 200 |
| Runtime | `GET /version` -> vLLM `0.24.0` |
| Model discovery | `GET /v1/models` |
| Served model | `sparkclaw-asr` |
| Root model | `Qwen/Qwen3-ASR-1.7B` |
| Model context | `4096` |
| Transcription | `POST /v1/audio/transcriptions` |
| Request format | OpenAI-compatible multipart upload |

Selection basis:

- It is already deployed and reachable from this development machine, so the feature can be verified against real inference instead of a fake transcriber.
- It accepts the canonical WAV format produced by WebChat and exposes the standard OpenAI-compatible transcription path.
- It is multilingual and successfully transcribed both English and Mandarin smoke samples.
- The endpoint reports stable runtime and model identity through `/version` and `/v1/models`, allowing `doctor.sh` to detect drift.
- Gateway can isolate its protocol, host, timeout, and privacy boundary behind one adapter without changing the public WebChat API.

This branch does not add an ASR Docker image or model-weight download. The managed endpoint is the declared production dependency. Reproducibility for this integration means the URL, served model, protocol, health checks, request limits, and expected runtime identity are version-controlled and checked; model deployment remains owned by the service operator.

## 4. Live Model Evidence

The following smoke was run from an Apple M5 Mac against the configured endpoint on 2026-07-14. Audio was generated into `/tmp`, converted to 16 kHz mono PCM16 WAV, uploaded once, and deleted after validation. No audio fixture is committed.

| Sample | Audio duration | HTTP total | Real-time factor | Result |
|---|---:|---:|---:|---|
| English | 4.936 s | 0.903 s | 0.183 | Correct sentence structure and intent; `SparkClaw` rendered as `Sparklaw`. |
| Mandarin | 6.586 s | 0.921 s | 0.140 | Correct sentence and intent with one character substitution (`只` -> `之`). |

After the two requests, the service reported:

- `request_success_total{finished_reason="stop"}=2`
- zero running and waiting requests
- zero KV-cache utilization
- `server_load=0`
- process resident memory about 1.72 GB

The resident-memory metric is process RSS, not total accelerator allocation. It is evidence that the live service was healthy and bounded during the smoke, not a complete DGX Spark GPU residency measurement.

## 5. User Experience Contract

The microphone button sits after the attachment controls and before the text area. It keeps a stable 42 px footprint and uses the existing icon library.

| State | Click behavior | Visible result |
|---|---|---|
| `disabled` | No action | Localized unavailable reason |
| `idle` | Request permission and start capture | Microphone icon |
| `requesting_permission` | Ignore repeated clicks | Busy icon |
| `recording` | Stop and transcribe | Stop icon, elapsed time, level meter |
| `encoding` | Cancel preparation | Busy icon and preparation label |
| `transcribing` | Abort HTTP request | Busy icon and local-transcription label |
| `error` | Start a new attempt | Recoverable localized error |

`Escape` cancels every active state. Cancellation stops all media tracks, closes the `AudioContext`, clears buffered samples, and aborts the Gateway request.

Draft insertion rules:

- Save the textarea selection when recording starts.
- Insert at that selection if the original draft is unchanged; otherwise append to the latest draft.
- Preserve attachments and text typed before or after recording.
- Add an ASCII space only when both adjacent characters require one.
- Focus the textarea and place the caret after the inserted transcript.
- Ignore empty or whitespace-only transcripts and show “No speech detected”.
- Never call the existing send function from the voice path.

Session changes, session deletion, authentication loss, component unmount, or an active chat send cancel voice input. A transcript is applied only if the active session still matches the session captured at recording start.

## 6. Browser Audio Contract

Canonical upload format:

| Field | Limit |
|---|---|
| Container | RIFF/WAVE |
| Codec | Signed PCM16 little-endian |
| Sample rate | 16,000 Hz |
| Channels | 1 |
| Minimum duration | 300 ms |
| Maximum duration | 60 s |
| Maximum Gateway body | 3 MiB |
| MIME type | `audio/wav` |

WebChat requests mono capture with echo cancellation, noise suppression, and automatic gain control as preferences. It uses `AudioWorklet` frames, computes a smoothed RMS level, buffers only the active recording, downsamples once on stop, and releases source buffers immediately after encoding.

Microphone access requires `navigator.mediaDevices.getUserMedia`, `AudioContext`, and `AudioWorklet` in a secure context. Supported origins are `localhost`, `127.0.0.1`, or HTTPS. Plain HTTP on a LAN hostname is unsupported.

## 7. Gateway API

### Status

`GET /api/speech/status` returns:

```json
{
  "enabled": true,
  "ready": true,
  "state": "ready",
  "backend": "openai-http",
  "model": "sparkclaw-asr",
  "supports_streaming": false,
  "accepted_content_types": ["audio/wav"],
  "max_audio_seconds": 60,
  "max_upload_bytes": 3145728
}
```

`GET /readyz` includes the same compact speech state. Speech failure does not make the whole Gateway unready, but the UI remains disabled until speech reports ready.

### Transcription

`POST /api/speech/transcriptions` is an authenticated `multipart/form-data` request with exactly one `file` plus required `session_id` and `request_id`; `language` is optional and defaults to `auto`.

The response includes request/session correlation, text, duration, inference latency, model, and `audio_retained: false`. It does not expose the upstream URL.

Stable errors include invalid request, too short, too large, unsupported format, busy, cancelled, disabled, unavailable, timeout, and inference failure. Gateway maps upstream 4xx/5xx responses into these codes without returning upstream response bodies verbatim.

## 8. Upstream Adapter Contract

The adapter is named `openai-http` and is configured, never request-directed.

- Readiness: `GET {base_url}/health`, 2-second child timeout.
- Model identity: the configured served name is `sparkclaw-asr`.
- Inference: `POST {base_url}/v1/audio/transcriptions` with `file`, `model`, optional `language`, and `response_format=json`.
- Redirects are rejected.
- Response bodies are capped at 1 MiB.
- Every request uses the caller context plus the configured HTTP timeout.
- The adapter owns one `http.Client`, closes idle connections through `Close()`, and is closed during Gateway shutdown.
- The hostname must exactly match `allowed_hosts`; the default list contains only `sparkclaw.infinimesh.cloud`.
- No fallback host, proxy-selected destination, browser-provided URL, or transcript logging is allowed.

The public endpoint currently requires no application credential. No credential storage or token configuration is added by this work. If the service later requires authentication, it must be added as a separate reviewed change using environment-only secret injection.

## 9. Privacy Boundary

The selected endpoint is HTTPS and project-specific, but it is not loopback. Raw audio crosses the workstation boundary from Gateway to `sparkclaw.infinimesh.cloud`. This is the explicit privacy boundary authorized for this implementation.

- WebChat never calls the ASR service directly.
- Audio exists only in browser memory, Gateway request memory, and the upstream request body during inference.
- Gateway does not write audio to workspaces, artifacts, traces, audit payloads, or state backends.
- Gateway does not write transcript text to logs, traces, or audit events.
- Audit metadata may include request ID, session ID, byte size, duration, model, latency, outcome, and stable error code.
- The transcript becomes durable only if the owner later sends the edited draft through the normal message API.
- The implementation does not assert how the managed ASR service stores data internally; service-side retention is an operator responsibility and remains a residual privacy risk.

## 10. Lifecycle And Resource Limits

Defaults for the single-owner runtime:

| Resource | Default |
|---|---:|
| Active transcriptions | 1 |
| Pending transcriptions | 1 |
| Gateway/upstream timeout | 120 s |
| Readiness timeout | 2 s |
| Audio duration | 60 s |
| Upload body | 3 MiB |
| Upstream JSON response | 1 MiB |

The adapter has one admission channel for active plus pending work and one worker channel for active inference. A full admission channel returns `429 speech_busy` immediately. Queue wait and HTTP inference both honor request cancellation.

Gateway startup constructs exactly one transcriber. Startup fails for invalid enabled configuration. Shutdown cancels the server context, drains HTTP work through the existing server shutdown, and calls `Transcriber.Close()`.

## 11. Configuration

The production default is:

```json
{
  "speech": {
    "enabled": true,
    "backend": "openai-http",
    "base_url": "https://sparkclaw.infinimesh.cloud/asr",
    "allowed_hosts": ["sparkclaw.infinimesh.cloud"],
    "model": "sparkclaw-asr",
    "default_language": "auto",
    "timeout_seconds": 120,
    "max_audio_seconds": 60,
    "max_upload_bytes": 3145728,
    "max_concurrency": 1,
    "max_pending": 1,
    "retain_audio": false
  }
}
```

Environment overrides use the `SPARKCLAW_SPEECH_*` prefix. `retain_audio=true` is rejected. Enabled configuration requires HTTPS, an exact allowlisted hostname, a non-empty model, and positive normalized limits. `enabled=false` normalizes the backend to `disabled`.

## 12. File Ownership And Shared-File Edits

Speech-owned files:

- `services/gateway/internal/speech/*`: types, disabled adapter, OpenAI HTTP adapter, WAV validation, and focused tests.
- `services/gateway/internal/gateway/speech.go`: speech-only HTTP decoding, validation, auditing, and error mapping.
- `apps/webchat/src/audio/*`, `public/pcm-worklet.js`: browser capture and encoding.
- `apps/webchat/src/hooks/useVoiceInput.ts`: voice state machine and cancellation.
- `apps/webchat/src/components/VoiceInputButton.tsx`: voice-only UI.
- `docs/local-speech-input-design.md` and its Chinese mirror: authoritative design.

Necessary shared-file changes, intentionally limited to speech concerns:

- `services/gateway/internal/config/config.go`: add `SpeechConfig`, defaults, validation, and environment overrides only.
- `services/gateway/cmd/sparkclaw/main.go`: construct one transcriber, inject it, and defer `Close()` only.
- `services/gateway/internal/gateway/server.go`: store the injected transcriber, register two speech routes, and expose compact readiness only.
- `apps/webchat/src/App.tsx`: add the textarea ref, voice hook/button/status, caret-aware draft insertion, and send/voice mutual exclusion only.
- `apps/webchat/src/api/client.ts` and `api/types.ts`: add speech status/transcription contracts only.
- `apps/webchat/src/i18n.ts` and `styles/app.css`: add localized speech copy and fixed composer states only.
- `configs/sparkclaw.default.json`, `docker/env/sparkclaw.example.env`, and `docker/compose.yaml`: declare/pass speech settings only; no ASR container is added.
- `scripts/doctor.sh`: verify configured health, runtime version, and served model without uploading audio.

No store interface, Telegram, binding, notification, reminder, Weixin, ToolHub, or Agent Runtime file belongs to this change.

## 13. Acceptance And Verification

Completion requires all of the following:

- A real browser recording reaches Gateway and the live Qwen3-ASR endpoint.
- A successful transcript is inserted at the saved draft position.
- Transcription alone creates no message and never triggers send.
- Audio and transcript content are absent from state, artifacts, traces, audit payloads, and application logs.
- Invalid WAV, oversize, over-duration, busy, cancellation, timeout, and upstream failures have focused tests.
- The HTTP adapter rejects redirects and non-allowlisted hosts and exposes `Close()`.
- `doctor.sh` validates `/health`, vLLM version, and `sparkclaw-asr` model identity.
- Desktop and narrow viewport checks show no composer overlap and usable button states.
- Live English and Mandarin smoke requests succeed; latency and transcript observations are reported without overstating quality.
- Gateway build/vet/test, WebChat build, docs mirror/link checks, and worktree cleanliness pass.

Future streaming, VAD, service authentication, and service-side retention guarantees require separate design review. They must not change the draft-only, review-before-send contract.
