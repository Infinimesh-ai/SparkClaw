import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const coreRoot = path.join(packageRoot, 'node_modules/playwright-core');
const expectedVersion = '1.63.0-alpha-2026-08-31';
const patches = [
  [
    'this._handler.onExtensionDisconnect(reason);',
    'this._sparkclawDownloads.close();\n          this._handler.onExtensionDisconnect(reason);',
  ],
  [
    'this._handler = new ExtensionProtocolV2(sendCommand);',
    'this._handler = new ExtensionProtocolV2(sendCommand);\n        this._sparkclawDownloads = new (require("../../../src/extension-downloads.cjs").ExtensionDownloads)(sendCommand, message => this._sendToCDPClient(message));',
  ],
  [
    'this._extensionConnection.onmessage = (method, params2) => this._handler.handleExtensionEvent(method, params2);',
    'this._extensionConnection.onmessage = (method, params2) => { if (!this._sparkclawDownloads.handleEvent(method, params2)) this._handler.handleExtensionEvent(method, params2); };',
  ],
  [
    'case "Browser.setDownloadBehavior": {\n            return {};\n          }',
    'case "Browser.setDownloadBehavior": {\n            return await this._sparkclawDownloads.configure(params2);\n          }\n          case "Browser.cancelDownload": {\n            return await this._sparkclawDownloads.cancel(params2);\n          }',
  ],
  [
    'if (this._browser.options.name !== "clank" && this._options.acceptDownloads !== "internal-browser-default") {\n          promises2.push(this._browser._session.send("Browser.setDownloadBehavior", {\n            behavior: this._options.acceptDownloads === "accept" ? "allowAndName" : "deny",',
    'if (this._browser.options.name !== "clank" && (this._options.acceptDownloads !== "internal-browser-default" || this._browser._userAgent === "CDP-Bridge-Server/1.0.0")) {\n          promises2.push(this._browser._session.send("Browser.setDownloadBehavior", {\n            behavior: this._options.acceptDownloads === "deny" ? "deny" : "allowAndName",',
  ],
];

export function patchedBundle(source) {
  let result = source;
  for (const [before, after] of patches) {
    if (result.split(after).length === 2) continue;
    if (result.split(before).length !== 2) throw new Error('Pinned Playwright download hook changed');
    result = result.replace(before, after);
  }
  return result;
}

export async function installDownloads({ check = false } = {}) {
  const metadata = JSON.parse(await fs.readFile(path.join(coreRoot, 'package.json'), 'utf8'));
  if (metadata.version !== expectedVersion) throw new Error('Unsupported Playwright download hook version');
  const bundle = path.join(coreRoot, 'lib/coreBundle.js');
  const source = await fs.readFile(bundle, 'utf8');
  const result = patchedBundle(source);
  if (check && source !== result) throw new Error('Playwright download hooks are not installed');
  if (source !== result) await fs.writeFile(bundle, result);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await installDownloads({ check: process.argv.includes('--check') });
}
