#!/usr/bin/env python3
"""Read retained lab evidence and independently check both durable write records.

Usage: python3 verify-client-controller-chunk-receipts.py LAB NEW_RECEIPT_JSON [MODE ...]
Neither the app nor Gateway needs to be running. No state is changed or restored.
"""
import datetime
import hashlib
import json
from pathlib import Path
import re
import sys

lab, output = map(Path, sys.argv[1:3])
modes = sys.argv[3:] or ["out_of_order", "lost_final", "wrong_offset", "bad_hash"]
assert modes and all(mode in ["out_of_order", "lost_final", "wrong_offset", "bad_hash", "cache_full", "expired_result", "exact_limit"] for mode in modes)
assert lab.is_absolute() and lab.name.startswith("sx-controller-iscp-")
assert output.is_absolute() and not output.exists()


def digest(file):
    return hashlib.sha256(file.read_bytes()).hexdigest()


def date(value):
    # Python 3.9 accepts microseconds; Go persists nanoseconds. Keep the six
    # significant fractional digits rather than dropping the subsecond value.
    return datetime.datetime.fromisoformat(re.sub(r"(\.\d{6})\d+", r"\1", value).replace("Z", "+00:00"))


rows = []
for mode in modes:
    candidates = sorted((lab / "evidence").glob(f"controller-chunks-{mode}-*.json"), reverse=True)
    evidence_file = next(file for file in candidates if json.loads(file.read_text())["passed"])
    evidence = json.loads(evidence_file.read_text())
    fill = evidence["trace"]["fill"]
    task = evidence["task"]
    additional = {}
    control = json.loads((lab / "state/gateway-state.json.execution/control.json").read_text())
    if mode == "cache_full":
        refused = evidence["trace"]["capacity"]["refused_command"]
        assert re.fullmatch(r"[A-Za-z0-9_.:-]{1,160}", refused["command_id"])
        refused_file = lab / "state/gateway-state.json.execution/browser-control" / f"{refused['command_id']}.json"
        refusal = json.loads(refused_file.read_text())
        assert refusal["state"] == "failed" and refusal["digest"] == refused["digest"]
        assert refusal["scope"]["local_task_id"] == task["local_task_id"]
        assert not (lab / "controller-native-profile/workbench/browser-journal" / f"{refused['command_id']}.json").exists()
        assert evidence["trace"]["capacity"]["bytes"] == 20 << 20
        assert evidence["cache_recovery"]["checked_at"] >= evidence["trace"]["capacity"]["expires_at"]
        assert evidence["cache_recovery"]["expired_buffers_zeroed"] and evidence["cache_recovery"]["bytes"] == 0
        original = next(row for row in control["fences"].values() if row["request_id"] == task["request_id"])
        assert original["state"] == "unknown"
        recovered = evidence["recovery_task"]
        task = {"local_task_id": recovered["id"], "request_id": recovered["request_id"], "conversation_id": recovered["conversation_id"]}
        additional = {"capacity_refusal_gateway_failed_local_journal_absent": True, "refused_fence_sha256": digest(refused_file),
                      "original_execution_state": original["state"], "capacity_recovered_after_fixed_expiry": True}
    elif mode == "expired_result":
        replay = evidence["expired_replay"]
        assert replay["slot_expired"] and replay["payload_absent"]
        assert replay["outcome"] == replay["journal_state"] == "completed"
        assert replay["error_code"] == "browser_result_unavailable" and replay["fill_calls"] == 1
        additional = {"expired_command_slot_replay_reads_completed_journal_without_dispatch": True}
    command = fill["command_id"]
    assert re.fullmatch(r"[A-Za-z0-9_.:-]{1,160}", command)
    gateway_file = lab / "state/gateway-state.json.execution/browser-control" / f"{command}.json"
    journal_file = lab / "controller-native-profile/workbench/browser-journal" / f"{command}.json"
    gateway, journal = (json.loads(file.read_text()) for file in (gateway_file, journal_file))
    assert gateway["state"] == journal["state"] == "completed"
    assert gateway["digest"] == journal["digest"] == fill["digest"]
    assert gateway["scope"]["local_task_id"] == journal["local_task_id"] == task["local_task_id"]
    assert gateway["scope"]["local_conversation_id"] == journal["local_conversation_id"] == task["conversation_id"]
    # Both timestamps are from the same lab host (Gateway runs in its container).
    # The observer uses millisecond Date precision; allow that rounding only.
    assert date(gateway["updated_at"]) <= date(fill["first_chunk_at"]) + datetime.timedelta(milliseconds=1)
    execution = next(row for row in control["fences"].values() if row["request_id"] == task["request_id"])
    assert execution["state"] == ("delivered" if mode in ["out_of_order", "cache_full", "exact_limit"] else "unknown")
    if mode == "exact_limit":
        limit = evidence["trace"]["limit"]
        assert fill["descriptor"]["total_bytes"] == limit["raw_chunk_bytes"] == 10 << 20
        assert limit["chunk_count"] == 320 and limit["reassembled_sha256"] == fill["descriptor"]["sha256"]
        assert limit["follow_up_execute_received_ms"] >= limit["last_chunk_reply_ready_ms"]
        refused = limit["follow_up_reply"]
        assert refused["outcome"] == "not_dispatched" and refused["error_code"] == "browser_result_capacity"
        assert limit["native_executes_after_descriptor"] == 0 and not refused["local_journal_present"]
        assert re.fullmatch(r"[A-Za-z0-9_.:-]{1,160}", refused["command_id"])
        assert not (lab / "controller-native-profile/workbench/browser-journal" / f"{refused['command_id']}.json").exists()
        additional = {"exact_10_MiB_synthetic_wire_result": True, "following_read_refused_before_native_dispatch": True,
                      "workflow_completed_successfully": False}
    assert evidence["trace"]["fill_calls"] == 1 and evidence["trace"]["http_attempts"] == 0
    rows.append({"mode": mode, "evidence_path": str(evidence_file), "evidence_sha256": digest(evidence_file),
                 "bytes": fill["descriptor"]["total_bytes"], "gateway_fence_sha256": digest(gateway_file),
                 "local_journal_sha256": digest(journal_file), "command_id": command,
                 "gateway_and_local_completed": True, "same_original_task_and_digest": True,
                 "gateway_completed_before_first_chunk": True, "durable_execution_state": execution["state"],
                 "native_write_calls": evidence["trace"]["fill_calls"], **additional})

receipt = {"schema_version": 1, "passed": True, "lab_id": json.loads((lab / "run.json").read_text())["lab_id"], "checks": rows}
with output.open("x") as stream:
    json.dump(receipt, stream, indent=2)
    stream.write("\n")
output.chmod(0o600)
print(json.dumps({"passed": True, "scenarios": len(rows), "output": str(output)}))
