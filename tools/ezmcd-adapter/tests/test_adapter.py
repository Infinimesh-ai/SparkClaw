from copy import deepcopy
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

from ezmcd.demo import DemoProvider
from ezmcd.errors import EzMcdError
from ezmcd.models import CheckoutInput, fingerprint
from ezmcd.pickup import selected_quote
from ezmcd.storage import Store
from ezmcd.transport import Transport

from sparkclaw_mcd.canonical import canonical, digest, loads
from sparkclaw_mcd.compatibility import BUILDS, validate_material, member_digest
from sparkclaw_mcd.errors import AdapterError
from sparkclaw_mcd.helper import dispatch
from sparkclaw_mcd.ledger import PrepareLedger
from sparkclaw_mcd.quotes import export_quote, export_stored_quote, validate, seal
from sparkclaw_mcd.transport import read_endpoint
from sparkclaw_mcd.pickup_probe import export_pickup
from sparkclaw_mcd.pickup_control import retire_pickup
from sparkclaw_mcd.receipt import decode_receipt, encode_receipt
from sparkclaw_mcd.reads import read


class FixtureProvider(DemoProvider):
    """Synthetic official-shaped data, injected in tests only; no wire flag."""
    def __init__(self, account, store):
        super().__init__(account, store)
        self.read_count = 0

    def _checkout_data(self, args):
        self.read_count += 1
        return super()._checkout_data(args)


class AdapterTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.now = int(time.time() * 1000)
        self.scope = {"deployment_id": "deployment", "owner_id": "owner", "client_id": "client",
                      "installation_id": "mac", "account_ref": "account", "member_digest": "1" * 64,
                      "credential_revision": 1, "execution_id": "execution"}
        self.installation = {k: self.scope[k] for k in ("deployment_id", "owner_id", "client_id", "installation_id")}
        self.source = Store(self.root / "linux-temporary")
        self.account = {"alias": "account", "identity": "1" * 64, "provider": "mcd-cn"}
        self.provider = FixtureProvider(self.account, self.source)
        self.request = {"account": "account", "store_code": "9000001",
                        "pickup_at": (datetime.now(timezone.utc) + timedelta(days=1)).replace(second=0, microsecond=0).isoformat(),
                        "max_amount_cny": "20.00", "eat_type": "locker-in"}
        self.envelope = export_quote(self.provider, self.request, self.scope, "purchase", 1, 1, clock=lambda: self.now / 1000)
        self.ledger = PrepareLedger(self.root / "mac", self.installation, initialize=True, clock=lambda: self.now / 1000)
        self.no_network = patch.object(Transport, "_open", side_effect=AssertionError("No real network in fixtures"))
        self.no_network.start()
        self.addCleanup(self.no_network.stop)

    def import_quote(self, envelope=None, key="prepare"):
        return self.ledger.prepare(self.scope, key, envelope or self.envelope, self.provider)

    def test_adopt_original_ledger_preserves_prior_paid_unknown_and_write_claims(self):
        original = Store(self.root / "original-host")
        original.add_account({**self.account, "policy": {"allow_checkout": False}})
        with original.connect() as conn:
            for n, state in enumerate(("PAID", "UNKNOWN")):
                conn.execute("INSERT INTO attempts VALUES (?,?,?,?,?,?,?,?)", (f"attempt-{n}", self.scope["member_digest"], f"key-{n}", f"intent-{n}", "2026-11-13", 1490, state, json.dumps({"state":state})))
                conn.execute("INSERT INTO writes VALUES (?,?,?,?)", (f"attempt-{n}", "consume", 1, None))
            before = {table: [tuple(r) for r in conn.execute(f"SELECT * FROM {table}")] for table in ("accounts", "attempts", "writes")}
        ledger = PrepareLedger.adopt_existing(original.root, "account", self.installation, initialize=True)
        self.assertEqual(ledger.store.root, original.root)
        with original.connect() as conn:
            after = {table: [tuple(r) for r in conn.execute(f"SELECT * FROM {table}")] for table in before}
        self.assertEqual(before, after)
        PrepareLedger.adopt_existing(original.root, "account", self.installation)
        (original.root / "installation.json").unlink()
        with self.assertRaisesRegex(AdapterError, "missing_recovery"):
            PrepareLedger.adopt_existing(original.root, "account", self.installation)
        with self.assertRaisesRegex(AdapterError, "partial_initialization"):
            PrepareLedger.adopt_existing(original.root, "account", self.installation, initialize=True)
        with original.connect() as conn:
            self.assertEqual(conn.execute("SELECT count(*) FROM writes").fetchone()[0], 2)

    def test_import_new_store_and_cold_retry_preserve_local_ref_and_expiry(self):
        with self.ledger.store.connect() as conn:
            self.assertEqual(conn.execute("SELECT count(*) FROM quotes").fetchone()[0], 0)
        result = self.import_quote()
        self.assertNotEqual(result["local_quote_ref"], self.envelope["source_quote_ref"])
        self.assertEqual(result["expires_at_ms"], self.envelope["expires_at_ms"])
        local = self.ledger.store.quote(self.scope["member_digest"], result["local_quote_ref"])
        self.assertEqual(local["body"], self.envelope["quote"]["body"])
        self.assertFalse(result["checkout_qualified"])
        reads = self.provider.read_count
        cold = PrepareLedger(self.root / "mac", self.installation, clock=lambda: self.now / 1000 + 1000)
        self.assertEqual(cold.prepare(self.scope, "prepare", self.envelope, self.provider), result)
        self.assertEqual(self.provider.read_count, reads)
        with self.ledger.store.connect() as conn:
            self.assertEqual(conn.execute("SELECT count(*) FROM attempts").fetchone()[0], 0)
            self.assertEqual(conn.execute("SELECT count(*) FROM writes").fetchone()[0], 0)
            self.assertEqual(conn.execute("SELECT count(*) FROM quotes").fetchone()[0], 1)

    def test_same_epoch_new_key_returns_same_local_reference(self):
        first = self.import_quote()
        self.assertEqual(self.import_quote(key="another-key"), first)

    def test_operation_key_drift_and_member_or_owner_mismatch_rejected(self):
        self.import_quote()
        drift = deepcopy(self.envelope)
        drift["source_quote_ref"] = "changed"
        with self.assertRaisesRegex(AdapterError, "operation_key_conflict"):
            self.import_quote(seal(drift))
        for field in ("owner_id", "client_id", "installation_id", "member_digest", "credential_revision", "execution_id"):
            changed = deepcopy(self.envelope)
            changed["scope"][field] = 2 if field == "credential_revision" else "2" * 64
            with self.subTest(field=field), self.assertRaisesRegex(AdapterError, "quote_scope_mismatch"):
                self.import_quote(seal(changed), key="other")

    def test_missing_fields_tampering_simulation_and_payment_channels_fail_before_reads(self):
        cases = []
        q = deepcopy(self.envelope); del q["quote"]["items"]; cases.append(q)
        q = deepcopy(self.envelope); q["quote"]["simulation"] = True; cases.append(seal(q))
        q = deepcopy(self.envelope); q["payment_method"] = "ALIPAY"; cases.append(seal(q))
        q = deepcopy(self.envelope); q["critical_future_field"] = True; cases.append(q)
        q = deepcopy(self.envelope); q["envelope_digest"] = "0" * 64; cases.append(q)
        q = deepcopy(self.envelope); q["quote"]["body"]["realTotalAmount"] = "1.00"; cases.append(seal(q))
        reads = self.provider.read_count
        for q in cases:
            with self.subTest(keys=list(q)), self.assertRaises((AdapterError, KeyError)):
                self.import_quote(q)
        self.assertEqual(reads, self.provider.read_count)

    def test_lifetime_120_seconds_clock_reversal_and_network_time(self):
        for when in (self.now - 1, self.now + 120000, self.now + 120001):
            with self.subTest(when=when), self.assertRaisesRegex(AdapterError, "quote_expired_or_clock_untrusted"):
                validate(self.envelope, self.scope, when)
        validate(self.envelope, self.scope, self.now + 119999)
        changed = deepcopy(self.envelope); changed["expires_at_ms"] += 1
        with self.assertRaisesRegex(AdapterError, "quote_expired_or_clock_untrusted"):
            self.import_quote(seal(changed))
        clock = iter([self.now, self.now, self.now + 120000])
        self.ledger.clock = lambda: next(clock) / 1000
        with self.assertRaisesRegex(AdapterError, "quote_expired_or_clock_untrusted"):
            self.import_quote()
        with self.ledger.store.connect() as conn:
            self.assertEqual(conn.execute("SELECT count(*) FROM quotes").fetchone()[0], 0)

    def test_purchase_digest_excludes_only_refresh_metadata(self):
        old = self.envelope["purchase_digest"]
        fresh = deepcopy(self.envelope)
        fresh.update(source_quote_ref="new", quote_revision=2, handoff_epoch=2,
                     generated_at_ms=self.now + 1, expires_at_ms=self.now + 120001)
        fresh["quote"]["wallet_before_cents"] += 200
        self.assertEqual(seal(fresh)["purchase_digest"], old)
        for field, value in (("amount_cents", 900), ("regular_cents", 1100), ("discount_names", ["coupon"]),
                             ("items", []), ("body", {}), ("intent", "2" * 64), ("store_code", "9000002"),
                             ("eat_type", "take-in-store"), ("pickup_at", "2099-01-01T10:00:00+08:00")):
            fresh = deepcopy(self.envelope); fresh["quote"][field] = value
            self.assertNotEqual(seal(fresh)["purchase_digest"], old, field)

    def test_cart_change_stops_import_and_insufficient_balance_blocks_purchasing(self):
        self.provider.write_cart({"quantity": 2})  # Offline fixture mutation only.
        with self.assertRaisesRegex(AdapterError, "cart_changed_requote"):
            self.import_quote()
        self.provider.write_cart({"quantity": 1})
        with patch.object(self.provider, "wallet", return_value={"balance_cents": 0}):
            receipt = self.import_quote()
        self.assertFalse(receipt["wallet_sufficient"])
        self.assertFalse(receipt["checkout_qualified"])
        self.assertIn("insufficient_wallet_balance", receipt["checkout_blockers"])
        self.assertEqual(self.import_quote(key="another-key"), receipt)
        quote = self.ledger.store.quote(self.scope["member_digest"], receipt["local_quote_ref"])
        self.assertEqual(quote["wallet_before_cents"], 0)
        self.assertEqual(quote["checkout_blockers"], receipt["checkout_blockers"])
        self.assertEqual(self.ledger.abort(self.scope, "abort", "purchase", 1, "execution_termination")["state"], "aborted_before_write")

    def test_abort_before_prepare_is_durable_and_does_not_claim_order_cancellation(self):
        receipt = self.ledger.abort(self.scope, "abort", "purchase", 1, "execution_termination")
        self.assertEqual(receipt["state"], "aborted_before_write")
        self.assertFalse(receipt["official_order_canceled"])
        cold = PrepareLedger(self.root / "mac", self.installation, clock=lambda: self.now / 1000)
        self.assertEqual(cold.abort(self.scope, "abort", "purchase", 1, "execution_termination"), receipt)
        with self.assertRaisesRegex(AdapterError, "handoff_epoch_aborted"):
            cold.prepare(self.scope, "prepare", self.envelope, self.provider)
        self.assertEqual(cold.status(self.scope, "missing"), {"state": "not_found", "non_dispatch_proven": False})

    def test_claimed_or_unknown_preparation_never_aborts(self):
        self.import_quote()
        with self.ledger.store.connect() as conn:
            conn.execute("UPDATE mcd_preparations SET state='unknown',write_claim=1")
        result = self.ledger.abort(self.scope, "abort", "purchase", 1, "user_abandonment")
        self.assertEqual(result["state"], "reconciliation_required")
        with self.ledger.store.connect() as conn:
            self.assertEqual(conn.execute("SELECT state FROM mcd_preparations").fetchone()[0], "unknown")

    def test_member_aliases_share_freeze_and_refresh_requires_abort(self):
        self.import_quote()
        fresh = deepcopy(self.envelope); fresh.update(quote_revision=2, handoff_epoch=2)
        with self.assertRaisesRegex(AdapterError, "member_preparation_unresolved"):
            self.import_quote(seal(fresh), key="new")
        self.ledger.abort(self.scope, "abort", "purchase", 1, "quote_refresh")
        result = self.import_quote(seal(fresh), key="new")
        self.assertEqual(result["handoff_epoch"], 2)
        self.ledger.abort(self.scope, "abort2", "purchase", 2, "quote_refresh")
        fresh.update(quote_revision=3, handoff_epoch=3)
        self.import_quote(seal(fresh), key="new3")
        self.ledger.abort(self.scope, "abort3", "purchase", 3, "quote_refresh")
        fresh.update(quote_revision=4, handoff_epoch=4)
        with self.assertRaisesRegex(AdapterError, "quote_refresh_limit_exceeded"):
            self.import_quote(seal(fresh), key="new4")

    def test_established_ledger_loss_corruption_binding_and_capacity_fail_closed(self):
        with self.assertRaisesRegex(AdapterError, "ledger_already_initialized"):
            PrepareLedger(self.root / "mac", self.installation, initialize=True)
        with self.assertRaisesRegex(AdapterError, "ledger_binding_mismatch"):
            PrepareLedger(self.root / "mac", self.installation | {"installation_id": "other"})
        (self.root / "mac").chmod(0o755)
        with self.assertRaisesRegex(AdapterError, "ledger_missing_recovery_required"):
            PrepareLedger(self.root / "mac", self.installation)
        (self.root / "mac").chmod(0o700)
        (self.root / "mac/journal.sqlite3").unlink()
        with self.assertRaisesRegex(AdapterError, "ledger_missing_recovery_required"):
            PrepareLedger(self.root / "mac", self.installation)

    def test_abandonment_closes_purchase_across_new_epochs(self):
        self.ledger.abort(self.scope, "abort", "purchase", 1, "user_abandonment")
        new = deepcopy(self.envelope); new.update(quote_revision=2, handoff_epoch=2)
        with self.assertRaisesRegex(AdapterError, "purchase_intent_closed"):
            self.import_quote(seal(new))

    def test_prepare_abort_process_lock_and_transaction_failure_preserve_fence(self):
        with self.ledger.store.lock(self.scope["member_digest"]):
            with self.assertRaisesRegex(EzMcdError, "account_busy"):
                self.ledger.abort(self.scope, "abort", "purchase", 1, "user_abandonment")
        with patch.object(self.ledger, "_record", side_effect=AdapterError("disk_full")):
            with self.assertRaisesRegex(AdapterError, "disk_full"):
                self.import_quote()
        with self.ledger.store.connect() as conn:
            self.assertEqual(conn.execute("SELECT count(*) FROM quotes").fetchone()[0], 0)
            self.assertEqual(conn.execute("SELECT count(*) FROM mcd_preparations").fetchone()[0], 0)
        self.import_quote()

    def test_unknown_pickup_never_replays_same_or_new_key(self):
        control = self.root / "control"; control.mkdir(mode=0o700)
        request = self.request | {"eat_type": "take-in-store"}
        with patch.object(self.provider, "switch_pickup", side_effect=EzMcdError("transport_unknown_do_not_retry_writes")) as switch:
            with self.assertRaises(EzMcdError):
                export_pickup(self.provider, request, self.scope, "purchase", 1, 1, "switch", control)
            for key, error in (("switch", "pickup_operation_requires_original_receipt"), ("other", "pickup_operation_unresolved")):
                with self.assertRaisesRegex(AdapterError, error):
                    export_pickup(self.provider, request, self.scope, "purchase", 1, 1, key, control)
            self.assertEqual(switch.call_count, 1)
        with sqlite3.connect(control / "pickup-fences.sqlite3") as conn:
            data = conn.execute("SELECT * FROM fences").fetchone()
            self.assertEqual(data[-1], "unknown")
            self.assertNotIn("9000001", json.dumps(data))

    def test_expired_pickup_retirement_preserves_unknown_and_never_replays_original(self):
        control = self.root / "retirement"; control.mkdir(mode=0o700)
        request = self.request | {"eat_type": "take-in-store"}
        with patch.object(self.provider, "switch_pickup", side_effect=EzMcdError("transport_unknown_do_not_retry_writes")) as switch:
            with self.assertRaises(EzMcdError):
                export_pickup(self.provider, request, self.scope, "purchase", 1, 1, "switch", control)
            with self.assertRaisesRegex(AdapterError, "pickup_intent_not_expired"):
                retire_pickup(request, self.scope, "purchase", 1, 1, "switch", control)
            with self.assertRaisesRegex(AdapterError, "operation_key_conflict"):
                retire_pickup(request | {"max_amount_cny": "19.00"}, self.scope, "purchase", 1, 1, "switch", control)
            expired = datetime.fromisoformat(request["pickup_at"]).timestamp() + 1
            receipt = retire_pickup(request, self.scope, "purchase", 1, 1, "switch", control, clock=lambda: expired)
            self.assertEqual(receipt["original_state"], "unknown")
            self.assertFalse(receipt["retirement_confirms_upstream_outcome"])
            self.assertEqual(retire_pickup(request, self.scope, "purchase", 1, 1, "switch", control, clock=lambda: expired + 1), receipt)
            with self.assertRaisesRegex(AdapterError, "pickup_operation_requires_original_receipt"):
                export_pickup(self.provider, request, self.scope, "purchase", 1, 1, "switch", control, clock=lambda: expired)
            with self.assertRaisesRegex(AdapterError, "pickup_time_has_passed"):
                export_pickup(self.provider, request, self.scope, "purchase", 1, 1, "another-key", control, clock=lambda: expired)
            self.assertEqual(switch.call_count, 1)
        with sqlite3.connect(control / "pickup-fences.sqlite3") as conn:
            self.assertEqual(conn.execute("SELECT state FROM fences").fetchone()[0], "unknown")
            self.assertEqual(conn.execute("SELECT count(*) FROM retirements").fetchone()[0], 1)
        new_request = request | {"pickup_at": (datetime.fromtimestamp(expired, timezone.utc) + timedelta(hours=1)).replace(second=0, microsecond=0).isoformat()}
        result = export_pickup(self.provider, new_request, self.scope, "new-purchase", 1, 2, "new-intent", control, clock=lambda: expired)
        self.assertEqual(result["quote"]["pickup_at"], CheckoutInput.model_validate(new_request).pickup_at)
        (control / "pickup-fences.sqlite3").unlink()
        with self.assertRaisesRegex(AdapterError, "pickup_control_missing_recovery_required"):
            retire_pickup(request, self.scope, "purchase", 1, 1, "switch", control, clock=lambda: expired)

    def test_qualification_failure_after_prepare_still_aborts_and_retains_attempt(self):
        import contextlib
        import importlib.util
        import io
        from types import SimpleNamespace
        script = Path(__file__).resolve().parents[3] / "scripts/qualify-ezmcd-adapter.py"
        spec = importlib.util.spec_from_file_location("qualify_mcd_test", script)
        runner = importlib.util.module_from_spec(spec); spec.loader.exec_module(runner)
        root = self.root / "runner"
        command = self.root / "command.json"; command.write_text('["synthetic-selection"]')
        prepared, aborted = [], []
        def call(argv, request):
            if request["operation"] == "quote.export":
                return deepcopy(self.envelope)
            if request["operation"] == "checkout.abort":
                aborted.append(request)
                return {"state": "aborted_before_write"}
            (root / "mac-store").mkdir(exist_ok=True)
            key = request["params"]["operation_key"]
            errors = {"reject-missing_items": "invalid_fields", "reject-wrong_member": "quote_scope_mismatch",
                      "reject-expired": "quote_expired_or_clock_untrusted"}
            if key in errors: raise AdapterError(errors[key])
            prepared.append(request)
            if len(prepared) > 2: raise AdapterError("injected_failure")
            return {"state": "prepared", "purchase_digest": self.envelope["purchase_digest"],
                    "expires_at_ms": self.envelope["expires_at_ms"], "checkout_qualified": False,
                    "checkout_blockers": ["split_integration_not_qualified"], "wallet_sufficient": True}
        fake = SimpleNamespace(wallet=lambda: None, http=SimpleNamespace(close=lambda: None))
        argv = [str(script), "--output", str(root), "--selection-command-file", str(command),
                "--store-code", "9000001", "--pickup-at", self.request["pickup_at"]]
        with patch.object(sys, "argv", argv), patch.object(runner, "acquire", return_value=("synthetic", {})), \
             patch.object(runner, "qualify", return_value={}), patch.object(runner, "provider", return_value=fake), \
             patch.object(runner, "member_digest", return_value="1" * 64), patch.object(runner, "call", side_effect=call), \
             contextlib.redirect_stdout(io.StringIO()):
            report = runner.main()
        self.assertEqual(report["failure"], "key_drift_not_rejected")
        self.assertEqual(report["cleanup_abort_receipt"], "aborted_before_write")
        self.assertEqual(len(aborted), 1)
        self.assertEqual(len(list((root / "attempts").glob("*.json"))), 1)
        self.assertFalse(report["g2_handoff_passed"])

    def test_fractional_receipt_metadata_survives_complete_handoff(self):
        control = self.root / "control"; control.mkdir(mode=0o700)
        original = self.provider.switch_pickup
        def response(body):
            result = original(body)
            result["response"]["data"]["confirmInfo"]["metadata"] = {"latitude": 30.123456, "version": 1.0}
            return result
        with patch.object(self.provider, "switch_pickup", side_effect=response):
            envelope = export_pickup(self.provider, self.request | {"eat_type": "take-in-store"}, self.scope,
                                     "purchase", 1, 1, "switch", control, clock=lambda: self.now / 1000)
        raw = decode_receipt(envelope["pickup_receipt"]["response_json"])
        self.assertEqual(raw["response"]["data"]["confirmInfo"]["metadata"]["latitude"], 30.123456)
        self.assertEqual(self.import_quote(envelope)["state"], "prepared")
        with self.assertRaisesRegex(AdapterError, "pickup_operation_requires_original_receipt"):
            export_pickup(self.provider, self.request | {"eat_type": "take-in-store"}, self.scope,
                          "purchase", 1, 1, "switch", control, clock=lambda: self.now / 1000)

    def test_pickup_quote_preserves_one_shot_receipt_and_default_live_method(self):
        args = CheckoutInput.model_validate(self.request | {"eat_type": "take-in-store"})
        prepared = self.provider.prepare_pickup(args)
        receipt = self.provider.switch_pickup(prepared["body"])
        cart, _, _ = self.provider._checkout_data(args)
        q = selected_quote(args, prepared, cart, receipt)
        q.update(request=args.model_dump(), quote_id="source-pickup", account="account",
                 expires_at=(self.now + 120000) / 1000, wallet_before_cents=10000, simulation=False)
        self.source.save_quote(self.scope["member_digest"], q)
        record = {"state": "READY", "quote_id": q["quote_id"], "operation_id": "pickup-op",
                  "created_at": self.now / 1000, "request": args.model_dump(), "prepared": prepared}
        with self.source.connect() as conn:
            conn.execute("INSERT INTO pickup_quotes VALUES (?,?,?,?,?,?,?)", ("pickup-op", self.scope["member_digest"], "cart-ref", "scope", self.now / 1000, 1, json.dumps(record)))
            conn.execute("INSERT INTO writes VALUES (?,?,?,?)", ("pickup-op", "pickup_quote", self.now / 1000, json.dumps(receipt)))
        envelope = export_stored_quote(self.source, self.scope["member_digest"], q["quote_id"], self.scope, "purchase", 1, 1, now_ms=self.now)
        # The normal live quote still defaults to locker-in; no replacement PUT.
        result = self.import_quote(envelope)
        imported = self.ledger.store.quote(self.scope["member_digest"], result["local_quote_ref"])
        self.assertEqual(imported["eat_type"], "take-in-store")
        self.assertEqual(imported["pickup_selection"], q["pickup_selection"])
        self.ledger.abort(self.scope, "abort", "purchase", 1, "execution_termination")
        tampered = deepcopy(envelope); tampered["pickup_receipt"]["response_json"] += " "
        tampered.update(handoff_epoch=2, quote_revision=2)
        with self.assertRaisesRegex(AdapterError, "pickup_receipt_tampered"):
            self.import_quote(seal(tampered), key="tamper")


