#!/usr/bin/env python3
"""Run a bounded, deterministic SparkClaw capability evaluation against one chat endpoint."""

import argparse
import concurrent.futures
import hashlib
import json
import math
import os
import random
import re
import statistics
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone


METRICS = ("contract", "groundedness", "injection", "failure_handling")


def utc_now():
    return datetime.now(timezone.utc).isoformat()


def percentile(values, percentile_value):
    if not values:
        return None
    ordered = sorted(values)
    rank = max(1, math.ceil(percentile_value * len(ordered)))
    return round(ordered[rank - 1], 1)


def redact_url(value):
    parsed = urllib.parse.urlsplit(value)
    host = parsed.hostname or ""
    if parsed.port:
        host += f":{parsed.port}"
    return urllib.parse.urlunsplit((parsed.scheme, host, parsed.path, "", ""))


def request_headers():
    headers = {"Content-Type": "application/json"}
    key = os.environ.get("SPARKCLAW_EVAL_API_KEY", "").strip()
    if key:
        headers["Authorization"] = "Bearer " + key
    return headers


def endpoint_models(base_url, timeout):
    request = urllib.request.Request(
        base_url.rstrip("/") + "/models", headers=request_headers(), method="GET"
    )
    started = time.perf_counter()
    with urllib.request.urlopen(request, timeout=timeout) as response:
        decoded = json.loads(response.read().decode("utf-8"))
    return {
        "latency_ms": round((time.perf_counter() - started) * 1000, 1),
        "model_ids": [item.get("id", "") for item in decoded.get("data", [])],
    }


def numeric_usage(value):
    value = value or {}
    return {
        key: int(value[key])
        for key in ("prompt_tokens", "completion_tokens", "total_tokens")
        if isinstance(value.get(key), (int, float))
    }


def stream_completion(base_url, model, case, settings, seed):
    body = {
        "model": model,
        "messages": [
            {"role": "system", "content": case["system"]},
            {"role": "user", "content": case["user"]},
        ],
        "temperature": settings["temperature"],
        "top_p": settings["top_p"],
        "max_tokens": case.get("max_tokens", settings["max_tokens"]),
        "seed": seed,
        "stream": True,
        "stream_options": {"include_usage": True},
        "chat_template_kwargs": {"enable_thinking": settings["enable_thinking"]},
    }
    raw = json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    request = urllib.request.Request(
        base_url.rstrip("/") + "/chat/completions",
        data=raw,
        headers=request_headers(),
        method="POST",
    )
    started = time.perf_counter()
    first_event_at = None
    first_content_at = None
    content = []
    reasoning_seen = False
    usage = {}
    finish_reason = ""
    with urllib.request.urlopen(request, timeout=settings["timeout"]) as response:
        for raw_line in response:
            line = raw_line.decode("utf-8", errors="replace").strip()
            if not line.startswith("data:"):
                continue
            payload = line[5:].strip()
            if not payload or payload == "[DONE]":
                continue
            event = json.loads(payload)
            if event.get("usage"):
                usage = numeric_usage(event["usage"])
            for choice in event.get("choices", []):
                delta = choice.get("delta") or {}
                piece = delta.get("content") or ""
                reasoning = delta.get("reasoning") or delta.get("reasoning_content") or ""
                if (piece or reasoning) and first_event_at is None:
                    first_event_at = time.perf_counter()
                if piece and first_content_at is None:
                    first_content_at = time.perf_counter()
                if reasoning:
                    reasoning_seen = True
                content.append(piece)
                if choice.get("finish_reason"):
                    finish_reason = choice["finish_reason"]
    completed = time.perf_counter()
    answer = "".join(content).strip()
    return {
        "response": answer,
        "ttfe_ms": round(((first_event_at or completed) - started) * 1000, 1),
        "ttft_ms": round(((first_content_at or completed) - started) * 1000, 1),
        "total_latency_ms": round((completed - started) * 1000, 1),
        "usage": usage,
        "finish_reason": finish_reason,
        "reasoning_seen": reasoning_seen,
        "request_sha256": hashlib.sha256(raw).hexdigest(),
    }


def json_path(value, path):
    current = value
    if not path:
        return current
    for part in path.split("."):
        if isinstance(current, dict) and part in current:
            current = current[part]
        else:
            raise KeyError(path)
    return current


def parse_strict_json(content):
    try:
        return json.loads(content), ""
    except (TypeError, json.JSONDecodeError) as error:
        return None, f"invalid_json:{error.__class__.__name__}"


def contains(content, expected, case_sensitive=False):
    if case_sensitive:
        return expected in content
    return expected.casefold() in content.casefold()


