from __future__ import annotations

import argparse
import asyncio
from concurrent.futures import ThreadPoolExecutor
from contextlib import asynccontextmanager
from dataclasses import dataclass
import importlib.metadata
import io
import logging
import re
import struct
import time
from typing import Any, AsyncIterator, Callable, Protocol

from fastapi import FastAPI, File, Form, HTTPException, UploadFile, WebSocket
import numpy as np
import soundfile as sf
import uvicorn


PROTOCOL = "sparkclaw.speech.realtime.v1"
SAMPLE_RATE = 16_000
FRAME_SAMPLES = 1_600
FRAME_MS = 100
MIN_AUDIO_MS = 300
MAX_START_BYTES = 4_096
REALTIME_IDLE_TIMEOUT_SECONDS = 10
REALTIME_FINAL_GRACE_SECONDS = 15
REQUEST_ID_PATTERN = re.compile(r"^[A-Za-z0-9._:-]{1,128}$")
STOP_REASONS = {"manual_stop", "silence_stop", "max_duration"}
LOGGER = logging.getLogger("sparkclaw.asr")

LANGUAGES = {
    "ar": "Arabic",
    "cs": "Czech",
    "da": "Danish",
    "de": "German",
    "el": "Greek",
    "en": "English",
    "es": "Spanish",
    "fa": "Persian",
    "fi": "Finnish",
    "fil": "Filipino",
    "fr": "French",
    "hi": "Hindi",
    "hu": "Hungarian",
    "id": "Indonesian",
    "it": "Italian",
    "ja": "Japanese",
    "ko": "Korean",
    "mk": "Macedonian",
    "ms": "Malay",
    "nl": "Dutch",
    "pl": "Polish",
    "pt": "Portuguese",
    "ro": "Romanian",
    "ru": "Russian",
    "sv": "Swedish",
    "th": "Thai",
    "tr": "Turkish",
    "vi": "Vietnamese",
    "yue": "Cantonese",
    "zh": "Chinese",
}


class ASRModel(Protocol):
    def transcribe(
        self,
        audio: tuple[np.ndarray, int],
        context: str = "",
        language: str | None = None,
        return_time_stamps: bool = False,
    ) -> list[Any]: ...

    def init_streaming_state(
        self,
        context: str = "",
        language: str | None = None,
        unfixed_chunk_num: int = 2,
        unfixed_token_num: int = 5,
        chunk_size_sec: float = 1.0,
    ) -> Any: ...

    def streaming_transcribe(self, pcm16k: np.ndarray, state: Any) -> Any: ...

    def finish_streaming_transcribe(self, state: Any) -> Any: ...


@dataclass(frozen=True)
class RuntimeSettings:
    served_model_name: str
    max_audio_seconds: int = 60
    max_upload_bytes: int = 3 << 20
    chunk_size_sec: float = 1.0
    unfixed_chunk_num: int = 2
    unfixed_token_num: int = 5


class OperationGate:
    def __init__(self) -> None:
        self._lock = asyncio.Lock()

    async def acquire(self) -> bool:
        if self._lock.locked():
            return False
        await self._lock.acquire()
        return True

    def release(self) -> None:
        if self._lock.locked():
            self._lock.release()

    @property
    def busy(self) -> bool:
        return self._lock.locked()


class ModelWorker:
    def __init__(self, factory: Callable[[], ASRModel]) -> None:
        self._executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix="sparkclaw-asr-model")
        self._model = self._executor.submit(factory).result()

    async def call(self, method: str, *args: Any, **kwargs: Any) -> Any:
        operation = lambda: getattr(self._model, method)(*args, **kwargs)
        return await asyncio.get_running_loop().run_in_executor(self._executor, operation)

    def warm_up(self) -> None:
        sample_count = SAMPLE_RATE * MIN_AUDIO_MS // 1_000
        silent_pcm = np.zeros(sample_count, dtype=np.float32)
        self._executor.submit(
            self._model.transcribe,
            (silent_pcm, SAMPLE_RATE),
            context="",
            language=None,
            return_time_stamps=False,
        ).result()

    def close(self) -> None:
        self._executor.shutdown(wait=True, cancel_futures=True)


