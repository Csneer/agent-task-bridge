import test from "node:test";
import assert from "node:assert/strict";
import {mkdtemp,rm,readFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {createServer} from "node:net";
import {spawn} from "node:child_process";
import {randomUUID} from "node:crypto";
import {setup,ts,contract,grants,getRecord,root} from "../lib.mjs";
import {LeaseSession,fetchImmutable} from "../../../clients/typescript/dist/index.js";
import {Reader} from "../../read-consumer/src/wire.mjs";
import {Consumer} from "../../read-consumer/src/consumer.mjs";
async function port(){const s=createServer();await new Promise(r=>s.listen(0,"127.0.0.1",r));const n=s.address().port;await new Promise(r=>s.close(r));return n;}
async function stop(child){if(child.exitCode!==null)return;child.kill("SIGTERM");await new Promise(r=>child.once("exit",r));}
test("real daemon: independent TS lifecycle, immutable HTTP input, reader snapshot race, retry and restart",async t=>{
  const dir=await mkdtemp(join(tmpdir(),"bridge-independent-"));
  const bridgePort=await port(),resourcePort=await port();const c=await setup(join(dir,"case"),{bridgePort,resourcePort});
  const daemon=spawn(join(root,"runtime/bin/bridge"),["--db",join(c.dir,"bridge.sqlite"),"--key",join(c.dir,"bridge.key"),"--init-key","--config",join(c.dir,"config.json"),"--listen",`127.0.0.1:${bridgePort}`],{stdio:["ignore","ignore","pipe"]});
  let daemonLog="";daemon.stderr.on("data",b=>daemonLog+=b);
  const resources=spawn(process.execPath,[join(root,"examples/handoff/resources.mjs"),c.dir],{stdio:"ignore"});
  t.after(async()=>{await stop(resources);await stop(daemon);await rm(dir,{recursive:true,force:true});});
  for(let i=0;i<100;i++){try{await ts(c,"O").connect();break;}catch(e){if(daemon.exitCode!==null)throw Error(daemonLog);if(i===99)throw e;await new Promise(r=>setTimeout(r,25));}}
  const owner=ts(c,"O"),b=ts(c,"B"),x=ts(c,"X");
  const created=(await owner.call("task.create",{contract:contract(c,"B","Integration fixture; not an agent-authored H01 review"),initial_grants:grants(c,"B")})).data;
  const taskID=created.task.task_id,grant=created.records.find(r=>r.record_type==="Grant"&&r.record.agent_id==="agent_B").record.grant_id;
  await assert.rejects(b.claim({task_id:taskID,expected_revision:created.task.revision,claim_grant_id:grant,lease_seconds:300}),e=>e.kind==="INVALID_STATE");
  const pub=(await owner.call("task.publish",{task_id:taskID,expected_revision:created.task.revision})).data;
  const discover=(await b.call("task.discover",{limit:100})).data;assert.ok(discover.tasks.some(t=>t.task_id===taskID));
  for(const ref of [...pub.task.contract.input_refs,...pub.task.contract.acceptance_refs])assert.ok((await fetchImmutable(ref)).length>0);
  const claim=await b.claim({task_id:taskID,expected_revision:pub.task.revision,claim_grant_id:grant,lease_seconds:300});
  const lease=LeaseSession.fromClaim(b,claim),started=await lease.start(),run=getRecord(started.data,"Run").run_id;
  await lease.report(run,"Fetched actual immutable HTTP inputs for integration validation",c.refs);
  const reader=new Reader({url:c.url,token:c.tokens.R}),file=join(c.dir,"view.json");
  const original=reader.call.bind(reader);let submitted;
  reader.call=async(method,params)=>{
    const result=await original(method,params);
    if(method==="snapshot.page"&&!submitted){submitted=await lease.submit(run,{content_version:1,summary:"Transport fixture result; not H01 authored work",artifact_refs:[c.refs[0]],evidence_refs:c.refs,extensions:{}});}
    return result;
  };
  const consumer=new Consumer({reader,file});await consumer.sync();
  let view=await consumer.load();assert.equal(Object.values(view.records).find(r=>r.record_type==="Task"&&r.record.task_id===taskID).record.state,"submitted");
  assert.ok(Object.values(view.records).some(r=>r.record_type==="Submission"));
  const subID=submitted.data.task.pending_submission_id;
  const submission=(await x.call("record.get",{task_id:taskID,record_type:"Submission",record_id:subID})).data;
  await fetchImmutable(submission.record.artifact_refs[0]);
  const accepted=await x.call("task.accept",{task_id:taskID,expected_revision:submitted.data.task.revision,submission_id:subID,reason:"Fetched immutable transport fixture; this is an integration check, not the H01 review acceptance",evidence_refs:submission.record.artifact_refs});
  const resumed=new Consumer({reader:new Reader({url:c.url,token:c.tokens.R}),file});await resumed.sync();view=await resumed.load();assert.equal(Object.values(view.records).find(r=>r.record_type==="Task").record.state,"accepted");
  assert.equal(Object.values(view.records).filter(r=>r.record_type==="Decision").length,1);
  const audit=await resumed.audit();assert.equal(audit.batches.flatMap(b=>b.events).filter(e=>e.type==="submission.created").length,1);
  const staleParams={task_id:taskID,expected_revision:accepted.data.task.revision,lease:{assignment_id:lease.identity.assignmentId,generation:lease.identity.generation,token:lease.identity.token},run_id:run,message:"obsolete executor",evidence_refs:[],extensions:{}};
  await assert.rejects(b.call("run.report",staleParams),e=>e.kind==="LEASE_FENCED");
  // Exact persisted write retry through a new TS process-object yields original result.
  const key=randomUUID();const current=(await owner.call("task.get",{task_id:taskID})).data;
  const request=await owner.prepare("task.close",{task_id:taskID,expected_revision:current.revision,idempotency_key:key,reason:"integration complete"});
  const first=await owner.execute(request),retry=await ts(c,"O").retry(key);assert.equal(retry.meta.replayed,true);assert.deepEqual(retry.data,first.data);
  await resumed.sync();assert.equal(Object.values((await resumed.load()).records).find(r=>r.record_type==="Task").record.state,"closed");
  const log=(await readFile(join(c.dir,"resource-access.jsonl"),"utf8")).trim().split("\n").map(JSON.parse);assert.ok(log.length>=4);assert.ok(log.every(e=>!e.bridge_credential_received));
});
