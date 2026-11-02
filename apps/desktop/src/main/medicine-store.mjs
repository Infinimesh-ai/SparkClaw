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
function text(value, max, label, optional = false, multiline = false) {
  required(typeof value === "string", `Invalid medicine ${label}`);
  const trimmed = (multiline ? value.replace(/\r\n?/gu, "\n") : value).trim();
  const control = multiline ? /[\u0000-\u0009\u000b-\u001f\u007f]/u : /[\u0000-\u001f\u007f]/u;
  required((optional || trimmed.length > 0) && trimmed.length <= max && !control.test(trimmed), `Invalid medicine ${label}`);
  return trimmed;
}
function quantity(value) {
  required(typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= 100000 && Math.abs(Math.round(value * 100) - value * 100) < 1e-7, "Invalid medicine quantity");
  return value;
}
function expiry(value) {
  if (value === "") return "";
  required(typeof value === "string" && /^\d{4}-\d{2}-\d{2}$/u.test(value) && new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value, "Invalid medicine expiry");
  return value;
}
const MEDICINE_ID = /^med_[a-f0-9]{32}$/u;
const row = (r) => r && ({ id: r.id, name: r.name, category: r.category, quantity: r.quantity, unit: r.unit, expires_on: r.expires_on,
  ...(r.version>0?{version:r.version}:{}), no_expiry: r.no_expiry === 1, location: r.location, usage: r.usage, notes: r.notes, created_at: r.created_at, updated_at: r.updated_at });
const ITEMS_V2 = (table) => `CREATE TABLE ${table}(scope TEXT NOT NULL,id TEXT NOT NULL,name TEXT NOT NULL,category TEXT NOT NULL,quantity REAL NOT NULL,unit TEXT NOT NULL,
  expires_on TEXT NOT NULL,no_expiry INTEGER NOT NULL,location TEXT NOT NULL,usage TEXT NOT NULL,notes TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(scope,id))`;
