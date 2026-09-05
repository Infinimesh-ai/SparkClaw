"""CPU-only tests. All request text and HTTP responses below are synthetic."""
import base64
import copy
from decimal import Decimal
from fractions import Fraction
import json
from pathlib import Path
import struct
import tempfile
import types
import unittest
from unittest.mock import patch

from scripts import e2_set_formal_native_v1 as m
from scripts import e2_set_tokenizer_v1 as tokenizer


def backend():
    return {"container_id": "a" * 64, "image_id": m.IMAGE, "image_reference": m.IMAGE,
        "started_at": "2026-09-05T00:00:00Z", "entrypoint": ["vllm"],
        "cmd": ["serve", "Qwen/Qwen3-Reranker-4B", "--no-enable-prefix-caching", "--enforce-eager",
                "--tensor-parallel-size", "1", "--max-num-seqs", "1", "--max-model-len", "8192", "--dtype", "bfloat16"],
        "user": "1000:1000", "readonly_rootfs": True, "cap_drop": ["ALL"],
        "security_opt": ["no-new-privileges:true"], "mounts": [], "networks": ["isolated"], "set_tokenizer_profile": m.PROFILE}


def synthetic_artifacts():
    cells, cases = [], []
    for ordinal in range(m.COUNT):
        depth = ordinal % 32 + 1
        cell_id = f"calibration.{ordinal // 32 + 1:04d}.depth.{depth:02d}"
        query = f"Synthetic unit query {ordinal // 32} with < & \u2028 \u2029"
        wire = tokenizer.encode_documents([f"Synthetic unit candidate {n}" for n in range(depth)])
        body = m.request_body(query, wire, tokenizer.INSTRUCTION)
        cells.append({"ordinal": ordinal, "cell_id": cell_id, "query_id": f"unit-{ordinal // 32}", "depth": depth,
            "prefix_ids": [f"unit-document-{n}" for n in range(depth)], "input_commitment_sha256": "d" * 64,
            "body": body, "body_pin": m.pin(m.canonical(body))})
        values = [tokenizer.PREFIX, tokenizer.BODY_PREFIX, query, tokenizer.BODY_MIDDLE, wire, tokenizer.SUFFIX]
        segments = [{"name": name, "trusted": i not in (2, 4), "utf8": value, "token_ids": [i + 1]} for i, (name, value) in enumerate(zip(("prefix", "body_prefix", "query", "body_middle", "document", "suffix"), values))]
        rendered = "".join(values)
        cases.append({"id": cell_id, "kind": "ordered_set", "rendered_utf8": rendered,
            "rendered_sha256": m.pin(rendered.encode())["sha256"], "rendered_size_bytes": len(rendered.encode()),
            "segments": segments, "token_ids": list(range(1, 7)), "token_count": 6, "untrusted_control_ids": []})
    inputs = m.envelope("cell-inputs", family="calibration", cell_count=m.COUNT,
        source_pins=[{"role": "unit-synthetic-source", "path": "unit.json", **m.pin(b"unit\n")}], provenance=m.PROVENANCE, cells=cells)
    catalog = copy.deepcopy(inputs)
    catalog["artifact"] = "imms-gb10-native-set-cell-catalog"
    for cell in catalog["cells"]:
        cell.update(token_count=6, token_ids=list(range(1, 7)), token_ids_sha256=m.token_sha(list(range(1, 7))))
    reference = m.envelope("reference-tokens", input_pin=m.pin(m.canonical(inputs)), reference_source_pin=m.REFERENCE_SOURCE_PIN,
        tokenizer_source_pin=m.TOKENIZER_PIN, provenance=m.PROVENANCE, cases=cases)
    return inputs, catalog, reference


