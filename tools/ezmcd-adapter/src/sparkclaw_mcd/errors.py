"""Public errors never include upstream bodies, exception messages or secrets."""
import json
from pathlib import Path


UPSTREAM_ERRORS = frozenset(json.loads(Path(__file__).with_name("upstream-error-codes.json").read_text()))


class AdapterError(Exception):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


def require(condition, code):
    if not condition:
        raise AdapterError(code)
