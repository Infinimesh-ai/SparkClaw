#!/usr/bin/env python3
"""Offline factual checks for retained workflow attempts; no model calls.

Keyword checks are regression checks, not a semantic quality score. Inspect the
answers and source-bound evidence before accepting a case. Failed runs remain
separate from later attempts; this script never overwrites their recordings.
"""
import argparse
import hashlib
import json
from pathlib import Path
import zipfile


def inspect_archive(path):
    with zipfile.ZipFile(path) as archive:
        manifest = json.loads(archive.read("evaluation.json"))
        for payload in manifest["payloads"]:
            raw = archive.read(payload["bundle_entry"])
            if len(raw) != payload["bytes"] or hashlib.sha256(raw).hexdigest() != payload["sha256"]:
                raise ValueError("payload mismatch: " + payload["bundle_entry"])
        events = [json.loads(line) for line in archive.read("events.jsonl").splitlines()]
        if len(events) != manifest["event_count"]:
            raise ValueError("event count mismatch")
        return {"verified_payloads": len(manifest["payloads"]), "events": len(events),
                "redacted_payloads": sum(bool(p.get("redacted")) for p in manifest["payloads"]),
                "model_dispatches": sum(e["kind"] == "model.dispatch" for e in events),
                "capture": manifest["capture"], "export": manifest["export"]}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--cases", type=Path, default=Path(__file__).with_name("cases.json"))
    parser.add_argument("--runs", type=Path, nargs="+", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    cases = {case["id"]: case for case in json.loads(args.cases.read_text())}
    attempts = []
    for directory in args.runs:
        for path in sorted(directory.glob("*.json")):
            if path.stem not in cases:
                continue
            data = json.loads(path.read_text())
            case = cases[data["id"]]
            result = data["result"]
            run = result["run"]
            workflow = run.get("workflow") or {}
            facts = (workflow.get("route") or {}).get("facts") or {}
            answer = result["message"].get("content", "")
            issues = []
            for key in ("run_error", "model_calls_error", "capture_error", "export_error"):
                if data.get(key):
                    issues.append(key + ": " + data[key])
            if workflow.get("plan", {}).get("profile_id") != case["workflow"]:
                issues.append("unexpected_workflow")
            if run["state"] != "completed" and not (case.get("allow_partial") and run["state"] == "blocked"):
                issues.append("state=" + run["state"])
            for group in case.get("contains", []):
                if not any(fragment.lower() in answer.lower() for fragment in group):
                    issues.append("missing: " + " | ".join(group))
            for forbidden in case.get("not_contains", []):
                if forbidden.lower() in answer.lower():
                    issues.append("forbidden: " + forbidden)
            if case.get("edit") and not result["message"].get("attachments"):
                issues.append("missing_maintained_document")
            if not data["model_calls"] or any(call.get("mock") for call in data["model_calls"]):
                issues.append("real_model_not_proven")
            try:
                archive = inspect_archive(path.with_suffix(".zip"))
                if not archive["capture"].get("capture_complete") or not archive["capture"].get("sealed"):
                    issues.append("capture_incomplete")
            except (OSError, ValueError, KeyError, zipfile.BadZipFile) as error:
                archive = {"error": str(error)}
                issues.append("archive_verification_failed")
            attempts.append({"id": data["id"], "attempt_directory": str(directory.resolve()),
                "run_id": run["id"], "workflow_revision": workflow.get("plan", {}).get("profile_revision"),
                "state": run["state"], "scope_status": facts.get("document_read_scope_status"),
                "task_mode": facts.get("document_read_task_mode"), "scope_extent": facts.get("document_read_scope_extent"),
                "elapsed_ms": data["elapsed_ms"], "model_calls": len(data["model_calls"]),
                "tool_calls": [{"tool": c["tool"], "status": c["status"], "error": c.get("error"),
                                "coverage": (c.get("result") or {}).get("coverage")}
                               for c in result.get("tool_calls") or []],
                "checks_pass": not issues, "issues": issues, "archive": archive, "answer": answer,
                "attachments": result["message"].get("attachments")})
    report = {"semantic_review": "manual_required", "attempts": attempts,
              "total": len(attempts), "checks_pass": sum(a["checks_pass"] for a in attempts)}
    with args.output.open("x", encoding="utf-8") as handle:
        json.dump(report, handle, ensure_ascii=False, indent=2)
        handle.write("\n")
    for row in attempts:
        print(row["id"], row["state"], "checks=pass" if row["checks_pass"] else "; ".join(row["issues"]))


if __name__ == "__main__":
    main()
