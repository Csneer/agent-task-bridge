import { createHash } from 'node:crypto';

export type JsonValue = null | boolean | number | string | JsonValue[] | JsonObject;
export interface JsonObject { [key: string]: JsonValue }
export const PROTOCOL_VERSION = 'bridge-m1-1';

export class JsonFormatError extends Error {
  constructor(message: string) { super(message); this.name = 'JsonFormatError'; }
}

function validString(value: string): boolean {
  for (let i = 0; i < value.length; i++) {
    const unit = value.charCodeAt(i);
    if (unit >= 0xd800 && unit <= 0xdbff) {
      const next = value.charCodeAt(++i);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return false;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) return false;
  }
  return true;
}

function integerFromLexeme(raw: string): number {
  const match = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(raw);
  if (!match) throw new JsonFormatError('Invalid JSON number');
  const fraction = match[3] ?? '';
  let digits = (match[2]! + fraction).replace(/^0+/, '');
  if (digits === '') return 0;
  const exponentText = (match[4] ?? '0').replace(/^[+-]?0+/, '');
  // With bounded input, a larger exponent cannot yield a nonzero safe integer.
  if (exponentText.replace(/^[+-]/, '').length > 6) throw new JsonFormatError('Number is not an exact safe integer');
  const exponent = Number(match[4] ?? '0');
  let shift = exponent - fraction.length;
  if (shift < 0) {
    const remove = -shift;
    if (remove >= digits.length || !/^0+$/.test(digits.slice(-remove))) {
      throw new JsonFormatError('Fractional numbers are not supported');
    }
    digits = digits.slice(0, -remove);
    shift = 0;
  }
  if (digits.length + shift > 16) throw new JsonFormatError('Number exceeds safe integer range');
  const exact = BigInt(digits + '0'.repeat(shift)) * (match[1] === '-' ? -1n : 1n);
  if (exact < -9007199254740991n || exact > 9007199254740991n) throw new JsonFormatError('Number exceeds safe integer range');
  return Number(exact);
}

/** Parse before conversion to JS numbers; never permit rounding to change a request. */
export function parseExact(input: string | Uint8Array, maxBytes = 33_554_432): JsonValue {
  let source: string;
  try { source = typeof input === 'string' ? input : new TextDecoder('utf-8', { fatal: true }).decode(input); }
  catch { throw new JsonFormatError('Invalid UTF-8'); }
  if (Buffer.byteLength(source, 'utf8') > maxBytes) throw new JsonFormatError('JSON exceeds client size limit');
  let position = 0;
  const fail = (): never => { throw new JsonFormatError(`Invalid JSON at character ${position}`); };
  const space = (): void => { while (position < source.length && /[\x20\t\r\n]/.test(source[position]!)) position++; };
  function string(): string {
    const begin = position++;
    while (position < source.length) {
      const c = source[position++]!;
      if (c === '\\') { position++; continue; }
      if (c === '"') {
        let value: unknown;
        try { value = JSON.parse(source.slice(begin, position)); } catch { return fail(); }
        if (typeof value !== 'string' || !validString(value)) throw new JsonFormatError('Invalid Unicode string');
        return value;
      }
    }
    return fail();
  }
  function value(depth: number): JsonValue {
    if (depth > 128) throw new JsonFormatError('JSON exceeds client nesting limit');
    space();
    const c = source[position];
    if (c === '"') return string();
    if (c === '{') {
      position++; space();
      const object: JsonObject = Object.create(null) as JsonObject;
      if (source[position] === '}') { position++; return object; }
      while (position < source.length) {
        space(); if (source[position] !== '"') return fail();
        const key = string();
        if (Object.hasOwn(object, key)) throw new JsonFormatError('Duplicate JSON property');
        space(); if (source[position++] !== ':') return fail();
        object[key] = value(depth + 1); space();
        const end = source[position++];
        if (end === '}') return object;
        if (end !== ',') return fail();
      }
      return fail();
    }
    if (c === '[') {
      position++; space();
      const array: JsonValue[] = [];
      if (source[position] === ']') { position++; return array; }
      while (position < source.length) {
        array.push(value(depth + 1)); space();
        const end = source[position++];
        if (end === ']') return array;
        if (end !== ',') return fail();
      }
      return fail();
    }
    for (const [word, literal] of [['true', true], ['false', false], ['null', null]] as const) {
      if (source.startsWith(word, position)) { position += word.length; return literal; }
    }
    const number = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(source.slice(position));
    if (!number) return fail();
    position += number[0].length;
    return integerFromLexeme(number[0]);
  }
  const result = value(0); space();
  if (position !== source.length) return fail();
  return result;
}

/** RFC 8785 serialization for the protocol's safe-integer-only subset. */
export function canonical(value: unknown): string {
  const ancestors = new Set<object>();
  function encode(item: unknown, depth: number): string {
    if (depth > 128) throw new JsonFormatError('JSON exceeds client nesting limit');
    if (item === null || typeof item === 'boolean') return JSON.stringify(item);
    if (typeof item === 'string') {
      if (!validString(item)) throw new JsonFormatError('Invalid Unicode string');
      return JSON.stringify(item);
    }
    if (typeof item === 'number') {
      if (!Number.isSafeInteger(item)) throw new JsonFormatError('Number is not a safe integer');
      return JSON.stringify(item);
    }
    if (typeof item !== 'object' || item === null) throw new JsonFormatError('Not a JSON value');
    if (ancestors.has(item)) throw new JsonFormatError('Cyclic JSON value');
    ancestors.add(item);
    let result: string;
    if (Array.isArray(item)) {
      if (Object.keys(item).length !== item.length) throw new JsonFormatError('Sparse or decorated array');
      result = '[' + item.map(child => encode(child, depth + 1)).join(',') + ']';
    } else {
      if (Object.getPrototypeOf(item) !== Object.prototype && Object.getPrototypeOf(item) !== null) throw new JsonFormatError('Not a plain JSON object');
      result = '{' + Object.keys(item).sort().map(key => encode(key, depth + 1) + ':' + encode((item as Record<string, unknown>)[key], depth + 1)).join(',') + '}';
    }
    ancestors.delete(item);
    return result;
  }
  return encode(value, 0);
}

export function requestHash(method: string, params: JsonObject): string {
  const { idempotency_key: _key, ...semantic } = params;
  return createHash('sha256').update(canonical({ protocol_version: PROTOCOL_VERSION, method, params: semantic }), 'utf8').digest('hex');
}

export function object(value: JsonValue): JsonObject {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) throw new JsonFormatError('Expected a JSON object');
  return value;
}
