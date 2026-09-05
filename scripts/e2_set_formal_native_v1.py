#!/usr/bin/env python3
"""Decision 0033 calibration-only native transport; no model imports or retries.

The external raw prereg and phase-intent pins are the authority. Git admission
is performed by the separate reviewed operator before this isolated process.
"""
from __future__ import annotations

import argparse
import base64
from decimal import Decimal, InvalidOperation
from fractions import Fraction
import hashlib
import http.client
from http.server import BaseHTTPRequestHandler, HTTPServer
import importlib
import json
import os
from pathlib import Path
import re
import signal
import ssl
import stat
import struct
import subprocess

EPOCH = "gb10-native-set-admission-v1"
ROOT = Path("/home/infinimesh/imms-debug/20260905")
CONTAINER = "imms-gb10-native-set-admission-v1-20260905"
ORIGIN = "https://127.0.0.1:19483"
BACKEND_PORT = 18485
PROFILE = "imms-set-native-v1"
IMAGE = "sha256:a727c4ae21174b2c87b3b8ab301fff389fbc052b0a2979b897022e86551e06c6"
REVISION = "22e683669bc0f0bd69640a1354a6d0aebcfeede5"
TOKENIZER_SOURCE_SHA = "218be8196402e4eb85420e954809c2a78af5ab5ce8f1ec2bea4277f45fb259e7"
REFERENCE_SOURCE_PIN = {"sha256": "e2927ece6ee009e0fcd7b36bc90c99fb9c901877568c52ef9da8d81edccfdcd5", "size_bytes": 33922}
TOKENIZER_PIN = {"sha256": "aeb13307a71acd8fe81861d94ad54ab689df773318809eed3cbe794b4492dae4", "size_bytes": 11422654}
PROVENANCE = {"static_access": "unknown", "strict_blindness": "unproven",
    "incident_commit": "c86569c52abc44cd9fa38bfa95e8ef346428251f",
    "incident_path": "docs/gb10-native-set-admission-2026-09-05.md",
    "incident_pin": {"sha256": "63dbfb4c1d071cf6ee37fca658bb32b88bc190c2c824a9476fa3f8f9e5856299", "size_bytes": 2858}}
RUNTIME_VERSIONS = {"vllm": "0.23.0", "torch": "2.11.0+cu130", "transformers": "5.12.0", "tokenizers": "0.22.2", "safetensors": "0.8.0"}
SCORE_RULE = {"rounding": "binary32_rne", "repeat_abs_epsilon": 0, "cutoff_margin": 0,
    "cutoff_derivation": "nextafter32(max640,+Inf)", "comparison": "p_yes_float32>=cutoff"}
PHASES = ("calibration-01", "calibration-02")
COUNT = 640
MAX_ARTIFACT, MAX_RESPONSE, MAX_GET = 128 << 20, 1 << 20, 64 << 10
PREREG_HEADER = "X-IMMS-Native-Set-Prereg-SHA256"
INTENT_HEADER = "X-IMMS-Native-Set-Intent-SHA256"
DEPLOYMENT_HEADER = "X-IMMS-Native-Set-Deployment-SHA256"
GET_PATHS = ("/v1/models", "/version", "/health", "/metrics")
BACKEND_KEYS = ("container_id", "image_id", "image_reference", "started_at", "entrypoint", "cmd", "user", "readonly_rootfs", "cap_drop", "security_opt", "mounts", "networks", "set_tokenizer_profile")
MODEL_PATHS = (".gitattributes", "1_LogitScore/config.json", "README.md", "chat_template.jinja", "config.json", "config_sentence_transformers.json", "generation_config.json", "merges.txt", "model-00001-of-00002.safetensors", "model-00002-of-00002.safetensors", "model.safetensors.index.json", "modules.json", "sentence_bert_config.json", "serving/qwen3_reranker.jinja", "special_tokens_map.json", "tokenizer.json", "tokenizer_config.json", "vocab.json")


class AdmissionError(ValueError):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


def fail(code):
    raise AdmissionError(code) from None


def require(ok, code):
    if not ok:
        fail(code)


def canonical(value):
    try:
        return (json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False) + "\n").encode("utf-8")
    except (TypeError, ValueError, UnicodeError, RecursionError):
        fail("json_invalid")


def _pairs(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "json_duplicate_key")
        result[key] = value
    return result


def _constant(_):
    fail("json_invalid")


class Number(str):
    """Preserve every JSON numeric lexeme, including integer -0."""


def decode(raw, *, canonical_required=False, numbers=False):
    require(type(raw) is bytes and len(raw) <= MAX_ARTIFACT and not raw.startswith(b"\xef\xbb\xbf"), "json_invalid")
    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=_pairs, parse_constant=_constant,
                           parse_int=Number if numbers else int, parse_float=Number if numbers else float)
        # json.loads accepts lone escaped surrogates; canonical UTF-8 cannot.
        if not numbers:
            normalized = canonical(value)
            if canonical_required:
                require(normalized == raw, "json_noncanonical")
        return value
    except AdmissionError:
        raise
    except (ValueError, TypeError, UnicodeError, RecursionError):
        fail("json_invalid")


def obj(value, keys, code="artifact_shape"):
    require(type(value) is dict and set(value) == set(keys), code)


def sha(value):
    return type(value) is str and re.fullmatch(r"[0-9a-f]{64}", value) is not None


def integer(value, lower=0, upper=MAX_ARTIFACT):
    return type(value) is int and lower <= value <= upper


def pin(raw):
    return {"sha256": hashlib.sha256(raw).hexdigest(), "size_bytes": len(raw)}


def valid_pin(value, *, empty=False):
    obj(value, ("sha256", "size_bytes"), "pin_invalid")
    require(sha(value["sha256"]) and integer(value["size_bytes"], 0 if empty else 1), "pin_invalid")


def _path_parts(path):
    value = os.fspath(path)
    require(type(value) is str and value.startswith("/") and "\x00" not in value, "path_invalid")
    parts = value.split("/")[1:]
    require(parts and all(p and p not in (".", "..") for p in parts), "path_invalid")
    return parts


def open_parent(path):
    """Walk every parent using directory descriptors and O_NOFOLLOW."""
    parts = _path_parts(path)
    descriptor = os.open("/", os.O_RDONLY | os.O_DIRECTORY)
    try:
        for name in parts[:-1]:
            next_fd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = next_fd
        return descriptor, parts[-1]
    except BaseException:
        os.close(descriptor)
        raise


