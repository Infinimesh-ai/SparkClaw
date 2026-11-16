"""Mac-only durable preparation ledger shared with the original purchase journal.

Full quotes stay in the Mac Store. Acceptance and abort records contain only
scope hashes, IDs, digests, epochs and states. Missing established files fail
closed. The later product integration also needs a platform-store anchor.
"""
from contextlib import contextmanager
import json
import os
from pathlib import Path
import sqlite3
import time
import uuid

from ezmcd.storage import Store, read_private, write_private

from .canonical import canonical, digest, fields, identifier, integer
from .errors import require, AdapterError
from .quotes import validate, validate_scope, revalidate, local_quote

ABORT_REASONS = ("user_abandonment", "quote_refresh", "execution_termination")


class PrepareLedger:
    def __init__(self, root, installation, *, initialize=False, clock=time.time):
        self.clock = clock
        self.root = Path(root).absolute()
        require(not any(p.is_symlink() for p in (self.root, *self.root.parents)), "ledger_path_invalid")
        self.binding = digest(installation)
        marker = self.root / "installation.json"
        db = self.root / "journal.sqlite3"
        if initialize:
            require(not self.root.exists() or not any(self.root.iterdir()), "ledger_already_initialized")
            self.store = Store(self.root)
            with self.store.connect() as conn:
                conn.executescript("""
                CREATE TABLE mcd_meta(binding TEXT PRIMARY KEY);
                CREATE TABLE mcd_authorities(member TEXT NOT NULL, purchase TEXT NOT NULL,
                  scope TEXT NOT NULL, approval TEXT NOT NULL, digest TEXT NOT NULL,
                  deadline INTEGER NOT NULL, state TEXT NOT NULL, attempt TEXT,
                  PRIMARY KEY(member,purchase), UNIQUE(scope,approval));
                CREATE TABLE mcd_commits(scope TEXT NOT NULL, key TEXT NOT NULL,
                  digest TEXT NOT NULL, purchase TEXT NOT NULL, epoch INTEGER NOT NULL,
                  state TEXT NOT NULL, attempt TEXT, receipt TEXT,
                  PRIMARY KEY(scope,key));
                CREATE TABLE mcd_preparations(
                  member TEXT NOT NULL, purchase TEXT NOT NULL, epoch INTEGER NOT NULL,
                  revision INTEGER, scope TEXT NOT NULL, digest TEXT, quote TEXT,
                  state TEXT NOT NULL, write_claim INTEGER NOT NULL DEFAULT 0,
                  PRIMARY KEY(member,purchase,epoch));
                CREATE TABLE mcd_operations(scope TEXT NOT NULL, key TEXT NOT NULL,
                  digest TEXT NOT NULL, receipt TEXT NOT NULL, PRIMARY KEY(scope,key));
                CREATE TABLE mcd_members(member TEXT PRIMARY KEY, epoch INTEGER NOT NULL);
                CREATE TABLE mcd_purchases(member TEXT, purchase TEXT, closed INTEGER NOT NULL,
                  PRIMARY KEY(member,purchase));
                """)
                conn.execute("INSERT INTO mcd_meta VALUES (?)", (self.binding,))
            write_private(marker, {"schema_version": 1, "binding": self.binding})
        else:
            require(self.root.is_dir() and self.root.stat().st_mode & 0o077 == 0
                    and marker.is_file() and db.is_file() and not db.is_symlink()
                    and db.stat().st_mode & 0o077 == 0, "ledger_missing_recovery_required")
            require(read_private(marker) == {"schema_version": 1, "binding": self.binding}, "ledger_binding_mismatch")
            try:
                # Do not let upstream's CREATE IF NOT EXISTS repair a partial ledger.
                with sqlite3.connect(db.as_uri() + "?mode=rw", uri=True) as conn:
                    require(conn.execute("PRAGMA quick_check").fetchone()[0] == "ok", "ledger_corrupt_recovery_required")
                    require(conn.execute("SELECT binding FROM mcd_meta").fetchall() == [(self.binding,)], "ledger_binding_mismatch")
                    for table in ("mcd_preparations", "mcd_operations", "mcd_members", "mcd_purchases", "mcd_authorities", "mcd_commits", "quotes", "attempts", "writes"):
                        conn.execute(f"SELECT * FROM {table} LIMIT 0")
            except sqlite3.Error:
                raise AdapterError("ledger_corrupt_recovery_required") from None
            self.store = object.__new__(Store)
            self.store.root = self.root

    @classmethod
    def adopt_existing(cls, root, alias, installation, *, initialize=False):
        """Attach control tables to the original Mac ledger, never copy/reset it.

        Initialization is an explicit native action protected by its platform
        store anchor. An established or partially created ledger is never
        repaired by this method.
        """
        from .imported_account import open_existing
        store, account = open_existing(root, alias)
        marker = store.root / "installation.json"
        if marker.exists():
            return cls(store.root, installation)
        require(initialize, "ledger_missing_recovery_required")
        with store.lock(account["identity"]):
            with store.connect() as conn:
                require(conn.execute("PRAGMA quick_check").fetchone()[0] == "ok", "ledger_corrupt_recovery_required")
                for table in ("accounts", "quotes", "attempts", "writes", "request_keys", "cart_snapshots", "cart_additions", "pickup_quotes"):
                    conn.execute(f"SELECT * FROM {table} LIMIT 0")
                require(not conn.execute("SELECT 1 FROM sqlite_master WHERE name LIKE 'mcd_%'").fetchone(),
                        "ledger_partial_initialization_recovery_required")
                conn.executescript("BEGIN IMMEDIATE;" + """
                CREATE TABLE mcd_meta(binding TEXT PRIMARY KEY);
                CREATE TABLE mcd_authorities(member TEXT NOT NULL, purchase TEXT NOT NULL,
                  scope TEXT NOT NULL, approval TEXT NOT NULL, digest TEXT NOT NULL,
                  deadline INTEGER NOT NULL, state TEXT NOT NULL, attempt TEXT,
                  PRIMARY KEY(member,purchase), UNIQUE(scope,approval));
                CREATE TABLE mcd_commits(scope TEXT NOT NULL, key TEXT NOT NULL,
                  digest TEXT NOT NULL, purchase TEXT NOT NULL, epoch INTEGER NOT NULL,
                  state TEXT NOT NULL, attempt TEXT, receipt TEXT,
                  PRIMARY KEY(scope,key));
                CREATE TABLE mcd_preparations(
                  member TEXT NOT NULL, purchase TEXT NOT NULL, epoch INTEGER NOT NULL,
                  revision INTEGER, scope TEXT NOT NULL, digest TEXT, quote TEXT,
                  state TEXT NOT NULL, write_claim INTEGER NOT NULL DEFAULT 0,
                  PRIMARY KEY(member,purchase,epoch));
                CREATE TABLE mcd_operations(scope TEXT NOT NULL, key TEXT NOT NULL,
                  digest TEXT NOT NULL, receipt TEXT NOT NULL, PRIMARY KEY(scope,key));
                CREATE TABLE mcd_members(member TEXT PRIMARY KEY, epoch INTEGER NOT NULL);
                CREATE TABLE mcd_purchases(member TEXT, purchase TEXT, closed INTEGER NOT NULL,
                  PRIMARY KEY(member,purchase));
                """)
                conn.execute("INSERT INTO mcd_meta VALUES (?)", (digest(installation),))
            write_private(marker, {"schema_version": 1, "binding": digest(installation)})
        return cls(store.root, installation)

    def _scope(self, scope):
        validate_scope(scope)
        installation = {k: scope[k] for k in ("deployment_id", "owner_id", "client_id", "installation_id")}
        require(digest(installation) == self.binding, "ledger_binding_mismatch")
        return digest(scope)

    @contextmanager
    def _transaction(self):
        try:
            with self.store.connect() as conn:
                conn.execute("BEGIN IMMEDIATE")
                yield conn
        except sqlite3.Error:
            raise AdapterError("ledger_write_failed_recovery_required") from None

    def _old(self, conn, scope_hash, key, input_digest):
        row = conn.execute("SELECT digest,receipt FROM mcd_operations WHERE scope=? AND key=?", (scope_hash, key)).fetchone()
        if row:
            require(row["digest"] == input_digest, "operation_key_conflict")
            return json.loads(row["receipt"])
        return None

    def _record(self, conn, scope_hash, key, input_digest, receipt):
        require(conn.execute("SELECT count(*) FROM mcd_operations").fetchone()[0] < 10000, "ledger_capacity_exhausted")
        conn.execute("INSERT INTO mcd_operations VALUES (?,?,?,?)", (scope_hash, key, input_digest, canonical(receipt).decode()))
        return receipt

    def prepare(self, scope, key, envelope, provider):
        scope_hash = self._scope(scope)
        identifier(key)
        input_digest = digest({"operation": "prepare", "envelope": envelope})
        require(envelope.get("scope") == scope, "quote_scope_mismatch")
        member = scope["member_digest"]
        with self.store.lock(member):
            with self._transaction() as conn:
                old = self._old(conn, scope_hash, key, input_digest)
                if old:
                    return old
                validate(envelope, scope, int(self.clock() * 1000))
                purchase, epoch = envelope["purchase_id"], envelope["handoff_epoch"]
                previous = conn.execute("SELECT * FROM mcd_preparations WHERE member=? AND purchase=? AND epoch=?",
                                        (member, purchase, epoch)).fetchone()
                if previous:
                    require(previous["state"] != "aborted_before_write", "handoff_epoch_aborted")
                    require(previous["digest"] == envelope["envelope_digest"] and previous["scope"] == scope_hash,
                            "handoff_epoch_conflict")
                    receipt = self._receipt(previous, envelope["purchase_digest"])
                    return self._record(conn, scope_hash, key, input_digest, receipt)
                ended = conn.execute("SELECT closed FROM mcd_purchases WHERE member=? AND purchase=?", (member, purchase)).fetchone()
                require(ended is None or not ended[0], "purchase_intent_closed")
                require(not conn.execute("SELECT 1 FROM mcd_preparations WHERE member=? AND state NOT IN ('aborted_before_write','PAID','CANCELLED')",
                                         (member,)).fetchone(), "member_preparation_unresolved")
                high = conn.execute("SELECT epoch FROM mcd_members WHERE member=?", (member,)).fetchone()
                require(high is None or epoch > high["epoch"], "handoff_epoch_stale")
                revisions = conn.execute("SELECT revision FROM mcd_preparations WHERE member=? AND purchase=? AND revision IS NOT NULL",
                                         (member, purchase)).fetchall()
                require(len(revisions) < 3, "quote_refresh_limit_exceeded")
                require(not revisions or envelope["quote_revision"] > max(r[0] for r in revisions), "quote_revision_stale")
            # Member file lock spans both live checks and final transaction, so
            # prepare/abort cannot race past one another in separate processes.
            balance = revalidate(envelope, provider, clock=self.clock)
            with self._transaction() as conn:
                validate(envelope, scope, int(self.clock() * 1000))
                quote_id = uuid.uuid4().hex
                q = local_quote(envelope, quote_id, balance)
                conn.execute("INSERT INTO quotes VALUES (?,?,?)", (quote_id, member, json.dumps(q, ensure_ascii=False)))
                conn.execute("INSERT INTO mcd_preparations VALUES (?,?,?,?,?,?,?,?,0)",
                             (member, purchase, epoch, envelope["quote_revision"], scope_hash,
                              envelope["envelope_digest"], quote_id, "prepared"))
                conn.execute("INSERT INTO mcd_members VALUES (?,?) ON CONFLICT(member) DO UPDATE SET epoch=excluded.epoch", (member, epoch))
                receipt = {"state": "prepared", "purchase_id": purchase, "handoff_epoch": epoch,
                           "local_quote_ref": quote_id, "purchase_digest": envelope["purchase_digest"],
                           "expires_at_ms": envelope["expires_at_ms"], "checkout_qualified": False,
                           "wallet_sufficient": balance >= q["amount_cents"], "checkout_blockers": q["checkout_blockers"],
                           "order_created": False, "payment_attempted": False}
                return self._record(conn, scope_hash, key, input_digest, receipt)

    def _receipt(self, row, purchase_digest):
        q = self.store.quote(row["member"], row["quote"])
        return {"state": row["state"], "purchase_id": row["purchase"], "handoff_epoch": row["epoch"],
                "local_quote_ref": row["quote"], "purchase_digest": purchase_digest,
                "expires_at_ms": round(q["expires_at"] * 1000), "checkout_qualified": False,
                "wallet_sufficient": q["wallet_before_cents"] >= q["amount_cents"], "checkout_blockers": q["checkout_blockers"],
                "order_created": False, "payment_attempted": False}

    def abort(self, scope, key, purchase, epoch, reason):
        scope_hash = self._scope(scope)
        identifier(key); identifier(purchase); integer(epoch)
        require(reason in ABORT_REASONS, "invalid_abort_reason")
        input_digest = digest({"operation": "abort", "purchase_id": purchase, "handoff_epoch": epoch, "reason": reason})
        member = scope["member_digest"]
        with self.store.lock(member), self._transaction() as conn:
            old = self._old(conn, scope_hash, key, input_digest)
            if old:
                return old
            row = conn.execute("SELECT * FROM mcd_preparations WHERE member=? AND purchase=? AND epoch=?", (member, purchase, epoch)).fetchone()
            require(row is None or row["scope"] == scope_hash, "quote_scope_mismatch")
            claimed = row and (row["write_claim"] or row["state"] not in ("prepared", "aborted_before_write"))
            # The upstream attempt table is independent corroborating evidence.
            if row and row["quote"]:
                claimed = claimed or conn.execute("SELECT 1 FROM attempts WHERE identity=? AND json_extract(data,'$.quote_id')=?",
                                                   (member, row["quote"])).fetchone()
            state = "reconciliation_required" if claimed else "aborted_before_write"
            if not claimed:
                conn.execute("UPDATE mcd_authorities SET state=? WHERE member=? AND purchase=? AND state='reserved'",
                             ("unused" if reason == "quote_refresh" else "revoked", member, purchase))
                conn.execute("INSERT INTO mcd_purchases VALUES (?,?,?) ON CONFLICT(member,purchase) DO UPDATE SET closed=max(closed,excluded.closed)",
                             (member, purchase, int(reason != "quote_refresh")))
                if row:
                    conn.execute("UPDATE mcd_preparations SET state=? WHERE member=? AND purchase=? AND epoch=?", (state, member, purchase, epoch))
                else:
                    conn.execute("INSERT INTO mcd_preparations VALUES (?,?,?,NULL,?,NULL,NULL,?,0)", (member, purchase, epoch, scope_hash, state))
                conn.execute("INSERT INTO mcd_members VALUES (?,?) ON CONFLICT(member) DO UPDATE SET epoch=max(epoch,excluded.epoch)", (member, epoch))
            receipt = {"state": state, "purchase_id": purchase, "handoff_epoch": epoch,
                       "reason": reason, "official_order_canceled": False, "refund_attempted": False}
            return self._record(conn, scope_hash, key, input_digest, receipt)

    def status(self, scope, key):
        scope_hash = self._scope(scope)
        identifier(key)
        with self.store.connect() as conn:
            row = conn.execute("SELECT receipt FROM mcd_operations WHERE scope=? AND key=?", (scope_hash, key)).fetchone()
            if not row:
                pending = conn.execute("SELECT receipt,purchase,epoch FROM mcd_commits WHERE scope=? AND key=?", (scope_hash,key)).fetchone()
                if pending:
                    return json.loads(pending[0]) if pending[0] else {"state":"reconciliation_required", "purchase_id":pending[1],
                        "handoff_epoch":pending[2], "terminal_verified":False, "automatic_write_retry":False}
        return json.loads(row[0]) if row else {"state": "not_found", "non_dispatch_proven": False}
