import { readFile, mkdir, writeFile, rm } from 'node:fs/promises';
import { createHash } from 'node:crypto';

// Published packages contain these resources and need no install scripts.
const root = new URL('../../../', import.meta.url);
let manifest;
try {
  manifest = JSON.parse(await readFile(new URL('protocol/m1/manifest.json', root), 'utf8'));
} catch {
  throw new Error('Building this SDK requires the full agent-task-bridge checkout with protocol/m1/manifest.json. Install the release tarball for standalone use.');
}
const resources = [];
for (const name of ['schema.json', 'methods.json']) {
  const path = `protocol/m1/${name}`;
  const bytes = await readFile(new URL(path, root));
  const expected = manifest.files.find(file => file.path === path);
  if (!expected || bytes.length !== expected.bytes || createHash('sha256').update(bytes).digest('hex') !== expected.sha256) {
    throw new Error(`Frozen protocol digest mismatch: ${path}`);
  }
  resources.push([name, bytes]);
}
const destination = new URL('../dist/protocol/', import.meta.url);
await rm(new URL('../dist/', import.meta.url), { recursive: true, force: true });
await mkdir(destination, { recursive: true });
for (const [name, bytes] of resources) await writeFile(new URL(name, destination), bytes);
