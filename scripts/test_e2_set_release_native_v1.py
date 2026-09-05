"""CPU-only, entirely synthetic release admission and one-shot transport tests."""
import base64
import copy
import json
import os
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

from scripts import e2_set_release_native_v1 as m
from scripts import e2_set_formal_native_v1 as h
from scripts import e2_set_tokenizer_v1 as tokenizer
from scripts.test_e2_set_formal_native_v1 import backend, metrics, reply


def source_files():
    return [{"path": name, **m.digest((Path(m.__file__).parent / name).read_bytes())}
            for name in ("__init__.py", "e2_set_formal_native_v1.py", "e2_set_release_native_v1.py", "e2_set_tokenizer_v1.py")]


def synthetic_artifacts():
    cells, cases = [], []
    for stream, count in m.STREAMS:
        for local in range(count):
            depth = local % 32 + 1 if stream != "injection" else local % 3 + 1
            query_id = f"unit-{stream}-{local // 32}" if stream != "injection" else f"unit-{local}"
            cell_id = f"{stream}.{local // 32 + 1:04d}.depth.{depth:02d}" if stream != "injection" else "injection." + query_id
            query = f"Synthetic {query_id} with < & \u2028 \u2029"
            wire = tokenizer.encode_documents([f"Synthetic candidate {n}" for n in range(depth)])
            body = h.request_body(query, wire, tokenizer.INSTRUCTION)
            prefix = [f"unit-document-{n}" for n in range(depth)] if stream != "injection" else [f"injection.{query_id}.document.{n + 1}" for n in range(depth)]
            cells.append(dict(ordinal=len(cells), stream=stream, stream_ordinal=local, cell_id=cell_id,
                query_id=query_id, depth=depth, prefix_ids=prefix, input_commitment_sha256="d" * 64,
                body=body, body_pin=m.digest(m.canonical(body)), expected_yes=stream == "old100"))
            values = [tokenizer.PREFIX, tokenizer.BODY_PREFIX, query, tokenizer.BODY_MIDDLE, wire, tokenizer.SUFFIX]
            names = ("prefix", "body_prefix", "query", "body_middle", "document", "suffix")
            segments = [dict(name=name, trusted=i not in (2, 4), utf8=text, token_ids=[i + 1])
                        for i, (name, text) in enumerate(zip(names, values))]
            rendered = "".join(values)
            cases.append(dict(id=cell_id, kind="ordered_set", rendered_utf8=rendered,
                rendered_sha256=m.digest(rendered.encode())["sha256"], rendered_size_bytes=len(rendered.encode()),
                segments=segments, token_ids=list(range(1, 7)), token_count=6, untrusted_control_ids=[]))
    inputs = m.envelope("cell-inputs", family=m.PHASE, cell_count=m.COUNT,
                        source_pins=[], provenance=h.PROVENANCE, cells=cells)
    catalog = copy.deepcopy(inputs)
    catalog["artifact"] = m.PREFIX + "cell-catalog"
    for cell in catalog["cells"]:
        cell.update(token_count=6, token_ids=list(range(1, 7)), token_ids_sha256=h.token_sha(list(range(1, 7))))
    reference = m.envelope("reference-tokens", input_pin=m.digest(b"unit"),
        reference_source_pin=h.REFERENCE_SOURCE_PIN, tokenizer_source_pin=h.TOKENIZER_PIN,
        provenance=h.PROVENANCE, cases=cases)
    return inputs, catalog, reference


