import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { controllerClient, measure, parseOptions as controlOptions, percentile, sessionProcesses, summarize } from "../mail-control-gate.mjs";
import { parseOptions as objectOptions, phases, resolveTarget, run, summarizeTraces, timedCall } from "../iscp-object-gate.mjs";
import { ISCPObjectClient } from "../../apps/desktop/src/main/iscp-object-client.mjs";

const TASK = "op3.1.8.928631bcbbe8649474e747c73f609861";
const privateTemp = t => {
  const directory = fs.mkdtempSync(path.join(fs.realpathSync(os.tmpdir()), "sparkclaw-gate-"));
  fs.chmodSync(directory, 0o700);
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  return directory;
};

test("nearest-rank percentiles and summaries", () => {
  const values = Array.from({ length: 20 }, (_, index) => index + 1);
  assert.equal(percentile(values, 0.5), 10);
  assert.equal(percentile(values, 0.95), 19);
  assert.equal(percentile([7], 0.95), 7);
  assert.equal(percentile([], 0.5), null);
  assert.deepEqual(summarize([3, 1, 2]), { n: 3, min: 1, p50: 2, p95: 3, max: 3, mean: 2 });
  assert.deepEqual(summarize([]), { n: 0 });
});

test("control gate accepts only an admitted send lookup on an absolute socket within the idle bound", () => {
  const options = controlOptions(["--socket", "/run/c.sock", "--provider", "qq_mail", "--task-id", TASK]);
  assert.deepEqual([options.samples, options.warmup, options.intervalMS, options.gateMS], [100, 3, 100, 20]);
  for (const args of [[], ["--socket", "relative", "--provider", "qq_mail", "--task-id", TASK], ["--socket", "/s", "--provider", "imap", "--task-id", TASK],
    ["--socket", "/s", "--provider", "gmail", "--task-id", "op3.1.1.short"], ["--socket", "/s", "--provider", "gmail", "--task-id", TASK, "--interval-ms", "30000"],
    ["--socket", "/s", "--provider", "gmail", "--task-id", TASK, "--action", "ack"]]) assert.throws(() => controlOptions(args));
});

test("control gate times every lookup after warm-up and fails on any non-executor answer or slow p95", async () => {
  const replies = [];
  const client = { request: async (method, route, body) => {
    if (method === "GET") return { status: 200, body: { profile_id: "default" } };
    replies.push(body);
    return replies.length === 4 ? { status: 200, body: { outcome: "not_admitted" } } : { status: 200, body: { outcome: "known", task: { id: "t" } } };
  }, close() {} };
  let clock = 0;
  const now = () => (clock += 5);
  const report = await measure({ provider: "gmail", taskID: TASK, samples: 3, warmup: 1, intervalMS: 0, gateMS: 20 }, { client, now, processes: () => [1, 2] });
  assert.equal(replies.length, 4);
  assert.deepEqual(replies[0], { profile_id: "default", action: "lookup", provider: "gmail", operation: "send", task_id: TASK });
  assert.deepEqual(report.samples_ms, [5, 5]);
  assert.deepEqual(report.failures, [{ index: 3, status: 200, outcome: "not_admitted", code: null }]);
  assert.equal(report.passed, false);
  const fast = await measure({ provider: "gmail", taskID: TASK, samples: 2, warmup: 0, intervalMS: 0, gateMS: 20 },
    { client: { request: async method => method === "GET" ? { status: 200, body: { profile_id: "p" } } : { status: 200, body: { outcome: "known", task: {} } }, close() {} }, now: (() => { let c = 0; return () => (c += 25); })(), processes: () => null });
  assert.equal(fast.summary_ms.p95, 25);
  assert.equal(fast.passed, false, "p95 above the gate fails");
});

