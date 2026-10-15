import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {spawnSync} from 'node:child_process';
import test from 'node:test';
import Ajv2020 from 'ajv/dist/2020.js';

const runtime = new URL('../', import.meta.resolve('@infinimesh/app-cli-runtime/release'));
const {validateManagedSend, isManagedSend} = await import(new URL('applications/mail/lib/managed-send.mjs', runtime));
const {inputSchema} = await import(new URL('applications/mail/input-schema.mjs', runtime));
const {verifyWorkspaceAttachments} = await import(new URL('applications/mail/lib/workspace-attachments.mjs', runtime));
const {openSendJournal} = await import(new URL('applications/mail/lib/send-journal.mjs', runtime));
const attachmentModule = new URL('applications/mail/lib/workspace-attachments.mjs', runtime).href;
const hash = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const stage = '.sparkclaw-mail-send-' + 'a'.repeat(32);
const manifest = (overrides = {}) => ({path:`${stage}/00/report.txt`, name:'report.txt', size_bytes:8,
  sha256:'sha256:'+hash(Buffer.from('reviewed')), ...overrides});
const input = (attachments, provider = 'gmail') => ({schema_version:1, operation:'send', invocation_id:'attachment-review',
  provider, account:'default', account_address:'owner@example.test', mode:'compose',
  message:{to:['sink@example.test'], cc:[], subject:'Reviewed workspace files',
    body:{format:'text', content:'Explicitly confirmed body'}, ...(attachments === undefined ? {} : {attachments})}});
const validate = request => validateManagedSend(request, request.provider);
const schemaFor = provider => new Ajv2020({strict:false}).compile(inputSchema(provider, 'send'));
const attachmentError = error => /^email_attachment_(?:invalid|limit|changed)$/u.test(error?.code ?? '');

async function workspace(t) {
  const root = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), 'mail-attachments-contract-'));
  await fs.chmod(root, 0o700);
  t.after(() => fs.rm(root, {recursive:true, force:true}));
  return root;
}
async function publish(root, index = '00', bytes = Buffer.from('reviewed'), name = 'report.txt') {
  const relative = `${stage}/${index}/${name}`;
  const directory = path.dirname(path.join(root, relative));
  await fs.mkdir(directory, {recursive:true, mode:0o700});
  await fs.writeFile(path.join(root, relative), bytes, {mode:0o600});
  return manifest({path:relative, name, size_bytes:bytes.length, sha256:'sha256:'+hash(bytes)});
}

test('Go attachment wire format passes every provider schema and preserves duplicate basenames', () => {
  for (const provider of ['gmail','outlook','qq_mail']) {
    const files = [manifest(), manifest({path:`${stage}/01/report.txt`, sha256:'sha256:'+hash('other bytes'), size_bytes:11})];
    const request = input(files, provider), schema = schemaFor(provider);
    assert.equal(schema(request), true, JSON.stringify(schema.errors));
    const approved = validate(request);
    assert.deepEqual(approved.message.attachments, files);
    assert.match(approved.message.attachments[0].sha256, /^sha256:[a-f0-9]{64}$/u);
    assert.equal(approved.message.attachments[0].name, approved.message.attachments[1].name);
    assert.notEqual(approved.message.attachments[0].path, approved.message.attachments[1].path);
    files[0].name = 'mutated-after-validation.txt';
    assert.equal(approved.message.attachments[0].name, 'report.txt');
  }
});

test('malformed metadata and arbitrary host paths cannot become upload inputs', () => {
  const cases = [
    manifest({path:'/etc/passwd'}), manifest({path:'https://example.test/file'}),
    manifest({path:`${stage}/00/../report.txt`}), manifest({path:`${stage}\\00\\report.txt`}),
    manifest({path:''}), manifest({name:'different.txt'}),
    manifest({name:'../escape', path:`${stage}/00/../escape`}),
    manifest({name:'.env', path:`${stage}/00/.env`}),
    manifest({name:'file:secret', path:`${stage}/00/file:secret`}),
    manifest({name:'文'.repeat(86), path:`${stage}/00/${'文'.repeat(86)}`}),
    manifest({sha256:hash('reviewed')}), manifest({sha256:'sha256:'+'A'.repeat(64)}),
    manifest({size_bytes:-1}), manifest({size_bytes:0.5}), manifest({size_bytes:'8'}),
    {...manifest(), content_base64:'dW50cnVzdGVk'}, null,
  ];
  for (const item of cases) assert.throws(() => validate(input([item])), attachmentError, JSON.stringify(item));
  assert.throws(() => validate(input([manifest(), manifest()])), attachmentError, 'same staged path reused twice');
  const schema = schemaFor('gmail');
  for (const item of [manifest({sha256:hash('reviewed')}), manifest({size_bytes:-1}), {...manifest(), bytes:'arbitrary'}]) {
    assert.equal(schema(input([item])), false, 'schema admitted invalid Go attachment metadata');
  }
});