def create_app(model: ASRModel | ModelWorker, settings: RuntimeSettings) -> FastAPI:
    gate = OperationGate()
    worker = model if isinstance(model, ModelWorker) else ModelWorker(lambda: model)

    @asynccontextmanager
    async def lifespan(_app: FastAPI) -> AsyncIterator[None]:
        try:
            yield
        finally:
            worker.close()

    app = FastAPI(title="SparkClaw ASR Runtime", version=PROTOCOL, lifespan=lifespan)

    @app.get("/health")
    async def health() -> dict[str, Any]:
        return {
            "status": "ok",
            "ready": True,
            "busy": gate.busy,
            "supports_streaming": True,
            "protocol": PROTOCOL,
            "sample_rate": SAMPLE_RATE,
            "frame_ms": FRAME_MS,
        }

    @app.get("/version")
    async def version() -> dict[str, str]:
        return {
            "version": package_version("vllm"),
            "runtime": "sparkclaw-asr-runtime",
            "protocol": PROTOCOL,
            "qwen_asr": package_version("qwen-asr"),
        }

    @app.get("/v1/models")
    async def models() -> dict[str, Any]:
        return {
            "object": "list",
            "data": [{
                "id": settings.served_model_name,
                "object": "model",
                "owned_by": "sparkclaw",
            }],
        }

    @app.post("/v1/audio/transcriptions")
    async def transcriptions(
        file: UploadFile = File(...),
        requested_model: str = Form("", alias="model"),
        language: str = Form("auto"),
        response_format: str = Form("json"),
    ) -> dict[str, Any]:
        if requested_model and requested_model != settings.served_model_name:
            raise HTTPException(status_code=404, detail="model not found")
        if response_format != "json":
            raise HTTPException(status_code=422, detail="only json response_format is supported")
        if file.content_type not in {"audio/wav", "audio/x-wav", "application/octet-stream"}:
            raise HTTPException(status_code=415, detail="audio/wav is required")
        raw = await file.read(settings.max_upload_bytes + 1)
        if len(raw) > settings.max_upload_bytes:
            raise HTTPException(status_code=413, detail="audio exceeds the configured limit")
        pcm = decode_wav(raw, settings.max_audio_seconds)
        if not await gate.acquire():
            raise HTTPException(status_code=429, detail="speech service is busy")
        started = time.monotonic()
        try:
            forced_language = normalize_language(language)
            result = await worker.call(
                "transcribe",
                (pcm, SAMPLE_RATE),
                context="",
                language=forced_language,
                return_time_stamps=False,
            )
            first = result[0]
            return {
                "text": str(getattr(first, "text", "")),
                "language": str(getattr(first, "language", "")),
                "model": settings.served_model_name,
                "inference_ms": round((time.monotonic() - started) * 1000),
            }
        except ValueError as error:
            raise HTTPException(status_code=422, detail="speech request is invalid") from error
        except Exception as error:
            raise HTTPException(status_code=500, detail="speech inference failed") from error
        finally:
            gate.release()

    @app.websocket("/v1/audio/realtime")
    async def realtime(websocket: WebSocket) -> None:
        await websocket.accept()
        acquired = False
        try:
            start = await receive_start(websocket)
            if not await gate.acquire():
                await send_terminal(websocket, "fallback", "speech_busy", True)
                return
            acquired = True
            forced_language = normalize_language(str(start.get("language", "auto")))
            state = await worker.call(
                "init_streaming_state",
                context="",
                language=forced_language,
                unfixed_chunk_num=settings.unfixed_chunk_num,
                unfixed_token_num=settings.unfixed_token_num,
                chunk_size_sec=settings.chunk_size_sec,
            )
            await websocket.send_json({
                "event": "ready",
                "protocol": PROTOCOL,
                "format": {
                    "sample_rate": SAMPLE_RATE,
                    "channels": 1,
                    "bits_per_sample": 16,
                    "frame_ms": FRAME_MS,
                },
                "limits": {
                    "max_audio_seconds": settings.max_audio_seconds,
                    "max_frame_samples": FRAME_SAMPLES,
                },
            })
            await serve_realtime(websocket, worker, state, settings)
        except ProtocolError as error:
            await send_terminal(websocket, "error", error.code, False)
        except Exception:
            await send_terminal(websocket, "fallback", "speech_inference_failed", True)
        finally:
            if acquired:
                gate.release()
            await websocket.close()

    return app


class ProtocolError(Exception):
    def __init__(self, code: str) -> None:
        super().__init__(code)
        self.code = code


