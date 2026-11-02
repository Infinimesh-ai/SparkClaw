import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { DatabaseSync } from "node:sqlite";
import { MedicineStore } from "../src/main/medicine-store.mjs";
import { MedicineBoxCapability } from "../src/main/medicine-capability.mjs";
import { projectISCPCapabilities } from "../src/main/iscp-capabilities.mjs";

const owner = { deployment_id: "deployment", owner_id: "owner" };
const medicine = (fields = {}) => ({ id: "", name: "布洛芬缓释胶囊", category: "fever", quantity: 12, unit: "capsule", expires_on: "2027-12-31", no_expiry: false,
  location: "客厅抽屉", usage: "退烧、止痛", notes: "饭后服用\n儿童减半", ...fields });
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

    // No expiry carries no date; an empty date alone stays readable for earlier rows.
    assert.equal(store.save(owner, medicine({ id: first.id, expires_on: "", no_expiry: true })).no_expiry, true);
    assert.equal(store.save(owner, medicine({ id: first.id, expires_on: "", no_expiry: false })).expires_on, "");
    assert.equal(store.save(owner, medicine({ id: first.id, location: "", usage: "", notes: "" })).notes, "");
    for (const bad of [{ name: "" }, { name: "x".repeat(81) }, { category: "magic" }, { unit: "spoon" }, { quantity: -1 }, { quantity: 1.234 },
      { quantity: Number.NaN }, { quantity: "3" }, { expires_on: "2026-02-30" }, { id: "1" }, { location: "a\nb" }, { usage: "x".repeat(81) },
      { notes: "x".repeat(501) }, { notes: "a\tb" }, { no_expiry: "yes" }, { no_expiry: true, expires_on: "2027-01-31" }]) {
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

// The version 1 layout as the first release created it.
function versionOneDatabase(root, rows) {
  fs.mkdirSync(root, { recursive: true });
  const db = new DatabaseSync(path.join(root, "medicine-box.sqlite"));
  db.exec(`CREATE TABLE items(scope TEXT NOT NULL,id TEXT NOT NULL,name TEXT NOT NULL,spec TEXT NOT NULL,manufacturer TEXT NOT NULL,category TEXT NOT NULL,quantity REAL NOT NULL,unit TEXT NOT NULL,expires_on TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(scope,id));
    PRAGMA user_version=1;`);
  const insert = db.prepare("INSERT INTO items VALUES(?,?,?,?,?,?,?,?,?,?,?)");
  for (const r of rows) insert.run(JSON.stringify([owner.deployment_id, owner.owner_id]), r.id, r.name, r.spec, r.manufacturer, r.category, r.quantity, r.unit, r.expires_on, r.created_at, r.updated_at);
  return db;
}
const v1Rows = [
  { id: "med_" + "1".repeat(32), name: "布洛芬缓释胶囊", spec: "0.3g×20粒", manufacturer: "中美天津史克", category: "fever", quantity: 12, unit: "capsule", expires_on: "2027-12-31", created_at: "2026-11-01T00:00:00.000Z", updated_at: "2026-11-01T01:00:00.000Z" },
  { id: "med_" + "2".repeat(32), name: "蒙脱石散", spec: "3g×10袋", manufacturer: "", category: "digestive", quantity: 2.5, unit: "sachet", expires_on: "", created_at: "2026-11-01T02:00:00.000Z", updated_at: "2026-11-01T02:00:00.000Z" },
  { id: "med_" + "3".repeat(32), name: "创可贴", spec: "", manufacturer: "云南白药", category: "wound", quantity: 30, unit: "patch", expires_on: "2028-01-31", created_at: "2026-11-01T03:00:00.000Z", updated_at: "2026-11-01T03:00:00.000Z" },
  { id: "med_" + "4".repeat(32), name: "维生素C片", spec: "", manufacturer: "", category: "other", quantity: 0, unit: "tablet", expires_on: "2026-01-31", created_at: "2026-11-01T04:00:00.000Z", updated_at: "2026-11-01T04:00:00.000Z" },
];

test("a version 1 box upgrades in place and keeps the pack size and manufacturer in the notes", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "medicine-box-"));
  try {
    versionOneDatabase(root, v1Rows).close();
    const store = new MedicineStore(root);
    try {
      const items = Object.fromEntries(store.list(owner).items.map((item) => [item.id, item]));
      assert.equal(Object.keys(items).length, v1Rows.length);
      for (const old of v1Rows) {
        const { spec, manufacturer, ...kept } = old;
        assert.deepEqual({ ...items[old.id], notes: undefined }, { ...kept, no_expiry: false, location: "", usage: "", notes: undefined });
      }
      assert.deepEqual(v1Rows.map((old) => items[old.id].notes), ["规格：0.3g×20粒\n生产企业：中美天津史克", "规格：3g×10袋", "生产企业：云南白药", ""]);
      store.save(owner, medicine({ id: v1Rows[1].id, expires_on: "", no_expiry: true }));
    } finally { store.close(); }
    const check = new DatabaseSync(path.join(root, "medicine-box.sqlite"));
    try { assert.equal(check.prepare("PRAGMA user_version").get().user_version, 3); } finally { check.close(); }
    // The upgraded box reopens without migrating again.
    const reopened = new MedicineStore(root);
    try { assert.equal(reopened.list(owner).items.find((item) => item.id === v1Rows[1].id).no_expiry, true); } finally { reopened.close(); }
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});