class Fixture:
    def __init__(self, root):
        self.root = root
        self.inputs, self.catalog, self.reference = synthetic_artifacts()
        self.ca = b"unit certificate placeholder\n"
        self.source_files = [{"path": name, **m.pin((Path(m.__file__).parent / name).read_bytes())} for name in ("__init__.py", "e2_set_formal_native_v1.py", "e2_set_tokenizer_v1.py")]
        self.deployment = m.envelope("deployment", container=m.CONTAINER, origin=m.ORIGIN, backend_port=m.BACKEND_PORT,
            profile=m.PROFILE, backend=backend(), model_revision=m.REVISION,
            model_files=[{"path": path, "sha256": "b" * 64, "size_bytes": 123} for path in m.MODEL_PATHS],
            runtime_versions=m.RUNTIME_VERSIONS, tls_ca_pin=m.pin(self.ca),
            transport_source_pin={k: self.source_files[1][k] for k in ("sha256", "size_bytes")}, provenance=m.PROVENANCE)
        self.plans = [{"phase": phase, "expected_posts": m.COUNT, "requests": [
            {"ordinal": c["ordinal"], "cell_id": c["cell_id"], "request_id": f'unit-{phase}-{c["ordinal"]}'} for c in self.catalog["cells"]]} for phase in m.PHASES]

    def write(self, *, phase="calibration-01", previous=None, mutation=None, input_raw=None):
        raw_inputs = input_raw if input_raw is not None else m.canonical(self.inputs)
        self.reference["input_pin"] = m.pin(raw_inputs)
        objects = {"inputs": raw_inputs, "catalog": m.canonical(self.catalog), "reference_tokens": m.canonical(self.reference),
                   "deployment_record": m.canonical(self.deployment), "ca_file": self.ca}
        for key, raw in objects.items():
            (self.root / key).write_bytes(raw)
        p = m.envelope("admission-prereg", input_pin=m.pin(objects["inputs"]), catalog_pin=m.pin(objects["catalog"]),
            reference_tokens_pin=m.pin(objects["reference_tokens"]), deployment_record_pin=m.pin(objects["deployment_record"]),
            ca_pin=m.pin(self.ca), source_files=self.source_files, score_rule=m.SCORE_RULE, request_plans=self.plans, provenance=m.PROVENANCE)
        if mutation:
            mutation(p)
        raw_prereg = m.canonical(p)
        (self.root / "prereg").write_bytes(raw_prereg)
        i = m.envelope("phase-intent", phase=phase, prereg_pin=m.pin(raw_prereg), prereg_commit="c" * 40,
            previous_phase_terminal=previous, provenance=m.PROVENANCE)
        raw_intent = m.canonical(i)
        (self.root / "intent").write_bytes(raw_intent)
        return p, m.pin(raw_prereg), m.pin(raw_intent)

    def plan(self, **kwargs):
        p, ppin, ipin = self.write(**kwargs)
        return m.Plan(self.root / "prereg", ppin["sha256"], ppin["size_bytes"],
            **{name: self.root / name for name in ("inputs", "catalog", "reference_tokens", "deployment_record", "ca_file")},
            intent=self.root / "intent", intent_sha256=ipin["sha256"], intent_size=ipin["size_bytes"])


