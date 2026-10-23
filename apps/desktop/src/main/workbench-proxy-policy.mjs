const PRESENTATION_API_PREFIXES = [
  "/api/approvals",
  "/api/artifacts",
  "/api/browser/extension",
  "/api/clients",
  "/api/config",
  "/api/connectors",
  "/api/delivery-endpoints",
  "/api/email",
  "/api/evals",
  "/api/integrations",
  "/api/iscp-pairing",
  "/api/mcp-access",
  "/api/memories",
  "/api/memory-candidates",
  "/api/notification-bindings",
  "/api/notifications",
  "/api/owner",
  "/api/speech",
  "/api/tool-policy",
  "/api/traces",
  "/api/workbench",
  "/api/workspace/screenshots",
];

function matchesPrefix(pathname, prefix) {
  return pathname === prefix || pathname.startsWith(`${prefix}/`);
}

// The workbench Mac renderer may use the same global settings, mail and presentation
// APIs as WebChat. Conversation history, schedules and uploads remain owned by
// the client-local store and are deliberately absent from this allowlist.
export function proxyAPIAllowed(pathname) {
  try { pathname = decodeURIComponent(pathname); } catch { return false; }
  // Website sessions are established in the backend browser. Desktop clients
  // can inspect/check those accounts but cannot initiate their sign-in flow.
  if (/^\/api\/email\/providers\/[^/]+\/login-browser\/?$/u.test(pathname) ||
      /^\/api\/browser\/extension\/ai-platforms\/[^/]+\/login\/?$/u.test(pathname)) return false;
  if (pathname === "/readyz" || pathname === "/api/documents/file") return true;
  return PRESENTATION_API_PREFIXES.some((prefix) => matchesPrefix(pathname, prefix));
}

const TRANSIENT_BACKEND_STATES = new Set(["reconnecting", "service_unavailable"]);

// Views mounted while a saved connection is still being verified (app start,
// update relaunch, reconnect) wait briefly for it instead of failing before
// anything was sent. Any other state, abort or timeout answers immediately.
export function settledBackendState({ state, subscribe, signal, timeoutMS = 15000 }) {
  if (!TRANSIENT_BACKEND_STATES.has(state()) || signal?.aborted) return Promise.resolve(state());
  return new Promise((resolve) => {
    let done = false;
    let unsubscribe = () => {};
    const finish = () => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      unsubscribe();
      signal?.removeEventListener("abort", finish);
      resolve(state());
    };
    const timer = setTimeout(finish, timeoutMS);
    signal?.addEventListener("abort", finish, { once: true });
    unsubscribe = subscribe(() => { if (!TRANSIENT_BACKEND_STATES.has(state())) finish(); });
  });
}
