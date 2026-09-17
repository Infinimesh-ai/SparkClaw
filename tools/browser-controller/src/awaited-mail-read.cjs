// The managed Reader already awaits its bounded native response and validates
// its account. Do not wait for unrelated mailbox analytics/long-poll traffic.
// Both a task-scoped environment gate and a Controller-owned code marker are
// required. All unmarked/UI/send operations retain upstream completion rules.
const MARKER = '/* sparkclaw:awaited-mail-read:v1 */';

async function waitForMailRead(tab, params, callback) {
  if (process.env.SPARKCLAW_AWAITED_MAIL_READ === '1' &&
      typeof params.code === 'string' && params.code.startsWith(MARKER) &&
      !params.filename) return await callback();
  return await tab.waitForCompletion(callback);
}

module.exports = { MARKER, waitForMailRead };
