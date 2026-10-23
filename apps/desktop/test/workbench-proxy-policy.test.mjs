import assert from "node:assert/strict";
import test from "node:test";

import { proxyAPIAllowed, settledBackendState } from "../src/main/workbench-proxy-policy.mjs";

test("workbench desktop exposes the shared presentation APIs", () => {
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

test("workbench desktop keeps conversation and scheduling ownership local", () => {
  for (const pathname of [
    "/api/sessions",
    "/api/sessions/session-1/messages",
    "/api/schedules",
    "/api/documents/upload",
    "/api/v1/mail/attachments",
    "/api/v1/mail/attachments/object-id",
    "/api/documents/available",
    "/api/runs/run-1/feedback",
    "/api/unknown",
  ]) assert.equal(proxyAPIAllowed(pathname), false, pathname);
});

test("desktop website sign-in stays on the backend while token bindings and checks remain available", () => {
  for (const path of [
    "/api/email/providers/outlook/login-browser",
    "/api/email/providers/gmail/%6cogin-browser",
    "/api/browser/extension/ai-platforms/chatgpt/login",
    "/api/browser/extension/ai-platforms/gemini/login/",
  ]) assert.equal(proxyAPIAllowed(path), false, path);
  for (const path of [
    "/api/email/providers/outlook/check",
    "/api/browser/extension/ai-platforms/chatgpt/check",
    "/api/browser/extension/token",
    "/api/notification-bindings/weixin/start",
    "/api/notification-bindings/binding-1/login-url",
    "/api/iscp-pairing/start",
  ]) assert.equal(proxyAPIAllowed(path), true, path);
});

function backendStates(initial) {
  let current = initial;
  const waiters = new Set();
  return {
    state: () => current,
    subscribe: (waiter) => { waiters.add(waiter); return () => waiters.delete(waiter); },
    set: (next) => { current = next; for (const waiter of [...waiters]) waiter(); },
    waiters,
  };
}

test("proxy requests made during startup verification wait for the saved connection", async () => {
  const states = backendStates("reconnecting");
  const settled = settledBackendState({ ...states, timeoutMS: 1000 });
  states.set("service_unavailable");
  states.set("reconnecting");
  states.set("connected");
  assert.equal(await settled, "connected");
  assert.equal(states.waiters.size, 0);
});

test("settled proxy states answer at once and transient waits stay bounded", async () => {
  for (const state of ["connected", "locked", "identity_conflict", "incomplete_setup"]) {
    assert.equal(await settledBackendState({ ...backendStates(state), timeoutMS: 1000 }), state);
  }
  const reconnecting = backendStates("reconnecting");
  assert.equal(await settledBackendState({ ...reconnecting, timeoutMS: 10 }), "reconnecting");
  assert.equal(reconnecting.waiters.size, 0);
  const locked = backendStates("reconnecting");
  const lockedWait = settledBackendState({ ...locked, timeoutMS: 1000 });
  locked.set("invalid_authentication");
  assert.equal(await lockedWait, "invalid_authentication");
  const aborted = backendStates("service_unavailable");
  const controller = new AbortController();
  const abortedWait = settledBackendState({ ...aborted, signal: controller.signal, timeoutMS: 1000 });
  controller.abort();
  assert.equal(await abortedWait, "service_unavailable");
  assert.equal(aborted.waiters.size, 0);
});