def evaluate_check(content, parsed, parse_error, check):
    kind = check["kind"]
    try:
        if kind == "strict_json":
            passed = parse_error == "" and isinstance(parsed, dict)
        elif kind == "json_exact_keys":
            target = json_path(parsed, check.get("path", ""))
            passed = isinstance(target, dict) and set(target) == set(check["value"])
        elif kind == "json_allowed_keys":
            target = json_path(parsed, check.get("path", ""))
            passed = isinstance(target, dict) and set(target).issubset(set(check["value"]))
        elif kind == "json_equals":
            passed = json_path(parsed, check["path"]) == check["value"]
        elif kind == "json_array_length":
            target = json_path(parsed, check["path"])
            passed = isinstance(target, list) and len(target) == check["value"]
        elif kind == "json_array_field_set":
            target = json_path(parsed, check["path"])
            passed = isinstance(target, list) and {
                item.get(check["field"]) for item in target if isinstance(item, dict)
            } == set(check["value"])
        elif kind == "json_array_field_unique":
            target = json_path(parsed, check["path"])
            values = [item.get(check["field"]) for item in target if isinstance(item, dict)]
            passed = len(values) == len(target) and len(values) == len(set(values))
        elif kind == "json_array_number_range":
            target = json_path(parsed, check["path"])
            values = [item.get(check["field"]) for item in target if isinstance(item, dict)]
            passed = len(values) == len(target) and all(
                isinstance(value, (int, float))
                and not isinstance(value, bool)
                and check["min"] <= value <= check["max"]
                for value in values
            )
        elif kind == "json_top_field":
            target = json_path(parsed, check["path"])
            best = max(target, key=lambda item: item[check["score_field"]])
            passed = best.get(check["id_field"]) == check["value"]
        elif kind == "json_top_n_field_set":
            target = json_path(parsed, check["path"])
            ordered = sorted(target, key=lambda item: item[check["score_field"]], reverse=True)
            actual = {item.get(check["id_field"]) for item in ordered[: check["n"]]}
            passed = actual == set(check["value"])
        elif kind == "json_top_gap_max":
            target = json_path(parsed, check["path"])
            scores = sorted((item[check["score_field"]] for item in target), reverse=True)
            passed = len(scores) >= 2 and scores[0] - scores[1] <= check["value"]
        elif kind == "contains_all":
            passed = all(contains(content, value, check.get("case_sensitive", False)) for value in check["value"])
        elif kind == "contains_any":
            passed = any(contains(content, value, check.get("case_sensitive", False)) for value in check["value"])
        elif kind == "not_contains_any":
            passed = not any(contains(content, value, check.get("case_sensitive", False)) for value in check["value"])
        elif kind == "regex":
            passed = re.search(check["value"], content, re.MULTILINE) is not None
        elif kind == "not_regex":
            passed = re.search(check["value"], content, re.MULTILINE) is None
        else:
            raise ValueError(f"unsupported check kind {kind}")
    except (KeyError, TypeError, ValueError):
        passed = False
    return {
        "kind": kind,
        "metric": check.get("metric", "task"),
        "passed": passed,
        "label": check.get("label", kind),
    }


def score_response(case, content):
    parsed, parse_error = parse_strict_json(content)
    checks = [evaluate_check(content, parsed, parse_error, check) for check in case["checks"]]
    metric_results = {}
    for metric in METRICS:
        selected = [item for item in checks if item["metric"] == metric]
        metric_results[metric] = None if not selected else all(item["passed"] for item in selected)
    failed = [item["label"] for item in checks if not item["passed"]]
    return {
        "task_success": bool(content) and not failed,
        "metrics": metric_results,
        "checks": checks,
        "failed_checks": failed,
        "response_sha256": hashlib.sha256(content.encode("utf-8")).hexdigest(),
    }


def safe_transport_error(error):
    if isinstance(error, urllib.error.HTTPError):
        return {"type": "http_error", "http_status": error.code}
    if isinstance(error, urllib.error.URLError):
        return {"type": "url_error", "reason_type": error.reason.__class__.__name__}
    if isinstance(error, TimeoutError):
        return {"type": "timeout"}
    return {"type": error.__class__.__name__}


