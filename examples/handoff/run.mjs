import {readFile,writeFile,mkdir} from "node:fs/promises";
import {join,resolve,basename} from "node:path";
import assert from "node:assert/strict";
import {randomUUID} from "node:crypto";
import {LeaseSession,fetchImmutable} from "../../clients/typescript/dist/index.js";
import {Reader} from "../read-consumer/src/wire.mjs";
import {Consumer} from "../read-consumer/src/consumer.mjs";
import {setup,readJSON,save,ts,go,contract,grants,getRecord,hash,inputNames} from "./lib.mjs";
const [stage,dirArg,artifactArg]=process.argv.slice(2);
const dir=resolve(dirArg ?? "runtime/handoff");
const context=stage==="init"?await setup(dir,{bridgePort:Number(process.env.HANDOFF_BRIDGE_PORT??18781),resourcePort:Number(process.env.HANDOFF_RESOURCE_PORT??18782)}):await readJSON(join(dir,"context.json"));
const stateFile=join(dir,"state.json");
let state;try{state=await readJSON(stateFile);}catch(e){if(e.code!=="ENOENT")throw e;state={};}
const task=async(client,id)=>(await client.call("task.get",{task_id:id})).data;
const resultData=async(client,method,params)=>(await client.call(method,params)).data;
const report=async(name,value)=>{await save(join(dir,name+".json"),value);console.log(JSON.stringify({stage:name,...value}));};
if(stage==="init") console.log(JSON.stringify({stage,dir,bridge:context.url,resources:context.resourceURL}));
else if(stage==="a-delegate") {
  if(state.parent)throw Error("Stage already executed; use a new runtime for a new scenario");
  const created=await go(context,"O","task.create",{contract:contract(context,"A","Prepare a release assessment of the checkout retry proposal; delegate an independent review."),initial_grants:grants(context,"A")});
  state.parent=created.task.task_id;
  const published=await go(context,"O","task.publish",{task_id:state.parent,expected_revision:created.task.revision});
  const claimGrant=created.records.find(r=>r.record_type==="Grant"&&r.record.agent_id==="agent_A").record.grant_id;
  const claim=await go(context,"A","task.claim",{task_id:state.parent,expected_revision:published.task.revision,claim_grant_id:claimGrant,lease_seconds:300});
  const assignment=getRecord(claim,"Assignment");
  state.parentLease={assignment_id:assignment.assignment_id,generation:assignment.generation,token:claim.lease_token};
  const started=await go(context,"A","run.start",{task_id:state.parent,expected_revision:claim.task.revision,lease:state.parentLease});state.parentRun=getRecord(started,"Run").run_id;
  const child=await go(context,"A","task.createChild",{parent_task_id:state.parent,contract:contract(context,"B","Independently review candidate v1 of the checkout retry policy against observations and acceptance criteria."),initial_grants:grants(context,"B")});
  assert.equal(child.task.state,"draft");assert.equal(getRecord(child,"Relation").parent_task_id,state.parent);
  state.child=child.task.task_id;state.relation=getRecord(child,"Relation").relation_id;
  const draftGrant=child.records.find(r=>r.record_type==="Grant"&&r.record.agent_id==="agent_B").record.grant_id;
  await assert.rejects(ts(context,"B").claim({task_id:state.child,expected_revision:child.task.revision,claim_grant_id:draftGrant,lease_seconds:300}),e=>e.kind==="INVALID_STATE");
  await go(context,"A","task.publish",{task_id:state.child,expected_revision:child.task.revision});
  await save(stateFile,state);await report("a-delegated",{parent:state.parent,parent_run:state.parentRun,child:state.child,relation:state.relation,client:"independent Go client"});
} else if(stage==="authorization-check") {
  const owner=ts(context,"O"),unprivileged=ts(context,"B");
  const parent=await resultData(owner,"task.create",{contract:contract(context,"B","Independent authorization fixture; parent has never been claimed"),initial_grants:grants(context,"B")});
  const child=await resultData(owner,"task.createChild",{parent_task_id:parent.task.task_id,contract:contract(context,"B","Owner may create child without parent lease"),initial_grants:grants(context,"B")});
  assert.equal(parent.task.current_assignment_id,null);assert.equal(getRecord(child,"Relation").parent_task_id,parent.task.task_id);
  await assert.rejects(unprivileged.call("task.createChild",{parent_task_id:parent.task.task_id,contract:contract(context,"B","Unauthorized relation is insufficient"),initial_grants:grants(context,"B")}),error=>error.kind==="FORBIDDEN");
  await report("authorization-checked",{parent:parent.task.task_id,child:child.task.task_id,parent_assignment:null,owner_create_without_lease:"pass",reader_only_agent_create:"FORBIDDEN"});
} else if(stage==="b-open") {
  if(state.childClaim)throw Error("B already claimed; do not recreate execution");
  const client=ts(context,"B");
  const discovered=await resultData(client,"task.discover",{limit:100});
  const target=discovered.tasks.find(t=>t.task_id===state.child);assert.ok(target,"Published C must be pull discoverable");
  const opened=await resultData(client,"snapshot.open",{scope:{}});let cursor=opened.page_cursor,claimGrant;
  while(true){const page=await resultData(client,"snapshot.page",{snapshot_id:opened.snapshot_id,page_cursor:cursor,limit:100});for(const item of page.records){if(item.record_type==="Grant"&&item.record.task_id===target.task_id&&item.record.agent_id==="agent_B"&&item.record.actions.includes("task.claim"))claimGrant=item.record.grant_id;}if(page.complete)break;cursor=page.next_page_cursor;}
  assert.ok(claimGrant,"Discover own explicit claim grant through public snapshot");
  const claim=await client.claim({task_id:target.task_id,expected_revision:target.revision,claim_grant_id:claimGrant,lease_seconds:300});
  const lease=LeaseSession.fromClaim(client,claim);const start=await lease.start();
  state.childClaim=claim;state.childRun=getRecord(start.data,"Run").run_id;
  const work=join(dir,"b-public-work");await mkdir(work,{mode:0o700});
  const accesses=[];
  for(const ref of [...target.contract.input_refs,...target.contract.acceptance_refs]) {const bytes=await fetchImmutable(ref);const name=basename(new URL(ref.uri).pathname);await writeFile(join(work,name),bytes,{mode:0o600});accesses.push({uri:ref.uri,sha256:hash(bytes),bytes:bytes.length});}
  await save(join(work,"task.json"),target);
  await lease.report(state.childRun,"Fetched and verified all public immutable inputs; preparing independent review",[...target.contract.input_refs,...target.contract.acceptance_refs]);
  await save(stateFile,state);await report("b-opened",{task_id:target.task_id,run_id:state.childRun,work_directory:work,resource_reads:accesses,client:"independent TypeScript client"});
} else if(stage==="supervise") {
  console.log("Supervising A and active B leases; stop after scenario verification");
  let stopped=false;for(const signal of ["SIGTERM","SIGINT"])process.on(signal,()=>{stopped=true;});
  while(!stopped){
    const current=await readJSON(stateFile);
    const p=await go(context,"A","task.get",{task_id:current.parent});
    if(p.state!=="active")throw Error("Parent is no longer active; H01 parent-lease invariant failed");
    await go(context,"A","assignment.renew",{task_id:current.parent,expected_revision:p.revision,lease:current.parentLease,lease_seconds:300});
    if(current.childClaim){const b=ts(context,"B"),c=await task(b,current.child);if(["claimed","active"].includes(c.state)){try{await LeaseSession.fromClaim(b,current.childClaim).renew(300);}catch(e){if(e.kind!=="STALE_REVISION")throw e;}}}
    await new Promise(r=>setTimeout(r,10000));
  }
} else if(stage==="b-submit") {
  if(!artifactArg)throw Error("Supply B-produced JSON artifact path");
  const client=ts(context,"B"),lease=LeaseSession.fromClaim(client,state.childClaim);
  await lease.resume(state.childRun); // fence before touching the delivered candidate
  const bytes=await readFile(resolve(artifactArg));JSON.parse(bytes.toString("utf8"));
  const name="review-"+hash(bytes)+".json";await writeFile(join(dir,"public",name),bytes,{mode:0o600,flag:"wx"});
  const ref={kind:"immutable",uri:context.resourceURL+"/"+name,media_type:"application/json",version:"1",sha256:hash(bytes)};
  await fetchImmutable(ref);
  const submitted=await lease.submit(state.childRun,{content_version:state.childClaim.data.task.content_version,summary:"Independent evidence-backed checkout retry review",artifact_refs:[ref],evidence_refs:context.refs,extensions:{}});
  state.submission=submitted.data.task.pending_submission_id;state.artifact=ref;await save(stateFile,state);
  await report("b-submitted",{task_id:state.child,submission_id:state.submission,artifact:ref,run_id:state.childRun});
} else if(stage==="x-inspect") {
  const client=ts(context,"X"),c=await task(client,state.child);
  assert.equal(c.state,"submitted");
  const sub=await resultData(client,"record.get",{task_id:state.child,record_type:"Submission",record_id:c.pending_submission_id});
  const artifact=sub.record.artifact_refs[0],bytes=await fetchImmutable(artifact),review=JSON.parse(Buffer.from(bytes));
  const inputs={};for(const ref of [...c.contract.input_refs,...c.contract.acceptance_refs]){const data=await fetchImmutable(ref);inputs[basename(new URL(ref.uri).pathname)]={text:Buffer.from(data).toString(),sha256:hash(data)};}
  const lines=inputs["observations-v1.csv"].text.trim().split("\n"),headers=lines.shift().split(","),totals={};
  for(const line of lines){const cells=line.split(","),group=cells[0].split("_")[0];totals[group]??={};for(let i=1;i<headers.length;i++)totals[group][headers[i]]=(totals[group][headers[i]]??0)+Number(cells[i]);}
  for(const total of Object.values(totals))total.error_basis_points=Math.round(10000*total.http_5xx/total.attempts);
  assert.deepEqual(review.totals,totals,"X independently recalculates metrics");
  assert.equal(review.candidate_within_budget,totals.candidate.http_5xx*100<totals.candidate.attempts);
  assert.deepEqual(review.input_sha256,Object.fromEntries(Object.entries(inputs).map(([name,input])=>[name,input.sha256])));
  assert.ok(["hold","proceed"].includes(review.recommendation));assert.ok(review.findings.length>=3);for(const finding of review.findings)for(const key of ["claim","evidence","implication"])assert.ok(typeof finding[key]==="string"&&finding[key].length>10);
  assert.ok(review.next_validation !== null && ["string","object"].includes(typeof review.next_validation) && JSON.stringify(review.next_validation).length>20);
  const inspection={task_id:state.child,submission_id:c.pending_submission_id,artifact,resource_reads:Object.entries(inputs).map(([name,x])=>({name,sha256:x.sha256})),recomputed_totals:totals,quantitative_checks:"pass",narrative_review:"pending independent X reviewer",review};
  await report("x-inspection",inspection);
} else if(stage==="x-decide") {
  if(!artifactArg)throw Error("Supply independent X reviewer decision JSON");
  const decision=await readJSON(resolve(artifactArg)),inspection=await readJSON(join(dir,"x-inspection.json"));
  assert.ok(["accepted","rejected"].includes(decision.outcome));assert.ok(typeof decision.reason==="string"&&decision.reason.length>=40);
  assert.equal(decision.artifact_sha256,inspection.artifact.sha256);
  const client=ts(context,"X"),c=await task(client,state.child);assert.equal(c.pending_submission_id,inspection.submission_id);
  await fetchImmutable(inspection.artifact); // X fetches exact submitted candidate before deciding
  const result=await resultData(client,decision.outcome==="accepted"?"task.accept":"task.reject",{task_id:c.task_id,expected_revision:c.revision,submission_id:c.pending_submission_id,reason:decision.reason,evidence_refs:[inspection.artifact]});
  state.decision=getRecord(result,"Decision").decision_id;await save(stateFile,state);await report("x-decided",{task_id:state.child,submission_id:inspection.submission_id,decision_id:state.decision,outcome:decision.outcome,reason:decision.reason});
} else if(stage==="r-sync"||stage==="verify") {
  const reader=new Reader({url:context.url,token:context.tokens.R});const consumer=new Consumer({reader,file:join(dir,"r-view.json")});const previous=await consumer.load();await consumer.sync();
  if(stage==="r-sync")await report("r-synced",{record_count:Object.keys((await consumer.load()).records).length});
  else {
    const view=await consumer.load(),records=Object.values(view.records),find=(type,id)=>records.find(r=>r.record_type===type&&Object.values(r.record).includes(id))?.record;
    const p=find("Task",state.parent),c=find("Task",state.child);assert.equal(p.state,"active");assert.equal(c.state,"accepted");assert.equal(find("Run",state.parentRun).state,"running");assert.equal(find("Run",state.childRun).state,"submitted");assert.equal(find("Assignment",state.childClaim.data.records.find(x=>x.record_type==="Assignment").record.assignment_id).state,"completed");assert.equal(find("Decision",state.decision).submission_id,state.submission);assert.equal(find("Relation",state.relation).parent_task_id,state.parent);assert.ok(find("Submission",state.submission));
    // Deliberately replay the previous persisted cursor with the new dedup set.
    // This models an at-least-once repeated delivery without touching bridge state.
    if(previous?.valid && previous.identity===view.identity){
      const repeated={...view,cursor:previous.cursor};await consumer.save(repeated);
      await new Consumer({reader:new Reader({url:context.url,token:context.tokens.R}),file:join(dir,"r-view.json")}).sync();
      const again=Object.values((await consumer.load()).records).filter(r=>r.record.task_id===state.child);
      assert.deepEqual(again,records.filter(r=>r.record.task_id===state.child));
    }
    const history=await consumer.audit();await save(join(dir,"r-audit.json"),history);const events=history.batches.flatMap(b=>b.events);assert.equal(events.filter(e=>e.task_id===state.child&&e.type==="submission.created").length,1);assert.equal(events.filter(e=>e.task_id===state.child&&e.type==="decision.created").length,1);
    const access=(await readFile(join(dir,"resource-access.jsonl"),"utf8")).trim().split("\n").map(JSON.parse);assert.ok(access.every(a=>!a.bridge_credential_received));
    await report("verified",{status:"pass",protocol:"bridge-m1-1",parent:state.parent,child:state.child,submission:state.submission,decision:state.decision,record_count:records.length,history_batches:history.batches.length,repeated_cursor_deduplicated:Boolean(previous?.valid),resource_gets:access.length,parent_state:p.state,child_state:c.state});
  }
} else throw Error("Use init, a-delegate, supervise, b-open, b-submit FILE, x-inspect, x-decide FILE, r-sync, verify");
