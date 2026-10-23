// Only bounded native codes cross the browser-result boundary. Electron's
// message and URL can contain page or credential data and remain local.
export class BrowserNavigationError extends Error {
  constructor(cause) {
    super("Browser navigation failed", { cause });
    this.nativeCode = typeof cause?.code === "string" && /^ERR_[A-Z0-9_]{1,64}$/u.test(cause.code) ? cause.code : "ERR_FAILED";
  }
}

export class BrowserCommandTimeoutError extends Error {
  constructor() { super("Browser command deadline"); }
}

export function browserCommandFailure(error, operation) {
  if (error instanceof BrowserNavigationError && operation === "navigate") return ["failed", { native_error_code: error.nativeCode }, "navigation_failed"];
  if (error instanceof BrowserCommandTimeoutError) return ["failed", {}, "command_timeout"];
  if (error?.code === "browser_snapshot_stale") return ["failed", {}, "browser_snapshot_stale"];
  return ["failed", {}, "command_fenced"];
}
