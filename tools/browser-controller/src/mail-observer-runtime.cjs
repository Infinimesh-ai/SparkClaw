// Loaded inside the owned CLI daemon, never in a provider document. The page
// has a fixed binding, not a localhost URL, filesystem path or credential.
const fs = require('node:fs/promises');
const net = require('node:net');
const installed = new WeakSet();
async function install(page) {
  if (installed.has(page)) return;
  const configPath = process.env.SPARKCLAW_MAIL_OBSERVER_CONFIG;
  if (!configPath) return;
  const config = JSON.parse(await fs.readFile(configPath, 'utf8'));
  const {installMailObserverPage} = await import('./mail-observer-page.mjs');
  const {classifyMailNotification} = await import('./mail-notification-rules.mjs');
  const socket = net.createConnection(config.socket);
  socket.on('error', () => {});
  let dropped = 0;
  await page.exposeBinding('__sparkclawMailObservation', (source, value) => {
    if (source.page !== page || source.frame !== page.mainFrame()) return;
    let origin; try { origin = new URL(source.frame.url()).origin; } catch { return; }
    if (!config.origins.includes(origin)) return;
    const data = JSON.stringify({key: config.key, value, dropped});
    if (data.length > 16384 || socket.destroyed || socket.writableLength > 65536) { dropped++; return; }
    socket.write(data + '\n'); dropped = 0;
  });
  page.once('close', () => socket.end());
  await page.addInitScript({content: `(${installMailObserverPage.toString()})(${JSON.stringify({provider: config.provider, account: config.account, origins: config.origins, evidence: config.evidence === true})}, ${classifyMailNotification.toString()});`});
  installed.add(page);
}
module.exports = {install};
