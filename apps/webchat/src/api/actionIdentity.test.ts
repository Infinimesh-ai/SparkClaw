// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { retainedAction } from './actionIdentity';
afterEach(() => { vi.unstubAllGlobals(); });
it('keeps only opaque identities through a lost/unknown result and starts a new explicit action after delivery', async () => {
  const { webcrypto } = await vi.importActual<{ webcrypto: Crypto }>('node:crypto');
  vi.stubGlobal('crypto', webcrypto);
  const values = new Map<string, string>();
  vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) });
  const input = ['/api/owner', 'PATCH', '{"display_name":"Private name"}'] as const;
  const first = await retainedAction(...input);
  expect((await retainedAction(...input)).id).toBe(first.id);
  first.delivered(409, { code: 'operation_outcome_unknown' });
  expect((await retainedAction(...input)).id).toBe(first.id);
  first.delivered(503, {});
  expect((await retainedAction(...input)).id).toBe(first.id);
  expect(JSON.stringify([...values])).not.toContain('Private name');
  first.delivered(200, { display_name: 'Private name' });
  const successor = await retainedAction(...input);
  expect(successor.id).not.toBe(first.id);
  first.delivered(200, {});
  expect((await retainedAction(...input)).id).toBe(successor.id);
});

it('allows a later explicit browser credential check after a parsed provider failure while keeping unknown outcomes', async () => {
  const { webcrypto } = await vi.importActual<{ webcrypto: Crypto }>('node:crypto');
  vi.stubGlobal('crypto', webcrypto);
  const values = new Map<string, string>();
  const storage = {getItem:(key:string)=>values.get(key)??null, setItem:(key:string,value:string)=>values.set(key,value), removeItem:(key:string)=>values.delete(key)} as unknown as Storage;
  const input = ['/api/browser/extension/check','POST','{}',storage] as const;
  const first = await retainedAction(...input);
  first.delivered(503,{code:'operation_outcome_unknown'},true);
  expect((await retainedAction(...input)).id).toBe(first.id);
  first.delivered(503,{code:'controller_unavailable'},true);
  expect((await retainedAction(...input)).id).not.toBe(first.id);
});
