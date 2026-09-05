#!/usr/bin/env python3
"""One reviewed synthetic request to an isolated HTTPS vLLM deployment (0030)."""
from __future__ import annotations

import argparse
from decimal import Decimal, InvalidOperation
import hashlib
import json
import os
from pathlib import Path
import re
import ssl
import stat
import urllib.request

from scripts.e2_reranker_evidence_v3 import (
    V3ContractBundle, DEFAULT_CONTRACT_ROOT, EvidenceFailure, canonical_bytes,
    fail, pinned_canonical_artifact, strict_canonical_bytes,
)
from scripts.e2_reranker_fake_smoke_v3 import SyntheticNativeRerankClient, _NoRedirect, _loopback_origin

LABELS = {"engine": "0", "model_name": "sparkclaw-reranker"}
COUNTERS = {"vllm:prefix_cache_queries_total": "prefix_cache_queries_total",
            "vllm:prefix_cache_hits_total": "prefix_cache_hits_total"}


def native_counters(raw: bytes) -> dict[str, int]:
    """Parse the reviewed TP=1 metric series, without implicit sums or zeros."""
    try:
        text = raw.decode("utf-8", errors="strict")
    except UnicodeDecodeError:
        fail("content_free_smoke", "metrics_utf8_invalid", "")
    found = {}
    for line in text.splitlines():
        if not line or line.startswith("#"):
            continue
        name = re.split(r"[\s{]", line, maxsplit=1)[0]
        if name not in COUNTERS:
            continue
        match = re.fullmatch(r"[^\s{]+\{([^{}]*)\}[ \t]+([^ \t]+)", line)
        if not match or name in found:
            fail("content_free_smoke", "native_metric_series_invalid", "")
        labels = {}
        for label in match[1].split(","):
            item = re.fullmatch(r'([a-zA-Z_][a-zA-Z0-9_]*)="([^"\\]*)"', label)
            if not item or item[1] in labels:
                fail("content_free_smoke", "native_metric_labels_invalid", "")
            labels[item[1]] = item[2]
        if labels != LABELS:
            fail("content_free_smoke", "native_metric_labels_invalid", "")
        number = match[2]
        if len(number) > 128 or not re.fullmatch(r"[+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?", number):
            fail("content_free_smoke", "native_metric_counter_invalid", "")
        try:
            value = Decimal(number)
            if not value.is_finite() or value < 0 or value > 2**53-1 or value != value.to_integral_value():
                raise ValueError()
            found[name] = int(value)
        except (InvalidOperation, ValueError):
            fail("content_free_smoke", "native_metric_counter_invalid", "")
    if set(found) != set(COUNTERS) or found["vllm:prefix_cache_hits_total"] > found["vllm:prefix_cache_queries_total"]:
        fail("content_free_smoke", "native_metric_series_invalid", "")
    return {COUNTERS[name]: value for name, value in found.items()}


