#!/usr/bin/env python3
"""Measure conservative prompt-estimation coefficients against vLLM."""

from __future__ import annotations

import argparse
import json
import urllib.request


SAMPLES = {
    "english": (
        "SparkClaw reads local documents, preserves provenance, and returns "
        "grounded answers from bounded workflow evidence."
    ),
    "chinese": (
        "SparkClaw 的文档工作流必须先确认文档地址，再执行读取或编辑。"
        "Embedding 只接收当前问题，Fast 使用有界上下文消解指代。"
    ),
    "json": (
        '{"type":"action","tool":"files.read","arguments":'
        '{"path":"reports/architecture-note.md","max_bytes":48000},'
        '"reason":"read the governed document"}'
    ),
    "mixed": (
        'TaskHint: {"workflow_id":"document.read",'
        '"requires_tool_evidence":true}\n'
        "用户问题：请阅读这份文档并回答双通道分别有什么要求？\n"
        'Observation: files.read completed path="notes.md" read_complete=true.'
    ),
}


def tokenize(endpoint: str, model: str, prompt: str) -> int:
    payload = json.dumps({"model": model, "prompt": prompt}).encode("utf-8")
    request = urllib.request.Request(
        endpoint,
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        decoded = json.load(response)
    count = decoded.get("count")
    if not isinstance(count, int):
        tokens = decoded.get("tokens")
        if not isinstance(tokens, list):
            raise RuntimeError(f"tokenize response has no count or tokens: {decoded}")
        count = len(tokens)
    return count


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--endpoint",
        default="http://127.0.0.1:8001/tokenize",
        help="OpenAI-compatible vLLM tokenize endpoint",
    )
    parser.add_argument("--model", default="sparkclaw-fast")
    args = parser.parse_args()

    rows = []
    for name, prompt in SAMPLES.items():
        byte_count = len(prompt.encode("utf-8"))
        token_count = tokenize(args.endpoint, args.model, prompt)
        rows.append(
            {
                "sample": name,
                "bytes": byte_count,
                "tokens": token_count,
                "bytes_per_token": round(byte_count / token_count, 3),
            }
        )
    print(json.dumps({"endpoint": args.endpoint, "model": args.model, "samples": rows}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