def run_task(base_url, model, case, settings, repeat, seed):
    started_at = utc_now()
    try:
        measurement = stream_completion(base_url, model, case, settings, seed)
    except Exception as error:  # Transport failures are evidence, not model-quality scores.
        return {
            "case_id": case["id"],
            "repeat": repeat,
            "seed": seed,
            "started_at": started_at,
            "completed_at": utc_now(),
            "status": "infrastructure_failure",
            "error": safe_transport_error(error),
        }
    score = score_response(case, measurement["response"])
    failure_type = ""
    if not measurement["response"]:
        failure_type = "empty_response"
    elif measurement["finish_reason"] == "length":
        failure_type = "truncated"
    elif not score["task_success"]:
        failure_type = "quality_failure"
    return {
        "case_id": case["id"],
        "category": case["category"],
        "language": case["language"],
        "dimensions": case.get("dimensions", []),
        "source": case["source"],
        "repeat": repeat,
        "seed": seed,
        "started_at": started_at,
        "completed_at": utc_now(),
        "status": "completed",
        "failure_type": failure_type,
        **measurement,
        **score,
    }


def rate(values):
    return None if not values else round(sum(bool(value) for value in values) / len(values), 4)


def summarize(results, cases):
    completed = [item for item in results if item["status"] == "completed"]
    latencies = [item["total_latency_ms"] for item in completed]
    ttfts = [item["ttft_ms"] for item in completed]
    completion_tokens = [
        item["usage"].get("completion_tokens")
        for item in completed
        if item.get("usage", {}).get("completion_tokens") is not None
    ]
    by_case = []
    for case in cases:
        items = [item for item in completed if item["case_id"] == case["id"]]
        hashes = {item["response_sha256"] for item in items}
        by_case.append({
            "case_id": case["id"],
            "runs": len(items),
            "success_rate": rate([item["task_success"] for item in items]),
            "stable_pass_fail": len({item["task_success"] for item in items}) <= 1 if items else None,
            "exact_output_consistency": len(hashes) <= 1 if items else None,
            "failure_types": sorted({item["failure_type"] for item in items if item["failure_type"]}),
        })
    metric_summary = {}
    for metric in METRICS:
        values = [item["metrics"][metric] for item in completed if item["metrics"][metric] is not None]
        metric_summary[metric + "_rate"] = rate(values)
    language_summary = {
        language: rate([item["task_success"] for item in completed if item["language"] == language])
        for language in ("en", "zh")
    }
    category_summary = {
        category: rate([item["task_success"] for item in completed if item["category"] == category])
        for category in sorted({case["category"] for case in cases})
    }
    stable = [item["stable_pass_fail"] for item in by_case if item["stable_pass_fail"] is not None]
    return {
        "requested_runs": len(results),
        "completed_runs": len(completed),
        "infrastructure_failures": len(results) - len(completed),
        "task_success_rate": rate([item["task_success"] for item in completed]),
        **metric_summary,
        "language_success_rate": language_summary,
        "category_success_rate": category_summary,
        "stable_pass_fail_rate": rate(stable),
        "latency_ms": {
            "ttft_p50": percentile(ttfts, 0.50),
            "ttft_p95": percentile(ttfts, 0.95),
            "total_p50": percentile(latencies, 0.50),
            "total_p95": percentile(latencies, 0.95),
        },
        "completion_tokens_median": None if not completion_tokens else statistics.median(completion_tokens),
        "by_case": by_case,
    }


