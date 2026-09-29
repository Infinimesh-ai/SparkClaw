import fs from 'node:fs';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

const root = new URL('../', import.meta.url);
export function applicationAsset(relative) {
  if (!/^(?:assets\/mail\/[a-z0-9.-]+|bindings\/provider-scripts\.json)$/u.test(relative)) throw new Error('Invalid application asset');
  const release = JSON.parse(fs.readFileSync(new URL('configs/app-cli-release.json', root)));
  const artifact = release.artifacts.runtime;
  const archive = new URL(`vendor/app-cli/${artifact.file}`, root);
  if (createHash('sha256').update(fs.readFileSync(archive)).digest('hex') !== artifact.sha256) throw new Error('Application artifact mismatch');
  return execFileSync('tar', ['-xOf', fileURLToPath(archive), `package/${relative}`], {maxBuffer: 4 * 1024 * 1024}).toString('utf8');
}
