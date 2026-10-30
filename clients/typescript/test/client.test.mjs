import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, writeFile, mkdtemp, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { BridgeClient, FileJournal, LeaseSession, PendingRequestError, RpcError, TransportError, fetchImmutable, parseExact, canonical } from '../dist/index.js';

const vectors = JSON.parse(await readFile(new URL('../../../protocol/m1/golden-vectors.json', import.meta.url), 'utf8'));
const sample = id => structuredClone(vectors.schema_cases.find(item => item.id === id).value);
const caps = sample('G01_Result_Capabilities').result.data;
const command = sample('G01_Result_CommandResult').result.data;
const params = sample('G01_Request_run.submit').params;
const send = (response, request, data, replayed = false) => {
  response.writeHead(200, { 'content-type': 'application/json' });
  response.end(JSON.stringify({ jsonrpc: '2.0', id: request.id, result: { data, meta: { authority_id: 'authority_demo', replayed } } }));
};
const reject = (response, request, code, kind, retry = 'none') => {
  response.writeHead(200, { 'content-type': 'application/json' });
  response.end(JSON.stringify({ jsonrpc: '2.0', id: request.id, error: { code, message: kind, data: { kind, retry } } }));
};

async function server(t, handler) {
  const http = createServer(async (request, response) => {
    try {
      const chunks = []; for await (const chunk of request) chunks.push(chunk);
      const body = parseExact(Buffer.concat(chunks));
      if (body.method === 'bridge.capabilities') send(response, body, caps);
      else await handler(body, response, request);
    } catch (error) { response.destroy(error); }
  });
  await new Promise(resolve => http.listen(0, '127.0.0.1', resolve));
  t.after(() => { http.closeAllConnections(); return new Promise(resolve => http.close(resolve)); });
  return `http://127.0.0.1:${http.address().port}`;
}

test('unknown submit survives rejection and process restart with exact original request', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'bridge-ts-journal-')); t.after(() => rm(directory, { recursive: true, force: true }));
  let attempts = 0; const seen = [];
  const url = await server(t, (request, response, transport) => {
    assert.equal(transport.headers.authorization, 'Bearer test-private-credential');
    seen.push(request.params); attempts++;
    if (attempts === 1) response.destroy();
    else if (attempts === 2) reject(response, request, -32001, 'FORBIDDEN');
    else send(response, request, command, true);
  });
  const options = { url, token: 'test-private-credential', journal: new FileJournal(directory) };
  const client = new BridgeClient(options);
  const request = await client.prepare('run.submit', params);
  await assert.rejects(client.execute(request), TransportError);
  assert.equal(client.requestState(request.key).unknown, true);
  await assert.rejects(client.retry(request.key), error => error instanceof RpcError && error.kind === 'FORBIDDEN');
  assert.equal(client.requestState(request.key).unknown, true);
  await assert.rejects(client.prepare('run.submit', { ...params, expected_revision: 99 }), PendingRequestError);
  const restarted = new BridgeClient(options);
  const recovered = await restarted.retry(request.key);
  assert.equal(canonical(recovered.data), canonical(command));
  assert.equal(recovered.meta.replayed, true);
  assert.equal(restarted.requestState(request.key).unknown, false);
  assert.equal(seen.length, 3);
  for (const item of seen) assert.deepEqual(item, seen[0]);
  assert.equal((await stat(join(directory, request.key + '.json'))).mode & 0o777, 0o600);
  assert.equal((await readFile(join(directory, request.key + '.json'), 'utf8')).includes('test-private-credential'), false);
});

test('prepared params cannot mutate and simultaneous copies are not started', async t => {
  let release; let entered; const enteredPromise = new Promise(resolve => { entered = resolve; });
  const url = await server(t, async (request, response) => { entered(); await new Promise(resolve => { release = resolve; }); send(response, request, command); });
  const client = new BridgeClient({ url, token: 'local-test' });
  const mutable = structuredClone(params); const request = await client.prepare('run.submit', mutable);
  mutable.summary = 'changed'; assert.notEqual(request.params.summary, mutable.summary);
  assert.throws(() => { request.params.summary = 'changed'; });
  const running = client.execute(request); await enteredPromise;
  await assert.rejects(client.execute(request), PendingRequestError);
  release(); await running;
});

