#!/usr/bin/env python3
"""Independently verify retained unknown fences after packaged journal failure.

Usage: python3 verify-client-controller-journal-receipts.py LAB NEW_RECEIPT_JSON
Run with both application and lab Gateway stopped; reads never change their data.
"""
import hashlib
import json
from pathlib import Path
import re
import sys

lab, output = map(Path, sys.argv[1:])
assert lab.is_absolute() and lab.name.startswith("sx-controller-iscp-")
assert output.is_absolute() and not output.exists()
files = sorted((lab / "evidence").glob("controller-journal-failure-*.json"), reverse=True)
evidence_file = next(file for file in files if json.loads(file.read_text())["passed"])
evidence = json.loads(evidence_file.read_text())
command = evidence["trace"]["command"]
assert re.fullmatch(r"[A-Za-z0-9_.:-]{1,160}", command["command_id"])
gateway_file = lab / "state/gateway-state.json.execution/browser-control" / f"{command['command_id']}.json"
journal_file = lab / "controller-native-profile/workbench/browser-journal" / f"{command['command_id']}.json"
gateway, journal = (json.loads(file.read_text()) for file in (gateway_file, journal_file))
assert gateway["state"] == journal["state"] == "unknown"
assert gateway["digest"] == journal["digest"] == command["digest"]
for key in ["owner_id", "client_id", "installation_id", "local_conversation_id", "local_task_id"]:
    assert gateway["scope"][key] == journal[key] == command["binding"][key]
assert journal["local_task_id"] == evidence["task"]["local_task_id"]
assert journal["local_conversation_id"] == evidence["task"]["conversation_id"]
control = json.loads((lab / "state/gateway-state.json.execution/control.json").read_text())
execution = next(row for row in control["fences"].values() if row["request_id"] == evidence["task"]["request_id"])
assert execution["state"] == "unknown"
assert evidence["cold_dispatched"]["state"] == "dispatched"
assert evidence["final_unknown"] == journal
assert evidence["trace"]["effect"] == {"value": "Alice", "inputs": 1}
assert sum(row.get("operation") == "page.fill" for row in evidence["trace"]["native_calls"]) == 1
assert evidence["trace"]["finish_attempts"] == ["completed", "unknown"]
assert evidence["reopened"]["native_calls"] == []
assert evidence["trace"]["business_http"] == evidence["reopened"]["business_http"] == 0
assert evidence["before_restart_probe"] == evidence["after_restart_probe"] == {
    "outcome": "not_dispatched", "error_code": "browser_write_outcome_unknown", "calls_added": 0}


def digest(file):
    return hashlib.sha256(file.read_bytes()).hexdigest()


receipt = {"schema_version": 1, "passed": True, "lab_id": json.loads((lab / "run.json").read_text())["lab_id"],
           "evidence_path": str(evidence_file), "evidence_sha256": digest(evidence_file),
           "command_id": command["command_id"], "gateway_fence_sha256": digest(gateway_file),
           "local_journal_sha256": digest(journal_file), "both_writes_and_execution_unknown": True,
           "same_command_digest_and_original_task": True, "native_write_calls": 1, "reopened_native_calls": 0}
with output.open("x") as stream:
    output.chmod(0o600)
    stream.write(json.dumps(receipt, indent=2) + "\n")
print(json.dumps({"passed": True, "output": str(output)}))
