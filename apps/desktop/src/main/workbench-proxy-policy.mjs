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
  if (pathname === "/readyz" || pathname === "/api/documents/file") return true;
  return PRESENTATION_API_PREFIXES.some((prefix) => matchesPrefix(pathname, prefix));
}
