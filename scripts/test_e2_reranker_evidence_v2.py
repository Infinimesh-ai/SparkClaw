from __future__ import annotations

import copy
import hashlib
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

from scripts import e2_reranker_evidence_v2 as evidence
from scripts import e2_reranker_fake_smoke_v2 as smoke


class FakeNativeRerankHandler(BaseHTTPRequestHandler):
    calls: list[tuple[str, str]] = []
    request_raw = b""
    request_id: str | None = None
    manifest: dict[str, object] = {}
    manifest_sha = ""
    duplicate_header = False
    redirect_first = False
    version_override: str | None = None
    cache_enabled_override: bool | None = None
    pre_queries = 10
    pre_hits = 4
    post_queries = 11
    post_hits = 5
    metrics_calls = 0
    multi_modal_mode = "null"
    score_lexeme = "0.75"
    response_id_override: str | None = None
    extra_top_level = False

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
            raw = json.dumps(
                {"data": [manifest["expected_models_projection"]]},
                separators=(",", ":"),
            ).encode("utf-8")
            self.respond(raw, "application/json")
        elif self.path == routes["version_path"]:
            raw = json.dumps(
                {
                    "version": type(self).version_override
                    or manifest["runtime"]["vllm_reported_version"]
                },
                separators=(",", ":"),
            ).encode("utf-8")
            self.respond(raw, "application/json")
        elif self.path == routes["health_path"]:
            self.respond(b"", "text/plain")
        elif self.path == routes["metrics_path"]:
            cache = manifest["serving"]["prefix_cache"]
            enabled = (
                cache["enabled"]
                if type(self).cache_enabled_override is None
                else type(self).cache_enabled_override
            )
            post = type(self).metrics_calls > 0
            type(self).metrics_calls += 1
            queries = type(self).post_queries if post else type(self).pre_queries
            hits = type(self).post_hits if post else type(self).pre_hits
            raw = (
                "sparkclaw:e2_prefix_cache_config{"
                f'enabled="{str(enabled).lower()}",'
                f'resolved_config_sha256="{cache["resolved_config_sha256"]}",'
                f'resolved_config_size_bytes="{cache["resolved_config_size_bytes"]}"}} 1\n'
                f"vllm:prefix_cache_queries_total {queries}\n"
                f"vllm:prefix_cache_hits_total {hits}\n"
            ).encode("ascii")
            self.respond(raw, "text/plain")
        else:
            self.send_error(404)

    def do_POST(self) -> None:
        type(self).calls.append(("POST", self.path))
        length = int(self.headers.get("Content-Length", "0"))
        type(self).request_raw = self.rfile.read(length)
        type(self).request_id = self.headers.get(evidence.REQUEST_ID_HEADER)
        manifest = type(self).manifest
        if self.path != manifest["routes"]["rerank_path"]:
            self.send_error(404)
            return
        document: dict[str, object] = {"text": evidence.DOCUMENT}
        if type(self).multi_modal_mode == "null":
            document["multi_modal"] = None
        elif type(self).multi_modal_mode == "non_null":
            document["multi_modal"] = {}
        envelope: dict[str, object] = {
            "id": type(self).response_id_override or evidence.RESPONSE_ID,
            "model": evidence.SERVED_NAME,
            "results": [
                {
                    "document": document,
                    "index": 0,
                    "relevance_score": "__RAW_SCORE__",
                }
            ],
            "usage": {"prompt_tokens": 64, "total_tokens": 64},
        }
        if type(self).extra_top_level:
            envelope["created"] = 1
        text = json.dumps(envelope, sort_keys=True, separators=(",", ":"))
        text = text.replace('"__RAW_SCORE__"', type(self).score_lexeme)
        self.respond((text + "\n").encode("utf-8"), "application/json")

    def respond(self, raw: bytes, content_type: str) -> None:
        manifest = type(self).manifest
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header(
            evidence.DEPLOYMENT_HEADER,
            manifest["runtime"]["deployment_revision"],
        )
        self.send_header(evidence.MANIFEST_HEADER, type(self).manifest_sha)
        if type(self).duplicate_header:
            self.send_header(evidence.MANIFEST_HEADER, type(self).manifest_sha)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, _format: str, *_args: object) -> None:
        return


@contextmanager
def fake_server(manifest: dict[str, object], manifest_sha: str):
    handler = FakeNativeRerankHandler
    handler.calls = []
    handler.request_raw = b""
    handler.request_id = None
    handler.manifest = manifest
    handler.manifest_sha = manifest_sha
    handler.duplicate_header = False
    handler.redirect_first = False
    handler.version_override = None
    handler.cache_enabled_override = None
    handler.pre_queries = 10
    handler.pre_hits = 4
    handler.post_queries = 11
    handler.post_hits = 5
    handler.metrics_calls = 0
    handler.multi_modal_mode = "null"
    handler.score_lexeme = "0.75"
    handler.response_id_override = None
    handler.extra_top_level = False
    server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        host, port = server.server_address
        yield f"http://{host}:{port}"
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


