#!/usr/bin/env node
import { parseArgs } from 'node:util';
import { createRequire } from 'node:module';
import { exportTimeline } from './batch-export.mjs';
import { connectBatchBridge } from './bridge-context.mjs';
const { values } = parseArgs({ options: {
  provider: { type:'string' }, workspace: { type:'string' }, account: { type:'string' }, cdp: { type:'string' }, bridge:{type:'boolean'}, help:{type:'boolean'}
}});
if (values.help) {
  console.log('Usage: node scripts/ai-chat/export-batch.mjs --provider chatgpt|claude|gemini|grok --workspace /absolute/session/workspace --account account-workspace-label [--bridge | --cdp http://127.0.0.1:PORT]\nDefaults to the configured native Browser Bridge. No sampling limit. Uses installed SparkClaw RevivalStack fork and only a newly created task page. Reports visible-history coverage; no browser restart.');
} else {
  if (!values.workspace || !values.account || !values.provider) throw new Error('Use --help for required arguments');
  if(values.bridge && values.cdp) throw new Error('choose_one_transport');
  let connection;
  if(values.cdp) {
    const endpoint = new URL(values.cdp);
    if (!['http:', 'ws:'].includes(endpoint.protocol) || !['127.0.0.1','localhost','[::1]'].includes(endpoint.hostname) || endpoint.username || endpoint.password) throw new Error('cdp_must_be_explicit_local_endpoint');
    const require = createRequire(new URL('../../tools/browser-controller/package.json', import.meta.url));
    const { chromium } = require('playwright');
    const browser = await chromium.connectOverCDP(values.cdp);
    connection={context:browser.contexts()[0],close:()=>browser.close()};
  } else { connection=await connectBatchBridge(); }
  const abort = new AbortController();
  const stop = () => abort.abort(new Error('batch_canceled'));
  process.once('SIGINT',stop); process.once('SIGTERM',stop);
  try {
    const context=connection.context;
    if (!context) throw new Error('browser_context_missing');
    const receipt=await exportTimeline({context,provider:values.provider,workspaceRoot:values.workspace,accountScope:values.account,signal:abort.signal});
    console.log(JSON.stringify(receipt,null,2));
    if(receipt.status === 'partial') process.exitCode=1;
  } finally {
    process.removeListener('SIGINT',stop);process.removeListener('SIGTERM',stop);
    // For a CDP connection close() disconnects this client, not the host browser.
    await connection.close();
  }
}
