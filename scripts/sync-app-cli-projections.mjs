import fs from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {applicationAsset} from './app-cli-artifacts.mjs';
const root = new URL('../', import.meta.url), check = process.argv.includes('--check');
const release = JSON.parse(await fs.readFile(new URL('configs/app-cli-release.json', root), 'utf8'));
const component = new URL('configs/browser-components.json', root);
const current = JSON.parse(await fs.readFile(component, 'utf8'));
const assets = JSON.parse(applicationAsset('assets/mail/manifest.json'));
current.scripts = current.scripts.filter(entry => entry.source !== 'app-cli' && !entry.file.endsWith('-mail-reader.user.js'));
current.scripts.push(...assets.scripts.map(entry => ({...entry, source:'app-cli',
  url:`${release.source_repository}/blob/${release.source_commit}/runtimes/browser/assets/mail/${entry.file}`})));
const lockFile = new URL('tools/browser-controller/package-lock.json', root);
const lock = JSON.parse(await fs.readFile(lockFile, 'utf8'));
const runtime = release.artifacts.runtime;
const packageFile = new URL('tools/browser-controller/package.json', root);
const pkg = JSON.parse(await fs.readFile(packageFile, 'utf8'));
const runtimeReference = 'file:../../vendor/app-cli/' + runtime.file;
pkg.dependencies['@infinimesh/app-cli-runtime'] = runtimeReference;
lock.packages[''].dependencies['@infinimesh/app-cli-runtime'] = runtimeReference;
lock.packages['node_modules/@infinimesh/app-cli-runtime'].version = release.id;
lock.packages['node_modules/@infinimesh/app-cli-runtime'].resolved = runtimeReference;
const packageText = JSON.stringify(pkg, null, 2) + '\n';
const archive = await fs.readFile(new URL('vendor/app-cli/' + runtime.file, root));
lock.packages['node_modules/@infinimesh/app-cli-runtime'].integrity = 'sha512-' + createHash('sha512').update(archive).digest('base64');
const lockText = JSON.stringify(lock, null, 2) + '\n';
const rendered = JSON.stringify(current, null, 2) + '\n';
const contract = new URL('services/gateway/internal/emailautomation/provider_scripts.json', root);
const projection = applicationAsset('bindings/provider-scripts.json');
if (check) {
  if (await fs.readFile(packageFile, 'utf8') !== packageText || await fs.readFile(lockFile, 'utf8') !== lockText || await fs.readFile(component, 'utf8') !== rendered || await fs.readFile(contract, 'utf8') !== projection) throw new Error('App-CLI consumer projections are stale');
} else {await fs.writeFile(packageFile, packageText); await fs.writeFile(lockFile, lockText); await fs.writeFile(component, rendered); await fs.writeFile(contract, projection);}
