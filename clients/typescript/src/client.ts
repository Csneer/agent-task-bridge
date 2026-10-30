import { randomUUID } from 'node:crypto';
import { canonical, object, parseExact, PROTOCOL_VERSION, requestHash, type JsonObject, type JsonValue } from './json.js';
import { METHODS, ProtocolValidator } from './protocol.js';
import type { JournalEntry, RequestJournal } from './journal.js';

export type RetryAction = 'none' | 'read_state' | 'same_request' | 'reinitialize';
export interface RpcResult<T = JsonValue> { data: T; meta: { authority_id: string; replayed: boolean } }

export class RpcError extends Error {
  constructor(public readonly code: number, public readonly kind: string, public readonly retry: RetryAction, public readonly details: JsonObject = {}) {
    super(`Bridge rejected request: ${kind}`); this.name = 'RpcError';
  }
}
export class TransportError extends Error {
  constructor(public readonly status: number | undefined, public readonly outcomeUnknown: boolean) {
    super(outcomeUnknown ? 'Bridge response unavailable; preserve the original request' : `Bridge transport rejected request (${status})`);
    this.name = 'TransportError';
  }
}
export class ProtocolError extends Error {
  constructor(message: string) { super(message); this.name = 'ProtocolError'; }
}
export class PendingRequestError extends Error {
  constructor() { super('This key belongs to a preserved request; retry its original parameters'); this.name = 'PendingRequestError'; }
}

export interface ClientOptions {
  url: string;
  token: string;
  agentId?: string;
  authorityId?: string;
  timeoutMs?: number;
  maxResponseBytes?: number;
  journal?: RequestJournal;
  fetch?: typeof fetch;
}

interface PendingState extends JournalEntry { inFlight: boolean }
export interface PreparedRequest { readonly key: string; readonly method: string; readonly params: JsonObject; readonly digest: string }

function immutable<T extends JsonValue>(value: T): T {
  const clone = parseExact(canonical(value)) as T;
  function freeze(item: JsonValue): void {
    if (item && typeof item === 'object') { for (const child of Object.values(item)) freeze(child); Object.freeze(item); }
  }
  freeze(clone); return clone;
}

export async function readBounded(response: Response, maximum: number): Promise<Uint8Array> {
  const announced = response.headers.get('content-length');
  if (announced !== null && Number(announced) > maximum) { await response.body?.cancel(); throw new ProtocolError('Response exceeds client size limit'); }
  if (!response.body) return new Uint8Array();
  const reader = response.body.getReader(); const pieces: Uint8Array[] = []; let size = 0;
  try {
    for (;;) {
      const result = await reader.read(); if (result.done) break;
      size += result.value.length;
      if (size > maximum) { await reader.cancel(); throw new ProtocolError('Response exceeds client size limit'); }
      pieces.push(result.value);
    }
  } finally { reader.releaseLock(); }
  return Buffer.concat(pieces, size);
}

export class BridgeClient {
  readonly validator = new ProtocolValidator();
  readonly endpoint: string;
  readonly agentId: string | undefined;
  private readonly token: string;
  private readonly transport: typeof fetch;
  private readonly timeout: number;
  private readonly maximum: number;
  private readonly journal: RequestJournal | undefined;
  private authority: string | undefined;
  private connected = false;
  private connecting: Promise<void> | undefined;
  private readonly pending = new Map<string, PendingState>();

  constructor(options: ClientOptions) {
    const url = new URL(options.url);
    const loopback = url.hostname === 'localhost' || url.hostname === '::1' || url.hostname === '[::1]' || /^127(?:\.\d{1,3}){3}$/.test(url.hostname);
    if ((url.protocol !== 'https:' && !(url.protocol === 'http:' && loopback)) || url.username || url.password || url.hash || url.search) throw new ProtocolError('Bridge URL requires TLS or loopback HTTP without embedded credentials');
    if (url.pathname === '' || url.pathname === '/') url.pathname = '/rpc';
    if (!options.token) throw new ProtocolError('A bearer credential is required');
    this.endpoint = url.href; this.token = options.token; this.agentId = options.agentId;
    this.authority = options.authorityId; this.timeout = options.timeoutMs ?? 10_000;
    this.maximum = options.maxResponseBytes ?? 105_906_176;
    this.journal = options.journal; this.transport = options.fetch ?? fetch;
  }

