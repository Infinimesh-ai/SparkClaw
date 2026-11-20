import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { LinkDomainEvents } from "../src/main/link-domain-events.mjs";

test("each connection reads every domain again, then pushed domain events invalidate only what changed", () => {
  const link = new EventEmitter(), seen = [];
  const events = new LinkDomainEvents({ auth: { link }, onEvents: (event) => seen.push(event.categories) });
  link.emit("event", { topic: "domain", data: { domains: ["mail"] } });
  assert.deepEqual(seen, [], "nothing is delivered before the first connection");
  events.start();
  assert.deepEqual(seen.at(-1), ["tasks", "approvals", "notifications", "settings", "email", "medicine"], "a connection is a full read");
  link.emit("event", { topic: "domain", data: { domains: ["mail", "medicine", "unknown"] } });
  assert.deepEqual(seen.at(-1), ["email", "medicine"]);
  link.emit("event", { topic: "domain", data: { domains: ["unknown"] } });
  link.emit("event", { topic: "sync", data: { cursor: 3 } });
  assert.equal(seen.length, 2, "unrelated topics and unknown domains are ignored");
  events.start();
  assert.equal(seen.length, 3, "a reconnection reads everything again");
  assert.equal(link.listenerCount("event"), 1, "starting again does not add a listener");
  events.close();
  link.emit("event", { topic: "domain", data: { domains: ["settings"] } });
  assert.equal(seen.length, 3);
  assert.equal(link.listenerCount("event"), 0);
});