def materialize_cases(document):
    if document.get("schema_version") != "sparkclaw_model_capability_cases_v1":
        raise ValueError("unsupported case schema_version")
    templates = document.get("templates") or {}
    system_templates = templates.get("systems") or {}
    user_templates = templates.get("users") or {}
    check_templates = templates.get("checks") or {}
    cases = json.loads(json.dumps(document.get("cases") or [], ensure_ascii=False))
    for case in cases:
        if "system_template" in case:
            case["system"] = system_templates[case.pop("system_template")]
        if "user_template" in case:
            case["user"] = user_templates[case.pop("user_template")]
        inherited_checks = []
        if "checks_template" in case:
            inherited_checks = check_templates[case.pop("checks_template")]
        case["checks"] = json.loads(json.dumps(inherited_checks + case.get("checks", [])))
        for key, value in (case.pop("variables", {}) or {}).items():
            marker = "{{" + key + "}}"
            case["system"] = case.get("system", "").replace(marker, str(value))
            case["user"] = case.get("user", "").replace(marker, str(value))
            for check in case["checks"]:
                encoded = json.dumps(check, ensure_ascii=False).replace(marker, str(value))
                check.clear()
                check.update(json.loads(encoded))
    ids = [case.get("id") for case in cases]
    if not cases or None in ids or len(ids) != len(set(ids)):
        raise ValueError("cases must have unique non-empty ids")
    for case in cases:
        for field in ("category", "language", "source", "system", "user", "checks"):
            if not case.get(field):
                raise ValueError(f"case {case['id']} is missing {field}")
    return cases


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cases", default="eval/model-capability/cases.json")
    parser.add_argument("--candidate-id", required=True)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--seeds", default="101,202,303")
    parser.add_argument("--order-seed", type=int, default=20260821)
    parser.add_argument("--timeout", type=int, default=120)
    parser.add_argument("--concurrency", type=int, default=1)
    parser.add_argument("--temperature", type=float, default=0.2)
    parser.add_argument("--top-p", type=float, default=1.0)
    parser.add_argument("--max-tokens", type=int, default=512)
    parser.add_argument("--enable-thinking", action="store_true")
    parser.add_argument("--case-id", action="append", default=[])
    parser.add_argument("--warmup", type=int, default=1)
    args = parser.parse_args()

    if args.repeats < 1 or args.timeout < 1 or not 1 <= args.concurrency <= 8:
        raise SystemExit("repeats/timeout must be positive and concurrency must be in [1,8]")
    seeds = [int(value.strip()) for value in args.seeds.split(",") if value.strip()]
    if len(seeds) < args.repeats:
        raise SystemExit("provide at least one seed per repeat")
    with open(args.cases, encoding="utf-8") as handle:
        case_document = json.load(handle)
        cases = materialize_cases(case_document)
    if args.case_id:
        selected = set(args.case_id)
        cases = [case for case in cases if case["id"] in selected]
        missing = selected - {case["id"] for case in cases}
        if missing:
            raise SystemExit("unknown case ids: " + ", ".join(sorted(missing)))

    settings = {
        "temperature": args.temperature,
        "top_p": args.top_p,
        "max_tokens": args.max_tokens,
        "enable_thinking": args.enable_thinking,
        "timeout": args.timeout,
        "concurrency": args.concurrency,
    }
    started_at = utc_now()
    try:
        endpoint_check = endpoint_models(args.base_url, args.timeout)
    except Exception as error:
        endpoint_check = {"status": "failed", "error": safe_transport_error(error)}
    else:
        endpoint_check["status"] = "passed"

    warmup_results = []
    if endpoint_check["status"] == "passed" and args.warmup > 0:
        for index in range(args.warmup):
            warmup_results.append(run_task(
                args.base_url, args.model, cases[0], settings, 0, seeds[index % len(seeds)]
            ))

    tasks = [
        (case, repeat + 1, seeds[repeat])
        for repeat in range(args.repeats)
        for case in cases
    ]
    random.Random(args.order_seed).shuffle(tasks)
    results = []
    if endpoint_check["status"] == "passed":
        with concurrent.futures.ThreadPoolExecutor(max_workers=args.concurrency) as executor:
            futures = [
                executor.submit(run_task, args.base_url, args.model, case, settings, repeat, seed)
                for case, repeat, seed in tasks
            ]
            for future in concurrent.futures.as_completed(futures):
                item = future.result()
                results.append(item)
                print(
                    f"{item['case_id']} repeat={item['repeat']} status={item['status']} "
                    f"success={item.get('task_success')}",
                    file=sys.stderr,
                )
    results.sort(key=lambda item: (item["case_id"], item["repeat"]))
    report = {
        "schema_version": "sparkclaw_model_capability_result_v1",
        "started_at": started_at,
        "completed_at": utc_now(),
        "candidate": {
            "id": args.candidate_id,
            "model": args.model,
            "base_url": redact_url(args.base_url),
        },
        "case_set": {
            "path": args.cases,
            "schema_version": case_document["schema_version"],
            "sha256": hashlib.sha256(
                json.dumps(case_document, ensure_ascii=False, sort_keys=True).encode("utf-8")
            ).hexdigest(),
            "case_ids": [case["id"] for case in cases],
        },
        "settings": {
            **settings,
            "repeats": args.repeats,
            "seeds": seeds[: args.repeats],
            "order_seed": args.order_seed,
            "stream": True,
        },
        "endpoint_check": endpoint_check,
        "warmup": {
            "runs": len(warmup_results),
            "completed": sum(item["status"] == "completed" for item in warmup_results),
        },
        "summary": summarize(results, cases),
        "results": results,
    }
    os.makedirs(os.path.dirname(args.output) or ".", exist_ok=True)
    with open(args.output, "w", encoding="utf-8") as handle:
        json.dump(report, handle, ensure_ascii=False, indent=2)
        handle.write("\n")
    print(json.dumps({"output": args.output, "summary": report["summary"]}, indent=2))
    if endpoint_check["status"] != "passed" or report["summary"]["infrastructure_failures"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