def read_raw(path, limit=MAX_ARTIFACT):
    try:
        parent, name = open_parent(path)
        try:
            fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
        finally:
            os.close(parent)
        with os.fdopen(fd, "rb") as source:
            before = os.fstat(source.fileno())
            require(stat.S_ISREG(before.st_mode) and before.st_nlink == 1 and before.st_size <= limit, "pin_file")
            raw = source.read(limit + 1)
            after = os.fstat(source.fileno())
        require(len(raw) <= limit and (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns) ==
                (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns), "pin_changed")
        return raw
    except OSError:
        fail("pin_io")


def read_pin(path, identity, *, empty=False):
    valid_pin(identity, empty=empty)
    raw = read_raw(path, identity["size_bytes"])
    require(pin(raw) == identity, "pin_mismatch")
    return raw


def durable_new(path, raw):
    require(type(raw) is bytes and len(raw) <= MAX_ARTIFACT, "artifact_limit")
    try:
        parent, name = open_parent(path)
        try:
            fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=parent)
            with os.fdopen(fd, "wb") as output:
                output.write(raw)
                output.flush()
                os.fsync(output.fileno())
            os.fsync(parent)
        finally:
            os.close(parent)
    except OSError:
        fail("durable_write")
    return pin(raw)


def directory(path, *, fresh):
    try:
        parent, name = open_parent(path)
        try:
            try:
                os.mkdir(name, 0o700, dir_fd=parent)
                os.fsync(parent)
            except FileExistsError:
                require(not fresh, "run_already_claimed")
            fd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent)
            os.close(fd)
        finally:
            os.close(parent)
    except OSError:
        fail("directory_io")


class Observations:
    def __init__(self, output):
        directory(output, fresh=False)
        self.root = output / "observations"
        directory(self.root, fresh=False)

    def save(self, value):
        raw = canonical(value)
        identity = pin(raw)
        target = self.root / (identity["sha256"] + ".json")
        try:
            return durable_new(target, raw)
        except AdmissionError as error:
            if error.code != "durable_write":
                raise
            # A content-addressed duplicate never authorizes overwriting.
            read_pin(target, identity)
            return identity


def envelope(artifact, **fields):
    return {"artifact": "imms-gb10-native-set-" + artifact, "version": 1, "epoch": EPOCH, **fields}


def check_envelope(value, artifact, keys):
    obj(value, ("artifact", "version", "epoch", *keys))
    require(value["artifact"] == "imms-gb10-native-set-" + artifact and type(value["version"]) is int and value["version"] == 1 and value["epoch"] == EPOCH, "artifact_identity")
    if "provenance" in keys:
        require(canonical(value["provenance"]) == canonical(PROVENANCE), "provenance")


def token_sha(ids):
    require(type(ids) is list and 0 < len(ids) <= 8192 and all(integer(i, 0, 0xffffffff) for i in ids), "token_ids")
    return hashlib.sha256(struct.pack(">I", len(ids)) + b"".join(struct.pack(">I", i) for i in ids)).hexdigest()


def request_body(query, document, instruction):
    return {"documents": [document], "instruction": instruction, "max_tokens_per_doc": 0,
        "max_tokens_per_query": 0, "model": "sparkclaw-reranker", "priority": 0,
        "query": query, "top_n": 1, "truncate_prompt_tokens": None, "truncation_side": None, "use_activation": True}


