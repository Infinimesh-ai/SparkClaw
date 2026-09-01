#!/usr/bin/env python3
"""Synthetic loopback-only native rerank client for E2 evidence v2."""

from __future__ import annotations

import argparse
import base64
import copy
import ipaddress
import json
import math
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
    from scripts.e2_reranker_evidence import _write_new
    from scripts.e2_reranker_evidence_v2 import (
        DEFAULT_CONTRACT_ROOT,
        DEPLOYMENT_HEADER,
        MANIFEST_HEADER,
        OBSERVATION_ORDER,
        EvidenceFailure,
        RawNumber,
        V2ContractBundle,
        _parse_raw_response,
        _raw_integer,
        canonical_bytes,
        fail,
        pinned_canonical_artifact,
        produce_receipt,
        sha256_bytes,
    )
except ModuleNotFoundError:  # Direct execution from scripts/.
    from e2_reranker_evidence import _write_new  # type: ignore[no-redef]
    from e2_reranker_evidence_v2 import (  # type: ignore[no-redef]
        DEFAULT_CONTRACT_ROOT,
        DEPLOYMENT_HEADER,
        MANIFEST_HEADER,
        OBSERVATION_ORDER,
        EvidenceFailure,
        RawNumber,
        V2ContractBundle,
        _parse_raw_response,
        _raw_integer,
        canonical_bytes,
        fail,
        pinned_canonical_artifact,
        produce_receipt,
        sha256_bytes,
    )


MAX_RESPONSE_BYTES = 1 << 20
METRICS_CONFIG = re.compile(
    r'^sparkclaw:e2_prefix_cache_config\{enabled="(?P<enabled>true|false)",'
    r'resolved_config_sha256="(?P<sha>[a-f0-9]{64})",'
    r'resolved_config_size_bytes="(?P<size>0|[1-9][0-9]*)"\} 1$',
    re.MULTILINE,
)
METRICS_QUERIES = re.compile(
    r"^vllm:prefix_cache_queries_total (?P<value>0|[1-9][0-9]*)$", re.MULTILINE
)
METRICS_HITS = re.compile(
    r"^vllm:prefix_cache_hits_total (?P<value>0|[1-9][0-9]*)$", re.MULTILINE
)


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args: Any, **_kwargs: Any) -> NoReturn:
        fail("content_free_smoke", "redirect_forbidden", "redirect is forbidden")


