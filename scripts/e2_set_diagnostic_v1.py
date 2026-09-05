#!/usr/bin/env python3
"""Pin-first, single-use HTTPS transport for the accepted 0031 diagnostic.

No model is imported. A consumed or failed run cannot resume. This module is
separate from the immutable v3 capability transport and its consumed ledgers.
"""
from __future__ import annotations

import argparse
from decimal import Decimal
import hashlib
import http.client
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
from pathlib import Path
import re
import ssl
import stat
import struct
import subprocess
import time

from scripts.e2_reranker_https_v3 import backend_identity as _legacy_backend_identity
from scripts.e2_reranker_live_smoke_v3 import native_counters
from scripts.e2_set_tokenizer_v1 import (
    BODY_MIDDLE, BODY_PREFIX, CAPABILITY_DOCUMENT, CAPABILITY_QUERY,
    INSTRUCTION, PREFIX, SUFFIX, encode_documents,
)

CASE_IDS = ("capability_raw", "partial_support", "wrong_subject", "wrong_aspect", "none_32",
            "support_1", "support_16", "support_32")
PLAN_HEADER = "X-IMMS-Set-Plan-SHA256"
DEPLOYMENT_HEADER = "X-IMMS-Set-Deployment-SHA256"
ORIGIN = "https://127.0.0.1:19482"
CONTAINER = "imms-gb10-set-reranker-20260905"
MAX_ARTIFACT = 32 << 20
MAX_RESPONSE = 1 << 20
GET_PATHS = ("/v1/models", "/version", "/health", "/metrics")
PROFILE_ENV = "IMMS_SET_TOKENIZER_PROFILE"
PROFILE = "imms-set-native-v1"


class DiagnosticError(ValueError):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


def fail(code):
    raise DiagnosticError(code) from None


def canonical(value):
    try:
        return (json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"),
                           allow_nan=False) + "\n").encode("utf-8")
    except (TypeError, ValueError, UnicodeError):
        fail("json_invalid")


def _pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            fail("json_duplicate_key")
        result[key] = value
    return result


def _constant(_value):
    fail("json_invalid")


class _DecimalLexeme(Decimal):
    def __new__(cls, value):
        result = super().__new__(cls, value)
        result.lexeme = value
        return result


def decode(raw, *, canonical_required=False, decimal=False):
    try:
        result = json.loads(raw.decode("utf-8"), object_pairs_hook=_pairs,
                            parse_constant=_constant, parse_float=_DecimalLexeme if decimal else float)
    except DiagnosticError:
        raise
    except (ValueError, TypeError, UnicodeError, RecursionError):
        fail("json_invalid")
    if canonical_required and canonical(result) != raw:
        fail("json_noncanonical")
    return result


def _object(value, keys, code):
    if type(value) is not dict or set(value) != set(keys):
        fail(code)


def _sha(value):
    return type(value) is str and re.fullmatch("[0-9a-f]{64}", value) is not None


def _absolute(value):
    if type(value) is not str or not Path(value).is_absolute() or "\x00" in value:
        fail("path_invalid")
    return Path(value)


