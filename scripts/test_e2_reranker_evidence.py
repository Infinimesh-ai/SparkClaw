from __future__ import annotations

import copy
import json
import os
import shutil
import tempfile
import threading
import unittest
from contextlib import contextmanager
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from unittest import mock

from scripts import e2_reranker_evidence as evidence
from scripts import e2_reranker_fake_smoke as smoke


class FakeE2Handler(BaseHTTPRequestHandler):
    calls: list[tuple[str, str]] = []
    request_raw = b""
    manifest: dict[str, object] = {}
    manifest_sha = ""
    duplicate_header = False
    redirect_first = False
    logprob_lexeme = "-0.6931471805599453"
    completion_raw = b""
    version_override: str | None = None

    def do_GET(self) -> None:
        type(self).calls.append(("GET", self.path))
        if type(self).redirect_first and len(type(self).calls) == 1:
            self.send_response(302)
            self.send_header("Location", "https://example.invalid/forbidden")
            self.end_headers()
            return
        manifest = type(self).manifest
        routes = manifest["routes"]
        if self.path == routes["models_path"]:
            payload = json.dumps(
                {"data": [manifest["expected_models_projection"]]},
                separators=(",", ":"),
            ).encode()
            self.respond(payload, "application/json")
        elif self.path == routes["version_path"]:
            payload = json.dumps(
                {
                    "version": type(self).version_override
                    or manifest["runtime"]["vllm_reported_version"]
                },
                separators=(",", ":"),
            ).encode()
            self.respond(payload, "application/json")
        elif self.path == routes["health_path"]:
            self.respond(b"", "text/plain")
        elif self.path == routes["metrics_path"]:
            self.respond(
                b'vllm:cache_config_info{enable_prefix_caching="False"} 1\n',
                "text/plain",
            )
        else:
            self.send_error(404)

    def do_POST(self) -> None:
        type(self).calls.append(("POST", self.path))
        length = int(self.headers.get("Content-Length", "0"))
        type(self).request_raw = self.rfile.read(length)
        manifest = type(self).manifest
        binding = manifest["model"]["model_binding"]
        prompt = binding["prefix_token_ids"] + binding["suffix_token_ids"]
        labels = manifest["response_contract"]["explicit_label_logprobs"]
        envelope = {
            "id": "cmpl-synthetic-loopback",
            "object": "text_completion",
            "created": 0,
            "model": manifest["model"]["served_name"],
            "prompt_token_ids": prompt,
            "choices": [
                {
                    "index": 0,
                    "text": "token_id:9693",
                    "token_ids": [labels[1]],
                    "finish_reason": "length",
                    "logprobs": {
                        "logprob_token_ids": [
                            {"token_id": labels[0], "logprob": "__LOGP0__"},
                            {"token_id": labels[1], "logprob": "__LOGP1__"},
                        ]
                    },
                }
            ],
            "usage": {
                "prompt_tokens": len(prompt),
                "completion_tokens": 1,
                "total_tokens": len(prompt) + 1,
            },
        }
        encoded = json.dumps(envelope, separators=(",", ":")).encode()
        encoded = encoded.replace(b'"__LOGP0__"', type(self).logprob_lexeme.encode())
        encoded = encoded.replace(b'"__LOGP1__"', type(self).logprob_lexeme.encode())
        type(self).completion_raw = encoded
        self.respond(encoded, "application/json")

    def respond(self, payload: bytes, content_type: str) -> None:
        manifest = type(self).manifest
        names = manifest["live_identity_headers"]
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header(names["manifest_sha256"], type(self).manifest_sha)
        if type(self).duplicate_header:
            self.send_header(names["manifest_sha256"], type(self).manifest_sha)
        self.send_header(
            names["deployment_revision"],
            manifest["runtime"]["deployment_revision"],
        )
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, _format: str, *_args: object) -> None:
        return


@contextmanager
def fake_server(manifest: dict[str, object], manifest_sha: str):
    FakeE2Handler.calls = []
    FakeE2Handler.request_raw = b""
    FakeE2Handler.manifest = manifest
    FakeE2Handler.manifest_sha = manifest_sha
    FakeE2Handler.duplicate_header = False
    FakeE2Handler.redirect_first = False
    FakeE2Handler.logprob_lexeme = "-0.6931471805599453"
    FakeE2Handler.completion_raw = b""
    FakeE2Handler.version_override = None
    server = ThreadingHTTPServer(("127.0.0.1", 0), FakeE2Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}"
    finally:
        server.shutdown()
        thread.join()
        server.server_close()


def fixture_artifacts(bundle: evidence.ContractBundle) -> dict[str, object]:
    positive = next(
        case for case in bundle.cases if case["outcome"] == "accept_synthetic_fixture"
    )
    fixture = json.loads((bundle.conformance_dir / positive["file"]).read_text())
    return copy.deepcopy(fixture["artifacts"])


