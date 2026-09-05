from __future__ import annotations

import copy
import hashlib
import json
from pathlib import Path
import ssl
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

from scripts import e2_set_diagnostic_v1 as subject


class DiagnosticTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cert_directory = tempfile.TemporaryDirectory()
        root = Path(cls.cert_directory.name)
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                        "-subj", "/CN=diagnostic-test-only", "-addext", "subjectAltName=IP:127.0.0.1",
                        "-keyout", str(root / "key.pem"), "-out", str(root / "ca.pem")],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        cls.ca = (root / "ca.pem").read_bytes()

    @classmethod
    def tearDownClass(cls):
        cls.cert_directory.cleanup()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.plan = self.make_plan("base")

    def pin(self, path, value, *, raw=False):
        data = value if raw else subject.canonical(value)
        path.write_bytes(data)
        return {"path": str(path), "sha256": hashlib.sha256(data).hexdigest(), "size_bytes": len(data)}

    def make_plan(self, name):
        root = self.root / name
        root.mkdir()
        inputs, tokens, requests = [], [], []
        for case_id in subject.CASE_IDS:
            raw_case = case_id == "capability_raw"
            query = subject.CAPABILITY_QUERY if raw_case else "Synthetic question " + case_id
            document = subject.CAPABILITY_DOCUMENT if raw_case else subject.encode_documents(["Synthetic record"])
            inputs.append({"id": case_id, "kind": "capability_raw" if raw_case else "ordered_set",
                           "query": query, "document": document})
            # Explicitly synthetic tokens for transport shape tests only.
            ids = [11, 12, 13]
            tokens.append({"id": case_id, "token_ids": ids, "token_count": len(ids),
                           "rendered_utf8": subject.PREFIX + subject.BODY_PREFIX + query
                           + subject.BODY_MIDDLE + document + subject.SUFFIX})
            for repeat in (1, 2):
                requests.append({"ordinal": len(requests) + 1, "case_id": case_id, "repeat": repeat,
                                 "request_id": subject.request_id(case_id, repeat),
                                 "body": subject.request_body(query, document),
                                 "token_count": len(ids), "token_ids_sha256": subject.token_sha(ids)})
        source_raw = Path(subject.__file__).read_bytes()
        source_pin = {"sha256": hashlib.sha256(source_raw).hexdigest(), "size_bytes": len(source_raw)}
        ca_pin = self.pin(root / "ca.pem", self.ca, raw=True)
        backend = {"container_id": "test-container", "entrypoint": ["vllm"],
                   "cmd": ["serve", "--no-enable-prefix-caching"], "readonly_rootfs": True,
                   "user": "1000:1000", "cap_drop": ["ALL"], "security_opt": ["no-new-privileges"],
                   "set_tokenizer_profile": subject.PROFILE}
        record = {"backend": backend, "proxy_source_sha256": source_pin["sha256"],
                  "https_port": 19482, "backend_port": 18484, "tls_ca_sha256": ca_pin["sha256"],
                  "operator_additional_fact": "retained"}
        value = {"artifact": "imms-gb10-set-diagnostic-prereg", "version": 1,
                 "inputs": self.pin(root / "inputs.json", {"artifact": "imms-gb10-set-diagnostic-inputs",
                                                           "version": 1, "cases": inputs}),
                 "native": {
                     "token_file": self.pin(root / "tokens.json", {"artifact": "imms-gb10-set-native-tokens",
                                                                  "version": 1, "cases": tokens}),
                     "deployment_record": self.pin(root / "record.json", record), "ca_file": ca_pin,
                     "source_pin": source_pin, "container": subject.CONTAINER, "origin": subject.ORIGIN,
                     "backend_port": 18484, "expected_requests": 16, "timeout_seconds": 300,
                     "ledger_directory": str(root / "client-ledger"),
                     "boundary_ledger_directory": str(root / "proxy-ledger"),
                     "output_directory": str(root / "output"), "requests": requests},
                 "reference": {"separate_preregistered_reference": True}}
        pin = self.pin(root / "prereg.json", value)
        return subject.Plan(pin["path"], pin["sha256"], pin["size_bytes"])

    def reload_mutation(self, mutate):
        value = copy.deepcopy(self.plan.value)
        mutate(value)
        pin = self.pin(self.root / "changed.json", value)
        return subject.Plan(pin["path"], pin["sha256"], pin["size_bytes"])

    def assert_code(self, code, function, *args, **kwargs):
        with self.assertRaises(subject.DiagnosticError) as caught:
            function(*args, **kwargs)
        self.assertEqual(str(caught.exception), code)

    def wire_headers(self, entry):
        return [("Host", "127.0.0.1:19482"), ("Content-Type", "application/json"),
                ("Content-Length", str(len(subject.canonical(entry["body"])))),
                ("X-Request-Id", entry["request_id"])]

    def reply(self, entry, score="0.75"):
        value = {"id": "score-" + entry["request_id"], "model": "sparkclaw-reranker",
                 "results": [{"index": 0, "document": {"text": entry["body"]["documents"][0],
                                                        "multi_modal": None}, "relevance_score": "SCORE"}],
                 "usage": {"prompt_tokens": entry["token_count"], "total_tokens": entry["token_count"]}}
        return subject.canonical(value).replace(b'"SCORE"', score.encode())

    def client_headers(self, plan=None):
        plan = plan or self.plan
        return [(subject.PLAN_HEADER, plan.sha), (subject.DEPLOYMENT_HEADER, plan.deployment_sha)]

    def fake_http(self, method, path, body, headers, **kwargs):
        if method == "POST":
            entry = next(e for e in self.plan.requests if e["request_id"] == headers["X-Request-Id"])
            self.assertTrue((Path(self.plan.native["ledger_directory"]) / f'{entry["ordinal"]:02d}-consumed.json').exists())
            raw = self.reply(entry)
        elif path == "/v1/models":
            raw = subject.canonical({"data": [{"id": "sparkclaw-reranker", "root": "Qwen/Qwen3-Reranker-4B",
                                              "max_model_len": 8192, "owned_by": "vllm", "created": 123}]})
        elif path == "/version":
            raw = b'{"version":"0.23.0"}'
        elif path == "/health":
            raw = b""
        else:
            raw = (b'vllm:prefix_cache_queries_total{engine="0",model_name="sparkclaw-reranker"} 0\n'
                   b'vllm:prefix_cache_hits_total{engine="0",model_name="sparkclaw-reranker"} 0\n')
        self.assertIsNotNone(kwargs["context"])
        self.assertEqual(kwargs["port"], 19482)
        return 200, self.client_headers(), raw

    def test_external_pin_precedes_decode_and_rejects_symlinks(self):
        malformed = self.root / "invalid.json"
        malformed.write_bytes(b"private not json")
        with mock.patch.object(subject, "decode") as decoder:
            self.assert_code("pin_mismatch", subject.Plan, malformed, "0" * 64, malformed.stat().st_size)
            decoder.assert_not_called()
        symlink = self.root / "symlink.json"
        symlink.symlink_to(malformed)
        self.assert_code("pin_io", subject.read_pin, symlink,
                         hashlib.sha256(malformed.read_bytes()).hexdigest(), malformed.stat().st_size)

    def test_plan_refuses_mutated_order_body_tls_source_and_counts(self):
        mutations = [
            (lambda p: p["native"].update(origin="http://127.0.0.1:19482"), "native_profile"),
            (lambda p: p["native"].update(expected_requests=15), "native_profile"),
            (lambda p: p["native"]["requests"].reverse(), "request_plan"),
            (lambda p: p["native"]["requests"][0]["body"].update(cache_salt="extra"), "request_plan"),
            (lambda p: p["native"]["source_pin"].update(sha256="0" * 64), "pin_mismatch"),
        ]
        for mutate, code in mutations:
            self.assert_code(code, self.reload_mutation, mutate)

    def test_go_struct_order_inputs_and_unsorted_tokens_keep_their_raw_pins(self):
        # Matches the real Go input writer's field order, including cases. It is
        # intentionally different from Python's canonical sorted-key encoding.
        inputs = self.plan.inputs
        ordered = {"artifact": inputs["artifact"], "version": inputs["version"], "cases": [
            {key: entry[key] for key in ("id", "kind", "query", "document")}
            for entry in inputs["cases"]]}
        inputs_raw = (json.dumps(ordered, ensure_ascii=False, indent=2) + "\n").encode()
        token_raw = (json.dumps(self.plan.tokens, ensure_ascii=False, indent=2) + "\n").encode()
        self.assertNotEqual(inputs_raw, subject.canonical(ordered))
        self.assertNotEqual(token_raw, subject.canonical(self.plan.tokens))
        input_pin = self.pin(self.root / "go-inputs.json", inputs_raw, raw=True)
        token_pin = self.pin(self.root / "external-tokens.json", token_raw, raw=True)
        plan = self.reload_mutation(lambda p: (
            p.update(inputs=input_pin), p["native"].update(token_file=token_pin)))
        self.assertEqual(plan.requests, self.plan.requests)
        self.assertEqual(Path(input_pin["path"]).read_bytes(), inputs_raw)
        self.assertEqual(Path(token_pin["path"]).read_bytes(), token_raw)
        with mock.patch.object(subject, "decode") as decoder:
            bad_pin = {**input_pin, "sha256": "0" * 64}
            self.assert_code("pin_mismatch", subject.artifact_pin, bad_pin, canonical_required=False)
            decoder.assert_not_called()

    def test_external_json_still_rejects_duplicates_and_nan_and_plan_stays_canonical(self):
        for index, (raw, code) in enumerate(((b'{"x":1,"x":2}', "json_duplicate_key"),
                                            (b'{"x":NaN}', "json_invalid"))):
            pin = self.pin(self.root / f"invalid-{index}.json", raw, raw=True)
            self.assert_code(code, subject.artifact_pin, pin, canonical_required=False)
        raw = json.dumps(self.plan.value, indent=2).encode()
        pin = self.pin(self.root / "unsorted-prereg.json", raw, raw=True)
        self.assert_code("json_noncanonical", subject.Plan, pin["path"], pin["sha256"], pin["size_bytes"])
        record = json.dumps(self.plan.record, indent=2).encode()
        pin = self.pin(self.root / "unsorted-record.json", record, raw=True)
        self.assert_code("json_noncanonical", self.reload_mutation,
                         lambda p: p["native"].update(deployment_record=pin))

    def test_backend_wrapper_binds_exact_profile_on_immutable_container_id(self):
        legacy = {"container_id": "actual-container-id", "public_fact": "retained"}
        accepted = (subject.PROFILE_ENV + "=" + subject.PROFILE + "\n").encode()
        with mock.patch.object(subject, "_legacy_backend_identity", return_value=legacy), \
                mock.patch.object(subject.subprocess, "check_output", return_value=accepted) as inspect:
            self.assertEqual(subject.backend_identity(subject.CONTAINER),
                             {**legacy, "set_tokenizer_profile": subject.PROFILE})
            command = inspect.call_args.args[0]
            self.assertEqual(command[:3], ["docker", "inspect", "--format"])
            self.assertEqual(command[-1], legacy["container_id"])
            self.assertIn(subject.PROFILE_ENV, command[3])
            self.assertNotIn("println", command[3])
        for raw in (b"\n", b"IMMS_SET_TOKENIZER_PROFILE=unknown\n", accepted + accepted,
                    accepted.rstrip(b"\n") + b"-other\n"):
            with self.subTest(raw=raw), mock.patch.object(subject, "_legacy_backend_identity", return_value=legacy), \
                    mock.patch.object(subject.subprocess, "check_output", return_value=raw):
                self.assert_code("backend_profile", subject.backend_identity, subject.CONTAINER)

    def test_deployment_without_exact_profile_is_rejected_before_claim(self):
        for index, profile in enumerate((None, "different")):
            record = copy.deepcopy(self.plan.record)
            record["backend"].pop("set_tokenizer_profile")
            if profile is not None:
                record["backend"]["set_tokenizer_profile"] = profile
            pin = self.pin(self.root / f"profile-{index}.json", record)
            self.assert_code("deployment_profile", self.reload_mutation,
                             lambda p: p["native"].update(deployment_record=pin))
        self.assertFalse(Path(self.plan.native["boundary_ledger_directory"]).exists())

    def test_boundary_consumes_before_failed_io_and_never_retries(self):
        entry = self.plan.requests[0]
        with mock.patch.object(subject, "backend_identity", return_value=self.plan.record["backend"]):
            state = subject.BoundaryState(self.plan)

            def failed_io(*args, **kwargs):
                self.assertTrue((state.ledger.directory / "01-consumed.json").exists())
                raise subject.DiagnosticError("transport_failure")

            with mock.patch.object(subject, "http_once", side_effect=failed_io) as network:
                self.assert_code("transport_failure", state.forward, "POST", "/v1/rerank",
                                 self.wire_headers(entry), subject.canonical(entry["body"]))
                self.assert_code("run_failed", state.forward, "POST", "/v1/rerank",
                                 self.wire_headers(entry), subject.canonical(entry["body"]))
                network.assert_called_once()
            self.assertTrue((state.ledger.directory / "failed.json").exists())
            for name in ("01-terminal.json", "terminal.json"):
                terminal = json.loads((state.ledger.directory / name).read_bytes())
                self.assertEqual(terminal["state"], "failed")
                self.assertEqual(terminal["code"], "transport_failure")
            self.assert_code("run_already_claimed", subject.BoundaryState, self.plan)

    def test_boundary_has_16_request_terminals_and_only_finishes_after_final_metrics(self):
        with mock.patch.object(subject, "backend_identity", return_value=self.plan.record["backend"]), \
                mock.patch.object(subject, "http_once") as network:
            state = subject.BoundaryState(self.plan)
            for entry in self.plan.requests:
                network.return_value = (200, [], self.reply(entry))
                state.forward("POST", "/v1/rerank", self.wire_headers(entry), subject.canonical(entry["body"]))
                terminal = json.loads((state.ledger.directory / f'{entry["ordinal"]:02d}-terminal.json').read_bytes())
                self.assertEqual(terminal["state"], "success")
                self.assertEqual(terminal["raw_response_sha256"], hashlib.sha256(self.reply(entry)).hexdigest())
            self.assertFalse((state.ledger.directory / "terminal.json").exists())
            network.return_value = (200, [], b"metrics")
            state.forward("GET", "/metrics", [("Host", "127.0.0.1:19482")], b"")
            terminal_path = state.ledger.directory / "terminal.json"
            terminal_raw = terminal_path.read_bytes()
            self.assertEqual(json.loads(terminal_raw)["state"], "success")
            self.assertEqual(len(network.call_args_list), 17)
            self.assert_code("run_complete", state.forward, "POST", "/v1/rerank",
                             self.wire_headers(entry), subject.canonical(entry["body"]))
            self.assertEqual(len(network.call_args_list), 17)
            self.assertEqual(terminal_path.read_bytes(), terminal_raw)
            self.assert_code("run_already_claimed", subject.BoundaryState, self.plan)

    def test_boundary_unexpected_failure_and_pre_request_failure_have_run_terminals(self):
        for index, method in enumerate(("GET", "POST")):
            plan = self.make_plan("unexpected-" + str(index))
            with mock.patch.object(subject, "backend_identity", return_value=plan.record["backend"]), \
                    mock.patch.object(subject, "http_once", side_effect=RuntimeError("not retried")) as network:
                state = subject.BoundaryState(plan)
                entry = plan.requests[0]
                args = (method, "/health", [("Host", "127.0.0.1:19482")], b"") if method == "GET" else (
                    method, "/v1/rerank", self.wire_headers(entry), subject.canonical(entry["body"]))
                self.assert_code("boundary_failure", state.forward, *args)
                self.assert_code("run_failed", state.forward, *args)
                network.assert_called_once()
                terminal = json.loads((state.ledger.directory / "terminal.json").read_bytes())
                self.assertEqual(terminal["state"], "failed")
                self.assertEqual(terminal["consumed_post_count"], int(method == "POST"))
                self.assertEqual((state.ledger.directory / "01-terminal.json").exists(), method == "POST")

    def test_boundary_rejects_order_body_duplicate_headers_te_and_alias(self):
        for number, change in enumerate(("order", "body", "duplicate", "te", "route", "query_string")):
            plan = self.make_plan("boundary-" + str(number))
            entry = plan.requests[1 if change == "order" else 0]
            body, headers, path = subject.canonical(entry["body"]), self.wire_headers(entry), "/v1/rerank"
            if change == "body":
                body = body.replace(b'"priority":0', b'"priority":1')
            if change == "duplicate":
                headers.append(("x-request-id", entry["request_id"]))
            if change == "te":
                headers.append(("Transfer-Encoding", "chunked"))
            if change == "route":
                path = "/rerank"
            if change == "query_string":
                path += "?x=1"
            with mock.patch.object(subject, "backend_identity", return_value=plan.record["backend"]), \
                    mock.patch.object(subject, "http_once") as network:
                state = subject.BoundaryState(plan)
                with self.assertRaises(subject.DiagnosticError):
                    state.forward("POST", path, headers, body)
                network.assert_not_called()
                self.assertTrue(state.ledger.failed)
                self.assertFalse((state.ledger.directory / "01-consumed.json").exists())

    def test_boundary_identity_drift_after_io_consumes_and_stops(self):
        entry = self.plan.requests[0]
        backend = self.plan.record["backend"]
        with mock.patch.object(subject, "backend_identity", side_effect=[backend, backend, {"changed": True}]), \
                mock.patch.object(subject, "http_once", return_value=(200, [], self.reply(entry))) as network:
            state = subject.BoundaryState(self.plan)
            self.assert_code("backend_identity", state.forward, "POST", "/v1/rerank",
                             self.wire_headers(entry), subject.canonical(entry["body"]))
            self.assertTrue(state.ledger.failed)
            network.assert_called_once()

    def test_boundary_preserves_raw_body_and_rejects_replay(self):
        entry = self.plan.requests[0]
        raw = self.reply(entry, "7.500e-1")
        with mock.patch.object(subject, "backend_identity", return_value=self.plan.record["backend"]), \
                mock.patch.object(subject, "http_once", return_value=(200, [("Content-Type", "application/json")], raw)) as network:
            state = subject.BoundaryState(self.plan)
            status, headers, actual = state.forward("POST", "/v1/rerank", self.wire_headers(entry),
                                                    subject.canonical(entry["body"]))
            self.assertEqual(actual, raw)
            self.assertEqual(status, 200)
            self.assertEqual(headers[subject.PLAN_HEADER], self.plan.sha)
            self.assert_code("request_mismatch", state.forward, "POST", "/v1/rerank",
                             self.wire_headers(entry), subject.canonical(entry["body"]))
            network.assert_called_once()

    def test_response_strict_score_echo_id_fields_and_usage(self):
        entry = self.plan.requests[0]
        result = subject.validate_reply(entry, self.reply(entry, "7.500e-1"))
        self.assertEqual(result, {"p_yes": "7.500e-1", "score_float64_bits": "3fe8000000000000",
                                  "score_float32_bits": "3f400000"})
        for score in ("NaN", "Infinity", "-0.1", "1.1", '"0.75"', "true", "null"):
            with self.subTest(score=score), self.assertRaises(subject.DiagnosticError):
                subject.validate_reply(entry, self.reply(entry, score))
        for change in ("echo", "id", "usage", "unknown", "index", "multimodal", "duplicate"):
            value = json.loads(self.reply(entry))
            if change == "echo": value["results"][0]["document"]["text"] = "different"
            if change == "id": value["id"] = "different"
            if change == "usage": value["usage"]["prompt_tokens"] += 1
            if change == "unknown": value["unknown"] = 0
            if change == "index": value["results"][0]["index"] = False
            if change == "multimodal": value["results"][0]["document"]["multi_modal"] = []
            raw = subject.canonical(value)
            if change == "duplicate": raw = raw.replace(b'"model":', b'"model":"x","model":', 1)
            with self.subTest(change=change), self.assertRaises(subject.DiagnosticError):
                subject.validate_reply(entry, raw)

    def test_tls_context_requires_certificate_and_hostname(self):
        collector = subject.Collector(self.plan)
        self.assertEqual(collector.context.verify_mode, ssl.CERT_REQUIRED)
        self.assertTrue(collector.context.check_hostname)
        broken = self.make_plan("bad-ca")
        broken.ca = b"not a certificate"
        self.assert_code("tls_configuration", subject.Collector, broken)

    def test_complete_collector_makes_exact_16_posts_and_saves_raw(self):
        collector = subject.Collector(self.plan)
        with mock.patch.object(subject, "http_once", side_effect=self.fake_http) as network:
            result = collector.run()
        self.assertEqual(len(network.call_args_list), 144)
        posts = [call for call in network.call_args_list if call.args[0] == "POST"]
        self.assertEqual(len(posts), 16)
        self.assertEqual([call.args[3]["X-Request-Id"] for call in posts],
                         [entry["request_id"] for entry in self.plan.requests])
        self.assertEqual(result["post_count"], 16)
        for entry in result["results"]:
            raw = (collector.output / entry["raw_response_path"]).read_bytes()
            request = (collector.output / entry["request_body_path"]).read_bytes()
            self.assertEqual(hashlib.sha256(raw).hexdigest(), entry["raw_response_sha256"])
            self.assertEqual(hashlib.sha256(request).hexdigest(), entry["request_body_sha256"])
            for suffix, state in (("intent", "intent"), ("consumed", "consumed_before_io"),
                                  ("started", "started_before_io"), ("terminal", "success")):
                record = json.loads((collector.ledger.directory / f'{entry["ordinal"]:02d}-{suffix}.json').read_bytes())
                self.assertEqual(record["state"], state)
        self.assertEqual(json.loads((collector.ledger.directory / "terminal.json").read_bytes())["state"], "success")
        self.assert_code("output_already_exists", subject.Collector, self.plan)

    def test_client_consumed_before_io_failure_then_cannot_resume(self):
        collector = subject.Collector(self.plan)

        def network(method, path, body, headers, **kwargs):
            if method == "POST":
                self.assertTrue((collector.ledger.directory / "01-consumed.json").exists())
                raise subject.DiagnosticError("transport_failure")
            return self.fake_http(method, path, body, headers, **kwargs)

        with mock.patch.object(subject, "http_once", side_effect=network) as transport:
            self.assert_code("transport_failure", collector.run)
            self.assertEqual(len(transport.call_args_list), 5)
            self.assert_code("run_already_consumed", collector.run)
            self.assertEqual(len(transport.call_args_list), 5)
        terminal = json.loads((collector.output / "terminal.json").read_bytes())
        self.assertEqual(terminal["consumed_post_count"], 1)
        self.assertEqual(terminal["completed_results"], 0)
        self.assertFalse((collector.output / "native-results.json").exists())
        for name in ("01-terminal.json", "terminal.json"):
            record = json.loads((collector.ledger.directory / name).read_bytes())
            self.assertEqual(record["state"], "failed")
            self.assertEqual(record["code"], "transport_failure")

    def test_redirect_and_invalid_reply_stop_without_follow_or_retry(self):
        for number, fault in enumerate(("redirect", "reply", "headers")):
            self.plan = self.make_plan("response-fault-" + str(number))
            collector = subject.Collector(self.plan)

            def network(method, path, body, headers, **kwargs):
                status, returned, raw = self.fake_http(method, path, body, headers, **kwargs)
                if method == "POST":
                    if fault == "redirect": return 302, returned + [("Location", "https://example.invalid")], b""
                    if fault == "reply": return 200, returned, b"{}"
                    if fault == "headers": return 200, returned + [(subject.PLAN_HEADER.lower(), self.plan.sha)], raw
                return status, returned, raw

            with mock.patch.object(subject, "http_once", side_effect=network) as transport:
                with self.assertRaises(subject.DiagnosticError): collector.run()
                self.assertEqual(len(transport.call_args_list), 5)

    def test_direct_http_has_no_proxy_redirect_or_retry_code(self):
        connection = mock.Mock()
        response = mock.Mock(status=302)
        response.read.return_value = b"redirect"
        response.getheaders.return_value = [("Location", "https://example.invalid")]
        connection.getresponse.return_value = response
        context = ssl.create_default_context()
        with mock.patch.object(subject.http.client, "HTTPSConnection", return_value=connection) as ctor:
            status, _, raw = subject.http_once("POST", "/v1/rerank", b"{}", {}, port=19482,
                                               timeout=300, context=context)
        ctor.assert_called_once_with("127.0.0.1", 19482, timeout=300, context=context)
        self.assertEqual(status, 302)
        self.assertEqual(raw, b"redirect")
        connection.putrequest.assert_called_once_with("POST", "/v1/rerank", skip_accept_encoding=True)
        connection.close.assert_called_once()


if __name__ == "__main__":
    unittest.main()
