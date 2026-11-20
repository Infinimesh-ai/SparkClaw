import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { LINK_ORIGIN, LinkAuth } from "../src/main/link-auth.mjs";
import { LinkDomainEvents } from "../src/main/link-domain-events.mjs";
import { mutationProtector } from "./fixtures/mutation-protector.mjs";

// A real Gateway (services/gateway/cmd/link-qualification-backend) and the
// real link helper: the desktop joins over the LAN connector with a local
// device directory, with no console and no Relay. Opt in with
// SPARKCLAW_LINK_INTEGRATION=1 (it builds both Go programs).
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const enabled = process.env.SPARKCLAW_LINK_INTEGRATION === "1";

function build(cwd, target, output, env = {}) {
  const result = spawnSync("go", ["build", "-trimpath", "-o", output, target], { cwd: path.join(ROOT, cwd), env: { ...process.env, CGO_ENABLED: "0", ...env }, stdio: "inherit", timeout: 600000 });
  assert.equal(result.status, 0, `${target} build failed`);
}

const until = async (check, timeoutMS = 20000) => {
  const deadline = Date.now() + timeoutMS;
  for (;;) { const value = await check(); if (value) return value; if (Date.now() > deadline) throw new Error("condition not reached"); await new Promise(r => setTimeout(r, 50)); }
};

test("a desktop joined to a LAN-only Gateway reaches conversations and business domains over the link", { skip: !enabled && "set SPARKCLAW_LINK_INTEGRATION=1" }, async t => {
  const root = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "sx-link-it-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  build("services/link", "./cmd/link-helper", path.join(ROOT, "apps/desktop/bin/link-helper"), { GOWORK: "off" });
  const backendBinary = path.join(root, "link-qualification-backend");
  build("services/gateway", "./cmd/link-qualification-backend", backendBinary);
  const user = path.join(root, "user");
  const backend = spawn(backendBinary, ["-dir", path.join(root, "backend"), "-device-state", path.join(user, "link")], { stdio: ["ignore", "pipe", "pipe"] });
  t.after(() => backend.kill("SIGTERM"));
  backend.stderr.on("data", () => {});
  const ready = await new Promise((resolve, reject) => {
    let buffer = "";
    backend.stdout.on("data", chunk => { buffer += chunk; const line = buffer.split("\n")[0]; if (buffer.includes("\n")) resolve(JSON.parse(line)); });
    backend.once("exit", code => reject(new Error(`backend exited ${code}`)));
  });
  assert.equal(ready.event, "link_qualification_backend_ready");

  const auth = new LinkAuth({ stateDirectory: path.join(user, "link"), userDataDirectory: user, vault: mutationProtector, hostName: "integration", appVersion: "test" });
  t.after(() => auth.close());
  await auth.initialize();
  await until(() => auth.status.state === "connected");
  assert.equal(auth.link.path, "lan", "the LAN path is the only one");
  assert.deepEqual(auth.identity(), { deployment_id: ready.deployment_id, owner_id: ready.owner_id, client_id: `link-${ready.device_id}` });
  for (const surface of ["settings_owner", "notifications", "mail_popup", "medicine_box_read", "files", "events"]) assert.equal(auth.status.capabilities.surfaces[surface].enabled, true, surface);

  // A business domain over domain.call, with the settings revision.
  const owner = await auth.authorizedFetch(`${LINK_ORIGIN}/api/owner`);
  assert.equal(owner.status, 200);
  const changes = [];
  const events = new LinkDomainEvents({ auth, onEvents: event => changes.push(event.categories) });
  events.start();
  t.after(() => events.close());
  const patched = await auth.authorizedFetch(`${LINK_ORIGIN}/api/owner`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ display_name: "Integration" }) });
  assert.equal(patched.status, 200);
  assert.equal((await patched.json()).display_name, "Integration");
  await until(() => changes.some(categories => categories.length === 1 && categories[0] === "settings"));
  assert.equal((await auth.authorizedFetch(`${LINK_ORIGIN}/api/notifications`)).status, 200);

  // A conversation run with ordered, numbered events.
  const runEvents = [];
  auth.link.on("event", frame => { if (frame.topic === "run") runEvents.push(frame.data); });
  const header = await auth.link.request("session.create", { title: "integration", client_request_id: "it-1" });
  await auth.link.request("run.subscribe", { session_id: header.id });
  const sent = await auth.link.request("message.send", { session_id: header.id, client_message_id: "it-message-1", content: "你好" });
  assert.ok(sent.run_id);
  await until(() => runEvents.some(event => event.kind === "final" && event.run_id === sent.run_id), 60000);
  const seqs = runEvents.filter(event => event.session_id === header.id).map(event => event.seq);
  assert.deepEqual(seqs, [...seqs].sort((a, b) => a - b), "events arrive in order");
  const replay = await auth.link.request("message.send", { session_id: header.id, client_message_id: "it-message-1", content: "你好", replay_only: true });
  assert.equal(replay.run_id, sent.run_id);
  assert.equal(replay.replayed, true);
});