def read_pin(path, sha256, size):
    """Bounded no-follow raw pin verification, strictly before any JSON decode."""
    if not _sha(sha256) or type(size) is not int or not 0 < size <= MAX_ARTIFACT:
        fail("pin_invalid")
    try:
        descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(descriptor, "rb") as source:
            before = os.fstat(source.fileno())
            if not stat.S_ISREG(before.st_mode) or before.st_size != size:
                fail("pin_mismatch")
            raw = source.read(size + 1)
            after = os.fstat(source.fileno())
        if (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (
                after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns):
            fail("pin_mismatch")
    except OSError:
        fail("pin_io")
    if len(raw) != size or hashlib.sha256(raw).hexdigest() != sha256:
        fail("pin_mismatch")
    return raw


def artifact_pin(pin, *, is_json=True, canonical_required=True):
    _object(pin, ("path", "sha256", "size_bytes"), "pin_invalid")
    raw = read_pin(_absolute(pin["path"]), pin["sha256"], pin["size_bytes"])
    return (decode(raw, canonical_required=canonical_required) if is_json else raw), raw


def backend_identity(name):
    """Bind the one non-secret profile switch without modifying the v3 helper."""
    backend = _legacy_backend_identity(name)
    # Docker formats only the selected public setting, so other environment
    # values (which may be credentials) never enter this process or evidence.
    template = '{{range .Config.Env}}{{if eq (index (split . "=") 0) "IMMS_SET_TOKENIZER_PROFILE"}}{{.}}{{end}}{{end}}'
    selected = subprocess.check_output(["docker", "inspect", "--format", template, backend["container_id"]], timeout=10)
    if selected != (PROFILE_ENV + "=" + PROFILE + "\n").encode("ascii"):
        fail("backend_profile")
    return {**backend, "set_tokenizer_profile": PROFILE}


def token_sha(ids):
    if type(ids) is not list or not 0 < len(ids) <= 8192 or any(
            type(i) is not int or not 0 <= i <= 0xffffffff for i in ids):
        fail("token_ids_invalid")
    return hashlib.sha256(struct.pack(">I", len(ids))
                          + b"".join(struct.pack(">I", i) for i in ids)).hexdigest()


def request_id(case_id, repeat):
    return f"imms-gb10-set-diagnostic-v1-{case_id}-{repeat:02d}"


def request_body(query, document):
    return {"documents": [document], "instruction": INSTRUCTION, "max_tokens_per_doc": 0,
            "max_tokens_per_query": 0, "model": "sparkclaw-reranker", "priority": 0,
            "query": query, "top_n": 1, "truncate_prompt_tokens": None,
            "truncation_side": None, "use_activation": True}


class Plan:
    def __init__(self, path, expected_sha256, expected_size):
        self.raw = read_pin(path, expected_sha256, expected_size)
        self.sha = expected_sha256
        self.value = decode(self.raw, canonical_required=True)
        p = self.value
        _object(p, ("artifact", "version", "inputs", "native", "reference"), "plan_shape")
        if (p["artifact"] != "imms-gb10-set-diagnostic-prereg" or type(p["version"]) is not int
                or p["version"] != 1 or type(p["reference"]) is not dict):
            fail("plan_shape")
        n = self.native = p["native"]
        _object(n, ("token_file", "deployment_record", "ca_file", "container", "origin", "backend_port",
                    "expected_requests", "timeout_seconds", "source_pin", "ledger_directory",
                    "boundary_ledger_directory", "output_directory", "requests"), "native_shape")
        if (n["container"] != CONTAINER or n["origin"] != ORIGIN or type(n["backend_port"]) is not int
                or n["backend_port"] != 18484 or type(n["expected_requests"]) is not int
                or n["expected_requests"] != 16 or type(n["timeout_seconds"]) is not int
                or n["timeout_seconds"] != 300):
            fail("native_profile")
        _object(n["source_pin"], ("sha256", "size_bytes"), "source_pin")
        read_pin(Path(__file__), n["source_pin"]["sha256"], n["source_pin"]["size_bytes"])
        self.directories = [_absolute(n[key]) for key in
                            ("ledger_directory", "boundary_ledger_directory", "output_directory")]
        if len(set(self.directories)) != 3:
            fail("directory_overlap")
        # These exact external bytes may come from Go struct-order encoding.
        # Their SHA/size remain authoritative; canonical ordering is a plan and
        # deployment-record rule, not a reason to rewrite approved input bytes.
        self.inputs, _ = artifact_pin(p["inputs"], canonical_required=False)
        self.tokens, _ = artifact_pin(n["token_file"], canonical_required=False)
        self.record, self.record_raw = artifact_pin(n["deployment_record"])
        self.ca, _ = artifact_pin(n["ca_file"], is_json=False)
        self.deployment_sha = n["deployment_record"]["sha256"]
        if (type(self.record) is not dict or not {"backend", "proxy_source_sha256", "https_port",
                "backend_port", "tls_ca_sha256"}.issubset(self.record)
                or self.record["proxy_source_sha256"] != n["source_pin"]["sha256"]
                or self.record["https_port"] != 19482 or self.record["backend_port"] != 18484
                or self.record["tls_ca_sha256"] != n["ca_file"]["sha256"]
                or type(self.record["backend"]) is not dict):
            fail("deployment_record")
        backend = self.record["backend"]
        if backend.get("set_tokenizer_profile") != PROFILE:
            fail("deployment_profile")
        argv = (backend.get("entrypoint") or []) + (backend.get("cmd") or [])
        if (type(argv) is not list or argv.count("--no-enable-prefix-caching") != 1
                or "--enable-prefix-caching" in argv or backend.get("readonly_rootfs") is not True
                or backend.get("user") not in ("1000", "1000:1000")
                or "ALL" not in backend.get("cap_drop", [])
                or not any(option in ("no-new-privileges", "no-new-privileges:true")
                           for option in backend.get("security_opt", []))):
            fail("deployment_profile")
        self._validate_inputs()

    def _validate_inputs(self):
        _object(self.inputs, ("artifact", "version", "cases"), "inputs_shape")
        if (self.inputs["artifact"] != "imms-gb10-set-diagnostic-inputs"
                or type(self.inputs["version"]) is not int or self.inputs["version"] != 1):
            fail("inputs_shape")
        if (type(self.tokens) is not dict or self.tokens.get("artifact") != "imms-gb10-set-native-tokens"
                or type(self.tokens.get("version")) is not int or self.tokens.get("version") != 1):
            fail("tokens_shape")
        inputs, tokens = self.inputs["cases"], self.tokens.get("cases")
        if (type(inputs) is not list or type(tokens) is not list or len(inputs) != 8 or len(tokens) != 8):
            fail("case_inventory")
        expected = []
        for case_id, entry, token in zip(CASE_IDS, inputs, tokens, strict=True):
            _object(entry, ("id", "kind", "query", "document"), "input_case")
            if (entry["id"] != case_id or type(entry["query"]) is not str or not entry["query"]
                    or type(entry["document"]) is not str or "\ufeff" in entry["query"] + entry["document"]):
                fail("input_case")
            if case_id == "capability_raw":
                if (entry["kind"] != "capability_raw" or entry["query"] != CAPABILITY_QUERY
                        or entry["document"] != CAPABILITY_DOCUMENT):
                    fail("input_case")
            else:
                value = decode(entry["document"].encode("utf-8"))
                _object(value, ("documents",), "input_document")
                if entry["kind"] != "ordered_set" or encode_documents(value["documents"]) != entry["document"]:
                    fail("input_document")
            if type(token) is not dict or token.get("id") != case_id:
                fail("token_case")
            ids = token.get("token_ids")
            digest = token_sha(ids)
            rendered = PREFIX + BODY_PREFIX + entry["query"] + BODY_MIDDLE + entry["document"] + SUFFIX
            if (type(token.get("token_count")) is not int or token.get("token_count") != len(ids)
                    or token.get("rendered_utf8") != rendered
                    or ("token_ids_sha256" in token and token["token_ids_sha256"] != digest)):
                fail("token_case")
            for repeat in (1, 2):
                expected.append({"ordinal": len(expected) + 1, "case_id": case_id, "repeat": repeat,
                                 "request_id": request_id(case_id, repeat),
                                 "body": request_body(entry["query"], entry["document"]),
                                 "token_count": len(ids), "token_ids_sha256": digest})
        if canonical(self.native["requests"]) != canonical(expected):
            fail("request_plan")
        self.requests = expected

    def verify_backend(self):
        try:
            current = backend_identity(self.native["container"])
        except Exception:
            fail("backend_unavailable")
        if current != self.record["backend"]:
            fail("backend_identity")


def durable_new(path, raw):
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "wb") as output:
            output.write(raw)
            output.flush()
            os.fsync(output.fileno())
        fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    except OSError:
        fail("durable_write")


