"""Reviewed read projections; selection cannot invoke arbitrary upstream tools."""
from ezmcd.models import CheckoutInput, StoreSearch
from decimal import Decimal, InvalidOperation

from .canonical import fields, integer
from .errors import require
from .receipt import encode_official_json


def _menu(provider, params):
    fields(params, ("request",), ("keyword", "offset", "limit"))
    offset, limit = params.get("offset", 0), params.get("limit", 20)
    integer(offset, 0, 10000); integer(limit, 1, 50)
    result = provider.menu(CheckoutInput.model_validate(params["request"]), params.get("keyword", ""))
    products = result["products"]
    result.update(products=products[offset:offset + limit], offset=offset, limit=limit,
                  total=len(products), has_more=offset + limit < len(products), complete_menu=False)
    return result


def _context(method, *extra):
    def call(provider, params):
        fields(params, ("request", *extra))
        return getattr(provider, method)(CheckoutInput.model_validate(params["request"]), *[params[k] for k in extra])
    return call


READERS = {
    "membership_assets": lambda p, a: (fields(a, ()) or p.assets()),
    "stores_favorites": lambda p, a: (fields(a, ()) or p.favorites()),
    "cities_list": lambda p, a: (fields(a, ("keyword",)) or p.cities(a["keyword"])),
    "stores_search": lambda p, a: p.search_stores(store_search(a)),
    "store_get": lambda p, a: (fields(a, ("store_code",)) or p.store(a["store_code"])),
    "pickup_slots": lambda p, a: (fields(a, ("store_code",)) or p.pickup_slots(a["store_code"])),
    "pickup_methods": _context("pickup_methods"),
    "menu_list": _menu,
    "product_detail": _context("product_detail", "product_code", "offer_id", "coupon_ref", "variant_of"),
    "coupons_list": _context("coupons", "keyword"),
    "coupon_products": _context("coupon_products", "coupon_ref"),
    "cart_get": _context("cart"),
}


def store_search(params):
    value = dict(params)
    for name, bound in (("latitude", 90), ("longitude", 180)):
        if name in value:
            coordinate = value[name]
            require(type(coordinate) is str and len(coordinate) <= 32, "invalid_store_coordinates")
            try:
                number = Decimal(coordinate)
                require(number.is_finite() and -bound <= number <= bound, "invalid_store_coordinates")
                value[name] = float(number)
            except InvalidOperation:
                require(False, "invalid_store_coordinates")
    return StoreSearch.model_validate(value)


def read(provider, operation, params):
    require(operation in READERS, "operation_not_permitted")
    # Each operation verifies the imported member, rather than assuming that a
    # portable session still represents the original member forever.
    provider.wallet()
    for request in (params, params.get("request", {})):
        if "account" in request:
            require(request["account"] == provider.account["alias"], "member_mismatch")
    result = READERS[operation](provider, params)
    require(isinstance(result, dict) and result.get("ok") is not False, "upstream_business_failure")
    # Store metadata contains fractional coordinates. Preserve the complete
    # bounded result as JSON inside the integer-only private protocol, just as
    # for official receipts. Product-facing projections belong to P1.
    return {"schema_version": 1, "result_encoding": "bounded-json-v1",
            "result_json": encode_official_json(result, 64 * 1024)}
