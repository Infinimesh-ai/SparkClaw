#!/usr/bin/env python3
import argparse
import json
import os
import statistics
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone


SCENARIOS = {
    "chat": "Reply in one concise paragraph: what is SparkClaw?",
    "summary": "Summarize this deployment note in five bullet points: SparkClaw runs local fast and deep model lanes, keeps dangerous tools approval-gated, records traces, and evaluates prompt-injection boundaries on DGX Spark.",
    "email_triage": "Triage this inbox item and propose a safe next action without sending anything: Alex asks for the DGX Spark deployment checklist, benchmark status, and any blockers before tomorrow.",
    "coding": "Given a Go HTTP handler that returns 500 when JSON decoding fails, explain the likely bug and provide a compact patch outline.",
    "long_answer": "Write a structured technical answer about how to validate a local agent runtime on DGX Spark. Keep it under 500 words.",
    "tool_json": 'Return only valid JSON for this tool call: {"tool":"files.search","args":{"query":"DGX Spark","max_results":5}}',
}


def env(name, default=""):
    return os.environ.get(name, default).strip()


def estimate_tokens(text):
    return max(1, len(text) // 4)


def chat_max_tokens(lane):
    return int(env(f"SPARKCLAW_{lane.upper()}_MAX_TOKENS", "512"))


def disable_thinking():
    value = env("SPARKCLAW_MODEL_DISABLE_THINKING", "true").lower()
    return value in {"1", "true", "yes", "on"}


def message_text(message):
    content = (message or {}).get("content") or ""
    return content.strip()


def annotate_empty_content(measurement, choice):
    message = (choice or {}).get("message") or {}
    reasoning = (message.get("reasoning") or "").strip()
    if measurement["response_preview"]:
        return
    if reasoning:
        measurement["empty_content_reason"] = "reasoning_only"
        measurement["reasoning_preview"] = reasoning[:240]
    else:
        measurement["empty_content_reason"] = "empty_message"


def request_json(method, url, body=None, timeout=120):
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    key = env("OPENAI_API_KEY")
    if key:
        headers["Authorization"] = "Bearer " + key
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))


def generative_scoring_base_url(base_url):
    stripped = base_url.rstrip("/")
    if stripped.endswith("/v1"):
        return stripped[:-3]
    return stripped


def stream_chat(lane, base_url, model, prompt, timeout):
    endpoint = base_url.rstrip("/") + "/chat/completions"
    body = {
        "model": model,
        "messages": [
            {"role": "system", "content": "You are the SparkClaw benchmark assistant. Answer plainly and follow formatting constraints exactly."},
            {"role": "user", "content": prompt},
        ],
        "temperature": 0.2,
        "max_tokens": chat_max_tokens(lane),
        "stream": True,
        "stream_options": {"include_usage": True},
    }
    if disable_thinking():
        body["chat_template_kwargs"] = {"enable_thinking": False}
    data = json.dumps(body).encode("utf-8")
    headers = {"Content-Type": "application/json"}
    key = env("OPENAI_API_KEY")
    if key:
        headers["Authorization"] = "Bearer " + key
    req = urllib.request.Request(endpoint, data=data, headers=headers, method="POST")
    started = time.perf_counter()
    first_token_at = None
    content = []
    reasoning = []
    usage = {}
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        for raw_line in resp:
            line = raw_line.decode("utf-8", errors="replace").strip()
            if not line or not line.startswith("data:"):
                continue
            payload = line[5:].strip()
            if payload == "[DONE]":
                break
            event = json.loads(payload)
            if event.get("usage"):
                usage = event["usage"]
            for choice in event.get("choices", []):
                delta = choice.get("delta", {})
                piece = delta.get("content") or ""
                reason_piece = delta.get("reasoning") or ""
                if piece and first_token_at is None:
                    first_token_at = time.perf_counter()
                content.append(piece)
                reasoning.append(reason_piece)
    completed = time.perf_counter()
    text = "".join(content).strip()
    reasoning_text = "".join(reasoning).strip()
    completion_tokens = usage.get("completion_tokens") or estimate_tokens(text)
    measurement = {
        "ttft_ms": round(((first_token_at or completed) - started) * 1000, 1),
        "total_latency_ms": round((completed - started) * 1000, 1),
        "completion_tokens": completion_tokens,
        "tokens_per_second": round(completion_tokens / max(completed - (first_token_at or started), 0.001), 2),
        "response_preview": text[:240],
        "usage": usage,
        "streaming": True,
    }
    if not text and reasoning_text:
        measurement["empty_content_reason"] = "reasoning_only"
        measurement["reasoning_preview"] = reasoning_text[:240]
    elif not text:
        measurement["empty_content_reason"] = "empty_message"
    return measurement