// Version 1 kept a pack size and a manufacturer. Version 2 drops both fields
// but keeps what the owner entered by folding them into the notes.
const MIGRATE_V1 = `BEGIN IMMEDIATE;
  ${ITEMS_V2("items_v2")};
  INSERT INTO items_v2 SELECT scope,id,name,category,quantity,unit,expires_on,0,'','',
    trim(CASE WHEN spec<>'' THEN '规格：'||spec ELSE '' END||CASE WHEN spec<>'' AND manufacturer<>'' THEN char(10) ELSE '' END||CASE WHEN manufacturer<>'' THEN '生产企业：'||manufacturer ELSE '' END),
    created_at,updated_at FROM items;
  DROP TABLE items; ALTER TABLE items_v2 RENAME TO items;
  PRAGMA user_version=2; COMMIT;`;

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
      required(version <= 3, "Medicine box schema is newer; preserve it and upgrade");
      if (!version) this.db.exec(`BEGIN IMMEDIATE; ${ITEMS_V2("items")}; PRAGMA user_version=2; COMMIT;`);
      if (version === 1) {
        // A failed upgrade rolls back whole and leaves version 1 for the next start.
        try { this.db.exec(MIGRATE_V1); } catch (error) { if (this.db.isTransaction) this.db.exec("ROLLBACK"); throw error; }
      }
      if (version < 3) this.#transaction(() => {
        this.db.exec(`CREATE TABLE legacy_items AS SELECT * FROM items;
          ALTER TABLE items ADD COLUMN version INTEGER NOT NULL DEFAULT 0;
          CREATE TABLE imports(scope TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(scope,id));
          CREATE TABLE sync_state(scope TEXT PRIMARY KEY,epoch TEXT NOT NULL,sequence INTEGER NOT NULL,cursor TEXT NOT NULL,synced_at TEXT NOT NULL);
          CREATE TABLE snapshots(scope TEXT PRIMARY KEY,epoch TEXT NOT NULL,sequence INTEGER NOT NULL,cursor TEXT NOT NULL);
          CREATE TABLE staged(scope TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(scope,id));
          CREATE TABLE mutations(scope TEXT PRIMARY KEY,operation_id TEXT NOT NULL,request TEXT NOT NULL,result TEXT,state TEXT NOT NULL);
          PRAGMA user_version=3;`);
      });
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
    const value = { name: text(fields.name, 80, "name"), quantity: quantity(fields.quantity), expires_on: expiry(fields.expires_on),
      location: text(fields.location, 80, "location", true), usage: text(fields.usage, 80, "usage", true), notes: text(fields.notes, 500, "notes", true, true) };
    // An explicit "no expiry" carries no date. An empty date without it is
    // accepted only so rows saved before expiry became required stay editable.
    required(typeof fields.no_expiry === "boolean" && (!fields.no_expiry || value.expires_on === ""), "Invalid medicine expiry");
    required(MEDICINE_CATEGORIES.includes(fields.category), "Invalid medicine category");
    required(MEDICINE_UNITS.includes(fields.unit), "Invalid medicine unit");
    const now = new Date().toISOString();
    return this.#transaction(() => {
      let id = fields.id;
      if (id) {
        required(this.db.prepare(`UPDATE items SET name=?,category=?,quantity=?,unit=?,expires_on=?,no_expiry=?,location=?,usage=?,notes=?,updated_at=? WHERE scope=? AND id=?`)
          .run(value.name, fields.category, value.quantity, fields.unit, value.expires_on, fields.no_expiry ? 1 : 0, value.location, value.usage, value.notes, now, key, id).changes === 1, "Medicine no longer exists");
      } else {
        required(this.db.prepare("SELECT COUNT(*) AS count FROM items WHERE scope=?").get(key).count < MEDICINE_LIMIT, "Medicine box is full");
        id = `med_${crypto.randomUUID().replaceAll("-", "")}`;
        this.db.prepare("INSERT INTO items(scope,id,name,category,quantity,unit,expires_on,no_expiry,location,usage,notes,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)")
          .run(key, id, value.name, fields.category, value.quantity, fields.unit, value.expires_on, fields.no_expiry ? 1 : 0, value.location, value.usage, value.notes, now, now);
      }
      return row(this.db.prepare("SELECT * FROM items WHERE scope=? AND id=?").get(key, id));
    });
  }
  version(scope, id) { return this.db.prepare("SELECT version FROM items WHERE scope=? AND id=?").get(scopeKey(scope), id)?.version; }
  pendingImport(scope) {
    const key = scopeKey(scope);
    return this.db.prepare("SELECT l.* FROM legacy_items l LEFT JOIN imports i ON i.scope=l.scope AND i.id=l.id WHERE l.scope=? AND i.id IS NULL ORDER BY l.id LIMIT 100").all(key).map(row);
  }
  imported(scope, items) {
    const key = scopeKey(scope);
    this.#transaction(() => { for (const item of items) { this.#put(key, item); this.db.prepare("INSERT OR IGNORE INTO imports VALUES(?,?)").run(key, item.id); } });
  }
  #put(key, item) {
    validateCanonicalItem(item);
    this.db.prepare(`INSERT INTO items VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(scope,id) DO UPDATE SET
      name=excluded.name,category=excluded.category,quantity=excluded.quantity,unit=excluded.unit,expires_on=excluded.expires_on,no_expiry=excluded.no_expiry,
      location=excluded.location,usage=excluded.usage,notes=excluded.notes,created_at=excluded.created_at,updated_at=excluded.updated_at,version=excluded.version`)
      .run(key,item.id,item.name,item.category,item.quantity,item.unit,item.expires_on,item.no_expiry?1:0,item.location,item.usage,item.notes,item.created_at,item.updated_at,item.version);
  }
  cache(scope, item) { this.#transaction(() => this.#put(scopeKey(scope), item)); return row(this.db.prepare("SELECT * FROM items WHERE scope=? AND id=?").get(scopeKey(scope),item.id)); }
  cursor(scope) {
    const key=scopeKey(scope);
    return this.db.prepare("SELECT cursor FROM snapshots WHERE scope=?").get(key)?.cursor ?? this.db.prepare("SELECT cursor FROM sync_state WHERE scope=?").get(key)?.cursor ?? '';
  }
  reset(scope) {
    const key=scopeKey(scope);
    this.#transaction(()=>{for(const table of ['sync_state','snapshots','staged'])this.db.prepare(`DELETE FROM ${table} WHERE scope=?`).run(key);});
  }
  apply(scope, page, requestedCursor) {
    const key=scopeKey(scope);
    required(page && page.schema_version===1 && typeof page.epoch==='string' && page.epoch.length>0 && page.epoch.length<=160 && ['snapshot','delta'].includes(page.mode) &&
      Number.isSafeInteger(page.sequence) && page.sequence>=0 && Number.isSafeInteger(page.base_sequence) && page.base_sequence>=0 && page.base_sequence<=page.sequence &&
      typeof page.cursor==='string' && page.cursor.length<=4096 && typeof page.more==='boolean' && Array.isArray(page.events) && page.events.length<=100, 'Invalid medicine sync page');
    const seen=new Set();let sequence=page.base_sequence;
    for(const event of page.events) {
      required(event && MEDICINE_ID.test(event.id) && (page.mode==='delta'||!seen.has(event.id)) && typeof event.deleted==='boolean' && Number.isSafeInteger(event.sequence) && event.sequence>=0 && event.sequence<=page.sequence, 'Invalid medicine sync event');
      if(page.mode==='delta') { required(event.sequence>sequence,'Invalid medicine event order');sequence=event.sequence; }
      if(page.mode==='snapshot')required(!event.deleted,'Invalid medicine snapshot deletion');
      validateCanonicalItem(event.item);required(event.item.id===event.id,'Medicine sync identity differs');seen.add(event.id);
    }
    return this.#transaction(()=>{
      required(this.cursor(scope)===requestedCursor,'Medicine sync cursor changed');
      if(page.mode==='snapshot') {
        const previous=this.db.prepare("SELECT * FROM snapshots WHERE scope=?").get(key);
        if(previous)required(previous.epoch===page.epoch && previous.sequence===page.sequence,'Medicine snapshot changed');
        else {required(!requestedCursor,'Medicine snapshot reset required');this.db.prepare("DELETE FROM staged WHERE scope=?").run(key);}
        for(const event of page.events)this.db.prepare("INSERT INTO staged VALUES(?,?,?)").run(key,event.id,JSON.stringify(event.item));
        required(this.db.prepare("SELECT COUNT(*) AS count FROM staged WHERE scope=?").get(key).count<=MEDICINE_LIMIT,'Medicine snapshot too large');
        if(page.more)this.db.prepare("INSERT INTO snapshots VALUES(?,?,?,?) ON CONFLICT(scope) DO UPDATE SET cursor=excluded.cursor").run(key,page.epoch,page.sequence,page.cursor);
        else {
          this.db.prepare("DELETE FROM items WHERE scope=?").run(key);
          for(const item of this.db.prepare("SELECT data FROM staged WHERE scope=?").all(key))this.#put(key,JSON.parse(item.data));
          this.db.prepare("DELETE FROM snapshots WHERE scope=?").run(key);this.db.prepare("DELETE FROM staged WHERE scope=?").run(key);
        }
      } else {
        const previous=this.db.prepare("SELECT * FROM sync_state WHERE scope=?").get(key);
        required(previous && previous.epoch===page.epoch && previous.sequence===page.base_sequence,'Medicine delta reset required');
        for(const event of page.events) {if(event.deleted)this.db.prepare("DELETE FROM items WHERE scope=? AND id=?").run(key,event.id);else this.#put(key,event.item);}
      }
      if(page.mode==='delta'||!page.more)this.db.prepare("INSERT INTO sync_state VALUES(?,?,?,?,?) ON CONFLICT(scope) DO UPDATE SET epoch=excluded.epoch,sequence=excluded.sequence,cursor=excluded.cursor,synced_at=excluded.synced_at").run(key,page.epoch,page.sequence,page.cursor,new Date().toISOString());
      return this.list(scope);
    });
  }
  mutation(scope) { const value=this.db.prepare("SELECT * FROM mutations WHERE scope=?").get(scopeKey(scope));return value ? {...value,request:JSON.parse(value.request),result:value.result?JSON.parse(value.result):undefined}:undefined; }
  beginMutation(scope, request) {
    const key=scopeKey(scope);
    required(!this.mutation(scope),'Previous medicine change is awaiting reconciliation');
    const operation_id=crypto.randomUUID();
    this.db.prepare("INSERT INTO mutations VALUES(?,?,?,NULL,'prepared')").run(key,operation_id,JSON.stringify(request));
    return this.mutation(scope);
  }
  attempted(scope,id) {required(this.db.prepare("UPDATE mutations SET state='attempted' WHERE scope=? AND operation_id=? AND state='prepared'").run(scopeKey(scope),id).changes===1,'Medicine change already attempted');}
  finishMutation(scope,id,result,recovered=false) {required(this.db.prepare("UPDATE mutations SET state=?,result=? WHERE scope=? AND operation_id=?").run(recovered?"reconciled":"completed",JSON.stringify(result),scopeKey(scope),id).changes===1,'Medicine receipt identity differs');}
  clearMutation(scope,id) { this.db.prepare("DELETE FROM mutations WHERE scope=? AND operation_id=? AND state IN ('prepared','completed','reconciled')").run(scopeKey(scope),id); }
  remove(scope, id) {
    const key = scopeKey(scope);
    required(typeof id === "string" && MEDICINE_ID.test(id), "Invalid medicine identity");
    return { deleted: this.db.prepare("DELETE FROM items WHERE scope=? AND id=?").run(key, id).changes === 1 };
  }
}

function validateCanonicalItem(item) {
  required(item && MEDICINE_ID.test(item.id) && Number.isSafeInteger(item.version) && item.version>0,'Invalid medicine record version');
  text(item.name,80,'name');quantity(item.quantity);expiry(item.expires_on);text(item.location,80,'location',true);text(item.usage,80,'usage',true);text(item.notes,500,'notes',true,true);
  required(MEDICINE_CATEGORIES.includes(item.category)&&MEDICINE_UNITS.includes(item.unit)&&typeof item.no_expiry==='boolean'&&(!item.no_expiry||item.expires_on===''),'Invalid medicine record fields');
  required(typeof item.created_at==='string'&&Number.isFinite(Date.parse(item.created_at))&&typeof item.updated_at==='string'&&Number.isFinite(Date.parse(item.updated_at)),'Invalid medicine timestamps');
}