def two_ticks():
    values = iter(
        [
            datetime(2000, 1, 1, tzinfo=timezone.utc),
            datetime(2000, 1, 1, 0, 0, 1, tzinfo=timezone.utc),
        ]
    )
    return lambda: next(values)


class E2NativeRerankerEvidenceV2Test(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.bundle = evidence.V2ContractBundle.load()
        cls.artifacts = cls.bundle.positive_artifacts()

    def manifest_bytes(self) -> tuple[dict[str, object], bytes]:
        manifest = copy.deepcopy(self.artifacts["deployment_manifest"])
        return manifest, evidence.canonical_bytes(manifest, stage="test")

    def test_contract_closure_dynamically_runs_every_case_and_inventory(self) -> None:
        self.assertEqual(len(self.bundle.cases), self.bundle.closure["case_count"])
        self.assertEqual(
            {case["file"] for case in self.bundle.cases},
            {
                path.relative_to(self.bundle.conformance_dir).as_posix()
                for path in (self.bundle.conformance_dir / "fixtures" / "cases").glob("*.json")
            },
        )
        pinned = self.bundle.closure["pinned_artifacts"]
        self.assertEqual(len(pinned), self.bundle.closure["pinned_artifact_count"])
        self.assertEqual(set(self.bundle.pinned_raw), {entry["path"] for entry in pinned})

    def test_root_pin_precedes_manifest_decode(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / "conformance/v2/manifest.json"
            path.parent.mkdir(parents=True)
            path.write_bytes(b"{not-json")
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.V2ContractBundle.load(root)
        self.assertEqual(raised.exception.code, "external_conformance_pin_mismatch")

    def test_root_inventory_rejects_unpinned_file(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            copied = Path(directory) / "contract"
            shutil.copytree(self.bundle.root, copied)
            (copied / "unclassified.txt").write_text("drift")
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.V2ContractBundle.load(copied)
        self.assertEqual(raised.exception.code, "root_inventory_mismatch")

    def test_pinned_artifact_drift_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            copied = Path(directory) / "contract"
            shutil.copytree(self.bundle.root, copied)
            (copied / "contract.md").write_bytes((copied / "contract.md").read_bytes() + b" ")
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.V2ContractBundle.load(copied)
        self.assertEqual(raised.exception.code, "pinned_sha256_mismatch")

    def test_external_four_pins_precede_any_artifact_decode(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            manifest = Path(directory) / "manifest.json"
            receipt = Path(directory) / "receipt.json"
            manifest_raw = b"{malformed-manifest"
            receipt_raw = b"{malformed-receipt"
            manifest.write_bytes(manifest_raw)
            receipt.write_bytes(receipt_raw)
            with mock.patch.object(
                evidence,
                "strict_canonical_bytes",
                side_effect=AssertionError("decode must not run"),
            ), self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.verify_external_pair(
                    self.bundle,
                    manifest,
                    receipt,
                    manifest_sha256="0" * 64,
                    manifest_size=len(manifest_raw),
                    receipt_sha256="1" * 64,
                    receipt_size=len(receipt_raw),
                )
            self.assertEqual(raised.exception.code, "external_manifest_pin_mismatch")
            with mock.patch.object(
                evidence,
                "strict_canonical_bytes",
                side_effect=AssertionError("decode must not run"),
            ), self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.verify_external_pair(
                    self.bundle,
                    manifest,
                    receipt,
                    manifest_sha256=hashlib.sha256(manifest_raw).hexdigest(),
                    manifest_size=len(manifest_raw),
                    receipt_sha256="1" * 64,
                    receipt_size=len(receipt_raw),
                )
            self.assertEqual(raised.exception.code, "external_receipt_pin_mismatch")

    def test_external_synthetic_pair_is_never_promoted(self) -> None:
        manifest_raw = evidence.canonical_bytes(
            self.artifacts["deployment_manifest"], stage="test"
        )
        receipt_raw = evidence.canonical_bytes(
            self.artifacts["synthetic_rerank_receipt"], stage="test"
        )
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

    def test_startup_argv_requires_exact_flags_values_and_order(self) -> None:
        manifest = copy.deepcopy(self.artifacts["deployment_manifest"])
        argv = manifest["serving"]["startup_argv"]
        index = argv.index("--hf-overrides")
        del argv[index : index + 2]
        with self.assertRaises(evidence.EvidenceFailure) as raised:
            self.bundle.validate_manifest(manifest, allow_synthetic=True)
        self.assertEqual(raised.exception.code, "startup_argv_required_flag_missing")

        manifest = copy.deepcopy(self.artifacts["deployment_manifest"])
        argv = manifest["serving"]["startup_argv"]
        index = argv.index("--hf-overrides") + 1
        argv[index] = argv[index].replace("single_label_classification", "regression")
        with self.assertRaises(evidence.EvidenceFailure) as raised:
            self.bundle.validate_manifest(manifest, allow_synthetic=True)
        self.assertEqual(raised.exception.code, "startup_argv_hf_overrides_mismatch")

        manifest = copy.deepcopy(self.artifacts["deployment_manifest"])
        argv = manifest["serving"]["startup_argv"]
        argv[-2], argv[-1] = argv[-1], argv[-2]
        with self.assertRaises(evidence.EvidenceFailure) as raised:
            self.bundle.validate_manifest(manifest, allow_synthetic=True)
        self.assertEqual(raised.exception.code, "startup_argv_order_mismatch")

    def test_catalog_builder_attests_full_two_pass_snapshot(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "model.bin").write_bytes(b"model")
            (root / "tokenizer.json").write_bytes(b"tokenizer")
            catalog = evidence.build_artifact_catalog(
                self.bundle,
                root,
                {"model.bin": "model", "tokenizer.json": "tokenizer"},
            )
        self.assertEqual(catalog["snapshot_root"], str(root.resolve()))
        self.assertEqual(catalog["file_count"], len(catalog["files"]))
        self.assertTrue(catalog["inventory_attestation"]["open_nofollow"])
        self.assertTrue(catalog["inventory_attestation"]["pre_post_inventory_equal"])

    def test_catalog_builder_rejects_snapshot_root_symlink(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory)
            root = parent / "real"
            root.mkdir()
            (root / "model.bin").write_bytes(b"model")
            (root / "tokenizer.json").write_bytes(b"tokenizer")
            link = parent / "snapshot"
            link.symlink_to(root, target_is_directory=True)
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                evidence.build_artifact_catalog(
                    self.bundle,
                    link,
                    {"model.bin": "model", "tokenizer.json": "tokenizer"},
                )
        self.assertEqual(raised.exception.code, "snapshot_root_invalid")

    def test_native_loopback_uses_exact_nine_steps_and_one_post(self) -> None:
        manifest, manifest_raw = self.manifest_bytes()
        manifest_sha = evidence.sha256_bytes(manifest_raw)
        with fake_server(manifest, manifest_sha) as origin, mock.patch.dict(
            os.environ,
            {"HTTP_PROXY": "http://127.0.0.1:1", "HTTPS_PROXY": "http://127.0.0.1:1"},
        ):
            client = smoke.SyntheticNativeRerankClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            receipt_raw = client.run(manifest, manifest_raw)
        receipt = evidence.strict_canonical_bytes(receipt_raw, stage="test")
        self.assertEqual(client.actual_order, receipt["observation_order"])
        self.assertEqual(
            [(method, path) for method, path in FakeNativeRerankHandler.calls if method == "POST"],
            [("POST", "/v1/rerank")],
        )
        self.assertEqual(
            FakeNativeRerankHandler.request_raw,
            evidence.canonical_bytes(receipt["rerank"]["request"]["body"], stage="test"),
        )
        self.assertEqual(FakeNativeRerankHandler.request_id, evidence.REQUEST_ID)
        document = receipt["rerank"]["response"]["projection"]["results"][0]["document"]
        self.assertEqual(document, {"multi_modal": None, "text": evidence.DOCUMENT})
        self.assertTrue(receipt["preflight"]["metrics"]["projection"]["prefix_cache_enabled"])
        self.assertEqual(
            self.bundle.evaluate_pair(
                {
                    "deployment_manifest": manifest,
                    "synthetic_rerank_receipt": receipt,
                },
                allow_synthetic=True,
            ),
            ("accept_synthetic_fixture", "accepted", "none"),
        )

    def test_cache_enabled_zero_hits_is_not_a_failure(self) -> None:
        manifest, manifest_raw = self.manifest_bytes()
        with fake_server(manifest, evidence.sha256_bytes(manifest_raw)) as origin:
            FakeNativeRerankHandler.pre_hits = 0
            FakeNativeRerankHandler.post_hits = 0
            client = smoke.SyntheticNativeRerankClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            receipt_raw = client.run(manifest, manifest_raw)
        receipt = evidence.strict_canonical_bytes(receipt_raw, stage="test")
        self.assertTrue(receipt["preflight"]["metrics"]["projection"]["prefix_cache_enabled"])
        self.assertEqual(receipt["postflight"]["metrics"]["projection"]["prefix_cache_hits_total"], 0)

    def test_duplicate_preflight_header_fails_before_post(self) -> None:
        manifest, manifest_raw = self.manifest_bytes()
        with fake_server(manifest, evidence.sha256_bytes(manifest_raw)) as origin:
            FakeNativeRerankHandler.duplicate_header = True
            client = smoke.SyntheticNativeRerankClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                client.run(manifest, manifest_raw)
        self.assertEqual(raised.exception.code, "live_header_cardinality_invalid")
        self.assertFalse(any(method == "POST" for method, _path in FakeNativeRerankHandler.calls))

    def test_preflight_identity_or_cache_drift_fails_before_post(self) -> None:
        for drift in ("version", "cache"):
            manifest, manifest_raw = self.manifest_bytes()
            with self.subTest(drift=drift), fake_server(
                manifest, evidence.sha256_bytes(manifest_raw)
            ) as origin:
                if drift == "version":
                    FakeNativeRerankHandler.version_override = "9.9.9"
                else:
                    FakeNativeRerankHandler.cache_enabled_override = False
                client = smoke.SyntheticNativeRerankClient(
                    self.bundle, origin=origin, timeout=1, clock=two_ticks()
                )
                with self.assertRaises(evidence.EvidenceFailure):
                    client.run(manifest, manifest_raw)
            self.assertFalse(
                any(method == "POST" for method, _path in FakeNativeRerankHandler.calls)
            )

    def test_redirect_is_not_followed_or_retried(self) -> None:
        manifest, manifest_raw = self.manifest_bytes()
        with fake_server(manifest, evidence.sha256_bytes(manifest_raw)) as origin:
            FakeNativeRerankHandler.redirect_first = True
            client = smoke.SyntheticNativeRerankClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                client.run(manifest, manifest_raw)
        self.assertEqual(raised.exception.code, "redirect_forbidden")
        self.assertEqual(len(FakeNativeRerankHandler.calls), 1)

    def test_hostname_and_non_loopback_are_rejected_without_dns(self) -> None:
        for origin, code in (
            ("http://localhost:8000", "dns_forbidden"),
            ("http://192.0.2.1:8000", "non_loopback_forbidden"),
        ):
            with self.subTest(origin=origin), self.assertRaises(
                evidence.EvidenceFailure
            ) as raised:
                smoke.SyntheticNativeRerankClient(
                    self.bundle, origin=origin, timeout=1
                )
            self.assertEqual(raised.exception.code, code)

    def test_raw_multi_modal_must_be_present_and_null(self) -> None:
        for mode in ("missing", "non_null"):
            manifest, manifest_raw = self.manifest_bytes()
            with self.subTest(mode=mode), fake_server(
                manifest, evidence.sha256_bytes(manifest_raw)
            ) as origin:
                FakeNativeRerankHandler.multi_modal_mode = mode
                client = smoke.SyntheticNativeRerankClient(
                    self.bundle, origin=origin, timeout=1, clock=two_ticks()
                )
                with self.assertRaises(evidence.EvidenceFailure):
                    client.run(manifest, manifest_raw)
            self.assertEqual(
                sum(method == "POST" for method, _path in FakeNativeRerankHandler.calls), 1
            )

    def test_raw_unknown_top_level_field_is_rejected(self) -> None:
        manifest, manifest_raw = self.manifest_bytes()
        with fake_server(manifest, evidence.sha256_bytes(manifest_raw)) as origin:
            FakeNativeRerankHandler.extra_top_level = True
            client = smoke.SyntheticNativeRerankClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                client.run(manifest, manifest_raw)
        self.assertEqual(raised.exception.code, "raw_response_unknown_field")

    def test_score_out_of_range_fails_without_retry(self) -> None:
        manifest, manifest_raw = self.manifest_bytes()
        with fake_server(manifest, evidence.sha256_bytes(manifest_raw)) as origin:
            FakeNativeRerankHandler.score_lexeme = "1.1"
            client = smoke.SyntheticNativeRerankClient(
                self.bundle, origin=origin, timeout=1, clock=two_ticks()
            )
            with self.assertRaises(evidence.EvidenceFailure) as raised:
                client.run(manifest, manifest_raw)
        self.assertEqual(raised.exception.code, "score_out_of_range")
        self.assertEqual(
            sum(method == "POST" for method, _path in FakeNativeRerankHandler.calls), 1
        )


if __name__ == "__main__":
    unittest.main()