def non_stream_chat(lane, base_url, model, prompt, timeout):
    endpoint = base_url.rstrip("/") + "/chat/completions"
    started = time.perf_counter()
    body = {
        "model": model,
        "messages": [
            {"role": "system", "content": "You are the SparkClaw benchmark assistant. Answer plainly and follow formatting constraints exactly."},
            {"role": "user", "content": prompt},
        ],
        "temperature": 0.2,
        "max_tokens": chat_max_tokens(lane),
    }
    if disable_thinking():
        body["chat_template_kwargs"] = {"enable_thinking": False}
    decoded = request_json("POST", endpoint, body, timeout=timeout)
    completed = time.perf_counter()
    choice = decoded.get("choices", [{}])[0]
    text = message_text(choice.get("message", {}))
    usage = decoded.get("usage") or {}
    completion_tokens = usage.get("completion_tokens") or estimate_tokens(text)
    total = completed - started
    measurement = {
        "ttft_ms": round(total * 1000, 1),
        "total_latency_ms": round(total * 1000, 1),
        "completion_tokens": completion_tokens,
        "tokens_per_second": round(completion_tokens / max(total, 0.001), 2),
        "response_preview": text[:240],
        "usage": usage,
        "streaming": False,
    }
    annotate_empty_content(measurement, choice)
    return measurement


def benchmark_chat(lane, base_url, model, scenarios, repeats, timeout):
    results = []
    request_json("GET", base_url.rstrip("/") + "/models", timeout=timeout)
    for scenario in scenarios:
        prompt = SCENARIOS[scenario]
        for iteration in range(1, repeats + 1):
            try:
                measurement = stream_chat(lane, base_url, model, prompt, timeout)
            except Exception as stream_err:
                measurement = non_stream_chat(lane, base_url, model, prompt, timeout)
                measurement["stream_error"] = str(stream_err)
            measurement.update({
                "lane": lane,
                "model": model,
                "scenario": scenario,
                "iteration": iteration,
            })
            results.append(measurement)
            print(f"{lane} {scenario} #{iteration}: ttft={measurement['ttft_ms']}ms total={measurement['total_latency_ms']}ms tok/s={measurement['tokens_per_second']}", file=sys.stderr)
    return results


def check_embedding(base_url, model, timeout):
    if not base_url or not model:
        return {"status": "skipped", "reason": "embedding endpoint or model is not configured"}
    started = time.perf_counter()
    decoded = request_json("POST", base_url.rstrip("/") + "/embeddings", {
        "model": model,
        "input": ["SparkClaw DGX Spark local agent runtime benchmark"],
    }, timeout=timeout)
    completed = time.perf_counter()
    vector = decoded.get("data", [{}])[0].get("embedding", [])
    return {
        "status": "passed" if vector else "failed",
        "model": model,
        "latency_ms": round((completed - started) * 1000, 1),
        "dimensions": len(vector),
        "usage": decoded.get("usage") or {},
    }


def check_reranker(base_url, model, timeout):
    if not base_url or not model:
        return {"status": "skipped", "reason": "reranker endpoint or model is not configured"}
    started = time.perf_counter()
    query = "DGX Spark validation"
    documents = [
        "SparkClaw records traces and approval decisions.",
        "A garden calendar lists watering reminders.",
    ]
    try:
        decoded = request_json("POST", base_url.rstrip("/") + "/rerank", {
            "model": model,
            "query": query,
            "documents": documents,
            "top_n": 2,
        }, timeout=timeout)
    except urllib.error.HTTPError as err:
        if err.code != 404:
            raise
        decoded = request_json("POST", generative_scoring_base_url(base_url) + "/generative_scoring", {
            "model": model,
            "query": "Is this document relevant to the query? Answer Yes or No.\nQuery: " + query,
            "items": ["Document: " + document + "\nAnswer:" for document in documents],
            "label_token_ids": [7414, 2308],
            "apply_softmax": True,
        }, timeout=timeout)
    completed = time.perf_counter()
    results = decoded.get("results") or decoded.get("data") or []
    return {
        "status": "passed" if results else "failed",
        "model": model,
        "latency_ms": round((completed - started) * 1000, 1),
        "results": results[:2],
        "usage": decoded.get("usage") or {},
    }


def summarize(results):
    grouped = {}
    for item in results:
        key = (item["lane"], item["scenario"])
        grouped.setdefault(key, []).append(item)
    rows = []
    for (lane, scenario), items in sorted(grouped.items()):
        rows.append({
            "lane": lane,
            "scenario": scenario,
            "runs": len(items),
            "ttft_p50_ms": round(statistics.median(x["ttft_ms"] for x in items), 1),
            "ttft_p95_ms": round(max(x["ttft_ms"] for x in items), 1) if len(items) < 20 else round(statistics.quantiles([x["ttft_ms"] for x in items], n=20)[18], 1),
            "total_p50_ms": round(statistics.median(x["total_latency_ms"] for x in items), 1),
            "total_p95_ms": round(max(x["total_latency_ms"] for x in items), 1) if len(items) < 20 else round(statistics.quantiles([x["total_latency_ms"] for x in items], n=20)[18], 1),
            "tokens_per_second_p50": round(statistics.median(x["tokens_per_second"] for x in items), 2),
        })
    return rows