test('five files and ten MiB are a combined bound, with receipt-only empty paths allowed', () => {
  const files = Array.from({length:5}, (_,index) => manifest({path:`${stage}/0${index}/report.txt`, size_bytes:2<<20}));
  assert.equal(validate(input(files)).message.attachments.length, 5);
  assert.equal(schemaFor('gmail')(input(files)), true);
  assert.throws(() => validate(input([...files, manifest({path:`${stage}/05/report.txt`,size_bytes:0})])), attachmentError);
  assert.throws(() => validate(input(files.map((item,index) => index ? item : {...item,size_bytes:item.size_bytes+1}))), attachmentError);
  assert.throws(() => validate(input([manifest({size_bytes:(10<<20)+1})])), attachmentError);
  const reconcile = {...input(files.map(item=>({...item,path:''}))),mode:'reconcile'};
  assert.equal(schemaFor('gmail')(reconcile), true);
  assert.equal(validate(reconcile).message.attachments.length, 5);
});

test('verified attachment buffers remain the approved bytes after files are replaced or removed', async t => {
  const root = await workspace(t), first = Buffer.from([0,255,1,2,3]), second = Buffer.from('independent same-name content');
  const attachments = [await publish(root,'00',first), await publish(root,'01',second)];
  const verified = await verifyWorkspaceAttachments(root,attachments);
  assert.equal(verified.length,2);
  for (const item of verified) {
    assert.deepEqual(Object.keys(item).sort(), ['bytes','name']);
    assert.equal(Buffer.isBuffer(item.bytes),true, 'upload must receive bytes, not a pathname to reopen');
    assert.equal(item.name,'report.txt');
  }
  await fs.writeFile(path.join(root,attachments[0].path),'source replaced after verification');
  await fs.unlink(path.join(root,attachments[1].path));
  assert.deepEqual(verified[0].bytes,first);
  assert.deepEqual(verified[1].bytes,second);
});

test('private staging verification rejects modified bytes, symlinks, hardlinks and public files', async t => {
  for (const kind of ['changed','missing','symlink','directory-symlink','hardlink','public-file','public-directory']) {
    await t.test(kind, async t => {
      const root = await workspace(t), item = await publish(root), filename = path.join(root,item.path);
      switch (kind) {
        case 'changed': await fs.writeFile(filename,'different'); break;
        case 'missing': await fs.unlink(filename); break;
        case 'symlink': await fs.unlink(filename); await fs.writeFile(path.join(root,'outside.txt'),'reviewed',{mode:0o600}); await fs.symlink(path.join(root,'outside.txt'),filename); break;
        case 'directory-symlink': {
          const original = path.dirname(filename), moved = path.join(root,'moved');
          await fs.rename(original,moved); await fs.symlink(moved,original); break;
        }
        case 'hardlink': await fs.link(filename,path.join(root,'second-link.txt')); break;
        case 'public-file': await fs.chmod(filename,0o644); break;
        case 'public-directory': await fs.chmod(path.dirname(filename),0o755); break;
      }
      await assert.rejects(verifyWorkspaceAttachments(root,[item]));
    });
  }
});

test('a staged FIFO is rejected without blocking the provider worker', {skip:process.platform==='win32'}, async t => {
  const root = await workspace(t), item = await publish(root), filename = path.join(root,item.path);
  await fs.unlink(filename);
  const created = spawnSync('mkfifo',['-m','600',filename],{encoding:'utf8',timeout:2000});
  assert.equal(created.status,0,created.stderr);
  const script = `import {verifyWorkspaceAttachments} from ${JSON.stringify(attachmentModule)};
    try {await verifyWorkspaceAttachments(${JSON.stringify(root)},${JSON.stringify([item])}); process.exit(2);}
    catch {process.exit(0);}`;
  const child = spawnSync(process.execPath,['--input-type=module','-e',script],{encoding:'utf8',timeout:2000});
  assert.equal(child.error,undefined,`non-regular staging file blocked verification: ${child.error}`);
  assert.equal(child.status,0,child.stderr);
});

