"""One bounded private-stdio request. Not an MCP server or a model tool.

The authenticated native/Gateway parent owns admission and process lifetime.
Selection never receives the host's full account or purchase authority. Listing
implemented operations does not grant product qualification or payment consent.
"""
import argparse
import json
import signal
import sys
import time

from pydantic import ValidationError

from ezmcd.errors import EzMcdError

from .canonical import canonical, fields, loads
from .compatibility import qualify, member_digest
from .errors import AdapterError, require, UPSTREAM_ERRORS
from .ledger import PrepareLedger
from .pickup_probe import export_pickup
from .pickup_control import retire_pickup
from .quotes import export_quote, validate_scope
from .reads import read
from .transport import provider
from .sessions import health, execute, issue

OPERATIONS = {
    "issuer": frozenset(("session.issue",)),
    "selection": frozenset(("capabilities", "selection.read", "session.validate", "selection.session.read", "selection.session.cart", "selection.session.quote", "quote.export", "quote.pickup.export", "quote.pickup.retire")),
    "host": frozenset(("capabilities", "runtime.qualify", "checkout.prepare", "checkout.abort", "operation.status", "host.capture", "host.prepare", "host.abort", "host.status", "host.commit", "host.orders")),
}


def dispatch(role, request, ledger_root=None, initialize=False, control_root=None, execution_root=None):
    fields(request, ("operation", "params"), ("scope", "material", "deadline_ms"))
    operation, params = request["operation"], request["params"]
    require(operation in OPERATIONS[role], "operation_not_permitted")
    if operation == "session.issue":
        from .imported_account import acquire
        from .canonical import digest
        fields(params, ("account_root", "alias", "binding", "connection_id", "request_id", "previous", "cart_enabled"))
        require(type(params["cart_enabled"]) is bool, "invalid_request")
        fields(params["binding"], ("deployment_id", "owner_id", "client_id", "installation_id", "account_ref"))
        previous = params["previous"]
        fields(previous, ("credential_revision", "grant_revision", "material_digest", "member_digest"))
        _, material = acquire(params["account_root"], params["alias"])
        member = member_digest(material)
        require(previous["member_digest"] in (None, member), "official_account_changed")
        changed = digest(material) != previous["material_digest"]
        from .sessions import SELECTION_OPERATIONS
        return issue(material, {**params["binding"], "member_digest": member}, params["connection_id"], params["request_id"],
                     previous["credential_revision"] + int(changed), previous["grant_revision"] + 1,
                     operations=sorted(SELECTION_OPERATIONS) if params["cart_enabled"] else None,
                     max_requests=128 if params["cart_enabled"] else 64)
    if operation == "capabilities":
        fields(params, ())
        return {"schema_version": 1, "role": role, "operations": sorted(OPERATIONS[role]),
                "qualification": "operator_qualification_required", "selection_qualified": False, "checkout_qualified": False,
                "payment_method": "ARCHCARD", "product_channel_connected": False}
    if operation == "runtime.qualify":
        require(role == "host", "operation_not_permitted")
        fields(params, ("binary",))
        return qualify(params["binary"])
    if operation == "session.validate":
        fields(params, ("envelope",))
        return health(params["envelope"])
    if operation == "selection.session.read":
        fields(params, ("envelope", "execution", "tool", "arguments"))
        return execute(params["envelope"], params["execution"], params["tool"], params["arguments"])
    if operation == "selection.session.cart":
        from .cart_session import execute as cart_execute
        fields(params, ("envelope", "execution", "tool", "arguments", "authority"))
        return cart_execute(params["envelope"], params["execution"], params["tool"], params["arguments"], execution_root, params["authority"])
    if operation == "selection.session.quote":
        from .sessions import admit
        from .cart_session import ExecutionStore
        from .quotes import export_stored_quote
        fields(params, ("envelope", "execution", "request", "purchase_id", "quote_revision", "handoff_epoch", "source_quote_ref"))
        value, execution = params["envelope"], params["execution"]
        admit(value, execution, "checkout_quote", {"checkout_quote"})
        scope = {**execution["identity"], "credential_revision": execution["credential_revision"], "execution_id": execution["execution_id"]}
        if params["source_quote_ref"]:
            require(execution_root is not None, "execution_store_required")
            store = ExecutionStore(execution_root, {"alias":scope["account_ref"]}, execution)
            envelope = export_stored_quote(store, scope["member_digest"], params["source_quote_ref"], scope,
                params["purchase_id"], params["quote_revision"], params["handoff_epoch"], now_ms=int(time.time()*1000))
            from ezmcd.models import CheckoutInput
            require(envelope["quote"]["request"] == CheckoutInput.model_validate(params["request"]).model_dump(), "quote_context_mismatch")
            return envelope
        client = provider(value["material"], scope["account_ref"], min(execution["deadline_ms"], value["session_not_after_ms"], int(time.time()*1000)+90000), requests=execution["max_requests"])
        try:
            client.wallet()
            return export_quote(client, params["request"], scope, params["purchase_id"], params["quote_revision"], params["handoff_epoch"])
        finally:
            client.http.close()
    if operation == "host.capture":
        from .imported_account import acquire, open_existing
        from .canonical import digest
        fields(params, ("source", "material_digest"))
        fields(params["source"], ("root", "alias"))
        source = params["source"]
        store, account = open_existing(source["root"], source["alias"])
        with store.lock(account["identity"]):
            from .compatibility import material_from_account
            current = store.account(source["alias"])
            require(digest(material_from_account(current)) == params["material_digest"], "credential_changed_reconnect")
            encoded = json.dumps(current, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
            require(len(encoded.encode()) <= 32*1024, "host_account_capacity_exceeded")
            return {"account_json": encoded, "material_digest": params["material_digest"]}
    if operation in ("host.prepare", "host.abort", "host.status", "host.commit", "host.orders"):
        from .imported_account import open_existing, acquire
        from .canonical import digest
        fields(params, ("source", "binding", "material_digest", "initialize", "request"), ("account_json",))
        fields(params["source"], ("root", "alias"))
        require(type(params["initialize"]) is bool, "invalid_request")
        scope = validate_scope(params["binding"])
        source = params["source"]
        original, account = open_existing(source["root"], source["alias"])
        require(account["identity"] == scope["member_digest"], "member_mismatch")
        installation = {k: scope[k] for k in ("deployment_id", "owner_id", "client_id", "installation_id")}
        ledger = PrepareLedger.adopt_existing(original.root, source["alias"], installation, initialize=params["initialize"])
        request = params["request"]
        if operation == "host.orders":
            from ezmcd.service import Service
            from ezmcd.models import Policy
            from .receipt import order_page
            fields(request, ("limit", "offset"))
            page = Service(original).orders(source["alias"], limit=request["limit"], offset=request["offset"])
            return order_page(page, Policy.model_validate(account["policy"]).max_order_cents)
        if operation == "host.status":
            from .recovery import retire_status
            fields(request, ("operation_key",))
            retired = retire_status(ledger, scope, request["operation_key"])
            if retired is not None:
                return retired
        if operation == "host.commit" or operation == "host.status" and params.get("account_json"):
            from ezmcd.models import fingerprint
            from ezmcd.provider import McdProvider
            from ezmcd.service import Service
            from .purchase import commit, status
            encoded = params.get("account_json")
            require(type(encoded) is str and len(encoded.encode()) <= 32*1024, "sealed_host_account_required")
            sealed = json.loads(encoded)
            require(fingerprint(sealed) == fingerprint(account), "official_account_changed")
            if operation == "host.commit":
                _, material = acquire(original.root, source["alias"])
                require(digest(material) == params["material_digest"], "credential_changed_reconnect")
                return commit(ledger, scope, request, sealed, McdProvider)
            fields(request, ("operation_key",))
            return status(ledger, scope, request["operation_key"], sealed, McdProvider)
        if operation == "host.abort":
            fields(request, ("operation_key", "purchase_id", "handoff_epoch", "reason"))
            return ledger.abort(scope, request["operation_key"], request["purchase_id"], request["handoff_epoch"], request["reason"])
        if operation == "host.status":
            fields(request, ("operation_key",))
            return ledger.status(scope, request["operation_key"])
        fields(request, ("operation_key", "envelope"))
        _, material = acquire(original.root, source["alias"])
        require(digest(material) == params["material_digest"], "credential_changed_reconnect")
        client = provider(material, scope["account_ref"], min(int(time.time()*1000)+90000, request["envelope"]["expires_at_ms"]))
        try:
            return ledger.prepare(scope, request["operation_key"], request["envelope"], client)
        finally:
            client.http.close()
    scope = validate_scope(request["scope"])
    ledger = None
    if role == "host":
        require(ledger_root is not None, "ledger_unavailable")
        ledger = PrepareLedger(ledger_root, {k: scope[k] for k in ("deployment_id", "owner_id", "client_id", "installation_id")},
                               initialize=initialize)
    if operation == "checkout.abort":
        fields(params, ("operation_key", "purchase_id", "handoff_epoch", "reason"))
        return ledger.abort(scope, params["operation_key"], params["purchase_id"], params["handoff_epoch"], params["reason"])
    if operation == "operation.status":
        fields(params, ("operation_key",))
        return ledger.status(scope, params["operation_key"])
    if operation == "quote.pickup.retire":
        require(control_root is not None, "pickup_qualification_control_required")
        fields(params, ("request", "purchase_id", "quote_revision", "handoff_epoch", "operation_key"))
        return retire_pickup(params["request"], scope, params["purchase_id"], params["quote_revision"],
                             params["handoff_epoch"], params["operation_key"], control_root)
    require(member_digest(request["material"]) == scope["member_digest"], "member_mismatch")
    pickup = operation == "quote.pickup.export"
    if pickup:
        require(control_root is not None, "pickup_qualification_control_required")
        fields(params, ("request", "purchase_id", "quote_revision", "handoff_epoch", "operation_key", "authorized_pickup_method"))
        require(params["authorized_pickup_method"] == params["request"]["eat_type"], "pickup_authorization_mismatch")
    client = provider(request["material"], scope["account_ref"], request["deadline_ms"], pickup_probe=pickup)
    try:
        if operation == "selection.read":
            fields(params, ("tool", "arguments"))
            return read(client, params["tool"], params["arguments"])
        if operation == "quote.export":
            fields(params, ("request", "purchase_id", "quote_revision", "handoff_epoch"))
            return export_quote(client, params["request"], scope, params["purchase_id"], params["quote_revision"], params["handoff_epoch"])
        if pickup:
            return export_pickup(client, params["request"], scope, params["purchase_id"], params["quote_revision"],
                                 params["handoff_epoch"], params["operation_key"], control_root)
        fields(params, ("operation_key", "envelope"))
        return ledger.prepare(scope, params["operation_key"], params["envelope"], client)
    finally:
        client.http.close()


def main():
    parser = argparse.ArgumentParser(description="Private EzMcDonalds qualification helper (no purchases)")
    parser.add_argument("--role", required=True, choices=OPERATIONS)
    parser.add_argument("--ledger")
    parser.add_argument("--initialize-ledger", action="store_true")
    parser.add_argument("--qualification-control")
    parser.add_argument("--execution-store")
    args = parser.parse_args()
    def timeout(*_):
        raise AdapterError("helper_deadline_exceeded")
    signal.signal(signal.SIGALRM, timeout)
    signal.alarm(90)
    try:
        require(not sys.stdin.isatty(), "private_stdin_required")
        # One JSON object, EOF closes the request. Trailing JSON/duplicate keys fail.
        raw = sys.stdin.buffer.read(320 * 1024 + 1)
        request = loads(raw, 320 * 1024)
        result = dispatch(args.role, request, args.ledger, args.initialize_ledger, args.qualification_control, args.execution_store)
        output = {"ok": True, "result": result}
        encoded = canonical(output, 320 * 1024)
    except AdapterError as error:
        encoded = canonical({"ok": False, "error": error.code})
    except EzMcdError as error:
        code = "upstream_" + error.code if error.code in UPSTREAM_ERRORS else "upstream_validation_failed"
        encoded = canonical({"ok": False, "error": code})
    except (KeyError, TypeError, ValidationError):
        encoded = canonical({"ok": False, "error": "invalid_request"})
    except Exception:
        # Never emit private exceptions/tracebacks, even for unexpected failures.
        encoded = canonical({"ok": False, "error": "helper_failed"})
    sys.stdout.buffer.write(encoded + b"\n")
    sys.stdout.buffer.flush()
    signal.alarm(0)
    return 0 if json.loads(encoded)["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
