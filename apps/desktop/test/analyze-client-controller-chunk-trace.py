#!/usr/bin/env python3
"""Correlate opt-in chunk observations with the installed helper's private trace.

Launch the exact_limit qualification with SPARKCLAW_CONTROLLER_CHUNK_TRACE=1
and SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR set to an existing private directory.
Usage: python3 analyze-client-controller-chunk-trace.py EVIDENCE TRACE_DIR NEW_REPORT
This read-only analysis never changes a failed qualification into a pass.
"""
import hashlib
import json
import math
from pathlib import Path
import statistics
import sys

evidence_file, trace_dir, output = map(Path, sys.argv[1:])
assert all(p.is_absolute() for p in (evidence_file, trace_dir, output))
assert not output.exists() and trace_dir.is_dir() and trace_dir.stat().st_mode & 0o077 == 0
evidence = json.loads(evidence_file.read_text())
assert evidence["mode"] == "exact_limit"
limit = evidence["trace"]["limit"]
chunks = limit["diagnostic_chunks"]
assert 0 < len(chunks) <= 400 and len({row["rpc_id"] for row in chunks}) == len(chunks)
assert len(chunks) == limit["chunk_count"]
stages = ("rpc.request_received", "rpc.request_authorization", "rpc.handler", "rpc.send_authorization")
by_id = {row["rpc_id"]: {} for row in chunks}


def artifact(file):
    raw = file.read_bytes()
    return {"path": str(file), "bytes": len(raw), "sha256": hashlib.sha256(raw).hexdigest()}


def summary(values):
    assert values and all(math.isfinite(v) and v >= 0 for v in values)
    return {"count": len(values), "minimum_ms": min(values), "median_ms": statistics.median(values),
            "mean_ms": statistics.mean(values), "maximum_ms": max(values)}


files = sorted(trace_dir.glob("go-*.ndjson"))
assert files
for file in files:
    for line in file.read_text().splitlines():
        row = json.loads(line)
        if row.get("rpc_id") not in by_id or row.get("stage") not in stages:
            continue
        assert row["operation"] == "browser.controller.chunk" and row["role"] == "initiator"
        assert row["status"] == "ok"
        target = by_id[row["rpc_id"]]
        assert row["stage"] not in target, "duplicate helper stage for one RPC"
        target[row["stage"]] = row["duration_ms"]
assert all(set(row) == set(stages) for row in by_id.values()), "incomplete matched helper stages"

# Only the non-secret transport mode is projected from the retained profile.
profile = Path(evidence["runtime"]["profile"])
assert profile.name == "controller-native-profile" and profile.parent.name.startswith("sx-controller-iscp-")
helper_file = profile.parent / "desktop-helper.json"
helper = json.loads(helper_file.read_text())
report = {
    "schema_version": 1,
    "analysis_complete_for_observed_chunks": True,
    "native_qualification_passed": evidence["passed"],
    "scope": "one_installed_helper_run_only_not_Gateway_or_end_to_end_network_RTT",
    "authorization_duration_includes": "mutex_wait_network_and_proof_verification",
    "overlapping_durations_are_not_summed": True,
    "cross_host_timestamps_are_not_compared": True,
    "helper_mode": helper["mode"],
    "relay_profile": helper["relay_profile"],
    "negotiated_controller_limits": evidence["trace"]["diagnostic_limits"],
    "observed_chunks": len(chunks),
    "observed_raw_bytes": limit["raw_chunk_bytes"],
    "descriptor_bytes": limit["total_bytes"],
    "helper_stages": {stage: summary([row[stage] for row in by_id.values()]) for stage in stages[1:]},
    "desktop_handler": summary([row["reply_ready_ms"] - row["received_ms"] for row in chunks]),
    "execution_state": evidence["terminal_task"]["status"],
    "native_fill_calls": evidence["trace"]["fill_calls"],
    "native_evidence": artifact(evidence_file),
    "helper_configuration": artifact(helper_file),
    "helper_traces": [artifact(file) for file in files],
}
with output.open("x") as stream:
    json.dump(report, stream, indent=2)
    stream.write("\n")
output.chmod(0o600)
print(json.dumps({"analysis_complete": True, "qualification_passed": evidence["passed"],
                  "observed_chunks": len(chunks), "output": str(output)}))