class Plan:
    def __init__(self, prereg, expected_sha256, expected_size, *, inputs, catalog,
                 reference_tokens, deployment_record, ca_file, intent=None,
                 intent_sha256=None, intent_size=None, previous_terminal=None):
        self.prereg_pin = {"sha256": expected_sha256, "size_bytes": expected_size}
        p = self.value = decode(read_pin(prereg, self.prereg_pin), canonical_required=True)
        check_envelope(p, "admission-prereg", ("input_pin", "catalog_pin", "reference_tokens_pin", "deployment_record_pin", "ca_pin", "source_files", "score_rule", "request_plans", "provenance"))
        require(canonical(p["score_rule"]) == canonical(SCORE_RULE), "score_rule")
        self._sources(p["source_files"])
        # All external raw pins pass before any corresponding content is decoded.
        raw_inputs = read_pin(inputs, p["input_pin"])
        raw_catalog = read_pin(catalog, p["catalog_pin"])
        raw_reference = read_pin(reference_tokens, p["reference_tokens_pin"])
        raw_deployment = read_pin(deployment_record, p["deployment_record_pin"])
        self.ca = read_pin(ca_file, p["ca_pin"])
        self.inputs, self.catalog, self.reference = decode(raw_inputs), decode(raw_catalog), decode(raw_reference)
        self.deployment = decode(raw_deployment, canonical_required=True)
        self._deployment()
        self._catalog()
        self._requests()
        self.intent = self.intent_pin = self.phase = None
        if intent is not None:
            self.intent_pin = {"sha256": intent_sha256, "size_bytes": intent_size}
            self.intent = decode(read_pin(intent, self.intent_pin), canonical_required=True)
            self._intent(previous_terminal)
            self.output = ROOT / "evidence/native-set-admission-v1" / self.phase
            self.runtime = ROOT / "runtime/native-set-admission-v1" / self.phase
            self.requests = [{**cell, "request_id": planned["request_id"]} for cell, planned in zip(self.catalog["cells"], self.request_plans[self.phase], strict=True)]

    def _sources(self, files):
        names = ["__init__.py", "e2_set_formal_native_v1.py", "e2_set_tokenizer_v1.py"]
        require(type(files) is list and len(files) == 3, "source_closure")
        for record, name in zip(files, names, strict=True):
            obj(record, ("path", "sha256", "size_bytes"), "source_closure")
            require(record["path"] == name, "source_closure")
            identity = {k: record[k] for k in ("sha256", "size_bytes")}
            raw = read_pin(Path(__file__).absolute().parent / name, identity, empty=name == "__init__.py")
            if name == "__init__.py":
                require(raw == b"", "source_closure")
            elif name == "e2_set_tokenizer_v1.py":
                require(identity["sha256"] == TOKENIZER_SOURCE_SHA, "source_closure")
            else:
                self.source_pin = identity
        self.formatter = importlib.import_module("scripts.e2_set_tokenizer_v1")

    def _deployment(self):
        d = self.deployment
        check_envelope(d, "deployment", ("container", "origin", "backend_port", "profile", "backend", "model_revision", "model_files", "runtime_versions", "tls_ca_pin", "transport_source_pin", "provenance"))
        require(d["container"] == CONTAINER and d["origin"] == ORIGIN and type(d["backend_port"]) is int and d["backend_port"] == BACKEND_PORT and d["profile"] == PROFILE and d["model_revision"] == REVISION, "deployment_identity")
        require(d["tls_ca_pin"] == self.value["ca_pin"] and d["transport_source_pin"] == self.source_pin and d["runtime_versions"] == RUNTIME_VERSIONS, "deployment_identity")
        b = d["backend"]
        obj(b, BACKEND_KEYS, "backend_shape")
        require(sha(b["container_id"]) and b["image_id"] == IMAGE and b["image_reference"] == IMAGE and b["set_tokenizer_profile"] == PROFILE and type(b["started_at"]) is str and b["started_at"], "backend_identity")
        require(b["readonly_rootfs"] is True and b["user"] in ("1000", "1000:1000") and type(b["cap_drop"]) is list and "ALL" in b["cap_drop"] and type(b["security_opt"]) is list and any(x in ("no-new-privileges", "no-new-privileges:true") for x in b["security_opt"]), "backend_security")
        require(b["entrypoint"] is None or type(b["entrypoint"]) is list, "backend_command")
        require(b["cmd"] is None or type(b["cmd"]) is list, "backend_command")
        argv = (b["entrypoint"] or []) + (b["cmd"] or [])
        require(all(type(x) is str for x in argv), "backend_command")
        for flag in ("--no-enable-prefix-caching", "--enforce-eager"):
            require(argv.count(flag) == 1, "backend_command")
        require("--enable-prefix-caching" not in argv, "backend_command")
        for flag, value in (("--tensor-parallel-size", "1"), ("--max-num-seqs", "1"), ("--max-model-len", "8192"), ("--dtype", "bfloat16")):
            require(argv.count(flag) == 1 and argv.index(flag) + 1 < len(argv) and argv[argv.index(flag) + 1] == value, "backend_command")
        require(type(b["mounts"]) is list and type(b["networks"]) is list and b["networks"] == sorted(set(b["networks"])), "backend_shape")
        for mount in b["mounts"]:
            obj(mount, ("source", "destination", "rw"), "backend_shape")
            _path_parts(mount["source"])
            _path_parts(mount["destination"])
            require(type(mount["rw"]) is bool, "backend_shape")
        require(b["mounts"] == sorted(b["mounts"], key=lambda m: (m["destination"], m["source"])), "backend_shape")
        files = d["model_files"]
        require(type(files) is list and len(files) == 18, "model_catalog")
        seen = set()
        for record in files:
            obj(record, ("path", "sha256", "size_bytes"), "model_catalog")
            path = record["path"]
            require(type(path) is str and path in MODEL_PATHS and path not in seen and sha(record["sha256"]) and integer(record["size_bytes"], 1, 1 << 40), "model_catalog")
            seen.add(path)
        require([record["path"] for record in files] == list(MODEL_PATHS), "model_catalog")

    def _catalog(self):
        common = ("family", "cell_count", "source_pins", "provenance", "cells")
        check_envelope(self.inputs, "cell-inputs", common)
        check_envelope(self.catalog, "cell-catalog", common)
        for artifact in (self.inputs, self.catalog):
            require(artifact["family"] == "calibration" and type(artifact["cell_count"]) is int and artifact["cell_count"] == COUNT and type(artifact["cells"]) is list and len(artifact["cells"]) == COUNT, "cell_inventory")
        sources = self.inputs["source_pins"]
        require(type(sources) is list and 0 < len(sources) <= 128, "input_sources")
        seen = set()
        for record in sources:
            obj(record, ("role", "path", "sha256", "size_bytes"), "input_sources")
            require(type(record["role"]) is str and re.fullmatch(r"[a-z0-9_.-]+", record["role"]) is not None and record["role"] not in seen and type(record["path"]) is str and record["path"] and sha(record["sha256"]) and integer(record["size_bytes"], 1), "input_sources")
            seen.add(record["role"])
        require(self.catalog["source_pins"] == sources, "input_sources")
        r = self.reference
        check_envelope(r, "reference-tokens", ("input_pin", "reference_source_pin", "tokenizer_source_pin", "provenance", "cases"))
        require(r["input_pin"] == self.value["input_pin"] and r["reference_source_pin"] == REFERENCE_SOURCE_PIN and r["tokenizer_source_pin"] == TOKENIZER_PIN and type(r["cases"]) is list and len(r["cases"]) == COUNT, "reference_identity")
        f = self.formatter
        cell_keys = ("ordinal", "cell_id", "query_id", "depth", "prefix_ids", "input_commitment_sha256", "body", "body_pin")
        previous_query, previous_prefix = None, []
        for ordinal, (source, cell, reference) in enumerate(zip(self.inputs["cells"], self.catalog["cells"], r["cases"], strict=True)):
            obj(source, cell_keys, "input_cell")
            obj(cell, (*cell_keys, "token_count", "token_ids", "token_ids_sha256"), "catalog_cell")
            require(canonical(source) == canonical({k: cell[k] for k in cell_keys}), "catalog_input_mismatch")
            depth = ordinal % 32 + 1
            require(type(cell["ordinal"]) is int and cell["ordinal"] == ordinal and cell["cell_id"] == f"calibration.{ordinal // 32 + 1:04d}.depth.{depth:02d}" and type(cell["depth"]) is int and cell["depth"] == depth, "cell_order")
            require(type(cell["query_id"]) is str and cell["query_id"] and sha(cell["input_commitment_sha256"]), "cell_identity")
            prefix = cell["prefix_ids"]
            require(type(prefix) is list and len(prefix) == depth and all(type(x) is str and x for x in prefix) and len(set(prefix)) == depth, "prefix_ids")
            body = cell["body"]
            require(type(body) is dict and type(body.get("query")) is str and body["query"] and "\ufeff" not in body["query"] and type(body.get("documents")) is list and len(body["documents"]) == 1 and type(body["documents"][0]) is str, "request_body")
            require(canonical(body) == canonical(request_body(body["query"], body["documents"][0], f.INSTRUCTION)), "request_body")
            wire = body["documents"][0]
            documents = decode(wire.encode("utf-8"))
            obj(documents, ("documents",), "request_document")
            try:
                require(f.encode_documents(documents["documents"]) == wire and len(documents["documents"]) == depth, "request_document")
            except f.SetTokenizerError:
                fail("request_document")
            if depth > 1:
                require(cell["query_id"] == previous_query[0] and body["query"] == previous_query[1] and prefix[:-1] == previous_prefix, "prefix_order")
            previous_query, previous_prefix = (cell["query_id"], body["query"]), prefix
            require(pin(canonical(body)) == cell["body_pin"] and len(canonical(body)) <= MAX_RESPONSE, "body_pin")
            require(token_sha(cell["token_ids"]) == cell["token_ids_sha256"] and type(cell["token_count"]) is int and len(cell["token_ids"]) == cell["token_count"], "token_identity")
            obj(reference, ("id", "kind", "rendered_utf8", "rendered_sha256", "rendered_size_bytes", "token_ids", "token_count", "segments", "untrusted_control_ids"), "reference_cell")
            values = [f.PREFIX, f.BODY_PREFIX, body["query"], f.BODY_MIDDLE, wire, f.SUFFIX]
            rendered = "".join(values)
            rendered_pin = pin(rendered.encode("utf-8"))
            require(reference["id"] == cell["cell_id"] and reference["kind"] == "ordered_set" and reference["rendered_utf8"] == rendered and reference["rendered_sha256"] == rendered_pin["sha256"] and type(reference["rendered_size_bytes"]) is int and reference["rendered_size_bytes"] == rendered_pin["size_bytes"] and reference["token_ids"] == cell["token_ids"] and type(reference["token_count"]) is int and reference["token_count"] == cell["token_count"] and reference["untrusted_control_ids"] == [], "reference_tokens")
            segments = reference["segments"]
            require(type(segments) is list and len(segments) == 6, "reference_segments")
            flat = []
            for n, (segment, name, text) in enumerate(zip(segments, ("prefix", "body_prefix", "query", "body_middle", "document", "suffix"), values, strict=True)):
                obj(segment, ("name", "trusted", "utf8", "token_ids"), "reference_segments")
                require(segment["name"] == name and segment["utf8"] == text and segment["trusted"] is (n not in (2, 4)), "reference_segments")
                token_sha(segment["token_ids"])
                require(n not in (2, 4) or not set(segment["token_ids"]).intersection(range(151643, 151669)), "untrusted_control")
                flat.extend(segment["token_ids"])
            require(flat == cell["token_ids"], "reference_segments")

    def _requests(self):
        plans = self.value["request_plans"]
        require(type(plans) is list and len(plans) == 2, "request_plans")
        seen, self.request_plans = set(), {}
        for phase, plan in zip(PHASES, plans, strict=True):
            obj(plan, ("phase", "expected_posts", "requests"), "request_plans")
            require(plan["phase"] == phase and type(plan["expected_posts"]) is int and plan["expected_posts"] == COUNT and type(plan["requests"]) is list and len(plan["requests"]) == COUNT, "request_plans")
            for ordinal, (request, cell) in enumerate(zip(plan["requests"], self.catalog["cells"], strict=True)):
                obj(request, ("ordinal", "cell_id", "request_id"), "request_plan")
                request_id = request["request_id"]
                require(type(request["ordinal"]) is int and request["ordinal"] == ordinal and request["cell_id"] == cell["cell_id"] and type(request_id) is str and re.fullmatch(r"[A-Za-z0-9._-]{1,128}", request_id) is not None and request_id not in seen, "request_plan")
                seen.add(request_id)
            self.request_plans[phase] = plan["requests"]

    def _intent(self, previous_terminal):
        i = self.intent
        check_envelope(i, "phase-intent", ("phase", "prereg_pin", "prereg_commit", "previous_phase_terminal", "provenance"))
        require(i["prereg_pin"] == self.prereg_pin and i["phase"] in PHASES and type(i["prereg_commit"]) is str and re.fullmatch(r"[0-9a-f]{40}", i["prereg_commit"]) is not None, "phase_intent")
        self.phase = i["phase"]
        if self.phase == PHASES[0]:
            require(i["previous_phase_terminal"] is None and previous_terminal is None, "phase_predecessor")
            return
        previous = i["previous_phase_terminal"]
        obj(previous, ("pin", "commit"), "phase_predecessor")
        require(previous_terminal is not None and type(previous["commit"]) is str and re.fullmatch(r"[0-9a-f]{40}", previous["commit"]) is not None, "phase_predecessor")
        terminal = decode(read_pin(previous_terminal, previous["pin"]), canonical_required=True)
        check_terminal(terminal)
        require(terminal["role"] == "collector" and terminal["phase"] == PHASES[0] and terminal["prereg_pin"] == self.prereg_pin and terminal["catalog_pin"] == self.value["catalog_pin"] and terminal["status"] == "success" and terminal["completed_posts"] == COUNT and terminal["consumed_posts"] == COUNT and terminal["count_known"] is True and terminal["code"] == "complete", "phase_predecessor")
        valid_pin(terminal["phase_result_pin"])

    def verify_backend(self):
        try:
            actual = backend_identity(CONTAINER)
        except Exception:
            fail("backend_unavailable")
        require(actual == self.deployment["backend"], "backend_identity")
        return actual