  get authorityId(): string {
    if (!this.authority) throw new ProtocolError('Connect before requesting authority identity');
    return this.authority;
  }

  async connect(): Promise<void> {
    if (this.connected) return;
    if (!this.connecting) {
      this.connecting = (async () => {
        const response = await this.send('bridge.capabilities', {});
        const caps = object(response.data);
        if (caps.protocol_version !== PROTOCOL_VERSION) throw new ProtocolError('Unsupported bridge protocol version');
        if (this.authority && this.authority !== caps.authority_id) throw new ProtocolError('Bridge authority differs from configured authority');
        this.authority = caps.authority_id as string;
        this.connected = true;
      })().finally(() => { this.connecting = undefined; });
    }
    await this.connecting;
  }

  private parameters(method: string, params: JsonObject): JsonObject {
    if (method === 'bridge.capabilities') return params;
    if (params.authority_id !== undefined && params.authority_id !== this.authorityId) throw new ProtocolError('Request authority differs from connected authority');
    const result: JsonObject = { ...params, authority_id: this.authorityId };
    if (this.agentId !== undefined && result.as_agent_id === undefined) result.as_agent_id = this.agentId;
    return result;
  }

  async prepare(method: string, params: JsonObject): Promise<PreparedRequest> {
    await this.connect();
    if (METHODS.get(method)?.kind !== 'write') throw new ProtocolError('Only write methods create preserved requests');
    const full = this.parameters(method, params);
    full.idempotency_key ??= randomUUID();
    this.validator.validate('Request', { jsonrpc: '2.0', id: 'validate', method, params: full });
    const key = full.idempotency_key as string;
    const digest = requestHash(method, full);
    let state = this.pending.get(key);
    if (!state) {
      const stored = await this.journal?.load(key);
      state = this.pending.get(key);
      if (!state && stored) {
        if (stored.endpoint !== this.endpoint || requestHash(stored.method, stored.params) !== stored.digest) throw new ProtocolError('Recovery journal identity or digest mismatch');
        state = { ...stored, params: immutable(stored.params), inFlight: false };
        this.pending.set(key, state);
      }
    }
    if (state && (state.digest !== digest || state.method !== method)) throw new PendingRequestError();
    if (!state) {
      state = { endpoint: this.endpoint, method, params: immutable(full), digest, unknown: false, attempts: 0, inFlight: false };
      this.pending.set(key, state);
      await this.save(key, state);
    }
    return Object.freeze({ key, method, params: state.params, digest });
  }

  private async save(key: string, state: PendingState): Promise<void> {
    const { inFlight: _inFlight, ...entry } = state;
    await this.journal?.save(key, entry);
  }

  async execute(prepared: PreparedRequest): Promise<RpcResult> {
    const state = this.pending.get(prepared.key);
    if (!state || state.digest !== prepared.digest || state.method !== prepared.method || requestHash(prepared.method, prepared.params) !== state.digest) throw new PendingRequestError();
    if (state.inFlight) throw new PendingRequestError();
    state.inFlight = true;
    const wasUnknown = state.unknown;
    state.unknown = true; state.attempts++; state.attemptId = randomUUID();
    // Persist uncertainty before sending: process death cannot turn an in-flight call into a fresh intent.
    try { await this.save(prepared.key, state); }
    catch (error) { state.inFlight = false; throw error; }
    try {
      const result = await this.send(state.method, state.params);
      state.unknown = false; state.confirmed = true;
      await this.save(prepared.key, state);
      return result;
    } catch (error) {
      const definite = error instanceof RpcError && error.code !== -32603 && error.kind !== 'UNAVAILABLE'
        || error instanceof TransportError && !error.outcomeUnknown;
      // Shared durable sends remain uncertain until a validated success. A rejection
      // in this instance cannot prove another instance did not commit the request.
      state.unknown = this.journal !== undefined || wasUnknown || !definite;
      await this.save(prepared.key, state);
      throw error;
    } finally { state.inFlight = false; }
  }

