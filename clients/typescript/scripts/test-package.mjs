import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, mkdir, copyFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';

const source = fileURLToPath(new URL('../', import.meta.url));
const metadata = JSON.parse(await readFile(join(source, 'package.json'), 'utf8'));
const temporary = await mkdtemp(join(tmpdir(), 'bridge-package-'));
function run(command, args, cwd = temporary) {
  const result = spawnSync(command, args, { cwd, encoding: 'utf8', timeout: 120_000 });
  assert.equal(result.status, 0, `${command} ${args.join(' ')}\n${result.error ?? ''}\n${result.stdout}\n${result.stderr}`);
  return result.stdout;
}
try {
  const packed = JSON.parse(run('npm', ['pack', '--json', '--pack-destination', temporary], source))[0];
  const expectedFiles = ['LICENSE', 'README.md', 'package.json', 'dist/protocol/schema.json', 'dist/protocol/methods.json'];
  for (const module of ['cli', 'client', 'index', 'journal', 'json', 'lease', 'protocol', 'resources']) {
    expectedFiles.push(`dist/${module}.js`, `dist/${module}.d.ts`);
  }
  assert.deepEqual(packed.files.map(file => file.path).sort(), expectedFiles.sort(), 'Release includes only public runtime files');
  await writeFile(join(temporary, 'package.json'), JSON.stringify({ private: true, type: 'module' }));
  run('npm', ['install', '--ignore-scripts', '--no-audit', '--no-fund', join(temporary, packed.filename),
    `typescript@${metadata.devDependencies.typescript}`, `@types/node@${metadata.devDependencies['@types/node']}`]);
  const installed = join(temporary, 'node_modules', metadata.name);
  const manifest = JSON.parse(await readFile(new URL('../../../protocol/m1/manifest.json', import.meta.url), 'utf8'));
  for (const name of ['schema.json', 'methods.json']) {
    const bytes = await readFile(join(installed, 'dist/protocol', name));
    const expected = manifest.files.find(file => file.path === `protocol/m1/${name}`);
    assert.equal(bytes.length, expected.bytes);
    assert.equal(createHash('sha256').update(bytes).digest('hex'), expected.sha256);
  }
  assert.deepEqual(await readFile(join(installed, 'LICENSE')), await readFile(new URL('../../../LICENSE', import.meta.url)));
  run(process.execPath, ['--input-type=module', '-e', `import assert from 'node:assert/strict'; import { BridgeClient, ProtocolValidator, METHODS } from '${metadata.name}'; assert.equal(typeof BridgeClient, 'function'); assert.equal(METHODS.size, 27); new ProtocolValidator().validate('ImmutableRef', {kind:'immutable',media_type:'text/plain',version:'1',uri:'https://example.com/resource',sha256:'a'.repeat(64)});`]);
  const cli = join(temporary, 'node_modules/.bin/bridge-client');
  assert.equal(run(cli, ['--version']), `bridge-client ${metadata.version}\n`);
  assert.match(run(cli, ['--help']), /call METHOD/);
  await writeFile(join(temporary, 'consumer.ts'), `import { BridgeClient, ProtocolValidator, type JsonValue } from '${metadata.name}';\nconst value: JsonValue = { limit: 20 };\nnew ProtocolValidator();\nconst client: BridgeClient = new BridgeClient({url:'http://localhost:8080',token:'test'});\nvoid client; void value;\n`);
  run(process.execPath, [join(temporary, 'node_modules/typescript/bin/tsc'), '--noEmit', '--strict', '--target', 'ES2023', '--module', 'NodeNext', '--moduleResolution', 'NodeNext', 'consumer.ts']);
  // A copied source directory without the repository protocol fails explicitly.
  const detached = join(temporary, 'detached/scripts/copy-protocol.mjs');
  await mkdir(dirname(detached), { recursive: true });
  await copyFile(join(source, 'scripts/copy-protocol.mjs'), detached);
  const failure = spawnSync(process.execPath, [detached], { encoding: 'utf8', cwd: temporary });
  assert.notEqual(failure.status, 0);
  assert.match(failure.stderr, /requires the full agent-task-bridge checkout/);
  console.log(`PASS ${packed.filename}: exact public file list, frozen digests, standalone import, CLI, TypeScript consumer, detached-source diagnostic`);
} finally {
  await rm(temporary, { recursive: true, force: true });
}
