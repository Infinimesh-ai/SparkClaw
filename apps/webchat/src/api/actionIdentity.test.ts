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
