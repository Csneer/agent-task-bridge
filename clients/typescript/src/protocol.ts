import { readFileSync } from 'node:fs';
import { isIP } from 'node:net';
import { Ajv2020 } from 'ajv/dist/2020.js';
import type { ValidateFunction } from 'ajv';
import { canonical, type JsonValue } from './json.js';

export interface MethodSpec {
  method: string;
  kind: 'read' | 'write';
  params: string;
  result_data: string;
  idempotency_required: boolean;
  task_cas_required: boolean;
  lease_required: boolean;
}

const schema = JSON.parse(readFileSync(new URL('./protocol/schema.json', import.meta.url), 'utf8'));
const catalog = JSON.parse(readFileSync(new URL('./protocol/methods.json', import.meta.url), 'utf8')) as { methods: MethodSpec[] };
export const METHODS = new Map(catalog.methods.map(item => [item.method, Object.freeze(item)]));

export class ValidationError extends Error {
  constructor(public readonly definition: string, public readonly paths: string[]) {
    super(`Protocol validation failed for ${definition}: ${paths.join(', ')}`);
    this.name = 'ValidationError';
  }
}

const uriPattern = new RegExp(schema.$defs.ImmutableRef.properties.uri.pattern, 'u');
export function validURI(value: string): boolean {
  if (!uriPattern.test(value)) return false;
  const authority = /^[A-Za-z][A-Za-z0-9+.-]*:\/\/([^/?#]*)/.exec(value)?.[1];
  if (authority === undefined) return true;
  if (authority.includes('@')) return false;
  if (authority.startsWith('[')) {
    const host = authority.slice(1, authority.indexOf(']'));
    return /^[vV][0-9A-Fa-f]+\./.test(host) || isIP(host) === 6;
  }
  return true;
}

export function validTimestamp(value: string): boolean {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})\.(\d{3})Z$/.exec(value);
  if (!match) return false;
  const [year, month, day, hour, minute, second] = match.slice(1).map(Number) as [number, number, number, number, number, number];
  if (year < 1 || month < 1 || month > 12 || hour > 23 || minute > 59 || second > 59) return false;
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return day >= 1 && day <= days[month - 1]!;
}

export class ProtocolValidator {
  private readonly ajv = new Ajv2020({ strict: false, allErrors: true, validateFormats: true });
  private readonly validators = new Map<string, ValidateFunction>();

  constructor() {
    this.ajv.addFormat('date-time', validTimestamp);
    this.ajv.addFormat('uri', validURI);
    this.ajv.addSchema(schema);
  }

  validate(definition: string, value: unknown): void {
    canonical(value); // Includes all extensions and unsafe JS values, before Ajv.
    let validator = this.validators.get(definition);
    if (!validator) {
      validator = this.ajv.getSchema(`${schema.$id}#/$defs/${definition}`);
      if (!validator) throw new Error(`Unknown protocol definition ${definition}`);
      this.validators.set(definition, validator);
    }
    if (!validator(value)) {
      throw new ValidationError(definition, (validator.errors ?? []).map(error => `${error.instancePath || '/'} ${error.keyword}`));
    }
  }
}
