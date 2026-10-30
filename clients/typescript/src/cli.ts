#!/usr/bin/env node
import { open, mkdir, readFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { homedir } from 'node:os';
import { createHash } from 'node:crypto';
import { BridgeClient, FileJournal, canonical, object, parseExact, fetchImmutable, RpcError, type JsonValue } from './index.js';

function redact(value: JsonValue): JsonValue {
  if (Array.isArray(value)) return value.map(redact);
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, child]) => [key, ['token', 'lease_token', 'authorization'].includes(key) ? '[REDACTED]' : redact(child)]));
  return value;
}

async function privateOutput(path: string, data: string | Uint8Array): Promise<void> {
  await mkdir(dirname(path), { recursive: true, mode: 0o700 });
  const file = await open(path, 'wx', 0o600);
  try { await file.writeFile(data); await file.sync(); } finally { await file.close(); }
}

async function stdin(): Promise<Uint8Array> {
  const chunks: Buffer[] = []; let length = 0;
  for await (const chunk of process.stdin) {
    const bytes = Buffer.from(chunk); length += bytes.length;
    if (length > 262_144) throw new Error('Input exceeds protocol request limit');
    chunks.push(bytes);
  }
  return Buffer.concat(chunks);
}

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  if (args.length === 1 && args[0] === '--version') {
    const metadata = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8')) as { version: string };
    process.stdout.write(`bridge-client ${metadata.version}\n`);
    return;
  }
  const outIndex = args.indexOf('--out');
  let output: string | undefined;
  if (outIndex >= 0) {
    output = args[outIndex + 1]; if (!output) throw new Error('--out requires a new file path');
    args.splice(outIndex, 2);
  }
  const operation = args.shift();
  if (!operation || operation === '--help') {
    process.stdout.write('bridge-client --version | --help | call METHOD | retry KEY | claim | start | report | submit | fetch-ref [--out NEW_FILE]\nJSON params/reference on stdin. BRIDGE_URL, BRIDGE_TOKEN, optional BRIDGE_AGENT, BRIDGE_JOURNAL.\nPrivate result files are mode 0600; stdout redacts lease credentials.\n');
    return;
  }
  if (operation === 'fetch-ref') {
    const ref = object(parseExact(await stdin()));
    const bytes = await fetchImmutable(ref);
    if (!output) throw new Error('fetch-ref requires --out for the verified resource');
    await privateOutput(output, bytes);
    process.stdout.write(canonical({ bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') }) + '\n');
    return;
  }
  const token = process.env.BRIDGE_TOKEN;
  const url = process.env.BRIDGE_URL;
  if (!token || !url) throw new Error('BRIDGE_URL and BRIDGE_TOKEN are required');
  const directory = process.env.BRIDGE_JOURNAL ?? join(process.env.XDG_STATE_HOME ?? join(homedir(), '.local/state'), 'agent-task-bridge', 'typescript', createHash('sha256').update(url + '\0' + token).digest('hex').slice(0, 24));
  const client = new BridgeClient({ url, token, ...(process.env.BRIDGE_AGENT ? { agentId: process.env.BRIDGE_AGENT } : {}), journal: new FileJournal(directory) });
  let result;
  if (operation === 'retry') {
    const key = args.shift(); if (!key) throw new Error('retry requires the original key');
    result = await client.retry(key);
  } else {
    const aliases: Record<string, string> = { claim: 'task.claim', start: 'run.start', report: 'run.report', submit: 'run.submit' };
    const method = operation === 'call' ? args.shift() : aliases[operation];
    if (!method) throw new Error('Unknown operation; use --help');
    const params = object(parseExact(await stdin()));
    if (['claim', 'start', 'report', 'submit'].includes(operation) || !['bridge.capabilities', 'task.get', 'record.get', 'task.discover', 'snapshot.open', 'snapshot.page', 'events.open', 'events.read', 'receipt.get'].includes(method)) {
      const request = await client.prepare(method, params);
      process.stderr.write(canonical({ idempotency_key: request.key }) + '\n');
      result = await client.execute(request);
    } else result = await client.call(method, params);
  }
  const encoded = canonical(result);
  if (output) await privateOutput(output, encoded + '\n');
  process.stdout.write(canonical(redact(parseExact(encoded))) + '\n');
}

main().catch(error => {
  const safe = error instanceof RpcError ? { error: error.kind, retry: error.retry } : { error: error instanceof Error ? error.name : 'Error', message: error instanceof Error ? error.message : 'Client failed' };
  process.stderr.write(canonical(safe) + '\n'); process.exitCode = 1;
});
