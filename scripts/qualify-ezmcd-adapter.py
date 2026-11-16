#!/usr/bin/env python3
"""Authorized G1/G2 probe: private subprocess channel, never a purchase.

Run with the built helper's Python. The selection command is an argv JSON
file, typically authenticated SSH to an isolated helper. It is not a shell
string. Session material is sent exclusively through the child's private stdin.
"""
from copy import deepcopy
import argparse
import json
from pathlib import Path
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tools/ezmcd-adapter/src"))

from ezmcd.storage import read_private, write_private
from sparkclaw_mcd.canonical import canonical, digest, loads
from sparkclaw_mcd.compatibility import member_digest, qualify
from sparkclaw_mcd.errors import AdapterError, require
from sparkclaw_mcd.official_app import acquire
from sparkclaw_mcd.quotes import seal
from sparkclaw_mcd.transport import provider


def call(argv, request):
    result = subprocess.run(argv, input=canonical(request, 320 * 1024), capture_output=True, timeout=95)
    try:
        response = loads(result.stdout, 320 * 1024)
    except AdapterError:
        raise AdapterError("helper_response_invalid") from None
    require(response.get("ok") is True and result.returncode == 0,
            response.get("error", "helper_process_failed") if type(response.get("error")) is str else "helper_process_failed")
    return response["result"]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True, help="Dedicated private qualification directory")
    parser.add_argument("--selection-command-file", type=Path)
    parser.add_argument("--store-code")
    parser.add_argument("--pickup-at")
    parser.add_argument("--max-amount-cny", default="20.00")
    parser.add_argument("--pickup-method", choices=("locker-in", "take-in-store"), default="locker-in")
    parser.add_argument("--pickup-selection", action="store_true", help="Explicitly authorize one non-purchase pickup-method PUT")
    args = parser.parse_args()
    root = args.output.absolute()
    require(not any(p.is_symlink() for p in (root, *root.parents)), "private_qualification_directory_required")
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    require(root.stat().st_mode & 0o077 == 0, "private_qualification_directory_required")
    host = scope = state = None
    abort_attempted = False
    report = {"schema_version": 1, "scope": "P0 authenticated-private-stdio prototype, not product ISCP",
              "order_created": False, "payment_attempted": False, "g1_purchase_qualified": False,
              "g2_handoff_passed": False, "negative_paths": {}}
    try:
        binary, material = acquire()
        report["g1"] = qualify(binary)
        local = provider(material, "qualification", int(time.time() * 1000) + 90000)
        local.wallet()
        local.http.close()
        report["g1"].update(v4_sample_verified=True, live_member_verified=True)
        if not args.selection_command_file:
            return report
        require(args.store_code and args.pickup_at, "quote_context_required")
        command = json.loads(args.selection_command_file.read_text())
        require(type(command) is list and command and all(type(v) is str and v for v in command), "invalid_helper_command")
        request = {"account": "qualification", "store_code": args.store_code, "pickup_at": args.pickup_at,
                   "max_amount_cny": args.max_amount_cny, "eat_type": args.pickup_method}
        intent = digest({"request": request, "pickup_selection": args.pickup_selection})
        state_path = root / "run-control.json"
        if state_path.exists():
            state = read_private(state_path)
            require(state["intent_digest"] == intent, "qualification_key_conflict")
            require(state["scope"]["member_digest"] == member_digest(material), "member_mismatch")
        else:
            scope = {"deployment_id": "p0-isolated", "owner_id": "qualification", "client_id": "qualification",
                     "installation_id": uuid.uuid4().hex, "account_ref": "qualification", "member_digest": member_digest(material),
                     "credential_revision": 1, "execution_id": uuid.uuid4().hex}
            state = {"scope": scope, "purchase_id": uuid.uuid4().hex, "operation_key": uuid.uuid4().hex, "intent_digest": intent}
            write_private(state_path, state)
        scope = state["scope"]
        params = {"request": request, "purchase_id": state["purchase_id"], "quote_revision": 1, "handoff_epoch": 1}
        if args.pickup_selection:
            params.update(operation_key=state["operation_key"], authorized_pickup_method=args.pickup_method)
        operation = "quote.pickup.export" if args.pickup_selection else "quote.export"
        envelope = call(command, {"operation": operation, "params": params, "scope": scope,
                                  "material": material, "deadline_ms": int(time.time() * 1000) + 90000})
        write_private(root / "received-envelope.json", envelope)
        report["complete_envelope_retained_on_mac"] = True
        report["selection_quote_produced"] = True
        report["pickup_receipt_included"] = envelope["pickup_receipt"] is not None
        report["quote_lifetime_ms"] = envelope["expires_at_ms"] - envelope["generated_at_ms"]
        report["wallet_sufficient"] = envelope["quote"]["wallet_before_cents"] >= envelope["quote"]["amount_cents"]
        ledger = root / "mac-store"
        host = [sys.executable, "-m", "sparkclaw_mcd.helper", "--role", "host", "--ledger", str(ledger)]
        initialization = [] if ledger.exists() else ["--initialize-ledger"]
        def prepare(value, key):
            nonlocal initialization
            try:
                return call(host + initialization, {"operation": "checkout.prepare", "scope": scope, "material": material,
                    "deadline_ms": int(time.time() * 1000) + 90000, "params": {"operation_key": key, "envelope": value}})
            finally:
                initialization = []
        variants = {}
        missing = deepcopy(envelope); del missing["quote"]["items"]; variants["missing_items"] = missing
        wrong = deepcopy(envelope); wrong["scope"]["member_digest"] = "0" * 64; variants["wrong_member"] = seal(wrong)
        expired = deepcopy(envelope); expired.update(generated_at_ms=1, expires_at_ms=120001); variants["expired"] = seal(expired)
        if args.pickup_selection:
            tampered = deepcopy(envelope); tampered["pickup_receipt"]["response_json"] += " "
            variants["tampered_pickup_receipt"] = seal(tampered)
        expected_errors = {"missing_items": "invalid_fields", "wrong_member": "quote_scope_mismatch",
                           "expired": "quote_expired_or_clock_untrusted", "tampered_pickup_receipt": "pickup_receipt_tampered"}
        for label, value in variants.items():
            try:
                prepare(value, "reject-" + label)
                raise AdapterError("negative_path_admitted")
            except AdapterError as error:
                require(error.code == expected_errors[label], "negative_path_wrong_rejection")
                report["negative_paths"][label] = error.code
        receipt = prepare(envelope, "prepare")
        report["import_state"] = receipt["state"]
        report["import_checkout_qualified"] = receipt["checkout_qualified"]
        report["import_checkout_blockers"] = receipt["checkout_blockers"]
        report["import_wallet_sufficient"] = receipt["wallet_sufficient"]
        require(receipt["checkout_qualified"] is False, "qualification_cannot_enable_purchasing")
        report["same_key_returns_original_receipt"] = prepare(envelope, "prepare") == receipt
        drift = deepcopy(envelope); drift["source_quote_ref"] = uuid.uuid4().hex
        try:
            prepare(seal(drift), "prepare")
            raise AdapterError("key_drift_admitted")
        except AdapterError as error:
            require(error.code == "operation_key_conflict", "key_drift_not_rejected")
            report["negative_paths"]["key_drift"] = error.code
        report["purchase_digest_matches"] = receipt["purchase_digest"] == envelope["purchase_digest"]
        report["original_expiry_preserved"] = receipt["expires_at_ms"] == envelope["expires_at_ms"]
        abort = call(host, {"operation": "checkout.abort", "scope": scope, "params": {"operation_key": "abort",
            "purchase_id": state["purchase_id"], "handoff_epoch": 1, "reason": "execution_termination"}})
        abort_attempted = True
        report["abort_receipt"] = abort["state"]
        report["g2_handoff_passed"] = all(report[k] for k in ("same_key_returns_original_receipt", "purchase_digest_matches", "original_expiry_preserved")) and abort["state"] == "aborted_before_write"
    except Exception as error:
        code = getattr(error, "code", "qualification_failed")
        report["failure"] = code if isinstance(code, str) and code.replace("_", "").isalnum() and len(code) < 120 else "qualification_failed"
    finally:
        if host is not None and not abort_attempted and (root / "mac-store").exists():
            # An assertion or transport failure after prepare still terminates
            # this exact epoch. Abort itself is durable/idempotent; it never
            # claims cancellation of an official order or an uncertain write.
            try:
                aborted = call(host, {"operation": "checkout.abort", "scope": scope, "params": {
                    "operation_key": "abort", "purchase_id": state["purchase_id"], "handoff_epoch": 1,
                    "reason": "execution_termination"}})
                report["cleanup_abort_receipt"] = aborted["state"]
            except Exception:
                report["cleanup_abort_receipt"] = "reconciliation_required"
        report["checked_at_ms"] = int(time.time() * 1000)
        write_private(root / "attempts" / (str(report["checked_at_ms"]) + "-" + uuid.uuid4().hex + ".json"), report)
        write_private(root / "report.json", report)
        print(json.dumps(report, ensure_ascii=False))
    return report


if __name__ == "__main__":
    outcome = main()
    sys.exit(0 if "failure" not in outcome else 1)
