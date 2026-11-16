"""Complete, bounded quote transfer independent of either upstream database."""
from copy import deepcopy
import math
import time
import uuid

from ezmcd.catalog import checkout
from ezmcd.models import CheckoutInput, cents, fingerprint
from ezmcd.pickup import cart_digest, preparation, selected_quote
from ezmcd.service import quote_authorization_fingerprint

from . import ADAPTER_REVISION
from .canonical import canonical, digest, fields, hash_value, identifier, integer
from .errors import require
from .receipt import encode_receipt, decode_receipt

SCOPE_FIELDS = ("deployment_id", "owner_id", "client_id", "installation_id", "account_ref",
                "member_digest", "credential_revision", "execution_id")
QUOTE_FIELDS = ("store_code", "store_name", "pickup_at", "eat_type", "items", "amount_cents", "regular_cents",
                "discount_cents", "discount_names", "body", "intent", "checkout_digest", "daypart", "request",
                "wallet_before_cents", "simulation")
ENVELOPE_FIELDS = ("schema_version", "canonicalization", "adapter_revision", "scope", "source_quote_ref", "purchase_id",
                   "quote_revision", "handoff_epoch", "generated_at_ms", "expires_at_ms", "currency", "payment_method",
                   "cart_digest", "quote", "pickup_receipt", "purchase_digest", "envelope_digest")


def validate_scope(scope):
    fields(scope, SCOPE_FIELDS)
    for key in SCOPE_FIELDS:
        if key == "credential_revision":
            integer(scope[key])
        elif key == "member_digest":
            hash_value(scope[key])
        else:
            identifier(scope[key])
    return scope


def purchase_digest(scope, quote):
    return digest({"canonicalization": "mcd-json-v1", "member_digest": scope["member_digest"],
                   "payment_method": "ARCHCARD", "currency": "CNY",
                   "quote_authorization_fingerprint": quote_authorization_fingerprint(scope["member_digest"], quote)})


def seal(envelope):
    envelope = deepcopy(envelope)
    envelope["purchase_digest"] = purchase_digest(envelope["scope"], envelope["quote"])
    envelope["envelope_digest"] = digest({k: v for k, v in envelope.items() if k != "envelope_digest"})
    return envelope