def fresh_directory(path, code):
    try:
        path.mkdir(mode=0o700)
        # Persist the directory entry itself as well as later ledger files.
        fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    except OSError:
        fail(code)


class Ledger:
    def __init__(self, directory, plan):
        self.directory, self.plan, self.next_ordinal, self.failed = Path(directory), plan, 1, False
        self.pending, self.completed = None, False
        fresh_directory(self.directory, "run_already_claimed")
        durable_new(self.directory / "intent.json", canonical({"plan_sha256": plan.sha, "state": "intent", "planned_post_count": 16}))
        durable_new(self.directory / "started.json", canonical({"plan_sha256": plan.sha, "state": "started"}))

    def consume(self, entry):
        if self.failed or entry["ordinal"] != self.next_ordinal:
            fail("request_order")
        if self.completed or self.pending is not None:
            fail("request_pending_or_complete")
        self.pending = entry
        facts = {
            "plan_sha256": self.plan.sha, "ordinal": entry["ordinal"], "request_id": entry["request_id"],
            "request_sha256": hashlib.sha256(canonical(entry["body"])).hexdigest()}
        durable_new(self.directory / f'{entry["ordinal"]:02d}-intent.json', canonical({**facts, "state": "intent"}))
        durable_new(self.directory / f'{entry["ordinal"]:02d}-consumed.json', canonical({**facts, "state": "consumed_before_io"}))
        durable_new(self.directory / f'{entry["ordinal"]:02d}-started.json', canonical({**facts, "state": "started_before_io"}))
        self.next_ordinal += 1

    def complete_request(self, entry, **facts):
        if self.failed or self.completed or self.pending != entry:
            fail("request_terminal_state")
        durable_new(self.directory / f'{entry["ordinal"]:02d}-terminal.json', canonical({
            "plan_sha256": self.plan.sha, "ordinal": entry["ordinal"], "request_id": entry["request_id"],
            "state": "success", **facts}))
        self.pending = None

    def finish(self):
        if self.failed or self.pending is not None or self.next_ordinal != 17 or self.completed:
            fail("run_terminal_state")
        durable_new(self.directory / "terminal.json", canonical({"plan_sha256": self.plan.sha, "state": "success", "post_count": 16}))
        self.completed = True

    def stop(self, code):
        if self.failed or self.completed:
            return
        self.failed = True
        if self.pending is not None:
            entry = self.pending
            durable_new(self.directory / f'{entry["ordinal"]:02d}-terminal.json', canonical({
                "plan_sha256": self.plan.sha, "ordinal": entry["ordinal"], "request_id": entry["request_id"], "state": "failed", "code": code}))
            self.pending = None
        terminal = {"plan_sha256": self.plan.sha, "state": "failed", "code": code, "consumed_post_count": self.next_ordinal - 1}
        durable_new(self.directory / "failed.json", canonical(terminal))
        durable_new(self.directory / "terminal.json", canonical(terminal))