test('send receipt identity ignores staging paths and preserves the at-most-once dispatch fence', async t => {
  const root = await workspace(t), original = validate(input([await publish(root)]));
  const first = await openSendJournal(root,original);
  assert.equal(await first.write('dispatching'),true);
  const moved = structuredClone(original);
  moved.message.attachments[0].path = moved.message.attachments[0].path.replace('/00/','/04/');
  const replay = await openSendJournal(root,moved);
  assert.equal(replay.saved.stage,'dispatching');
  assert.equal(await replay.write('dispatching'),false,'retry claimed a second send effect');
  await fs.rm(path.join(root,stage),{recursive:true});
  const receiptOnly = validate({...input(original.message.attachments.map(item=>({...item,path:''}))),mode:'reconcile'});
  assert.equal((await openSendJournal(root,receiptOnly)).saved.stage,'dispatching');
  const receipt = {schema_version:1,provider:'gmail',status:'sent',recipient_digest:original.recipientDigest};
  await first.write('sent',receipt);
  assert.deepEqual((await openSendJournal(root,receiptOnly)).saved.receipt,receipt);
});

test('an existing receipt cannot be relabeled with changed attachment bytes, names or message fields', async t => {
  const root = await workspace(t), original = validate(input([await publish(root)]));
  const journal = await openSendJournal(root,original); await journal.write('dispatching');
  for (const change of ['hash','name','size','body','recipient']) {
    const changed = structuredClone(original);
    const file = changed.message.attachments[0];
    if (change==='hash') file.sha256='sha256:'+hash('different');
    if (change==='name') {file.name='different.txt';file.path=`${stage}/00/different.txt`;}
    if (change==='size') file.size_bytes++;
    if (change==='body') changed.message.body.content='changed body';
    if (change==='recipient') changed.message.to=['different@example.test'];
    await assert.rejects(openSendJournal(root,changed),{code:'email_send_journal_conflict'},change);
  }
});

test('legacy no-attachment journal remains readable with omitted or empty attachment arrays', async t => {
  const root = await workspace(t);
  const request = validate({...input(undefined),invocation_id:'legacy-no-attachments',
    message:{to:['sink@example.test'],cc:[],subject:'Legacy',body:{format:'text',content:'Unchanged body'}}});
  // This literal is the old journal's canonical payload, before attachments
  // existed. Compatibility does not rely on the new fingerprint helper.
  const legacyCanonical = '{"account":"owner@example.test","message":{"body":{"content":"Unchanged body","format":"text"},"cc":[],"subject":"Legacy","to":["sink@example.test"]},"provider":"gmail","target":null}';
  const directory = path.join(root,'email-send',hash('gmail\0owner@example.test'));
  await fs.mkdir(directory,{recursive:true,mode:0o700});
  await fs.writeFile(path.join(directory,hash(request.invocation_id)+'.json'),JSON.stringify({schema_version:1,
    fingerprint:hash(legacyCanonical),mode:'compose',stage:'dispatching',receipt:null,updated_at:'2026-10-09T00:00:00Z'}),{mode:0o600});
  assert.equal((await openSendJournal(root,request)).saved.stage,'dispatching');
  const empty = {...request,mode:'reconcile',message:{...request.message,attachments:[]}};
  assert.equal((await openSendJournal(root,empty)).saved.stage,'dispatching');
  assert.equal(schemaFor('gmail')(input(undefined)),true);
  assert.equal(Object.hasOwn(validate(input(undefined)).message,'attachments'),false);
  const legacyWire = {schema_version:1,operation:'send',invocation_id:'old-legacy-send',provider:'gmail',account:'default',
    message:{recipient:'sink@example.test',subject:'Legacy',body:{format:'text',content:'Unchanged body'}}};
  assert.equal(schemaFor('gmail')(legacyWire),true);
  assert.equal(Boolean(isManagedSend(legacyWire)),false,'old recipient-only sends must retain their original adapter');
});