def backend_identity(name):
    """Docker emits only reviewed public facts, never its environment object."""
    require(name == CONTAINER, "backend_name")
    fields = (".State.Running", ".Id", ".Image", ".Config.Image", ".State.StartedAt", ".Config.Entrypoint", ".Config.Cmd", ".Config.User", ".HostConfig.ReadonlyRootfs", ".HostConfig.CapDrop", ".HostConfig.SecurityOpt", ".Mounts", ".NetworkSettings.Networks")
    template = "\n".join("{{json " + field + "}}" for field in fields)
    template += '\n{{range .Config.Env}}{{if eq (index (split . "=") 0) "IMMS_SET_TOKENIZER_PROFILE"}}{{.}}{{end}}{{end}}'
    raw = subprocess.check_output(["docker", "inspect", "--format", template, name], timeout=10)
    require(len(raw) <= MAX_GET, "backend_shape")
    lines = raw.splitlines()
    require(len(lines) == 14 and lines[-1] == b"IMMS_SET_TOKENIZER_PROFILE=imms-set-native-v1", "backend_profile")
    values = [decode(line) for line in lines[:-1]]
    require(values[0] is True, "backend_unavailable")
    b = dict(zip(BACKEND_KEYS[:10], values[1:11], strict=True))
    b["mounts"] = sorted([{"source": m["Source"], "destination": m["Destination"], "rw": m["RW"]} for m in values[11]], key=lambda m: (m["destination"], m["source"]))
    b["networks"] = sorted(values[12])
    b["set_tokenizer_profile"] = PROFILE
    return b