def headers_unique(items):
    result = {}
    for name, value in items:
        key = name.lower()
        if key in result:
            fail("duplicate_header")
        result[key] = value
    return result


def validate_reply(entry, raw):
    response = decode(raw, decimal=True)
    _object(response, ("id", "model", "results", "usage"), "response_shape")
    if response["id"] != "score-" + entry["request_id"] or response["model"] != "sparkclaw-reranker":
        fail("response_identity")
    _object(response["usage"], ("prompt_tokens", "total_tokens"), "response_usage")
    if any(type(value) is not int or value != entry["token_count"] for value in response["usage"].values()):
        fail("response_usage")
    if type(response["results"]) is not list or len(response["results"]) != 1:
        fail("response_shape")
    result = response["results"][0]
    _object(result, ("index", "document", "relevance_score"), "response_shape")
    if type(result["index"]) is not int or result["index"] != 0:
        fail("response_shape")
    _object(result["document"], ("text", "multi_modal"), "response_document")
    if result["document"] != {"text": entry["body"]["documents"][0], "multi_modal": None}:
        fail("response_document")
    score = result["relevance_score"]
    if type(score) not in (int, _DecimalLexeme) or not Decimal(score).is_finite() or not 0 <= score <= 1:
        fail("response_score")
    # Retain the exact decimal token, including exponent spelling and trailing
    # zeros. The float32 conversion is an observation, not quality authority.
    value = float(score)
    return {"p_yes": score.lexeme if isinstance(score, _DecimalLexeme) else str(score),
            "score_float64_bits": struct.pack(">d", value).hex(),
            "score_float32_bits": struct.pack(">f", value).hex()}


