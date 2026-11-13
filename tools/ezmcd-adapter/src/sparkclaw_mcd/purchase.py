"""Native-host-only purchase adapter. The backend never receives this account.

Authority comes from the native client's persisted, matching user approval.
The original upstream journal still owns budgets, attempts and write receipts.
An interrupted commit is always queried with its original key, never resumed.
"""
from contextlib import contextmanager
from copy import deepcopy
import json
import sqlite3
import time

from ezmcd.errors import EzMcdError
from ezmcd.models import Policy, QuoteScope, fingerprint
from ezmcd.service import Service, quote_authorization_fingerprint
from ezmcd.storage import Store

from .canonical import canonical, digest, fields, identifier, integer
from .errors import require
from .quotes import purchase_digest


def review(scope, purchase, q):
    return {"purchase_id": purchase, "purchase_digest": purchase_digest(scope, q),
            "credential_revision": scope["credential_revision"], "currency": "CNY", "payment_method": "ARCHCARD",
            **{k: q[k] for k in ("amount_cents", "store_code", "store_name", "pickup_at", "eat_type", "items", "discount_names")}}


class PurchaseStore(Store):
    def __init__(self, ledger, scope, purchase, epoch, account, authority):
        # Never run Store.__init__: missing original tables must not be repaired.
        self.root = ledger.root
        self.ledger, self.scope, self.purchase, self.epoch = ledger, scope, purchase, epoch
        self.original, self.authority = deepcopy(account), authority
        self.scope_hash = ledger._scope(scope)
        self.member = scope["member_digest"]

    @contextmanager
    def lock(self, identity):
        # The caller holds the original member lock across commit and status.
        require(identity == self.member, "member_mismatch")
        yield

    def account(self, alias):
        require(alias == self.scope["account_ref"], "account_scope_mismatch")
        current = Store.account(self, self.original["alias"])
        require(fingerprint(current) == fingerprint(self.original), "official_account_changed")
        account = deepcopy(self.original)
        account["alias"] = alias
        if self.authority:
            # A single-purchase approval grants only this content. Preserve the
            # original shared daily/order caps, including every past UNKNOWN.
            policy = Policy.model_validate(account["policy"])
            require(self.authority["review"]["amount_cents"] <= policy.max_order_cents,
                    "order_outside_local_budget")
            if policy.allowed_stores:
                require(self.authority["review"]["store_code"] in policy.allowed_stores,
                        "store_outside_local_policy")
            policy.allow_checkout = True
            policy.expires_at = None  # Exact native milliseconds checked below.
            policy.allowed_stores = [self.authority["review"]["store_code"]]
            policy.max_order_cents = self.authority["review"]["amount_cents"]
            policy.quote_scope = QuoteScope(quote_id=self.quote_id,
                content_fingerprint=quote_authorization_fingerprint(self.member,self.quote(self.member,self.quote_id)))
            account["policy"] = policy.model_dump()
        return account

    def claim(self, attempt_id, stage):
        require(stage in ("order", "preorder", "consume"), "purchase_stage_invalid")
        self.check_deadline()
        with self.connect() as conn:
            conn.execute("BEGIN IMMEDIATE")
            auth = conn.execute("SELECT * FROM mcd_authorities WHERE member=? AND purchase=?",
                                (self.member, self.purchase)).fetchone()
            require(auth and auth["scope"] == self.scope_hash and auth["digest"] == digest(self.authority)
                    and auth["state"] in ("reserved", "consumed")
                    and auth["attempt"] in (None, attempt_id), "purchase_authority_unavailable")
            # The first official claim and authority consumption are one commit.
            # Every later stage belongs to exactly that attempt, including after
            # an uncertain order response. Nothing clears this consumed state.
            try:
                conn.execute("INSERT INTO writes VALUES (?,?,?,NULL)", (attempt_id, stage, time.time()))
            except sqlite3.IntegrityError:
                raise EzMcdError("write_already_attempted_query_only") from None
            conn.execute("UPDATE mcd_authorities SET state='consumed',attempt=? WHERE member=? AND purchase=?",
                         (attempt_id, self.member, self.purchase))
            conn.execute("UPDATE mcd_preparations SET write_claim=1,state='commit_started' WHERE member=? AND purchase=? AND epoch=?",
                         (self.member, self.purchase, self.epoch))

    def reserve(self, identity, key, quote, policy, data):
        self.check_deadline()
        # Upstream reserve is the sole budget implementation. Its transaction
        # precedes any write; a crash here remains query-only and conservatively
        # retains the budget reservation, even before an official write claim.
        super().reserve(identity, key, quote, policy, data)
        with self.connect() as conn:
            conn.execute("UPDATE mcd_commits SET attempt=? WHERE scope=? AND key=?",
                         (data["attempt_id"], self.scope_hash, key))

    def check_deadline(self):
        require(self.authority and int(self.ledger.clock()*1000) < self.authority["expires_at_ms"],
                "purchase_authority_expired")


class PurchaseService(Service):
    def _check_spending(self, account, quote, *, current_quote=False):
        self.store.check_deadline()
        return super()._check_spending(account, quote, current_quote=current_quote)


