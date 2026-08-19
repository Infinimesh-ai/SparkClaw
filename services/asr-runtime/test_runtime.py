from __future__ import annotations

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
    def setUp(self) -> None:
        app = create_app(FakeModel(), RuntimeSettings(served_model_name="sparkclaw-asr"))
        self.client = TestClient(app)

    def test_health_advertises_native_streaming(self) -> None:
        response = self.client.get("/health")
        self.assertEqual(response.status_code, 200)
        self.assertTrue(response.json()["supports_streaming"])
        self.assertEqual(response.json()["protocol"], PROTOCOL)

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
        response = self.client.post(
            "/v1/audio/transcriptions",
            files={"file": ("recording.wav", wav_bytes(8_000), "audio/wav")},
            data={"model": "sparkclaw-asr", "language": "en", "response_format": "json"},
        )
        self.assertEqual(response.status_code, 200, response.text)
        self.assertEqual(response.json()["text"], "batch 8000")
        self.assertEqual(response.json()["language"], "English")

    def test_realtime_emits_partial_before_final(self) -> None:
        with self.client.websocket_connect("/v1/audio/realtime") as socket:
            socket.send_json(start_event())
            self.assertEqual(socket.receive_json()["event"], "ready")
            samples = np.full(1_600, 100, dtype="<i2")
            partial = None
            for sequence in range(3):
                socket.send_bytes(struct.pack("!II", sequence, samples.size) + samples.tobytes())
                self.assertEqual(socket.receive_json()["event"], "ack")
                partial = socket.receive_json()
                self.assertEqual(partial["event"], "partial")
            self.assertEqual(partial["text"], "partial 4800")
            socket.send_json({
                "event": "finish",
                "last_sequence": 2,
                "captured_ms": 300,
                "reason": "manual_stop",
            })
            final = socket.receive_json()
            self.assertEqual(final["event"], "final")
            self.assertEqual(final["text"], "final transcript")
            self.assertGreater(final["revision"], partial["revision"])

    def test_realtime_rejects_finish_below_minimum_audio(self) -> None:
        with self.client.websocket_connect("/v1/audio/realtime") as socket:
            socket.send_json(start_event())
            self.assertEqual(socket.receive_json()["event"], "ready")
            samples = np.zeros(1_600, dtype="<i2")
            socket.send_bytes(struct.pack("!II", 0, samples.size) + samples.tobytes())
            self.assertEqual(socket.receive_json()["event"], "ack")
            self.assertEqual(socket.receive_json()["event"], "partial")
            socket.send_json({
                "event": "finish",
                "last_sequence": 0,
                "captured_ms": 100,
                "reason": "manual_stop",
            })
            error = socket.receive_json()
            self.assertEqual(error["event"], "error")
            self.assertEqual(error["code"], "speech_too_short")

    def test_realtime_rejects_sequence_gaps(self) -> None:
        with self.client.websocket_connect("/v1/audio/realtime") as socket:
            socket.send_json(start_event())
            self.assertEqual(socket.receive_json()["event"], "ready")
            samples = np.zeros(100, dtype="<i2")
            socket.send_bytes(struct.pack("!II", 1, samples.size) + samples.tobytes())
            error = socket.receive_json()
            self.assertEqual(error["event"], "error")
            self.assertEqual(error["code"], "speech_stream_sequence_invalid")


def start_event() -> dict[str, object]:
    return {
        "event": "start",
        "protocol": PROTOCOL,
        "request_id": "voice-test",
        "language": "auto",
        "format": {"sample_rate": 16_000, "channels": 1, "bits_per_sample": 16},
    }


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
