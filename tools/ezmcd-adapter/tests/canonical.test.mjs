import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const vectors = JSON.parse(readFileSync(new URL('./canonical-vectors.json', import.meta.url)));
function canonical(value) {
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']';
  // JSON.stringify(object) reorders integer-looking property keys. Serialize
  // entries directly so their order remains lexicographic like Python's.
  if (value && typeof value === 'object') return '{' + Object.keys(value).sort().map(key => JSON.stringify(key) + ':' + canonical(value[key])).join(',') + '}';
  if (typeof value === 'number') assert.ok(Number.isSafeInteger(value));
  return JSON.stringify(value);
}
const digest = value => createHash('sha256').update(canonical(value)).digest('hex');
const fields = ['store_code', 'pickup_at', 'eat_type', 'items', 'discount_names', 'amount_cents',
  'regular_cents', 'discount_cents', 'body', 'intent', 'checkout_digest'];

test('numeric-looking keys retain lexicographic order', () => {
  assert.equal(canonical({ 2: '汉堡', 10: ['a\nb', true, null], 1: 1490 }), '{"1":1490,"10":["a\\nb",true,null],"2":"汉堡"}');
});

test('leased session fixture shares Python/JavaScript material and envelope digests', () => {
  const value = JSON.parse(readFileSync(new URL('./session-envelope-v1.json', import.meta.url)));
  const { envelope_digest, ...body } = value;
  assert.equal(digest(body), envelope_digest);
  assert.equal(digest(value.material), value.material_digest);
});

for (const { name, envelope } of vectors) {
  test(`Python and JavaScript share ${name} transport and purchase digests`, () => {
    const { envelope_digest, ...body } = envelope;
    assert.equal(digest(body), envelope_digest);
    const upstream = digest({ identity: envelope.scope.member_digest,
      ...Object.fromEntries(fields.map(key => [key, envelope.quote[key]])) });
    assert.equal(digest({ canonicalization: 'mcd-json-v1', member_digest: envelope.scope.member_digest,
      payment_method: 'ARCHCARD', currency: 'CNY', quote_authorization_fingerprint: upstream }), envelope.purchase_digest);
  });
}
test('refresh metadata cannot change purchase content but a different benefit must', () => {
  assert.equal(vectors[0].envelope.purchase_digest, vectors[1].envelope.purchase_digest);
  assert.notEqual(vectors[0].envelope.envelope_digest, vectors[1].envelope.envelope_digest);
  assert.notEqual(vectors[0].envelope.purchase_digest, vectors[2].envelope.purchase_digest);
});
