import assert from "node:assert/strict";
import crypto from "node:crypto";
import test from "node:test";

import {
  ELECTRON_ADAPTER_PROTOCOL_VERSION,
  parseConnectionBinding,
  parseOpenRequest,
  parsePersonalRequest,
  parsePlaywrightConnectionURL,
} from "../src/browser/protocol.mjs";

const binding = Object.freeze({
  task_id: "task-phase1",
  session_id: `session_${"1".repeat(32)}`,
  controller_generation: 7,
  session_generation: 2,
  page_generation: 1,
});

test("Electron adapter parses the pinned relay envelope without treating it as a loaded extension", () => {
  const token = "private-phase-one-token";
  const url = new URL("chrome-extension://mmlmfjhmonkocbjadbfplnigmagldckm/connect.html");
  url.searchParams.set("mcpRelayUrl", "ws://127.0.0.1:32100/extension/12345678-1234-4234-8234-123456789abc");
  url.searchParams.set("client", JSON.stringify({ name: "playwright-mcp" }));
  url.searchParams.set("protocolVersion", "2");
  url.searchParams.set("token", token);
  assert.deepEqual(parsePlaywrightConnectionURL(url.toString()), {
    relayURL: "ws://127.0.0.1:32100/extension/12345678-1234-4234-8234-123456789abc",
    tokenHash: crypto.createHash("sha256").update(token).digest("hex"),
    clientName: "playwright-mcp",
  });
});

test("Electron adapter requires an exact task and generation binding", () => {
  assert.deepEqual(parseConnectionBinding(binding), binding);
  assert.throws(() => parseConnectionBinding({ ...binding, web_contents_id: 12 }));
  assert.throws(() => parseConnectionBinding({ ...binding, session_generation: 0 }));
  assert.throws(() => parseConnectionBinding({ ...binding, session_id: "foreign" }));
});

test("Electron open requests require a one-time credential-shaped value", () => {
  const request = {
    schema_version: 1,
    operation: "openConnection",
    runtime_kind: "electron",
    protocol_version: ELECTRON_ADAPTER_PROTOCOL_VERSION,
    connection_credential: "A".repeat(43),
    connection_url: "redacted-in-this-unit-test",
    binding,
  };
  assert.deepEqual(parseOpenRequest(request), {
    credential: request.connection_credential,
    connectionURL: request.connection_url,
    binding,
  });
  assert.throws(() => parseOpenRequest({ ...request, connection_credential: "short" }));
  assert.throws(() => parseOpenRequest({ ...request, runtime_kind: "extension" }));
});

test("personal navigation is HTTPS-only outside an explicit loopback qualification", () => {
  const base = {
    schema_version: 1,
    operation: "openPersonalPage",
    runtime_kind: "electron",
    runtime_secret: "S".repeat(43),
  };
  assert.equal(parsePersonalRequest({ ...base, url: "https://example.test/login" }).url,
    "https://example.test/login");
  assert.throws(() => parsePersonalRequest({ ...base, url: "http://127.0.0.1:8080/login" }));
  assert.equal(parsePersonalRequest({ ...base, url: "http://127.0.0.1:8080/login" },
    { allowLoopbackHTTP: true }).url, "http://127.0.0.1:8080/login");
});