def two_ticks():
    values = iter(
        [
            datetime(2000, 1, 1, tzinfo=timezone.utc),
            datetime(2000, 1, 1, 0, 0, 1, tzinfo=timezone.utc),
        ]
    )
    return lambda: next(values)


class E2RerankerEvidenceTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.bundle = evidence.ContractBundle.load()

    def test_contract_closure_dynamically_runs_every_case(self) -> None:
        self.assertEqual(len(self.bundle.cases), self.bundle.closure["fixture_count"])
        self.assertEqual(
            {case["file"] for case in self.bundle.cases},
            {
                path.relative_to(self.bundle.conformance_dir).as_posix()
                for path in (self.bundle.conformance_dir / "fixtures").glob("*.json")
            },
        )

    def test_root_pin_precedes_manifest_decode(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / "conformance/v1/manifest.json"
            path.parent.mkdir(parents=True)
            path.write_bytes(b"{not-json")
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.ContractBundle.load(root)
        self.assertEqual(raised.exception.code, "external_conformance_pin_mismatch")

    def test_fixture_inventory_pin_detects_drift(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            copied = Path(directory) / "contract"
            shutil.copytree(self.bundle.root, copied)
            first = copied / "conformance/v1" / self.bundle.cases[0]["file"]
            first.write_bytes(first.read_bytes() + b" ")
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.ContractBundle.load(copied)
        self.assertEqual(raised.exception.code, "fixture_pin_mismatch")

    def test_external_four_pins_precede_artifact_decode(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            manifest = Path(directory) / "manifest.json"
            receipt = Path(directory) / "receipt.json"
            manifest.write_bytes(b"{malformed")
            receipt.write_bytes(b"{malformed")
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.verify_external_pair(
                    self.bundle,
                    manifest,
                    receipt,
                    manifest_sha256="0" * 64,
                    manifest_size=10,
                    receipt_sha256="1" * 64,
                    receipt_size=10,
                )
        self.assertEqual(raised.exception.code, "external_manifest_pin_mismatch")

    def test_correct_external_pins_still_reject_synthetic_pair(self) -> None:
        artifacts = fixture_artifacts(self.bundle)
        manifest_raw = evidence.canonical_bytes(
            artifacts["deployment_manifest"], stage="test"
        )
        receipt_raw = evidence.canonical_bytes(artifacts["smoke_receipt"], stage="test")
        with tempfile.TemporaryDirectory() as directory:
            manifest = Path(directory) / "manifest.json"
            receipt = Path(directory) / "receipt.json"
            manifest.write_bytes(manifest_raw)
            receipt.write_bytes(receipt_raw)
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.verify_external_pair(
                    self.bundle,
                    manifest,
                    receipt,
                    manifest_sha256=evidence.sha256_bytes(manifest_raw),
                    manifest_size=len(manifest_raw),
                    receipt_sha256=evidence.sha256_bytes(receipt_raw),
                    receipt_size=len(receipt_raw),
                )
        self.assertEqual(raised.exception.code, "synthetic_artifact_not_live")

    def test_catalog_producer_closes_over_full_snapshot(self) -> None:
        artifacts = fixture_artifacts(self.bundle)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "model.bin").write_bytes(b"model")
            (root / "tokenizer.json").write_bytes(b"tokenizer")
            raw = evidence.produce_manifest(
                self.bundle,
                artifacts["deployment_manifest"],
                root,
                {"model.bin": "model", "tokenizer.json": "tokenizer"},
                allow_synthetic=True,
            )
        manifest = evidence.strict_canonical_bytes(raw, stage="test")
        catalog = manifest["model"]["artifact_catalog"]
        self.assertEqual(catalog["file_count"], len(catalog["files"]))
        self.assertEqual(
            catalog["files_sha256"],
            evidence.sha256_bytes(evidence.canonical_bytes(catalog["files"], stage="test")),
        )

    def test_catalog_rejects_unclassified_file(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "model.bin").write_bytes(b"model")
            (root / "tokenizer.json").write_bytes(b"tokenizer")
            (root / "unclassified.txt").write_bytes(b"extra")
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.build_artifact_catalog(
                    self.bundle,
                    root,
                    {"model.bin": "model", "tokenizer.json": "tokenizer"},
                )
        self.assertEqual(raised.exception.code, "catalog_classification_incomplete")

    def test_fake_smoke_uses_exact_nine_steps_and_canonical_request(self) -> None:
        artifacts = fixture_artifacts(self.bundle)
        manifest = artifacts["deployment_manifest"]
        manifest_raw = evidence.canonical_bytes(manifest, stage="test")
        manifest_sha = evidence.sha256_bytes(manifest_raw)
        with fake_server(manifest, manifest_sha) as origin, mock.patch.dict(
            os.environ,
            {"HTTP_PROXY": "http://127.0.0.1:1", "HTTPS_PROXY": "http://127.0.0.1:1"},
        ):
            client = smoke.SyntheticLoopbackSmokeClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            receipt_raw = client.run(manifest, manifest_raw)
        receipt = evidence.strict_canonical_bytes(receipt_raw, stage="test")
        self.assertEqual(client.actual_order, receipt["observation_order"])
        self.assertEqual(
            sum(method == "POST" for method, _path in FakeE2Handler.calls), 1
        )
        self.assertEqual(
            FakeE2Handler.request_raw,
            evidence.canonical_bytes(receipt["completion"]["request"]["body"], stage="test"),
        )
        expected_request = artifacts["smoke_receipt"]["completion"]["request"]
        self.assertEqual(
            (
                receipt["completion"]["request"]["raw_body_sha256"],
                receipt["completion"]["request"]["raw_body_size_bytes"],
            ),
            (expected_request["raw_body_sha256"], expected_request["raw_body_size_bytes"]),
        )
        response = receipt["completion"]["response"]
        self.assertNotEqual(
            (response["raw_body_sha256"], response["raw_body_size_bytes"]),
            (response["projection_sha256"], response["projection_size_bytes"]),
        )
        self.assertEqual(
            self.bundle.evaluate_pair(
                {"deployment_manifest": manifest, "smoke_receipt": receipt},
                allow_synthetic=True,
            ),
            ("accept_synthetic_fixture", "accepted", "none"),
        )

    def test_duplicate_live_header_fails_before_completion(self) -> None:
        artifacts = fixture_artifacts(self.bundle)
        manifest = artifacts["deployment_manifest"]
        manifest_raw = evidence.canonical_bytes(manifest, stage="test")
        with fake_server(manifest, evidence.sha256_bytes(manifest_raw)) as origin:
            FakeE2Handler.duplicate_header = True
            client = smoke.SyntheticLoopbackSmokeClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                client.run(manifest, manifest_raw)
        self.assertEqual(raised.exception.code, "live_header_cardinality_invalid")
        self.assertFalse(any(method == "POST" for method, _path in FakeE2Handler.calls))

    def test_preflight_identity_drift_fails_before_completion(self) -> None:
        artifacts = fixture_artifacts(self.bundle)
        manifest = artifacts["deployment_manifest"]
        manifest_raw = evidence.canonical_bytes(manifest, stage="test")
        with fake_server(manifest, evidence.sha256_bytes(manifest_raw)) as origin:
            FakeE2Handler.version_override = "9.9.9"
            client = smoke.SyntheticLoopbackSmokeClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                client.run(manifest, manifest_raw)
        self.assertEqual(raised.exception.code, "vllm_version_mismatch")
        self.assertFalse(any(method == "POST" for method, _path in FakeE2Handler.calls))

    def test_redirect_is_not_followed_or_retried(self) -> None:
        artifacts = fixture_artifacts(self.bundle)
        manifest = artifacts["deployment_manifest"]
        manifest_raw = evidence.canonical_bytes(manifest, stage="test")
        with fake_server(manifest, evidence.sha256_bytes(manifest_raw)) as origin:
            FakeE2Handler.redirect_first = True
            client = smoke.SyntheticLoopbackSmokeClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                client.run(manifest, manifest_raw)
        self.assertEqual(raised.exception.code, "redirect_forbidden")
        self.assertEqual(len(FakeE2Handler.calls), 1)

    def test_hostname_and_non_loopback_are_rejected_without_dns(self) -> None:
        for origin, code in (
            ("http://localhost:8000", "dns_forbidden"),
            ("http://192.0.2.1:8000", "non_loopback_forbidden"),
        ):
            with self.subTest(origin=origin), self.assertRaises(evidence.EvidenceFailure) as raised:
                smoke.SyntheticLoopbackSmokeClient(
                    self.bundle, origin=origin, timeout=1
                )
            self.assertEqual(raised.exception.code, code)

    def test_logprob_underflow_fails_without_retry(self) -> None:
        artifacts = fixture_artifacts(self.bundle)
        manifest = artifacts["deployment_manifest"]
        manifest_raw = evidence.canonical_bytes(manifest, stage="test")
        with fake_server(manifest, evidence.sha256_bytes(manifest_raw)) as origin:
            FakeE2Handler.logprob_lexeme = "-1000"
            client = smoke.SyntheticLoopbackSmokeClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                client.run(manifest, manifest_raw)
        self.assertEqual(raised.exception.code, "label_logprob_underflow")
        self.assertEqual(
            sum(method == "POST" for method, _path in FakeE2Handler.calls), 1
        )

    def test_negative_zero_logprob_is_not_canonical(self) -> None:
        artifacts = fixture_artifacts(self.bundle)
        manifest = artifacts["deployment_manifest"]
        manifest_raw = evidence.canonical_bytes(manifest, stage="test")
        with fake_server(manifest, evidence.sha256_bytes(manifest_raw)) as origin:
            FakeE2Handler.logprob_lexeme = "-0"
            client = smoke.SyntheticLoopbackSmokeClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                client.run(manifest, manifest_raw)
        self.assertEqual(raised.exception.code, "smoke_receipt_schema_invalid")


if __name__ == "__main__":
    unittest.main()