def append_markdown(path, report):
    date = report["started_at"][:10]
    hardware = report["hardware"].replace("|", "/")
    with open(path, "a", encoding="utf-8") as f:
        f.write("\n## DGX Spark Run " + report["started_at"] + "\n\n")
        f.write("| Date | Hardware | Lane | Scenario | Runs | TTFT p50 ms | TTFT p95 ms | Total p50 ms | Total p95 ms | Tokens/s p50 | Notes |\n")
        f.write("|---|---|---|---|---:|---:|---:|---:|---:|---:|---|\n")
        for row in report["summary"]:
            f.write(f"| {date} | {hardware} | {row['lane']} | {row['scenario']} | {row['runs']} | {row['ttft_p50_ms']} | {row['ttft_p95_ms']} | {row['total_p50_ms']} | {row['total_p95_ms']} | {row['tokens_per_second_p50']} | real endpoint benchmark |\n")
        f.write("\n")


def main():
    parser = argparse.ArgumentParser(description="Benchmark SparkClaw OpenAI-compatible local model endpoints.")
    parser.add_argument("--lanes", default=env("SPARKCLAW_BENCH_LANES", "fast,deep"))
    parser.add_argument("--scenarios", default=env("SPARKCLAW_BENCH_SCENARIOS", "chat,summary,email_triage,coding,long_answer,tool_json"))
    parser.add_argument("--repeats", type=int, default=int(env("SPARKCLAW_BENCH_REPEATS", "3")))
    parser.add_argument("--timeout", type=int, default=int(env("SPARKCLAW_BENCH_TIMEOUT", "180")))
    parser.add_argument("--output", default=env("SPARKCLAW_BENCH_OUTPUT", "data/eval/model-benchmark-report.json"))
    parser.add_argument("--append-markdown", default="")
    args = parser.parse_args()

    lanes = [x.strip() for x in args.lanes.split(",") if x.strip()]
    scenarios = [x.strip() for x in args.scenarios.split(",") if x.strip()]
    unknown = sorted(set(scenarios) - set(SCENARIOS))
    if unknown:
        raise SystemExit("unknown scenarios: " + ", ".join(unknown))

    endpoints = {
        "fast": (env("SPARKCLAW_FAST_BASE_URL", "http://127.0.0.1:8001/v1"), env("SPARKCLAW_FAST_MODEL", env("SPARKCLAW_FAST_SERVED_NAME", "sparkclaw-fast"))),
        "deep": (env("SPARKCLAW_DEEP_BASE_URL", "http://127.0.0.1:8002/v1"), env("SPARKCLAW_DEEP_MODEL", env("SPARKCLAW_DEEP_SERVED_NAME", "sparkclaw-deep"))),
    }
    started_at = datetime.now(timezone.utc).isoformat()
    results = []
    errors = []
    for lane in lanes:
        base_url, model = endpoints.get(lane, ("", ""))
        if not base_url or not model:
            errors.append({"lane": lane, "error": "lane is not configured"})
            continue
        try:
            results.extend(benchmark_chat(lane, base_url, model, scenarios, args.repeats, args.timeout))
        except Exception as err:
            errors.append({"lane": lane, "base_url": base_url, "model": model, "error": str(err)})

    report = {
        "started_at": started_at,
        "completed_at": datetime.now(timezone.utc).isoformat(),
        "hardware": env("SPARKCLAW_HARDWARE_LABEL", "DGX Spark"),
        "lanes": lanes,
        "scenarios": scenarios,
        "repeats": args.repeats,
        "summary": summarize(results),
        "results": results,
        "endpoint_checks": {
            "embedding": None,
            "reranker": None,
        },
        "errors": errors,
    }
    try:
        embedding_model = env("SPARKCLAW_EMBEDDING_SERVED_NAME", env("SPARKCLAW_EMBEDDING_MODEL", "Qwen/Qwen3-Embedding-0.6B"))
        report["endpoint_checks"]["embedding"] = check_embedding(env("SPARKCLAW_EMBEDDING_BASE_URL", "http://127.0.0.1:8003/v1"), embedding_model, args.timeout)
    except Exception as err:
        report["endpoint_checks"]["embedding"] = {"status": "failed", "error": str(err)}
    try:
        reranker_model = env("SPARKCLAW_RERANKER_SERVED_NAME", env("SPARKCLAW_RERANKER_MODEL", "Qwen/Qwen3-Reranker-0.6B"))
        report["endpoint_checks"]["reranker"] = check_reranker(env("SPARKCLAW_RERANKER_BASE_URL", "http://127.0.0.1:8004/v1"), reranker_model, args.timeout)
    except Exception as err:
        report["endpoint_checks"]["reranker"] = {"status": "failed", "error": str(err)}

    os.makedirs(os.path.dirname(args.output) or ".", exist_ok=True)
    with open(args.output, "w", encoding="utf-8") as f:
        json.dump(report, f, indent=2)
        f.write("\n")
    if args.append_markdown and report["summary"]:
        append_markdown(args.append_markdown, report)
    print(json.dumps({
        "output": args.output,
        "summary_rows": len(report["summary"]),
        "errors": report["errors"],
        "endpoint_checks": report["endpoint_checks"],
    }, indent=2))
    if errors:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