def validate(envelope, expected_scope, now_ms):
    canonical(envelope)  # Size, numeric and nesting admission before any networking/storage.
    fields(envelope, ENVELOPE_FIELDS)
    validate_scope(expected_scope)
    validate_scope(envelope["scope"])
    require(envelope["scope"] == expected_scope, "quote_scope_mismatch")
    require(type(envelope["schema_version"]) is int and envelope["schema_version"] == 1
            and envelope["canonicalization"] == "mcd-json-v1"
            and envelope["adapter_revision"] == ADAPTER_REVISION, "quote_version_unsupported")
    require(envelope["currency"] == "CNY" and envelope["payment_method"] == "ARCHCARD", "payment_channel_unsupported")
    for key in ("source_quote_ref", "purchase_id"):
        identifier(envelope[key])
    for key in ("quote_revision", "handoff_epoch", "generated_at_ms", "expires_at_ms"):
        integer(envelope[key])
    integer(now_ms)
    require(envelope["generated_at_ms"] <= now_ms < envelope["expires_at_ms"]
            and 0 < envelope["expires_at_ms"] - envelope["generated_at_ms"] <= 120000, "quote_expired_or_clock_untrusted")
    hash_value(envelope["cart_digest"])
    q = envelope["quote"]
    fields(q, QUOTE_FIELDS, ("pickup_selection", "pickup_confirmation"))
    require(q["simulation"] is False, "simulation_not_permitted")
    args = CheckoutInput.model_validate(q["request"])
    require(args.model_dump() == q["request"] and args.account == expected_scope["account_ref"], "quote_context_mismatch")
    require(all(q[key] == getattr(args, key) for key in ("store_code", "pickup_at", "eat_type")), "quote_context_mismatch")
    require(type(q["items"]) is list and 0 < len(q["items"]) <= 100
            and type(q["discount_names"]) is list and type(q["body"]) is dict, "quote_evidence_incomplete")
    for key in ("amount_cents", "regular_cents", "discount_cents", "wallet_before_cents"):
        integer(q[key], 0, 100000000)
    require(0 < q["amount_cents"] <= cents(args.max_amount_cny)
            and q["regular_cents"] - q["discount_cents"] == q["amount_cents"], "quote_amount_invalid")
    body = q["body"]
    require(body.get("storeCode") == args.store_code and body.get("eatTypeCode") == args.eat_type
            and cents(body.get("realTotalAmount")) == q["amount_cents"], "quote_body_mismatch")
    require(fingerprint(body) == q["checkout_digest"], "quote_body_mismatch")
    hash_value(q["intent"])
    selected = "pickup_selection" in q
    require(selected == (envelope["pickup_receipt"] is not None)
            and selected == ("pickup_confirmation" in q), "pickup_receipt_required")
    if selected:
        receipt = envelope["pickup_receipt"]
        fields(receipt, ("operation_id", "created_at_ms", "request", "prepared", "response_json", "receipt_digest"))
        identifier(receipt["operation_id"])
        integer(receipt["created_at_ms"])
        require(receipt["request"] == q["request"] and receipt["created_at_ms"] <= envelope["generated_at_ms"]
                and envelope["expires_at_ms"] <= receipt["created_at_ms"] + 120000, "pickup_receipt_context_mismatch")
        require(receipt["receipt_digest"] == digest({k: v for k, v in receipt.items() if k != "receipt_digest"}),
                "pickup_receipt_tampered")
        decode_receipt(receipt["response_json"])
        fields(q["pickup_selection"], ("cart_digest", "confirmation_digest"))
        require(q["pickup_selection"]["cart_digest"] == envelope["cart_digest"], "pickup_receipt_context_mismatch")
    require(envelope["purchase_digest"] == purchase_digest(expected_scope, q), "purchase_digest_mismatch")
    require(envelope["envelope_digest"] == digest({k: v for k, v in envelope.items() if k != "envelope_digest"}),
            "envelope_digest_mismatch")
    return args


def export_quote(provider, request, scope, purchase_id, quote_revision, handoff_epoch, *, clock=time.time):
    """Generate ordinary quote from official reads; no mutation, DB copy or MCP."""
    validate_scope(scope)
    require(provider.account["identity"] == scope["member_digest"], "member_mismatch")
    args = CheckoutInput.model_validate(request)
    require(args.account == scope["account_ref"], "quote_context_mismatch")
    cart, confirmation, daypart = provider._checkout_data(args)
    q = checkout(args, cart, confirmation, daypart)
    q.update(request=args.model_dump(), wallet_before_cents=provider.wallet()["balance_cents"], simulation=False)
    generated = int(clock() * 1000)
    envelope = seal({"schema_version": 1, "canonicalization": "mcd-json-v1", "adapter_revision": ADAPTER_REVISION,
        "scope": deepcopy(scope), "source_quote_ref": uuid.uuid4().hex, "purchase_id": purchase_id,
        "quote_revision": quote_revision, "handoff_epoch": handoff_epoch, "generated_at_ms": generated,
        "expires_at_ms": generated + 120000, "currency": "CNY", "payment_method": "ARCHCARD",
        "cart_digest": cart_digest(cart), "quote": q, "pickup_receipt": None})
    validate(envelope, scope, generated)
    return envelope