  async retry(key: string): Promise<RpcResult> {
    await this.connect();
    let state = this.pending.get(key);
    if (!state) {
      const stored = await this.journal?.load(key);
      if (!stored) throw new ProtocolError('No preserved request for this key');
      await this.prepare(stored.method, stored.params);
      state = this.pending.get(key)!;
    }
    return this.execute({ key, method: state.method, params: state.params, digest: state.digest });
  }

  requestState(key: string): { unknown: boolean; attempts: number; inFlight: boolean } | undefined {
    const state = this.pending.get(key);
    return state ? { unknown: state.unknown, attempts: state.attempts, inFlight: state.inFlight } : undefined;
  }

  async call<T = JsonValue>(method: string, params: JsonObject = {}): Promise<RpcResult<T>> {
    if (!METHODS.has(method)) throw new ProtocolError('Unknown bridge method');
    if (method !== 'bridge.capabilities') await this.connect();
    if (METHODS.get(method)!.kind === 'write') return await this.execute(await this.prepare(method, params)) as RpcResult<T>;
    return await this.send(method, this.parameters(method, params)) as RpcResult<T>;
  }

  async claim(params: JsonObject): Promise<RpcResult> { return this.call('task.claim', params); }
  async start(params: JsonObject): Promise<RpcResult> { return this.call('run.start', params); }
  async report(params: JsonObject): Promise<RpcResult> { return this.call('run.report', params); }
  async submit(params: JsonObject): Promise<RpcResult> { return this.call('run.submit', params); }

  private async send(method: string, params: JsonObject): Promise<RpcResult> {
    const request = { jsonrpc: '2.0', id: randomUUID(), method, params };
    this.validator.validate('Request', request);
    const body = canonical(request);
    if (Buffer.byteLength(body) > 262_144) throw new ProtocolError('Request exceeds protocol size limit');
    let response: Response;
    let raw: Uint8Array;
    try {
      response = await this.transport(this.endpoint, { method: 'POST', headers: { 'content-type': 'application/json', authorization: `Bearer ${this.token}` }, body, redirect: 'error', signal: AbortSignal.timeout(this.timeout) });
      raw = await readBounded(response, this.maximum);
    } catch (error) {
      if (error instanceof ProtocolError) throw error;
      throw new TransportError(undefined, true);
    }
    if (response.status !== 200) throw new TransportError(response.status, response.status >= 500 || ![400, 401, 405, 413, 415].includes(response.status));
    if (!response.headers.get('content-type')?.toLowerCase().startsWith('application/json')) throw new ProtocolError('Unexpected bridge response media type');
    const envelope = object(parseExact(raw, this.maximum));
    if (envelope.id !== request.id) throw new ProtocolError('Mismatched JSON-RPC response ID');
    if (envelope.error !== undefined) {
      this.validator.validate('ErrorResponse', envelope);
      const error = object(envelope.error); const data = object(error.data!);
      throw new RpcError(error.code as number, data.kind as string, data.retry as RetryAction, data.details ? object(data.details) : {});
    }
    this.validator.validate('SuccessResponse', envelope);
    const result = object(envelope.result!);
    const meta = object(result.meta!);
    this.validator.validate(METHODS.get(method)!.result_data.split('/').at(-1)!, result.data);
    if (this.authority && meta.authority_id !== this.authority) throw new ProtocolError('Response authority mismatch');
    if (method === 'bridge.capabilities' && meta.authority_id !== object(result.data!).authority_id) throw new ProtocolError('Capabilities authority mismatch');
    return result as unknown as RpcResult;
  }
}
