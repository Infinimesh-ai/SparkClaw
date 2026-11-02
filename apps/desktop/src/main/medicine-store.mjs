import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { DatabaseSync } from "node:sqlite";

export const MEDICINE_CATEGORIES = ["cold", "fever", "pain", "cough", "digestive", "allergy", "wound", "other"];
export const MEDICINE_UNITS = ["tablet", "capsule", "sachet", "bottle", "tube", "patch", "box", "ml", "g"];
export const MEDICINE_LIMIT = 2000;
const ID = /^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,255}$/u;
const required = (condition, message) => { if (!condition) throw new Error(message); };

function scopeKey(scope) {
  for (const key of ["deployment_id", "owner_id"]) required(typeof scope?.[key] === "string" && ID.test(scope[key]), "Invalid medicine box identity");
  return JSON.stringify([scope.deployment_id, scope.owner_id]);
}
function text(value, max, label, optional = false) {
  required(typeof value === "string", `Invalid medicine ${label}`);
  const trimmed = value.trim();
  required((optional || trimmed.length > 0) && trimmed.length <= max && !/[\u0000-\u001f\u007f]/u.test(trimmed), `Invalid medicine ${label}`);
  return trimmed;
}
function quantity(value) {
  required(typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= 100000 && Math.round(value * 100) === value * 100, "Invalid medicine quantity");
  return value;
}
function expiry(value) {
  if (value === "") return "";
  required(typeof value === "string" && /^\d{4}-\d{2}-\d{2}$/u.test(value) && new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value, "Invalid medicine expiry");
  return value;
}
const MEDICINE_ID = /^med_[a-f0-9]{32}$/u;
const row = (r) => r && ({ id: r.id, name: r.name, spec: r.spec, manufacturer: r.manufacturer, category: r.category,
  quantity: r.quantity, unit: r.unit, expires_on: r.expires_on, created_at: r.created_at, updated_at: r.updated_at });

// A main-owned household inventory. The renderer recognizes a medicine from a
// photo through the backend and submits only the confirmed fields; the photo
// itself is never stored here.
export class MedicineStore {
  constructor(root) {
    required(path.isAbsolute(root), "Medicine box requires an absolute directory");
    fs.mkdirSync(root, { recursive: true, mode: 0o700 });
    const info = fs.lstatSync(root); required(info.isDirectory() && !info.isSymbolicLink(), "Invalid medicine box directory"); fs.chmodSync(root, 0o700);
    const file = path.join(root, "medicine-box.sqlite");
    if (fs.existsSync(file)) required(fs.lstatSync(file).isFile() && !fs.lstatSync(file).isSymbolicLink(), "Invalid medicine box file");
    const fd = fs.openSync(file, "a", 0o600); fs.closeSync(fd); fs.chmodSync(file, 0o600);
    this.db = new DatabaseSync(file);
    try {
      this.db.exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;");
      const version = this.db.prepare("PRAGMA user_version").get().user_version;
      required(version <= 1, "Medicine box schema is newer; preserve it and upgrade");
      if (!version) this.db.exec(`BEGIN IMMEDIATE;
        CREATE TABLE items(scope TEXT NOT NULL,id TEXT NOT NULL,name TEXT NOT NULL,spec TEXT NOT NULL,manufacturer TEXT NOT NULL,category TEXT NOT NULL,quantity REAL NOT NULL,unit TEXT NOT NULL,expires_on TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(scope,id));
        PRAGMA user_version=1; COMMIT;`);
    } catch (error) { this.db.close(); throw error; }
  }
  close() { this.db.close(); }
  #transaction(fn) { this.db.exec("BEGIN IMMEDIATE"); try { const out = fn(); this.db.exec("COMMIT"); return out; } catch (error) { this.db.exec("ROLLBACK"); throw error; } }
  list(scope) {
    const key = scopeKey(scope);
    return { items: this.db.prepare("SELECT * FROM items WHERE scope=? ORDER BY name COLLATE NOCASE, created_at").all(key).map(row) };
  }
  // A new medicine has an empty id; an existing one keeps its id and creation time.
  save(scope, fields) {
    const key = scopeKey(scope);
    required(fields && typeof fields === "object" && !Array.isArray(fields), "Invalid medicine fields");
    required(fields.id === "" || typeof fields.id === "string" && MEDICINE_ID.test(fields.id), "Invalid medicine identity");
    const value = { name: text(fields.name, 80, "name"), spec: text(fields.spec, 80, "spec", true), manufacturer: text(fields.manufacturer, 80, "manufacturer", true),
      quantity: quantity(fields.quantity), expires_on: expiry(fields.expires_on) };
    required(MEDICINE_CATEGORIES.includes(fields.category), "Invalid medicine category");
    required(MEDICINE_UNITS.includes(fields.unit), "Invalid medicine unit");
    const now = new Date().toISOString();
    return this.#transaction(() => {
      let id = fields.id;
      if (id) {
        required(this.db.prepare(`UPDATE items SET name=?,spec=?,manufacturer=?,category=?,quantity=?,unit=?,expires_on=?,updated_at=? WHERE scope=? AND id=?`)
          .run(value.name, value.spec, value.manufacturer, fields.category, value.quantity, fields.unit, value.expires_on, now, key, id).changes === 1, "Medicine no longer exists");
      } else {
        required(this.db.prepare("SELECT COUNT(*) AS count FROM items WHERE scope=?").get(key).count < MEDICINE_LIMIT, "Medicine box is full");
        id = `med_${crypto.randomUUID().replaceAll("-", "")}`;
        this.db.prepare("INSERT INTO items VALUES(?,?,?,?,?,?,?,?,?,?,?)")
          .run(key, id, value.name, value.spec, value.manufacturer, fields.category, value.quantity, fields.unit, value.expires_on, now, now);
      }
      return row(this.db.prepare("SELECT * FROM items WHERE scope=? AND id=?").get(key, id));
    });
  }
  remove(scope, id) {
    const key = scopeKey(scope);
    required(typeof id === "string" && MEDICINE_ID.test(id), "Invalid medicine identity");
    return { deleted: this.db.prepare("DELETE FROM items WHERE scope=? AND id=?").run(key, id).changes === 1 };
  }
}