class Fixture:
    def __init__(self, root):
        self.root = root
        self.inputs, self.catalog, self.reference = synthetic_artifacts()
        self.source_files = source_files()
        self.ca = b"unit certificate placeholder\n"
        self.deployment = h.envelope("deployment", container=h.CONTAINER, origin=h.ORIGIN,
            backend_port=h.BACKEND_PORT, profile=h.PROFILE, backend=backend(), model_revision=h.REVISION,
            model_files=[dict(path=p, sha256="b" * 64, size_bytes=123) for p in h.MODEL_PATHS],
            runtime_versions=h.RUNTIME_VERSIONS, tls_ca_pin=m.digest(self.ca),
            transport_source_pin=m.FORMAL_PIN, provenance=h.PROVENANCE)
        self.calibration = {"request_plans": [{"requests": [{"request_id": f"old-{n}"} for n in range(1280)]}]}
        self.policy = dict(prereg_pin=m.digest(m.canonical(self.calibration)), cutoff_float32_bits="3f7e3eb1",
            maximum_float32_bits="3f7e3eb0", deployment_record_pin=m.digest(m.canonical(self.deployment)),
            phases=[{"phase": "unit-original-calibration-01"}, {"phase": "unit-original-calibration-02"}])
        self.bge = dict(artifact="imms-gb10-native-set-heldout-bge-terminal", version=1, epoch=m.EPOCH,
            phase="heldout-bge-20", status="success", policy_pin=m.digest(m.canonical(self.policy)),
            policy_commit=m.POLICY_COMMIT, execution_commit="a" * 40, execution_host="local-development-cpu",
            consumed_posts=20, completed_posts=20, count_known=True, retry_count=0,
            failure_code="", provenance=h.PROVENANCE)
        for key in ("intent_pin", "started_pin", "deployment_pin", "vector_set_pin", "receipt_pin", "runtime_poststate_pin"):
            self.bge[key] = m.digest(key.encode())
        self.replay = m.envelope("policy-replay", policy_commit=m.POLICY_COMMIT,
            policy_pin=m.digest(m.canonical(self.policy)), prereg_pin=self.policy["prereg_pin"],
            phases=self.policy["phases"], replayed_posts=1280, repeat_cells_bits_exact=640,
            cutoff_float32_bits="3f7e3eb1", provenance=h.PROVENANCE, quality_pass=False, authority="unissued", counter="0/5")
        source_roles = {"native_policy": m.digest(m.canonical(self.policy)),
                        "heldout_bge_terminal": m.digest(m.canonical(self.bge)),
                        "heldout_bge_runtime_prestate": m.digest(b"unit-runtime-prestate")}
        for field, role in (("intent_pin", "intent"), ("started_pin", "started"), ("deployment_pin", "deployment"),
                            ("vector_set_pin", "vectors"), ("receipt_pin", "receipt"), ("runtime_poststate_pin", "runtime_poststate")):
            source_roles["heldout_bge_" + role] = self.bge[field]
        self.inputs["source_pins"] = [{"role": role, "path": role + ".json", **identity} for role, identity in sorted(source_roles.items())]
        self.catalog["source_pins"] = copy.deepcopy(self.inputs["source_pins"])
        self.plans = [dict(stream=stream, expected_posts=count, requests=[
            {key: cell[key] for key in ("ordinal", "stream_ordinal", "cell_id")} | {"request_id": f'unit-release-{cell["ordinal"]}'}
            for cell in self.catalog["cells"] if cell["stream"] == stream]) for stream, count in m.STREAMS]

    def pins(self):
        return dict(POLICY_PIN=m.digest(m.canonical(self.policy)),
            CALIBRATION_PREREG_PIN=m.digest(m.canonical(self.calibration)),
            DEPLOYMENT_PIN=m.digest(m.canonical(self.deployment)), CA_PIN=m.digest(self.ca))

    def write(self, *, mutation=None, input_raw=None, intent_mutation=None):
        raw_inputs = input_raw if input_raw is not None else m.canonical(self.inputs)
        self.reference["input_pin"] = m.digest(raw_inputs)
        objects = dict(inputs=raw_inputs, catalog=m.canonical(self.catalog), reference_tokens=m.canonical(self.reference),
            deployment_record=m.canonical(self.deployment), ca_file=self.ca, policy=m.canonical(self.policy),
            policy_replay=m.canonical(self.replay), bge_terminal=m.canonical(self.bge), calibration_prereg=m.canonical(self.calibration))
        for key, raw in objects.items():
            (self.root / key).write_bytes(raw)
        p = m.envelope("prereg", phase=m.PHASE, input_pin=m.digest(raw_inputs), catalog_pin=m.digest(objects["catalog"]),
            reference_tokens_pin=m.digest(objects["reference_tokens"]), deployment_record_pin=m.digest(objects["deployment_record"]),
            ca_pin=m.digest(self.ca), policy_pin=m.digest(objects["policy"]), policy_commit=m.POLICY_COMMIT,
            policy_replay_pin=m.digest(objects["policy_replay"]), bge_terminal_pin=m.digest(objects["bge_terminal"]),
            bge_terminal_commit="b" * 40, source_files=self.source_files, expected_posts=m.COUNT,
            request_plans=self.plans, provenance=h.PROVENANCE)
        if mutation:
            mutation(p)
        raw = m.canonical(p)
        (self.root / "prereg").write_bytes(raw)
        intent = m.envelope("intent", phase=m.PHASE, prereg_pin=m.digest(raw), prereg_commit="c" * 40,
            **{k: p[k] for k in ("policy_pin", "policy_commit", "bge_terminal_pin", "bge_terminal_commit")},
            expected_posts=m.COUNT, provenance=h.PROVENANCE)
        if intent_mutation:
            intent_mutation(intent)
        (self.root / "intent").write_bytes(m.canonical(intent))
        return m.digest(raw), m.digest(m.canonical(intent))

    def plan(self, **kwargs):
        ppin, ipin = self.write(**kwargs)
        with patch.multiple(m, **self.pins()):
            return m.Plan(self.root / "prereg", ppin["sha256"], ppin["size_bytes"],
                **{name: self.root / name for name in ("inputs", "catalog", "reference_tokens", "deployment_record",
                    "ca_file", "policy", "policy_replay", "bge_terminal", "calibration_prereg")},
                intent=self.root / "intent", intent_sha256=ipin["sha256"], intent_size=ipin["size_bytes"])