def _json_object(raw: bytes) -> dict[str, Any]:
    def unique(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        value: dict[str, Any] = {}
        for key, child in pairs:
            if key in value:
                fail("content_free_smoke", "duplicate_json_key", "response repeats a JSON key")
            value[key] = child
        return value

    def reject(_value: str) -> NoReturn:
        fail("content_free_smoke", "non_finite_json_number", "response contains a non-finite number")

    try:
        value = json.loads(
            raw.decode("utf-8", errors="strict"),
            object_pairs_hook=unique,
            parse_constant=reject,
        )
    except EvidenceFailure:
        raise
    except (UnicodeDecodeError, json.JSONDecodeError):
        fail("content_free_smoke", "response_json_invalid", "response is not strict JSON")
    if not isinstance(value, dict):
        fail("content_free_smoke", "response_shape_invalid", "response is not an object")
    return value


def _const_object(schema: dict[str, Any]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for field in schema["required"]:
        definition = schema["properties"][field]
        if "const" in definition:
            result[field] = copy.deepcopy(definition["const"])
        elif definition.get("type") == "object":
            result[field] = _const_object(definition)
        else:
            fail("content_free_smoke", "schema_constant_missing", f"{field} is not constant")
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


class SyntheticNativeRerankClient:
    """Runs one native synthetic smoke without proxy, retry, redirect, or fallback."""

    def __init__(
        self,
        bundle: V2ContractBundle,
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
        self.opener.addheaders = []
        self.actual_order: list[str] = []

    def _request(
        self,
        step: str,
        method: str,
        path: str,
        *,
        body: bytes | None = None,
        headers: dict[str, str] | None = None,
    ) -> tuple[bytes, Any, str]:
        self.actual_order.append(step)
        request_headers: dict[str, str] = {}
        if body is not None:
            request_headers["Content-Type"] = "application/json"
        request_headers.update(headers or {})
        request = urllib.request.Request(
            self.origin + path,
            data=body,
            headers=request_headers,
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
    def _pin_headers(headers: Any, manifest: dict[str, Any], manifest_sha: str) -> list[dict[str, str]]:
        expected = (
            (DEPLOYMENT_HEADER, manifest["runtime"]["deployment_revision"]),
            (MANIFEST_HEADER, manifest_sha),
        )
        observed: list[dict[str, str]] = []
        for name, expected_value in expected:
            values = headers.get_all(name, [])
            if len(values) != 1:
                fail(
                    "content_free_smoke",
                    "live_header_cardinality_invalid",
                    "live pin header is missing or repeated",
                )
            if values[0] != expected_value:
                fail("content_free_smoke", "live_header_mismatch", "live pin header differs")
            observed.append({"name": name, "value": values[0]})
        return observed

    @staticmethod
    def _metrics_projection(raw: bytes) -> dict[str, Any]:
        try:
            text = raw.decode("utf-8", errors="strict")
        except UnicodeDecodeError:
            fail("content_free_smoke", "metrics_utf8_invalid", "metrics are not UTF-8")
        configs = list(METRICS_CONFIG.finditer(text))
        queries = list(METRICS_QUERIES.finditer(text))
        hits = list(METRICS_HITS.finditer(text))
        if len(configs) != 1 or len(queries) != 1 or len(hits) != 1:
            fail(
                "content_free_smoke",
                "prefix_cache_observation_invalid",
                "synthetic metrics must expose one config and one pair of counters",
            )
        config = configs[0].groupdict()
        return {
            "prefix_cache_config_sha256": config["sha"],
            "prefix_cache_config_size_bytes": int(config["size"]),
            "prefix_cache_enabled": config["enabled"] == "true",
            "prefix_cache_hits_total": int(hits[0].group("value")),
            "prefix_cache_queries_total": int(queries[0].group("value")),
        }

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
            if raw:
                fail("content_free_smoke", "health_body_invalid", "health response must be empty")
            projection = {"healthy": True}
        elif kind == "metrics":
            projection = self._metrics_projection(raw)
            cache = manifest["serving"]["prefix_cache"]
            if (
                projection["prefix_cache_enabled"] != cache["enabled"]
                or projection["prefix_cache_config_sha256"] != cache["resolved_config_sha256"]
                or projection["prefix_cache_config_size_bytes"]
                != cache["resolved_config_size_bytes"]
                or projection["prefix_cache_hits_total"]
                > projection["prefix_cache_queries_total"]
            ):
                fail(
                    "content_free_smoke",
                    "prefix_cache_observation_invalid",
                    "prefix cache metrics differ from the manifest",
                )
        else:
            fail("content_free_smoke", "observation_kind_invalid", "observation kind is invalid")
        return {
            "headers": pin_headers,
            "path": path,
            "projection": projection,
            "raw_body_sha256": sha256_bytes(raw),
            "raw_body_size_bytes": len(raw),
            "status": 200,
        }

    def _rerank(self, manifest: dict[str, Any], manifest_sha: str) -> dict[str, Any]:
        body = copy.deepcopy(manifest["request_contract"]["body"])
        request_raw = canonical_bytes(body, stage="content_free_smoke")
        request_headers = manifest["request_contract"]["headers"]
        wire_headers = {entry["name"]: entry["value"] for entry in request_headers}
        path = manifest["routes"]["rerank_path"]
        raw, headers, content_type = self._request(
            "rerank",
            "POST",
            path,
            body=request_raw,
            headers=wire_headers,
        )
        if content_type != "application/json":
            fail("content_free_smoke", "rerank_content_type_invalid", "rerank response is not JSON")
        pin_headers = self._pin_headers(headers, manifest, manifest_sha)
        parsed = _parse_raw_response(raw, "content_free_smoke")
        if set(parsed) != {"id", "model", "results", "usage"}:
            fail("content_free_smoke", "raw_response_unknown_field", "raw response fields differ")
        results = parsed["results"]
        usage = parsed["usage"]
        if (
            parsed["id"] != manifest["response_contract"]["exact_id"]
            or parsed["model"] != manifest["response_contract"]["exact_model"]
            or not isinstance(results, list)
            or len(results) != 1
            or not isinstance(results[0], dict)
            or not isinstance(usage, dict)
            or set(usage) != {"prompt_tokens", "total_tokens"}
        ):
            fail("content_free_smoke", "rerank_shape_invalid", "rerank envelope differs")
        item = results[0]
        if set(item) != {"document", "index", "relevance_score"}:
            fail("content_free_smoke", "raw_response_unknown_field", "result fields differ")
        document = item["document"]
        if (
            not isinstance(document, dict)
            or set(document) != {"multi_modal", "text"}
            or document["multi_modal"] is not None
            or document["text"] != body["documents"][0]
            or _raw_integer(item["index"], "content_free_smoke") != 0
        ):
            fail("content_free_smoke", "rerank_shape_invalid", "result document/index differs")
        score_number = item["relevance_score"]
        if not isinstance(score_number, RawNumber):
            fail("content_free_smoke", "raw_response_score_not_number", "score is not a JSON number")
        score_lexeme = score_number.lexeme
        try:
            score = float(score_lexeme)
        except ValueError:
            fail("content_free_smoke", "score_decimal_invalid", "score is invalid")
        if not math.isfinite(score) or score < 0 or score > 1:
            fail("content_free_smoke", "score_out_of_range", "score is outside [0,1]")
        prompt_tokens = _raw_integer(usage["prompt_tokens"], "content_free_smoke")
        total_tokens = _raw_integer(usage["total_tokens"], "content_free_smoke")
        if prompt_tokens < 1 or prompt_tokens != total_tokens:
            fail("content_free_smoke", "usage_token_count_mismatch", "usage token counts differ")
        projection = {
            "id": parsed["id"],
            "model": parsed["model"],
            "results": [
                {
                    "document": {"multi_modal": None, "text": document["text"]},
                    "index": 0,
                    "relevance_score": {
                        "decimal": score_lexeme,
                        "float64_bits": struct.pack(">d", score).hex(),
                    },
                }
            ],
            "usage": {"prompt_tokens": prompt_tokens, "total_tokens": total_tokens},
        }
        projection_raw = canonical_bytes(projection, stage="content_free_smoke")
        return {
            "request": {
                "authorization_material_persisted": False,
                "body": body,
                "content_type": "application/json",
                "headers": copy.deepcopy(request_headers),
                "method": "POST",
                "path": path,
                "raw_body_base64": base64.b64encode(request_raw).decode("ascii"),
                "raw_body_sha256": sha256_bytes(request_raw),
                "raw_body_size_bytes": len(request_raw),
            },
            "response": {
                "content_type": "application/json",
                "headers": pin_headers,
                "projection": projection,
                "projection_excluded_raw_fields": [],
                "projection_sha256": sha256_bytes(projection_raw),
                "projection_size_bytes": len(projection_raw),
                "raw_body_base64": base64.b64encode(raw).decode("ascii"),
                "raw_body_sha256": sha256_bytes(raw),
                "raw_body_size_bytes": len(raw),
                "raw_top_level_fields": ["id", "model", "results", "usage"],
                "status": 200,
            },
        }

    def run(self, manifest: dict[str, Any], manifest_raw: bytes) -> bytes:
        if manifest.get("artifact_class") != "synthetic_fixture":
            fail(
                "content_free_smoke",
                "live_smoke_not_implemented",
                "client accepts only reviewed synthetic fixtures on loopback",
            )
        if manifest_raw != canonical_bytes(manifest, stage="deployment_manifest_bytes"):
            fail(
                "deployment_manifest_bytes",
                "manifest_bytes_mismatch",
                "manifest object does not match canonical bytes",
            )
        self.bundle.validate_manifest(manifest, allow_synthetic=True)
        if self.actual_order:
            fail("content_free_smoke", "client_single_use", "smoke client is single-use")
        manifest_sha = sha256_bytes(manifest_raw)
        started_at = _utc_micro(self.clock())
        flights: dict[str, dict[str, Any]] = {"preflight": {}, "postflight": {}}
        rerank: dict[str, Any] | None = None
        for step in OBSERVATION_ORDER:
            if step == "rerank":
                rerank = self._rerank(manifest, manifest_sha)
                continue
            try:
                kind, phase = step.rsplit("_", 1)
                flight_name = {"pre": "preflight", "post": "postflight"}[phase]
                path = manifest["routes"][f"{kind}_path"]
            except (KeyError, ValueError):
                fail("content_free_smoke", "observation_order_invalid", "observation order is invalid")
            flights[flight_name][kind] = self._observation(
                step, kind, path, manifest, manifest_sha
            )
        if self.actual_order != OBSERVATION_ORDER or rerank is None:
            fail("content_free_smoke", "observation_order_mismatch", "transport order differs")
        finished_at = _utc_micro(self.clock())
        schema = self.bundle.receipt_schema
        receipt = {
            "$schema": schema["properties"]["$schema"]["const"],
            "artifact": schema["properties"]["artifact"]["const"],
            "artifact_class": "synthetic_fixture",
            "attempt": _const_object(schema["properties"]["attempt"]),
            "canonicalization": copy.deepcopy(manifest["canonicalization"]),
            "finished_at": finished_at,
            "format_version": schema["properties"]["format_version"]["const"],
            "manifest_binding": {
                "artifact_class": "synthetic_fixture",
                "sha256": manifest_sha,
                "size_bytes": len(manifest_raw),
            },
            "observation_order": copy.deepcopy(OBSERVATION_ORDER),
            "postflight": flights["postflight"],
            "preflight": flights["preflight"],
            "rerank": rerank,
            "result": _const_object(schema["properties"]["result"]),
            "scope": _const_object(schema["properties"]["scope"]),
            "started_at": started_at,
        }
        return produce_receipt(self.bundle, manifest, receipt, allow_synthetic=True)


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
        bundle = V2ContractBundle.load(args.contract_root)
        manifest, manifest_raw = pinned_canonical_artifact(
            bundle,
            args.synthetic_manifest,
            expected_sha256=args.expected_manifest_sha256,
            expected_size=args.expected_manifest_size,
            target="deployment_manifest",
            allow_synthetic=True,
        )
        client = SyntheticNativeRerankClient(
            bundle,
            origin=args.loopback_origin,
            timeout=args.timeout,
        )
        receipt_raw = client.run(manifest, manifest_raw)
        _write_new(args.output, receipt_raw)
        print(
            "PASS synthetic native rerank loopback smoke: "
            f"sha256={sha256_bytes(receipt_raw)} size={len(receipt_raw)}"
        )
    except EvidenceFailure as error:
        print(f"FAIL [{error.stage}/{error.code}] {error.message}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
