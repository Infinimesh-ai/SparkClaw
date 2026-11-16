"""Explicit local handoff source. Reading it never initializes a fresh ledger."""
from pathlib import Path
import sqlite3
import stat

from ezmcd.storage import Store, read_private

from .compatibility import material_from_account
from .errors import require


def open_existing(root, alias):
    root = Path(root).expanduser().absolute()
    require(not any(p.is_symlink() for p in (root, *root.parents)), "private_account_source_required")
    for path in (root, root / "accounts", root / "locks", root / "journal.sqlite3"):
        info = path.stat()
        require(not path.is_symlink() and not info.st_mode & 0o077, "private_account_source_required")
    require(stat.S_ISREG((root / "journal.sqlite3").stat().st_mode), "existing_ledger_required")
    with sqlite3.connect((root / "journal.sqlite3").as_uri() + "?mode=ro", uri=True) as db:
        row = db.execute("SELECT identity FROM accounts WHERE alias=?", (alias,)).fetchone()
    require(row is not None and type(row[0]) is str and len(row[0]) == 64
            and all(c in "0123456789abcdef" for c in row[0]), "official_account_unavailable")
    record = read_private(root / "accounts" / (row[0] + ".json"))
    require(record.get("identity") == row[0] and record.get("alias") == alias, "member_mismatch")
    # Only existing account/lock/read methods are used. Store.__init__ would
    # create directories/tables and could conceal a missing original ledger.
    store = object.__new__(Store)
    store.root = root
    return store, record


def acquire(root, alias):
    store, record = open_existing(root, alias)
    with store.lock(record["identity"]):
        current = store.account(alias)
        require(current == record, "official_account_changed")
        return record["binary"], material_from_account(current)
