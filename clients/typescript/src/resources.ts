import { createHash, timingSafeEqual } from 'node:crypto';
import { ProtocolError, readBounded } from './client.js';
import { type JsonObject } from './json.js';
import { ProtocolValidator } from './protocol.js';

/** Resources have their own authorization; the bridge bearer is never forwarded. */
export async function fetchImmutable(ref: JsonObject, options: { fetch?: typeof fetch; maxBytes?: number; timeoutMs?: number } = {}): Promise<Uint8Array> {
  const validator = new ProtocolValidator(); validator.validate('ImmutableRef', ref);
  const uri = new URL(ref.uri as string);
  if (!['https:', 'http:'].includes(uri.protocol)) throw new ProtocolError('This resource adapter supports HTTP(S) references only');
  const response = await (options.fetch ?? fetch)(uri, { redirect: 'error', signal: AbortSignal.timeout(options.timeoutMs ?? 10_000) });
  if (!response.ok) { await response.body?.cancel(); throw new ProtocolError(`Resource read failed (${response.status})`); }
  const data = await readBounded(response, options.maxBytes ?? 16_777_216);
  const actual = createHash('sha256').update(data).digest();
  const expected = Buffer.from(ref.sha256 as string, 'hex');
  if (!timingSafeEqual(actual, expected)) throw new ProtocolError('Resource digest mismatch');
  return data;
}
