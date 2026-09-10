import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { createRequire } from "node:module";

const { simpleParser } = createRequire(new URL("../../../tools/browser-controller/package.json", import.meta.url))("mailparser");

export const CAPTURE_LIMITS = Object.freeze({ parts: 20, partBytes: 25 << 20, totalBytes: 100 << 20, bodyBytes: 2 << 20, manifestBytes: 1 << 20 });
const scopePattern = /^[a-f0-9]{64}$/u;
const hash = value => crypto.createHash("sha256").update(value).digest("hex");
function error(code) { return Object.assign(new Error(code), { code }); }
const json = value => Buffer.from(`${JSON.stringify(value, null, 2)}\n`);

export function validateCaptureInput(input, provider) {
  const keys = ["schema_version", "operation", "invocation_id", "provider", "account", "owner_scope"];
  if (input?.operation === 'capture') keys.push('target');
  if (input?.operation === 'discover' && Object.hasOwn(input, 'discovery')) keys.push('discovery');
  if (input?.operation === 'collect_page') { keys.push('discovery'); if (Object.hasOwn(input,'ack_page_id')) keys.push('ack_page_id'); }
  if (input?.operation === 'enumerate_thread') keys.push('thread', 'continuation', 'limit');
  if (input?.operation === 'mark_read') keys.push('target', 'committed_capture');
  if (!input || typeof input !== "object" || Object.keys(input).length !== keys.length ||
      keys.some(key => !Object.hasOwn(input, key)) || input.schema_version !== 1 || !['read','discover','capture','enumerate_thread','mark_read','collect_page'].includes(input.operation) ||
      input.provider !== provider || input.account !== "default" || typeof input.owner_scope !== "string" || !scopePattern.test(input.owner_scope) ||
      typeof input.invocation_id !== "string" || !/^[A-Za-z0-9._:-]{1,128}$/u.test(input.invocation_id)) throw error("invalid_request");
  if (input.operation === 'collect_page' && input.discovery?.limit > 50) throw error('invalid_request');
  if (input.operation === 'collect_page' && Object.hasOwn(input,'ack_page_id') && (typeof input.ack_page_id !== 'string' || !/^(?:page_[a-f0-9]{64})?$/u.test(input.ack_page_id))) throw error('invalid_request');
  if (['capture','mark_read'].includes(input.operation)) validateMailTarget(input.target);
  if (Object.hasOwn(input,'discovery')) {
    const d = input.discovery;
    if (!d || ['account_address','continuation','lane','limit'].some(key=>!Object.hasOwn(d,key)) ||
        Object.keys(d).some(key=>!['account_address','continuation','interval_end','interval_start','lane','limit'].includes(key)) ||
        ['interval_start','interval_end'].some(key=>Object.hasOwn(d,key) && typeof d[key]!=='string') ||
        !['unread','recent_inbound'].includes(d.lane) || !validContinuation(d.continuation) || !validBatchLimit(d.limit)) throw error('invalid_request');
    validateMailTarget({account_address:d.account_address,provider_message_id:'check',provider_selection_id:'check'});
    if(d.lane==='recent_inbound'){
      const start = Date.parse(d.interval_start), end = Date.parse(d.interval_end);
      if (!Number.isFinite(start) || !Number.isFinite(end) || start===Date.parse('0001-01-01T00:00:00Z') || start >= end) throw error('invalid_request');
    }
  }
  if (input.operation === 'enumerate_thread') {
    const target = input.thread;
    if (!target || Object.keys(target).sort().join(',') !== 'account_address,folder,provider_selection_id,provider_thread_id' ||
        !validContinuation(input.continuation) || !validBatchLimit(input.limit)) throw error('invalid_request');
    validateMailTarget({...target,provider_message_id:target.provider_selection_id});
  }
  if (input.operation === 'mark_read') {
    const receipt = input.committed_capture;
    if (!receipt || Object.keys(receipt).sort().join(',') !== 'attachments_count,capture_id,mail_id,mailbox_id,manifest_path,manifest_sha256,read_state' ||
        typeof receipt.manifest_sha256 !== 'string' || !/^sha256:[a-f0-9]{64}$/u.test(receipt.manifest_sha256) ||
        !/^cap_[a-f0-9]{32}$/u.test(receipt.capture_id) || !Number.isInteger(receipt.attachments_count) || receipt.attachments_count < 0 || receipt.attachments_count > CAPTURE_LIMITS.parts ||
        !['read','unread','unknown'].includes(receipt.read_state)) throw error('invalid_request');
    const identity = accountIdentity(provider,input.target);
    if (receipt.mailbox_id !== identity.mailbox_id || receipt.mail_id !== identity.mail_id ||
        receipt.manifest_path !== `email/${input.owner_scope}/${identity.mailbox_id}/${identity.mail_id}/source/${receipt.capture_id}/capture.json`) throw error('invalid_request');
  }
}

