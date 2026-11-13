#!/usr/bin/env python3
"""Read-only G1 check of the original imported account; never changes policy."""
import argparse
import hashlib
import json
from pathlib import Path
import sqlite3
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tools/ezmcd-adapter/src"))
from ezmcd.runtime_check import check_runtime
from ezmcd.storage import write_private
from ezmcd.errors import EzMcdError
from sparkclaw_mcd.compatibility import material_from_account, recognized_build
from sparkclaw_mcd.imported_account import open_existing
from sparkclaw_mcd.transport import provider
from sparkclaw_mcd.errors import AdapterError, require


def ledger_digest(root):
    with sqlite3.connect((root / "journal.sqlite3").as_uri() + "?mode=ro", uri=True) as db:
        # Logical snapshot includes original keys, quotes, claims and receipts;
        # no values are printed or copied into the report.
        return hashlib.sha256("\n".join(db.iterdump()).encode()).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--account-root", type=Path, required=True)
    parser.add_argument("--alias", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    require(not args.output.exists(), "qualification_report_already_exists")
    report = {"schema_version": 1, "scope": "current-build v4/v5 read-only G1; not product checkout qualification",
              "order_created": False, "payment_attempted": False, "product_qualified": False, "passed": False}
    try:
        store, record = open_existing(args.account_root, args.alias)
        account_file = store.root / "accounts" / (record["identity"] + ".json")
        before_account = hashlib.sha256(account_file.read_bytes()).digest()
        before_ledger = ledger_digest(store.root)
        material = material_from_account(record)
        live = provider(material, args.alias, int(time.time()*1000)+60000, requests=4)
        try:
            live.wallet()
            report["live_v4_member_verified"] = True
        finally:
            live.http.close()
        checked = check_runtime(store, args.alias)
        report["live_v5_read_verified"] = checked["read_only_v5_verified"]
        report["observation_expired_by_project_policy"] = checked["observation_expired_by_project_policy"]
        report["observation_time_changed"] = checked["observation_time_changed"]
        report["binary_sha256"] = recognized_build(record["binary"])["sha256"]
        report["account_unchanged"] = before_account == hashlib.sha256(account_file.read_bytes()).digest()
        report["original_ledger_unchanged"] = before_ledger == ledger_digest(store.root)
        report["passed"] = all(report[k] for k in ("live_v4_member_verified", "live_v5_read_verified", "account_unchanged", "original_ledger_unchanged"))
    except AdapterError as error:
        report["error"] = error.code
    except EzMcdError:
        report["error"] = "upstream_readonly_check_failed"
    except Exception:
        report["error"] = "handoff_qualification_failed"
    report["checked_at_ms"] = int(time.time()*1000)
    write_private(args.output.absolute(), report)
    print(json.dumps(report, sort_keys=True))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