def export_stored_quote(store, identity, source_quote_ref, scope, purchase_id, revision, epoch, *, now_ms):
    """Trusted selection service exports an existing upstream quote and receipt.

    Not a model-facing route. The caller must have frozen the member cart.
    """
    q = deepcopy(store.quote(identity, source_quote_ref))
    require(identity == scope["member_digest"] and q["account"] == scope["account_ref"], "member_mismatch")
    expiry = math.floor(q["expires_at"] * 1000)
    bundle = None
    # Ordinary callers must export during live quotation to retain cart evidence.
    require("pickup_selection" in q, "ordinary_quote_requires_live_export")
    record = store.pickup_quote(identity)
    require(record and record.get("state") == "READY" and record.get("quote_id") == source_quote_ref,
            "pickup_receipt_required")
    receipt = store.write_receipt(record["operation_id"], "pickup_quote")
    require(receipt is not None, "pickup_receipt_required")
    bundle = {"operation_id": record["operation_id"], "created_at_ms": math.floor(record["created_at"] * 1000),
              "request": record["request"], "prepared": record["prepared"], "response_json": encode_receipt(receipt)}
    bundle["receipt_digest"] = digest(bundle)
    q = {k: v for k, v in q.items() if k in (*QUOTE_FIELDS, "pickup_selection", "pickup_confirmation")}
    envelope = seal({"schema_version": 1, "canonicalization": "mcd-json-v1", "adapter_revision": ADAPTER_REVISION,
        "scope": deepcopy(scope), "source_quote_ref": source_quote_ref, "purchase_id": purchase_id,
        "quote_revision": revision, "handoff_epoch": epoch, "generated_at_ms": expiry - 120000,
        "expires_at_ms": expiry, "currency": "CNY", "payment_method": "ARCHCARD",
        "cart_digest": q["pickup_selection"]["cart_digest"], "quote": q, "pickup_receipt": bundle})
    validate(envelope, scope, now_ms)
    return envelope


def revalidate(envelope, provider, *, clock=time.time):
    scope, q = envelope["scope"], envelope["quote"]
    args = validate(envelope, scope, int(clock() * 1000))
    require(provider.account["identity"] == scope["member_digest"], "member_mismatch")
    cart, confirmation, daypart = provider._checkout_data(args)
    require(cart_digest(cart) == envelope["cart_digest"], "cart_changed_requote")
    if envelope["pickup_receipt"] is not None:
        # An ordinary confirmation may have the default method. Reconstruct the
        # selected method from its original receipt, then run upstream's path.
        bundle = envelope["pickup_receipt"]
        current = preparation(args, cart, confirmation, daypart)
        require(all(current[k] == bundle["prepared"].get(k) == q["pickup_selection"][k]
                    for k in ("cart_digest", "confirmation_digest")), "pickup_receipt_context_mismatch")
        live = selected_quote(args, bundle["prepared"], cart, decode_receipt(bundle["response_json"]))
        require(live["pickup_selection"] == q["pickup_selection"]
                and live["pickup_confirmation"] == q["pickup_confirmation"], "pickup_receipt_tampered")
        provider.revalidate_pickup_quote(q)
    else:
        live = checkout(args, cart, confirmation, daypart)
    require(quote_authorization_fingerprint(scope["member_digest"], live)
            == quote_authorization_fingerprint(scope["member_digest"], q), "purchase_content_changed_requote")
    balance = integer(provider.wallet()["balance_cents"], 0, 100000000)
    # Import/prepare verifies a quote; it grants no purchase permission. Keep
    # the live balance and explicit blockers in the imported quote/receipt.
    # A purchase executor must separately enforce affordability before writes.
    validate(envelope, scope, int(clock() * 1000))  # Never restart expiry after reads.
    return balance


def local_quote(envelope, quote_id, wallet_balance):
    blockers = ["split_integration_not_qualified"]
    if wallet_balance < envelope["quote"]["amount_cents"]:
        blockers.append("insufficient_wallet_balance")
    return deepcopy(envelope["quote"]) | {"quote_id": quote_id, "account": envelope["scope"]["account_ref"],
        "expires_at": envelope["expires_at_ms"] / 1000, "wallet_before_cents": wallet_balance,
        "checkout_blockers": blockers}
