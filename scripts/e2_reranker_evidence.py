#!/usr/bin/env python3
"""Offline provider tooling for the SparkClaw -> IMMS E2 evidence contract."""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import math
import os
import stat
import struct
import sys
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any, NoReturn

from jsonschema import Draft202012Validator, FormatChecker


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_CONTRACT_ROOT = (
    ROOT.parents[1]
    / "InfiniCenter"
    / "clusters"
    / "ProjectGroup-2"
    / "contracts"
    / "SparkClaw--IMMS"
    / "e2-reranker-evidence"
)
ACCEPTED_MACHINE_COMMIT = "717970a95997be18a073060cba6422d906a7dece"
CONFORMANCE_MANIFEST_SHA256 = (
    "ea245a63c8ba343ee73899df5f8006bdf407108daaa0317f17310bb007dc2f0a"
)
CONFORMANCE_MANIFEST_SIZE = 7840
MAX_ARTIFACT_BYTES = 64 << 20


class EvidenceFailure(RuntimeError):
    """Content-free fail-closed result."""

    def __init__(self, stage: str, code: str, message: str):
        super().__init__(message)
        self.stage = stage
        self.code = code
        self.message = message


def fail(stage: str, code: str, message: str) -> NoReturn:
    raise EvidenceFailure(stage, code, message)


