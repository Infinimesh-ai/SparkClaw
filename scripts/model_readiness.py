#!/usr/bin/env python3
"""Readiness probe that warms an OpenAI-compatible chat model once."""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


MAX_RESPONSE_BYTES = 1 << 20
DEFAULT_WARMUP_PROMPT = "SparkClaw readiness probe"
WARMUP_PROMPT_UNIT = "SparkClaw synthetic routing context for bounded model warmup. "


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


def warmup_payload(
    model: str,
    *,
    prompt_repetitions: int = 0,
    max_tokens: int = 8,
    min_tokens: int = 0,
) -> dict[str, Any]:
    if prompt_repetitions < 0:
        raise ReadinessError("warmup prompt repetitions must not be negative")
    if max_tokens <= 0:
        raise ReadinessError("warmup max tokens must be positive")
    if min_tokens < 0 or min_tokens > max_tokens:
        raise ReadinessError("warmup min tokens must be between zero and max tokens")

    prompt = DEFAULT_WARMUP_PROMPT
    if prompt_repetitions:
        prompt += "\n" + WARMUP_PROMPT_UNIT * prompt_repetitions
    payload: dict[str, Any] = {
        "model": model,
        "messages": [
            {"role": "user", "content": prompt},
        ],
        "temperature": 0,
        "max_tokens": max_tokens,
        "chat_template_kwargs": {"enable_thinking": False},
    }
    if min_tokens:
        payload["min_tokens"] = min_tokens
    return payload


def process_instance_id(stat_path: Path = Path("/proc/1/stat")) -> str:
    raw = stat_path.read_text(encoding="utf-8").strip()
    closing_parenthesis = raw.rfind(")")
    if closing_parenthesis < 0:
        raise ReadinessError("model process stat is malformed")
    fields = raw[closing_parenthesis + 1 :].split()
    if len(fields) <= 19:
        raise ReadinessError("model process stat has no start time")
    return fields[19]


def marker_value(
    *,
    model: str,
    payload: dict[str, Any],
    instance_id: str,
) -> str:
    signature = json.dumps(
        {"instance_id": instance_id, "model": model, "payload": payload},
        ensure_ascii=True,
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    return "v2:" + hashlib.sha256(signature).hexdigest()


def write_marker(marker: Path, value: str) -> None:
    temporary = marker.with_name(marker.name + ".tmp")
    temporary.write_text(value + "\n", encoding="utf-8")
    temporary.replace(marker)


def check_readiness(
    *,
    base_url: str,
    model: str,
    marker: Path,
    health_timeout: float,
    warmup_timeout: float,
    instance_id: str,
    prompt_repetitions: int = 0,
    max_tokens: int = 8,
    min_tokens: int = 0,
) -> None:
    base_url = base_url.rstrip("/")
    payload = warmup_payload(
        model,
        prompt_repetitions=prompt_repetitions,
        max_tokens=max_tokens,
        min_tokens=min_tokens,
    )
    expected_marker = marker_value(
        model=model,
        payload=payload,
        instance_id=instance_id,
    )
    try:
        stored_marker = marker.read_text(encoding="utf-8").strip()
    except OSError:
        stored_marker = ""
    if stored_marker == expected_marker:
        response = request_json(base_url + "/models", timeout=health_timeout)
        require_served_model(response, model)
        return

    response = request_json(
        base_url + "/chat/completions",
        timeout=warmup_timeout,
        payload=payload,
    )
    require_completion(response)
    try:
        write_marker(marker, expected_marker)
    except OSError:
        print(
            "model readiness warning: warmup succeeded but marker could not be persisted",
            file=sys.stderr,
        )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--marker", type=Path, required=True)
    parser.add_argument("--health-timeout", type=float, default=3)
    parser.add_argument("--warmup-timeout", type=float, default=110)
    parser.add_argument("--warmup-prompt-repetitions", type=int, default=0)
    parser.add_argument("--warmup-max-tokens", type=int, default=8)
    parser.add_argument("--warmup-min-tokens", type=int, default=0)
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
            instance_id=process_instance_id(),
            prompt_repetitions=args.warmup_prompt_repetitions,
            max_tokens=args.warmup_max_tokens,
            min_tokens=args.warmup_min_tokens,
        )
    except (OSError, ReadinessError) as exc:
        print(f"model readiness failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
