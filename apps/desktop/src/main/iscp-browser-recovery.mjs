// These are lifecycle operations, never page commands or audio/heartbeat frames.
// Their original operation receipt must settle before a replacement lifecycle
// can revoke a grant or change the polling epoch.
const lifecycle = new Set(['browser.host.grant', 'browser.host.register', 'browser.host.close', 'browser.host.revoke', 'browser.reconcile']);
export function browserMutationResource(request) {
  if (!lifecycle.has(request.operation)) return;
  const installation = request.installation_id || '', body = request.body || {};
  if (request.operation === 'browser.host.grant') return `browser-grant:${installation}`;
  if (request.operation === 'browser.host.register') return `browser-register:${installation}:${body.host_id}`;
  if (request.operation === 'browser.reconcile') return `browser-reconcile:${installation}:${body.command_id}`;
  return `${request.operation}:${installation}:${body.host_id}:${body.connection_epoch || ''}`;
}

function knownResult(journal, resource) {
  const row = journal?.read(resource);
  return row?.schema_version === 2 && ['terminal', 'applied'].includes(row.phase) && row.response?.status === 200 ? row : undefined;
}

export function expireBrowserGrant(journal, request) {
  if (request.operation !== 'browser.host.grant') return;
  const resource = browserMutationResource(request), row = knownResult(journal, resource);
  const expiry = Date.parse(row?.response.body?.expires_at);
  if (Number.isFinite(expiry) && expiry <= Date.now()) journal.retire(resource, row.operation_id, { reason: 'grant_expired', expires_at: row.response.body.expires_at });
}

export function settleBrowserLifecycle(journal, request, result, errorCode) {
  if (!journal) return;
  const body = request.body || {}, installation = request.installation_id || '';
  const registerResource = `browser-register:${installation}:${body.host_id}`;
  const registered = knownResult(journal, registerResource);
  const epoch = registered?.response.body?.connection_epoch;
  if (request.operation === 'browser.host.register' && errorCode === 'browser_fenced') {
    const rejected = journal.read(registerResource);
    const resource = `browser-grant:${installation}`, grant = knownResult(journal, resource);
    // The original registration has a durable, authenticated terminal refusal.
    // A still-unknown registration can never use this successor path.
    if (rejected?.response?.status === 409 && ['terminal', 'applied'].includes(rejected.phase)) {
      journal.retire(registerResource, rejected.operation_id, { reason: 'registration_fenced', host_id: body.host_id });
      if (grant?.response.body?.host_id === body.host_id) journal.retire(resource, grant.operation_id, { reason: 'registration_fenced', operation_id: rejected.operation_id });
    }
  }
  // A typed fence concerns this authenticated polling epoch only. It does not
  // clear an unknown register, an old page write, or a newer replacement epoch.
  if ((request.operation === 'browser.host.close' && result?.closed === true || request.operation === 'browser.host.poll' && errorCode === 'browser_fenced') && epoch && epoch === body.connection_epoch) {
    journal.retire(registerResource, registered.operation_id, { reason: 'epoch_closed', host_id: body.host_id, connection_epoch: epoch });
  }
  if (request.operation === 'browser.host.revoke' && result?.closed === true) {
    if (registered) journal.retire(registerResource, registered.operation_id, { reason: 'grant_revoked', host_id: body.host_id });
    const resource = `browser-grant:${installation}`, grant = knownResult(journal, resource);
    if (grant?.response.body?.host_id === body.host_id) journal.retire(resource, grant.operation_id, { reason: 'grant_revoked', host_id: body.host_id });
  }
}