def round_score(lexeme):
    require(type(lexeme) in (str, Number) and len(lexeme) <= 128 and re.fullmatch(r"-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?", lexeme) is not None, "response_score")
    try:
        value = Decimal(lexeme)
        require(value.is_finite() and 0 <= value <= 1, "response_score")
        if value.is_zero():
            bits = 0x80000000 if value.is_signed() else 0
        elif value.adjusted() < -60:
            bits = 0
        else:
            exact = Fraction(*value.as_integer_ratio())
            def rational(bits):
                return Fraction.from_float(struct.unpack(">f", struct.pack(">I", bits))[0])
            low, high = 0, 0x3f800000
            while low + 1 < high:
                middle = (low + high) // 2
                if rational(middle) <= exact:
                    low = middle
                else:
                    high = middle
            left, right = exact - rational(low), rational(high) - exact
            bits = low if left < right or (left == right and low % 2 == 0) else high
        number = struct.unpack(">f", struct.pack(">I", bits))[0]
        return {"score_decimal_lexeme": str(lexeme), "score_float32_decimal": format(Decimal.from_float(number), "f"), "score_float32_bits": f"{bits:08x}"}
    except (InvalidOperation, OverflowError, ValueError):
        fail("response_score")


def numeric_integer(value, expected):
    return type(value) is Number and re.fullmatch(r"0|[1-9][0-9]*", value) is not None and len(value) < 16 and int(value) == expected


def validate_reply(entry, raw):
    require(len(raw) <= MAX_RESPONSE, "response_limit")
    result = decode(raw, numbers=True)
    obj(result, ("id", "model", "results", "usage"), "response_shape")
    require(result["id"] == "score-" + entry["request_id"] and result["model"] == "sparkclaw-reranker", "response_identity")
    obj(result["usage"], ("prompt_tokens", "total_tokens"), "response_usage")
    require(all(numeric_integer(v, entry["token_count"]) for v in result["usage"].values()), "response_usage")
    require(type(result["results"]) is list and len(result["results"]) == 1, "response_shape")
    value = result["results"][0]
    obj(value, ("index", "document", "relevance_score"), "response_shape")
    require(numeric_integer(value["index"], 0), "response_shape")
    obj(value["document"], ("text", "multi_modal"), "response_document")
    require(value["document"] == {"text": entry["body"]["documents"][0], "multi_modal": None}, "response_document")
    require(type(value["relevance_score"]) is Number, "response_score")
    return round_score(value["relevance_score"])


def headers_unique(items):
    values = {}
    for name, value in items:
        require(type(name) is str and type(value) is str and re.fullmatch(r"[!#$%&'*+.^_`|~0-9A-Za-z-]+", name) is not None and not any(c in value for c in "\r\n\x00"), "header_invalid")
        key = name.lower()
        require(key not in values, "duplicate_header")
        values[key] = value
    return values


def native_counters(raw):
    try:
        text = raw.decode("utf-8")
    except UnicodeError:
        fail("metrics_invalid")
    found = {}
    names = {"vllm:prefix_cache_queries_total": "prefix_cache_queries_total", "vllm:prefix_cache_hits_total": "prefix_cache_hits_total"}
    for line in text.splitlines():
        if not line or line.startswith("#"):
            continue
        name = re.split(r"[\s{]", line, maxsplit=1)[0]
        if name not in names:
            continue
        match = re.fullmatch(r"[^\s{]+\{([^{}]*)\}[ \t]+([^ \t]+)", line)
        require(match is not None and name not in found, "metrics_invalid")
        labels = {}
        for label in match[1].split(","):
            item = re.fullmatch(r'([a-zA-Z_][a-zA-Z0-9_]*)="([^"\\]*)"', label)
            require(item is not None and item[1] not in labels, "metrics_invalid")
            labels[item[1]] = item[2]
        require(labels == {"engine": "0", "model_name": "sparkclaw-reranker"}, "metrics_invalid")
        lexeme = match[2]
        require(len(lexeme) <= 128 and re.fullmatch(r"[+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?", lexeme) is not None, "metrics_invalid")
        try:
            value = Decimal(lexeme)
            require(value.is_finite() and 0 <= value <= 2**53 - 1 and value == value.to_integral_value(), "metrics_invalid")
            found[name] = int(value)
        except (InvalidOperation, ValueError):
            fail("metrics_invalid")
    require(set(found) == set(names) and found["vllm:prefix_cache_hits_total"] <= found["vllm:prefix_cache_queries_total"], "metrics_invalid")
    return {names[key]: value for key, value in found.items()}


def check_terminal(value):
    check_envelope(value, "phase-terminal", ("role", "phase", "status", "prereg_pin", "intent_pin", "catalog_pin", "completed_posts", "consumed_posts", "count_known", "code", "phase_result_pin", "provenance"))
    require(value["role"] in ("provider", "collector") and value["phase"] in PHASES and value["status"] in ("success", "failed") and integer(value["completed_posts"], 0, COUNT) and integer(value["consumed_posts"], 0, COUNT) and value["completed_posts"] <= value["consumed_posts"] and type(value["count_known"]) is bool, "terminal_shape")
    for key in ("prereg_pin", "intent_pin", "catalog_pin"):
        valid_pin(value[key])


