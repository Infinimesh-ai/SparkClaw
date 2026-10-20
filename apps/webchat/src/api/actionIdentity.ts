const PREFIX = 'sparkclaw.action.v1.';
const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i;
const unresolved = new Set(['operation_outcome_unknown', 'operation_receipt_unavailable', 'operation_receipt_capacity']);

// Keep only opaque identities in renderer storage. Private intent/result data
// belongs to the native encrypted journal. Loss before a parsed reply keeps the
// same action; a later explicit action after delivery gets a distinct identity.
export async function retainedAction(target: string, method: string, body: string, storage: Storage = window.localStorage) {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify([target, method, body])));
  const key = PREFIX + Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('');
  const previous = storage.getItem(key);
  if (previous !== null && !UUID.test(previous)) throw new Error('Saved action identity is invalid');
  const id = previous ?? crypto.randomUUID();
  if (!previous) storage.setItem(key, id);
  return {
    id,
    delivered(status: number, result: unknown, repeatableCheck = false) {
      const value = result as { code?: string; error_code?: string } | null;
      if (status >= 500 && !repeatableCheck || status === 429 || unresolved.has(value?.code ?? '') || unresolved.has(value?.error_code ?? '')) return;
      // A credential check changes no binding. A later explicit check may get
      // a new UI identity; the native journal still reconciles any unresolved
      // operation before it admits that successor.
      if (storage.getItem(key) === id) storage.removeItem(key);
    }
  };
}