test("a failed upgrade leaves version 1 untouched and the next start upgrades it", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "medicine-box-"));
  try {
    const db = versionOneDatabase(root, v1Rows);
    // A stray table with the migration's name makes the upgrade fail midway.
    db.exec("CREATE TABLE items_v2(x)");
    db.close();
    assert.throws(() => new MedicineStore(root), /items_v2/);
    const check = new DatabaseSync(path.join(root, "medicine-box.sqlite"));
    try {
      assert.equal(check.prepare("PRAGMA user_version").get().user_version, 1);
      assert.equal(check.prepare("SELECT COUNT(*) AS count FROM items").get().count, v1Rows.length);
      assert.ok(check.prepare("SELECT spec FROM items LIMIT 1").get());
      check.exec("DROP TABLE items_v2");
    } finally { check.close(); }
    const store = new MedicineStore(root);
    try { assert.equal(store.list(owner).items.length, v1Rows.length); } finally { store.close(); }

    const newer = new DatabaseSync(path.join(root, "medicine-box.sqlite"));
    newer.exec("PRAGMA user_version=4"); newer.close();
    assert.throws(() => new MedicineStore(root), /newer/);
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});

test("medicine box IPC takes its scope from the authenticated identity and rejects injected fields", async () => {
  const frame = { url: "sparkclaw-app://workbench/index.html" }, webContents = { mainFrame: frame };
  let identity = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  const calls = [];
  const store = { list: (scope) => { calls.push(["list", scope]); return { items: [] }; }, save: (scope, value) => { calls.push(["save", scope, value]); return value; },
    remove: (scope, id) => { calls.push(["remove", scope, id]); return { deleted: true }; } };
  const client = { refresh: async () => {}, save: medicine => store.save(owner, medicine), remove: id => store.remove(owner, id) };
  const capability = new MedicineBoxCapability({ window: { webContents }, store, client, getIdentity: () => identity });
  const event = { sender: webContents, senderFrame: frame };
  await capability.dispatch(event, { schema_version: 1, operation: "list" });
  await capability.dispatch(event, { schema_version: 1, operation: "save", medicine: medicine() });
  await capability.dispatch(event, { schema_version: 1, operation: "remove", medicine_id: "med_x" });
  assert.deepEqual(calls.map(([operation, scope]) => [operation, scope]), ["list", "save", "remove"].map((operation) => [operation, owner]));
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "list", owner_id: "other" }), /fields/);
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "save", medicine: { ...medicine(), owner_id: "other" } }), /fields/);
  // The removed version 1 fields are no longer accepted.
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "save", medicine: { ...medicine(), spec: "0.3g" } }), /fields/);
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "lookup", code: "1" }), /unavailable/);
  await assert.rejects(capability.dispatch({ ...event, senderFrame: { ...frame } }, { schema_version: 1, operation: "list" }), /trusted/);
  identity = null;
  await assert.rejects(capability.dispatch(event, { schema_version: 1, operation: "list" }), /locked/);
  assert.equal(calls.length, 3);
});