class Ledger:
    def __init__(self, plan, role):
        require(plan.intent is not None and role in ("provider", "collector"), "intent_required")
        self.plan, self.role = plan, role
        self.directory = plan.runtime / (role + "-ledger")
        self.completed = self.consumed = 0
        self.pending = self.previous = None
        self.ended = False
        self.success = False
        directory(self.directory, fresh=True)
        for name in ("intent", "started"):
            self._write(name + ".json", envelope("run-" + name, **self.base(), expected_posts=COUNT, provenance=PROVENANCE))

    def base(self):
        return {"role": self.role, "phase": self.plan.phase, "prereg_pin": self.plan.prereg_pin, "intent_pin": self.plan.intent_pin}

    def _write(self, name, value):
        return durable_new(self.directory / name, canonical(value))

    def consume(self, entry):
        require(not self.ended and self.pending is None and self.consumed == self.completed and self.consumed < COUNT and entry == self.plan.requests[self.consumed], "request_order")
        value = envelope("cell-admission", **self.base(), **{k: entry[k] for k in ("ordinal", "cell_id", "input_commitment_sha256", "request_id", "body_pin")}, previous_result_pin=self.previous)
        # In-memory pending is set before durable I/O so write failures consume
        # this process and cannot accidentally open a backend connection.
        self.pending = (entry, pin(canonical(value)))
        self.consumed += 1
        return self._write(f'{entry["ordinal"]:04d}-admission.json', value)

    def cell_terminal(self, status, code, result_pin):
        entry, admission_pin = self.pending
        return self._write(f'{entry["ordinal"]:04d}-terminal.json', envelope("cell-terminal", **self.base(), **{k: entry[k] for k in ("ordinal", "cell_id", "request_id")}, admission_pin=admission_pin, status=status, code=code, result_pin=result_pin))

    def complete(self, value):
        require(not self.ended and self.pending is not None, "request_order")
        entry, admission_pin = self.pending
        result = envelope(self.role + "-cell-result", **self.base(), **{k: entry[k] for k in ("ordinal", "cell_id", "input_commitment_sha256", "request_id", "body_pin", "token_count")}, admission_pin=admission_pin, **value)
        identity = self._write(f'{entry["ordinal"]:04d}-result.json', result)
        self.cell_terminal("success", "complete", identity)
        self.previous, self.pending = identity, None
        self.completed += 1
        return identity

    def terminal(self, status, code, result_pin):
        return self._write("terminal.json", envelope("phase-terminal", **self.base(), status=status, catalog_pin=self.plan.value["catalog_pin"], completed_posts=self.completed, consumed_posts=self.consumed, count_known=self.pending is None, code=code, phase_result_pin=result_pin, provenance=PROVENANCE))

    def finish(self, result_pin=None):
        require(not self.ended and self.pending is None and self.completed == self.consumed == COUNT, "run_incomplete")
        self.terminal("success", "complete", result_pin)
        self.ended = self.success = True

    def stop(self, code):
        if self.ended:
            return
        self.ended = True
        require(type(code) is str and re.fullmatch(r"[a-z_]{1,64}", code) is not None, "error_code")
        # Preserve pending until run terminal so the uncertain I/O count remains
        # unknown. Failure of durable writes is itself irrecoverably consumed.
        try:
            if self.pending is not None:
                self.cell_terminal("failed", code, None)
        finally:
            self.terminal("failed", code, None)


def failure_response(ledger, observed):
    if observed is None:
        return
    status, headers, raw, tls = observed
    ledger._write("failure-response.json", envelope("failure-response", **ledger.base(), http_status=status,
        response_headers=headers, raw_body_base64=base64.b64encode(raw).decode("ascii"), raw_body_pin=pin(raw), tls=tls))


def http_once(method, path, body, headers, *, port, context=None):
    connection = (http.client.HTTPSConnection("127.0.0.1", port, timeout=300, context=context)
                  if context is not None else http.client.HTTPConnection("127.0.0.1", port, timeout=300))
    tls, observed = None, None
    try:
        connection.connect()
        if context is not None:
            require(context.check_hostname and context.verify_mode == ssl.CERT_REQUIRED, "tls_configuration")
            tls = {"version": connection.sock.version(), "cipher": connection.sock.cipher()[0], "peer_certificate_sha256": hashlib.sha256(connection.sock.getpeercert(binary_form=True)).hexdigest()}
        connection.putrequest(method, path, skip_accept_encoding=True)
        for key, value in headers.items():
            connection.putheader(key, value)
        if body is not None:
            connection.putheader("Content-Length", str(len(body)))
        connection.endheaders(body)
        response = connection.getresponse()
        returned = response.getheaders()
        raw = response.read((MAX_RESPONSE if method == "POST" else MAX_GET) + 1)
        observed = (response.status, returned, raw, tls)
        normalized = headers_unique(returned)
        require("transfer-encoding" not in normalized, "response_framing")
        limit = MAX_RESPONSE if method == "POST" else MAX_GET
        length = normalized.get("content-length")
        require(type(length) is str and len(length) <= 8 and re.fullmatch(r"0|[1-9][0-9]*", length) is not None and int(length) <= limit, "response_framing")
        require(len(raw) == int(length) and len(raw) <= limit, "response_framing")
        return response.status, returned, raw, tls
    except AdmissionError as error:
        error.observed = observed
        raise
    except Exception:
        error = AdmissionError("transport_failure")
        error.observed = observed
        raise error from None
    finally:
        connection.close()


def check_observation(kind, raw):
    if kind.startswith("models_"):
        value = decode(raw)
        require(type(value) is dict and type(value.get("data")) is list and len(value["data"]) == 1, "models_identity")
        model = value["data"][0]
        require(type(model) is dict and model.get("id") == "sparkclaw-reranker" and model.get("root") == "Qwen/Qwen3-Reranker-4B" and model.get("owned_by") == "vllm" and type(model.get("max_model_len")) is int and model["max_model_len"] == 8192, "models_identity")
    elif kind.startswith("version_"):
        require(decode(raw) == {"version": "0.23.0"}, "version_identity")
    elif kind.startswith("health_"):
        require(raw == b"", "health_identity")
    else:
        return native_counters(raw)