async def receive_start(websocket: WebSocket) -> dict[str, Any]:
    try:
        raw = await asyncio.wait_for(websocket.receive_text(), timeout=5)
    except Exception as error:
        raise ProtocolError("speech_stream_start_invalid") from error
    if len(raw.encode("utf-8")) > MAX_START_BYTES:
        raise ProtocolError("speech_stream_start_invalid")
    try:
        payload = __import__("json").loads(raw)
    except ValueError as error:
        raise ProtocolError("speech_stream_start_invalid") from error
    if not isinstance(payload, dict) or payload.get("event") != "start":
        raise ProtocolError("speech_stream_start_invalid")
    request_id = str(payload.get("request_id", ""))
    if not REQUEST_ID_PATTERN.fullmatch(request_id):
        raise ProtocolError("speech_stream_start_invalid")
    if payload.get("protocol") != PROTOCOL:
        raise ProtocolError("speech_stream_protocol_unsupported")
    audio_format = payload.get("format")
    if audio_format != {"sample_rate": SAMPLE_RATE, "channels": 1, "bits_per_sample": 16}:
        raise ProtocolError("speech_stream_format_unsupported")
    return payload


async def serve_realtime(
    websocket: WebSocket,
    worker: ModelWorker,
    state: Any,
    settings: RuntimeSettings,
) -> None:
    expected_sequence = 0
    total_samples = 0
    revision = 0
    prior_text = ""
    prior_language = ""
    inference_ms = 0
    loop = asyncio.get_running_loop()
    deadline = loop.time() + settings.max_audio_seconds + REALTIME_FINAL_GRACE_SECONDS
    while True:
        remaining = deadline - loop.time()
        if remaining <= 0:
            raise ProtocolError("speech_stream_session_expired")
        try:
            message = await asyncio.wait_for(
                websocket.receive(),
                timeout=min(REALTIME_IDLE_TIMEOUT_SECONDS, remaining),
            )
        except asyncio.TimeoutError as error:
            raise ProtocolError("speech_stream_session_expired") from error
        if message.get("type") == "websocket.disconnect":
            return
        binary = message.get("bytes")
        if binary is not None:
            sequence, pcm = decode_audio_frame(binary, expected_sequence)
            if total_samples + pcm.size > settings.max_audio_seconds * SAMPLE_RATE:
                raise ProtocolError("speech_too_large")
            started = time.monotonic()
            state = await worker.call("streaming_transcribe", pcm, state)
            inference_ms += round((time.monotonic() - started) * 1000)
            total_samples += int(pcm.size)
            expected_sequence += 1
            await websocket.send_json({
                "event": "ack",
                "accepted_sequence": sequence,
                "received_audio_ms": samples_to_ms(total_samples),
            })
            text = str(getattr(state, "text", ""))
            language = str(getattr(state, "language", ""))
            if text != prior_text or language != prior_language:
                revision += 1
                prior_text, prior_language = text, language
                await websocket.send_json({
                    "event": "partial",
                    "revision": revision,
                    "text": text,
                    "language": language,
                    "audio_end_ms": samples_to_ms(total_samples),
                })
            continue

        text_message = message.get("text")
        if text_message is None or len(text_message.encode("utf-8")) > MAX_START_BYTES:
            raise ProtocolError("speech_stream_control_invalid")
        try:
            control = __import__("json").loads(text_message)
        except ValueError as error:
            raise ProtocolError("speech_stream_control_invalid") from error
        event = control.get("event") if isinstance(control, dict) else ""
        if event == "cancel":
            if int(control.get("last_sequence", -1)) != max(0, expected_sequence - 1):
                raise ProtocolError("speech_stream_sequence_invalid")
            return
        if event != "finish":
            raise ProtocolError("speech_stream_control_invalid")
        if control.get("reason") not in STOP_REASONS:
            raise ProtocolError("speech_stream_control_invalid")
        if int(control.get("last_sequence", -2)) != expected_sequence - 1:
            raise ProtocolError("speech_stream_sequence_invalid")
        captured_ms = int(control.get("captured_ms", -1))
        if abs(captured_ms - samples_to_ms(total_samples)) > 10:
            raise ProtocolError("speech_stream_duration_invalid")
        if samples_to_ms(total_samples) < MIN_AUDIO_MS:
            raise ProtocolError("speech_too_short")
        started = time.monotonic()
        state = await worker.call("finish_streaming_transcribe", state)
        inference_ms += round((time.monotonic() - started) * 1000)
        revision += 1
        await websocket.send_json({
            "event": "final",
            "revision": revision,
            "text": str(getattr(state, "text", "")),
            "language": str(getattr(state, "language", "")),
            "duration_ms": samples_to_ms(total_samples),
            "inference_ms": inference_ms,
            "stop_reason": control["reason"],
            "model": settings.served_model_name,
        })
        return


def decode_audio_frame(raw: bytes, expected_sequence: int) -> tuple[int, np.ndarray]:
    if len(raw) < 8:
        raise ProtocolError("speech_stream_frame_invalid")
    sequence, sample_count = struct.unpack("!II", raw[:8])
    if sequence != expected_sequence:
        raise ProtocolError("speech_stream_sequence_invalid")
    if sample_count <= 0 or sample_count > FRAME_SAMPLES or len(raw) != 8 + sample_count * 2:
        raise ProtocolError("speech_stream_frame_invalid")
    return sequence, np.frombuffer(raw, dtype="<i2", offset=8, count=sample_count).copy()


