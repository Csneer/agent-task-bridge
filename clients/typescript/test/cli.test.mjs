import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { readFile, mkdtemp, rm, stat } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

const vectors = JSON.parse(await readFile(new URL('../../../protocol/m1/golden-vectors.json', import.meta.url), 'utf8'));
const sample = id => structuredClone(vectors.schema_cases.find(item => item.id === id).value);

test('CLI writes private claim response and redacts terminal output', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'bridge-cli-')); t.after(() => rm(directory, { recursive: true, force: true }));
  const caps = sample('G01_Result_Capabilities').result.data;
  const task = sample('G01_Task'); Object.assign(task, { state: 'claimed', revision: 3, generation: 1, current_assignment_id: 'assignment_B' });
  const assignment = sample('G01_Assignment'); Object.assign(assignment, { state: 'claimed', ended_at: null, end_reason: null });
  const privateToken = 'B'.repeat(43);
  const http = createServer(async (request, response) => {
    const chunks = []; for await (const chunk of request) chunks.push(chunk);
    const rpc = JSON.parse(Buffer.concat(chunks).toString());
    const data = rpc.method === 'bridge.capabilities' ? caps : { task, records: [{ record_type: 'Assignment', record: assignment }], commit_id: 'commit_claim', lease_token: privateToken };
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ jsonrpc: '2.0', id: rpc.id, result: { data, meta: { authority_id: 'authority_demo', replayed: false } } }));
  });
  await new Promise(resolve => http.listen(0, '127.0.0.1', resolve));
  t.after(() => { http.closeAllConnections(); return new Promise(resolve => http.close(resolve)); });
  const destination = join(directory, 'claim.json');
  const child = spawn(process.execPath, [new URL('../dist/cli.js', import.meta.url).pathname, 'claim', '--out', destination], {
    cwd: '/tmp', env: { ...process.env, BRIDGE_URL: `http://127.0.0.1:${http.address().port}`, BRIDGE_TOKEN: 'test-secret-bearer', BRIDGE_AGENT: 'agent_B', BRIDGE_JOURNAL: join(directory, 'journal') },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stdout = ''; let stderr = '';
  child.stdout.on('data', data => { stdout += data; }); child.stderr.on('data', data => { stderr += data; });
  child.stdin.end(JSON.stringify({ task_id: 'task_T', expected_revision: 2, claim_grant_id: 'grant_B', lease_seconds: 30 }));
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve); });
  assert.equal(code, 0, stderr);
  assert.equal(stdout.includes(privateToken), false); assert.equal(stderr.includes(privateToken), false);
  assert.equal(stdout.includes('test-secret-bearer'), false); assert.equal(stderr.includes('test-secret-bearer'), false);
  assert.equal(JSON.parse(stdout).data.lease_token, '[REDACTED]');
  assert.equal(JSON.parse(await readFile(destination, 'utf8')).data.lease_token, privateToken);
  assert.equal((await stat(destination)).mode & 0o777, 0o600);
  assert.match(JSON.parse(stderr).idempotency_key, /^[0-9a-f-]{36}$/);
});
