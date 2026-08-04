#!/usr/bin/env python3
"""Readiness probe that warms an OpenAI-compatible chat model once."""

from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


MAX_RESPONSE_BYTES = 1 << 20


class ReadinessError(RuntimeError):
    pass


def request_json(
    url: str,
    *,
    timeout: float,
    payload: dict[str, Any] | None = None,
) -> dict[str, Any]:
    data = None
    headers: dict[str, str] = {}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, headers=headers)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            raw = response.read(MAX_RESPONSE_BYTES + 1)
    except (OSError, urllib.error.URLError) as exc:
        raise ReadinessError(f"request failed: {exc}") from exc
    if len(raw) > MAX_RESPONSE_BYTES:
        raise ReadinessError("response exceeds readiness probe limit")
    try:
        decoded = json.loads(raw)
    except (TypeError, ValueError) as exc:
        raise ReadinessError("response is not valid JSON") from exc
    if not isinstance(decoded, dict):
        raise ReadinessError("response must be a JSON object")
    return decoded


def require_served_model(response: dict[str, Any], model: str) -> None:
    entries = response.get("data")
    if not isinstance(entries, list):
        raise ReadinessError("model listing has no data array")
    served = {
        entry.get("id")
        for entry in entries
        if isinstance(entry, dict) and isinstance(entry.get("id"), str)
    }
    if model not in served:
        raise ReadinessError(f"served model is missing: {model}")


def require_completion(response: dict[str, Any]) -> None:
    choices = response.get("choices")
    if not isinstance(choices, list) or not choices:
        raise ReadinessError("warmup completion has no choices")
    first = choices[0]
    if not isinstance(first, dict):
        raise ReadinessError("warmup completion choice is invalid")
    message = first.get("message")
    if not isinstance(message, dict):
        raise ReadinessError("warmup completion has no message")
    content = message.get("content")
    if not isinstance(content, str) or not content.strip():
        raise ReadinessError("warmup completion content is empty")


def warmup_payload(model: str) -> dict[str, Any]:
    return {
        "model": model,
        "messages": [
            {"role": "user", "content": "SparkClaw readiness probe"},
        ],
        "temperature": 0,
        "max_tokens": 8,
        "chat_template_kwargs": {"enable_thinking": False},
    }


def write_marker(marker: Path, model: str) -> None:
    temporary = marker.with_name(marker.name + ".tmp")
    temporary.write_text(model + "\n", encoding="utf-8")
    temporary.replace(marker)


def check_readiness(
    *,
    base_url: str,
    model: str,
    marker: Path,
    health_timeout: float,
    warmup_timeout: float,
) -> None:
    base_url = base_url.rstrip("/")
    try:
        warmed_model = marker.read_text(encoding="utf-8").strip()
    except FileNotFoundError:
        warmed_model = ""
    if warmed_model == model:
        response = request_json(base_url + "/models", timeout=health_timeout)
        require_served_model(response, model)
        return

    response = request_json(
        base_url + "/chat/completions",
        timeout=warmup_timeout,
        payload=warmup_payload(model),
    )
    require_completion(response)
    write_marker(marker, model)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--marker", type=Path, required=True)
    parser.add_argument("--health-timeout", type=float, default=3)
    parser.add_argument("--warmup-timeout", type=float, default=110)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        check_readiness(
            base_url=args.base_url,
            model=args.model,
            marker=args.marker,
            health_timeout=args.health_timeout,
            warmup_timeout=args.warmup_timeout,
        )
    except (OSError, ReadinessError) as exc:
        print(f"model readiness failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
