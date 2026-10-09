// Runs inside the owned Playwright daemon. The host selects this reviewed
// module through its release binding; requests cannot select a module path.
const fs = require('node:fs/promises');
const path = require('node:path');
const watched = new WeakMap();
const AWAITED_MARKER = '/* app-cli:awaited-code:v1 */';
const OWNED_CHOOSER_MARKER = AWAITED_MARKER + '\n/* app-cli:owned-file-chooser:v1 */\n';
const OWNED_CHOOSER_ACK = Symbol.for('sparkclaw.app-cli.owned-file-chooser.v1');

async function readStamp() {
  const file = process.env.APP_CLI_LEASE_FILE;
  if (!file || !path.isAbsolute(file)) throw new Error('host_lease_missing');
  const stat = await fs.lstat(file);
  if (!stat.isFile() || stat.isSymbolicLink() || (stat.mode & 0o077) || stat.size > 65536) throw new Error('host_lease_invalid');
  const stamp = JSON.parse(await fs.readFile(file, 'utf8'));
  if (!Array.isArray(stamp.activities)) throw new Error('host_lease_invalid');
  return stamp;
}
async function applicationHook() {
  const file = process.env.APP_CLI_HOOK_CONFIG;
  if (!file) return null;
  const config = JSON.parse(await fs.readFile(file, 'utf8'));
  if (!path.isAbsolute(config.module)) throw new Error('host_hook_invalid');
  return require(config.module);
}
async function guard(page) {
  if (watched.has(page)) return;
  const original = await readStamp();
  let busy = false, closed = false;
  const deadlines = new Map();
  const live = item => {
    const key = item.id ?? 'idle';
    const previous = deadlines.get(key);
    if (!previous || previous.expires !== item.expires_ms) deadlines.set(key, {
      expires: item.expires_ms, end: performance.now() + Math.max(0, Math.min(item.id ? 30000 : 1800000, item.expires_ms - Date.now())),
    });
    return item.expires_ms > Date.now() && deadlines.get(key).end > performance.now();
  };
  const inspect = async () => {
    if (busy || closed) return; busy = true;
    try {
      const stamp = await readStamp();
      if (stamp.epoch !== original.epoch || stamp.generation !== original.generation) throw new Error('host_generation_stale');
      const activities = stamp.activities.filter(live);
      if (!activities.length && !(live({expires_ms: stamp.idle_until_ms ?? 0}))) throw new Error('host_lease_expired');
      if (stamp.activities.some(item => item.kind !== 'watch' && !live(item))) throw new Error('host_action_expired');
      // A watch has its own activity lease. Its expiration never closes a
      // page while another authorized activity is still using that page.
      if (!activities.some(item => item.kind === 'watch')) {
        try {await (await applicationHook())?.suspend?.(page);}
        catch (error) {
          // A valid Reader lease also covers its initial navigation. The old
          // document can disappear during this local observer suspension;
          // retry on the next guard tick, never retire the authorized page.
          // Lease validation above and every other hook failure stay closed.
          if (!/Execution context was destroyed|Cannot find context with specified id/u.test(String(error?.message))) throw error;
        }
      }
    } catch {
      closed = true; clearInterval(timer);
      try {await (await applicationHook())?.dispose?.(page);} catch {}
      try {await page.close();} catch {}
    } finally {busy = false;}
  };
  const timer = setInterval(inspect, 500); timer.unref();
  page.once('close', () => {closed = true; clearInterval(timer);});
  watched.set(page, {inspect});
}
// The fixed application upload consumes bytes itself, so the MCP wrapper's
// generic path-upload modal must be retired only for that exact completed event.
async function completeOwnedChooser(tab, callback) {
  const page = tab.page, observed = [];
  if (tab.modalStates().length || page[OWNED_CHOOSER_ACK] !== undefined) throw new Error('host_owned_chooser_unverified');
  const remember = chooser => observed.push(chooser);
  page.on('filechooser', remember);
  try {
    const result = await callback();
    const states = tab.modalStates(), ack = page[OWNED_CHOOSER_ACK];
    // A typed prepare rejection can return before opening any chooser.
    if (!states.length && !observed.length && ack === undefined) return result;
    if (states.length !== 1 || observed.length !== 1 || states[0].type !== 'fileChooser' ||
        states[0].fileChooser !== observed[0] || ack?.chooser !== observed[0] ||
        ack.input !== observed[0].element()) throw new Error('host_owned_chooser_unverified');
    tab.clearModalState(states[0]);
    return result;
  } finally {
    page.off('filechooser', remember);
    delete page[OWNED_CHOOSER_ACK];
  }
}
async function waitForCompletion(tab, params, callback) {
  if (!process.env.APP_CLI_LEASE_FILE) return tab.waitForCompletion(callback);
  await guard(tab.page);
  if (params.code === '/* app-cli:secrets:reload:v1 */ async page => true' && !params.filename) {
    // The daemon starts before the application supplies private form values.
    // CLI command environments do not reload its Context configuration. Read
    // only this owned session's fixed file, never a request-supplied path.
    const file = path.join(path.dirname(process.env.APP_CLI_LEASE_FILE), 'secrets.json');
    const stat = await fs.lstat(file);
    if (!stat.isFile() || stat.isSymbolicLink() || (stat.mode & 0o077) || stat.size > (2 << 20)) throw new Error('host_secrets_invalid');
    const value = JSON.parse(await fs.readFile(file, 'utf8'));
    const secrets = value?.secrets;
    if (!secrets || Array.isArray(secrets) || Object.keys(value).join(',') !== 'secrets' ||
        Object.keys(secrets).length > 104 || Object.entries(secrets).some(([key, value]) =>
          !/^[A-Z][A-Z0-9_]{0,63}$/.test(key) || typeof value !== 'string' || Buffer.byteLength(value) > 204800)) throw new Error('host_secrets_invalid');
    tab.context.config.secrets = secrets;
  }
  if (params.code === '/* app-cli:hook:install:v1 */ async page => true') await (await applicationHook())?.install?.(tab.page);
  if (params.code === '/* app-cli:hook:activate:v1 */ async page => true') await (await applicationHook())?.activate?.(tab.page);
  if (process.env.APP_CLI_AWAITED_CODE === '1' && typeof params.code === 'string' && params.code.startsWith(AWAITED_MARKER) && !params.filename) {
    if (params.code.startsWith(OWNED_CHOOSER_MARKER)) return completeOwnedChooser(tab, callback);
    return callback();
  }
  return tab.waitForCompletion(callback);
}
module.exports = {waitForCompletion, guard};
