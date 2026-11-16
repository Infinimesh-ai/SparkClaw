"""Public errors never include upstream bodies, exception messages or secrets."""


class AdapterError(Exception):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


def require(condition, code):
    if not condition:
        raise AdapterError(code)