class BoundaryTests(unittest.TestCase):
    def test_read_projection_preserves_fractional_metadata_and_bounds_result(self):
        from types import SimpleNamespace
        result = {"latitude": 30.123456, "longitude": 120.123456, "balance_cents": 1490}
        provider = SimpleNamespace(wallet=lambda: None, store=lambda _: result, account={"alias": "account"})
        value = read(provider, "store_get", {"store_code": "9000001"})
        self.assertEqual(json.loads(value["result_json"]), result)
        self.assertEqual(loads(canonical(value)), value)
        result["extra"] = "x" * (64 * 1024)
        with self.assertRaisesRegex(AdapterError, "official_json_too_large"):
            read(provider, "store_get", {"store_code": "9000001"})

    def test_shared_canonical_vectors(self):
        vectors = json.loads(Path(__file__).with_name("canonical-vectors.json").read_text())
        for vector in vectors:
            self.assertEqual(seal(vector["envelope"]), vector["envelope"], vector["name"])

    def test_opaque_receipt_rejects_duplicates_nonfinite_and_business_failure(self):
        for raw in ('{"http_status":200,"response":{"success":true,"success":false}}',
                    '{"http_status":200,"response":{"success":true,"x":NaN}}',
                    '{"http_status":200,"response":{"success":false}}'):
            with self.subTest(raw=raw), self.assertRaises(AdapterError): decode_receipt(raw)
        with self.assertRaises(AdapterError):
            encode_receipt({"http_status": 200, "response": {"success": True, "data": "x" * 131072}})

    def test_v4_synthetic_vector_and_v5_explicit_runtime_vector(self):
        from ezmcd.protocol.signing import sign_v4
        from ezmcd.protocol.v5 import ObservedRuntime, encode, decode_verified_sample
        headers = {"sid": "synthetic-session", "token": "synthetic-device", "sv": "v4", "v": "test", "ct": "101"}
        sig = sign_v4(headers, "test-ak", "test-secret", datetime(2026,1,1,tzinfo=timezone.utc), "GET", "/test", "a=1", b"{}")
        self.assertIn("f0QciLQ+jMxJW9GBjeJRH1zZopwJTwKnDfW4Di0cBYk=", sig["authorization"])
        runtime = ObservedRuntime("A" * 32, "b" * 16)
        constants = tuple(range(64))  # Deliberately synthetic, never official signing material.
        signature = encode("/bff/order/orders", runtime, milliseconds=1700000000000, random_value=1, counter=1, constants=constants)
        restored, _ = decode_verified_sample("/bff/order/orders", signature, constants)
        self.assertEqual(restored, runtime)

    def test_inventory_matches_all_pinned_upstream_tools_and_annotations(self):
        import ast
        import ezmcd
        from sparkclaw_mcd.helper import UPSTREAM_ERRORS
        from sparkclaw_mcd.reads import READERS
        import sparkclaw_mcd
        root = Path(ezmcd.__file__).parent
        tree = ast.parse((root / "mcp_server.py").read_text())
        names = {n.name for n in ast.walk(tree) if isinstance(n, ast.FunctionDef)
                 and any(isinstance(d, ast.Call) and isinstance(d.func, ast.Attribute) and d.func.attr == "tool" for d in n.decorator_list)}
        inventory = json.loads(Path(sparkclaw_mcd.__file__).with_name("tool-inventory.json").read_text())["tools"]
        self.assertEqual({row["name"] for row in inventory}, names)
        self.assertEqual(len(inventory), 38)
        self.assertTrue(all(not row["public_model_exposure"] for row in inventory))
        self.assertEqual(set(READERS), {r["name"] for r in inventory if r["classification"] == "external_read"})
        codes = {n.args[0].value for p in root.rglob("*.py") for n in ast.walk(ast.parse(p.read_text()))
                 if isinstance(n, ast.Call) and isinstance(n.func, ast.Name) and n.func.id == "EzMcdError"
                 and n.args and isinstance(n.args[0], ast.Constant) and isinstance(n.args[0].value, str)}
        self.assertEqual(codes, UPSTREAM_ERRORS)
    def test_canonical_is_strict_and_deterministic(self):
        self.assertEqual(canonical({"z": [None, True, "汉堡"], "a": 1490}), '{"a":1490,"z":[null,true,"汉堡"]}'.encode())
        for value in (b'{"a":1,"a":2}', b'{"a":NaN}', b'{"a":1.0}', b'{"a":9007199254740992}', b'{"a":"\\ud800"}'):
            with self.subTest(value=value), self.assertRaises(AdapterError):
                loads(value)
        with self.assertRaisesRegex(AdapterError, "payload_too_large"):
            canonical({"value": "x" * (256 * 1024)})

    def test_no_order_cashier_token_refresh_or_arbitrary_url_on_selection(self):
        for method, path in (("POST", "/bff/order/orders"), ("GET", "/bff/member/mwallet/token"),
                             ("GET", "/bff/cashier/channels"), ("POST", "/bff/passport/login/session/refresh"),
                             ("GET", "https://api.mcd.cn/bff/cart/carts"), ("GET", "/bff/order/orders"),
                             ("PUT", "/bff/cart/carts"), ("PUT", "/bff/order/confirmation/eattype/switch")):
            with self.subTest(path=path), self.assertRaisesRegex(AdapterError, "endpoint_not_permitted"):
                read_endpoint(method, path)
        self.assertEqual(read_endpoint("POST", "/bff/spc/products/detail/123"), ("1001", "101"))

    def test_material_rejects_new_headers_crlf_refresh_tokens_and_unknown_builds(self):
        m = {"signing_profile": "mcd-v4-v1", "app_version": BUILDS[1]["version"], "binary_sha256": BUILDS[1]["sha256"],
             "headers": {"ct": "101", "v": "7.0.45.1", "language": "zh", "sid": "synthetic", "token": "device", "meddyid": "member"},
             "access_key": "a" * 20, "secret": "s" * 40}
        validate_material(m)
        for change in (lambda x: x["headers"].update(sid="bad\r\nInjected: value"),
                       lambda x: x.update(refresh_token="secret"), lambda x: x["headers"].update(host="evil"),
                       lambda x: x.update(binary_sha256="0" * 64)):
            altered = deepcopy(m); change(altered)
            with self.assertRaises(AdapterError): validate_material(altered)

    def test_helper_private_error_boundary_and_purchase_disabled_on_both_hosts(self):
        for role in ("host", "selection"):
            for operation in ("checkout.commit", "checkout_submit", "order_pay", "account_refresh"):
                result = subprocess.run([sys.executable, "-m", "sparkclaw_mcd.helper", "--role", role],
                    input=json.dumps({"operation": operation, "params": {"secret": "DO-NOT-LEAK"}}), text=True,
                    capture_output=True, timeout=10)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(json.loads(result.stdout), {"ok": False, "error": "operation_not_permitted"})
                self.assertEqual(result.stderr, "")
                self.assertNotIn("DO-NOT-LEAK", result.stdout)
            result = dispatch(role, {"operation": "capabilities", "params": {}})
            self.assertFalse(result["checkout_qualified"])
            self.assertFalse(result["product_channel_connected"])


if __name__ == "__main__":
    unittest.main()
