"""Canonical v1: sorted ASCII keys, UTF-8 strings, safe integers, no floats.

Null is explicit, arrays retain order, Unicode is not normalized. Timestamps
use integer epoch milliseconds. Duplicate keys and non-finite numbers reject.
This deliberately narrower JSON domain has identical Python/JS/Go encodings.
"""
import hashlib
import json

from .errors import AdapterError, require

MAX_BYTES = 256 * 1024
MAX_INTEGER = 2**53 - 1


def _validate(value, depth=0):
    require(depth <= 32, "payload_depth_exceeded")
    if value is None or type(value) is bool:
        return
    if type(value) is int:
        require(abs(value) <= MAX_INTEGER, "integer_out_of_range")
    elif type(value) is str:
        require(not any(0xD800 <= ord(c) <= 0xDFFF for c in value), "invalid_unicode")
    elif type(value) is list:
        require(len(value) <= 4096, "array_limit_exceeded")
        for item in value:
            _validate(item, depth + 1)
    elif type(value) is dict:
        require(len(value) <= 512, "object_limit_exceeded")
        for key, item in value.items():
            require(type(key) is str and key.isascii() and 0 < len(key) <= 128
                    and all(32 <= ord(c) < 127 for c in key), "invalid_object_key")
            _validate(item, depth + 1)
    else:
        raise AdapterError("invalid_json_type")


def canonical(value, limit=MAX_BYTES):
    _validate(value)
    raw = json.dumps(value, ensure_ascii=False, sort_keys=True,
                     separators=(",", ":"), allow_nan=False).encode("utf-8")
    require(len(raw) <= limit, "payload_too_large")
    return raw


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def _object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate_json_key")
        result[key] = value
    return result


def loads(raw, limit=MAX_BYTES):
    require(isinstance(raw, bytes) and len(raw) <= limit, "payload_too_large")
    try:
        value = json.loads(raw, object_pairs_hook=_object,
                           parse_constant=lambda _: (_ for _ in ()).throw(AdapterError("invalid_json_number")))
        canonical(value, limit)
        return value
    except (ValueError, UnicodeError, RecursionError):
        raise AdapterError("invalid_json") from None


def fields(value, required, optional=()):
    require(type(value) is dict and set(required) <= value.keys()
            and value.keys() <= set(required) | set(optional), "invalid_fields")


def integer(value, minimum=1, maximum=MAX_INTEGER):
    require(type(value) is int and minimum <= value <= maximum, "invalid_integer")
    return value


def identifier(value):
    import re
    require(type(value) is str and re.fullmatch(r"[A-Za-z0-9_-]{1,128}", value), "invalid_identifier")
    return value


def hash_value(value):
    import re
    require(type(value) is str and re.fullmatch(r"[a-f0-9]{64}", value), "invalid_digest")
    return value
