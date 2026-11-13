#!/usr/bin/env python3
"""Live Vault/read-lease acceptance over an explicitly supplied private argv.

Each invocation of the Go probe is a new process. Secrets travel only on its
private stdin; the report contains no envelope, credential or provider body.
No product ISCP, native keychain, Workflow or purchase qualification is implied.
"""
import argparse
from copy import deepcopy
import json
from pathlib import Path
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tools/ezmcd-adapter/src"))
from ezmcd.storage import write_private
from sparkclaw_mcd.canonical import canonical, loads
from sparkclaw_mcd.compatibility import member_digest
from sparkclaw_mcd.errors import AdapterError, require
from sparkclaw_mcd.official_app import acquire
from sparkclaw_mcd.sessions import issue, seal


def call(argv, request):
    process = subprocess.run(argv, input=canonical(request, 64 * 1024), capture_output=True, timeout=100)
    response = loads(process.stdout, 160 * 1024)
    require(response.get("ok") is True and process.returncode == 0,
            response.get("error", "session_probe_failed"))
    return response["result"]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--command-file", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--store-code", required=True)
    parser.add_argument("--initialize", action="store_true")
    args = parser.parse_args()
    output = args.output.absolute()
    require(not any(p.is_symlink() for p in (output, *output.parents)), "private_output_required")
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    command = json.loads(args.command_file.read_text())
    require(type(command) is list and command and all(type(v) is str and v for v in command), "invalid_command")
    report = {"schema_version": 1, "scope": "P1 Vault/read-lease foundation over authenticated private stdin; not product ISCP",
              "order_created": False, "payment_attempted": False, "product_qualified": False, "passed": False}
    base = None
    try:
        _, material = acquire()
        binding = {"deployment_id": "p1-isolated", "owner_id": "qualification", "client_id": "qualification",
                   "installation_id": uuid.uuid4().hex, "account_ref": "qualification", "member_digest": member_digest(material)}
        base = {"identity": binding, "connection_id": uuid.uuid4().hex}
        write_private(output / "control.json", base)
        envelope = issue(material, binding, base["connection_id"], uuid.uuid4().hex, 1, 1,
                         lease_ms=300000, operations=["store_get"], max_requests=8)
        original = call(command + (["-initialize"] if args.initialize else []), {**base, "operation": "provision", "envelope": envelope})
        report["vault_provisioned"] = True
        replay = call(command, {**base, "operation": "provision", "envelope": envelope})
        require(original == replay, "receipt_changed")
        report["same_id_original_receipt_after_restart"] = True

        def expect_rejection(request, expected):
            try:
                call(command, request)
            except AdapterError as error:
                require(error.code == expected, "unexpected_rejection")
                return
            raise AdapterError("negative_path_accepted")

        drift = deepcopy(envelope); drift["max_requests"] -= 1
        expect_rejection({**base, "operation": "provision", "envelope": seal(drift)}, "mcd_session_conflict")
        report["same_id_drift_rejected"] = True

        def read(value):
            execution = {k: value[k] for k in ("identity", "connection_id", "credential_revision", "grant_revision")}
            execution.update(execution_id=uuid.uuid4().hex, deadline_ms=int(time.time() * 1000) + 60000, max_requests=8)
            response = call(command, {**base, "operation": "read", "execution": execution,
                                      "tool": "store_get", "arguments": {"store_code": args.store_code}})
            require(response.get("result_encoding") == "bounded-json-v1", "read_projection_invalid")
            result = json.loads(response["result_json"])
            require(type(result) is dict and result.get("success") is not False and result.get("ok") is not False
                    and args.store_code in json.dumps(result), "target_store_not_verified")

        read(envelope)  # Request contains no credential or original envelope.
        report["fresh_process_read_without_mac_material"] = True
        renewed = issue(material, binding, base["connection_id"], uuid.uuid4().hex, 1, 2,
                        lease_ms=300000, operations=["store_get"], max_requests=8)
        call(command, {**base, "operation": "provision", "envelope": renewed})
        read(renewed)
        report["lease_only_renewal_preserves_material_digest"] = renewed["material_digest"] == envelope["material_digest"]
        expect_rejection({**base, "operation": "provision", "envelope": seal({**envelope, "provision_request_id": uuid.uuid4().hex})}, "mcd_session_conflict")
        report["old_revision_rejected"] = True
        call(command, {**base, "operation": "revoke", "request_id": "final-revoke"})
        report["revoked"] = True
        late = seal({**renewed, "grant_revision": 3, "provision_request_id": uuid.uuid4().hex})
        expect_rejection({**base, "operation": "provision", "envelope": late}, "mcd_session_closed")
        report["higher_revision_cannot_reopen_revoked_connection"] = True
        require(call(command, {**base, "operation": "provision", "envelope": envelope}) == original, "receipt_changed")
        report["historical_receipt_survives_revocation"] = True
        report["passed"] = True
    except AdapterError as error:
        report["error"] = error.code
    except Exception:
        report["error"] = "session_qualification_failed"
    finally:
        if base and not report.get("revoked"):
            try:
                call(command, {**base, "operation": "revoke", "request_id": "failure-revoke"})
                report["revoked"] = True
            except Exception:
                report["revocation_requires_reconciliation"] = True
        report["checked_at_ms"] = int(time.time() * 1000)
        write_private(output / "report.json", report)
        print(json.dumps(report, sort_keys=True))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
