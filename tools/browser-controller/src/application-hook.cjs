// Runs inside the owned Playwright daemon. The host selects this reviewed
// module through its release binding; requests cannot select a module path.
const fs = require('node:fs/promises');
const path = require('node:path');
const watched = new WeakMap();
const AWAITED_MARKER = '/* app-cli:awaited-code:v1 */';

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
      if (!activities.some(item => item.kind === 'watch')) await (await applicationHook())?.suspend?.(page);
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
async function waitForCompletion(tab, params, callback) {
  if (!process.env.APP_CLI_LEASE_FILE) return tab.waitForCompletion(callback);
  await guard(tab.page);
  if (params.code === '/* app-cli:hook:install:v1 */ async page => true') await (await applicationHook())?.install?.(tab.page);
  if (params.code === '/* app-cli:hook:activate:v1 */ async page => true') await (await applicationHook())?.activate?.(tab.page);
  if (process.env.APP_CLI_AWAITED_CODE === '1' && typeof params.code === 'string' && params.code.startsWith(AWAITED_MARKER) && !params.filename) return callback();
  return tab.waitForCompletion(callback);
}
module.exports = {waitForCompletion, guard};
