#!/usr/bin/env python3
"""Synthetic loopback-only client for the E2 reranker nine-step smoke."""

from __future__ import annotations

import argparse
import copy
import hashlib
import ipaddress
import json
import math
import os
import re
import struct
import sys
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable, NoReturn

try:
    from scripts.e2_reranker_evidence import (
        ContractBundle,
        DEFAULT_CONTRACT_ROOT,
        EvidenceFailure,
        canonical_bytes,
        fail,
        pinned_canonical_artifact,
        produce_receipt,
        sha256_bytes,
    )
except ModuleNotFoundError:  # Direct execution from scripts/.
    from e2_reranker_evidence import (  # type: ignore[no-redef]
        ContractBundle,
        DEFAULT_CONTRACT_ROOT,
        EvidenceFailure,
        canonical_bytes,
        fail,
        pinned_canonical_artifact,
        produce_receipt,
        sha256_bytes,
    )


MAX_RESPONSE_BYTES = 1 << 20
METRICS_PREFIX_CACHE = re.compile(
    r'enable_prefix_caching="(?P<value>true|false)"', re.IGNORECASE
)


class NumberLexeme(str):
    """A JSON number whose exact transport spelling is retained."""


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args: Any, **_kwargs: Any) -> NoReturn:
        fail("content_free_smoke", "redirect_forbidden", "redirect is forbidden")


def _reject_constant(_value: str) -> NoReturn:
    fail("content_free_smoke", "non_finite_json_number", "non-finite JSON is forbidden")


def _unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    value: dict[str, Any] = {}
    for key, child in pairs:
        if key in value:
            fail("content_free_smoke", "duplicate_json_key", "response repeats a JSON key")
        value[key] = child
    return value


def _json_object(raw: bytes, *, preserve_numbers: bool = False) -> dict[str, Any]:
    try:
        text = raw.decode("utf-8", errors="strict")
        options: dict[str, Any] = {
            "object_pairs_hook": _unique_object,
            "parse_constant": _reject_constant,
        }
        if preserve_numbers:
            options.update(parse_int=NumberLexeme, parse_float=NumberLexeme)
        value = json.loads(text, **options)
    except EvidenceFailure:
        raise
    except (UnicodeDecodeError, json.JSONDecodeError):
        fail("content_free_smoke", "response_json_invalid", "response is not strict JSON")
    if not isinstance(value, dict):
        fail("content_free_smoke", "response_shape_invalid", "response is not an object")
    return value


def _integer(value: Any) -> int:
    if isinstance(value, bool):
        fail("content_free_smoke", "response_shape_invalid", "boolean is not an integer")
    text = str(value)
    if not re.fullmatch(r"-?(?:0|[1-9][0-9]*)", text):
        fail("content_free_smoke", "response_shape_invalid", "integer field is invalid")
    return int(text)


def _integer_array(value: Any) -> list[int]:
    if not isinstance(value, list):
        fail("content_free_smoke", "response_shape_invalid", "token IDs are not an array")
    return [_integer(item) for item in value]


def _const_object(schema: dict[str, Any]) -> dict[str, Any]:
    properties = schema.get("properties")
    required = schema.get("required")
    if not isinstance(properties, dict) or not isinstance(required, list):
        fail("content_free_smoke", "central_schema_shape_invalid", "central const object is invalid")
    result: dict[str, Any] = {}
    for field in required:
        definition = properties.get(field)
        if not isinstance(definition, dict) or "const" not in definition:
            fail("content_free_smoke", "central_schema_shape_invalid", "required const is missing")
        result[field] = copy.deepcopy(definition["const"])
    return result


def _utc_micro(value: datetime) -> str:
    if value.tzinfo is None or value.utcoffset() is None:
        fail("content_free_smoke", "clock_invalid", "clock must be timezone-aware")
    return value.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")


def _loopback_origin(origin: str) -> str:
    parsed = urllib.parse.urlsplit(origin)
    if (
        parsed.scheme not in {"http", "https"}
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
        or parsed.path not in {"", "/"}
        or parsed.hostname is None
    ):
        fail("content_free_smoke", "loopback_origin_invalid", "fake origin is invalid")
    try:
        address = ipaddress.ip_address(parsed.hostname)
    except ValueError:
        fail("content_free_smoke", "dns_forbidden", "fake origin must use a numeric IP")
    if not address.is_loopback:
        fail("content_free_smoke", "non_loopback_forbidden", "fake smoke is loopback-only")
    return origin.rstrip("/")


