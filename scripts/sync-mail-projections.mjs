import fs from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {mailAsset} from './local-mail-assets.mjs';

const root = new URL('../', import.meta.url);
const check = process.argv.includes('--check');

// Bindings include source checksums and authorization schemas. Check their
// generator before publishing either consumer projection.
execFileSync(process.execPath, [fileURLToPath(new URL(
  'tools/browser-controller/mail-runtime/applications/mail/build-bindings.mjs', root)), '--check'], {stdio: 'inherit'});
const assets = JSON.parse(mailAsset('assets/mail/manifest.json'));
if (assets.schema_version !== 1 || !Array.isArray(assets.scripts) || !assets.scripts.length) {
  throw new Error('Invalid local mail asset manifest');
}
const files = new Set(), identities = new Set();
for (const entry of assets.scripts) {
  if (!/^[a-z0-9-]+\.user\.js$/u.test(entry.file) || !entry.uuid || files.has(entry.file) || identities.has(entry.uuid)) {
    throw new Error('Invalid or duplicate local mail asset');
  }
  files.add(entry.file);
  identities.add(entry.uuid);
  const source = mailAsset(`assets/mail/${entry.file}`);
  if (createHash('sha256').update(source).digest('hex') !== entry.sha256) {
    throw new Error(`Local mail asset checksum mismatch: ${entry.file}`);
  }
  if (source.match(/^\/\/ @version\s+(\S+)\s*$/mu)?.[1] !== entry.version) {
    throw new Error(`Local mail asset version mismatch: ${entry.file}`);
  }
}
const component = new URL('configs/browser-components.json', root);
const current = JSON.parse(await fs.readFile(component, 'utf8'));
current.scripts = current.scripts.filter(entry => !['app-cli', 'local-mail'].includes(entry.source) && !entry.file.endsWith('-mail-reader.user.js'));
current.scripts.push(...assets.scripts.map(entry => ({...entry, source: 'local-mail',
  url: `https://github.com/Infinimesh-ai/SparkClaw/blob/main/tools/browser-controller/mail-runtime/assets/mail/${entry.file}`})));
const contract = new URL('services/gateway/internal/emailautomation/provider_scripts.json', root);
for (const [target, expected] of [[component, JSON.stringify(current, null, 2) + '\n'],
  [contract, mailAsset('bindings/provider-scripts.json')]]) {
  if (check) {
    if (await fs.readFile(target, 'utf8') !== expected) throw new Error(`Local mail consumer projection is stale: ${fileURLToPath(target)}`);
  } else await fs.writeFile(target, expected);
}
