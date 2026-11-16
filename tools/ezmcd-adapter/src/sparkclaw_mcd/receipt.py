"""Opaque official JSON evidence inside the integer-only envelope domain.

Official confirmation receipts include fractional coordinates and other
non-money metadata. Preserve their JSON as a bounded UTF-8 string; never round
them, silently drop fields, or reinterpret them as canonical money values.
The envelope and receipt digests bind the exact string on both hosts.
"""
import json
import math

from .errors import require, AdapterError

MAX_RECEIPT_BYTES = 128 * 1024


def _object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "pickup_receipt_duplicate_key")
        result[key] = value
    return result


def _check(value, depth=0):
    require(depth <= 24, "pickup_receipt_depth_exceeded")
    if type(value) is float:
        require(math.isfinite(value), "pickup_receipt_invalid_number")
    elif type(value) is dict:
        require(len(value) <= 512, "pickup_receipt_too_large")
        for v in value.values():
            _check(v, depth + 1)
    elif type(value) is list:
        require(len(value) <= 4096, "pickup_receipt_too_large")
        for v in value:
            _check(v, depth + 1)


def encode_official_json(value, limit=MAX_RECEIPT_BYTES):
    _check(value)
    raw = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
    require(len(raw.encode("utf-8")) <= limit, "official_json_too_large")
    return raw


def encode_receipt(response):
    raw = encode_official_json(response)
    decode_receipt(raw)
    return raw


def decode_receipt(raw):
    require(type(raw) is str and len(raw.encode("utf-8")) <= MAX_RECEIPT_BYTES, "pickup_receipt_too_large")
    try:
        result = json.loads(raw, object_pairs_hook=_object)
    except (ValueError, RecursionError):
        raise AdapterError("pickup_receipt_invalid_json") from None
    _check(result)
    require(type(result) is dict and result.keys() == {"http_status", "response"}, "pickup_receipt_invalid_fields")
    require(result["http_status"] == 200 and type(result["response"]) is dict
            and result["response"].get("success") is True, "pickup_switch_not_confirmed")
    return result