test("control client speaks HTTP/1.1 JSON over the Controller unix socket", async t => {
  const directory = privateTemp(t), socketPath = path.join(directory, "controller.sock");
  const seen = [];
  const server = http.createServer((request, response) => {
    let raw = "";
    request.on("data", chunk => { raw += chunk; });
    request.on("end", () => {
      seen.push({ method: request.method, url: request.url, type: request.headers["content-type"], body: raw && JSON.parse(raw) });
      response.setHeader("content-type", "application/json");
      response.end(JSON.stringify(request.url === "/v1/health" ? { profile_id: "default" } : { outcome: "known", task: { id: "x" } }));
    });
  });
  await new Promise(resolve => server.listen(socketPath, resolve));
  t.after(() => server.close());
  const report = await measure({ socket: socketPath, provider: "outlook", taskID: TASK, samples: 2, warmup: 1, intervalMS: 0, gateMS: 1000 }, { client: controllerClient(socketPath), processes: () => [] });
  assert.equal(report.passed, true);
  assert.equal(report.samples_ms.length, 2);
  assert.equal(seen[0].url, "/v1/health");
  assert.deepEqual(seen.slice(1).map(row => [row.method, row.url, row.type, row.body.action]), Array(3).fill(["POST", "/v1/application-lifecycle", "application/json", "lookup"]));
});

test("resident session processes are recognized from /proc command lines", t => {
  const proc = privateTemp(t);
  for (const [pid, args] of [["10", ["python", "-m", "app_cli", "--machine", "--session"]], ["11", ["python", "-m", "app_cli", "--machine"]], ["12", ["node", "client.mjs"]]]) {
    fs.mkdirSync(path.join(proc, pid)); fs.writeFileSync(path.join(proc, pid, "cmdline"), args.join("\0") + "\0");
  }
  fs.mkdirSync(path.join(proc, "self"));
  assert.deepEqual(sessionProcesses(proc), [10]);
  assert.equal(sessionProcesses(path.join(proc, "missing")), null);
});

test("object gate requires one explicit target and refuses production helper configs without consent", async t => {
  assert.throws(() => objectOptions(["--output", "/o"]));
  assert.throws(() => objectOptions(["--lab", "/l", "--helper-config", "/h", "--output", "/o"]));
  assert.throws(() => objectOptions(["--lab", "/l", "--output", "relative"]));
  assert.equal(objectOptions(["--lab", "/l", "--output", "/o"]).sizeMiB, 10);
  const directory = privateTemp(t), helper = path.join(directory, "helper.json");
  fs.writeFileSync(helper, JSON.stringify({ relay_profile: "production", expected_installation_id: crypto.randomUUID() }));
  await assert.rejects(resolveTarget({ helperConfig: helper, allowProduction: false }), /owner approval/u);
  assert.equal((await resolveTarget({ helperConfig: helper, allowProduction: true })).link, "production helper config");
  fs.writeFileSync(path.join(directory, "run.json"), JSON.stringify({ schema_version: 1, relay_profile: "local-lab" }));
  await assert.rejects(resolveTarget({ lab: directory }), /prepare-expansion/u);
});

test("timed calls record each RPC, peak concurrency and per-operation spans", async () => {
  let clock = 0;
  const timed = timedCall(async operation => { clock += operation === "slow" ? 10 : 1; await new Promise(resolve => setImmediate(resolve)); return operation; }, () => clock);
  await Promise.all([timed.call("transfer.open", { transfer_id: "a" }), timed.call("slow"), timed.call("slow")]);
  assert.equal(timed.state.maxInflight, 3);
  assert.deepEqual([...timed.state.openTransfers], ["a"]);
  const summary = phases(timed.state.events);
  assert.equal(summary.slow.count, 2);
  assert.equal(summary["transfer.open"].failed, 0);
  await timed.call("transfer.commit", { transfer_id: "a" });
  assert.equal(timed.state.openTransfers.size, 0);
});

test("trace summaries group private latency rows by role, stage and operation", t => {
  const directory = privateTemp(t);
  fs.writeFileSync(path.join(directory, "go-1.ndjson"), [{ role: "initiator", stage: "rpc.call", operation: "object.read", duration_ms: 4 }, { role: "initiator", stage: "rpc.call", operation: "object.read", duration_ms: 6 }, { stage: "rpc.call.start" }].map(row => JSON.stringify(row)).join("\n") + "\nnot json\n");
  assert.deepEqual(summarizeTraces(directory), { "initiator rpc.call object.read": { n: 2, min: 4, p50: 4, p95: 6, max: 6, mean: 5 } });
  assert.deepEqual(summarizeTraces(path.join(directory, "missing")), {});
});