test('response identity and unsupported version stop the client', async t => {
  const url = await server(t, (request, response) => send(response, { ...request, id: 'wrong' }, sample('G01_Task')));
  await assert.rejects(new BridgeClient({ url, token: 'local-test' }).call('task.get', { task_id: 'task_T' }), /response ID/);
  const wrong = structuredClone(caps); wrong.protocol_version = 'bridge-other';
  const client = new BridgeClient({ url, token: 'local-test', fetch: async (_url, init) => {
    const request = JSON.parse(init.body);
    return new Response(JSON.stringify({ jsonrpc: '2.0', id: request.id, result: { data: wrong, meta: { authority_id: 'authority_demo', replayed: false } } }), { headers: { 'content-type': 'application/json' } });
  } });
  await assert.rejects(client.connect());
});

test('read response rejects raw fractional rounding before JSON conversion', async t => {
  const url = await server(t, (request, response) => {
    const task = sample('G01_Task');
    const body = JSON.stringify({ jsonrpc: '2.0', id: request.id, result: { data: task, meta: { authority_id: 'authority_demo', replayed: false } } }).replace('"revision":2', '"revision":2.00000000000000001');
    response.writeHead(200, { 'content-type': 'application/json' }); response.end(body);
  });
  await assert.rejects(new BridgeClient({ url, token: 'local-test' }).call('task.get', { task_id: 'task_T' }), /Fractional/);
});

test('lease session serializes renew/report CAS and checks a fenced operation on resume', async t => {
  const task = sample('G01_Task'); Object.assign(task, { state: 'active', revision: 4, generation: 1, current_assignment_id: 'assignment_B' });
  const assignment = sample('G01_Assignment'); Object.assign(assignment, { state: 'active', ended_at: null, end_reason: null });
  const run = sample('G01_Run'); Object.assign(run, { state: 'running', ended_at: null, end_reason: null });
  const methods = [];
  const url = await server(t, (request, response) => {
    if (request.method === 'task.get') { send(response, request, task); return; }
    methods.push(request.method); assert.equal(request.params.expected_revision, task.revision);
    task.revision++;
    send(response, request, { task, records: [{ record_type: 'Assignment', record: assignment }, { record_type: 'Run', record: run }], commit_id: `commit_${task.revision}` });
  });
  const client = new BridgeClient({ url, token: 'local-test', agentId: 'agent_B' });
  const session = new LeaseSession(client, { taskId: 'task_T', assignmentId: 'assignment_B', generation: 1, token: 'A'.repeat(43), agentId: 'agent_B' });
  await Promise.all([session.renew(30), session.report('run_B', 'working')]);
  await session.resume('run_B');
  assert.deepEqual(methods, ['assignment.renew', 'run.report', 'run.report']);
  task.current_assignment_id = null; task.state = 'submitted'; task.pending_submission_id = 'submission_B';
  await assert.rejects(session.resume('run_B'), /no longer current/);
  assert.equal(methods.length, 3);
});

test('resource reads verify bytes and never forward bridge credentials', async t => {
  const contents = Buffer.from('actual review input\n'); let credentials;
  const http = createServer((request, response) => { credentials = request.headers.authorization; response.end(contents); });
  await new Promise(resolve => http.listen(0, '127.0.0.1', resolve));
  t.after(() => { http.closeAllConnections(); return new Promise(resolve => http.close(resolve)); });
  const ref = { kind: 'immutable', uri: `http://127.0.0.1:${http.address().port}/input`, media_type: 'text/plain', version: '1', sha256: createHash('sha256').update(contents).digest('hex') };
  assert.deepEqual(Buffer.from(await fetchImmutable(ref)), contents); assert.equal(credentials, undefined);
  await assert.rejects(fetchImmutable({ ...ref, sha256: 'a'.repeat(64) }), /digest mismatch/);
});

test('remote plaintext endpoints and embedded credentials are rejected', () => {
  for (const url of ['http://example.test/rpc', 'https://user:password@example.test/rpc']) assert.throws(() => new BridgeClient({ url, token: 'local-test' }));
});

