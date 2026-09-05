export class ControllerError extends Error {
  constructor(
    code,
    message,
    {
      status = 500,
      retryable = false,
      cause,
      diagnosticReason,
      diagnosticContext,
    } = {},
  ) {
    super(message, { cause });
    this.name = "ControllerError";
    this.code = code;
    this.status = status;
    this.retryable = retryable;
    if (diagnosticReason) {
      Object.defineProperty(this, "diagnosticReason", {
        value: diagnosticReason,
        enumerable: false,
      });
    }
    if (diagnosticContext) {
      Object.defineProperty(this, "diagnosticContext", {
        value: diagnosticContext,
        enumerable: false,
      });
    }
  }
}

export function asControllerError(error) {
  if (error instanceof ControllerError) return error;
  return new ControllerError(
    "browser_controller_unavailable",
    "browser controller is temporarily unavailable",
    { status: 503, retryable: true, cause: error },
  );
}

export function invalidRequest(message = "browser controller request is invalid") {
  return new ControllerError("invalid_request", message, { status: 400 });
}

export function publicError(error) {
  const safe = asControllerError(error);
  return {
    error: safe.message,
    code: safe.code,
    retryable: safe.retryable,
  };
}