const validBatchLimit = value => Number.isInteger(value) && value >= 1 && value <= 100;
const validContinuation = value => typeof value === 'string' && (value === '' || /^(?:[a-f0-9]{64}:[1-9][0-9]{0,3}|q1:[A-Za-z0-9_-]{1,1000})$/u.test(value));

export function validateMailTarget(target) {
  if (!target || Array.isArray(target) || Object.keys(target).some(key => !['account_address','provider_message_id','provider_selection_id','provider_thread_id','folder'].includes(key)) ||
      typeof target.account_address !== 'string' || target.account_address.length > 320 ||
      /[\x00-\x20\x7f]/u.test(target.account_address) ||
      !/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/u.test(target.account_address) ||
      [target.provider_message_id,target.provider_selection_id,...(target.provider_thread_id === undefined ? [] : [target.provider_thread_id])].some(id=>typeof id!=='string'||!/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(id)) ||
      target.folder !== undefined && !['inbox','sent','all'].includes(target.folder) && !/^qq:[1-9][0-9]{3,9}$/u.test(target.folder)) throw error('invalid_request');
}

async function directory(parent, name) {
  if (!/^[a-zA-Z0-9_.-]+$/u.test(name) || name === "." || name === "..") throw error("email_capture_invalid");
  const target = path.join(parent, name);
  await fs.mkdir(target, { mode: 0o700 }).catch(cause => { if (cause.code !== "EEXIST") throw cause; });
  const stat = await fs.lstat(target);
  if (!stat.isDirectory() || stat.isSymbolicLink() || (stat.mode & 0o077)) throw error("email_capture_invalid");
  return target;
}

async function syncDirectory(target) {
  const handle = await fs.open(target, "r");
  try { await handle.sync(); } finally { await handle.close(); }
}

async function writeExclusive(target, bytes) {
  const handle = await fs.open(target, "wx", 0o600);
  try { await handle.writeFile(bytes); await handle.sync(); } finally { await handle.close(); }
  await syncDirectory(path.dirname(target));
}

async function replaceJSON(target, value) {
  const temporary = `${target}.${crypto.randomUUID()}.tmp`;
  try {
    await writeExclusive(temporary, json(value));
    await fs.rename(temporary, target);
    await syncDirectory(path.dirname(target));
  } finally { await fs.rm(temporary, { force: true }); }
}

async function readJSON(target) {
  try {
    const stat = await fs.lstat(target);
    if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 1 << 20) throw error("email_capture_invalid");
    return JSON.parse(await fs.readFile(target, "utf8"));
  } catch (cause) { if (cause.code === "ENOENT") return null; throw cause; }
}

function accountIdentity(provider, message) {
  const account = message.account_address;
  const id = message.provider_message_id;
  if (typeof account !== "string" || !/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/u.test(account) || account.length > 320 ||
      typeof id !== "string" || !id || Buffer.byteLength(id) > 1024 || /[\r\n\0]/u.test(id)) throw error("email_capture_invalid");
  const mailboxID = `mb_${hash(`${provider}\0${account.toLowerCase()}`).slice(0, 32)}`;
  return { account_address: account, provider_message_id: id, mailbox_id: mailboxID, mail_id: `mail_${hash(`${mailboxID}\0${id}`).slice(0, 32)}` };
}

function selectionIdentity(message) {
  const id = message.provider_selection_id ?? message.provider_message_id;
  const account = message.account_address;
  if (typeof account !== "string" || !/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/u.test(account) || account.length > 320 ||
      typeof id !== "string" || !id || Buffer.byteLength(id) > 2048 || /[\r\n\0]/u.test(id)) throw error("email_capture_invalid");
  return { account_address: account.toLowerCase(), provider_selection_id: id, ...(message.folder ? {folder:message.folder} : {}) };
}

function safeName(name) {
  const cleaned = String(name ?? "attachment").normalize("NFC").replace(/[/\\\x00-\x1f\x7f]/gu, "_").replace(/^\.+/u, "_");
  let result = "", bytes = 0;
  for (const character of cleaned) {
    const size = Buffer.byteLength(character);
    if (bytes + size > 200) break;
    result += character;
    bytes += size;
  }
  return result || "attachment";
}

