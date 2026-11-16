"""Status-only local retirement of a known, provably undispatched purchase.

No provider is constructed. Original attempts, budget reservations and commit
receipts remain intact; the additional receipt only removes payment authority.
"""
import json

from .canonical import digest, identifier
from .errors import UPSTREAM_ERRORS


def commit_key(purchase, epoch):
    # Matches Go mcdintegration.CommitKey; scope is checked independently.
    return "commit-" + digest([purchase, epoch])


def retire_status(ledger, scope, key):
    scope_hash = ledger._scope(scope)
    identifier(key)
    member = scope["member_digest"]
    proof_key = "status-abort-" + digest([scope_hash, key])
    input_digest = digest({"operation": "status_retire", "operation_key": key})
    with ledger.store.lock(member), ledger._transaction() as conn:
        old = ledger._old(conn, scope_hash, proof_key, input_digest)
        if old:
            return old
        # Hashes cannot be inverted. Match only actual preparations owned by
        # this exact scope; absence of a commit is never itself negative proof.
        rows = conn.execute("SELECT * FROM mcd_preparations WHERE member=? AND scope=?",
                            (member, scope_hash)).fetchall()
        row = next((r for r in rows if commit_key(r["purchase"], r["epoch"]) == key), None)
        if not row or not row["quote"] or not row["digest"]:
            return None
        purchase, epoch = row["purchase"], row["epoch"]
        commit = conn.execute("SELECT * FROM mcd_commits WHERE scope=? AND key=?", (scope_hash, key)).fetchone()
        if commit and (commit["purchase"] != purchase or commit["epoch"] != epoch):
            return None
        # Querying a previously aborted epoch must not retire a newer payable
        # preparation of that purchase, including one in a different scope.
        if conn.execute("SELECT 1 FROM mcd_preparations WHERE member=? AND purchase=? AND epoch<>? "
                        "AND state NOT IN ('aborted_before_write','PAID','CANCELLED')",
                        (member, purchase, epoch)).fetchone():
            return None
        if not ledger.non_dispatch_proven(conn, scope_hash, member, purchase, epoch):
            return None
        prior = json.loads(commit["receipt"]) if commit and commit["receipt"] else {}
        reason = prior.get("stop_reason")
        receipt = {"state": "aborted_before_write", "operation_key": key, "scope_digest": scope_hash,
                   "purchase_id": purchase, "handoff_epoch": epoch, "non_dispatch_proven": True,
                   "purchase_closed": True, "reason": "execution_termination",
                   "original_state": commit["state"] if commit else row["state"],
                   "stop_reason": reason if reason in UPSTREAM_ERRORS else "",
                   "terminal_verified": False, "automatic_write_retry": False,
                   "official_order_canceled": False, "refund_attempted": False}
        conn.execute("UPDATE mcd_authorities SET state='revoked' WHERE member=? AND purchase=? AND scope=? "
                     "AND state IN ('reserved','unused')", (member, purchase, scope_hash))
        conn.execute("UPDATE mcd_preparations SET state='aborted_before_write' WHERE member=? AND purchase=? AND epoch=?",
                     (member, purchase, epoch))
        conn.execute("INSERT INTO mcd_purchases VALUES (?,?,1) ON CONFLICT(member,purchase) DO UPDATE SET closed=1",
                     (member, purchase))
        return ledger._record(conn, scope_hash, proof_key, input_digest, receipt)