class PureTests(unittest.TestCase):
    def reject(self, code, call):
        with self.assertRaises((m.ReleaseError, ValueError)) as raised:
            call()
        self.assertEqual(raised.exception.code, code)

    def test_strict_json_and_go_order(self):
        for raw in (b'{"a":1,"a":2}', b'{"x":NaN}', b'{"x":1e999}', b'\xef\xbb\xbf{}', b'{"x":"\\ud800"}'):
            with self.assertRaises(m.ReleaseError):
                m.decode(raw)
        raw = b'{"version":1,"artifact":"unit"}\n'
        self.assertEqual(m.decode(raw)["version"], 1)
        self.reject("json_noncanonical", lambda: m.decode(raw, canonical_required=True))

    def test_bootstrap_pin_before_parse_nofollow_fifo_hardlink(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            path = root / "raw"
            path.write_bytes(b"not json")
            self.reject("pin_file", lambda: m.bootstrap_read(path, m.digest(b"{}")))
            (root / "parent").symlink_to(root, target_is_directory=True)
            (root / "link").symlink_to(path)
            for selected in (root / "parent/raw", root / "link"):
                self.reject("pin_io", lambda: m.bootstrap_read(selected, m.digest(b"not json")))
            os.mkfifo(root / "fifo")
            self.reject("pin_file", lambda: m.bootstrap_read(root / "fifo", m.digest(b"x")))
            os.link(path, root / "hard")
            self.reject("pin_file", lambda: m.bootstrap_read(path, m.digest(b"not json")))

    def test_closure_all_pins_precede_dependency_import(self):
        for index in (0, 1, 2, 3):
            closure = source_files()
            closure[index]["sha256"] = "0" * 64
            with patch.object(m, "execute_pinned") as imported:
                with self.assertRaises(m.ReleaseError):
                    m.load_helpers(closure)
                imported.assert_not_called()
        self.assertEqual(m.load_helpers(source_files()).PROFILE, h.PROFILE)

    def test_verified_raw_execution_ignores_import_cache_and_rejects_private_preload(self):
        poisoned = types.ModuleType("scripts.e2_set_formal_native_v1")
        poisoned.PROFILE = "poisoned"
        with patch.dict(sys.modules, {"scripts.e2_set_formal_native_v1": poisoned}):
            loaded = m.load_helpers(source_files())
            self.assertEqual(loaded.PROFILE, h.PROFILE)
            self.assertEqual(loaded.release_tokenizer.INSTRUCTION, tokenizer.INSTRUCTION)
        with patch.dict(sys.modules, {"_imms_release_formal_pinned": poisoned}):
            self.reject("source_preloaded", lambda: m.load_helpers(source_files()))
        self.assertNotIn("_imms_release_formal_pinned", sys.modules)
        self.assertNotIn("_imms_release_tokenizer_pinned", sys.modules)


class PlanTests(unittest.TestCase):
    reject = PureTests.reject

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.fixture = Fixture(Path(self.temporary.name))

    def test_complete3849_go_order_and_source_role_names(self):
        raw = (json.dumps(self.fixture.inputs, ensure_ascii=False, separators=(",", ":")) + "\n").encode()
        plan = self.fixture.plan(input_raw=raw)
        self.assertEqual(len(plan.requests), 3849)
        self.assertEqual([len(plan.by_stream[s]) for s, _ in m.STREAMS], [640, 3200, 9])
        self.assertNotEqual(raw, m.canonical(self.fixture.inputs))
        self.assertNotIn("expected_yes", plan.requests[640]["body"])
        self.assertNotEqual(plan.intent["bge_terminal_commit"], plan.bge_terminal["execution_commit"])

    def test_exact_common_count_no_partial_or_phase_selector(self):
        self.reject("prerequisite_identity", lambda: self.fixture.plan(mutation=lambda p: p.update(expected_posts=640)))
        self.reject("phase_identity", lambda: self.fixture.plan(mutation=lambda p: p.update(phase="heldout")))
        self.fixture.plans[1]["requests"].pop()
        self.reject("request_plans", self.fixture.plan)

    def test_request_ids_cannot_reuse_calibration_or_cross_stream(self):
        self.fixture.plans[0]["requests"][0]["request_id"] = "old-0"
        self.reject("request_plan", self.fixture.plan)
        self.fixture.plans[0]["requests"][0]["request_id"] = self.fixture.plans[1]["requests"][0]["request_id"]
        self.reject("request_plan", self.fixture.plan)

    def test_heldout_truth_and_injection_member_identity(self):
        for obj in (self.fixture.inputs, self.fixture.catalog):
            obj["cells"][0]["expected_yes"] = True
        self.reject("cell_truth", self.fixture.plan)
        for obj in (self.fixture.inputs, self.fixture.catalog):
            obj["cells"][0]["expected_yes"] = False
            obj["cells"][3840]["prefix_ids"] = ["changed-member"]
        self.reject("injection_identity", self.fixture.plan)

    def test_draft_and_unknown_blindness_claim_rejected(self):
        self.fixture.inputs["artifact"] = m.PREFIX + "request-draft"
        self.reject("artifact_identity", self.fixture.plan)
        self.fixture.inputs["artifact"] = m.PREFIX + "cell-inputs"
        self.reject("provenance", lambda: self.fixture.plan(mutation=lambda p: p.update(provenance={"static_access": "untouched"})))

    def test_reference_full_tokens_and_untrusted_boundary(self):
        self.fixture.reference["cases"][640]["token_ids"][0] = 99
        self.reject("reference_tokens", self.fixture.plan)
        self.fixture.reference["cases"][640]["token_ids"][0] = 1
        self.fixture.reference["cases"][640]["segments"][2]["token_ids"] = [151643]
        self.reject("untrusted_control", self.fixture.plan)

    def test_vocab_out_of_range_rejected_before_execution(self):
        cell = self.fixture.catalog["cells"][0]
        cell["token_ids"][0] = 151669
        cell["token_ids_sha256"] = h.token_sha(cell["token_ids"])
        self.reject("token_vocabulary", self.fixture.plan)

    def test_bge_success20_no_retry_and_original_cpu_host(self):
        for key, value in (("status", "failed"), ("completed_posts", 19), ("retry_count", 1), ("count_known", False),
                           ("execution_host", "gb10"), ("failure_code", "error"), ("vector_set_pin", None)):
            original = self.fixture.bge[key]
            self.fixture.bge[key] = value
            with self.subTest(key=key), self.assertRaises((m.ReleaseError, ValueError)):
                self.fixture.plan()
            self.fixture.bge[key] = original

    def test_bge_source_link_and_policy_replay_mismatch(self):
        self.fixture.replay["repeat_cells_bits_exact"] = 639
        self.reject("policy_replay", self.fixture.plan)
        self.fixture.replay["repeat_cells_bits_exact"] = 640
        for artifact in (self.fixture.inputs, self.fixture.catalog):
            record = next(p for p in artifact["source_pins"] if p["role"] == "heldout_bge_terminal")
            record["role"] = "bge_terminal"
        self.reject("source_prerequisites", self.fixture.plan)

    def test_joint_intent_cannot_switch_prerequisite(self):
        self.reject("release_intent", lambda: self.fixture.plan(intent_mutation=lambda i: i.update(bge_terminal_commit="e" * 40)))


class ExecutionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        root = Path(self.temporary.name)
        self.fixture = Fixture(root)
        self.plan = self.fixture.plan()
        self.h = self.plan.h
        self.plan.runtime, self.plan.output = root / "runtime", root / "output"
        self.plan.runtime.mkdir()
        self.backend_checks = 0
        def verified():
            self.backend_checks += 1
            return backend()
        self.plan.verify_backend = verified
        self.native_posts, self.gets = 0, []
        self.state = m.BoundaryState(self.plan)
        self.tls = dict(version="TLSv1.3", cipher="TLS_AES_256_GCM_SHA384", peer_certificate_sha256=m.digest(b"unit DER")["sha256"])
        self.pin_patch = patch.multiple(m, **self.fixture.pins())
        self.pin_patch.start()
        self.addCleanup(self.pin_patch.stop)

    def fake_http(self, method, path, body, headers, *, port, context=None):
        if port == h.BACKEND_PORT:
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

    def test_full3849_dual_ledgers_three_independent_metric_streams(self):
        collector = self.collector()
        with patch.object(self.h, "http_once", self.fake_http):
            result = collector.run()
        self.assertEqual(self.native_posts, 3849)
        self.assertEqual(self.gets.count("/metrics"), 3852)
        self.assertEqual(self.gets.count("/health"), 6)
        self.assertEqual([len(v["metrics"]) for v in result["stream_observations"]], [641, 3201, 10])
        self.assertGreaterEqual(self.backend_checks, 7698)
        for role in ("provider", "collector"):
            ledger = self.plan.runtime / (role + "-ledger")
            terminal = m.decode((ledger / "terminal.json").read_bytes())
            self.assertEqual((terminal["completed_posts"], terminal["consumed_posts"], terminal["status"]), (3849, 3849, "success"))
            self.assertEqual(len(list(ledger.glob("*-terminal.json"))), 3849)
            for ordinal in (0, 639, 640, 3839, 3840, 3848):
                entry = self.plan.requests[ordinal]
                raw = (ledger / f"{ordinal:04d}-result.json").read_bytes()
                record = m.decode(raw)
                cell = m.decode((ledger / f"{ordinal:04d}-terminal.json").read_bytes())
                admission_raw = (ledger / f"{ordinal:04d}-admission.json").read_bytes()
                self.assertEqual(cell["result_pin"], m.digest(raw))
                self.assertEqual(record["admission_pin"], m.digest(admission_raw))
                self.assertTrue(all(v["artifact"].startswith(m.PREFIX) for v in (record, cell, m.decode(admission_raw))))
                self.assertEqual((cell["stream"], cell["stream_ordinal"]), (entry["stream"], entry["stream_ordinal"]))
                if ordinal:
                    self.assertEqual(m.decode(admission_raw)["previous_result_pin"], m.digest((ledger / f"{ordinal-1:04d}-result.json").read_bytes()))
            with self.assertRaises(ValueError):
                m.Ledger(self.plan, role)
        first = m.decode((self.plan.runtime / "collector-ledger/0000-result.json").read_bytes())
        self.assertEqual(m.digest(base64.b64decode(first["raw_response_base64"])), first["raw_response_pin"])
        self.assertEqual(first["score_float32_bits"], "3dcccccd")
        with self.assertRaises(m.ReleaseError):
            collector.run()

    def test_failure_old100_consumes_whole_bundle_no_injection_or_retry(self):
        collector = self.collector()
        original = self.fake_http
        def fail_native(method, path, body, headers, **kwargs):
            if kwargs["port"] == h.BACKEND_PORT and method == "POST" and self.native_posts == 642:
                self.native_posts += 1
                raise RuntimeError("private model details must not leak")
            return original(method, path, body, headers, **kwargs)
        with patch.object(self.h, "http_once", fail_native):
            with self.assertRaises(m.ReleaseError):
                collector.run()
            with self.assertRaises(m.ReleaseError):
                collector.run()
        self.assertEqual(self.native_posts, 643)
        for role in ("provider", "collector"):
            root = self.plan.runtime / (role + "-ledger")
            raw = (root / "terminal.json").read_bytes()
            terminal = m.decode(raw)
            self.assertEqual((terminal["completed_posts"], terminal["consumed_posts"], terminal["status"], terminal["count_known"]), (642, 643, "failed", False))
            self.assertNotIn(b"private", raw)
            self.assertFalse((root / "3840-admission.json").exists())
            failed = m.decode((root / "0642-terminal.json").read_bytes())
            self.assertEqual((failed["stream"], failed["stream_ordinal"], failed["result_pin"]), ("old100", 2, None))

    def test_failed_raw_response_retained_without_score(self):
        original = self.fake_http
        def invalid(method, path, body, headers, **kwargs):
            status, returned, raw, tls = original(method, path, body, headers, **kwargs)
            if kwargs["port"] == h.BACKEND_PORT and method == "POST":
                raw = raw.replace(b'"relevance_score":0.1', b'"relevance_score":null')
            return status, returned, raw, tls
        with patch.object(self.h, "http_once", invalid), self.assertRaises(m.ReleaseError):
            self.collector().run()
        failure = m.decode((self.plan.runtime / "provider-ledger/failure-response.json").read_bytes())
        self.assertEqual(failure["raw_body_pin"], m.digest(base64.b64decode(failure["raw_body_base64"])))
        self.assertEqual(failure["artifact"], m.PREFIX + "failure-response")
        self.assertFalse(any("score" in key for key in failure))
        self.assertEqual(self.native_posts, 1)

    def test_stream_boundary_cache_regression_stops_before_old100_post(self):
        original = self.fake_http
        metrics_at_640 = 0
        def reset(method, path, body, headers, **kwargs):
            nonlocal metrics_at_640
            result = original(method, path, body, headers, **kwargs)
            if kwargs["port"] == h.BACKEND_PORT and path == "/metrics" and self.native_posts == 640:
                metrics_at_640 += 1
                if metrics_at_640 == 2:
                    return result[0], result[1], metrics(0), result[3]
            return result
        collector = self.collector()
        with patch.object(self.h, "http_once", reset), self.assertRaises(m.ReleaseError) as raised:
            collector.run()
        self.assertEqual(raised.exception.code, "cache_regression")
        self.assertEqual(self.native_posts, 640)
        self.assertFalse((self.plan.runtime / "collector-ledger/0640-admission.json").exists())

    def test_tls_tuple_change_stops_before_first_post(self):
        original = self.fake_http
        def changed(method, path, body, headers, **kwargs):
            result = original(method, path, body, headers, **kwargs)
            if kwargs["port"] == 19483 and path == "/version":
                result = (*result[:3], {**result[3], "cipher": "OTHER_CIPHER"})
            return result
        with patch.object(self.h, "http_once", changed), self.assertRaises(m.ReleaseError) as raised:
            self.collector().run()
        self.assertEqual(raised.exception.code, "tls_identity")
        self.assertEqual(self.native_posts, 0)

    def test_durable_admission_failure_prevents_backend_and_cannot_resume(self):
        entry = self.plan.requests[0]
        body = m.canonical(entry["body"])
        headers = [("Host", "127.0.0.1:19483"), ("Content-Type", "application/json"), ("Content-Length", str(len(body))), ("X-Request-Id", entry["request_id"])]
        h.durable_new(self.plan.runtime / "provider-ledger/0000-admission.json", b"preexisting consumed record")
        with patch.object(self.h, "http_once", self.fake_http), self.assertRaises(m.ReleaseError):
            self.state.forward("POST", "/v1/rerank", headers, body)
        self.assertEqual(self.native_posts, 0)
        terminal = m.decode((self.plan.runtime / "provider-ledger/terminal.json").read_bytes())
        self.assertFalse(terminal["count_known"])
        with self.assertRaises(ValueError):
            m.Ledger(self.plan, "provider")

    def test_wrong_request_id_and_tls_peer_stop_before_model(self):
        self.tls["peer_certificate_sha256"] = "0" * 64
        with patch.object(self.h, "http_once", self.fake_http), self.assertRaises(m.ReleaseError):
            self.collector().run()
        self.assertEqual(self.native_posts, 0)
        failure = m.decode((self.plan.runtime / "collector-ledger/failure-response.json").read_bytes())
        self.assertEqual(failure["tls"], self.tls)
        entry, body = self.plan.requests[0], m.canonical(self.plan.requests[0]["body"])
        headers = [("Host", "127.0.0.1:19483"), ("Content-Type", "application/json"), ("Content-Length", str(len(body))), ("X-Request-Id", "wrong")]
        with patch.object(self.h, "http_once", self.fake_http), self.assertRaises(m.ReleaseError):
            self.state.forward("POST", "/v1/rerank", headers, body)
        self.assertEqual(self.native_posts, 0)

    def test_normal_stop_after_durable_success_returns_zero(self):
        args = types.SimpleNamespace(mode="collect", expected_sha256="0" * 64, expected_size=1, intent_sha256="0" * 64, intent_size=1)
        for key in ("prereg", "inputs", "catalog", "reference_tokens", "deployment_record", "ca_file", "policy", "policy_replay", "bge_terminal", "calibration_prereg", "intent"):
            setattr(args, key, "unit")
        state = types.SimpleNamespace(ledger=types.SimpleNamespace(success=True), run=lambda: m.require(False, "process_interrupted"))
        with patch.object(m.argparse.ArgumentParser, "parse_args", return_value=args), patch.object(m, "Plan", return_value=self.plan), patch.object(m, "Collector", return_value=state), patch.object(m.signal, "signal"), patch("builtins.print"):
            self.assertEqual(m.main(), 0)


if __name__ == "__main__":
    unittest.main()