function boundedText(value, maxBytes, nullable = false) {
  if (nullable && (value === null || value === undefined)) return null;
  if (typeof value !== "string" || value.includes("\0") || Buffer.byteLength(value) > maxBytes) throw error("email_capture_limit");
  return value;
}

async function fileRef(root, absolute) {
  const stat = await fs.lstat(absolute);
  if (!stat.isFile() || stat.isSymbolicLink() || await fs.realpath(absolute) !== path.resolve(absolute)) throw error("email_capture_invalid");
  const handle = await fs.open(absolute, "r");
  try { await handle.sync(); } finally { await handle.close(); }
  const bytes = await fs.readFile(absolute);
  return { path: path.relative(root, absolute).split(path.sep).join("/"), bytes: bytes.length, sha256: `sha256:${hash(bytes)}` };
}

async function verifyReceipt(root, receipt) {
  const ref = receipt.capture;
  if (!ref || typeof ref.manifest_path !== "string" || ref.manifest_path.split("/").some(part => !part || part === "." || part === "..")) throw error("email_capture_invalid");
  const manifestPath = path.resolve(root, ref.manifest_path);
  if (!manifestPath.startsWith(`${root}${path.sep}`)) throw error("email_capture_invalid");
  const manifestRef = await fileRef(root, manifestPath);
  if (manifestRef.sha256 !== ref.manifest_sha256) throw error("email_capture_invalid");
  const manifest = await readJSON(manifestPath);
  for (const file of manifest.files) {
    const absolute = path.resolve(root, file.path);
    if (!absolute.startsWith(`${path.dirname(manifestPath)}${path.sep}`)) throw error("email_capture_invalid");
    const observed = await fileRef(root, absolute);
    if (observed.sha256 !== file.sha256 || observed.bytes !== file.bytes) throw error("email_capture_invalid");
  }
  return receipt;
}

async function existingCapture(root, finalDir, input, provider, identity, captureID) {
  const manifest=await readJSON(path.join(finalDir,'capture.json'));
  if(!manifest)return null;
  if(manifest.schema_version!==1 || manifest.stage!=='script_capture' || manifest.provider!==provider ||
      manifest.invocation_id!==input.invocation_id || manifest.mailbox_id!==identity.mailbox_id ||
      manifest.mail_id!==identity.mail_id || manifest.capture_id!==captureID ||
      !['collected','partial'].includes(manifest.status) || !Array.isArray(manifest.attachments) ||
      !Array.isArray(manifest.files) || !manifest.files.length)throw error('email_capture_invalid');
  const receipt=receiptFor(provider,manifest,await fileRef(root,path.join(finalDir,'capture.json')),'unknown');
  await verifyReceipt(root,receipt);
  const state=await readJSON(path.join(finalDir,'read-state.json'));
  if(state?.schema_version===1 && state.state==='confirmed' && state.observed==='read')receipt.capture.read_state='read';
  return {manifest,receipt};
}