test("text reading is a surface of its own, so an older Gateway only disables text entry", () => {
  const manifest = { schema_version: 2, profile: "sparkclaw.workbench.transport.v2", session_id: "session", authorization_revision: 1, expires_at: new Date(Date.now() + 60000).toISOString(), operations: ["medicine.recognize", "medicine.parse"] };
  const rows = ["medicine", "medicine_text"].map((id) => ({ id, supported: true, permitted: true, qualified: true, enabled: true, dependencies_ready: true }));
  const report = { schema_version: 2, profile: manifest.profile, session_id: "session", authorization_revision: 1, expires_at: manifest.expires_at, capabilities: rows };
  const both = projectISCPCapabilities(manifest, report).surfaces;
  assert.deepEqual([both.medicine.enabled, both.medicine_text.enabled], [true, true]);
  const older = projectISCPCapabilities({ ...manifest, operations: ["medicine.recognize"] }, { ...report, capabilities: rows.slice(0, 1) }).surfaces;
  assert.deepEqual([older.medicine.enabled, older.medicine_text.enabled, older.medicine_text.reason], [true, false, "permission_or_implementation_missing"]);
});

test("photo recognition is its own ISCP surface", () => {
  const manifest = { schema_version: 2, profile: "sparkclaw.workbench.transport.v2", session_id: "session", authorization_revision: 1, expires_at: new Date(Date.now() + 60000).toISOString(), operations: ["medicine.recognize"] };
  const row = { id: "medicine", supported: true, permitted: true, qualified: true, enabled: true, dependencies_ready: true };
  const report = { schema_version: 2, profile: manifest.profile, session_id: "session", authorization_revision: 1, expires_at: manifest.expires_at, capabilities: [row] };
  assert.equal(projectISCPCapabilities(manifest, report).surfaces.medicine.enabled, true);
  assert.equal(projectISCPCapabilities({ ...manifest, operations: [] }, report).surfaces.medicine.enabled, false);
  assert.equal(projectISCPCapabilities(manifest, { ...report, capabilities: [{ ...row, enabled: false, reason: "dependency_unavailable" }] }).surfaces.medicine.reason, "dependency_unavailable");
});

test('inventory write capability requires the separate read capability without hiding original recognition', () => {
  const read = ['medicine.sync', 'medicine.operations.get'];
  const write = ['medicine.items.create', 'medicine.items.update', 'medicine.items.delete', 'medicine.items.batch', 'medicine.import'];
  const manifest = { schema_version: 2, profile: 'sparkclaw.workbench.transport.v2', session_id: 'session', authorization_revision: 1, expires_at: new Date(Date.now() + 60000).toISOString(), operations: [...read, ...write, 'medicine.recognize'] };
  const capabilities = ['medicine_box_read', 'medicine_box_write', 'medicine'].map(id => ({ id, supported: true, permitted: true, qualified: true, enabled: true, dependencies_ready: true }));
  const report = { ...manifest, capabilities };
  assert.equal(projectISCPCapabilities(manifest, report).surfaces.medicine_box_write.enabled, true);
  const limited = projectISCPCapabilities({ ...manifest, operations: [...write, 'medicine.recognize'] }, report).surfaces;
  assert.equal(limited.medicine_box_write.enabled, false); assert.equal(limited.medicine.enabled, true);
});

test('medicine IPC preserves a typed version conflict for the original dialog alert', async () => {
  const frame = { url: 'sparkclaw-app://workbench/index.html' }, webContents = { mainFrame: frame };
  const capability = new MedicineBoxCapability({ window: { webContents }, getIdentity: () => owner, client: { save: async () => { throw Object.assign(new Error('changed'), { status: 409 }); } } });
  assert.deepEqual(await capability.dispatch({ sender: webContents, senderFrame: frame }, { schema_version: 1, operation: 'save', medicine: medicine() }), { medicine_error: 'medicine_conflict' });
});
