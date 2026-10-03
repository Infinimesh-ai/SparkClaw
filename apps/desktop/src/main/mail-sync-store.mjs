import fs from 'node:fs';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';

export const MAIL_PAGE_LIMIT = 100;
const ID = /^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,255}$/u;
const MESSAGE_FIELDS = ['id','mailbox_id','version','conversation_id','subject','from','to','cc','receiving_address','direction','sent_at','arrived_at','summary','body_text','body_truncated','viewed','processing_state','original_available','attachments'];
const required = (condition, message) => { if (!condition) throw new Error(message); };
function scopeKey(scope) {
  for (const key of ['deployment_id','owner_id','client_id']) required(typeof scope?.[key] === 'string' && ID.test(scope[key]), 'Invalid mail cache identity');
  return JSON.stringify([scope.deployment_id,scope.owner_id,scope.client_id]);
}
function mailboxID(value) { required(typeof value === 'string' && ID.test(value), 'Invalid mailbox identity'); return value; }
function plain(value) { return value && typeof value === 'object' && !Array.isArray(value); }
function message(value, mailbox) {
  required(plain(value) && Object.keys(value).every((key) => MESSAGE_FIELDS.includes(key)), 'Invalid mail projection fields');
  required(value.mailbox_id === mailbox && ID.test(value.id) && Number.isSafeInteger(value.version) && value.version >= 0, 'Invalid mail projection identity');
  for (const key of ['subject','from','receiving_address','direction','sent_at','arrived_at','summary','body_text','processing_state']) required(typeof value[key] === 'string', 'Invalid mail projection text');
  required(Buffer.byteLength(JSON.stringify(value)) <= 256 * 1024, 'Mail projection exceeds capacity');
  for (const key of ['to','cc']) required(Array.isArray(value[key]) && value[key].length <= 100 && value[key].every((v) => typeof v === 'string' && v.length <= 1024), 'Invalid mail addresses');
  for (const key of ['body_truncated','viewed','original_available']) required(typeof value[key] === 'boolean', 'Invalid mail projection flags');
  required(Array.isArray(value.attachments) && value.attachments.length <= 100 && value.attachments.every((a) => plain(a) && Object.keys(a).every((k) => ['id','name','size','available'].includes(k)) && typeof a.id === 'string' && typeof a.name === 'string' && Number.isSafeInteger(a.size) && a.size >= 0 && typeof a.available === 'boolean'), 'Invalid attachment manifest');
  return value;
}