export async function captureUnread(input, runtime, provider, adapter) {
  validateCaptureInput(input, provider);
  if (!['read','capture'].includes(input.operation)) throw error('invalid_request');
  const root = runtime.emailWorkspaceRoot;
  if (typeof root !== "string" || !path.isAbsolute(root)) throw error("email_capture_unavailable");
  const rootStat = await fs.lstat(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink() || await fs.realpath(root) !== path.resolve(root)) throw error("email_capture_invalid");
  const emailRoot = await directory(root, "email");
  const ownerRoot = await directory(emailRoot, input.owner_scope);
  const invocations = await directory(ownerRoot, "invocations");
  const invocationHash = hash(`${provider}\0${input.invocation_id}`);
  const journalPath = path.join(invocations, `${invocationHash}.json`);
  let journal = await readJSON(journalPath);
  if (input.target) {
    const identity = accountIdentity(provider, input.target), selection = selectionIdentity(input.target);
    if (journal && (journal.identity?.mail_id !== identity.mail_id || journal.identity?.mailbox_id !== identity.mailbox_id)) throw error('email_capture_invalid');
    if (journal && (journal.selection?.provider_selection_id !== selection.provider_selection_id || (journal.selection?.folder ?? 'inbox') !== (selection.folder ?? 'inbox'))) {
      if (!adapter.allowCapturedRelocation) throw error('email_capture_invalid');
      // Only the trusted page collector can rebind a locator, and only after
      // proving that the immutable source belongs to this exact account/message.
      const captureID = `cap_${invocationHash.slice(0,32)}`;
      const recovered = await existingCapture(root,path.join(ownerRoot,identity.mailbox_id,identity.mail_id,'source',captureID),input,provider,identity,captureID);
      if (!recovered) throw error('email_capture_invalid');
      if (journal.receipt) await verifyReceipt(root,journal.receipt);
      journal.selection = selection;
      journal.receipt = recovered.receipt;
      await replaceJSON(journalPath,journal);
    }
    if (!journal) {
      journal = {schema_version:1,provider,invocation_id:input.invocation_id,identity,selection,receipt:null};
      await replaceJSON(journalPath,journal);
    }
  }
  if(journal?.identity && !journal.receipt) {
    const identity=accountIdentity(provider,journal.identity);
    const captureID=`cap_${invocationHash.slice(0,32)}`;
    const recovered=await existingCapture(root,path.join(ownerRoot,identity.mailbox_id,identity.mail_id,'source',captureID),input,provider,identity,captureID);
    if(recovered){journal.receipt=recovered.receipt;await replaceJSON(journalPath,journal);}
  }
  if (journal?.receipt) {
    if (journal.receipt.status === "empty") return journal.receipt;
    await verifyReceipt(root, journal.receipt);
    if (input.operation === 'capture' && !adapter.markAfterCapture || journal.receipt.status === "partial" || journal.receipt.capture.read_state === "read") return journal.receipt;
  }
  const onSelected = async message => {
    const selection = selectionIdentity(message);
    if (journal?.selection && (selection.account_address !== journal.selection.account_address || selection.provider_selection_id !== journal.selection.provider_selection_id)) throw error("email_capture_invalid");
    const identity = message.provider_message_id === undefined ? journal?.identity ?? null : accountIdentity(provider, message);
    if (journal?.identity && (identity.mail_id !== journal.identity.mail_id || identity.mailbox_id !== journal.identity.mailbox_id)) throw error("email_capture_invalid");
    journal = { schema_version: 1, provider, invocation_id: input.invocation_id, selection, identity, receipt: journal?.receipt ?? null };
    await replaceJSON(journalPath, journal);
  };
  return runtime.withReadTab(async tab => {
    const message = await adapter.collectUnread(tab, provider, { account_address:journal?.identity?.account_address, folder:journal?.selection?.folder, pinned_message_id: journal?.identity?.provider_message_id, pinned_selection_id: journal?.selection?.provider_selection_id, capture_required: !journal?.receipt, onSelected });
    if (message.status === "empty") {
      if (journal?.identity || journal?.selection) throw error("email_capture_invalid");
      const receipt = { schema_version: 1, status: "empty", provider, capture: null };
      await replaceJSON(journalPath, { schema_version: 1, provider, invocation_id: input.invocation_id, receipt });
      return receipt;
    }
    if (!journal?.identity) throw error("email_capture_invalid");
    const identity = accountIdentity(provider, message);
    if (identity.mail_id !== journal.identity.mail_id) throw error("email_capture_invalid");
    const mailboxRoot = await directory(ownerRoot, identity.mailbox_id);
    const mailRoot = await directory(mailboxRoot, identity.mail_id);
    const sources = await directory(mailRoot, "source");
    const captureID = `cap_${invocationHash.slice(0, 32)}`;
    const finalDir = path.join(sources, captureID);
    const existing = await existingCapture(root,finalDir,input,provider,identity,captureID);
    if (existing) {
      const {receipt,manifest}=existing;
      receipt.capture.read_state = await recordReadState(finalDir, (input.operation === 'read' || adapter.markAfterCapture) && manifest.status === "collected", message, () => adapter.markRead(tab, provider, message));
      journal.receipt = receipt;
      await replaceJSON(journalPath, journal);
      return verifyReceipt(root, receipt);
    }
    const stagingParent = await directory(mailRoot, "staging");
    // Account execution is serialized by the Controller. Recover only this
    // invocation's unfinished staging tree; never sweep another invocation.
    const stagingName = `attempt_${invocationHash}`;
    await fs.rm(path.join(stagingParent, stagingName), { recursive: true, force: true });
    const staging = await directory(stagingParent, stagingName);
    try {
      const captured = await collectFiles(tab, staging, message);
      const manifest = {
        schema_version: 1, stage: "script_capture", provider, ...identity, capture_id: captureID,
        invocation_id: input.invocation_id, captured_at: new Date().toISOString(), acquisition: captured.mode,
        status: captured.complete ? "collected" : "partial", metadata: captured.metadata,
        attachments: captured.attachments, files: [], coverage: captured.coverage,
      };
      for (const relative of captured.files) {
        const ref = await fileRef(root, path.join(staging, relative));
        ref.path = path.relative(root, path.join(finalDir, relative)).split(path.sep).join("/");
        manifest.files.push(ref);
      }
      const manifestBytes = json(manifest);
      if (manifestBytes.length > CAPTURE_LIMITS.manifestBytes) throw error("email_capture_limit");
      await writeExclusive(path.join(staging, "capture.json"), manifestBytes);
      await fs.rename(staging, finalDir);
      await syncDirectory(sources);
      // Background intake publishes through Store before a separate mark_read
      // job. Explicit human reading retains its existing read confirmation.
      const readState = await recordReadState(finalDir, (input.operation === 'read' || adapter.markAfterCapture) && captured.complete, message, () => adapter.markRead(tab, provider, message));
      const receipt = receiptFor(provider, manifest, await fileRef(root, path.join(finalDir, "capture.json")), readState);
      journal.receipt = receipt;
      await replaceJSON(journalPath, journal);
      return receipt;
    } finally {
      await fs.rm(staging, { recursive: true, force: true });
      await syncDirectory(stagingParent);
    }
  });
}

