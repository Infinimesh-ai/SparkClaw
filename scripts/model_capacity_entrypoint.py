#!/usr/bin/env python3
"""Start vLLM with the selected SparkClaw catalog capacity."""

from __future__ import annotations

import json
import os
import pathlib
import sys
from typing import Any


DEFAULT_CATALOG = "/opt/sparkclaw/model.profiles.json"
VLLM_MODULE = "vllm.entrypoints.openai.api_server"


def _required_object(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise ValueError(f"{label} must be an object")
    return value


def resolve_context_tokens(catalog_path: str, profile_id: str, lane: str) -> int:
    if not profile_id.strip():
        raise ValueError("SPARKCLAW_MODEL_CAPACITY_PROFILE is required")
    if not lane.strip():
        raise ValueError("SPARKCLAW_MODEL_CAPACITY_LANE is required")
    try:
        catalog = json.loads(pathlib.Path(catalog_path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"load model capacity catalog: {exc}") from exc
    profiles = _required_object(_required_object(catalog, "capacity catalog").get("profiles"), "capacity profiles")
    profile = _required_object(profiles.get(profile_id), f"capacity profile {profile_id!r}")
    if profile.get("executable") is not True:
        raise ValueError(f"capacity profile {profile_id!r} is not executable")
    if profile.get("mock") is True:
        raise ValueError(f"capacity profile {profile_id!r} is a mock profile and cannot launch a model server")
    lanes = _required_object(profile.get("lanes"), f"capacity profile {profile_id!r} lanes")
    lane_config = _required_object(lanes.get(lane), f"capacity lane {lane!r}")
    physical_id = lane_config.get("physical_model")
    if not isinstance(physical_id, str) or not physical_id.strip():
        raise ValueError(f"capacity lane {lane!r} has no physical model")
    physical_models = _required_object(profile.get("physical_models"), f"capacity profile {profile_id!r} physical models")
    physical = _required_object(physical_models.get(physical_id), f"physical model {physical_id!r}")
    context_tokens = physical.get("context_tokens")
    if isinstance(context_tokens, bool) or not isinstance(context_tokens, int) or context_tokens <= 0:
        raise ValueError(f"physical model {physical_id!r} context_tokens must be a positive integer")
    return context_tokens


def vllm_command(arguments: list[str], context_tokens: int) -> list[str]:
    if "--max-model-len" in arguments:
        raise ValueError("--max-model-len must come from the SparkClaw capacity catalog")
    return ["python3", "-m", VLLM_MODULE, *arguments, "--max-model-len", str(context_tokens)]


def main() -> int:
    if len(sys.argv) == 5 and sys.argv[1] == "--resolve-context" and sys.argv[3] == "--lane":
        try:
            print(resolve_context_tokens(sys.argv[2], os.environ.get("SPARKCLAW_MODEL_CAPACITY_PROFILE", ""), sys.argv[4]))
        except ValueError as exc:
            print(f"model capacity configuration error: {exc}", file=sys.stderr)
            return 2
        return 0
    try:
        context_tokens = resolve_context_tokens(
            os.environ.get("SPARKCLAW_MODEL_CAPACITY_CATALOG", DEFAULT_CATALOG),
            os.environ.get("SPARKCLAW_MODEL_CAPACITY_PROFILE", ""),
            os.environ.get("SPARKCLAW_MODEL_CAPACITY_LANE", ""),
        )
        command = vllm_command(sys.argv[1:], context_tokens)
    except ValueError as exc:
        print(f"model capacity configuration error: {exc}", file=sys.stderr)
        return 2
    os.execvp(command[0], command)
    return 127


if __name__ == "__main__":
    raise SystemExit(main())
