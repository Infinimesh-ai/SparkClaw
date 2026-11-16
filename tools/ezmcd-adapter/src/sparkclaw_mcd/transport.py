"""Selection's v4 transport. No refresh, order, cashier or wallet token."""
from datetime import datetime, timezone
import json
import re
import secrets
import time
from urllib.parse import quote, urlencode

from ezmcd.provider import McdProvider
from ezmcd.protocol.signing import sign_v4
from ezmcd.transport import Transport, READS

from .compatibility import validate_material, member_digest, member_id
from .canonical import integer
from .errors import require, AdapterError

READ_PATHS = frozenset((
    "/bff/member/mwallet/balance", "/bff/promotion/coupons/rightCards",
    "/bff/promotion/coupons/menu/available", "/bff/promotion/coupons/products",
    "/bff/store/stores/collection/list", "/bff/store/cities/group", "/bff/store/stores",
    "/bff/store/stores/vicinity", "/bff/spc/menu", "/bff/cart/carts", "/bff/order/confirmation/info"))


def read_endpoint(method, path):
    if method == "GET":
        if path in READ_PATHS:
            return READS[path]
        if re.fullmatch(r"/bff/store/stores/[0-9]{7}", path):
            return ("1004", "101")
        if re.fullmatch(r"/bff/store/stores/[0-9]{7}/menu/reservationTimeOption", path):
            return None
    if method == "POST" and re.fullmatch(r"/bff/spc/products/detail/[A-Za-z0-9]{1,20}", path):
        return ("1001", "101")
    raise AdapterError("endpoint_not_permitted")


class SelectionTransport:
    def __init__(self, material, *, deadline_ms, requests=64, clock=time.time):
        self.material = validate_material(material)
        self.clock = clock
        self.deadline_ms = integer(deadline_ms)
        require(0 < deadline_ms - int(clock() * 1000) <= 120000, "invalid_execution_deadline")
        self.remaining = integer(requests, 1, 128)

    def send(self, method, path, params=None, body=None):
        business = read_endpoint(method, path)
        return self._transmit(method, path, business, params, body)

    def _transmit(self, method, path, business, params=None, body=None):
        now = self.clock()
        require(now * 1000 < self.deadline_ms and self.remaining > 0, "request_budget_exhausted")
        self.remaining -= 1
        material = self.material
        require(material is not None, "session_closed")
        headers = material["headers"].copy()
        headers.update({"User-Agent": "McdApp (iOS)", "Accept": "application/json",
                        "Content-Type": "application/json; charset=utf-8", "sv": "v4", "x-mcd-gw-v": "1",
                        "st": f"{now:.0f}", "nonce": str(int(now * 1000)) + str(secrets.randbelow(1000000))})
        if business:
            headers.update(biz_from=business[0], biz_scenario=business[1])
        if path == "/bff/promotion/coupons/rightCards":
            headers["biz_form"] = headers.pop("biz_from")
        query = urlencode(sorted((params or {}).items()), quote_via=quote, doseq=True)
        raw = b"" if body is None else json.dumps(body, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode()
        require(len(raw) <= 256 * 1024 and len(query) <= 8192, "request_too_large")
        headers.update(sign_v4(headers, material["access_key"], material["secret"],
                               datetime.fromtimestamp(now, timezone.utc), method, path, query, raw))
        # The upstream fixed-host transport disables proxies, redirects and retries.
        result = Transport._open(method, "https://api.mcd.cn" + path + ("?" + query if query else ""),
                                 headers, raw if body is not None else None)
        require(self.clock() * 1000 < self.deadline_ms, "execution_deadline_exceeded")
        return result

    def close(self):
        self.material = None
        self.remaining = 0


class PickupProbeTransport(SelectionTransport):
    """G2's explicitly requested one-shot pickup selection, never a purchase."""
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.switch_claimed = False

    def send(self, method, path, params=None, body=None):
        if method == "PUT" and path == "/bff/order/confirmation/eattype/switch":
            require(not self.switch_claimed, "pickup_switch_already_dispatched")
            self.switch_claimed = True
            return self._transmit(method, path, ("1029", "202"), params, body)
        return super().send(method, path, params, body)


def provider(material, account_ref, deadline_ms, *, pickup_probe=False, requests=64):
    material = validate_material(material)
    account = {"alias": account_ref, "identity": member_digest(material), "provider": "mcd-cn",
               "profile": {"member_source": "balance_response", "fields": {"meddyId": member_id(material)}}}
    transport = PickupProbeTransport if pickup_probe else SelectionTransport
    return McdProvider(account, transport=transport(material, deadline_ms=deadline_ms, requests=requests))