export async function markCapturedRead(input, runtime, provider, adapter) {
  validateCaptureInput(input,provider);
  if (input.operation !== 'mark_read') throw error('invalid_request');
  const root = runtime.emailWorkspaceRoot;
  if (typeof root !== 'string' || !path.isAbsolute(root) || await fs.realpath(root) !== root) throw error('email_capture_invalid');
  const receipt = {schema_version:1,provider,status:'collected',capture:input.committed_capture};
  await verifyReceipt(root,receipt);
  const manifestPath = path.resolve(root,input.committed_capture.manifest_path);
  const manifest = await readJSON(manifestPath);
  const identity = accountIdentity(provider,input.target);
  if (manifest.status !== 'collected' || manifest.provider !== provider || manifest.mail_id !== identity.mail_id ||
      manifest.mailbox_id !== identity.mailbox_id || manifest.provider_message_id !== input.target.provider_message_id ||
      manifest.account_address.toLowerCase() !== input.target.account_address.toLowerCase()) throw error('email_capture_invalid');
  const finalDir = path.dirname(manifestPath);
  const prior = await readJSON(path.join(finalDir,'read-state.json'));
  let readState = 'read';
  if (prior?.state !== 'confirmed' || prior.observed !== 'read') {
    readState = await runtime.withReadTab(async tab => {
      const message = await adapter.collectUnread(tab,provider,{account_address:input.target.account_address,
        pinned_message_id:input.target.provider_message_id,pinned_selection_id:input.target.provider_selection_id,
        folder:input.target.folder,capture_required:false,onSelected:async selected => {
          if (accountIdentity(provider,selected).mail_id !== identity.mail_id) throw error('email_capture_invalid');
        }});
      return recordReadState(finalDir,true,message,()=>adapter.markRead(tab,provider,message));
    });
  }
  return {schema_version:1,provider,target:input.target,read_state:readState,observed_at:new Date().toISOString()};
}

async function recordReadState(finalDir, complete, message, markRead) {
  let readState = "unknown";
  const statePath = path.join(finalDir, "read-state.json");
  const previous = await readJSON(statePath);
  // A crash after confirmation but before receipt publication must not repeat
  // the remote effect or overwrite the durable confirmation with pending.
  if (complete && previous?.schema_version === 1 && previous.state === "confirmed" && previous.observed === "read") return "read";
  await replaceJSON(statePath, { schema_version: 1, state: complete ? "pending" : "not_requested", at: new Date().toISOString() });
  if (complete) {
    try { readState = await markRead(); } catch { readState = "unknown"; }
  } else if (["read", "unread"].includes(message.read_state)) readState = message.read_state;
  if (!["read", "unread", "unknown"].includes(readState)) readState = "unknown";
  await replaceJSON(statePath, { schema_version: 1, state: complete && readState === "read" ? "confirmed" : complete ? "unknown" : "not_requested", observed: readState, at: new Date().toISOString() });
  return readState;
}

function receiptFor(provider, manifest, ref, readState) {
  return { schema_version: 1, status: manifest.status, provider, capture: {
    manifest_path: ref.path, manifest_sha256: ref.sha256,
    mailbox_id: manifest.mailbox_id, mail_id: manifest.mail_id, capture_id: manifest.capture_id,
    attachments_count: manifest.attachments.filter(part => part.status === "available").length, read_state: readState,
  } };
}

