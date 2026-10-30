import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { canonical, parseExact, requestHash, ProtocolValidator, validURI } from '../dist/index.js';

const vectors = JSON.parse(await readFile(new URL('../../../protocol/m1/golden-vectors.json', import.meta.url), 'utf8'));
const validator = new ProtocolValidator();

for (const item of vectors.schema_cases) {
  test(`schema ${item.id}`, () => {
    if (item.valid) assert.doesNotThrow(() => validator.validate(item.definition, item.value));
    else assert.throws(() => validator.validate(item.definition, item.value));
  });
}
for (const item of vectors.hash_cases) {
  test(`digest ${item.id}`, () => {
    const { idempotency_key, ...params } = item.request.params;
    assert.equal(canonical({ protocol_version: 'bridge-m1-1', method: item.request.method, params }), item.canonical);
    assert.equal(requestHash(item.request.method, item.request.params), item.sha256);
  });
}
for (const item of vectors.parser_cases) {
  test(`parser ${item.id}`, () => {
    if (item.valid) assert.equal(canonical(parseExact(item.json)), item.canonical);
    else assert.throws(() => parseExact(item.json));
  });
}

test('exact parser rejects alias keys, invalid UTF8, syntax and hidden rounding', () => {
  for (const invalid of ['{"x":1,"\\u0078":2}', '{"x":1,}', '[1,]', '01', '[1 2]', 'true false', '1e999999999999999999999', '1e-999999999999999999999', '9007199254740991.00000000001', '"\\udc00"']) {
    assert.throws(() => parseExact(invalid), invalid);
  }
  assert.throws(() => parseExact(Uint8Array.from([0xc0, 0x80])));
  assert.equal(canonical(parseExact('{"__proto__":{"safe":1},"a":1.000e0}')), '{"__proto__":{"safe":1},"a":1}');
  assert.equal({}.safe, undefined);
  assert.equal(parseExact('9007199254740991000e-3'), Number.MAX_SAFE_INTEGER);
  assert.equal(parseExact('0e999999999999999999999999'), 0);
});

test('JCS preserves UTF16 sorting and rejects non-JSON values', () => {
  assert.equal(canonical({ '😀': 1, '\ue000': 2, '2': 3, '11': 4 }), '{"11":4,"2":3,"😀":1,"":2}');
  for (const value of [NaN, Infinity, 0.5, undefined, new Date(), [1, , 2]]) assert.throws(() => canonical(value));
  const circular = {}; circular.self = circular; assert.throws(() => canonical(circular));
});

test('URI distinguishes valid IP literals and invalid syntax', () => {
  for (const value of ['https://[v1.a]/', 'https://[V1.a]/', 'https://[::1]/', 'urn:example:foo']) assert.equal(validURI(value), true);
  for (const value of ['https://[1:2:3]/', 'https://[gg::1]/', 'https://u:p@example.test/', 'https://example.test/%xz']) assert.equal(validURI(value), false);
});