class SyntheticLoopbackSmokeClient:
    """Runs one synthetic smoke without proxy, DNS, redirect, retry, or fallback."""

    def __init__(
        self,
        bundle: ContractBundle,
        *,
        origin: str,
        timeout: float,
        clock: Callable[[], datetime] | None = None,
    ):
        if timeout <= 0:
            fail("content_free_smoke", "timeout_invalid", "timeout must be positive")
        self.bundle = bundle
        self.origin = _loopback_origin(origin)
        self.timeout = timeout
        self.clock = clock or (lambda: datetime.now(timezone.utc))
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            _NoRedirect(),
        )
        self.actual_order: list[str] = []

    def _request(
        self,
        step: str,
        method: str,
        path: str,
        *,
        body: bytes | None = None,
    ) -> tuple[bytes, Any, str]:
        self.actual_order.append(step)
        headers = {"Accept": "application/json"}
        if body is not None:
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(
            self.origin + path,
            data=body,
            headers=headers,
            method=method,
        )
        try:
            with self.opener.open(request, timeout=self.timeout) as response:
                raw = response.read(MAX_RESPONSE_BYTES + 1)
                status = response.status
                response_headers = response.headers
                content_type = response_headers.get_content_type()
        except EvidenceFailure:
            raise
        except urllib.error.HTTPError as error:
            if 300 <= error.code < 400:
                fail("content_free_smoke", "redirect_forbidden", "redirect is forbidden")
            fail("content_free_smoke", "http_status_invalid", "response is not HTTP 200")
        except (OSError, urllib.error.URLError):
            fail("content_free_smoke", "transport_failed", "loopback transport failed")
        if status != 200:
            fail("content_free_smoke", "http_status_invalid", "response is not HTTP 200")
        if len(raw) > MAX_RESPONSE_BYTES:
            fail("content_free_smoke", "response_too_large", "response exceeds the smoke limit")
        return raw, response_headers, content_type

    @staticmethod
    def _pin_headers(headers: Any, manifest: dict[str, Any], manifest_sha: str) -> dict[str, str]:
        names = manifest["live_identity_headers"]
        expected = {
            names["manifest_sha256"]: manifest_sha,
            names["deployment_revision"]: manifest["runtime"]["deployment_revision"],
        }
        observed: dict[str, str] = {}
        for name, expected_value in expected.items():
            values = headers.get_all(name, [])
            if len(values) != 1:
                fail("content_free_smoke", "live_header_cardinality_invalid", "live pin header is missing or repeated")
            if values[0] != expected_value:
                fail("content_free_smoke", "live_header_mismatch", "live pin header differs")
            observed[name] = values[0]
        return observed

    def _observation(
        self,
        step: str,
        kind: str,
        path: str,
        manifest: dict[str, Any],
        manifest_sha: str,
    ) -> dict[str, Any]:
        raw, headers, _content_type = self._request(step, "GET", path)
        pin_headers = self._pin_headers(headers, manifest, manifest_sha)
        if kind == "models":
            body = _json_object(raw)
            data = body.get("data")
            if not isinstance(data, list) or len(data) != 1 or not isinstance(data[0], dict):
                fail("content_free_smoke", "models_shape_invalid", "models response shape differs")
            expected = manifest["expected_models_projection"]
            try:
                projection = {field: data[0][field] for field in expected}
            except KeyError:
                fail("content_free_smoke", "models_shape_invalid", "models projection is missing")
            if projection != expected:
                fail("content_free_smoke", "models_projection_mismatch", "models projection differs")
        elif kind == "version":
            body = _json_object(raw)
            projection = {"vllm_reported_version": body.get("version")}
            if projection["vllm_reported_version"] != manifest["runtime"]["vllm_reported_version"]:
                fail("content_free_smoke", "vllm_version_mismatch", "vLLM version differs")
        elif kind == "health":
            projection = {"healthy": True}
        elif kind == "metrics":
            try:
                text = raw.decode("utf-8", errors="strict")
            except UnicodeDecodeError:
                fail("content_free_smoke", "metrics_utf8_invalid", "metrics are not UTF-8")
            matches = METRICS_PREFIX_CACHE.findall(text)
            if len(matches) != 1 or matches[0].lower() != "false":
                fail("content_free_smoke", "prefix_cache_observation_invalid", "prefix cache is not provably disabled")
            projection = {"prefix_caching_enabled": False}
        else:
            fail("content_free_smoke", "observation_kind_invalid", "observation kind is invalid")
        return {
            "path": path,
            "status": 200,
            "headers": pin_headers,
            "raw_body_sha256": sha256_bytes(raw),
            "raw_body_size_bytes": len(raw),
            "projection": projection,
        }

    def _completion(
        self,
        manifest: dict[str, Any],
        manifest_sha: str,
    ) -> dict[str, Any]:
        binding = manifest["model"]["model_binding"]
        body = {
            "model": manifest["model"]["served_name"],
            "prompt": binding["prefix_token_ids"] + binding["suffix_token_ids"],
            **manifest["request_contract"]["parameters"],
        }
        request_raw = canonical_bytes(body, stage="content_free_smoke")
        path = manifest["routes"]["completion_path"]
        raw, headers, content_type = self._request(
            "completion", "POST", path, body=request_raw
        )
        if content_type != "application/json":
            fail("content_free_smoke", "completion_content_type_invalid", "completion is not JSON")
        pin_headers = self._pin_headers(headers, manifest, manifest_sha)
        envelope = _json_object(raw, preserve_numbers=True)
        try:
            choices = envelope["choices"]
            if not isinstance(choices, list) or len(choices) != 1 or not isinstance(choices[0], dict):
                raise TypeError
            choice = choices[0]
            if _integer(choice["index"]) != 0:
                raise TypeError
            logprobs = choice["logprobs"]
            if not isinstance(logprobs, dict):
                raise TypeError
            raw_labels = logprobs["logprob_token_ids"]
            if not isinstance(raw_labels, list):
                raise TypeError
            prompt_token_ids = _integer_array(envelope["prompt_token_ids"])
            generated_token_ids = _integer_array(choice["token_ids"])
            model = envelope["model"]
            finish_reason = choice["finish_reason"]
        except (KeyError, TypeError):
            fail("content_free_smoke", "completion_shape_invalid", "completion envelope differs")

        labels: list[dict[str, Any]] = []
        for raw_label in raw_labels:
            if not isinstance(raw_label, dict):
                fail("content_free_smoke", "completion_shape_invalid", "label logprob shape differs")
            try:
                token_id = _integer(raw_label["token_id"])
                lexeme_value = raw_label["logprob"]
            except KeyError:
                fail("content_free_smoke", "completion_shape_invalid", "label logprob is missing")
            if not isinstance(lexeme_value, NumberLexeme):
                fail("content_free_smoke", "logprob_number_required", "logprob must be a JSON number")
            lexeme = str(lexeme_value).lower()
            try:
                value = float(lexeme)
            except ValueError:
                fail("content_free_smoke", "label_logprob_invalid", "logprob number is invalid")
            if not math.isfinite(value) or value > 0:
                fail("content_free_smoke", "label_logprob_invalid", "logprob is not finite and <= 0")
            if math.exp(value) == 0.0:
                fail("content_free_smoke", "label_logprob_underflow", "logprob exp underflows")
            labels.append(
                {
                    "token_id": token_id,
                    "logprob": lexeme,
                    "float64_bits": struct.pack(">d", value).hex(),
                }
            )
        expected_labels = manifest["response_contract"]["explicit_label_logprobs"]
        if [entry["token_id"] for entry in labels] != expected_labels:
            fail("content_free_smoke", "label_logprob_ids_mismatch", "explicit label order differs")
        projection = {
            "model": model,
            "choice_count": len(choices),
            "prompt_token_ids": prompt_token_ids,
            "generated_token_ids": generated_token_ids,
            "finish_reason": finish_reason,
            "label_logprobs": labels,
        }
        projection_raw = canonical_bytes(projection, stage="content_free_smoke")
        if raw == projection_raw:
            fail("content_free_smoke", "raw_projection_not_distinct", "fake envelope must differ from projection")
        request_definition = self.bundle.smoke_schema["$defs"]["completion"]["properties"]["request"]
        request_constants = _const_object(
            {
                "required": [
                    field
                    for field in request_definition["required"]
                    if field not in {"raw_body_sha256", "raw_body_size_bytes", "body"}
                ],
                "properties": request_definition["properties"],
            }
        )
        return {
            "request": {
                **request_constants,
                "raw_body_sha256": sha256_bytes(request_raw),
                "raw_body_size_bytes": len(request_raw),
                "body": body,
            },
            "response": {
                "status": 200,
                "content_type": "application/json",
                "headers": pin_headers,
                "raw_body_sha256": sha256_bytes(raw),
                "raw_body_size_bytes": len(raw),
                "projection_sha256": sha256_bytes(projection_raw),
                "projection_size_bytes": len(projection_raw),
                "projection": projection,
            },
        }

    def run(self, manifest: dict[str, Any], manifest_raw: bytes) -> bytes:
        if manifest.get("artifact_class") != "synthetic_fixture":
            fail(
                "content_free_smoke",
                "live_smoke_not_implemented",
                "stage 3 client accepts only synthetic fixtures on loopback",
            )
        if manifest_raw != canonical_bytes(manifest, stage="deployment_manifest_bytes"):
            fail(
                "deployment_manifest_bytes",
                "manifest_bytes_mismatch",
                "manifest object does not match its canonical bytes",
            )
        self.bundle.validate_manifest(manifest, allow_synthetic=True)
        if self.actual_order:
            fail("content_free_smoke", "client_single_use", "smoke client instance is single-use")
        manifest_sha = sha256_bytes(manifest_raw)
        started_at = _utc_micro(self.clock())
        order = copy.deepcopy(
            self.bundle.smoke_schema["properties"]["observation_order"]["const"]
        )
        flights: dict[str, dict[str, Any]] = {"preflight": {}, "postflight": {}}
        completion: dict[str, Any] | None = None
        for step in order:
            if step == "completion":
                completion = self._completion(manifest, manifest_sha)
                continue
            try:
                kind, phase = step.rsplit("_", 1)
                flight_name = {"pre": "preflight", "post": "postflight"}[phase]
                path = manifest["routes"][f"{kind}_path"]
            except (KeyError, ValueError):
                fail("content_free_smoke", "observation_order_invalid", "central observation order is invalid")
            flights[flight_name][kind] = self._observation(
                step, kind, path, manifest, manifest_sha
            )
        if self.actual_order != order or completion is None:
            fail("content_free_smoke", "observation_order_mismatch", "transport order differs")
        finished_at = _utc_micro(self.clock())

        smoke_schema = self.bundle.smoke_schema
        receipt = {
            "$schema": smoke_schema["properties"]["$schema"]["const"],
            "artifact": smoke_schema["properties"]["artifact"]["const"],
            "format_version": smoke_schema["properties"]["format_version"]["const"],
            "artifact_class": "synthetic_fixture",
            "canonicalization": copy.deepcopy(manifest["canonicalization"]),
            "manifest_binding": {
                "sha256": manifest_sha,
                "size_bytes": len(manifest_raw),
                "artifact_class": "synthetic_fixture",
            },
            "scope": _const_object(smoke_schema["properties"]["scope"]),
            "started_at": started_at,
            "finished_at": finished_at,
            "attempt": _const_object(smoke_schema["properties"]["attempt"]),
            "observation_order": order,
            "preflight": flights["preflight"],
            "completion": completion,
            "postflight": flights["postflight"],
            "result": _const_object(smoke_schema["properties"]["result"]),
        }
        return produce_receipt(
            self.bundle, manifest, receipt, allow_synthetic=True
        )