class PureTests(unittest.TestCase):
    def reject(self, code, call):
        with self.assertRaises(m.AdmissionError) as error:
            call()
        self.assertEqual(error.exception.code, code)

    def test_raw_decimal_direct_rne_avoids_double_rounding(self):
        # Halfway between 1/2 and its next binary32, plus a decimal amount
        # smaller than a binary64 ULP: binary64 would erase that difference.
        midpoint = Decimal("0.5000000298023223876953125")
        above = str(midpoint + Decimal("0.0000000000000000000000001"))
        below = str(midpoint - Decimal("0.0000000000000000000000001"))
        self.assertEqual(m.round_score(str(midpoint))["score_float32_bits"], "3f000000")
        self.assertEqual(m.round_score(above)["score_float32_bits"], "3f000001")
        self.assertEqual(m.round_score(below)["score_float32_bits"], "3f000000")
        self.assertEqual(struct.pack(">f", float(above)).hex(), "3f000000")

    def test_exact_expansion_and_negative_zero(self):
        self.assertEqual(m.round_score("0.1")["score_float32_decimal"], "0.100000001490116119384765625")
        self.assertEqual(m.round_score("-0")["score_float32_decimal"], "-0")
        self.assertEqual(m.round_score("1e-999999999")["score_float32_bits"], "00000000")
        self.assertEqual(m.round_score("1")["score_float32_bits"], "3f800000")

    def test_subnormal_halfway_and_even_tie(self):
        from decimal import localcontext
        with localcontext() as context:
            context.prec = 180
            half = Decimal(2) ** -150
            self.assertEqual(m.round_score(str(half))["score_float32_bits"], "00000000")
            self.assertEqual(m.round_score(str(3 * half))["score_float32_bits"], "00000002")

    def test_score_rejections(self):
        for value in ("NaN", "Infinity", "1.0001", "-0.0001", "+0.1", "01", "0x1", "1e999999999", "0." + "1" * 128):
            with self.subTest(value=value):
                self.reject("response_score", lambda: m.round_score(value))

    def test_strict_json(self):
        for value in (b'{"a":1,"a":2}', b'{"a":NaN}', b'{"a":1e999}', b'\xef\xbb\xbf{}', b'{"a":"\\ud800"}'):
            with self.assertRaises(m.AdmissionError):
                m.decode(value)

    def test_noncanonical_pinned_json_is_accepted(self):
        raw = b'{"version":1,"artifact":"unit","x":"< & \xe2\x80\xa8"}\n'
        self.assertEqual(m.decode(raw)["version"], 1)
        self.reject("json_noncanonical", lambda: m.decode(raw, canonical_required=True))

    def test_bool_is_not_integer(self):
        self.assertFalse(m.integer(True))
        self.reject("token_ids", lambda: m.token_sha([True]))
        self.reject("pin_invalid", lambda: m.valid_pin({"sha256": "a" * 64, "size_bytes": True}))

    def test_nofollow_parent_and_final_and_hardlink(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "real").mkdir()
            (root / "real/file").write_bytes(b"ok")
            (root / "parent-link").symlink_to(root / "real", target_is_directory=True)
            (root / "file-link").symlink_to(root / "real/file")
            for path in (root / "parent-link/file", root / "file-link"):
                self.reject("pin_io", lambda: m.read_pin(path, m.pin(b"ok")))
            self.reject("durable_write", lambda: m.durable_new(root / "parent-link/new", b"ok"))
            import os
            os.link(root / "real/file", root / "hard")
            self.reject("pin_file", lambda: m.read_pin(root / "hard", m.pin(b"ok")))

    def test_pin_before_decode_and_create_only(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "file"
            path.write_bytes(b'{"duplicate":1,"duplicate":2}')
            self.reject("pin_file", lambda: m.read_pin(path, m.pin(b"{}")))
            new = Path(temporary) / "new"
            m.durable_new(new, b"first")
            self.reject("durable_write", lambda: m.durable_new(new, b"second"))
            self.assertEqual(new.read_bytes(), b"first")

    def test_metrics_require_exact_series_and_monotone_domain(self):
        raw = metrics(4, 2)
        self.assertEqual(m.native_counters(raw), {"prefix_cache_queries_total": 4, "prefix_cache_hits_total": 2})
        for bad in (raw + raw, raw.replace(b'engine="0"', b'engine="1"'), metrics(1, 2), raw.splitlines()[0] + b"\n", raw.replace(b" 4\n", b" NaN\n")):
            self.reject("metrics_invalid", lambda: m.native_counters(bad))

    def test_reply_requires_numeric_lexeme_echo_and_usage(self):
        entry = {"request_id": "unit", "token_count": 6, "body": {"documents": ["unit document"]}}
        good = reply(entry)
        self.assertEqual(m.validate_reply(entry, good)["score_decimal_lexeme"], "0.1")
        for old, new in ((b'0.1', b'"0.1"'), (b'"total_tokens":6', b'"total_tokens":6.0'), (b'"index":0', b'"index":false'), (b'unit document', b'wrong document')):
            with self.assertRaises(m.AdmissionError):
                m.validate_reply(entry, good.replace(old, new))

    def test_header_duplicates_rejected(self):
        self.reject("duplicate_header", lambda: m.headers_unique([("X-Request-Id", "a"), ("x-request-id", "a")]))


class PlanTests(unittest.TestCase):
    reject = PureTests.reject
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.fixture = Fixture(Path(self.temporary.name))

    def test_complete_two_phase_640_plan_and_go_order(self):
        raw = (json.dumps(self.fixture.inputs, ensure_ascii=False, separators=(",", ":")) + "\n").encode()
        plan = self.fixture.plan(input_raw=raw)
        self.assertEqual(len(plan.requests), 640)
        self.assertNotEqual(raw, m.canonical(self.fixture.inputs))

    def test_model_catalog_real_nested_names_and_bad_paths(self):
        self.fixture.plan()
        for path in ("../config.json", "serving/../config.json", "extra/file", ".gitattributes"):
            self.fixture.deployment["model_files"][1]["path"] = path
            self.reject("model_catalog", self.fixture.plan)

    def test_source_pin_self_and_dependency(self):
        self.fixture.source_files[1]["sha256"] = "0" * 64
        self.reject("pin_mismatch", self.fixture.plan)

    def test_empty_init_only(self):
        self.assertEqual(self.fixture.source_files[0]["size_bytes"], 0)
        self.fixture.source_files[1]["size_bytes"] = 0
        self.reject("pin_invalid", self.fixture.plan)

    def test_global_request_id_uniqueness(self):
        self.fixture.plans[1]["requests"][0]["request_id"] = self.fixture.plans[0]["requests"][0]["request_id"]
        self.reject("request_plan", self.fixture.plan)

    def test_exact_640_not_partial_plan(self):
        self.fixture.plans[0]["requests"].pop()
        self.reject("request_plans", self.fixture.plan)

    def test_reference_entire_array_not_count(self):
        self.fixture.reference["cases"][23]["token_ids"][0] = 8
        self.reject("reference_tokens", self.fixture.plan)

    def test_untrusted_control_and_segment_consistency(self):
        self.fixture.reference["cases"][23]["segments"][2]["token_ids"] = [151643]
        self.reject("untrusted_control", self.fixture.plan)

    def test_provenance_unknown_cannot_be_dropped(self):
        self.reject("provenance", lambda: self.fixture.plan(mutation=lambda p: p.update(provenance={"static_access": "untouched"})))

    def test_second_phase_requires_committed_previous_success(self):
        self.reject("phase_predecessor", lambda: self.fixture.plan(phase="calibration-02", previous={"pin": m.pin(b"unit"), "commit": "c" * 40}))

    def test_deployment_cannot_enable_prefix_cache(self):
        self.fixture.deployment["backend"]["cmd"].append("--enable-prefix-caching")
        self.reject("backend_command", self.fixture.plan)

    def test_raw_capability_document_rejected(self):
        for artifact in (self.fixture.inputs, self.fixture.catalog):
            artifact["cells"][0]["body"]["documents"] = ["unit raw text"]
        with self.assertRaises(m.AdmissionError):
            self.fixture.plan()


def metrics(queries=0, hits=0):
    return (f'vllm:prefix_cache_queries_total{{engine="0",model_name="sparkclaw-reranker"}} {queries}\n'
            f'vllm:prefix_cache_hits_total{{engine="0",model_name="sparkclaw-reranker"}} {hits}\n').encode()


def reply(entry):
    return m.canonical({"id": "score-" + entry["request_id"], "model": "sparkclaw-reranker",
        "results": [{"index": 0, "document": {"text": entry["body"]["documents"][0], "multi_modal": None}, "relevance_score": 0.1}],
        "usage": {"prompt_tokens": entry["token_count"], "total_tokens": entry["token_count"]}})


class ExecutionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        root = Path(self.temporary.name)
        self.plan = Fixture(root).plan()
        self.plan.runtime, self.plan.output = root / "runtime", root / "output"
        self.plan.runtime.mkdir()
        self.backend_checks = 0
        def verified():
            self.backend_checks += 1
            return backend()
        self.plan.verify_backend = verified
        self.native_posts, self.gets = 0, []
        self.state = m.BoundaryState(self.plan)
        self.tls = {"version": "TLSv1.3", "cipher": "TLS_AES_256_GCM_SHA384", "peer_certificate_sha256": m.pin(b"unit DER")["sha256"]}

    def fake_http(self, method, path, body, headers, *, port, context=None):
        if port == m.BACKEND_PORT:
            if method == "POST":
                entry = self.plan.requests[self.native_posts]
                self.native_posts += 1
                raw, content_type = reply(entry), "application/json"
            else:
                self.gets.append(path)
                raw = {"/metrics": metrics(self.native_posts), "/health": b"", "/version": b'{"version":"0.23.0"}',
                    "/v1/models": m.canonical({"data": [{"id": "sparkclaw-reranker", "root": "Qwen/Qwen3-Reranker-4B", "owned_by": "vllm", "max_model_len": 8192}]})}[path]
                content_type = "application/json"
            return 200, [("Content-Type", content_type), ("Content-Length", str(len(raw)))], raw, None
        sent = [("Host", "127.0.0.1:19483"), *headers.items()]
        if body is not None:
            sent.append(("Content-Length", str(len(body))))
        status, returned, raw = self.state.forward(method, path, sent, body or b"")
        return status, [*returned.items(), ("Content-Length", str(len(raw)))], raw, self.tls

    def collector(self):
        with patch.object(m.ssl, "SSLContext"), patch.object(m.ssl, "PEM_cert_to_DER_cert", return_value=b"unit DER"):
            return m.Collector(self.plan)

    def test_complete640_dual_journals_and_641_observations(self):
        collector = self.collector()
        with patch.object(m, "http_once", self.fake_http):
            result = collector.run()
        self.assertEqual(self.native_posts, 640)
        self.assertEqual(self.gets.count("/metrics"), 641)
        self.assertEqual(len(result["observation_pins"]["metrics"]), 641)
        self.assertEqual(self.gets.count("/health"), 2)
        self.assertGreaterEqual(self.backend_checks, 1280)
        for role in ("provider", "collector"):
            ledger = self.plan.runtime / (role + "-ledger")
            terminal = m.decode((ledger / "terminal.json").read_bytes())
            self.assertEqual((terminal["completed_posts"], terminal["consumed_posts"], terminal["status"]), (640, 640, "success"))
            self.assertEqual(len(list(ledger.glob("*-terminal.json"))), 640)
            with self.assertRaises(m.AdmissionError):
                m.Ledger(self.plan, role)
        first = m.decode((self.plan.runtime / "collector-ledger/0000-result.json").read_bytes())
        second = m.decode((self.plan.runtime / "collector-ledger/0001-result.json").read_bytes())
        self.assertEqual(first["cache_after"], second["cache_before"])
        self.assertEqual(m.pin(base64.b64decode(first["raw_response_base64"])), first["raw_response_pin"])
        self.assertEqual(first["score_float32_bits"], "3dcccccd")

    def test_failure_consumed_no_retry_and_unknown_pending(self):
        collector = self.collector()
        original = self.fake_http
        def fail_native(method, path, body, headers, **kwargs):
            if kwargs["port"] == m.BACKEND_PORT and method == "POST":
                self.native_posts += 1
                raise RuntimeError("private response must never leak")
            return original(method, path, body, headers, **kwargs)
        with patch.object(m, "http_once", fail_native):
            with self.assertRaises(m.AdmissionError):
                collector.run()
            with self.assertRaises(m.AdmissionError):
                collector.run()
        self.assertEqual(self.native_posts, 1)
        for role in ("provider", "collector"):
            root = self.plan.runtime / (role + "-ledger")
            terminal = m.decode((root / "terminal.json").read_bytes())
            self.assertEqual(terminal["status"], "failed")
            self.assertEqual(terminal["completed_posts"], 0)
            self.assertEqual(terminal["consumed_posts"], 1)
            self.assertFalse(terminal["count_known"])
            self.assertNotIn("private", (root / "terminal.json").read_text())
            self.assertIsNone(m.decode((root / "0000-terminal.json").read_bytes())["result_pin"])

    def test_provider_rejects_wrong_id_before_backend_io(self):
        entry = self.plan.requests[0]
        body = m.canonical(entry["body"])
        headers = [("Host", "127.0.0.1:19483"), ("Content-Type", "application/json"), ("Content-Length", str(len(body))), ("X-Request-Id", "wrong")]
        with patch.object(m, "http_once", self.fake_http), self.assertRaises(m.AdmissionError):
            self.state.forward("POST", "/v1/rerank", headers, body)
        self.assertEqual(self.native_posts, 0)
        self.assertTrue(self.state.ledger.ended)

    def test_durable_admission_precedes_backend_connection(self):
        entry = self.plan.requests[0]
        body = m.canonical(entry["body"])
        headers = [("Host", "127.0.0.1:19483"), ("Content-Type", "application/json"), ("Content-Length", str(len(body))), ("X-Request-Id", entry["request_id"])]
        def inspect_before_io(*args, **kwargs):
            self.assertTrue((self.plan.runtime / "provider-ledger/0000-admission.json").exists())
            return self.fake_http(*args, **kwargs)
        with patch.object(m, "http_once", inspect_before_io):
            self.state.forward("POST", "/v1/rerank", headers, body)
            with self.assertRaises(m.AdmissionError):
                self.state.forward("POST", "/v1/rerank", headers, body)
        self.assertEqual(self.native_posts, 1)

    def test_no_querystring_get_or_nonzero_body(self):
        with patch.object(m, "http_once", self.fake_http), self.assertRaises(m.AdmissionError):
            self.state.forward("GET", "/health?x=1", [("Host", "127.0.0.1:19483")], b"")
        self.assertEqual(self.gets, [])

    def test_failed_native_reply_is_preserved_without_score(self):
        original = self.fake_http
        rejected = None
        def invalid(method, path, body, headers, **kwargs):
            nonlocal rejected
            status, returned, raw, tls = original(method, path, body, headers, **kwargs)
            if kwargs["port"] == m.BACKEND_PORT and method == "POST":
                raw = raw.replace(b'"relevance_score":0.1', b'"relevance_score":null')
                rejected = raw
            return status, returned, raw, tls
        with patch.object(m, "http_once", invalid), self.assertRaises(m.AdmissionError):
            self.collector().run()
        failure = m.decode((self.plan.runtime / "provider-ledger/failure-response.json").read_bytes())
        self.assertEqual(base64.b64decode(failure["raw_body_base64"]), rejected)
        self.assertEqual(failure["raw_body_pin"], m.pin(rejected))
        self.assertIsNone(failure["tls"])
        self.assertFalse(any("score" in key for key in failure))
        self.assertEqual(self.native_posts, 1)

    def test_failed_collector_tls_identity_retains_raw_no_post(self):
        self.tls["peer_certificate_sha256"] = "0" * 64
        collector = self.collector()
        with patch.object(m, "http_once", self.fake_http), self.assertRaises(m.AdmissionError):
            collector.run()
        failure = m.decode((self.plan.runtime / "collector-ledger/failure-response.json").read_bytes())
        self.assertEqual(failure["tls"]["peer_certificate_sha256"], "0" * 64)
        self.assertEqual(failure["http_status"], 200)
        self.assertEqual(self.native_posts, 0)

    def test_normal_stop_after_durable_success_returns_zero(self):
        arguments = types.SimpleNamespace(mode="collect", prereg="unit", expected_sha256="0" * 64,
            expected_size=1, inputs="unit", catalog="unit", reference_tokens="unit", deployment_record="unit",
            ca_file="unit", intent="unit", intent_sha256="0" * 64, intent_size=1, previous_terminal=None)
        state = types.SimpleNamespace(ledger=types.SimpleNamespace(success=True), run=lambda: m.fail("process_interrupted"))
        with patch.object(m.argparse.ArgumentParser, "parse_args", return_value=arguments), patch.object(m, "Plan", return_value=self.plan), patch.object(m, "Collector", return_value=state), patch.object(m.signal, "signal"), patch("builtins.print"):
            self.assertEqual(m.main(), 0)

    def test_admission_write_failure_never_opens_backend(self):
        entry = self.plan.requests[0]
        body = m.canonical(entry["body"])
        headers = [("Host", "127.0.0.1:19483"), ("Content-Type", "application/json"), ("Content-Length", str(len(body))), ("X-Request-Id", entry["request_id"])]
        m.durable_new(self.plan.runtime / "provider-ledger/0000-admission.json", b"preexisting consumed record")
        with patch.object(m, "http_once", self.fake_http), self.assertRaises(m.AdmissionError):
            self.state.forward("POST", "/v1/rerank", headers, body)
        self.assertEqual(self.native_posts, 0)
        terminal = m.decode((self.plan.runtime / "provider-ledger/terminal.json").read_bytes())
        self.assertEqual(terminal["status"], "failed")
        self.assertFalse(terminal["count_known"])


if __name__ == "__main__":
    unittest.main()