def http_once(method, path, body, headers, *, port, timeout, context=None):
    """Direct literal-loopback connection: no proxy, redirect or retry path."""
    connection = (http.client.HTTPSConnection("127.0.0.1", port, timeout=timeout, context=context)
                  if context is not None else http.client.HTTPConnection("127.0.0.1", port, timeout=timeout))
    try:
        connection.putrequest(method, path, skip_accept_encoding=True)
        for key, value in headers.items():
            connection.putheader(key, value)
        if body is not None:
            connection.putheader("Content-Length", str(len(body)))
        connection.endheaders(body)
        response = connection.getresponse()
        raw = response.read(MAX_RESPONSE + 1)
        if len(raw) > MAX_RESPONSE:
            fail("response_too_large")
        return response.status, response.getheaders(), raw
    except DiagnosticError:
        raise
    except Exception:
        fail("transport_failure")
    finally:
        connection.close()


class BoundaryState:
    def __init__(self, plan):
        self.plan = plan
        plan.verify_backend()
        self.ledger = Ledger(plan.native["boundary_ledger_directory"], plan)

    def forward(self, method, path, header_items, body):
        try:
            if self.ledger.failed:
                fail("run_failed")
            headers = headers_unique(header_items)
            if "transfer-encoding" in headers:
                fail("transfer_encoding")
            if headers.get("host") != "127.0.0.1:19482":
                fail("request_host")
            if method == "GET":
                if path not in GET_PATHS or body or set(headers) - {"host", "content-length"}:
                    fail("route_invalid")
                entry, forward_headers = None, {}
            elif method == "POST" and path == "/v1/rerank":
                if set(headers) != {"host", "content-length", "content-type", "x-request-id"}:
                    fail("request_headers")
                if self.ledger.next_ordinal > 16:
                    fail("run_complete")
                entry = self.plan.requests[self.ledger.next_ordinal - 1]
                if (headers["host"] != "127.0.0.1:19482" or headers["content-type"] != "application/json"
                        or headers["content-length"] != str(len(body))
                        or headers["x-request-id"] != entry["request_id"] or body != canonical(entry["body"])):
                    fail("request_mismatch")
                forward_headers = {"Content-Type": "application/json", "X-Request-Id": entry["request_id"]}
            else:
                fail("route_invalid")
            self.plan.verify_backend()
            if entry is not None:
                self.ledger.consume(entry)
            status, response_headers, raw = http_once(method, path, body if entry else None, forward_headers,
                port=self.plan.native["backend_port"], timeout=self.plan.native["timeout_seconds"])
            self.plan.verify_backend()
            response_headers = headers_unique(response_headers)
            if status != 200:
                self.ledger.stop("upstream_status")
            elif entry is not None:
                try:
                    validate_reply(entry, raw)
                    self.ledger.complete_request(entry, response_status=status, raw_response_sha256=hashlib.sha256(raw).hexdigest(), raw_response_size_bytes=len(raw))
                except DiagnosticError as error:
                    self.ledger.stop(error.code)
            elif path == "/metrics" and self.ledger.next_ordinal == 17 and not self.ledger.completed:
                # The fixed collector's final post-request observation ends at
                # /metrics. Do not mark the boundary complete before that GET.
                self.ledger.finish()
            return status, {"Content-Type": response_headers.get("content-type", "application/octet-stream"),
                            PLAN_HEADER: self.plan.sha, DEPLOYMENT_HEADER: self.plan.deployment_sha}, raw
        except DiagnosticError as error:
            self.ledger.stop(error.code)
            raise
        except Exception:
            self.ledger.stop("boundary_failure")
            fail("boundary_failure")


