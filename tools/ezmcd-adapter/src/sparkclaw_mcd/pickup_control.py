"""Private P0 pickup fences and explicit retirement of expired meal intents.

Retirement never changes the original dispatch state or proves its outcome.
This ledger cannot contain payment claims and never performs official writes.
"""
from contextlib import contextmanager
import fcntl
import json
import os
from pathlib import Path
import sqlite3
import time

from ezmcd.models import CheckoutInput, pickup_time

from .canonical import canonical, digest, identifier, integer
from .errors import require, AdapterError
from .quotes import validate_scope


def context(request, scope, purchase, revision, epoch, key):
    validate_scope(scope)
    identifier(purchase); integer(revision); integer(epoch); identifier(key)
    args = CheckoutInput.model_validate(request)
    require(args.account == scope["account_ref"], "member_mismatch")
    bound = digest({"scope": scope, "request": args.model_dump(), "purchase": purchase, "revision": revision, "epoch": epoch})
    return args, bound


@contextmanager
def control(root):
    root = Path(root).absolute()
    require(root.is_dir() and not any(p.is_symlink() for p in (root, *root.parents))
            and root.stat().st_mode & 0o077 == 0, "private_control_directory_required")
    path = root / "pickup-fences.sqlite3"
    require(not path.is_symlink(), "private_control_directory_required")
    existing = path.exists()
    require(existing or not any(root.iterdir()), "pickup_control_missing_recovery_required")
    if existing:
        require(path.is_file() and path.stat().st_mode & 0o077 == 0, "private_control_directory_required")
    lock = os.open(root / "member.lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    conn = None
    try:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise AdapterError("pickup_control_busy") from None
        if not existing:
            fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_RDWR | os.O_NOFOLLOW, 0o600)
            os.close(fd)
        conn = sqlite3.connect(path.as_uri() + "?mode=rw", uri=True, timeout=3)
        conn.execute("PRAGMA synchronous=FULL")
        if existing:
            require(conn.execute("PRAGMA quick_check").fetchone()[0] == "ok", "pickup_control_corrupt")
            require([r[1] for r in conn.execute("PRAGMA table_info(fences)")] == ["member", "key", "digest", "state"],
                    "pickup_control_corrupt")
        conn.executescript("""
          CREATE TABLE IF NOT EXISTS fences(member TEXT,key TEXT,digest TEXT,state TEXT, PRIMARY KEY(member,key));
          CREATE TABLE IF NOT EXISTS retirements(member TEXT,key TEXT,digest TEXT,receipt TEXT, PRIMARY KEY(member,key));
        """)
        conn.commit()
        directory = os.open(root, os.O_RDONLY)
        try: os.fsync(directory)
        finally: os.close(directory)
        yield conn
    finally:
        if conn is not None: conn.close()
        os.close(lock)


def retire_pickup(request, scope, purchase, revision, epoch, key, root, *, clock=time.time):
    args, bound = context(request, scope, purchase, revision, epoch, key)
    member = scope["member_digest"]
    with control(root) as conn:
        row = conn.execute("SELECT digest,state FROM fences WHERE member=? AND key=?", (member, key)).fetchone()
        require(row is not None, "pickup_operation_not_found")
        require(row[0] == bound, "operation_key_conflict")
        old = conn.execute("SELECT digest,receipt FROM retirements WHERE member=? AND key=?", (member, key)).fetchone()
        if old:
            require(old[0] == bound, "operation_key_conflict")
            return json.loads(old[1])
        now = integer(int(clock() * 1000))
        require(pickup_time(args.pickup_at).timestamp() * 1000 <= now, "pickup_intent_not_expired")
        require(row[1] in ("unknown", "receipt_produced"), "pickup_retirement_state_unsupported")
        receipt = {"state": "retired", "operation_id": key, "original_state": row[1],
                   "retired_at_ms": now, "local_only": True, "retirement_confirms_upstream_outcome": False,
                   "automatic_write_retry": False, "official_cart_changed": False,
                   "order_created": False, "payment_attempted": False}
        conn.execute("INSERT INTO retirements VALUES (?,?,?,?)", (member, key, bound, canonical(receipt).decode()))
        conn.commit()
        return receipt