def durable_new(path: Path, raw: bytes) -> None:
    """An attempt is consumed before network I/O and survives process failure."""
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "wb") as output:
        output.write(raw)
        output.flush()
        os.fsync(output.fileno())
    directory = os.open(path.parent, os.O_DIRECTORY | os.O_RDONLY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def read_config(path: Path, manifest: dict) -> dict:
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or not 0 < info.st_size <= 65536:
            fail("deployment_configuration", "file_invalid", "")
        raw = source.read(65537)
    cache = manifest["serving"]["prefix_cache"]
    if len(raw) != cache["resolved_config_size_bytes"] or hashlib.sha256(raw).hexdigest() != cache["resolved_config_sha256"]:
        fail("deployment_configuration", "pin_mismatch", "")
    config = strict_canonical_bytes(raw, stage="deployment_configuration")
    for key in ("enabled", "block_size_tokens", "cache_dtype", "hash_algorithm"):
        if config.get(key) != cache[key]:
            fail("deployment_configuration", "resolved_config_mismatch", "")
    return cache


class LiveNativeRerankClient(SyntheticNativeRerankClient):
    def __init__(self, bundle, *, manifest, manifest_raw, expected_sha256, expected_size,
                 ca_file: Path, config_file: Path, evidence_dir: Path, ledger: Path, timeout=300.0):
        # These external values come from the persisted manifest review, not the file itself.
        if len(manifest_raw) != expected_size or hashlib.sha256(manifest_raw).hexdigest() != expected_sha256:
            fail("deployment_manifest_bytes", "external_manifest_pin_mismatch", "")
        if manifest_raw != canonical_bytes(manifest, stage="deployment_manifest_bytes"):
            fail("deployment_manifest_bytes", "manifest_bytes_mismatch", "")
        bundle.validate_manifest(manifest, allow_synthetic=False)
        origin = _loopback_origin(manifest["routes"]["origin"])
        if not origin.startswith("https://"):
            fail("content_free_smoke", "https_required", "")
        self.cache = read_config(config_file, manifest)
        context = ssl.create_default_context(cafile=str(ca_file))
        super().__init__(bundle, origin=origin, timeout=timeout)
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), _NoRedirect(),
                                                  urllib.request.HTTPSHandler(context=context))
        self.opener.addheaders = []
        self.manifest = manifest
        self.manifest_raw = manifest_raw
        self.ledger = ledger
        self.evidence_dir = evidence_dir
        evidence_dir.mkdir(mode=0o700)
        if ledger.exists():
            fail("content_free_smoke", "attempt_already_consumed", "")

    def _metrics_projection(self, raw: bytes) -> dict:
        return {**native_counters(raw), "configuration_source": "reviewed_deployment_configuration",
                "metric_labels": dict(LABELS), "prefix_cache_enabled": self.cache["enabled"],
                "prefix_cache_config_sha256": self.cache["resolved_config_sha256"],
                "prefix_cache_config_size_bytes": self.cache["resolved_config_size_bytes"]}

    def _request(self, step, method, path, *, body=None, headers=None):
        if method == "POST":
            durable_new(self.ledger, canonical_bytes({"state": "consumed_before_io", "request_id":
                self.manifest["request_contract"]["headers"][0]["value"], "manifest_sha256":
                hashlib.sha256(self.manifest_raw).hexdigest(), "request_sha256": hashlib.sha256(body).hexdigest()},
                stage="attempt_ledger"))
        raw, response_headers, content_type = super()._request(step, method, path, body=body, headers=headers)
        durable_new(self.evidence_dir/(step+".raw"), raw)
        return raw, response_headers, content_type

    def run(self) -> bytes:
        return self._collect(self.manifest, self.manifest_raw, allow_synthetic=False)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--contract-root", type=Path, default=DEFAULT_CONTRACT_ROOT)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--expected-sha256", required=True)
    parser.add_argument("--expected-size", type=int, required=True)
    parser.add_argument("--ca-file", type=Path, required=True)
    parser.add_argument("--config-file", type=Path, required=True)
    parser.add_argument("--evidence-dir", type=Path, required=True)
    parser.add_argument("--ledger", type=Path, required=True)
    args = parser.parse_args()
    try:
        bundle = V3ContractBundle.load(args.contract_root)
        manifest, raw = pinned_canonical_artifact(bundle, args.manifest, expected_sha256=args.expected_sha256,
            expected_size=args.expected_size, target="deployment_manifest", allow_synthetic=False)
        client = LiveNativeRerankClient(bundle, manifest=manifest, manifest_raw=raw,
            expected_sha256=args.expected_sha256, expected_size=args.expected_size, ca_file=args.ca_file,
            config_file=args.config_file, evidence_dir=args.evidence_dir, ledger=args.ledger)
        receipt = client.run()
        durable_new(args.evidence_dir/"receipt.json", receipt)
        print(json.dumps({"receipt_sha256":hashlib.sha256(receipt).hexdigest(),"receipt_size_bytes":len(receipt),
                          "planned_post_count":1,"retry_count":0,"authority":"unissued"}))
        return 0
    except (EvidenceFailure, OSError) as error:
        print(type(error).__name__, getattr(error,"code","operator_io_failure"))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