class BoundaryHandler(BaseHTTPRequestHandler):
    server_version = "IMMSSetDiagnostic"
    sys_version = ""

    def setup(self):
        super().setup()
        self.connection.settimeout(10)

    def log_message(self, *_args):
        pass

    def do_GET(self):
        self._forward("GET")

    def do_POST(self):
        self._forward("POST")

    def do_PUT(self):
        self._forward("PUT")

    do_DELETE = do_PATCH = do_OPTIONS = do_HEAD = do_PUT

    def _forward(self, method):
        try:
            headers = headers_unique(self.headers.items())
            length = headers.get("content-length", "0")
            if not re.fullmatch("0|[1-9][0-9]*", length) or len(length) > 8 or int(length) > MAX_RESPONSE:
                fail("request_length")
            body = self.rfile.read(int(length))
            status, response_headers, raw = self.server.state.forward(method, self.path, self.headers.items(), body)
        except DiagnosticError as error:
            self.server.state.ledger.stop(error.code)
            status, response_headers, raw = 409, {}, b""
        except Exception:
            self.server.state.ledger.stop("boundary_failure")
            status, response_headers, raw = 503, {}, b""
        self.send_response(status)
        for key, value in response_headers.items():
            self.send_header(key, value)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


class Collector:
    def __init__(self, plan):
        self.plan = plan
        # This context validates the pinned CA and the literal IP SAN. There is
        # no verify=False, custom dialer, alternate origin, or proxy support.
        try:
            self.context = ssl.create_default_context(cadata=plan.ca.decode("ascii"))
        except Exception:
            fail("tls_configuration")
        self.output = Path(plan.native["output_directory"])
        fresh_directory(self.output, "output_already_exists")
        self.ledger = Ledger(plan.native["ledger_directory"], plan)

    def request(self, label, method, path, *, entry=None):
        body = canonical(entry["body"]) if entry is not None else None
        headers = {"Content-Type": "application/json", "X-Request-Id": entry["request_id"]} if entry else {}
        if entry is not None:
            durable_new(self.output / (label + ".request.raw"), body)
            self.ledger.consume(entry)
        status, returned, raw = http_once(method, path, body, headers, port=19482,
                                         timeout=self.plan.native["timeout_seconds"], context=self.context)
        durable_new(self.output / (label + ".raw"), raw)
        durable_new(self.output / (label + ".http.json"), canonical({"status": status, "headers": returned}))
        response_headers = headers_unique(returned)
        if (response_headers.get(PLAN_HEADER.lower()) != self.plan.sha
                or response_headers.get(DEPLOYMENT_HEADER.lower()) != self.plan.deployment_sha):
            fail("response_header_identity")
        if status != 200:
            fail("response_status")
        return raw

    def observe(self, ordinal, phase):
        observed = {}
        for path in GET_PATHS:
            name = path.rsplit("/", 1)[-1]
            raw = self.request(f"{ordinal:02d}-{phase}-{name}", "GET", path)
            if name == "models":
                models = decode(raw)
                if type(models) is not dict or type(models.get("data")) is not list or len(models["data"]) != 1:
                    fail("models_identity")
                model = models["data"][0]
                expected = {"id": "sparkclaw-reranker", "root": "Qwen/Qwen3-Reranker-4B",
                            "max_model_len": 8192, "owned_by": "vllm"}
                if type(model) is not dict or any(model.get(k) != v for k, v in expected.items()):
                    fail("models_identity")
            elif name == "version":
                if decode(raw) != {"version": "0.23.0"}:
                    fail("version_identity")
            elif name == "health":
                if raw != b"":
                    fail("health_identity")
            else:
                try:
                    observed = native_counters(raw)
                except Exception:
                    fail("metrics_invalid")
        return {"configuration_source": "reviewed_deployment_configuration", "prefix_cache_enabled": False,
                "metric_labels": {"engine": "0", "model_name": "sparkclaw-reranker"}, **observed}

    def run(self):
        if self.ledger.failed or self.ledger.next_ordinal != 1:
            fail("run_already_consumed")
        results, last = [], None
        try:
            for entry in self.plan.requests:
                before = self.observe(entry["ordinal"], "pre")
                if last and any(before[key] < last[key] for key in
                                ("prefix_cache_queries_total", "prefix_cache_hits_total")):
                    fail("cache_counter_regression")
                begin = time.monotonic()
                label = f'{entry["ordinal"]:02d}-rerank'
                raw = self.request(label, "POST", "/v1/rerank", entry=entry)
                elapsed = time.monotonic() - begin
                score = validate_reply(entry, raw)
                after = self.observe(entry["ordinal"], "post")
                if any(after[key] < before[key] for key in ("prefix_cache_queries_total", "prefix_cache_hits_total")):
                    fail("cache_counter_regression")
                last = after
                result = {k: entry[k] for k in ("ordinal", "case_id", "repeat", "request_id", "token_count", "token_ids_sha256")}
                result.update(score)
                result.update({"elapsed_seconds": format(elapsed, ".9f"), "raw_response_path": label + ".raw",
                    "request_body_path": label + ".request.raw",
                    "raw_response_sha256": hashlib.sha256(raw).hexdigest(), "raw_response_size_bytes": len(raw),
                    "request_body_sha256": hashlib.sha256(canonical(entry["body"])).hexdigest(),
                    "request_body_size_bytes": len(canonical(entry["body"])), "cache_before": before, "cache_after": after,
                    "thermal_class": "first_served_not_device_cold" if entry["ordinal"] == 1 else "service_warm"})
                durable_new(self.output / f'{entry["ordinal"]:02d}-result.json', canonical(result))
                self.ledger.complete_request(entry, result_sha256=hashlib.sha256(canonical(result)).hexdigest(), result_size_bytes=len(canonical(result)))
                results.append(result)
            result = {"artifact": "imms-gb10-set-native-results", "version": 1, "status": "success",
                      "plan_sha256": self.plan.sha, "deployment_sha256": self.plan.deployment_sha,
                      "post_count": len(results), "retry_count": 0, "authority": "unissued", "results": results}
            durable_new(self.output / "native-results.json", canonical(result))
            self.ledger.finish()
            return result
        except Exception as error:
            code = error.code if isinstance(error, DiagnosticError) else "collector_failure"
            self.ledger.stop(code)
            durable_new(self.output / "terminal.json", canonical({"state": "failed", "code": code,
                "plan_sha256": self.plan.sha,
                "completed_results": len(results), "consumed_post_count": self.ledger.next_ordinal - 1}))
            fail(code)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("validate-plan", "boundary", "collect"))
    parser.add_argument("--prereg", type=Path, required=True)
    parser.add_argument("--expected-sha256", required=True)
    parser.add_argument("--expected-size", type=int, required=True)
    parser.add_argument("--cert-file", type=Path)
    parser.add_argument("--key-file", type=Path)
    args = parser.parse_args()
    state = None
    try:
        plan = Plan(args.prereg, args.expected_sha256, args.expected_size)
        if args.mode == "boundary":
            if args.cert_file is None or args.key_file is None:
                fail("tls_files_required")
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.minimum_version = ssl.TLSVersion.TLSv1_2
            context.load_cert_chain(args.cert_file, args.key_file)
            state = BoundaryState(plan)
            server = HTTPServer(("127.0.0.1", 19482), BoundaryHandler)
            server.state = state
            server.socket = context.wrap_socket(server.socket, server_side=True)
            server.serve_forever()
        elif args.mode == "collect":
            Collector(plan).run()
        else:
            print(json.dumps({"status": "plan_valid", "planned_post_count": 16, "authority": "unissued"}))
        return 0
    except Exception as error:
        if state is not None:
            state.ledger.stop(error.code if isinstance(error, DiagnosticError) else "boundary_failure")
        print(error.code if isinstance(error, DiagnosticError) else "operator_failure")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
