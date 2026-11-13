"""Private leased read-session contract; no renewal or purchasing on Linux."""
from copy import deepcopy
import time

from . import ADAPTER_REVISION
from .canonical import canonical, digest, fields, hash_value, identifier, integer
from .compatibility import validate_material, member_digest
from .errors import require
from .reads import READERS, read
from .transport import provider
from .cart_session import CART_OPERATIONS

SELECTION_OPERATIONS = frozenset(READERS) | CART_OPERATIONS | {"checkout_quote"}

IDENTITY_FIELDS = ("deployment_id", "owner_id", "client_id", "installation_id", "account_ref", "member_digest")
SESSION_FIELDS = ("schema_version", "canonicalization", "adapter_revision", "identity", "connection_id",
                  "provision_request_id", "credential_revision", "grant_revision", "issued_at_ms",
                  "session_not_after_ms", "operations", "max_requests", "material", "material_digest", "envelope_digest")
MAX_LEASE_MS = 86400000


def identity(value):
    fields(value, IDENTITY_FIELDS)
    for key in IDENTITY_FIELDS:
        (hash_value if key == "member_digest" else identifier)(value[key])
    return value


def seal(value):
    value = deepcopy(value)
    value["material_digest"] = digest(value["material"])
    value["envelope_digest"] = digest({k: v for k, v in value.items() if k != "envelope_digest"})
    return value


def validate(value, now_ms=None):
    canonical(value, 48 * 1024)
    fields(value, SESSION_FIELDS)
    require(type(value["schema_version"]) is int and value["schema_version"] == 1
            and value["canonicalization"] == "mcd-json-v1"
            and value["adapter_revision"] == ADAPTER_REVISION, "session_version_unsupported")
    identity(value["identity"])
    for key in ("connection_id", "provision_request_id"):
        identifier(value[key])
    for key in ("credential_revision", "grant_revision", "issued_at_ms", "session_not_after_ms"):
        integer(value[key])
    integer(value["max_requests"], 1, 128)
    operations = value["operations"]
    require(type(operations) is list and 0 < len(operations) <= len(SELECTION_OPERATIONS)
            and all(type(op) is str and op in SELECTION_OPERATIONS for op in operations)
            and operations == sorted(set(operations)), "session_scope_not_permitted")
    material = validate_material(value["material"])
    require(member_digest(material) == value["identity"]["member_digest"], "session_member_mismatch")
    require(value["material_digest"] == digest(material)
            and value["envelope_digest"] == digest({k: v for k, v in value.items() if k != "envelope_digest"}),
            "session_digest_mismatch")
    now = int(time.time() * 1000) if now_ms is None else integer(now_ms)
    require(value["issued_at_ms"] <= now < value["session_not_after_ms"]
            and 0 < value["session_not_after_ms"] - value["issued_at_ms"] <= MAX_LEASE_MS,
            "session_expired_or_clock_untrusted")
    return value


def issue(material, binding, connection_id, provision_request_id, credential_revision, grant_revision,
          *, lease_ms=MAX_LEASE_MS, operations=None, max_requests=64):
    identity(binding)
    validate_material(material)
    require(member_digest(material) == binding["member_digest"], "session_member_mismatch")
    integer(lease_ms, 1, MAX_LEASE_MS)
    now = int(time.time() * 1000)
    client = provider(material, binding["account_ref"], now + 90000)
    try:
        client.wallet()
    finally:
        client.http.close()
    # The lease begins only after real member verification. No upstream expiry
    # is invented; this is SparkClaw's maximum usage lease, not a scoped token.
    issued = int(time.time() * 1000)
    require(issued >= now, "session_expired_or_clock_untrusted")
    value = seal({"schema_version": 1, "canonicalization": "mcd-json-v1", "adapter_revision": ADAPTER_REVISION,
                  "identity": binding, "connection_id": connection_id, "provision_request_id": provision_request_id,
                  "credential_revision": credential_revision, "grant_revision": grant_revision,
                  "issued_at_ms": issued, "session_not_after_ms": issued + lease_ms,
                  "operations": sorted(READERS) if operations is None else operations,
                  "max_requests": max_requests, "material": material})
    return validate(value)


def execute(value, execution, operation, arguments):
    admit(value, execution, operation, READERS)
    client = provider(value["material"], value["identity"]["account_ref"],
                      min(execution["deadline_ms"], value["session_not_after_ms"], int(time.time()*1000)+90000), requests=execution["max_requests"])
    try:
        return read(client, operation, arguments)
    finally:
        client.http.close()


def admit(value, execution, operation, allowed):
    validate(value)
    fields(execution, ("identity", "connection_id", "execution_id", "credential_revision", "grant_revision", "deadline_ms", "max_requests"))
    identity(execution["identity"])
    identifier(execution["execution_id"])
    now = int(time.time() * 1000)
    integer(execution["deadline_ms"])
    integer(execution["max_requests"], 1, value["max_requests"])
    require(all(execution[key] == value[key] for key in ("identity", "connection_id", "credential_revision"))
            and type(execution["grant_revision"]) is int and 1 <= execution["grant_revision"] <= value["grant_revision"],
            "session_scope_mismatch")
    require(operation in value["operations"] and operation in allowed, "session_scope_not_permitted")
    require(0 < execution["deadline_ms"] - now <= 900000, "invalid_execution_deadline")


def health(value):
    validate(value)
    now = int(time.time() * 1000)
    client = provider(value["material"], value["identity"]["account_ref"],
                      min(now + 90000, value["session_not_after_ms"]), requests=min(4, value["max_requests"]))
    try:
        client.wallet()
    finally:
        client.http.close()
    return {"member_verified": True, "checkout_qualified": False}
