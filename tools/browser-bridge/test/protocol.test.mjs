import assert from "node:assert/strict";
import test from "node:test";

import {
  boundedClientName,
  exactKeys,
  parseRelayURL,
  REJECTION_MARKER,
  rejectRelayConnection,
} from "../src/protocol.mjs";

test("relay URL accepts only the pinned loopback path shape", () => {
  const valid = "ws://127.0.0.1:4123/extension/123e4567-e89b-42d3-a456-426614174000";
  assert.equal(parseRelayURL(valid), valid);
  for (const value of [
    "wss://127.0.0.1:4123/extension/123e4567-e89b-42d3-a456-426614174000",
    "ws://example.com/extension/123e4567-e89b-42d3-a456-426614174000",
    "ws://127.0.0.1:4123/other/123e4567-e89b-42d3-a456-426614174000",
    "ws://127.0.0.1:4123/extension/123e4567-e89b-42d3-a456-426614174000?x=1",
  ]) assert.throws(() => parseRelayURL(value));
});

test("message and client projections reject extra authority", () => {
  assert.equal(exactKeys({ type: "keepalive" }, ["type"]), true);
  assert.equal(exactKeys({ type: "keepalive", tab: 7 }, ["type"]), false);
  assert.equal(boundedClientName("  client\u0000name  "), "clientname");
  assert.equal(boundedClientName("x".repeat(100)).length, 64);
});

test("credential rejection sends only the fixed marker to a validated relay", async () => {
  const valid = "ws://127.0.0.1:4123/extension/123e4567-e89b-42d3-a456-426614174000";
  const sockets = [];
  class FakeWebSocket {
    constructor(url) {
      this.url = url;
      sockets.push(this);
      setImmediate(() => this.onopen());
    }

    close(code, reason) {
      this.closed = { code, reason };
      setImmediate(() => this.onclose());
    }

    send(value) {
      this.sent = value;
    }
  }

  await rejectRelayConnection(valid, FakeWebSocket);

  assert.equal(sockets.length, 1);
  assert.equal(sockets[0].url, valid);
  assert.deepEqual(JSON.parse(sockets[0].sent), { id: -1, error: REJECTION_MARKER });
  assert.deepEqual(sockets[0].closed, { code: 4001, reason: REJECTION_MARKER });
  await assert.rejects(rejectRelayConnection("ws://example.com/rejected", FakeWebSocket));
  assert.equal(sockets.length, 1);
});