async function collectFiles(tab, staging, message) {
  const files = [], attachments = [];
  const partsDir = await directory(staging, "parts");
  let body, html, metadata, mode = "browser_dom", total = 0, skippedParts = 0, complete = message.inventory_complete === true;
  const write = async (relative, bytes) => { await writeExclusive(path.join(staging, relative), bytes); files.push(relative); };
  const storePart = async (part, index, bytes) => {
    const record = { part_id: `part_${index}`, name: safeName(part.name), original_name: part.name ?? null, disposition: part.kind ?? "attachment", content_id: part.content_id ?? null, declared_type: part.content_type ?? null, detected_type: null, status: "available", path: null };
    if (index >= CAPTURE_LIMITS.parts || bytes && (bytes.length > CAPTURE_LIMITS.partBytes || total + bytes.length > CAPTURE_LIMITS.totalBytes)) {
      record.status = "skipped_limit"; complete = false; attachments.push(record); return;
    }
    await directory(partsDir, record.part_id);
    const relative = `parts/${record.part_id}/${record.name}`;
    try {
      if (bytes) await write(relative, bytes);
      else {
        await tab.download(part.selector, path.join(staging, relative), Math.min(CAPTURE_LIMITS.partBytes, CAPTURE_LIMITS.totalBytes - total));
        files.push(relative);
      }
      const stat = await fs.stat(path.join(staging, relative));
      total += stat.size;
      record.path = relative; record.bytes = stat.size;
      record.sha256 = `sha256:${hash(await fs.readFile(path.join(staging, relative)))}`;
    } catch {
      await fs.rm(path.join(staging, relative), { force: true });
      record.status = "failed"; complete = false;
    }
    attachments.push(record);
  };
  if (message.original?.selector) {
    await tab.download(message.original.selector, path.join(staging, "message.eml"), 110 << 20);
    files.push("message.eml");
    const parsed = await simpleParser(await fs.readFile(path.join(staging, "message.eml")), { skipHtmlToText: false, skipTextToHtml: true, skipImageLinks: true });
    if (!parsed.from?.value?.length) throw error("email_capture_invalid");
    if (message.subject !== undefined && message.subject !== (parsed.subject ?? "") ||
        message.rfc_message_id && message.rfc_message_id !== parsed.messageId) throw error("email_capture_invalid");
    body = parsed.text ?? ""; html = typeof parsed.html === "string" ? parsed.html : null;
    if (message.verify_body_text) {
      // Outlook adds font icons and folds older quoted history. Compare the
      // entire visible prefix against the original's decoded text; preserve
      // every original byte and the additional historical text unchanged.
      const normalized = value=>String(value??'').normalize('NFC').replace(/[\uE000-\uF8FF]/gu,'')
        .replace(/&(lt|gt|amp|quot|apos|nbsp);/gu,(_,name)=>({lt:'<',gt:'>',amp:'&',quot:'"',apos:"'",nbsp:' '})[name])
        .replace(/\s+/gu,' ').trim();
      const visible = normalized(message.body_text), original = normalized(body);
      if (!visible || original!==visible && !original.startsWith(`${visible} `)) throw error('email_capture_invalid');
    }
    metadata = { subject: parsed.subject ?? "", from: parsed.from.value, to: parsed.to?.value ?? [], cc: parsed.cc?.value ?? [], reply_to: parsed.replyTo?.value ?? [], date: parsed.date?.toISOString() ?? null, message_id: parsed.messageId ?? null, in_reply_to: parsed.inReplyTo ?? null, references: parsed.references ?? [] };
    complete = true; mode = "rfc822";
    skippedParts = Math.max(0, parsed.attachments.length - CAPTURE_LIMITS.parts);
    if (skippedParts) complete = false;
    for (const [index, part] of parsed.attachments.slice(0, CAPTURE_LIMITS.parts).entries()) {
      await storePart({ name: part.filename, kind: part.contentDisposition === "inline" ? "inline" : "attachment", content_id: part.contentId, content_type: part.contentType }, index, part.content);
    }
  } else {
    body = message.body_text; html = message.body_html ?? null;
    metadata = { subject: message.subject ?? null, from: message.sender ?? null, to: message.to ?? [], cc: message.cc ?? [], received_at: message.received_at ?? null, headers: message.headers ?? {} };
    if (!Array.isArray(message.attachments)) throw error("email_capture_invalid");
    skippedParts = Math.max(0, message.attachments.length - CAPTURE_LIMITS.parts);
    if (skippedParts) complete = false;
    for (const [index, part] of message.attachments.slice(0, CAPTURE_LIMITS.parts).entries()) await storePart(part, index);
  }
  boundedText(body, CAPTURE_LIMITS.bodyBytes);
  boundedText(html, CAPTURE_LIMITS.bodyBytes, true);
  await write("body.txt", Buffer.from(body));
  if (html !== null) await write("body.html", Buffer.from(html));
  await write("headers.json", json(metadata));
  return { files, attachments, metadata, mode, complete, coverage: { body: "available", inventory_complete: mode === "rfc822" || message.inventory_complete === true, attachments_complete: complete, skipped_parts: skippedParts } };
}