def sha256_bytes(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def read_bytes(path: Path, *, stage: str, limit: int = MAX_ARTIFACT_BYTES) -> bytes:
    try:
        size = path.stat().st_size
        if size > limit:
            fail(stage, "file_too_large", "input exceeds the offline verifier limit")
        raw = path.read_bytes()
    except EvidenceFailure:
        raise
    except OSError as error:
        fail(stage, "file_read_failed", f"input could not be read: {error}")
    if len(raw) != size:
        fail(stage, "file_changed_during_read", "input size changed during read")
    return raw


def _unique_object(stage: str):
    def hook(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
        value: dict[str, Any] = {}
        for key, child in pairs:
            if key in value:
                fail(stage, "duplicate_json_key", "duplicate JSON key")
            value[key] = child
        return value

    return hook


def _reject_constant(stage: str):
    def reject(_value: str) -> NoReturn:
        fail(stage, "non_finite_json_number", "non-finite JSON number")

    return reject


def strict_json_bytes(raw: bytes, *, stage: str) -> Any:
    try:
        text = raw.decode("utf-8", errors="strict")
    except UnicodeDecodeError:
        fail(stage, "invalid_utf8", "input is not strict UTF-8")
    try:
        return json.loads(
            text,
            object_pairs_hook=_unique_object(stage),
            parse_constant=_reject_constant(stage),
        )
    except EvidenceFailure:
        raise
    except json.JSONDecodeError:
        fail(stage, "invalid_json", "input is not valid JSON")


def assert_ascii_keys(value: Any, *, stage: str) -> None:
    if isinstance(value, dict):
        for key, child in value.items():
            if not isinstance(key, str) or not key.isascii():
                fail(stage, "non_ascii_json_key", "JSON object key is not ASCII")
            assert_ascii_keys(child, stage=stage)
    elif isinstance(value, list):
        for child in value:
            assert_ascii_keys(child, stage=stage)
    elif isinstance(value, float):
        fail(stage, "floating_json_number_forbidden", "artifact JSON uses a number")


def canonical_bytes(value: Any, *, stage: str) -> bytes:
    assert_ascii_keys(value, stage=stage)
    try:
        encoded = json.dumps(
            value,
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
            allow_nan=False,
        )
    except (TypeError, ValueError):
        fail(stage, "canonical_encoding_failed", "artifact cannot be canonically encoded")
    return (encoded + "\n").encode("utf-8")


def strict_canonical_bytes(raw: bytes, *, stage: str) -> Any:
    value = strict_json_bytes(raw, stage=stage)
    if raw != canonical_bytes(value, stage=stage):
        fail(
            stage,
            "noncanonical_json_bytes",
            "expected compact recursively sorted JSON and one terminal LF",
        )
    return value


def validate_schema(instance: Any, schema: dict[str, Any], *, stage: str, code: str) -> None:
    errors = sorted(
        Draft202012Validator(schema, format_checker=FormatChecker()).iter_errors(instance),
        key=lambda item: [str(part) for part in item.absolute_path],
    )
    if errors:
        fail(stage, code, "artifact does not satisfy the accepted central schema")


def _safe_path(
    base: Path,
    relative: str,
    *,
    stage: str,
    boundary: Path | None = None,
) -> Path:
    candidate = Path(relative)
    if candidate.is_absolute():
        fail(stage, "pinned_path_escape", "pinned path must be relative")
    resolved = (base / candidate).resolve()
    try:
        resolved.relative_to((boundary or base).resolve())
    except ValueError:
        fail(stage, "pinned_path_escape", "pinned path escapes the contract root")
    return resolved


def _schema_const(schema: dict[str, Any], *parts: str) -> Any:
    current: Any = schema
    for part in parts:
        current = current[part]
    return copy.deepcopy(current["const"])


def _schema_value(schema: dict[str, Any], *parts: str) -> Any:
    current: Any = schema
    for part in parts:
        current = current[part]
    return copy.deepcopy(current)


@dataclass
class ContractBundle:
    root: Path
    conformance_dir: Path
    closure: dict[str, Any]
    manifest_schema: dict[str, Any]
    fixture_schema: dict[str, Any]
    deployment_schema: dict[str, Any]
    smoke_schema: dict[str, Any]
    cases: tuple[dict[str, Any], ...]

    @classmethod
    def load(
        cls,
        root: Path = DEFAULT_CONTRACT_ROOT,
        *,
        expected_manifest_sha256: str = CONFORMANCE_MANIFEST_SHA256,
        expected_manifest_size: int = CONFORMANCE_MANIFEST_SIZE,
    ) -> "ContractBundle":
        root = root.resolve()
        conformance_dir = root / "conformance" / "v1"
        manifest_path = conformance_dir / "manifest.json"
        raw = read_bytes(manifest_path, stage="conformance_manifest")
        if sha256_bytes(raw) != expected_manifest_sha256 or len(raw) != expected_manifest_size:
            fail(
                "conformance_manifest",
                "external_conformance_pin_mismatch",
                "central conformance manifest differs from accepted decision 0027 pin",
            )
        closure = strict_json_bytes(raw, stage="conformance_manifest")
        if not isinstance(closure, dict):
            fail("conformance_manifest", "invalid_shape", "closure must be an object")

        pinned_paths: dict[str, Path] = {}
        for key in closure:
            if not key.endswith("_sha256"):
                continue
            field = key[: -len("_sha256")]
            size_field = f"{field}_size_bytes"
            if field not in closure or size_field not in closure:
                fail("conformance_manifest", "pin_tuple_incomplete", "central pin tuple is incomplete")
            relative = closure[field]
            if not isinstance(relative, str):
                fail("conformance_manifest", "pinned_path_invalid", "central pinned path is invalid")
            path = _safe_path(
                conformance_dir,
                relative,
                stage="conformance_manifest",
                boundary=root,
            )
            pinned_raw = read_bytes(path, stage="conformance_manifest")
            if sha256_bytes(pinned_raw) != closure[key]:
                fail("conformance_manifest", "pinned_sha256_mismatch", f"{field} SHA-256 differs")
            if len(pinned_raw) != closure[size_field]:
                fail("conformance_manifest", "pinned_size_mismatch", f"{field} size differs")
            pinned_paths[field] = path

        required_schema_fields = {
            "manifest_schema",
            "fixture_schema",
            "deployment_manifest_schema",
            "smoke_receipt_schema",
        }
        if not required_schema_fields.issubset(pinned_paths):
            fail("conformance_manifest", "schema_pin_missing", "central schema pin is missing")
        schemas = {
            field: strict_json_bytes(
                read_bytes(pinned_paths[field], stage="conformance_manifest"),
                stage="conformance_manifest",
            )
            for field in required_schema_fields
        }
        for schema in schemas.values():
            try:
                Draft202012Validator.check_schema(schema)
            except Exception:
                fail("conformance_manifest", "schema_definition_invalid", "central schema is invalid")
        validate_schema(
            closure,
            schemas["manifest_schema"],
            stage="conformance_manifest",
            code="conformance_manifest_schema_invalid",
        )

        cases = closure["cases"]
        listed = {case["file"] for case in cases}
        actual = {
            path.relative_to(conformance_dir).as_posix()
            for path in (conformance_dir / "fixtures").glob("*.json")
        }
        if listed != actual or closure["fixture_count"] != len(cases):
            fail("conformance_manifest", "fixture_inventory_mismatch", "fixture inventory differs")
        names = [case["name"] for case in cases]
        if len(names) != len(set(names)):
            fail("conformance_manifest", "fixture_name_duplicate", "fixture names are duplicated")

        accepted = negative = raw_count = assertion_references = 0
        assertions: set[str] = set()
        for case in cases:
            fixture_path = _safe_path(conformance_dir, case["file"], stage="conformance_manifest")
            if fixture_path.parent != (conformance_dir / "fixtures").resolve():
                fail("conformance_manifest", "fixture_path_escape", "fixture path escapes inventory")
            fixture_raw = read_bytes(fixture_path, stage="conformance_manifest")
            if sha256_bytes(fixture_raw) != case["sha256"] or len(fixture_raw) != case["size_bytes"]:
                fail("conformance_manifest", "fixture_pin_mismatch", "fixture SHA-256/size differs")
            fixture = strict_json_bytes(fixture_raw, stage="conformance_fixture")
            validate_schema(
                fixture,
                schemas["fixture_schema"],
                stage="conformance_fixture",
                code="fixture_schema_invalid",
            )
            expected = fixture["expect"]
            if case["name"] != fixture["name"] or any(
                case[field] != expected[field]
                for field in ("outcome", "stage", "failure_code")
            ):
                fail("conformance_manifest", "fixture_metadata_mismatch", "fixture metadata differs")
            accepted += case["outcome"] == "accept_synthetic_fixture"
            negative += case["outcome"] == "reject"
            raw_count += case["stage"].endswith("_bytes")
            assertion_references += len(expected["assertions"])
            assertions.update(expected["assertions"])
        derived_counts = {
            "accepted_fixture_count": accepted,
            "negative_fixture_count": negative,
            "raw_fixture_count": raw_count,
            "assertion_reference_count": assertion_references,
            "unique_assertion_count": len(assertions),
        }
        if any(closure[field] != value for field, value in derived_counts.items()):
            fail("conformance_manifest", "derived_count_mismatch", "central derived count differs")

        bundle = cls(
            root=root,
            conformance_dir=conformance_dir,
            closure=closure,
            manifest_schema=schemas["manifest_schema"],
            fixture_schema=schemas["fixture_schema"],
            deployment_schema=schemas["deployment_manifest_schema"],
            smoke_schema=schemas["smoke_receipt_schema"],
            cases=tuple(copy.deepcopy(cases)),
        )
        bundle.validate_all_cases()
        return bundle

    def _fixture(self, relative: str) -> dict[str, Any]:
        path = _safe_path(self.conformance_dir / "fixtures", relative, stage="conformance_fixture")
        if path.parent != (self.conformance_dir / "fixtures").resolve():
            fail("conformance_fixture", "fixture_path_escape", "fixture path escapes inventory")
        value = strict_json_bytes(read_bytes(path, stage="conformance_fixture"), stage="conformance_fixture")
        if not isinstance(value, dict):
            fail("conformance_fixture", "invalid_shape", "fixture must be an object")
        return value

    def validate_all_cases(self) -> None:
        for case in self.cases:
            fixture = self._fixture(Path(case["file"]).name)
            if "raw_artifact" in fixture:
                raw_artifact = fixture["raw_artifact"]
                stage = f"{raw_artifact['target']}_bytes"
                try:
                    strict_canonical_bytes(raw_artifact["utf8"].encode("utf-8"), stage=stage)
                except EvidenceFailure as error:
                    actual = ("reject", error.stage, error.code)
                else:
                    actual = ("reject", stage, "raw_fixture_unexpectedly_canonical")
            else:
                if "artifacts" in fixture:
                    artifacts = copy.deepcopy(fixture["artifacts"])
                else:
                    base = self._fixture(fixture["base_fixture"])
                    if "artifacts" not in base:
                        fail("conformance_fixture", "base_fixture_not_materialized", "base fixture is invalid")
                    artifacts = copy.deepcopy(base["artifacts"])
                    for mutation in fixture["mutations"]:
                        _apply_mutation(artifacts, mutation)
                actual = self.evaluate_pair(artifacts, allow_synthetic=True)
            expected = (
                fixture["expect"]["outcome"],
                fixture["expect"]["stage"],
                fixture["expect"]["failure_code"],
            )
            if actual != expected:
                fail("conformance_fixture", "fixture_expectation_failed", f"provider adapter failed {case['name']}")

    def evaluate_pair(
        self, artifacts: dict[str, Any], *, allow_synthetic: bool
    ) -> tuple[str, str, str]:
        try:
            manifest = artifacts["deployment_manifest"]
            receipt = artifacts["smoke_receipt"]
            self.validate_manifest(manifest, allow_synthetic=allow_synthetic)
            validate_schema(
                receipt,
                self.smoke_schema,
                stage="smoke_receipt_schema",
                code="smoke_receipt_schema_invalid",
            )
            self.validate_receipt(manifest, receipt, allow_synthetic=allow_synthetic)
        except (KeyError, TypeError):
            return "reject", "deployment_manifest_schema", "deployment_manifest_schema_invalid"
        except EvidenceFailure as error:
            return "reject", error.stage, error.code
        outcome = "accept_synthetic_fixture" if allow_synthetic else "accept_live_evidence"
        return outcome, "accepted", "none"

    def validate_manifest(self, manifest: dict[str, Any], *, allow_synthetic: bool) -> None:
        validate_schema(
            manifest,
            self.deployment_schema,
            stage="deployment_manifest_schema",
            code="deployment_manifest_schema_invalid",
        )
        stage = "deployment_manifest_semantics"
        assert_ascii_keys(manifest, stage=stage)
        artifact_class = manifest["artifact_class"]
        if artifact_class == "synthetic_fixture":
            if not allow_synthetic:
                fail(stage, "synthetic_artifact_not_live", "synthetic fixture is never live")
            if ".invalid" not in manifest["routes"]["origin"]:
                fail(stage, "synthetic_fixture_origin_not_invalid", "synthetic origin must be .invalid")
        else:
            flattened = json.dumps(manifest, ensure_ascii=False, separators=(",", ":"))
            if ".invalid" in flattened or "synthetic_fixture" in flattened:
                fail(
                    stage,
                    "synthetic_material_in_deployment_candidate",
                    "synthetic material cannot be promoted",
                )

        catalog = manifest["model"]["artifact_catalog"]
        files = catalog["files"]
        paths = [entry["path"] for entry in files]
        if paths != sorted(paths, key=lambda value: value.encode("ascii")) or len(paths) != len(set(paths)):
            fail(stage, "catalog_path_order_invalid", "catalog paths are not unique ASCII order")
        components = set(
            _schema_value(
                self.deployment_schema,
                "$defs",
                "catalog_file",
                "properties",
                "component",
                "enum",
            )
        )
        if {entry["component"] for entry in files} != components:
            fail(stage, "catalog_component_closure_missing", "catalog component closure is incomplete")
        if catalog["file_count"] != len(files):
            fail(stage, "catalog_file_count_mismatch", "catalog file count differs")
        if catalog["total_size_bytes"] != sum(entry["size_bytes"] for entry in files):
            fail(stage, "catalog_total_size_mismatch", "catalog total size differs")
        if catalog["files_sha256"] != sha256_bytes(canonical_bytes(files, stage=stage)):
            fail(stage, "catalog_files_sha256_mismatch", "catalog digest differs")

        runtime = manifest["runtime"]
        if not runtime["container_image_reference"].endswith("@" + runtime["container_image_digest"]):
            fail(stage, "container_image_digest_mismatch", "image reference and digest differ")
        binding = manifest["model"]["model_binding"]
        prompt = binding["prefix_token_ids"] + binding["suffix_token_ids"]
        if binding["smoke_prompt_token_count"] != len(prompt):
            fail(stage, "smoke_prompt_binding_mismatch", "smoke prompt count differs")
        if _token_ids_digest(prompt) != binding["smoke_prompt_token_ids_sha256"]:
            fail(stage, "smoke_prompt_digest_mismatch", "smoke prompt digest differs")
        dimensions = _schema_value(
            self.deployment_schema,
            "$defs",
            "e2_e3_difference",
            "properties",
            "dimension",
            "enum",
        )
        if [item["dimension"] for item in manifest["e2_e3_differences"]] != dimensions:
            fail(stage, "e2_e3_dimensions_mismatch", "E2/E3 dimensions differ")
        if any(
            item["e2_value"] == "unknown_pending_e3_measurement"
            for item in manifest["e2_e3_differences"]
        ):
            fail(stage, "e2_value_unknown", "actual E2 value is unknown")
        _validate_startup_argv(manifest)

    def validate_receipt(
        self,
        manifest: dict[str, Any],
        receipt: dict[str, Any],
        *,
        allow_synthetic: bool,
    ) -> None:
        stage = "smoke_receipt_semantics"
        manifest_raw = canonical_bytes(manifest, stage=stage)
        manifest_sha = sha256_bytes(manifest_raw)
        binding = receipt["manifest_binding"]
        if binding["sha256"] != manifest_sha or binding["size_bytes"] != len(manifest_raw):
            fail(stage, "manifest_binding_mismatch", "receipt manifest binding differs")
        expected_receipt_class = (
            "synthetic_fixture"
            if manifest["artifact_class"] == "synthetic_fixture"
            else "live_evidence"
        )
        if receipt["artifact_class"] != expected_receipt_class or binding["artifact_class"] != manifest["artifact_class"]:
            fail(stage, "artifact_class_mismatch", "manifest/receipt classes differ")
        if receipt["artifact_class"] == "synthetic_fixture" and not allow_synthetic:
            fail(stage, "synthetic_artifact_not_live", "synthetic receipt is never live")

        order = _schema_const(self.smoke_schema, "properties", "observation_order")
        if receipt["observation_order"] != order:
            fail(stage, "observation_order_mismatch", "observation order differs")
        started = _parse_utc_micro(receipt["started_at"])
        finished = _parse_utc_micro(receipt["finished_at"])
        if finished < started:
            fail(stage, "timestamp_order_invalid", "receipt timestamps are reversed")

        header_names = manifest["live_identity_headers"]
        expected_headers = {
            header_names["manifest_sha256"]: manifest_sha,
            header_names["deployment_revision"]: manifest["runtime"]["deployment_revision"],
        }
        observations = _receipt_observations(receipt)
        for observation in observations:
            if observation["headers"] != expected_headers:
                if observation["headers"].get(header_names["manifest_sha256"]) != manifest_sha:
                    fail(stage, "live_manifest_header_mismatch", "live manifest header differs")
                fail(stage, "live_deployment_header_mismatch", "live deployment header differs")
            empty_size = observation["raw_body_size_bytes"] == 0
            empty_hash = observation["raw_body_sha256"] == sha256_bytes(b"")
            if empty_size != empty_hash:
                fail(stage, "raw_body_empty_commitment_mismatch", "empty body commitment differs")

        for flight_name in ("preflight", "postflight"):
            flight = receipt[flight_name]
            if flight["models"]["projection"] != manifest["expected_models_projection"]:
                fail(stage, "models_projection_mismatch", "models projection differs")
            if flight["version"]["projection"]["vllm_reported_version"] != manifest["runtime"]["vllm_reported_version"]:
                fail(stage, "vllm_version_mismatch", "vLLM version differs")
            if flight["health"]["projection"] != {"healthy": True}:
                fail(stage, "health_projection_mismatch", "health projection differs")
            if flight["metrics"]["projection"] != {"prefix_caching_enabled": False}:
                fail(stage, "prefix_cache_observation_mismatch", "prefix cache is not disabled")

        request = receipt["completion"]["request"]
        model_binding = manifest["model"]["model_binding"]
        expected_body = {
            "model": manifest["model"]["served_name"],
            "prompt": model_binding["prefix_token_ids"] + model_binding["suffix_token_ids"],
            **manifest["request_contract"]["parameters"],
        }
        if request["body"] != expected_body:
            fail(stage, "request_body_mismatch", "completion request differs")
        request_raw = canonical_bytes(request["body"], stage=stage)
        if request["raw_body_sha256"] != sha256_bytes(request_raw) or request["raw_body_size_bytes"] != len(request_raw):
            fail(stage, "request_body_commitment_mismatch", "request commitment differs")

        response = receipt["completion"]["response"]
        projection = response["projection"]
        projection_raw = canonical_bytes(projection, stage=stage)
        if response["projection_sha256"] != sha256_bytes(projection_raw) or response["projection_size_bytes"] != len(projection_raw):
            fail(stage, "response_projection_commitment_mismatch", "response projection commitment differs")
        if projection["prompt_token_ids"] != expected_body["prompt"]:
            fail(stage, "response_prompt_echo_mismatch", "prompt token echo differs")
        labels = manifest["response_contract"]["explicit_label_logprobs"]
        generated = projection["generated_token_ids"]
        if len(generated) != 1 or generated[0] not in labels:
            fail(stage, "generated_token_invalid", "generated token differs")
        if [entry["token_id"] for entry in projection["label_logprobs"]] != labels:
            fail(stage, "label_logprob_ids_mismatch", "explicit label logprobs differ")
        for entry in projection["label_logprobs"]:
            try:
                value = float(entry["logprob"])
            except (TypeError, ValueError):
                fail(stage, "label_logprob_invalid", "label logprob is invalid")
            if not math.isfinite(value) or value > 0:
                fail(stage, "label_logprob_invalid", "label logprob is outside the accepted domain")
            if math.exp(value) == 0.0:
                fail(stage, "label_logprob_underflow", "label logprob exp underflows")
            if struct.pack(">d", value).hex() != entry["float64_bits"]:
                fail(stage, "label_logprob_float64_bits_mismatch", "label logprob bits differ")


def _pointer_parts(pointer: str) -> list[str]:
    return [part.replace("~1", "/").replace("~0", "~") for part in pointer[1:].split("/")]


def _apply_mutation(artifacts: dict[str, Any], mutation: dict[str, Any]) -> None:
    parts = _pointer_parts(mutation["path"])
    current: Any = artifacts
    try:
        for part in parts[:-1]:
            current = current[int(part)] if isinstance(current, list) else current[part]
        leaf = parts[-1]
        operation = mutation["op"]
        if isinstance(current, list):
            index = int(leaf)
            if operation == "remove":
                current.pop(index)
            elif operation == "replace":
                current[index] = copy.deepcopy(mutation["value"])
            else:
                current.insert(index, copy.deepcopy(mutation["value"]))
        elif operation == "remove":
            del current[leaf]
        elif operation == "replace":
            if leaf not in current:
                raise KeyError(leaf)
            current[leaf] = copy.deepcopy(mutation["value"])
        else:
            current[leaf] = copy.deepcopy(mutation["value"])
    except (KeyError, IndexError, TypeError, ValueError):
        fail("conformance_fixture", "mutation_path_invalid", "fixture mutation path is invalid")


def _token_ids_digest(token_ids: list[int]) -> str:
    payload = struct.pack(">I", len(token_ids)) + b"".join(
        struct.pack(">I", token_id) for token_id in token_ids
    )
    digest = hashlib.sha256()
    for piece in (b"imms-set-admission-token-ids-v1", payload):
        digest.update(struct.pack(">Q", len(piece)))
        digest.update(piece)
    return digest.hexdigest()


def _parse_startup_argv(argv: list[str]) -> tuple[dict[str, str | bool], list[str]]:
    flags: dict[str, str | bool] = {}
    positional: list[str] = []
    index = 0
    while index < len(argv):
        token = argv[index]
        if not token.startswith("--"):
            positional.append(token)
            index += 1
            continue
        if "=" in token:
            name, value = token.split("=", 1)
        elif index + 1 < len(argv) and not argv[index + 1].startswith("--"):
            name, value = token, argv[index + 1]
            index += 1
        else:
            name, value = token, True
        if name in flags:
            fail("deployment_manifest_semantics", "startup_argv_duplicate_flag", "startup flag repeats")
        flags[name] = value
        index += 1
    return flags, positional


def _validate_startup_argv(manifest: dict[str, Any]) -> None:
    stage = "deployment_manifest_semantics"
    serving = manifest["serving"]
    model = manifest["model"]
    flags, positional = _parse_startup_argv(serving["startup_argv"])
    required = {
        "--revision": model["revision"],
        "--served-model-name": model["served_name"],
        "--dtype": serving["dtype"],
        "--tensor-parallel-size": str(serving["tensor_parallel_size"]),
        "--max-model-len": str(serving["max_model_len"]),
        "--max-num-seqs": str(serving["max_num_seqs"]),
        "--seed": str(serving["seed"]),
    }
    if any(flags.get(name) != value for name, value in required.items()):
        fail(stage, "startup_argv_resolved_mismatch", "startup argv differs from manifest")
    if flags.get("--enforce-eager") is not True or flags.get("--disable-log-requests") is not True:
        fail(stage, "startup_argv_resolved_mismatch", "eager/logging startup flags are missing")
    if model["id"] not in positional and flags.get("--model") != model["id"]:
        fail(stage, "startup_argv_model_mismatch", "startup argv model differs")
    if "--quantization" in flags and flags["--quantization"] != "none":
        fail(stage, "startup_argv_resolved_mismatch", "quantization is not disabled")
    forbidden = {
        "--enable-prefix-caching",
        "--enable-lora",
        "--enable-auto-tool-choice",
        "--chat-template",
        "--truncate-prompt-tokens",
    }
    if any(name in forbidden or name.startswith("--speculative") or name.startswith("--lora-") for name in flags):
        fail(stage, "startup_argv_forbidden_feature", "startup argv enables a forbidden feature")


def _parse_utc_micro(value: str) -> datetime:
    try:
        parsed = datetime.strptime(value, "%Y-%m-%dT%H:%M:%S.%fZ")
    except (TypeError, ValueError):
        fail("smoke_receipt_semantics", "timestamp_invalid", "timestamp is invalid")
    if parsed.strftime("%Y-%m-%dT%H:%M:%S.%fZ") != value:
        fail("smoke_receipt_semantics", "timestamp_noncanonical", "timestamp is noncanonical")
    return parsed


def _receipt_observations(receipt: dict[str, Any]) -> list[dict[str, Any]]:
    return [
        receipt["preflight"]["models"],
        receipt["preflight"]["version"],
        receipt["preflight"]["health"],
        receipt["preflight"]["metrics"],
        receipt["completion"]["response"],
        receipt["postflight"]["models"],
        receipt["postflight"]["version"],
        receipt["postflight"]["health"],
        receipt["postflight"]["metrics"],
    ]


def _enumerate_regular_files(root: Path) -> list[tuple[str, Path]]:
    try:
        root_stat = root.lstat()
    except OSError as error:
        fail("catalog", "snapshot_root_invalid", f"snapshot root is unavailable: {error}")
    if stat.S_ISLNK(root_stat.st_mode) or not stat.S_ISDIR(root_stat.st_mode):
        fail("catalog", "snapshot_root_invalid", "snapshot root must be a real directory")
    found: list[tuple[str, Path]] = []

    def visit(directory: Path, prefix: tuple[str, ...]) -> None:
        try:
            entries = list(os.scandir(directory))
        except OSError as error:
            fail("catalog", "snapshot_read_failed", f"snapshot directory is unreadable: {error}")
        entries.sort(key=lambda entry: entry.name.encode("utf-8"))
        for entry in entries:
            if not entry.name.isascii():
                fail("catalog", "catalog_path_non_ascii", "snapshot path is not ASCII")
            parts = (*prefix, entry.name)
            relative = "/".join(parts)
            if entry.is_symlink():
                fail("catalog", "catalog_symlink_forbidden", "snapshot contains a symlink")
            if entry.is_dir(follow_symlinks=False):
                visit(Path(entry.path), parts)
            elif entry.is_file(follow_symlinks=False):
                found.append((relative, Path(entry.path)))
            else:
                fail("catalog", "catalog_non_regular_file", "snapshot contains a non-regular file")

    visit(root, ())
    return sorted(found, key=lambda item: item[0].encode("ascii"))


def _hash_regular_file(path: Path) -> tuple[str, int]:
    flags = os.O_RDONLY | getattr(os, "O_CLOEXEC", 0) | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags)
    except OSError as error:
        fail("catalog", "catalog_file_open_failed", f"snapshot file cannot be opened: {error}")
    try:
        before = os.fstat(descriptor)
        if not stat.S_ISREG(before.st_mode):
            fail("catalog", "catalog_non_regular_file", "snapshot entry is not a regular file")
        digest = hashlib.sha256()
        size = 0
        while True:
            chunk = os.read(descriptor, 1 << 20)
            if not chunk:
                break
            digest.update(chunk)
            size += len(chunk)
        after = os.fstat(descriptor)
    finally:
        os.close(descriptor)
    identity_before = (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns)
    identity_after = (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns)
    if identity_before != identity_after or size != after.st_size:
        fail("catalog", "catalog_file_changed", "snapshot file changed while hashing")
    return digest.hexdigest(), size


def build_artifact_catalog(
    bundle: ContractBundle,
    snapshot_root: Path,
    classifications: dict[str, str],
) -> dict[str, Any]:
    files = _enumerate_regular_files(snapshot_root)
    paths = [relative for relative, _path in files]
    if set(classifications) != set(paths):
        fail("catalog", "catalog_classification_incomplete", "classification must cover the full snapshot")
    allowed_components = set(
        _schema_value(
            bundle.deployment_schema,
            "$defs",
            "catalog_file",
            "properties",
            "component",
            "enum",
        )
    )
    if set(classifications.values()) != allowed_components:
        fail("catalog", "catalog_component_closure_missing", "component closure is incomplete")
    entries: list[dict[str, Any]] = []
    for relative, path in files:
        component = classifications[relative]
        if component not in allowed_components:
            fail("catalog", "catalog_component_invalid", "classification component is invalid")
        digest, size = _hash_regular_file(path)
        entries.append(
            {"component": component, "path": relative, "sha256": digest, "size_bytes": size}
        )
    if [relative for relative, _path in _enumerate_regular_files(snapshot_root)] != paths:
        fail("catalog", "catalog_inventory_changed", "snapshot inventory changed while hashing")
    catalog = {
        "format": "path-sha256-size-v1",
        "closure": "complete_model_and_tokenizer_snapshot",
        "file_count": len(entries),
        "total_size_bytes": sum(entry["size_bytes"] for entry in entries),
        "files_sha256": sha256_bytes(canonical_bytes(entries, stage="catalog")),
        "files": entries,
    }
    fragment = {
        "$schema": bundle.deployment_schema["$schema"],
        "$ref": "#/$defs/artifact_catalog",
        "$defs": bundle.deployment_schema["$defs"],
    }
    validate_schema(catalog, fragment, stage="catalog", code="catalog_schema_invalid")
    return catalog


def pinned_canonical_artifact(
    bundle: ContractBundle,
    path: Path,
    *,
    expected_sha256: str,
    expected_size: int,
    target: str,
    allow_synthetic: bool,
) -> tuple[dict[str, Any], bytes]:
    stage = f"{target}_bytes"
    raw = read_bytes(path, stage=stage)
    if sha256_bytes(raw) != expected_sha256 or len(raw) != expected_size:
        fail(stage, f"external_{target}_pin_mismatch", "artifact differs from reviewed external pin")
    value = strict_canonical_bytes(raw, stage=stage)
    if target == "deployment_manifest":
        bundle.validate_manifest(value, allow_synthetic=allow_synthetic)
    return value, raw


def verify_external_pair(
    bundle: ContractBundle,
    manifest_path: Path,
    receipt_path: Path,
    *,
    manifest_sha256: str,
    manifest_size: int,
    receipt_sha256: str,
    receipt_size: int,
) -> None:
    manifest_raw = read_bytes(manifest_path, stage="deployment_manifest_bytes")
    receipt_raw = read_bytes(receipt_path, stage="smoke_receipt_bytes")
    manifest_pin_ok = sha256_bytes(manifest_raw) == manifest_sha256 and len(manifest_raw) == manifest_size
    receipt_pin_ok = sha256_bytes(receipt_raw) == receipt_sha256 and len(receipt_raw) == receipt_size
    if not manifest_pin_ok:
        fail("deployment_manifest_bytes", "external_manifest_pin_mismatch", "reviewed manifest pin differs")
    if not receipt_pin_ok:
        fail("smoke_receipt_bytes", "external_receipt_pin_mismatch", "reviewed receipt pin differs")
    manifest = strict_canonical_bytes(manifest_raw, stage="deployment_manifest_bytes")
    receipt = strict_canonical_bytes(receipt_raw, stage="smoke_receipt_bytes")
    outcome = bundle.evaluate_pair(
        {"deployment_manifest": manifest, "smoke_receipt": receipt},
        allow_synthetic=False,
    )
    if outcome != ("accept_live_evidence", "accepted", "none"):
        fail(outcome[1], outcome[2], "external pair is not accepted live evidence")


def produce_manifest(
    bundle: ContractBundle,
    draft: dict[str, Any],
    snapshot_root: Path,
    classifications: dict[str, str],
    *,
    allow_synthetic: bool = False,
) -> bytes:
    document = copy.deepcopy(draft)
    try:
        document["model"]["artifact_catalog"] = build_artifact_catalog(
            bundle, snapshot_root, classifications
        )
    except (KeyError, TypeError):
        fail("deployment_manifest_schema", "deployment_manifest_schema_invalid", "draft shape is invalid")
    bundle.validate_manifest(document, allow_synthetic=allow_synthetic)
    return canonical_bytes(document, stage="deployment_manifest_bytes")


def produce_receipt(
    bundle: ContractBundle,
    manifest: dict[str, Any],
    receipt: dict[str, Any],
    *,
    allow_synthetic: bool = False,
) -> bytes:
    outcome = bundle.evaluate_pair(
        {"deployment_manifest": manifest, "smoke_receipt": receipt},
        allow_synthetic=allow_synthetic,
    )
    expected = "accept_synthetic_fixture" if allow_synthetic else "accept_live_evidence"
    if outcome != (expected, "accepted", "none"):
        fail(outcome[1], outcome[2], "receipt is not accepted")
    return canonical_bytes(receipt, stage="smoke_receipt_bytes")


def _load_object(path: Path, *, stage: str) -> dict[str, Any]:
    value = strict_json_bytes(read_bytes(path, stage=stage), stage=stage)
    if not isinstance(value, dict):
        fail(stage, "invalid_shape", "input must be a JSON object")
    return value


def _valid_pin(value: str) -> bool:
    return len(value) == 64 and all(character in "0123456789abcdef" for character in value)


def _write_new(path: Path, raw: bytes) -> None:
    try:
        with path.open("xb") as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
    except FileExistsError:
        fail("output", "output_exists", "refusing to overwrite an existing artifact")
    except OSError as error:
        fail("output", "output_write_failed", f"artifact could not be written: {error}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--contract-root", type=Path, default=DEFAULT_CONTRACT_ROOT)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("check-contract")

    catalog = commands.add_parser("produce-manifest")
    catalog.add_argument("--draft", type=Path, required=True)
    catalog.add_argument("--snapshot-root", type=Path, required=True)
    catalog.add_argument("--classifications", type=Path, required=True)
    catalog.add_argument("--output", type=Path, required=True)

    verify = commands.add_parser("verify-pair")
    verify.add_argument("--deployment-manifest", type=Path, required=True)
    verify.add_argument("--smoke-receipt", type=Path, required=True)
    verify.add_argument("--expected-deployment-manifest-sha256", required=True)
    verify.add_argument("--expected-deployment-manifest-size", type=int, required=True)
    verify.add_argument("--expected-smoke-receipt-sha256", required=True)
    verify.add_argument("--expected-smoke-receipt-size", type=int, required=True)
    args = parser.parse_args()
    if args.command == "verify-pair":
        for value in (
            args.expected_deployment_manifest_sha256,
            args.expected_smoke_receipt_sha256,
        ):
            if not _valid_pin(value):
                parser.error("reviewed SHA-256 pins must be 64 lowercase hex")
        if args.expected_deployment_manifest_size < 1 or args.expected_smoke_receipt_size < 1:
            parser.error("reviewed sizes must be positive")
    return args


def main() -> int:
    args = parse_args()
    try:
        bundle = ContractBundle.load(args.contract_root)
        if args.command == "check-contract":
            print(
                "PASS accepted E2 evidence contract: "
                f"{len(bundle.cases)} dynamically enumerated fixtures"
            )
        elif args.command == "produce-manifest":
            draft = _load_object(args.draft, stage="deployment_manifest_draft")
            classifications = _load_object(args.classifications, stage="catalog_classifications")
            raw = produce_manifest(bundle, draft, args.snapshot_root, classifications)
            _write_new(args.output, raw)
            print(f"PASS wrote deployment candidate: sha256={sha256_bytes(raw)} size={len(raw)}")
        else:
            verify_external_pair(
                bundle,
                args.deployment_manifest,
                args.smoke_receipt,
                manifest_sha256=args.expected_deployment_manifest_sha256,
                manifest_size=args.expected_deployment_manifest_size,
                receipt_sha256=args.expected_smoke_receipt_sha256,
                receipt_size=args.expected_smoke_receipt_size,
            )
            print("PASS external live evidence pair")
    except EvidenceFailure as error:
        print(f"FAIL [{error.stage}/{error.code}] {error.message}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
