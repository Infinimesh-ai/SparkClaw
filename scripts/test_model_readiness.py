from __future__ import annotations

import json
import tempfile
import threading
import unittest
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from scripts import model_readiness


class ProbeHandler(BaseHTTPRequestHandler):
    get_count = 0
    post_count = 0
    completion_content = "Safety: Safe"
    last_payload: dict[str, object] = {}
    served_model = "guard-test"

    def do_GET(self) -> None:
        type(self).get_count += 1
        self.respond({"data": [{"id": type(self).served_model}]})

    def do_POST(self) -> None:
        type(self).post_count += 1
        length = int(self.headers.get("Content-Length", "0"))
        type(self).last_payload = json.loads(self.rfile.read(length))
        self.respond(
            {
                "choices": [
                    {"message": {"content": type(self).completion_content}}
                ]
            }
        )

    def respond(self, payload: dict[str, object]) -> None:
        raw = json.dumps(payload).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, _format: str, *_args: object) -> None:
        return


@contextmanager
def probe_server():
    ProbeHandler.get_count = 0
    ProbeHandler.post_count = 0
    ProbeHandler.completion_content = "Safety: Safe"
    ProbeHandler.last_payload = {}
    server = ThreadingHTTPServer(("127.0.0.1", 0), ProbeHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}/v1"
    finally:
        server.shutdown()
        thread.join()
        server.server_close()


class ModelReadinessTest(unittest.TestCase):
    def check(
        self,
        base_url: str,
        marker: Path,
        *,
        instance_id: str = "instance-a",
        prompt_repetitions: int = 0,
        max_tokens: int = 8,
        min_tokens: int = 0,
    ) -> None:
        model_readiness.check_readiness(
            base_url=base_url,
            model="guard-test",
            marker=marker,
            health_timeout=1,
            warmup_timeout=1,
            instance_id=instance_id,
            prompt_repetitions=prompt_repetitions,
            max_tokens=max_tokens,
            min_tokens=min_tokens,
        )

    def test_first_check_runs_completion_and_marks_ready(self) -> None:
        with tempfile.TemporaryDirectory() as directory, probe_server() as base_url:
            marker = Path(directory) / "ready"
            self.check(base_url, marker)

            self.assertEqual(ProbeHandler.post_count, 1)
            self.assertEqual(ProbeHandler.get_count, 0)
            self.assertTrue(
                marker.read_text(encoding="utf-8").strip().startswith("v2:")
            )
            self.assertEqual(ProbeHandler.last_payload["model"], "guard-test")
            self.assertEqual(
                ProbeHandler.last_payload["chat_template_kwargs"],
                {"enable_thinking": False},
            )

    def test_warmed_check_uses_lightweight_model_listing(self) -> None:
        with tempfile.TemporaryDirectory() as directory, probe_server() as base_url:
            marker = Path(directory) / "ready"
            self.check(base_url, marker)
            self.check(base_url, marker)

            self.assertEqual(ProbeHandler.post_count, 1)
            self.assertEqual(ProbeHandler.get_count, 1)

    def test_empty_completion_does_not_mark_ready(self) -> None:
        with tempfile.TemporaryDirectory() as directory, probe_server() as base_url:
            marker = Path(directory) / "ready"
            ProbeHandler.completion_content = ""

            with self.assertRaises(model_readiness.ReadinessError):
                self.check(base_url, marker)

            self.assertFalse(marker.exists())

    def test_model_change_requires_another_warmup(self) -> None:
        with tempfile.TemporaryDirectory() as directory, probe_server() as base_url:
            marker = Path(directory) / "ready"
            old_payload = model_readiness.warmup_payload("old-model")
            marker.write_text(
                model_readiness.marker_value(
                    model="old-model",
                    payload=old_payload,
                    instance_id="instance-a",
                )
                + "\n",
                encoding="utf-8",
            )

            self.check(base_url, marker)

            self.assertEqual(ProbeHandler.post_count, 1)
            self.assertTrue(
                marker.read_text(encoding="utf-8").strip().startswith("v2:")
            )

    def test_warmed_check_rejects_missing_served_model(self) -> None:
        with tempfile.TemporaryDirectory() as directory, probe_server() as base_url:
            marker = Path(directory) / "ready"
            payload = model_readiness.warmup_payload("guard-test")
            marker.write_text(
                model_readiness.marker_value(
                    model="guard-test",
                    payload=payload,
                    instance_id="instance-a",
                )
                + "\n",
                encoding="utf-8",
            )
            ProbeHandler.served_model = "other-model"
            try:
                with self.assertRaises(model_readiness.ReadinessError):
                    self.check(base_url, marker)
            finally:
                ProbeHandler.served_model = "guard-test"

    def test_heavy_warmup_shape_is_sent_to_completion(self) -> None:
        with tempfile.TemporaryDirectory() as directory, probe_server() as base_url:
            marker = Path(directory) / "ready"
            self.check(
                base_url,
                marker,
                prompt_repetitions=3,
                max_tokens=16,
                min_tokens=12,
            )

            message = ProbeHandler.last_payload["messages"][0]
            self.assertEqual(
                message["content"].count(model_readiness.WARMUP_PROMPT_UNIT),
                3,
            )
            self.assertEqual(ProbeHandler.last_payload["max_tokens"], 16)
            self.assertEqual(ProbeHandler.last_payload["min_tokens"], 12)

    def test_warmup_shape_change_requires_another_completion(self) -> None:
        with tempfile.TemporaryDirectory() as directory, probe_server() as base_url:
            marker = Path(directory) / "ready"
            self.check(base_url, marker)
            self.check(base_url, marker, prompt_repetitions=1)

            self.assertEqual(ProbeHandler.post_count, 2)
            self.assertEqual(ProbeHandler.get_count, 0)

    def test_process_instance_change_requires_another_completion(self) -> None:
        with tempfile.TemporaryDirectory() as directory, probe_server() as base_url:
            marker = Path(directory) / "ready"
            self.check(base_url, marker, instance_id="instance-a")
            self.check(base_url, marker, instance_id="instance-b")

            self.assertEqual(ProbeHandler.post_count, 2)
            self.assertEqual(ProbeHandler.get_count, 0)

    def test_invalid_warmup_shape_is_rejected(self) -> None:
        invalid_shapes = [
            {"prompt_repetitions": -1},
            {"max_tokens": 0},
            {"max_tokens": 8, "min_tokens": 9},
            {"min_tokens": -1},
        ]
        for shape in invalid_shapes:
            with self.subTest(shape=shape):
                with self.assertRaises(model_readiness.ReadinessError):
                    model_readiness.warmup_payload("guard-test", **shape)

    def test_process_instance_uses_pid_start_time(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            stat = Path(directory) / "stat"
            tail = ["S"] + [str(value) for value in range(4, 23)]
            stat.write_text(f"1 (model server) {' '.join(tail)}\n", encoding="utf-8")

            self.assertEqual(model_readiness.process_instance_id(stat), "22")


if __name__ == "__main__":
    unittest.main()
