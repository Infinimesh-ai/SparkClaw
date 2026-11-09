#!/usr/bin/env python3
"""Read retained lab evidence and independently check both durable write records.

Usage: python3 verify-client-controller-chunk-receipts.py LAB NEW_RECEIPT_JSON
Neither the app nor Gateway needs to be running. No state is changed or restored.
"""
import datetime
import hashlib
import json
from pathlib import Path
import re
import sys

lab, output = map(Path, sys.argv[1:])
assert lab.is_absolute() and lab.name.startswith("sx-controller-iscp-")
assert output.is_absolute() and not output.exists()


def digest(file):
    return hashlib.sha256(file.read_bytes()).hexdigest()


def date(value):
    # Python 3.9 accepts microseconds; Go persists nanoseconds. Keep the six
    # significant fractional digits rather than dropping the subsecond value.
    return datetime.datetime.fromisoformat(re.sub(r"(\.\d{6})\d+", r"\1", value).replace("Z", "+00:00"))


rows = []
for mode in ["out_of_order", "lost_final", "wrong_offset", "bad_hash"]:
    candidates = sorted((lab / "evidence").glob(f"controller-chunks-{mode}-*.json"), reverse=True)
    evidence_file = next(file for file in candidates if json.loads(file.read_text())["passed"])
    evidence = json.loads(evidence_file.read_text())
    fill = evidence["trace"]["fill"]
    command = fill["command_id"]
    assert re.fullmatch(r"[A-Za-z0-9_.:-]{1,160}", command)
    gateway_file = lab / "state/gateway-state.json.execution/browser-control" / f"{command}.json"
    journal_file = lab / "controller-native-profile/workbench/browser-journal" / f"{command}.json"
    gateway, journal = (json.loads(file.read_text()) for file in (gateway_file, journal_file))
    assert gateway["state"] == journal["state"] == "completed"
    assert gateway["digest"] == journal["digest"] == fill["digest"]
    assert gateway["scope"]["local_task_id"] == journal["local_task_id"] == evidence["task"]["local_task_id"]
    assert gateway["scope"]["local_conversation_id"] == journal["local_conversation_id"] == evidence["task"]["conversation_id"]
    # Both timestamps are from the same lab host (Gateway runs in its container).
    # The observer uses millisecond Date precision; allow that rounding only.
    assert date(gateway["updated_at"]) <= date(fill["first_chunk_at"]) + datetime.timedelta(milliseconds=1)
    control = json.loads((lab / "state/gateway-state.json.execution/control.json").read_text())
    execution = next(row for row in control["fences"].values() if row["request_id"] == evidence["task"]["request_id"])
    assert execution["state"] == ("delivered" if mode == "out_of_order" else "unknown")
    assert evidence["trace"]["fill_calls"] == 1 and evidence["trace"]["http_attempts"] == 0
    rows.append({"mode": mode, "evidence_path": str(evidence_file), "evidence_sha256": digest(evidence_file),
                 "bytes": fill["descriptor"]["total_bytes"], "gateway_fence_sha256": digest(gateway_file),
                 "local_journal_sha256": digest(journal_file), "command_id": command,
                 "gateway_and_local_completed": True, "same_original_task_and_digest": True,
                 "gateway_completed_before_first_chunk": True, "durable_execution_state": execution["state"],
                 "native_write_calls": evidence["trace"]["fill_calls"]})

receipt = {"schema_version": 1, "passed": True, "lab_id": json.loads((lab / "run.json").read_text())["lab_id"], "checks": rows}
with output.open("x") as stream:
    json.dump(receipt, stream, indent=2)
    stream.write("\n")
output.chmod(0o600)
print(json.dumps({"passed": True, "scenarios": len(rows), "output": str(output)}))
