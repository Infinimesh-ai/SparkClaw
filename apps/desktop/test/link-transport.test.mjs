import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { LinkError, LinkTransport, LINK_REVERSE_OPERATIONS } from "../src/main/link-transport.mjs";
import { fakeHelper, linkError, tick, until } from "./link-fakes.mjs";

async function transport(handlers, options = {}) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "link-transport-"));
  const helper = fakeHelper(handlers);
  const link = new LinkTransport({ stateDirectory: path.join(directory, "link"), spawnProcess: helper.spawnProcess, restartDelayMS: 10, ...options });
  return { link, helper, directory };
}

test("requests fail at once while no path is up and are correlated once one is", async () => {
  const { link, helper } = await transport({ request: async (op, params) => ({ op, echo: params }) });
  await link.start();
  await assert.rejects(link.request("sync.changes", { cursor: 0 }), (error) => error instanceof LinkError && error.code === "backend_unavailable");
  assert.equal(helper.sent.filter(m => m.method === "request").length, 0, "nothing is sent or queued while offline");
  const states = [];
  link.on("state", (state) => states.push(state.path));
  helper.state("iscp");
  await until(() => link.online);
  const [a, b] = await Promise.all([link.request("sync.changes", { cursor: 1 }), link.request("ping", {})]);
  assert.deepEqual(a, { op: "sync.changes", echo: { cursor: 1 } });
  assert.equal(b.op, "ping");
  assert.equal(helper.sent.find(m => m.op === "sync.changes").timeout_ms, 30000);
  helper.state("");
  await until(() => !link.online);
  assert.deepEqual(states, ["iscp", ""]);
  link.close();
});

test("helper errors keep the link contract code and retryability", async () => {
  const { link, helper } = await transport({ request: async () => { throw linkError("busy", "another message is running", true); } });
  await link.start(); helper.state("lan"); await until(() => link.online);
  await assert.rejects(link.request("message.send", {}), (error) => error.code === "busy" && error.retryable === true && /another message/u.test(error.message));
  link.close();
});

test("join and open pass the credential, describe and the reverse list", async () => {
  const { link, helper } = await transport({ join: async () => ({ joined: true, device_id: "desktop-1" }) });
  const describe = { class: "desktop", name: "Mac", app: "SparkX 1", conditions: { browser_surface: true, attachments: true, microphone: true, screen: "large" } };
  assert.equal((await link.join("sparkclaw-link-v1.abc", "Mac")).device_id, "desktop-1");
  await link.open(describe);
  const join = helper.sent.find(m => m.method === "join"), open = helper.sent.find(m => m.method === "open");
  assert.equal(join.credential, "sparkclaw-link-v1.abc"); assert.equal(join.name, "Mac");
  assert.deepEqual(open.describe, describe);
  assert.deepEqual(open.reverse, [...LINK_REVERSE_OPERATIONS]);
  link.close();
});

test("pushed frames become events and reverse calls are answered by the handler", async () => {
  let reverseRequest;
  const replies = [];
  const { link, helper } = await transport({ reverseResponse: (m) => replies.push(m) }, {
    onReverse: async (op, params) => { reverseRequest = { op, params }; if (params.fail) throw new LinkError("unsupported_capability", "no"); return { status: 200, body: { ok: true } }; },
  });
  await link.start();
  const events = [];
  link.on("event", (frame) => events.push(frame));
  helper.event("sync", { cursor: 7 });
  await until(() => events.length === 1);
  assert.deepEqual(events[0].data, { cursor: 7 });
  helper.reverse("s1", "browser.controller.status", { body: {} });
  await until(() => replies.length === 1);
  assert.deepEqual(reverseRequest, { op: "browser.controller.status", params: { body: {} } });
  assert.deepEqual(replies[0].result, { status: 200, body: { ok: true } });
  helper.reverse("s2", "browser.controller.status", { fail: true });
  helper.reverse("s3", "files.delete", {});
  await until(() => replies.length === 3);
  assert.equal(replies.find(r => r.reverse_id === "s2").error.code, "unsupported_capability");
  assert.equal(replies.find(r => r.reverse_id === "s3").error.code, "unsupported_capability", "undeclared operations are refused");
  link.close();
});

test("a crashed helper fails pending calls, goes offline and reopens the link", async () => {
  let opens = 0;
  const { link, helper } = await transport({ open: async () => { opens++; return { ok: true }; }, request: () => new Promise(() => {}) });
  await link.open({ class: "desktop", conditions: {} });
  helper.state("iscp"); await until(() => link.online);
  const pending = link.request("run.status", {});
  await tick(5);
  const first = helper.child;
  first.emit("exit", 1);
  await assert.rejects(pending, (error) => error.code === "backend_unavailable");
  assert.equal(link.online, false);
  await until(() => opens === 2 && helper.children.length === 2);
  link.close();
});