export function pageCaptureInvocation(input, provider, target) {
  const namespace = input.invocation_id.replace(/_(?:unread|recent_observation|recent_inbound)$/u, '');
  return `email_capture_${hash([namespace, provider, target.account_address.toLowerCase(), target.provider_message_id].join('\n'))}`;
}

// A page is acknowledged only after the Gateway publishes every durable source.
// Retaining the selected inventory prevents unread transitions from hiding work
// when the process crashes before the batch response reaches the Gateway.
export async function capturePage(input, runtime, provider, adapter) {
  validateCaptureInput(input, provider);
  if (input.operation !== 'collect_page') throw error('invalid_request');
  const root = runtime.emailWorkspaceRoot;
  if (typeof root !== 'string' || !path.isAbsolute(root)) throw error('email_capture_unavailable');
  const stat = await fs.lstat(root);
  if (!stat.isDirectory() || stat.isSymbolicLink() || await fs.realpath(root) !== path.resolve(root)) throw error('email_capture_invalid');
  const ownerRoot = await directory(await directory(root, 'email'), input.owner_scope);
  const pages = await directory(ownerRoot, 'pages');
  const checkpointPath = path.join(pages, `${hash(`${provider}\0${input.invocation_id}`)}.json`);
  let checkpoint = await readJSON(checkpointPath);
  const account = input.discovery.account_address.toLowerCase();
  if (checkpoint && (checkpoint.schema_version !== 1 || checkpoint.provider !== provider ||
      checkpoint.invocation_id !== input.invocation_id || checkpoint.account_address !== account || checkpoint.lane !== input.discovery.lane ||
      !/^page_[a-f0-9]{64}$/u.test(checkpoint.page_id) || !Array.isArray(checkpoint.discovery?.candidates) ||
      checkpoint.discovery.candidates.length > 50 || !Array.isArray(checkpoint.captures) || !Array.isArray(checkpoint.failures) ||
      !Number.isInteger(checkpoint.next_index) || checkpoint.next_index < 0 || checkpoint.next_index > checkpoint.discovery.candidates.length)) throw error('email_capture_invalid');
  if (checkpoint) validateCaptureInput({...input,discovery:checkpoint.discovery_options},provider);
  const save = async () => {
    if (json(checkpoint).length > 1 << 20) throw error('email_capture_limit');
    await replaceJSON(checkpointPath, checkpoint);
  };
  const receiptResult = async () => {
    for (const entry of checkpoint.captures) {
      validateMailTarget(entry.target);
      const identity = accountIdentity(provider, entry.target);
      const invocationID = pageCaptureInvocation(input, provider, entry.target);
      const captureID = `cap_${hash(`${provider}\0${invocationID}`).slice(0,32)}`;
      const recovered = await existingCapture(root, path.join(ownerRoot, identity.mailbox_id, identity.mail_id, 'source', captureID),
        {invocation_id:invocationID}, provider, identity, captureID);
      if (!recovered || JSON.stringify(recovered.receipt.capture) !== JSON.stringify({...entry.result.capture, read_state:recovered.receipt.capture.read_state})) throw error('email_capture_invalid');
      await verifyReceipt(root,entry.result);
      // Read state is mutable evidence outside the immutable source manifest.
      // Never replay a stale confirmation after its durable record is lost.
      entry.result.capture.read_state = recovered.receipt.capture.read_state;
    }
    const {page_id,discovery,discovery_options,captures,failures} = checkpoint;
    // Empty describes this observed batch, not complete mailbox coverage. Only
    // qualified bounded observations may end normally without any mail effects;
    // missing receipt/scope evidence must remain explicitly partial.
    const emptyBatch = !discovery.candidates.length && !captures.length && !failures.length && discovery.coverage?.unsupported_rows === 0 &&
      (discovery.status === 'empty' || discovery.status === 'partial' &&
        ['loaded_rows_only','native_folder_scan_partial','folder_scope_and_pagination_unqualified'].includes(discovery.coverage.reason));
    return {schema_version:1,provider,page_id,account_address:account,discovery,discovery_options,captures,failures,
      status:emptyBatch?'empty':failures.length || discovery.status==='partial' || captures.some(entry=>entry.result.status==='partial')?'partial':'collected',
      observed_at:checkpoint.observed_at};
  };
  if (checkpoint?.complete) {
    const prior = await receiptResult();
    const unfinished = checkpoint.failures.length;
    if (input.ack_page_id === checkpoint.page_id) {
      if (unfinished) throw error('invalid_request');
      const next = await adapter.continuePage?.(checkpoint.listed,checkpoint.discovery_options,checkpoint.discovery);
      if (next) {
        checkpoint = {...checkpoint,...next,page_id:`page_${hash(crypto.randomUUID())}`,captures:[],failures:[],next_index:0,complete:false};
        delete checkpoint.observed_at;
        await save();
      } else {
        await fs.rm(checkpointPath);
        await syncDirectory(pages);
        checkpoint = null;
      }
    } else if (!unfinished) return prior;
    else {
      // Revisit unsuccessful targets from the saved inventory even if opening
      // them removed their unread bit. Completed originals remain immutable.
      // Partial originals are terminal browser results: extraction limits remain
      // explicit downstream rather than repeatedly downloading identical bytes.
      checkpoint.failures = [];
      checkpoint.next_index = 0;
      checkpoint.complete = false;
      await save();
    }
  } else if (checkpoint && input.ack_page_id === checkpoint.page_id) throw error('invalid_request');
  const recovering = Boolean(checkpoint);
  return runtime.withReadTab(async tab => {
    if (!checkpoint) {
      const {discovery,listed} = await adapter.discover(tab, input.discovery);
      if (discovery.account_address !== account) throw error('email_account_identity_mismatch');
      checkpoint = {schema_version:1,provider,invocation_id:input.invocation_id,account_address:account,lane:input.discovery.lane,
        page_id:`page_${hash(crypto.randomUUID())}`,discovery,discovery_options:input.discovery,listed,captures:[],failures:[],next_index:0,complete:false};
      await save();
    }
    for (let index=checkpoint.next_index;index<checkpoint.discovery.candidates.length;index++) {
      if (runtime.signal?.aborted) throw error('browser_extension_unavailable');
      const target = checkpoint.discovery.candidates[index];
      validateMailTarget(target);
      if (checkpoint.captures.some(entry=>entry.target.provider_message_id === target.provider_message_id)) { checkpoint.next_index = index+1; continue; }
      let opened = false;
      try {
        const captureInput = {schema_version:1,operation:'capture',provider,account:'default',owner_scope:input.owner_scope,
          invocation_id:pageCaptureInvocation(input,provider,target),target};
        const result = await captureUnread(captureInput, {...runtime,withReadTab:callback=>callback(tab)}, provider, {
          markAfterCapture:true, allowCapturedRelocation:true, markRead:adapter.markRead,
          collectUnread:async (sameTab, sameProvider, options) => {
            opened = true;
            return adapter.collect(sameTab,sameProvider,options,checkpoint.listed,recovering);
          },
        });
        checkpoint.captures.push({target,result});
      } catch (cause) {
        if (runtime.signal?.aborted) throw cause;
        const error_code = typeof cause?.code === 'string' && /^[a-z0-9_]{1,64}$/u.test(cause.code) ? cause.code : 'provider_script_failed';
        checkpoint.failures.push({target,error_code});
      }
      checkpoint.next_index = index+1;
      await save();
      if (opened && index+1<checkpoint.discovery.candidates.length) {
        try { await adapter.restore(tab,provider,checkpoint.listed); }
        catch (cause) {
          if (runtime.signal?.aborted) throw cause;
          for (const remaining of checkpoint.discovery.candidates.slice(index+1)) checkpoint.failures.push({target:remaining,error_code:'email_page_contract_changed'});
          checkpoint.next_index = checkpoint.discovery.candidates.length;
          break;
        }
      }
    }
    checkpoint.complete = true;
    checkpoint.observed_at = new Date().toISOString();
    await save();
    return receiptResult();
  });
}
