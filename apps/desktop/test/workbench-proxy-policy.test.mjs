import assert from "node:assert/strict";
import test from "node:test";

import { proxyAPIAllowed } from "../src/main/workbench-proxy-policy.mjs";

test("R3 desktop exposes the shared presentation APIs", () => {
  for (const pathname of [
    "/readyz",
    "/api/config",
    "/api/owner/language",
    "/api/clients/client-1/revoke",
    "/api/email/conversations",
    "/api/email/drafts/draft-1/send",
    "/api/integrations/localmind/credentials",
    "/api/browser/extension/check",
    "/api/mcp-access/tickets",
    "/api/approvals/pending/approve",
    "/api/memories/export",
    "/api/notifications/events/stream",
    "/api/speech/transcriptions",
  ]) assert.equal(proxyAPIAllowed(pathname), true, pathname);
});

test("R3 desktop keeps conversation and scheduling ownership local", () => {
  for (const pathname of [
    "/api/sessions",
    "/api/sessions/session-1/messages",
    "/api/schedules",
    "/api/documents/upload",
    "/api/documents/available",
    "/api/runs/run-1/feedback",
    "/api/unknown",
  ]) assert.equal(proxyAPIAllowed(pathname), false, pathname);
});
