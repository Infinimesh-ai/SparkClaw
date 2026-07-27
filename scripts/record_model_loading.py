#!/usr/bin/env python3
import argparse
import csv
import json
import os
import subprocess
import urllib.error
import urllib.request
from datetime import datetime, timezone


def env(name, default=""):
    return os.environ.get(name, default).strip()


def run(cmd):
    try:
        completed = subprocess.run(cmd, check=False, text=True, capture_output=True)
    except FileNotFoundError as err:
        return {"ok": False, "error": str(err)}
    return {
        "ok": completed.returncode == 0,
        "returncode": completed.returncode,
        "stdout": completed.stdout.strip(),
        "stderr": completed.stderr.strip(),
    }


def nvidia_processes():
    result = run([
        "nvidia-smi",
        "--query-compute-apps=pid,process_name,used_memory",
        "--format=csv,noheader,nounits",
    ])
    if not result["ok"]:
        return {"status": "failed", "error": result.get("stderr") or result.get("error", "")}
    rows = []
    for row in csv.reader(result["stdout"].splitlines()):
        if len(row) < 3:
            continue
        rows.append({
            "pid": row[0].strip(),
            "process_name": row[1].strip(),
            "used_memory_mib": row[2].strip(),
        })
    return {"status": "passed", "processes": rows}


def nvidia_gpu():
    result = run([
        "nvidia-smi",
        "--query-gpu=name,driver_version,utilization.gpu,temperature.gpu",
        "--format=csv,noheader,nounits",
    ])
    if not result["ok"]:
        return {"status": "failed", "error": result.get("stderr") or result.get("error", "")}
    rows = []
    for row in csv.reader(result["stdout"].splitlines()):
        if len(row) < 4:
            continue
        rows.append({
            "name": row[0].strip(),
            "driver_version": row[1].strip(),
            "utilization_gpu_percent": row[2].strip(),
            "temperature_c": row[3].strip(),
        })
    return {"status": "passed", "gpus": rows}


def endpoint_models(base_url, timeout):
    try:
        with urllib.request.urlopen(base_url.rstrip("/") + "/models", timeout=timeout) as resp:
            decoded = json.loads(resp.read().decode("utf-8"))
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as err:
        return {"status": "failed", "error": str(err)}
    return {"status": "passed", "models": decoded}


def load_json(path):
    if not path or not os.path.exists(path):
        return None
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def main():
    parser = argparse.ArgumentParser(description="Record a SparkClaw model-loading experiment snapshot.")
    parser.add_argument("--profile", default=env("SPARKCLAW_BENCH_PROFILE", env("SPARKCLAW_MODEL_LOADING_PROFILE", "")))
    parser.add_argument("--benchmark", default=env("SPARKCLAW_BENCH_OUTPUT", "data/eval/model-benchmark-report.json"))
    parser.add_argument("--output", default="")
    parser.add_argument("--timeout", type=int, default=5)
    args = parser.parse_args()

    started_at = datetime.now(timezone.utc).isoformat()
    profile = args.profile or "unknown"
    output = args.output or "data/eval/model-loading-" + profile + "-" + started_at.replace(":", "").replace("+", "Z") + ".json"
    report = {
        "recorded_at": started_at,
        "profile": profile,
        "hardware_label": env("SPARKCLAW_HARDWARE_LABEL", "DGX Spark"),
        "gpu": nvidia_gpu(),
        "compute_processes": nvidia_processes(),
        "endpoints": {
            "fast": endpoint_models(env("SPARKCLAW_FAST_BASE_URL", "http://127.0.0.1:8001/v1"), args.timeout),
            "deep": endpoint_models(env("SPARKCLAW_DEEP_BASE_URL", "http://127.0.0.1:8002/v1"), args.timeout),
            "embedding": endpoint_models(env("SPARKCLAW_EMBEDDING_BASE_URL", "http://127.0.0.1:8003/v1"), args.timeout),
            "guard": endpoint_models(env("SPARKCLAW_GUARD_BASE_URL", "http://127.0.0.1:8005/v1"), args.timeout),
        },
        "benchmark": load_json(args.benchmark),
    }
    os.makedirs(os.path.dirname(output) or ".", exist_ok=True)
    with open(output, "w", encoding="utf-8") as f:
        json.dump(report, f, indent=2)
        f.write("\n")
    print(json.dumps({"output": output, "profile": profile}, indent=2))


if __name__ == "__main__":
    main()
