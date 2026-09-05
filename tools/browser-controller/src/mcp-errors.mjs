import { ControllerError } from "./errors.mjs";

export function clientError(error) {
  if (error instanceof ControllerError) return error;
  return new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
    status: 503,
    retryable: true,
    cause: error,
  });
}

export function extensionRejected() {
  return new ControllerError("browser_extension_rejected", "browser extension rejected the credential", {
    status: 401,
    retryable: false,
  });
}

export function pageStale(detail) {
  return new ControllerError("browser_page_stale", "browser page generation is stale", {
    status: 409,
    cause: new Error(detail),
  });
}

export function clientContractError() {
  return new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
    status: 503,
    retryable: true,
  });
}
