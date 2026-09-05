#!/usr/bin/env python3
"""Policy-gated 3849-request release bundle. No model imports or retry path.

The frozen calibration module is imported only after this new source and its
complete dependency closure have passed external raw SHA/size verification.
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import ssl
import stat
import sys
import types

ROOT = Path("/home/infinimesh/imms-debug/20260905")
EPOCH, PHASE = "gb10-native-set-admission-v1", "release-bundle"
STREAMS = (("heldout", 640), ("old100", 3200), ("injection", 9))
COUNT, LIMIT = 3849, 128 << 20
POLICY_PIN = {"sha256": "cb2b33040451990586ef053bf79a684cc2cdbee4fe745d92491a5e692894da85", "size_bytes": 2406}
POLICY_COMMIT = "b075a37c49bf08fcf0ec1ffbb37d0670b789388b"
CALIBRATION_PREREG_PIN = {"sha256": "a5d6d07c1575ad7ccb94ded15e82c91ed5f274b12f7301541e8fe0f1da4ac320", "size_bytes": 146100}
DEPLOYMENT_PIN = {"sha256": "7e0797f17d36afd12f62b756f8dcb0fa33e4517074e90b348d8bbbe06c021e79", "size_bytes": 5058}
CA_PIN = {"sha256": "c7a7c4dca65042bf3faf1e58845aceb240d9ed30a27d875a7878f6a7c9031320", "size_bytes": 1570}
FORMAL_PIN = {"sha256": "ea9008c22f1cc7508f95753426fadca8696eda46ac6ff2ff91603600ec905631", "size_bytes": 54228}
TOKENIZER_MODULE_PIN = {"sha256": "218be8196402e4eb85420e954809c2a78af5ab5ce8f1ec2bea4277f45fb259e7", "size_bytes": 10177}
EMPTY_PIN = {"sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "size_bytes": 0}
PREFIX = "imms-gb10-native-set-release-"


class ReleaseError(ValueError):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


def require(condition, code):
    if not condition:
        raise ReleaseError(code) from None


def canonical(value):
    try:
        return (json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":"), allow_nan=False) + "\n").encode("utf-8")
    except (ValueError, TypeError, UnicodeError, RecursionError):
        raise ReleaseError("json_invalid") from None


def _pairs(pairs):
    value = {}
    for key, item in pairs:
        require(key not in value, "json_duplicate_key")
        value[key] = item
    return value


def decode(raw, *, canonical_required=False):
    require(type(raw) is bytes and len(raw) <= LIMIT and not raw.startswith(b"\xef\xbb\xbf"), "json_invalid")
    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=_pairs, parse_constant=lambda _: require(False, "json_invalid"))
        encoded = canonical(value)
        require(not canonical_required or encoded == raw, "json_noncanonical")
        return value
    except ReleaseError:
        raise
    except (ValueError, TypeError, UnicodeError, RecursionError):
        raise ReleaseError("json_invalid") from None


def obj(value, keys, code="artifact_shape"):
    require(type(value) is dict and set(value) == set(keys), code)


def digest(raw):
    return {"sha256": hashlib.sha256(raw).hexdigest(), "size_bytes": len(raw)}


def commit(value):
    return type(value) is str and re.fullmatch(r"[0-9a-f]{40}", value) is not None


def bootstrap_read(path, identity, *, empty=False):
    """Small bootstrap only; no dependency module is imported before its pin."""
    obj(identity, ("sha256", "size_bytes"), "pin_invalid")
    require(type(identity["sha256"]) is str and re.fullmatch(r"[0-9a-f]{64}", identity["sha256"]) is not None and type(identity["size_bytes"]) is int and (0 if empty else 1) <= identity["size_bytes"] <= LIMIT, "pin_invalid")
    value = os.fspath(path)
    require(type(value) is str and value.startswith("/") and "\x00" not in value and all(p and p not in (".", "..") for p in value.split("/")[1:]), "path_invalid")
    parent = None
    try:
        parent = os.open("/", os.O_RDONLY | os.O_DIRECTORY)
        parts = value.split("/")[1:]
        for part in parts[:-1]:
            next_fd = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent)
            os.close(parent)
            parent = next_fd
        fd = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
        with os.fdopen(fd, "rb") as source:
            before = os.fstat(source.fileno())
            require(stat.S_ISREG(before.st_mode) and before.st_nlink == 1 and before.st_size == identity["size_bytes"], "pin_file")
            raw = source.read(identity["size_bytes"] + 1)
            after = os.fstat(source.fileno())
        stamp = lambda s: (s.st_dev, s.st_ino, s.st_size, s.st_mtime_ns, s.st_ctime_ns)
        require(stamp(before) == stamp(after) and digest(raw) == identity, "pin_mismatch")
        return raw
    except OSError:
        raise ReleaseError("pin_io") from None
    finally:
        if parent is not None:
            os.close(parent)


def execute_pinned(name, path, raw):
    require(name not in sys.modules, "source_preloaded")
    module = types.ModuleType(name)
    module.__file__ = str(path)
    sys.modules[name] = module
    try:
        exec(compile(raw, str(path), "exec"), module.__dict__)
        return module
    finally:
        del sys.modules[name]


def load_helpers(source_files):
    names = ("__init__.py", "e2_set_formal_native_v1.py", "e2_set_release_native_v1.py", "e2_set_tokenizer_v1.py")
    require(type(source_files) is list and len(source_files) == 4, "source_closure")
    expected = {names[0]: EMPTY_PIN, names[1]: FORMAL_PIN, names[3]: TOKENIZER_MODULE_PIN}
    verified = {}
    for name, record in zip(names, source_files, strict=True):
        obj(record, ("path", "sha256", "size_bytes"), "source_closure")
        require(record["path"] == name, "source_closure")
        identity = {key: record[key] for key in ("sha256", "size_bytes")}
        require(name not in expected or identity == expected[name], "source_closure")
        path = Path(__file__).absolute().parent / name
        verified[name] = (path, bootstrap_read(path, identity, empty=name == "__init__.py"))
    helpers = execute_pinned("_imms_release_formal_pinned", *verified[names[1]])
    helpers.release_tokenizer = execute_pinned("_imms_release_tokenizer_pinned", *verified[names[3]])
    return helpers


def envelope(artifact_kind, **fields):
    return {"artifact": PREFIX + artifact_kind, "version": 1, "epoch": EPOCH, **fields}


def check_envelope(value, kind, keys, helpers):
    obj(value, ("artifact", "version", "epoch", *keys))
    require(value["artifact"] == PREFIX + kind and type(value["version"]) is int and value["version"] == 1 and value["epoch"] == EPOCH, "artifact_identity")
    if "phase" in keys:
        require(value["phase"] == PHASE, "phase_identity")
    if "provenance" in keys:
        require(canonical(value["provenance"]) == canonical(helpers.PROVENANCE), "provenance")


class Plan:
    def __init__(self, prereg, expected_sha256, expected_size, *, inputs, catalog, reference_tokens,
                 deployment_record, ca_file, policy, policy_replay, bge_terminal,
                 calibration_prereg=None, intent=None, intent_sha256=None, intent_size=None):
        self.prereg_pin = {"sha256": expected_sha256, "size_bytes": expected_size}
        p = self.value = decode(bootstrap_read(prereg, self.prereg_pin), canonical_required=True)
        obj(p, ("artifact", "version", "epoch", "phase", "input_pin", "catalog_pin", "reference_tokens_pin", "deployment_record_pin", "ca_pin", "policy_pin", "policy_commit", "policy_replay_pin", "bge_terminal_pin", "bge_terminal_commit", "source_files", "expected_posts", "request_plans", "provenance"))
        self.h = load_helpers(p["source_files"])
        check_envelope(p, "prereg", tuple(k for k in p if k not in ("artifact", "version", "epoch")), self.h)
        require(type(p["expected_posts"]) is int and p["expected_posts"] == COUNT and p["policy_pin"] == POLICY_PIN and p["policy_commit"] == POLICY_COMMIT and p["deployment_record_pin"] == DEPLOYMENT_PIN and p["ca_pin"] == CA_PIN and commit(p["bge_terminal_commit"]), "prerequisite_identity")
        paths = {"input": inputs, "catalog": catalog, "reference_tokens": reference_tokens, "deployment_record": deployment_record, "ca": ca_file, "policy": policy, "policy_replay": policy_replay, "bge_terminal": bge_terminal}
        # Complete raw pin closure before decoding any prerequisite/input body.
        raw = {key: self.h.read_pin(path, p[key + "_pin"]) for key, path in paths.items()}
        raw_calibration = self.h.read_pin(calibration_prereg or ROOT / "evidence/native-set-admission-v1/prereg.json", CALIBRATION_PREREG_PIN)
        self.ca = raw.pop("ca")
        self.inputs, self.catalog, self.reference = (decode(raw[k]) for k in ("input", "catalog", "reference_tokens"))
        self.deployment, self.policy, self.policy_replay, self.bge_terminal = (decode(raw[k], canonical_required=True) for k in ("deployment_record", "policy", "policy_replay", "bge_terminal"))
        self.calibration = decode(raw_calibration, canonical_required=True)
        self._prerequisites()
        self._catalog()
        self._requests()
        self.intent = self.intent_pin = None
        self.phase = PHASE
        self.runtime = ROOT / "runtime/native-set-admission-v1/release-bundle"
        self.output = ROOT / "evidence/native-set-admission-v1/release-bundle"
        if intent is not None:
            self.intent_pin = {"sha256": intent_sha256, "size_bytes": intent_size}
            self.intent = decode(self.h.read_pin(intent, self.intent_pin), canonical_required=True)
            self._intent()

    def _prerequisites(self):
        h, p, policy = self.h, self.value, self.policy
        # The physical deployment's transport pin still identifies the frozen
        # calibration helper, not this new release proxy. Preserve that fact.
        physical = object.__new__(h.Plan)
        physical.value, physical.deployment, physical.source_pin = {"ca_pin": CA_PIN}, self.deployment, FORMAL_PIN
        physical._deployment()
        require(policy["prereg_pin"] == CALIBRATION_PREREG_PIN and policy["cutoff_float32_bits"] == "3f7e3eb1" and policy["maximum_float32_bits"] == "3f7e3eb0" and policy["deployment_record_pin"] == DEPLOYMENT_PIN, "policy_identity")
        expected = envelope("policy-replay", policy_commit=POLICY_COMMIT, policy_pin=POLICY_PIN, prereg_pin=CALIBRATION_PREREG_PIN, phases=policy["phases"], replayed_posts=1280, repeat_cells_bits_exact=640, cutoff_float32_bits="3f7e3eb1", provenance=h.PROVENANCE, quality_pass=False, authority="unissued", counter="0/5")
        require(canonical(self.policy_replay) == canonical(expected), "policy_replay")
        b = self.bge_terminal
        obj(b, ("artifact", "version", "epoch", "phase", "status", "intent_pin", "started_pin", "policy_pin", "policy_commit", "deployment_pin", "vector_set_pin", "receipt_pin", "runtime_poststate_pin", "execution_commit", "execution_host", "consumed_posts", "completed_posts", "count_known", "retry_count", "failure_code", "provenance"), "bge_terminal")
        require(b["artifact"] == "imms-gb10-native-set-heldout-bge-terminal" and type(b["version"]) is int and b["version"] == 1 and b["epoch"] == EPOCH and b["phase"] == "heldout-bge-20" and b["status"] == "success" and b["policy_pin"] == POLICY_PIN and b["policy_commit"] == POLICY_COMMIT and b["execution_host"] == "local-development-cpu" and commit(b["execution_commit"]) and canonical(b["provenance"]) == canonical(h.PROVENANCE), "bge_terminal")
        require(type(b["consumed_posts"]) is int and type(b["completed_posts"]) is int and b["consumed_posts"] == b["completed_posts"] == 20 and type(b["retry_count"]) is int and b["retry_count"] == 0 and b["count_known"] is True and b["failure_code"] == "", "bge_terminal")
        for key in ("intent_pin", "started_pin", "deployment_pin", "vector_set_pin", "receipt_pin", "runtime_poststate_pin"):
            h.valid_pin(b[key])

    def _catalog(self):
        h = self.h
        keys = ("family", "cell_count", "source_pins", "provenance", "cells")
        for value, kind in ((self.inputs, "cell-inputs"), (self.catalog, "cell-catalog")):
            check_envelope(value, kind, keys, h)
            require(value["family"] == PHASE and type(value["cell_count"]) is int and value["cell_count"] == COUNT and type(value["cells"]) is list and len(value["cells"]) == COUNT, "cell_inventory")
        sources = self.inputs["source_pins"]
        require(type(sources) is list and 0 < len(sources) <= 128 and canonical(sources) == canonical(self.catalog["source_pins"]), "source_pins")
        roles = {}
        for record in sources:
            obj(record, ("role", "path", "sha256", "size_bytes"), "source_pins")
            role, path = record["role"], record["path"]
            require(type(role) is str and re.fullmatch(r"[a-z0-9_.-]+", role) is not None and role not in roles and type(path) is str and path and not path.startswith("/") and all(x not in ("", ".", "..") for x in path.split("/")), "source_pins")
            identity = {key: record[key] for key in ("sha256", "size_bytes")}
            h.valid_pin(identity)
            roles[role] = identity
        require(roles.get("native_policy") == POLICY_PIN and roles.get("heldout_bge_terminal") == self.value["bge_terminal_pin"], "source_prerequisites")
        require("heldout_bge_runtime_prestate" in roles, "source_prerequisites")
        for field, role in (("intent_pin", "heldout_bge_intent"), ("started_pin", "heldout_bge_started"), ("deployment_pin", "heldout_bge_deployment"), ("vector_set_pin", "heldout_bge_vectors"), ("receipt_pin", "heldout_bge_receipt"), ("runtime_poststate_pin", "heldout_bge_runtime_poststate")):
            require(roles.get(role) == self.bge_terminal[field], "source_prerequisites")
        r = self.reference
        check_envelope(r, "reference-tokens", ("input_pin", "reference_source_pin", "tokenizer_source_pin", "provenance", "cases"), h)
        require(r["input_pin"] == self.value["input_pin"] and r["reference_source_pin"] == h.REFERENCE_SOURCE_PIN and r["tokenizer_source_pin"] == h.TOKENIZER_PIN and type(r["cases"]) is list and len(r["cases"]) == COUNT, "reference_identity")
        formatter = h.release_tokenizer
        cell_keys = ("ordinal", "stream", "stream_ordinal", "cell_id", "query_id", "depth", "prefix_ids", "input_commitment_sha256", "body", "body_pin", "expected_yes")
        ordinal, seen = 0, set()
        for stream, count in STREAMS:
            previous = None
            for local in range(count):
                source, cell, reference = self.inputs["cells"][ordinal], self.catalog["cells"][ordinal], r["cases"][ordinal]
                obj(source, cell_keys, "input_cell")
                obj(cell, (*cell_keys, "token_count", "token_ids", "token_ids_sha256"), "catalog_cell")
                require(canonical(source) == canonical({key: cell[key] for key in cell_keys}), "catalog_input")
                require(type(cell["ordinal"]) is int and cell["ordinal"] == ordinal and cell["stream"] == stream and type(cell["stream_ordinal"]) is int and cell["stream_ordinal"] == local and type(cell["query_id"]) is str and cell["query_id"] and type(cell["cell_id"]) is str and cell["cell_id"] not in seen and h.sha(cell["input_commitment_sha256"]) and type(cell["expected_yes"]) is bool, "cell_order")
                seen.add(cell["cell_id"])
                depth = cell["depth"]
                require(h.integer(depth, 1, 32) and (stream != "heldout" or cell["expected_yes"] is False), "cell_truth")
                if stream != "injection":
                    require(depth == local % 32 + 1 and cell["cell_id"] == f"{stream}.{local // 32 + 1:04d}.depth.{depth:02d}", "cell_order")
                else:
                    require(cell["cell_id"] == "injection." + cell["query_id"] and cell["prefix_ids"] == [f'injection.{cell["query_id"]}.document.{n + 1}' for n in range(depth)], "injection_identity")
                prefix = cell["prefix_ids"]
                require(type(prefix) is list and len(prefix) == depth and all(type(x) is str and x for x in prefix) and len(set(prefix)) == depth, "prefix_ids")
                body = cell["body"]
                require(type(body) is dict and type(body.get("query")) is str and body["query"] and "\ufeff" not in body["query"] and type(body.get("documents")) is list and len(body["documents"]) == 1 and type(body["documents"][0]) is str, "request_body")
                wire = body["documents"][0]
                require(canonical(body) == canonical(h.request_body(body["query"], wire, formatter.INSTRUCTION)), "request_body")
                documents = decode(wire.encode("utf-8"))
                obj(documents, ("documents",), "request_document")
                require(formatter.encode_documents(documents["documents"]) == wire and len(documents["documents"]) == depth, "request_document")
                if stream != "injection" and depth > 1:
                    require(previous is not None and previous[0] == cell["query_id"] and previous[1] == body["query"] and prefix[:-1] == previous[2] and documents["documents"][:-1] == previous[3], "prefix_order")
                previous = (cell["query_id"], body["query"], prefix, documents["documents"])
                require(digest(canonical(body)) == cell["body_pin"] and len(canonical(body)) <= h.MAX_RESPONSE and h.token_sha(cell["token_ids"]) == cell["token_ids_sha256"] and type(cell["token_count"]) is int and cell["token_count"] == len(cell["token_ids"]), "input_tokens")
                require(all(token <= 151668 for token in cell["token_ids"]), "token_vocabulary")
                obj(reference, ("id", "kind", "rendered_utf8", "rendered_sha256", "rendered_size_bytes", "segments", "token_ids", "token_count", "untrusted_control_ids"), "reference_cell")
                values = [formatter.PREFIX, formatter.BODY_PREFIX, body["query"], formatter.BODY_MIDDLE, wire, formatter.SUFFIX]
                rendered = "".join(values)
                rendered_pin = digest(rendered.encode("utf-8"))
                require(reference["id"] == cell["cell_id"] and reference["kind"] == "ordered_set" and reference["rendered_utf8"] == rendered and reference["rendered_sha256"] == rendered_pin["sha256"] and type(reference["rendered_size_bytes"]) is int and reference["rendered_size_bytes"] == rendered_pin["size_bytes"] and reference["token_ids"] == cell["token_ids"] and type(reference["token_count"]) is int and reference["token_count"] == cell["token_count"] and reference["untrusted_control_ids"] == [], "reference_tokens")
                require(type(reference["segments"]) is list and len(reference["segments"]) == 6, "reference_segments")
                flat = []
                for i, (segment, name, text) in enumerate(zip(reference["segments"], ("prefix", "body_prefix", "query", "body_middle", "document", "suffix"), values, strict=True)):
                    obj(segment, ("name", "trusted", "utf8", "token_ids"), "reference_segments")
                    require(segment["name"] == name and segment["trusted"] is (i not in (2, 4)) and segment["utf8"] == text, "reference_segments")
                    h.token_sha(segment["token_ids"])
                    require(all(token <= 151668 for token in segment["token_ids"]), "token_vocabulary")
                    require(i not in (2, 4) or not set(segment["token_ids"]).intersection(range(151643, 151669)), "untrusted_control")
                    flat.extend(segment["token_ids"])
                require(flat == cell["token_ids"], "reference_segments")
                ordinal += 1

    def _requests(self):
        plans = self.value["request_plans"]
        require(type(plans) is list and len(plans) == 3, "request_plans")
        # The old raw prereg is pinned by the immutable policy. Request IDs may
        # differ between stages, but no accepted content POST is reused.
        used = {r["request_id"] for plan in self.calibration["request_plans"] for r in plan["requests"]}
        require(len(used) == 1280, "calibration_identity")
        self.requests, self.by_stream = [], {}
        for (stream, count), plan in zip(STREAMS, plans, strict=True):
            obj(plan, ("stream", "expected_posts", "requests"), "request_plans")
            require(plan["stream"] == stream and type(plan["expected_posts"]) is int and plan["expected_posts"] == count and type(plan["requests"]) is list and len(plan["requests"]) == count, "request_plans")
            entries = []
            for local, request in enumerate(plan["requests"]):
                obj(request, ("ordinal", "stream_ordinal", "cell_id", "request_id"), "request_plan")
                cell = self.catalog["cells"][len(self.requests)]
                require(type(request["ordinal"]) is int and request["ordinal"] == cell["ordinal"] and type(request["stream_ordinal"]) is int and request["stream_ordinal"] == local and request["cell_id"] == cell["cell_id"] and type(request["request_id"]) is str and re.fullmatch(r"[A-Za-z0-9._-]{1,128}", request["request_id"]) is not None and request["request_id"] not in used, "request_plan")
                used.add(request["request_id"])
                entry = {**cell, "request_id": request["request_id"]}
                entries.append(entry)
                self.requests.append(entry)
            self.by_stream[stream] = entries

    def _intent(self):
        i = self.intent
        check_envelope(i, "intent", ("phase", "prereg_pin", "prereg_commit", "policy_pin", "policy_commit", "bge_terminal_pin", "bge_terminal_commit", "expected_posts", "provenance"), self.h)
        require(i["prereg_pin"] == self.prereg_pin and commit(i["prereg_commit"]) and type(i["expected_posts"]) is int and i["expected_posts"] == COUNT, "release_intent")
        require(all(i[key] == self.value[key] for key in ("policy_pin", "policy_commit", "bge_terminal_pin", "bge_terminal_commit")), "release_intent")

    def verify_backend(self):
        try:
            actual = self.h.backend_identity(self.h.CONTAINER)
        except Exception:
            raise ReleaseError("backend_unavailable") from None
        require(actual == self.deployment["backend"], "backend_identity")
        return actual


class Ledger:
    """Release counts are sealed here; frozen helpers never change globals."""
    def __init__(self, plan, role):
        require(plan.intent is not None and role in ("provider", "collector"), "intent_required")
        self.plan, self.h, self.role = plan, plan.h, role
        self.directory = plan.runtime / (role + "-ledger")
        self.completed = self.consumed = 0
        self.pending = self.previous = None
        self.ended = self.success = False
        self.h.directory(self.directory, fresh=True)
        for name in ("intent", "started"):
            self._write(name + ".json", envelope("run-" + name, **self.base(), expected_posts=COUNT, provenance=self.h.PROVENANCE))

    def base(self):
        return {"role": self.role, "phase": PHASE, "prereg_pin": self.plan.prereg_pin, "intent_pin": self.plan.intent_pin}

    def _write(self, name, value):
        value = dict(value)
        # Reuse the frozen write/terminal algorithm with an explicit artifact
        # translation. Old source and module constants are never modified.
        original = "imms-gb10-native-set-"
        if value["artifact"].startswith(original) and not value["artifact"].startswith(PREFIX):
            value["artifact"] = PREFIX + value["artifact"][len(original):]
        if "ordinal" in value:
            entry = self.plan.requests[value["ordinal"]]
            value.update(stream=entry["stream"], stream_ordinal=entry["stream_ordinal"])
        return self.h.durable_new(self.directory / name, canonical(value))

    def consume(self, entry):
        require(not self.ended and self.pending is None and self.consumed == self.completed and self.consumed < COUNT and entry == self.plan.requests[self.consumed], "request_order")
        value = envelope("cell-admission", **self.base(), **{k: entry[k] for k in ("ordinal", "stream", "stream_ordinal", "cell_id", "input_commitment_sha256", "request_id", "body_pin")}, previous_result_pin=self.previous)
        self.pending = (entry, digest(canonical(value)))
        self.consumed += 1
        return self._write(f'{entry["ordinal"]:04d}-admission.json', value)

    def complete(self, value):
        return self.h.Ledger.complete(self, value)

    def cell_terminal(self, status, code, result_pin):
        return self.h.Ledger.cell_terminal(self, status, code, result_pin)

    def terminal(self, status, code, result_pin):
        return self.h.Ledger.terminal(self, status, code, result_pin)

    def finish(self, result_pin=None):
        require(not self.ended and self.pending is None and self.completed == self.consumed == COUNT, "run_incomplete")
        self.terminal("success", "complete", result_pin)
        self.ended = self.success = True

    def stop(self, code):
        return self.h.Ledger.stop(self, code)


def error_code(error, default):
    code = getattr(error, "code", default)
    return code if type(code) is str and re.fullmatch(r"[a-z_]{1,64}", code) is not None else default


def stop_with_response(ledger, error, observed, default):
    code = error_code(error, default)
    try:
        ledger.h.failure_response(ledger, getattr(error, "observed", None) or observed)
    finally:
        ledger.stop(code)
    raise ReleaseError(code) from None


class BoundaryState:
    def __init__(self, plan):
        self.plan, self.h = plan, plan.h
        plan.verify_backend()
        self.ledger = Ledger(plan, "provider")
        self.observations = self.h.Observations(plan.output)
        self.metrics_due = False
        self.last_http = None

    def forward(self, method, path, header_items, body):
        h = self.h
        try:
            require(not self.ledger.ended or self.ledger.success, "run_failed")
            headers = h.headers_unique(header_items)
            require(headers.get("host") == "127.0.0.1:19483" and "transfer-encoding" not in headers, "request_headers")
            if method == "GET":
                require(path in h.GET_PATHS and body == b"" and set(headers) <= {"host", "content-length"} and headers.get("content-length", "0") == "0", "route_invalid")
                entry, forwarded = None, {}
            elif method == "POST" and path == "/v1/rerank":
                require(not self.ledger.ended and not self.metrics_due and self.ledger.consumed < COUNT, "request_order")
                entry = self.plan.requests[self.ledger.consumed]
                require(set(headers) == {"host", "content-length", "content-type", "x-request-id"} and headers["content-type"] == "application/json" and headers["content-length"] == str(len(body)) and headers["x-request-id"] == entry["request_id"] and body == canonical(entry["body"]), "request_mismatch")
                forwarded = {"Content-Type": "application/json", "X-Request-Id": entry["request_id"]}
            else:
                raise ReleaseError("route_invalid")
            before = self.observations.save(self.plan.verify_backend())
            if entry is not None:
                self.ledger.consume(entry)
            status, upstream_headers, raw, _ = h.http_once(method, path, body if entry else None, forwarded, port=h.BACKEND_PORT)
            self.last_http = (status, upstream_headers, raw, None)
            after = self.observations.save(self.plan.verify_backend())
            upstream = h.headers_unique(upstream_headers)
            require(status == 200, "upstream_status")
            if entry is not None:
                require(upstream.get("content-type", "").split(";", 1)[0] == "application/json", "response_content_type")
                self.ledger.complete({"raw_response_pin": digest(raw), "http_status": status, **h.validate_reply(entry, raw), "backend_before_pin": before, "backend_after_pin": after})
                self.metrics_due = True
            else:
                h.check_observation({"/v1/models": "models_start", "/version": "version_start", "/health": "health_start", "/metrics": "metrics"}[path], raw)
                if path == "/metrics":
                    self.metrics_due = False
                    if self.ledger.completed == COUNT and not self.ledger.ended:
                        self.ledger.finish()
            returned = {"Content-Type": upstream.get("content-type", "application/octet-stream"), h.PREREG_HEADER: self.plan.prereg_pin["sha256"], h.INTENT_HEADER: self.plan.intent_pin["sha256"], h.DEPLOYMENT_HEADER: DEPLOYMENT_PIN["sha256"]}
            if entry is not None:
                returned["X-Request-Id"] = entry["request_id"]
            self.last_http = None
            return status, returned, raw
        except BaseException as error:
            stop_with_response(self.ledger, error, self.last_http, "boundary_failure")


class Collector:
    def __init__(self, plan):
        self.plan, self.h = plan, plan.h
        self.ledger = Ledger(plan, "collector")
        self.observations = self.h.Observations(plan.output)
        self.last_http = None
        self.expected_tls = None
        try:
            self.context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
            self.context.minimum_version = ssl.TLSVersion.TLSv1_2
            self.context.load_verify_locations(cadata=plan.ca.decode("ascii"))
            self.peer_sha = hashlib.sha256(ssl.PEM_cert_to_DER_cert(plan.ca.decode("ascii"))).hexdigest()
        except Exception:
            self.ledger.stop("tls_configuration")
            raise ReleaseError("tls_configuration") from None

    def request(self, method, path, entry=None):
        response = self.h.Collector.request(self, method, path, entry)
        if self.expected_tls is None:
            self.expected_tls = response[3]
        require(response[3] == self.expected_tls, "tls_identity")
        return response

    def observe(self, stream, kind, index=None):
        name = "models" if kind.startswith("models_") else kind.split("_", 1)[0]
        status, headers, raw, tls = self.request("GET", "/v1/models" if name == "models" else "/" + name)
        identity = self.observations.save(envelope("http-observation", phase=PHASE, stream=stream, kind=kind, index=index, http_status=status, response_headers=headers, raw_body_base64=base64.b64encode(raw).decode("ascii"), raw_body_pin=digest(raw), tls=tls))
        counters = self.h.check_observation(kind, raw)
        self.last_http = None
        return identity, ({"observation_pin": identity, **counters} if counters is not None else None)

    def provider_record(self, entry, raw, score):
        record_raw = self.h.read_raw(self.plan.runtime / "provider-ledger" / f'{entry["ordinal"]:04d}-result.json')
        record = decode(record_raw, canonical_required=True)
        expected = envelope("provider-cell-result", role="provider", phase=PHASE, prereg_pin=self.plan.prereg_pin, intent_pin=self.plan.intent_pin, **{key: entry[key] for key in ("ordinal", "stream", "stream_ordinal", "cell_id", "input_commitment_sha256", "request_id", "body_pin", "token_count")}, raw_response_pin=digest(raw), http_status=200, **score)
        obj(record, (*expected.keys(), "admission_pin", "backend_before_pin", "backend_after_pin"), "provider_record")
        require(all(record[key] == value for key, value in expected.items()), "provider_record")
        for key in ("admission_pin", "backend_before_pin", "backend_after_pin"):
            self.h.valid_pin(record[key])
        for key in ("backend_before_pin", "backend_after_pin"):
            require(self.h.read_pin(self.observations.root / (record[key]["sha256"] + ".json"), record[key]) == canonical(self.plan.deployment["backend"]), "provider_record")
        return digest(record_raw)

    def run(self):
        require(not self.ledger.ended and self.ledger.consumed == 0, "run_already_consumed")
        results, observations, previous_cache = [], [], None
        try:
            for stream, count in STREAMS:
                observed = {"stream": stream, "metrics": []}
                for kind in ("models_start", "version_start", "health_start"):
                    observed[kind], _ = self.observe(stream, kind)
                identity, before = self.observe(stream, "metrics", 0)
                require(previous_cache is None or all(before[key] >= previous_cache[key] for key in ("prefix_cache_queries_total", "prefix_cache_hits_total")), "cache_regression")
                observed["metrics"].append(identity)
                for entry in self.plan.by_stream[stream]:
                    status, headers, raw, tls = self.request("POST", "/v1/rerank", entry)
                    score = self.h.validate_reply(entry, raw)
                    provider_pin = self.provider_record(entry, raw, score)
                    identity, after = self.observe(stream, "metrics", entry["stream_ordinal"] + 1)
                    observed["metrics"].append(identity)
                    require(all(after[key] >= before[key] for key in ("prefix_cache_queries_total", "prefix_cache_hits_total")), "cache_regression")
                    collector_pin = self.ledger.complete({"raw_response_pin": digest(raw), "http_status": status, **score, "provider_record_pin": provider_pin, "response_headers": headers, "raw_response_base64": base64.b64encode(raw).decode("ascii"), "cache_before": before, "cache_after": after, "tls": tls})
                    results.append({**{key: entry[key] for key in ("ordinal", "stream", "stream_ordinal", "cell_id", "input_commitment_sha256", "request_id")}, **score, "collector_record_pin": collector_pin, "provider_record_pin": provider_pin})
                    self.last_http = None
                    before = after
                require(len(observed["metrics"]) == count + 1, "stream_incomplete")
                for kind in ("models_end", "version_end", "health_end"):
                    observed[kind], _ = self.observe(stream, kind)
                observations.append(observed)
                previous_cache = before
            terminal = decode(self.h.read_raw(self.plan.runtime / "provider-ledger/terminal.json"), canonical_required=True)
            expected = envelope("phase-terminal", role="provider", phase=PHASE, prereg_pin=self.plan.prereg_pin, intent_pin=self.plan.intent_pin, status="success", catalog_pin=self.plan.value["catalog_pin"], completed_posts=COUNT, consumed_posts=COUNT, count_known=True, code="complete", phase_result_pin=None, provenance=self.h.PROVENANCE)
            require(canonical(terminal) == canonical(expected), "provider_terminal")
            result = envelope("phase-result", phase=PHASE, status="success", prereg_pin=self.plan.prereg_pin, intent_pin=self.plan.intent_pin, catalog_pin=self.plan.value["catalog_pin"], deployment_record_pin=DEPLOYMENT_PIN, post_count=COUNT, retry_count=0, results=results, stream_observations=observations, provenance=self.h.PROVENANCE)
            identity = self.h.durable_new(self.plan.output / "phase-result.json", canonical(result))
            self.ledger.finish(identity)
            return result
        except BaseException as error:
            stop_with_response(self.ledger, error, self.last_http, "collector_failure")


def _signal(_number, _frame):
    raise ReleaseError("process_interrupted")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("validate-plan", "boundary", "collect"))
    for key in ("prereg", "inputs", "catalog", "reference-tokens", "deployment-record", "ca-file", "policy", "policy-replay", "bge-terminal"):
        parser.add_argument("--" + key, type=Path, required=True)
    parser.add_argument("--expected-sha256", required=True)
    parser.add_argument("--expected-size", type=int, required=True)
    for key in ("calibration-prereg", "intent", "cert-file", "key-file"):
        parser.add_argument("--" + key, type=Path)
    parser.add_argument("--intent-sha256")
    parser.add_argument("--intent-size", type=int)
    args = parser.parse_args()
    state = None
    try:
        plan = Plan(args.prereg, args.expected_sha256, args.expected_size, **{key: getattr(args, key) for key in ("inputs", "catalog", "reference_tokens", "deployment_record", "ca_file", "policy", "policy_replay", "bge_terminal", "calibration_prereg", "intent", "intent_sha256", "intent_size")})
        if args.mode == "validate-plan":
            print(json.dumps({"status": "valid", "planned_posts": COUNT, "executed_posts": 0}, sort_keys=True))
            return 0
        require(plan.intent is not None, "intent_required")
        signal.signal(signal.SIGTERM, _signal)
        signal.signal(signal.SIGINT, _signal)
        if args.mode == "collect":
            state = Collector(plan)
            state.run()
            return 0
        require(args.cert_file is not None and args.key_file is not None, "tls_configuration")
        descriptors = []
        try:
            for path in (args.cert_file, args.key_file):
                parent, name = plan.h.open_parent(path)
                try:
                    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
                finally:
                    os.close(parent)
                descriptors.append(fd)
                st = os.fstat(fd)
                require(stat.S_ISREG(st.st_mode) and st.st_nlink == 1 and 0 < st.st_size <= plan.h.MAX_GET, "tls_configuration")
                if path == args.cert_file:
                    raw = os.read(fd, st.st_size + 1)
                    require(raw == plan.ca, "tls_certificate_pin")
                    os.lseek(fd, 0, os.SEEK_SET)
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.minimum_version = ssl.TLSVersion.TLSv1_2
            context.load_cert_chain(*(f"/proc/self/fd/{fd}" for fd in descriptors))
        finally:
            for fd in descriptors:
                os.close(fd)
        state = BoundaryState(plan)
        handler = type("ReleaseHandler", (plan.h.BoundaryHandler,), {"server_version": "IMMSNativeSetRelease"})
        with plan.h.HTTPServer(("127.0.0.1", 19483), handler) as server:
            server.state = state
            server.socket = context.wrap_socket(server.socket, server_side=True)
            server.serve_forever()
        return 0
    except BaseException as error:
        code = error_code(error, "process_failure")
        if code == "process_interrupted" and state is not None and state.ledger.success:
            print(json.dumps({"status": "completed_stop", "executed_posts": COUNT}, sort_keys=True))
            return 0
        if state is not None:
            try:
                state.ledger.stop(code)
            except Exception:
                code = "terminal_write_failure"
        print(json.dumps({"status": "failed", "code": code}, sort_keys=True))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
