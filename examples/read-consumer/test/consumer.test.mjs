import test from "node:test";
import assert from "node:assert/strict";
import {createServer} from "node:http";
import {readFile,mkdtemp,rm,stat} from "node:fs/promises";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {Reader,decode} from "../src/wire.mjs";
import {Consumer} from "../src/consumer.mjs";
const schema = JSON.parse(await readFile(new URL("../../../protocol/m1/schema.json",import.meta.url)));
const vectors = JSON.parse(await readFile(new URL("../../../protocol/m1/golden-vectors.json",import.meta.url)));
const methods = JSON.parse(await readFile(new URL("../../../protocol/m1/methods.json",import.meta.url))).methods.map(x=>x.method);
const task = vectors.schema_cases.find(x=>x.id === "G01_Task").value;
const record = {record_type:"Task",record:task};
const batch = revision => ({commit_id:"commit_"+revision,committed_at:task.updated_at,actor:{kind:"authority",principal_id:null,agent_id:null,authorization_ids:[]},audit:{action:"task.publish",request_id:null,reason:null},events:[{event_id:"event_"+revision,type:"task.updated",task_id:task.task_id,task_revision:revision,record_type:"Task",record:{...task,revision}}]});
async function fixture(t, custom=async()=>undefined) {
  const dir = await mkdtemp(join(tmpdir(),"bridge-reader-"));
  const calls = [];
  const server = createServer(async(req,res)=>{
    const chunks=[]; for await(const c of req) chunks.push(c);
    const input=JSON.parse(Buffer.concat(chunks)); calls.push(input);
    try {
      let data = await custom(input,res);
      if(res.destroyed || res.writableEnded) return;
      if(data === undefined) {
        switch(input.method) {
          case "bridge.capabilities": data={authority_id:"authority_demo",protocol_version:"bridge-m1-1",methods,limits:Object.fromEntries(Object.entries(schema.$defs.Limits.properties).map(([k,v])=>[k,v.const]))}; break;
          case "snapshot.open": data={snapshot_id:"snapshot_1",watermark:"W:opaque-opaque-padding",replay_cursor:"cursor/W+1=-opaque-padding",page_cursor:"page/z=-opaque-padding",expires_at:"2099-01-01T00:00:00.000Z",auth_valid_until:null}; break;
          case "snapshot.page": data={records:[record],next_page_cursor:null,complete:true}; break;
          case "events.open": data={cursor:"audit/origin=-opaque-padding",auth_valid_until:null}; break;
          case "events.read": data={batches:[],next_cursor:"cursor/head=-opaque-padding",at_head:true}; break;
        }
      }
      res.setHeader("content-type","application/json");
      res.end(JSON.stringify(data.error ? {jsonrpc:"2.0",id:input.id,error:data.error} : {jsonrpc:"2.0",id:input.id,result:{data,meta:{authority_id:"authority_demo",replayed:false}}}));
    } catch(e) { res.statusCode=500;res.end(); }
  });
  await new Promise(r=>server.listen(0,"127.0.0.1",r));
  t.after(async()=>{await new Promise(r=>server.close(r));await rm(dir,{recursive:true,force:true});});
  const options={reader:new Reader({url:`http://127.0.0.1:${server.address().port}/rpc`,token:"test-only"}),file:join(dir,"view.json")};
  return {consumer:new Consumer(options),options,calls};
}
test("snapshot pagination stages before atomic replacement; concurrent writes replay after W",async t=>{
  let f;
  f=await fixture(t,async input=>{
    if(input.method === "snapshot.page") {
      assert.equal((await f.consumer.load()).valid,false);
      if(input.params.page_cursor === "page/z=-opaque-padding") return {records:[record],next_page_cursor:"page/a=-opaque-padding",complete:false};
      assert.equal(input.params.page_cursor,"page/a=-opaque-padding");
      return {records:[],next_page_cursor:null,complete:true};
    }
    if(input.method === "events.read") {assert.equal(input.params.cursor,"cursor/W+1=-opaque-padding");return {batches:[batch(3)],next_cursor:"opaque/no-order-opaque-padding",at_head:true};}
  });
  await f.consumer.sync();
  const state=await f.consumer.load();
  assert.equal(Object.values(state.records)[0].record.revision,3);
  assert.equal(state.cursor,"opaque/no-order-opaque-padding");
  assert.equal((await stat(f.options.file)).mode & 0o777,0o600);
});
test("failure before persistence preserves complete projection/checkpoint; restart retries",async t=>{
  const f=await fixture(t,input=> input.method === "events.read" ? {batches:[batch(3),batch(4)],next_cursor:"next-opaque-padding",at_head:true}:undefined);
  f.consumer.beforeCommit=async state=>{if(state.cursor === "next-opaque-padding") throw Error("simulated crash before rename");};
  await assert.rejects(f.consumer.once(),/simulated crash/);
  const old=await f.consumer.load(); assert.equal(old.cursor,"cursor/W+1=-opaque-padding");assert.equal(Object.values(old.records)[0].record.revision,2);
  const resumed=new Consumer(f.options);await resumed.sync();
  assert.equal(Object.values((await resumed.load()).records)[0].record.revision,4);
});
test("duplicate delivery does not regress projection",async t=>{
  let n=0;
  const f=await fixture(t,input=>input.method === "events.read" ? {batches:++n===1?[batch(3),batch(4)]:[batch(3)],next_cursor:"head-opaque-padding"+n,at_head:true}:undefined);
  await f.consumer.sync(); await f.consumer.sync();
  const state=await f.consumer.load();assert.equal(Object.values(state.records)[0].record.revision,4);assert.equal(state.seen.length,2);
});
test("revoked authorization invalidates persistently and replaces view",async t=>{
  let revoked=false, rebuild=false;
  const f=await fixture(t,input=>{
    if(input.method === "events.read" && revoked && !rebuild) {rebuild=true;return {error:{code:-32009,message:"changed",data:{kind:"REINITIALIZE_REQUIRED",retry:"reinitialize"}}};}
    if(input.method === "snapshot.page" && rebuild) return {records:[],next_page_cursor:null,complete:true};
  });
  await f.consumer.sync(); revoked=true;
  await assert.rejects(f.consumer.once(),e=>e.kind === "REINITIALIZE_REQUIRED");
  assert.deepEqual(await f.consumer.load(),{version:1,identity:f.consumer.identity,valid:false});
  await new Consumer(f.options).sync();assert.equal(Object.keys((await f.consumer.load()).records).length,0);
});
test("disconnect keeps checkpoint, next invocation recovers; audit leaves checkpoint alone",async t=>{
  let disconnect=false;
  const f=await fixture(t,(input,res)=>{
    if(input.method === "events.read" && disconnect) {disconnect=false;res.destroy();}
    if(input.method === "events.read" && input.params.cursor === "audit/origin=-opaque-padding") return {batches:[batch(3)],next_cursor:"audit/end-opaque-padding",at_head:true};
  });
  await f.consumer.sync();const before=await readFile(f.options.file,"utf8");
  disconnect=true;await assert.rejects(f.consumer.once());assert.equal(await readFile(f.options.file,"utf8"),before);
  await new Consumer(f.options).sync();const current=await readFile(f.options.file,"utf8");
  const audit=await f.consumer.audit();assert.equal(audit.batches.length,1);assert.equal(await readFile(f.options.file,"utf8"),current);
});
test("401 removes authorized view",async t=>{
  let revoked=false;
  const f=await fixture(t,(input,res)=>{if(revoked){res.statusCode=401;res.end();}});
  await f.consumer.sync();revoked=true;await assert.rejects(f.consumer.once(),e=>e.kind === "UNAUTHORIZED");assert.equal((await f.consumer.load()).valid,false);
});
for(const raw of ["1.0000000000000001","9007199254740990.5","1e-400","9007199254740992", "{\"x\":1,\"x\":2}","\"\\ud800\""]){test("exact parser rejects " + raw,()=>assert.throws(()=>decode(raw)));}
test("exact parser preserves valid integral decimals and rejects malformed UTF8",()=>{assert.equal(decode("12.00e1"),120);assert.equal(decode("9007199254740991"),Number.MAX_SAFE_INTEGER);assert.throws(()=>decode(Uint8Array.of(0xff)));});
test("sync automatically rebuilds after epoch invalidation",async t=>{
  let invalid=false, rebuilt=false;
  const f=await fixture(t,input=>{
    if(input.method === "events.read" && invalid && !rebuilt) {rebuilt=true;return {error:{code:-32009,message:"changed",data:{kind:"REINITIALIZE_REQUIRED",retry:"reinitialize"}}};}
    if(input.method === "snapshot.page" && rebuilt) return {records:[],next_page_cursor:null,complete:true};
  });
  await f.consumer.sync(); invalid=true;await f.consumer.sync();
  assert.equal((await f.consumer.load()).valid,true);
  assert.equal(Object.keys((await f.consumer.load()).records).length,0);
});
test("expired known horizon invalidates before contacting network",async t=>{
  const f=await fixture(t);await f.consumer.sync();
  const state=await f.consumer.load();state.auth_valid_until="2000-01-01T00:00:00.000Z";await f.consumer.save(state);
  const callCount=f.calls.length;
  await assert.rejects(f.consumer.once(),e=>e.kind === "REINITIALIZE_REQUIRED");
  assert.equal(f.calls.length,callCount);assert.equal((await f.consumer.load()).valid,false);
});