// A separate main-owned database contains only authorized mail projections.
// Snapshot pages stage separately: readers keep the last complete revision
// until the final page commits replacement and missing IDs disappear together.
export class MailSyncStore {
  constructor(root) {
    required(path.isAbsolute(root), 'Mail cache requires an absolute directory');
    fs.mkdirSync(root,{recursive:true,mode:0o700});
    const info=fs.lstatSync(root); required(info.isDirectory() && !info.isSymbolicLink(), 'Invalid mail cache directory'); fs.chmodSync(root,0o700);
    const file=path.join(root,'mail-cache.sqlite');
    if (fs.existsSync(file)) required(fs.lstatSync(file).isFile() && !fs.lstatSync(file).isSymbolicLink(), 'Invalid mail cache file');
    const fd=fs.openSync(file,'a',0o600);fs.closeSync(fd);fs.chmodSync(file,0o600);
    this.db=new DatabaseSync(file);
    try {
      this.db.exec('PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;');
      const version=this.db.prepare('PRAGMA user_version').get().user_version;
      required(version <= 1, 'Mail cache schema is newer; preserve it and upgrade');
      if (!version) this.db.exec(`BEGIN IMMEDIATE;
        CREATE TABLE boxes(scope TEXT NOT NULL,mailbox TEXT NOT NULL,epoch TEXT NOT NULL,sequence INTEGER NOT NULL,cursor TEXT NOT NULL,synced_at TEXT NOT NULL,PRIMARY KEY(scope,mailbox));
        CREATE TABLE mail(scope TEXT NOT NULL,mailbox TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(scope,mailbox,id));
        CREATE TABLE snapshots(scope TEXT NOT NULL,mailbox TEXT NOT NULL,epoch TEXT NOT NULL,sequence INTEGER NOT NULL,cursor TEXT NOT NULL,PRIMARY KEY(scope,mailbox));
        CREATE TABLE staged(scope TEXT NOT NULL,mailbox TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(scope,mailbox,id));
        CREATE TABLE catalogs(scope TEXT PRIMARY KEY,data TEXT NOT NULL);
        PRAGMA user_version=1; COMMIT;`);
    } catch (error) {this.db.close();throw error;}
  }
  close(){this.db.close();}
  #transaction(fn){this.db.exec('BEGIN IMMEDIATE');try{const out=fn();this.db.exec('COMMIT');return out;}catch(error){this.db.exec('ROLLBACK');throw error;}}
  catalog(scope, boxes) {
    const key=scopeKey(scope);
    if (boxes!==undefined) {
      required(Array.isArray(boxes) && boxes.length<=100, 'Invalid mailbox catalog');
      const allowed=['id','version','provider','address','intake_enabled','active_binding','state','scope_version','provider_mode','last_sync_at','coverage_start','coverage_end','poll_through','inflight_until','pending_failure_count','suppressed_mail_count','coverage_gap_count','unacknowledged_warning_count','refresh_available','gap','error','refresh_pending','refresh_request_id'];
      for(const box of boxes) required(plain(box) && ID.test(box.id) && typeof box.address==='string' && typeof box.provider==='string' && Object.keys(box).every((k)=>allowed.includes(k)), 'Invalid mailbox catalog fields');
      required(Buffer.byteLength(JSON.stringify(boxes))<=256*1024, 'Mailbox catalog exceeds capacity');
      this.db.prepare('INSERT INTO catalogs VALUES(?,?) ON CONFLICT(scope) DO UPDATE SET data=excluded.data').run(key,JSON.stringify(boxes));
    }
    return JSON.parse(this.db.prepare('SELECT data FROM catalogs WHERE scope=?').get(key)?.data || '[]');
  }
  read(scope, mailbox) {
    const key=scopeKey(scope);mailboxID(mailbox);
    const box=this.db.prepare('SELECT epoch,sequence,synced_at FROM boxes WHERE scope=? AND mailbox=?').get(key,mailbox);
    return {mailbox_id:mailbox,epoch:box?.epoch||'',sequence:box?.sequence||0,synced_at:box?.synced_at||'',messages:this.db.prepare('SELECT data FROM mail WHERE scope=? AND mailbox=? ORDER BY id').all(key,mailbox).map((r)=>JSON.parse(r.data))};
  }
  cursor(scope,mailbox){const key=scopeKey(scope);mailboxID(mailbox);return this.db.prepare('SELECT cursor FROM snapshots WHERE scope=? AND mailbox=?').get(key,mailbox)?.cursor ?? this.db.prepare('SELECT cursor FROM boxes WHERE scope=? AND mailbox=?').get(key,mailbox)?.cursor ?? '';}
  reset(scope,mailbox){const key=scopeKey(scope);mailboxID(mailbox);this.#transaction(()=>{this.db.prepare('DELETE FROM snapshots WHERE scope=? AND mailbox=?').run(key,mailbox);this.db.prepare('DELETE FROM staged WHERE scope=? AND mailbox=?').run(key,mailbox);this.db.prepare("UPDATE boxes SET cursor='' WHERE scope=? AND mailbox=?").run(key,mailbox);});}
  apply(scope,mailbox,response,requestedCursor) {
    const key=scopeKey(scope);mailboxID(mailbox);
    required(plain(response) && Object.keys(response).sort().join(',') === ['schema_version','mailbox_id','epoch','mode','base_sequence','sequence','events','cursor','more'].sort().join(','), 'Invalid mail sync envelope');
    required(response.schema_version===1 && response.mailbox_id===mailbox && /^[a-f0-9]{32}$/u.test(response.epoch) && ['snapshot','delta'].includes(response.mode) && typeof response.cursor==='string' && response.cursor.length<=1024 && typeof response.more==='boolean' && Number.isSafeInteger(response.sequence) && response.sequence>=0 && Number.isSafeInteger(response.base_sequence) && response.base_sequence>=0 && response.base_sequence<=response.sequence, 'Invalid mail sync revision');
    required(Array.isArray(response.events) && response.events.length<=MAIL_PAGE_LIMIT && Buffer.byteLength(JSON.stringify(response))<=1024*1024, 'Mail sync page exceeds capacity');
    let sequence=response.base_sequence;
    const seen=new Set();
    for(const event of response.events){required(plain(event) && Object.keys(event).every((k)=>['sequence','id','deleted','mail'].includes(k)) && ID.test(event.id) && typeof event.deleted==='boolean' && Number.isSafeInteger(event.sequence), 'Invalid mail sync event');
      if(response.mode==='delta') required(event.sequence===++sequence && event.sequence<=response.sequence,'Mail delta has a gap');
      else required(event.sequence===response.sequence && !event.deleted && !seen.has(event.id),'Invalid snapshot event');
      seen.add(event.id);if(event.deleted) required(event.mail===undefined,'Tombstone contains mail');else required(message(event.mail,mailbox).id===event.id,'Mail event identity differs');
    }
    if(response.mode==='delta' && !response.more) required(sequence===response.sequence,'Mail delta incomplete');
    return this.#transaction(()=>{
      required(this.cursor(scope,mailbox)===requestedCursor,'Mail response is stale');
      const box=this.db.prepare('SELECT * FROM boxes WHERE scope=? AND mailbox=?').get(key,mailbox);
      const snapshot=this.db.prepare('SELECT * FROM snapshots WHERE scope=? AND mailbox=?').get(key,mailbox);
      if(response.mode==='snapshot') {
        if(!snapshot) {
          required(requestedCursor==='' && response.base_sequence===response.sequence,'Snapshot must start at a fresh cursor');
          this.db.prepare('DELETE FROM staged WHERE scope=? AND mailbox=?').run(key,mailbox);
          this.db.prepare('INSERT INTO snapshots VALUES(?,?,?,?,?)').run(key,mailbox,response.epoch,response.sequence,'');
        } else required(snapshot.epoch===response.epoch && snapshot.sequence===response.sequence,'Snapshot revision changed');
        for(const event of response.events) this.db.prepare('INSERT INTO staged VALUES(?,?,?,?)').run(key,mailbox,event.id,JSON.stringify(event.mail));
        if(response.more) this.db.prepare('UPDATE snapshots SET cursor=? WHERE scope=? AND mailbox=?').run(response.cursor,key,mailbox);
        else {
          this.db.prepare('DELETE FROM mail WHERE scope=? AND mailbox=?').run(key,mailbox);
          this.db.prepare('INSERT INTO mail SELECT * FROM staged WHERE scope=? AND mailbox=?').run(key,mailbox);
          this.db.prepare('DELETE FROM staged WHERE scope=? AND mailbox=?').run(key,mailbox);
          this.db.prepare('DELETE FROM snapshots WHERE scope=? AND mailbox=?').run(key,mailbox);
        }
      } else {
        required(!snapshot && box?.epoch===response.epoch && box.sequence===response.base_sequence,'Mail epoch or cursor changed; refresh snapshot');
        for(const event of response.events) {if(event.deleted)this.db.prepare('DELETE FROM mail WHERE scope=? AND mailbox=? AND id=?').run(key,mailbox,event.id);else this.db.prepare('INSERT INTO mail VALUES(?,?,?,?) ON CONFLICT(scope,mailbox,id) DO UPDATE SET data=excluded.data').run(key,mailbox,event.id,JSON.stringify(event.mail));}
      }
      const usage=this.db.prepare('SELECT COUNT(*) AS count,COALESCE(SUM(LENGTH(data)),0) AS bytes FROM mail WHERE scope=? AND mailbox=?').get(key,mailbox);
      const staging=this.db.prepare('SELECT COUNT(*) AS count,COALESCE(SUM(LENGTH(data)),0) AS bytes FROM staged WHERE scope=? AND mailbox=?').get(key,mailbox);
      required(usage.count<=20000 && staging.count<=20000 && usage.bytes+staging.bytes<=32*1024*1024,'Mail cache capacity exceeded');
      if(response.mode==='delta'||!response.more) this.db.prepare('INSERT INTO boxes VALUES(?,?,?,?,?,?) ON CONFLICT(scope,mailbox) DO UPDATE SET epoch=excluded.epoch,sequence=excluded.sequence,cursor=excluded.cursor,synced_at=excluded.synced_at').run(key,mailbox,response.epoch,response.mode==='delta'?sequence:response.sequence,response.cursor,new Date().toISOString());
      return this.read(scope,mailbox);
    });
  }
}
