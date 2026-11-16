"""G2-only one-shot switch with a durable, payload-free pre-dispatch fence.

Only a trusted qualification runner may use this entry point. Losing the
envelope or temporary state requires manual reconciliation, never a new PUT.
"""
import time
import uuid

from ezmcd.models import pickup_time

from . import ADAPTER_REVISION
from .canonical import digest
from .errors import require
from .quotes import seal, validate
from .receipt import encode_receipt
from .pickup_control import context, control


def export_pickup(provider, request, scope, purchase, revision, epoch, key, control_root, *, clock=time.time):
    args, request_digest = context(request, scope, purchase, revision, epoch, key)
    require(provider.account["identity"] == scope["member_digest"], "member_mismatch")
    with control(control_root) as conn:
        old = conn.execute("SELECT digest FROM fences WHERE member=? AND key=?", (scope["member_digest"], key)).fetchone()
        if old:
            require(old[0] == request_digest, "operation_key_conflict")
            require(False, "pickup_operation_requires_original_receipt")
        require(not conn.execute("SELECT 1 FROM fences f WHERE member=? AND NOT EXISTS "
                "(SELECT 1 FROM retirements r WHERE r.member=f.member AND r.key=f.key AND r.digest=f.digest)",
                (scope["member_digest"],)).fetchone(),
                "pickup_operation_unresolved")
        require(conn.execute("SELECT count(*) FROM fences").fetchone()[0] < 10000, "control_capacity_exhausted")
        require(pickup_time(args.pickup_at).timestamp() > clock(), "pickup_time_has_passed")
        provider.wallet()  # Real member validation before any state change.
        prepared = provider.prepare_pickup(args)
        require(not prepared["already_selected"], "pickup_already_selected_use_ordinary_quote")
        created = int(clock() * 1000)
        conn.execute("INSERT INTO fences VALUES (?,?,?,'unknown')", (scope["member_digest"], key, request_digest))
        conn.commit()  # At-most-once claim is durable BEFORE the official PUT.
        receipt = provider.switch_pickup(prepared["body"])
        q = provider.finish_pickup(args, prepared, receipt)
        q.update(request=args.model_dump(), wallet_before_cents=provider.wallet()["balance_cents"], simulation=False)
        bundle = {"operation_id": key, "created_at_ms": created, "request": args.model_dump(), "prepared": prepared, "response_json": encode_receipt(receipt)}
        bundle["receipt_digest"] = digest(bundle)
        envelope = seal({"schema_version": 1, "canonicalization": "mcd-json-v1", "adapter_revision": ADAPTER_REVISION,
            "scope": scope, "source_quote_ref": uuid.uuid4().hex, "purchase_id": purchase,
            "quote_revision": revision, "handoff_epoch": epoch, "generated_at_ms": created, "expires_at_ms": created + 120000,
            "currency": "CNY", "payment_method": "ARCHCARD", "cart_digest": prepared["cart_digest"], "quote": q, "pickup_receipt": bundle})
        validate(envelope, scope, int(clock() * 1000))
        conn.execute("UPDATE fences SET state='receipt_produced' WHERE member=? AND key=?", (scope["member_digest"], key))
        conn.commit()
        return envelope
