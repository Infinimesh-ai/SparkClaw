from __future__ import annotations

from contextlib import contextmanager
import io
from pathlib import Path
import struct
import sys
import threading
from types import SimpleNamespace
import unittest
import wave

import numpy as np
from fastapi.testclient import TestClient

sys.path.insert(0, str(Path(__file__).resolve().parent))
from runtime import MIN_AUDIO_MS, PROTOCOL, SAMPLE_RATE, ModelWorker, RuntimeSettings, create_app  # noqa: E402


class FakeState:
    def __init__(self) -> None:
        self.samples = 0
        self.language = ""
        self.text = ""


class FakeModel:
    def transcribe(self, audio, **_kwargs):
        samples, sample_rate = audio
        assert sample_rate == 16_000
        return [SimpleNamespace(text=f"batch {samples.size}", language="English")]

    def init_streaming_state(self, **_kwargs):
        return FakeState()

    def streaming_transcribe(self, pcm, state):
        state.samples += pcm.size
        state.language = "English"
        state.text = f"partial {state.samples}"
        return state

    def finish_streaming_transcribe(self, state):
        state.text = "final transcript"
        return state


class RuntimeTest(unittest.TestCase):
    def test_fake_model_app_lifecycle_and_discovery(self) -> None:
        class TrackingWorker(ModelWorker):
            def __init__(self) -> None:
                self.closed = False
                super().__init__(FakeModel)

            def close(self) -> None:
                super().close()
                self.closed = True

        worker = TrackingWorker()
        app = create_app(worker, test_settings())
        with TestClient(app) as client:
            health = client.get("/health")
            self.assertEqual(health.status_code, 200)
            self.assertEqual(health.json(), {
                "status": "ok",
                "ready": True,
                "busy": False,
                "supports_streaming": True,
                "protocol": PROTOCOL,
                "sample_rate": SAMPLE_RATE,
                "frame_ms": 100,
            })
            version = client.get("/version")
            self.assertEqual(version.status_code, 200)
            self.assertEqual(version.json()["runtime"], "sparkclaw-asr-runtime")
            self.assertEqual(version.json()["protocol"], PROTOCOL)
            models = client.get("/v1/models")
            self.assertEqual(models.status_code, 200)
            self.assertEqual(models.json()["data"][0]["id"], "sparkclaw-asr")
            self.assertFalse(worker.closed)

        self.assertTrue(worker.closed)
        imported = {name.split(".", 1)[0] for name in sys.modules}
        self.assertNotIn("qwen_asr", imported)
        self.assertNotIn("vllm", imported)

    def test_model_worker_warms_up_on_its_owner_thread(self) -> None:
        owner_thread = None
        calls = []

        class ThreadBoundModel(FakeModel):
            def __init__(self) -> None:
                nonlocal owner_thread
                owner_thread = threading.get_ident()

            def transcribe(self, audio, **kwargs):
                calls.append((threading.get_ident(), audio[0].size, kwargs))
                return super().transcribe(audio, **kwargs)

        worker = ModelWorker(ThreadBoundModel)
        try:
            worker.warm_up()
        finally:
            worker.close()

        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0][0], owner_thread)
        self.assertEqual(calls[0][1], SAMPLE_RATE * MIN_AUDIO_MS // 1_000)
        self.assertFalse(calls[0][2]["return_time_stamps"])

    def test_batch_contract_remains_openai_compatible(self) -> None:
        with runtime_client() as client:
            response = batch_request(client)
            self.assertEqual(response.status_code, 200, response.text)
            self.assertEqual(response.json()["text"], "batch 8000")
            self.assertEqual(response.json()["language"], "English")
            self.assertEqual(response.json()["model"], "sparkclaw-asr")

    def test_batch_failure_releases_operation_gate(self) -> None:
        class FailingOnceModel(FakeModel):
            def __init__(self) -> None:
                self.calls = 0

            def transcribe(self, audio, **kwargs):
                self.calls += 1
                if self.calls == 1:
                    raise RuntimeError("injected batch failure")
                return super().transcribe(audio, **kwargs)

        with runtime_client(FailingOnceModel()) as client:
            failed = batch_request(client)
            self.assertEqual(failed.status_code, 500)
            self.assertEqual(failed.json()["detail"], "speech inference failed")
            self.assert_gate_released(client)

    def test_realtime_emits_partial_before_final(self) -> None:
        with runtime_client() as client:
            with client.websocket_connect("/v1/audio/realtime") as socket:
                socket.send_json(start_event())
                ready = socket.receive_json()
                self.assertEqual(ready["event"], "ready")
                self.assertEqual(ready["protocol"], PROTOCOL)
                self.assertEqual(ready["limits"]["max_frame_samples"], 1_600)
                partial = None
                for sequence in range(3):
                    send_frame(socket, sequence)
                    ack = socket.receive_json()
                    self.assertEqual(ack["event"], "ack")
                    self.assertEqual(ack["accepted_sequence"], sequence)
                    partial = socket.receive_json()
                    self.assertEqual(partial["event"], "partial")
                self.assertEqual(partial["text"], "partial 4800")
                socket.send_json(finish_event(last_sequence=2, captured_ms=300))
                final = socket.receive_json()
                self.assertEqual(final["event"], "final")
                self.assertEqual(final["text"], "final transcript")
                self.assertEqual(final["duration_ms"], 300)
                self.assertGreater(final["revision"], partial["revision"])
            self.assert_gate_released(client)

    def test_realtime_rejects_finish_below_minimum_audio(self) -> None:
        with runtime_client() as client:
            with client.websocket_connect("/v1/audio/realtime") as socket:
                socket.send_json(start_event())
                self.assertEqual(socket.receive_json()["event"], "ready")
                send_frame(socket, 0)
                self.assertEqual(socket.receive_json()["event"], "ack")
                self.assertEqual(socket.receive_json()["event"], "partial")
                socket.send_json(finish_event(last_sequence=0, captured_ms=100))
                error = socket.receive_json()
                self.assertEqual(error["event"], "error")
                self.assertEqual(error["code"], "speech_too_short")
            self.assert_gate_released(client)

    def test_realtime_rejects_sequence_gaps(self) -> None:
        with runtime_client() as client:
            with client.websocket_connect("/v1/audio/realtime") as socket:
                socket.send_json(start_event())
                self.assertEqual(socket.receive_json()["event"], "ready")
                send_frame(socket, 1, sample_count=100)
                error = socket.receive_json()
                self.assertEqual(error["event"], "error")
                self.assertEqual(error["code"], "speech_stream_sequence_invalid")
            self.assert_gate_released(client)

    def test_realtime_rejects_malformed_start_without_acquiring_gate(self) -> None:
        starts = (
            ("not-json", "speech_stream_start_invalid"),
            ({**start_event(), "protocol": "unsupported"}, "speech_stream_protocol_unsupported"),
            ({**start_event(), "format": {"sample_rate": 8_000}}, "speech_stream_format_unsupported"),
        )
        for start, expected_code in starts:
            with self.subTest(code=expected_code), runtime_client() as client:
                with client.websocket_connect("/v1/audio/realtime") as socket:
                    if isinstance(start, str):
                        socket.send_text(start)
                    else:
                        socket.send_json(start)
                    error = socket.receive_json()
                    self.assertEqual(error["event"], "error")
                    self.assertEqual(error["code"], expected_code)
                self.assert_gate_released(client)

    def test_realtime_rejects_malformed_frame_and_control(self) -> None:
        cases = (
            (lambda socket: socket.send_bytes(b"short"), "speech_stream_frame_invalid"),
            (lambda socket: socket.send_text("not-json"), "speech_stream_control_invalid"),
            (lambda socket: socket.send_json({"event": "unknown"}), "speech_stream_control_invalid"),
        )
        for send_invalid, expected_code in cases:
            with self.subTest(code=expected_code), runtime_client() as client:
                with client.websocket_connect("/v1/audio/realtime") as socket:
                    socket.send_json(start_event())
                    self.assertEqual(socket.receive_json()["event"], "ready")
                    send_invalid(socket)
                    error = socket.receive_json()
                    self.assertEqual(error["event"], "error")
                    self.assertEqual(error["code"], expected_code)
                self.assert_gate_released(client)

    def test_realtime_busy_and_cancel_release_operation_gate(self) -> None:
        with runtime_client() as client:
            with client.websocket_connect("/v1/audio/realtime") as socket:
                socket.send_json(start_event())
                self.assertEqual(socket.receive_json()["event"], "ready")
                self.assertTrue(client.get("/health").json()["busy"])
                self.assertEqual(batch_request(client).status_code, 429)
                socket.send_json({"event": "cancel", "last_sequence": 0})
            self.assert_gate_released(client)

    def test_realtime_inference_failure_releases_operation_gate(self) -> None:
        class FailingStreamingModel(FakeModel):
            def streaming_transcribe(self, pcm, state):
                raise RuntimeError("injected streaming failure")

        with runtime_client(FailingStreamingModel()) as client:
            with client.websocket_connect("/v1/audio/realtime") as socket:
                socket.send_json(start_event())
                self.assertEqual(socket.receive_json()["event"], "ready")
                send_frame(socket, 0)
                failure = socket.receive_json()
                self.assertEqual(failure["event"], "fallback")
                self.assertEqual(failure["code"], "speech_inference_failed")
                self.assertTrue(failure["retryable"])
            self.assert_gate_released(client)

    def assert_gate_released(self, client: TestClient) -> None:
        self.assertFalse(client.get("/health").json()["busy"])
        response = batch_request(client)
        self.assertEqual(response.status_code, 200, response.text)


@contextmanager
def runtime_client(model=None, settings=None):
    app = create_app(model or FakeModel(), settings or test_settings())
    with TestClient(app) as client:
        yield client


def test_settings() -> RuntimeSettings:
    return RuntimeSettings(served_model_name="sparkclaw-asr")


def start_event() -> dict[str, object]:
    return {
        "event": "start",
        "protocol": PROTOCOL,
        "request_id": "voice-test",
        "language": "auto",
        "format": {"sample_rate": 16_000, "channels": 1, "bits_per_sample": 16},
    }


def send_frame(socket, sequence: int, sample_count: int = 1_600) -> None:
    samples = np.full(sample_count, 100, dtype="<i2")
    socket.send_bytes(struct.pack("!II", sequence, samples.size) + samples.tobytes())


def finish_event(last_sequence: int, captured_ms: int) -> dict[str, object]:
    return {
        "event": "finish",
        "last_sequence": last_sequence,
        "captured_ms": captured_ms,
        "reason": "manual_stop",
    }


def batch_request(client: TestClient):
    return client.post(
        "/v1/audio/transcriptions",
        files={"file": ("recording.wav", wav_bytes(8_000), "audio/wav")},
        data={"model": "sparkclaw-asr", "language": "en", "response_format": "json"},
    )


def wav_bytes(sample_count: int) -> bytes:
    output = io.BytesIO()
    with wave.open(output, "wb") as writer:
        writer.setnchannels(1)
        writer.setsampwidth(2)
        writer.setframerate(16_000)
        writer.writeframes(b"\x00\x00" * sample_count)
    return output.getvalue()


if __name__ == "__main__":
    unittest.main()