test("supervisor stops before confirmed expiry while HTTP renew is blocked; late success cannot restart it", async t => {
  const task = sample("G01_Task"); Object.assign(task, { state:"active",revision:4,generation:1,current_assignment_id:"assignment_B" });
  const assignment = sample("G01_Assignment"); Object.assign(assignment,{state:"active",ended_at:null,end_reason:null,lease_expires_at:new Date(Date.now()+1500).toISOString()});
  let release; const blocked = new Promise(resolve => {release=resolve;}); let renews=0;
  const url = await server(t, async(request,response)=>{
    if(request.method === "task.get"){send(response,request,task);return;}
    renews++; await blocked;task.revision++;
    assignment.lease_expires_at=new Date(Date.now()+30000).toISOString();
    send(response,request,{task,records:[{record_type:"Assignment",record:assignment}],commit_id:"commit_renew"});
  });
  const client=new BridgeClient({url,token:"local-test",agentId:"agent_B",timeoutMs:5000});
  const claim={data:{task,records:[{record_type:"Assignment",record:assignment}],lease_token:"A".repeat(43)},meta:{authority_id:"authority_demo",replayed:false}};
  const session=LeaseSession.fromClaim(client,claim);let failures=0;
  const expiry=Date.parse(assignment.lease_expires_at);
  const failed=new Promise(resolve=>session.supervise({leaseSeconds:30,intervalMs:10,safetyMarginMs:500,onError:error=>{failures++;resolve({error,at:Date.now()});}}));
  const failure=await failed;assert.match(failure.error.message,/safety deadline/);assert.ok(failure.at<expiry);assert.equal(renews,1);
  release();await new Promise(resolve=>setTimeout(resolve,100));assert.equal(renews,1);assert.equal(failures,1);
});

test("journal concurrent clients cannot erase uncertain send or replace an immutable request",async t=>{
  const directory=await mkdtemp(join(tmpdir(),"bridge-shared-journal-"));t.after(()=>rm(directory,{recursive:true,force:true}));
  let attempts=0,release,entered;const blocked=new Promise(resolve=>{release=resolve;});const reached=new Promise(resolve=>{entered=resolve;});
  const url=await server(t,async(request,response)=>{
    attempts++;
    if(attempts===1){entered();await blocked;response.destroy();}
    else if(attempts===2)reject(response,request,-32001,"FORBIDDEN");
    else send(response,request,command,true);
  });
  const options={url,token:"local-test",journal:new FileJournal(directory)};
  const a=new BridgeClient(options),b=new BridgeClient(options);
  const first=await a.prepare("run.submit",params),second=await b.prepare("run.submit",params);
  const inFlight=a.execute(first);const firstFailure=assert.rejects(inFlight,TransportError);await reached;
  await assert.rejects(b.execute(second),error=>error.kind==="FORBIDDEN");
  assert.equal(b.requestState(second.key).unknown,true);
  assert.equal((await options.journal.load(first.key)).unknown,true);
  release();await firstFailure;
  assert.equal((await new FileJournal(directory).load(first.key)).unknown,true);
  const competing=new BridgeClient(options);
  await assert.rejects(competing.prepare("run.submit",{...params,summary:"different command"}));
  assert.equal((await options.journal.load(first.key)).params.summary,params.summary);
  await new BridgeClient(options).retry(first.key);
  const restored=await options.journal.load(first.key);assert.equal(restored.unknown,false);assert.equal(restored.confirmed,true);assert.equal(restored.attempts,3);
});

test("simultaneous first journal publications permit only one immutable digest",async t=>{
  const directory=await mkdtemp(join(tmpdir(),"bridge-journal-race-"));t.after(()=>rm(directory,{recursive:true,force:true}));
  const url=await server(t,()=>{throw Error("No command should be sent");});
  const a=new BridgeClient({url,token:"local-test",journal:new FileJournal(directory)}),b=new BridgeClient({url,token:"local-test",journal:new FileJournal(directory)});
  const results=await Promise.allSettled([a.prepare("run.submit",params),b.prepare("run.submit",{...params,summary:"other intent"})]);
  assert.equal(results.filter(r=>r.status==="fulfilled").length,1);
  const winner=results.find(r=>r.status==="fulfilled").value;
  assert.equal((await new FileJournal(directory).load(winner.key)).digest,winner.digest);
});


test('legacy mutable uncertainty is rejected instead of treated as unsent', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'bridge-legacy-journal-'));
  t.after(() => rm(directory, {recursive:true,force:true}));
  await writeFile(join(directory, params.idempotency_key + '.json'), JSON.stringify({endpoint:'http://127.0.0.1/rpc',method:'run.submit',params,digest:'old',unknown:true,attempts:1}), {mode:0o600});
  await assert.rejects(new FileJournal(directory).load(params.idempotency_key), /refusing to discard uncertainty/);
});