def _write_new(path: Path, raw: bytes) -> None:
    try:
        with path.open("xb") as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
    except FileExistsError:
        fail("output", "output_exists", "refusing to overwrite output")
    except OSError as error:
        fail("output", "output_write_failed", f"output could not be written: {error}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--contract-root", type=Path, default=DEFAULT_CONTRACT_ROOT)
    parser.add_argument("--synthetic-manifest", type=Path, required=True)
    parser.add_argument("--expected-manifest-sha256", required=True)
    parser.add_argument("--expected-manifest-size", type=int, required=True)
    parser.add_argument("--loopback-origin", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--timeout", type=float, default=2.0)
    args = parser.parse_args()
    if not re.fullmatch(r"[a-f0-9]{64}", args.expected_manifest_sha256):
        parser.error("expected manifest SHA-256 must be 64 lowercase hex")
    if args.expected_manifest_size < 1:
        parser.error("expected manifest size must be positive")
    return args


def main() -> int:
    args = parse_args()
    try:
        bundle = ContractBundle.load(args.contract_root)
        manifest, manifest_raw = pinned_canonical_artifact(
            bundle,
            args.synthetic_manifest,
            expected_sha256=args.expected_manifest_sha256,
            expected_size=args.expected_manifest_size,
            target="deployment_manifest",
            allow_synthetic=True,
        )
        client = SyntheticLoopbackSmokeClient(
            bundle,
            origin=args.loopback_origin,
            timeout=args.timeout,
        )
        receipt_raw = client.run(manifest, manifest_raw)
        _write_new(args.output, receipt_raw)
        print(
            "PASS synthetic loopback smoke: "
            f"sha256={hashlib.sha256(receipt_raw).hexdigest()} size={len(receipt_raw)}"
        )
    except EvidenceFailure as error:
        print(f"FAIL [{error.stage}/{error.code}] {error.message}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