def decode_wav(raw: bytes, max_audio_seconds: int) -> np.ndarray:
    try:
        audio, sample_rate = sf.read(io.BytesIO(raw), dtype="float32", always_2d=False)
    except Exception as error:
        raise HTTPException(status_code=422, detail="invalid WAV audio") from error
    pcm = np.asarray(audio, dtype=np.float32)
    duration_ms = samples_to_ms(int(pcm.size)) if sample_rate == SAMPLE_RATE else 0
    if pcm.ndim != 1 or sample_rate != SAMPLE_RATE:
        raise HTTPException(status_code=422, detail="16 kHz mono WAV is required")
    if duration_ms < MIN_AUDIO_MS:
        raise HTTPException(status_code=422, detail="audio is too short")
    if duration_ms > max_audio_seconds * 1000:
        raise HTTPException(status_code=413, detail="audio exceeds the configured duration")
    return pcm


def normalize_language(language: str) -> str | None:
    value = language.strip().lower()
    if not value or value == "auto":
        return None
    primary = value.split("-", 1)[0]
    normalized = LANGUAGES.get(primary)
    if normalized is None:
        raise ValueError("unsupported language")
    return normalized


async def send_terminal(websocket: WebSocket, event: str, code: str, retryable: bool) -> None:
    try:
        await websocket.send_json({"event": event, "code": code, "retryable": retryable})
    except Exception:
        pass


def samples_to_ms(samples: int) -> int:
    return round(samples * 1000 / SAMPLE_RATE)


def package_version(name: str) -> str:
    try:
        return importlib.metadata.version(name)
    except importlib.metadata.PackageNotFoundError:
        return "unknown"


def parse_byte_size(raw: str) -> int:
    value = raw.strip().upper()
    factors = {"K": 1 << 10, "M": 1 << 20, "G": 1 << 30}
    if value[-1:] in factors:
        return int(float(value[:-1]) * factors[value[-1]])
    return int(value)


def load_model(args: argparse.Namespace) -> ASRModel:
    from qwen_asr import Qwen3ASRModel

    return Qwen3ASRModel.LLM(
        model=args.model,
        gpu_memory_utilization=args.gpu_memory_utilization,
        kv_cache_memory_bytes=parse_byte_size(args.kv_cache_memory_bytes),
        max_model_len=args.max_model_len,
        max_num_seqs=args.max_num_seqs,
        dtype=args.dtype,
        enforce_eager=args.enforce_eager,
        max_new_tokens=args.max_new_tokens,
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="SparkClaw Qwen3-ASR runtime")
    parser.add_argument("model")
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8006)
    parser.add_argument("--served-model-name", default="sparkclaw-asr")
    parser.add_argument("--gpu-memory-utilization", type=float, default=0.10)
    parser.add_argument("--kv-cache-memory-bytes", default="2G")
    parser.add_argument("--max-model-len", type=int, default=8192)
    parser.add_argument("--max-num-seqs", type=int, default=1)
    parser.add_argument("--dtype", default="bfloat16")
    parser.add_argument("--enforce-eager", action="store_true")
    parser.add_argument("--max-new-tokens", type=int, default=512)
    parser.add_argument("--max-audio-seconds", type=int, default=60)
    parser.add_argument("--max-upload-bytes", type=int, default=3 << 20)
    parser.add_argument("--chunk-size-sec", type=float, default=1.0)
    parser.add_argument("--unfixed-chunk-num", type=int, default=2)
    parser.add_argument("--unfixed-token-num", type=int, default=5)
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    worker = ModelWorker(lambda: load_model(args))
    started = time.monotonic()
    LOGGER.info("warming ASR inference before advertising readiness")
    worker.warm_up()
    LOGGER.info("ASR inference warm-up completed in %d ms", round((time.monotonic() - started) * 1_000))
    settings = RuntimeSettings(
        served_model_name=args.served_model_name,
        max_audio_seconds=args.max_audio_seconds,
        max_upload_bytes=args.max_upload_bytes,
        chunk_size_sec=args.chunk_size_sec,
        unfixed_chunk_num=args.unfixed_chunk_num,
        unfixed_token_num=args.unfixed_token_num,
    )
    uvicorn.run(create_app(worker, settings), host=args.host, port=args.port, access_log=False)


if __name__ == "__main__":
    main()