// An in-memory Gateway object store behind a fake transport: the real desktop
// object client uploads, downloads and the run releases every object.
function fakeTransport(store) {
  return class {
    constructor(options) { this.options = options; this.capabilities = { profile: "v2", limits: { concurrent: 0 }, authorization_revision: 1, schema_version: 2 }; }
    async start() { store.started = true; }
    close() { store.closed = true; }
    async invoke(operation, body) {
      store.operations.push(operation);
      if (operation === "installation.bind" || operation === "capabilities.get") return {};
      if (operation === "transfer.open") { store.objects.set(body.transfer_id, { manifest: body, chunks: new Map() }); return { window: 8 }; }
      if (operation === "transfer.chunk") { store.objects.get(body.transfer_id).chunks.set(body.index, Buffer.from(body.data_base64, "base64")); return {}; }
      if (operation === "transfer.commit") {
        const entry = store.objects.get(body.transfer_id);
        entry.bytes = Buffer.concat([...entry.chunks.keys()].sort((a, b) => a - b).map(index => entry.chunks.get(index)));
        return { object: { object_id: body.transfer_id, version: 1, size: entry.bytes.length, sha256: entry.manifest.sha256 } };
      }
      if (operation === "object.read") {
        const data = store.objects.get(body.object_id).bytes.subarray(body.offset, body.offset + body.length);
        return { offset: body.offset, data_base64: data.toString("base64"), sha256: crypto.createHash("sha256").update(data).digest("hex") };
      }
      if (operation === "object.release") { store.released.push(body.object_id); return {}; }
      throw new Error(`unexpected ${operation}`);
    }
  };
}

test("object gate run uploads, verifies, releases each sample and reports the gate", async t => {
  const lab = privateTemp(t), output = path.join(privateTemp(t), "run");
  fs.mkdirSync(path.join(lab, "evidence"), { mode: 0o700 });
  fs.writeFileSync(path.join(lab, "run.json"), JSON.stringify({ schema_version: 1, relay_profile: "local-lab", qualified_operations: [], relay_id: "r", lab_id: "l" }));
  const store = { operations: [], objects: new Map(), released: [] };
  const report = await run({ lab, output, samples: 2, sizeMiB: 0.05, controlSamples: 3, controlIntervalMS: 0, gateSeconds: 30 },
    { Transport: fakeTransport(store), ObjectClient: ISCPObjectClient, sleep: async () => {} });
  assert.equal(report.passed, true, JSON.stringify(report.failures));
  assert.equal(report.samples.length, 2);
  assert.equal(report.control_idle_ms.length, 3);
  assert.equal(store.released.length, 2);
  assert.equal(report.samples[0].upload["transfer.chunk"].count, Math.ceil(0.05 * 1024 * 1024 / 8192));
  assert.ok(report.samples[0].upload_max_inflight > 1, "chunks are pipelined");
  assert.ok(store.closed);
  assert.equal(fs.statSync(path.join(output, "object-gate.json")).mode & 0o077, 0);
  // The lab keeps one installation for every later run.
  const installation = JSON.parse(fs.readFileSync(path.join(lab, "evidence/object-gate-installation.json"), "utf8")).installation_id;
  assert.equal((await resolveTarget({ lab })).installationID, installation);
});

test("object gate aborts an upload interrupted before commit", async t => {
  const lab = privateTemp(t), output = path.join(privateTemp(t), "run");
  fs.mkdirSync(path.join(lab, "evidence"), { mode: 0o700 });
  fs.writeFileSync(path.join(lab, "run.json"), JSON.stringify({ schema_version: 1, relay_profile: "local-lab", qualified_operations: [] }));
  const store = { operations: [], objects: new Map(), released: [] };
  const Base = fakeTransport(store);
  class Failing extends Base { async invoke(operation, body) { if (operation === "transfer.commit") throw new Error("lost"); if (operation === "transfer.abort") { store.aborted = body.transfer_id; return {}; } return super.invoke(operation, body); } }
  const report = await run({ lab, output, samples: 1, sizeMiB: 0.02, controlSamples: 0, controlIntervalMS: 0, gateSeconds: 30 }, { Transport: Failing, ObjectClient: ISCPObjectClient, sleep: async () => {} });
  assert.equal(report.passed, false);
  assert.match(report.failures[0].error, /lost/u);
  assert.ok(store.aborted);
  assert.deepEqual(report.aborted, [store.aborted]);
});
