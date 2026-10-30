// Independent ECMAScript check of the profile's RFC 8785 safe-integer subset.
// This is not a daemon, SDK, or full cross-language protocol conformance test.
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';

const vectors = JSON.parse(readFileSync(new URL('./golden-vectors.json', import.meta.url), 'utf8'));
function canonical(value) {
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']';
  if (value !== null && typeof value === 'object') {
    return '{' + Object.keys(value).sort().map(key => JSON.stringify(key) + ':' + canonical(value[key])).join(',') + '}';
  }
  if (typeof value === 'number' && !Number.isSafeInteger(value)) throw new Error('not safe integer');
  if (typeof value === 'string' && !value.isWellFormed()) throw new Error('invalid Unicode');
  return JSON.stringify(value);
}
for (const test of vectors.hash_cases) {
  const { idempotency_key, ...params } = test.request.params;
  const bytes = canonical({ protocol_version: 'bridge-m1-1', method: test.request.method, params });
  const digest = createHash('sha256').update(bytes, 'utf8').digest('hex');
  if (bytes !== test.canonical || digest !== test.sha256) throw new Error(test.id + ': mismatch');
}
console.log(`PASS: ${vectors.hash_cases.length} canonical bytes and hashes match in ECMAScript. Full client conformance NOT RUN.`);