class BoundaryState:
    def __init__(self, plan):
        plan.verify_backend()
        self.plan, self.ledger = plan, Ledger(plan, "provider")
        self.observations = Observations(plan.output)
        self.metrics_due = False
        self.last_http = None

    def forward(self, method, path, header_items, body):
        try:
            require(not self.ledger.ended or self.ledger.completed == COUNT, "run_failed")
            headers = headers_unique(header_items)
            require(headers.get("host") == "127.0.0.1:19483" and "transfer-encoding" not in headers, "request_headers")
            if method == "GET":
                require(path in GET_PATHS and body == b"" and set(headers) <= {"host", "content-length"} and headers.get("content-length", "0") == "0", "route_invalid")
                entry, forwarded = None, {}
            elif method == "POST" and path == "/v1/rerank":
                require(not self.ledger.ended and not self.metrics_due and self.ledger.consumed < COUNT, "request_order")
                entry = self.plan.requests[self.ledger.consumed]
                require(set(headers) == {"host", "content-length", "content-type", "x-request-id"} and headers["content-type"] == "application/json" and headers["content-length"] == str(len(body)) and headers["x-request-id"] == entry["request_id"] and body == canonical(entry["body"]), "request_mismatch")
                forwarded = {"Content-Type": "application/json", "X-Request-Id": entry["request_id"]}
            else:
                fail("route_invalid")
            before = self.observations.save(self.plan.verify_backend())
            if entry is not None:
                self.ledger.consume(entry)
            status, upstream_headers, raw, _ = http_once(method, path, body if entry else None, forwarded, port=BACKEND_PORT)
            self.last_http = (status, upstream_headers, raw, None)
            after = self.observations.save(self.plan.verify_backend())
            upstream = headers_unique(upstream_headers)
            require(status == 200, "upstream_status")
            if entry is not None:
                require(upstream.get("content-type", "").split(";", 1)[0] == "application/json", "response_content_type")
                self.ledger.complete({"raw_response_pin": pin(raw), "http_status": status, **validate_reply(entry, raw), "backend_before_pin": before, "backend_after_pin": after})
                self.metrics_due = True
            else:
                kind = {"/v1/models": "models_start", "/version": "version_start", "/health": "health_start", "/metrics": "metrics"}[path]
                check_observation(kind, raw)
                if path == "/metrics":
                    self.metrics_due = False
                    if self.ledger.completed == COUNT and not self.ledger.ended:
                        self.ledger.finish()
            returned = {"Content-Type": upstream.get("content-type", "application/octet-stream"), PREREG_HEADER: self.plan.prereg_pin["sha256"], INTENT_HEADER: self.plan.intent_pin["sha256"], DEPLOYMENT_HEADER: self.plan.value["deployment_record_pin"]["sha256"]}
            if entry is not None:
                returned["X-Request-Id"] = entry["request_id"]
            self.last_http = None
            return status, returned, raw
        except BaseException as error:
            code = error.code if isinstance(error, AdmissionError) else "boundary_failure"
            try:
                failure_response(self.ledger, getattr(error, "observed", None) or self.last_http)
            finally:
                self.ledger.stop(code)
            fail(code)


class BoundaryHandler(BaseHTTPRequestHandler):
    server_version, sys_version = "IMMSNativeSet", ""

    def setup(self):
        super().setup()
        self.connection.settimeout(10)

    def log_message(self, *_):
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
            require(len(length) <= 8 and re.fullmatch(r"0|[1-9][0-9]*", length) is not None and int(length) <= MAX_RESPONSE, "request_length")
            body = self.rfile.read(int(length))
            require(len(body) == int(length), "request_length")
            status, returned, raw = self.server.state.forward(method, self.path, self.headers.items(), body)
        except BaseException as error:
            code = error.code if isinstance(error, AdmissionError) else "boundary_failure"
            self.server.state.ledger.stop(code)
            status, returned, raw = 409, {}, b""
        try:
            self.send_response(status)
            for key, value in returned.items():
                self.send_header(key, value)
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
        except Exception:
            self.server.state.ledger.stop("response_write")