def commit(ledger, scope, request, account, provider_factory):
    fields(request, ("operation_key", "purchase_id", "handoff_epoch", "authority"))
    key, purchase, epoch, authority = (request[k] for k in ("operation_key", "purchase_id", "handoff_epoch", "authority"))
    identifier(key); identifier(purchase); integer(epoch)
    require(len(key) <= 80, "invalid_operation_key")
    fields(authority, ("approval_id", "approval_digest", "expires_at_ms", "review"))
    identifier(authority["approval_id"])
    require(type(authority["approval_digest"]) is str and len(authority["approval_digest"]) == 64
            and all(c in "0123456789abcdef" for c in authority["approval_digest"]), "invalid_authority")
    integer(authority["expires_at_ms"])
    scope_hash, member = ledger._scope(scope), scope["member_digest"]
    require(account["identity"] == member and account["provider"] == "mcd-cn", "member_mismatch")
    input_digest = digest(request)
    with ledger.store.lock(member):
        with ledger._transaction() as conn:
            old = conn.execute("SELECT * FROM mcd_commits WHERE scope=? AND key=?", (scope_hash, key)).fetchone()
            if old:
                require(old["digest"] == input_digest, "operation_key_conflict")
            else:
                prepared = conn.execute("SELECT * FROM mcd_preparations WHERE member=? AND purchase=? AND epoch=?",
                                        (member, purchase, epoch)).fetchone()
                require(prepared and prepared["scope"] == scope_hash and prepared["state"] == "prepared"
                        and not prepared["write_claim"], "purchase_not_prepared")
                q = ledger.store.quote(member, prepared["quote"])
                require(authority["review"] == review(scope, purchase, q), "purchase_authority_mismatch")
                now = int(ledger.clock()*1000)
                require(now < authority["expires_at_ms"] <= now+900000, "purchase_authority_expired")
                require(now < round(q["expires_at"]*1000), "quote_expired")
                prior = conn.execute("SELECT * FROM mcd_authorities WHERE member=? AND purchase=?", (member,purchase)).fetchone()
                if prior:
                    require(prior["state"] == "unused" and prior["scope"] == scope_hash
                            and prior["digest"] == digest(authority), "purchase_authority_unavailable")
                    conn.execute("UPDATE mcd_authorities SET state='reserved' WHERE member=? AND purchase=?", (member,purchase))
                else:
                    conn.execute("INSERT INTO mcd_authorities VALUES (?,?,?,?,?,?,'reserved',NULL)",
                                 (member,purchase,scope_hash,authority["approval_id"],digest(authority),authority["expires_at_ms"]))
                conn.execute("INSERT INTO mcd_commits VALUES (?,?,?,?,?,'started',NULL,NULL)", (scope_hash,key,input_digest,purchase,epoch))
        store = PurchaseStore(ledger,scope,purchase,epoch,account,authority)
        if old:
            # Do not call submit/pay_existing for a repeated commit, even when
            # a process died before reserve. Absence of an attempt is not proof
            # that the old execution is safe to re-dispatch.
            return _status(ledger,store,key,provider_factory)
        store.quote_id = q["quote_id"]
        client = provider_factory(store.account(scope["account_ref"]))
        try:
            service = PurchaseService(store, lambda _: client)
            result = service.submit(scope["account_ref"], q["quote_id"], key)
        except Exception:
            # Even preflight failures keep the original command claimed. Abort
            # may prove non-dispatch by checking the original attempt tables.
            result = {"state":"UNKNOWN", "automatic_write_retry":False, "paid_verified":False}
        finally:
            if callable(getattr(getattr(client, "http", None), "close", None)):
                client.http.close()
        return _persist(ledger,store,key,result)


def _persist(ledger, store, key, result):
    terminal = result.get("state") == "PAID" and result.get("paid_verified") is True or result.get("state") == "CANCELLED"
    receipt = {"state":result.get("state","UNKNOWN"), "purchase_id":store.purchase, "handoff_epoch":store.epoch,
               "terminal_verified":bool(terminal), "automatic_write_retry":False,
               "result_json":json.dumps(result,ensure_ascii=False,separators=(",",":"))}
    with ledger._transaction() as conn:
        conn.execute("UPDATE mcd_commits SET state=?,receipt=? WHERE scope=? AND key=?",
                     (receipt["state"],canonical(receipt).decode(),store.scope_hash,key))
        if terminal:
            conn.execute("UPDATE mcd_preparations SET state=? WHERE member=? AND purchase=? AND epoch=?",
                         (receipt["state"],store.member,store.purchase,store.epoch))
            conn.execute("INSERT INTO mcd_purchases VALUES (?,?,1) ON CONFLICT(member,purchase) DO UPDATE SET closed=1",(store.member,store.purchase))
    return receipt


def _status(ledger, store, key, provider_factory):
    with ledger.store.connect() as conn:
        # The reserve->commit metadata gap is recovered from the original key.
        row=conn.execute("SELECT attempt FROM request_keys WHERE identity=? AND request_key=?",(store.member,key)).fetchone()
    if not row:
        return {"state":"reconciliation_required", "purchase_id":store.purchase, "handoff_epoch":store.epoch,
                "terminal_verified":False, "automatic_write_retry":False}
    store.authority = None  # Status never renews or consumes purchase authority.
    client=provider_factory(store.account(store.scope["account_ref"]))
    try:
        result=Service(store,lambda _:client).status(store.scope["account_ref"],row[0])
        return _persist(ledger,store,key,result)
    finally:
        if callable(getattr(getattr(client,"http",None),"close",None)): client.http.close()


def status(ledger,scope,key,account,provider_factory):
    scope_hash=ledger._scope(scope); identifier(key)
    with ledger.store.lock(scope["member_digest"]):
        with ledger.store.connect() as conn:
            row=conn.execute("SELECT * FROM mcd_commits WHERE scope=? AND key=?",(scope_hash,key)).fetchone()
        if not row: return ledger.status(scope,key)
        store=PurchaseStore(ledger,scope,row["purchase"],row["epoch"],account,None)
        return _status(ledger,store,key,provider_factory)
