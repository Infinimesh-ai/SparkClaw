"""Execution-owned upstream cart Store, with one narrowly allowed write.

The Gateway must durably claim the member operation before invoking this role.
No credentials enter this temporary Store. It is not a purchase Store and cannot
replace or repair any Mac ledger.
"""
from pathlib import Path
import os
import sqlite3
import time

from ezmcd.models import CheckoutInput, ItemSelection, Policy
from ezmcd.errors import EzMcdError
from pydantic import ValidationError
from ezmcd.provider import McdProvider
from ezmcd.service import Service
from ezmcd.storage import Store, read_private, write_private

from .canonical import canonical, digest, fields, identifier
from .compatibility import member_digest, member_id
from .errors import require, AdapterError
from .receipt import encode_official_json
from .transport import SelectionTransport

CART_OPERATIONS = frozenset(("cart_snapshot", "cart_add_simple", "cart_add_configured", "cart_clear", "cart_add_status", "checkout_quote_pickup", "checkout_quote_pickup_status"))
WRITE_PATHS = {"cart_add_simple": "/bff/cart/carts", "cart_add_configured": "/bff/cart/carts",
               "cart_clear": "/bff/cart/carts/empty", "checkout_quote_pickup": "/bff/order/confirmation/eattype/switch"}


class CartTransport(SelectionTransport):
    def __init__(self, *args, operation, **kwargs):
        super().__init__(*args, **kwargs)
        self.operation = operation
        self.write_claimed = False

    def send(self, method, path, params=None, body=None):
        if method == "PUT" and path == WRITE_PATHS.get(self.operation):
            require(not self.write_claimed, "cart_write_already_dispatched")
            self.write_claimed = True
            business = {"/bff/cart/carts": None, "/bff/cart/carts/empty": ("1003", "200"),
                        "/bff/order/confirmation/eattype/switch": ("1029", "202")}[path]
            return self._transmit(method, path, business, params, body)
        return super().send(method, path, params, body)


class ExecutionStore(Store):
    def __init__(self, root, account, execution):
        root = Path(root).absolute()
        require(not any(p.is_symlink() for p in (root, *root.parents)), "execution_store_invalid")
        marker = root / "execution.json"
        binding = {"version": 1, "binding": digest([execution["identity"], execution["execution_id"]])}
        if root.exists() and any(root.iterdir()):
            require(marker.is_file() and read_private(marker) == binding
                    and (root / "journal.sqlite3").is_file(), "execution_store_missing_recovery_required")
            require(root.stat().st_mode & 0o077 == 0 and root.stat().st_uid == os.getuid(), "execution_store_invalid")
            require(sum(p.stat().st_size for p in root.rglob("*") if p.is_file()) <= 4 * 1024 * 1024,
                    "execution_store_budget_exhausted")
            require(not any(p.is_symlink() for p in root.rglob("*")), "execution_store_invalid")
            try:
                with sqlite3.connect((root / "journal.sqlite3").as_uri() + "?mode=rw", uri=True) as conn:
                    require(conn.execute("PRAGMA quick_check").fetchone()[0] == "ok", "execution_store_corrupt")
                    for table in ("accounts", "quotes", "attempts", "writes", "request_keys", "cart_snapshots", "cart_additions", "pickup_quotes"):
                        conn.execute(f"SELECT * FROM {table} LIMIT 0")
            except sqlite3.Error:
                raise AdapterError("execution_store_missing_recovery_required") from None
        super().__init__(root)
        if not marker.exists():
            write_private(marker, binding)
        self.current_account = account

    def connect(self):
        # SQLite enforces the same admission bound during writes, including
        # writes made internally by upstream recovery routines.
        from contextlib import contextmanager
        @contextmanager
        def bounded():
            with super(ExecutionStore, self).connect() as conn:
                size = conn.execute("PRAGMA page_size").fetchone()[0]
                conn.execute(f"PRAGMA max_page_count={2 * 1024 * 1024 // size}")
                yield conn
        return bounded()

    def account(self, alias):
        require(alias == self.current_account["alias"], "member_mismatch")
        return self.current_account


def execute(value, execution, operation, arguments, root, authority=None):
    from .sessions import admit
    admit(value, execution, operation, CART_OPERATIONS)
    require(root is not None, "execution_store_required")
    if operation in WRITE_PATHS:
        fields(authority, ("operation_key", "input_digest", "deadline_ms"))
        identifier(authority["operation_key"])
        require(authority["input_digest"] == digest({"operation": operation, "arguments": arguments})
                and int(time.time() * 1000) < authority["deadline_ms"] <= execution["deadline_ms"],
                "cart_authority_mismatch")
    else:
        require(authority is None, "unexpected_cart_authority")
    material = value["material"]
    account = {"alias": value["identity"]["account_ref"], "provider": "mcd-cn", "identity": member_digest(material),
        "profile": {"member_source": "balance_response", "fields": {"meddyId": member_id(material)}}, "policy": Policy().model_dump()}
    store = ExecutionStore(root, account, execution)
    transport = CartTransport(material, operation=operation, deadline_ms=min(execution["deadline_ms"], value["session_not_after_ms"], int(time.time()*1000)+90000), requests=execution["max_requests"])
    provider = McdProvider(account, transport=transport)
    service = Service(store, provider_factory=lambda _: provider)
    try:
        provider.wallet()
        if operation in ("cart_add_status", "checkout_quote_pickup_status"):
            fields(arguments, ("operation_id",))
            result = (service.cart_add_status if operation == "cart_add_status" else service.pickup_quote_status)(account["alias"], arguments["operation_id"])
        else:
            extras = ("items",) if operation in ("cart_add_simple", "cart_add_configured") else ("cart_ref",) if operation in ("cart_clear", "checkout_quote_pickup") else ()
            fields(arguments, ("request", *extras))
            args = CheckoutInput.model_validate(arguments["request"])
            require(args.account == account["alias"], "member_mismatch")
            if operation == "cart_snapshot":
                result = service.cart(args)
            elif operation in ("cart_add_simple", "cart_add_configured"):
                require(type(arguments["items"]) is list and 1 <= len(arguments["items"]) <= 20, "invalid_cart_items")
                items = [ItemSelection.model_validate(item) for item in arguments["items"]]
                method = service.add_simple_items if operation == "cart_add_simple" else service.add_configured_items
                result = method(args, items, authority["operation_key"])
            else:
                method = service.clear_cart if operation == "cart_clear" else service.quote_pickup
                result = method(args, arguments["cart_ref"], authority["operation_key"])
        return {"schema_version": 1, "result_encoding": "bounded-json-v1", "result_json": encode_official_json(result, 64 * 1024),
                "dispatch_state": "dispatched" if transport.write_claimed else "not_dispatched",
                "write_claimed": transport.write_claimed, "order_created": False, "payment_attempted": False}
    except (AdapterError, EzMcdError, ValidationError, OSError, ValueError, KeyError, TypeError):
        # A structured non-dispatch receipt can release the member fence. A
        # killed process or missing receipt cannot provide that proof.
        return {"schema_version": 1, "result_encoding": "bounded-json-v1", "result_json": "{}",
                "dispatch_state": "unknown" if transport.write_claimed else "not_dispatched",
                "error_code": "cart_operation_requires_review", "write_claimed": transport.write_claimed,
                "order_created": False, "payment_attempted": False}
    finally:
        transport.close()