class Collector:
    def __init__(self, plan):
        self.plan, self.ledger = plan, Ledger(plan, "collector")
        self.observations = Observations(plan.output)
        self.last_http = None
        try:
            self.context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
            self.context.minimum_version = ssl.TLSVersion.TLSv1_2
            self.context.load_verify_locations(cadata=plan.ca.decode("ascii"))
            self.peer_sha = hashlib.sha256(ssl.PEM_cert_to_DER_cert(plan.ca.decode("ascii"))).hexdigest()
        except Exception:
            self.ledger.stop("tls_configuration")
            fail("tls_configuration")

    def request(self, method, path, entry=None):
        body = canonical(entry["body"]) if entry else None
        headers = {"Content-Type": "application/json", "X-Request-Id": entry["request_id"]} if entry else {}
        if entry is not None:
            self.ledger.consume(entry)
        status, returned, raw, tls = http_once(method, path, body, headers, port=19483, context=self.context)
        self.last_http = (status, returned, raw, tls)
        identity = headers_unique(returned)
        require(status == 200, "response_status")
        for header, expected in ((PREREG_HEADER, self.plan.prereg_pin), (INTENT_HEADER, self.plan.intent_pin), (DEPLOYMENT_HEADER, self.plan.value["deployment_record_pin"])):
            require(identity.get(header.lower()) == expected["sha256"], "response_header_identity")
        require(identity.get("x-request-id") == entry["request_id"] if entry else "x-request-id" not in identity, "response_header_identity")
        obj(tls, ("version", "cipher", "peer_certificate_sha256"), "tls_identity")
        require(tls["version"] in ("TLSv1.2", "TLSv1.3") and type(tls["cipher"]) is str and tls["cipher"] and tls["peer_certificate_sha256"] == self.peer_sha, "tls_identity")
        if entry:
            require(identity.get("content-type", "").split(";", 1)[0] == "application/json", "response_content_type")
        return status, returned, raw, tls

    def observe(self, kind, index=None):
        name = "models" if kind.startswith("models_") else kind.split("_", 1)[0]
        path = "/v1/models" if name == "models" else "/" + name
        status, returned, raw, tls = self.request("GET", path)
        observation = envelope("http-observation", phase=self.plan.phase, kind=kind, index=index, http_status=status, response_headers=returned, raw_body_base64=base64.b64encode(raw).decode("ascii"), raw_body_pin=pin(raw), tls=tls)
        identity = self.observations.save(observation)
        counters = check_observation(kind, raw)
        self.last_http = None
        return identity, ({"observation_pin": identity, **counters} if counters is not None else None)

    def provider_record(self, entry, raw, score):
        record_raw = read_raw(self.plan.runtime / "provider-ledger" / f'{entry["ordinal"]:04d}-result.json')
        record = decode(record_raw, canonical_required=True)
        expected = envelope("provider-cell-result", role="provider", phase=self.plan.phase, prereg_pin=self.plan.prereg_pin, intent_pin=self.plan.intent_pin, **{k: entry[k] for k in ("ordinal", "cell_id", "input_commitment_sha256", "request_id", "body_pin", "token_count")}, raw_response_pin=pin(raw), http_status=200, **score)
        obj(record, (*expected.keys(), "admission_pin", "backend_before_pin", "backend_after_pin"), "provider_record")
        require(all(record[k] == v for k, v in expected.items()), "provider_record")
        for key in ("admission_pin", "backend_before_pin", "backend_after_pin"):
            valid_pin(record[key])
        for key in ("backend_before_pin", "backend_after_pin"):
            actual = read_pin(self.observations.root / (record[key]["sha256"] + ".json"), record[key])
            require(actual == canonical(self.plan.deployment["backend"]), "provider_record")
        return pin(record_raw)

    def run(self):
        require(not self.ledger.ended and self.ledger.consumed == 0, "run_already_consumed")
        results, observations = [], {"metrics": []}
        try:
            for kind in ("models_start", "version_start", "health_start"):
                observations[kind], _ = self.observe(kind)
            observation, before = self.observe("metrics", 0)
            observations["metrics"].append(observation)
            for entry in self.plan.requests:
                status, returned, raw, tls = self.request("POST", "/v1/rerank", entry)
                score = validate_reply(entry, raw)
                provider_pin = self.provider_record(entry, raw, score)
                observation, after = self.observe("metrics", entry["ordinal"] + 1)
                observations["metrics"].append(observation)
                require(all(after[key] >= before[key] for key in ("prefix_cache_queries_total", "prefix_cache_hits_total")), "cache_regression")
                collector_pin = self.ledger.complete({"raw_response_pin": pin(raw), "http_status": status, **score, "provider_record_pin": provider_pin, "response_headers": returned, "raw_response_base64": base64.b64encode(raw).decode("ascii"), "cache_before": before, "cache_after": after, "tls": tls})
                self.last_http = None
                results.append({**{k: entry[k] for k in ("ordinal", "cell_id", "input_commitment_sha256", "request_id")}, **score, "collector_record_pin": collector_pin, "provider_record_pin": provider_pin})
                before = after
            for kind in ("models_end", "version_end", "health_end"):
                observations[kind], _ = self.observe(kind)
            provider_terminal = decode(read_raw(self.plan.runtime / "provider-ledger/terminal.json"), canonical_required=True)
            check_terminal(provider_terminal)
            require(provider_terminal["status"] == "success" and provider_terminal["role"] == "provider" and provider_terminal["phase"] == self.plan.phase and provider_terminal["prereg_pin"] == self.plan.prereg_pin and provider_terminal["intent_pin"] == self.plan.intent_pin and provider_terminal["catalog_pin"] == self.plan.value["catalog_pin"] and provider_terminal["completed_posts"] == provider_terminal["consumed_posts"] == COUNT and provider_terminal["count_known"] is True and provider_terminal["code"] == "complete" and provider_terminal["phase_result_pin"] is None, "provider_terminal")
            result = envelope("phase-result", phase=self.plan.phase, status="success", prereg_pin=self.plan.prereg_pin, intent_pin=self.plan.intent_pin, catalog_pin=self.plan.value["catalog_pin"], deployment_record_pin=self.plan.value["deployment_record_pin"], post_count=COUNT, retry_count=0, results=results, observation_pins=observations, provenance=PROVENANCE)
            result_pin = durable_new(self.plan.output / "phase-result.json", canonical(result))
            self.ledger.finish(result_pin)
            return result
        except BaseException as error:
            code = error.code if isinstance(error, AdmissionError) else "collector_failure"
            try:
                failure_response(self.ledger, getattr(error, "observed", None) or self.last_http)
            finally:
                self.ledger.stop(code)
            fail(code)


def _signal(_number, _frame):
    fail("process_interrupted")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("validate-plan", "boundary", "collect"))
    for key in ("prereg", "inputs", "catalog", "reference-tokens", "deployment-record", "ca-file"):
        parser.add_argument("--" + key, type=Path, required=True)
    parser.add_argument("--expected-sha256", required=True)
    parser.add_argument("--expected-size", type=int, required=True)
    for key in ("intent", "previous-terminal", "cert-file", "key-file"):
        parser.add_argument("--" + key, type=Path)
    parser.add_argument("--intent-sha256")
    parser.add_argument("--intent-size", type=int)
    args = parser.parse_args()
    state = None
    try:
        plan = Plan(args.prereg, args.expected_sha256, args.expected_size, inputs=args.inputs, catalog=args.catalog, reference_tokens=args.reference_tokens, deployment_record=args.deployment_record, ca_file=args.ca_file, intent=args.intent, intent_sha256=args.intent_sha256, intent_size=args.intent_size, previous_terminal=args.previous_terminal)
        if args.mode == "validate-plan":
            print(json.dumps({"status": "valid", "planned_posts_per_phase": COUNT, "executed_posts": 0}, sort_keys=True))
            return 0
        require(plan.intent is not None, "intent_required")
        signal.signal(signal.SIGTERM, _signal)
        signal.signal(signal.SIGINT, _signal)
        if args.mode == "collect":
            state = Collector(plan)
            state.run()
            return 0
        require(args.cert_file is not None and args.key_file is not None, "tls_configuration")
        # Read/validate every path component before OpenSSL consumes these task
        # owned files. Keep the opened descriptors for /proc/self/fd loading.
        descriptors = []
        try:
            for path in (args.cert_file, args.key_file):
                raw = read_raw(path, MAX_GET)
                if path == args.cert_file:
                    require(raw == plan.ca, "tls_certificate_pin")
                parent, name = open_parent(path)
                try:
                    descriptors.append(os.open(name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=parent))
                finally:
                    os.close(parent)
                require(stat.S_ISREG(os.fstat(descriptors[-1]).st_mode), "tls_configuration")
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.minimum_version = ssl.TLSVersion.TLSv1_2
            context.load_cert_chain(*(f"/proc/self/fd/{fd}" for fd in descriptors))
        finally:
            for fd in descriptors:
                os.close(fd)
        state = BoundaryState(plan)
        with HTTPServer(("127.0.0.1", 19483), BoundaryHandler) as server:
            server.state = state
            server.socket = context.wrap_socket(server.socket, server_side=True)
            server.serve_forever()
        return 0
    except BaseException as error:
        code = error.code if isinstance(error, AdmissionError) else "process_failure"
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
