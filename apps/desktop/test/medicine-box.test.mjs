import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { MedicineStore } from "../src/main/medicine-store.mjs";
import { MedicineBoxCapability } from "../src/main/medicine-capability.mjs";
import { projectISCPCapabilities } from "../src/main/iscp-capabilities.mjs";

const owner = { deployment_id: "deployment", owner_id: "owner" };
const medicine = (fields = {}) => ({ id: "", name: "布洛芬缓释胶囊", spec: "0.3g×20粒", manufacturer: "中美天津史克", category: "fever", quantity: 12, unit: "capsule", expires_on: "", ...fields });
const temporaryStore = () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "medicine-box-"));
  return { store: new MedicineStore(root), root };
};

test("recognized medicines are added, updated by id and removed per account", () => {
  const { store, root } = temporaryStore();
  try {
    const first = store.save(owner, medicine());
    assert.match(first.id, /^med_[a-f0-9]{32}$/u);
    assert.deepEqual({ ...first, id: "", created_at: "", updated_at: "" }, { ...medicine(), created_at: "", updated_at: "" });
    // A second box of the same medicine is its own row.
    const second = store.save(owner, medicine({ quantity: 20 }));
    assert.notEqual(second.id, first.id);
    assert.equal(store.list(owner).items.length, 2);

    const updated = store.save(owner, medicine({ id: first.id, quantity: 3.5, category: "pain", expires_on: "2027-01-31" }));
    assert.equal(updated.id, first.id);
    assert.equal(updated.created_at, first.created_at);
    assert.deepEqual([updated.quantity, updated.category, updated.expires_on], [3.5, "pain", "2027-01-31"]);
    assert.throws(() => store.save(owner, medicine({ id: "med_" + "f".repeat(32) })), /no longer exists/);
    // Another account on the same installation never sees or edits this inventory.
    const other = { deployment_id: "deployment", owner_id: "someone-else" };
    assert.deepEqual(store.list(other).items, []);
    assert.throws(() => store.save(other, medicine({ id: first.id })), /no longer exists/);

    for (const bad of [{ name: "" }, { name: "x".repeat(81) }, { category: "magic" }, { unit: "spoon" }, { quantity: -1 }, { quantity: 1.234 },
      { quantity: Number.NaN }, { quantity: "3" }, { expires_on: "2026-02-30" }, { id: "1" }, { manufacturer: "a\nb" }]) {
      assert.throws(() => store.save(owner, medicine(bad)), /Invalid medicine/);
    }
    assert.deepEqual(store.remove(owner, first.id), { deleted: true });
    assert.deepEqual(store.remove(owner, first.id), { deleted: false });
    assert.deepEqual(store.list(owner).items.map((item) => item.id), [second.id]);
  } finally { store.close(); fs.rmSync(root, { recursive: true, force: true }); }
});

test("the store survives reopening", () => {
  const { store, root } = temporaryStore();
  try {
    store.save(owner, medicine());
    store.close();
    const reopened = new MedicineStore(root);
    try { assert.equal(reopened.list(owner).items[0].name, "布洛芬缓释胶囊"); } finally { reopened.close(); }
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});

test("medicine box IPC takes its scope from the authenticated identity and rejects injected fields", async () => {
  const frame = { url: "sparkclaw-app://workbench/index.html" }, webContents = { mainFrame: frame };
  let identity = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  const calls = [];
  const store = { list: (scope) => { calls.push(["list", scope]); return { items: [] }; }, save: (scope, value) => { calls.push(["save", scope, value]); return value; },
    remove: (scope, id) => { calls.push(["remove", scope, id]); return { deleted: true }; } };
  const capability = new MedicineBoxCapability({ window: { webContents }, store, getIdentity: () => identity });
  const event = { sender: webContents, senderFrame: frame };
  await capability.dispatch(event, { schema_version: 1, operation: "list" });
  await capability.dispatch(event, { schema_version: 1, operation: "save", medicine: medicine() });
  await capability.dispatch(event, { schema_version: 1, operation: "remove", medicine_id: "med_x" });
  assert.deepEqual(calls.map(([operation, scope]) => [operation, scope]), ["list", "save", "remove"].map((operation) => [operation, owner]));
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "list", owner_id: "other" }), /fields/);
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "save", medicine: { ...medicine(), owner_id: "other" } }), /fields/);
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "lookup", code: "1" }), /unavailable/);
  await assert.rejects(capability.dispatch({ ...event, senderFrame: { ...frame } }, { schema_version: 1, operation: "list" }), /trusted/);
  identity = null;
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "list" }), /locked/);
  assert.equal(calls.length, 3);
});

test("photo recognition is its own ISCP surface", () => {
  const manifest = { schema_version: 2, profile: "sparkclaw.workbench.transport.v2", session_id: "session", authorization_revision: 1, expires_at: new Date(Date.now() + 60000).toISOString(), operations: ["medicine.recognize"] };
  const row = { id: "medicine", supported: true, permitted: true, qualified: true, enabled: true, dependencies_ready: true };
  const report = { schema_version: 2, profile: manifest.profile, session_id: "session", authorization_revision: 1, expires_at: manifest.expires_at, capabilities: [row] };
  assert.equal(projectISCPCapabilities(manifest, report).surfaces.medicine.enabled, true);
  assert.equal(projectISCPCapabilities({ ...manifest, operations: [] }, report).surfaces.medicine.enabled, false);
  assert.equal(projectISCPCapabilities(manifest, { ...report, capabilities: [{ ...row, enabled: false, reason: "dependency_unavailable" }] }).surfaces.medicine.reason, "dependency_unavailable");
});
