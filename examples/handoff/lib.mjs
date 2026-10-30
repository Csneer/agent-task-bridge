import {readFile,writeFile,mkdir,rename} from "node:fs/promises";
import {createHash,randomBytes,randomUUID} from "node:crypto";
import {join,resolve} from "node:path";
import {spawn} from "node:child_process";
import {BridgeClient,FileJournal} from "../../clients/typescript/dist/index.js";
export const root = resolve(new URL("../..",import.meta.url).pathname);
export const inputNames=["proposal-v1.md","observations-v1.csv","acceptance-v1.md"];
export const hash = bytes => createHash("sha256").update(bytes).digest("hex");
export const readJSON = async file => JSON.parse(await readFile(file,"utf8"));
export async function save(file,data) {
  const temp=file+"."+randomUUID()+".tmp";
  await writeFile(temp,JSON.stringify(data,null,2)+"\n",{mode:0o600,flag:"wx"});await rename(temp,file);
}
export async function setup(dir,{bridgePort=18781,resourcePort=18782}={}) {
  dir=resolve(dir);await mkdir(dir,{recursive:true,mode:0o700});
  await mkdir(join(dir,"public"),{mode:0o700});
  const now=Date.now(),start=new Date(now-60000).toISOString(),end=new Date(now+86400000).toISOString();
  const tokens=Object.fromEntries(["O","A","B","X","R"].map(x=>[x,randomBytes(32).toString("base64url")]));
  const principals=Object.keys(tokens).map(x=>({principal_id:"principal_"+x,enabled:true}));
  const cfg={authority_id:"authority_handoff",principals,agents:["A","B"].map(x=>({agent_id:"agent_"+x,enabled:true})),
    credentials:Object.entries(tokens).map(([x,token])=>({credential_id:"credential_"+x,principal_id:"principal_"+x,token,not_before:start,expires_at:end,revoked_at:null})),
    bindings:["A","B"].map(x=>({binding_id:"binding_"+x,principal_id:"principal_"+x,agent_id:"agent_"+x,not_before:start,expires_at:end,revoked_at:null})),
    owner_capabilities:[{id:"capability_A",owner_principal_id:"principal_O",subject_principal_id:"principal_A",actions:["task.create","task.link","grant.issue"],all_tasks:true,task_ids:[],parent_task_ids:[],grant_actions:["task.read","task.claim","task.publish","task.link","submission.decide"],grant_subjects:principals.map(p=>p.principal_id),eligible_agent_ids:["agent_A","agent_B"],acceptance_principal_ids:["principal_X"],max_grant_expires_at:end,not_before:start,expires_at:end,revoked_at:null}]};
  await writeFile(join(dir,"config.json"),JSON.stringify(cfg),{mode:0o600,flag:"wx"});
  const context={dir,url:`http://127.0.0.1:${bridgePort}/rpc`,resourceURL:`http://127.0.0.1:${resourcePort}`,tokens,start,end,refs:[]};
  for(const name of inputNames) {
    const bytes=await readFile(new URL("./inputs/"+name,import.meta.url));
    await writeFile(join(dir,"public",name),bytes,{mode:0o600});
    context.refs.push({kind:"immutable",uri:context.resourceURL+"/"+name,media_type:name.endsWith("csv")?"text/csv":"text/markdown",version:"1",sha256:hash(bytes)});
  }
  await save(join(dir,"context.json"),context);
  return context;
}
export function ts(context,who) {return new BridgeClient({url:context.url,token:context.tokens[who],agentId:["A","B"].includes(who)?"agent_"+who:undefined,journal:new FileJournal(join(context.dir,"journal-"+who))});}
export function contract(context,agent,goal) {return {profile:"delivery-acceptance-v1",owner_principal_id:"principal_O",goal,constraints:["Review only; no payment-system mutation", "Use only public task and immutable input references"],input_refs:context.refs.slice(0,2),acceptance_refs:context.refs.slice(2),context_refs:[],expected_output:"Evidence-backed JSON review complying with acceptance-v1.md",eligible_agent_ids:["agent_"+agent],acceptance_principal_id:"principal_X",recovery_policy:"safe_reopen",allow_rework:true,lease_policy:{min_seconds:5,max_seconds:300},extensions:{"org.example.handoff":{scenario:"H01",role:agent}}};}
export function grants(context,agent) {
  const grant=(who,agentID,actions)=>({subject_principal_id:"principal_"+who,agent_id:agentID,actions,not_before:context.start,expires_at:context.end});
  return [grant(agent,"agent_"+agent,["task.read","task.claim"]),grant("A",null,["task.read","task.publish","task.link"]),grant("X",null,["task.read","submission.decide"]),grant("R",null,["task.read"])];
}
export async function go(context,who,method,params={}) {
  const out=join(context.dir,"rpc-"+randomUUID()+".json");
  const executable=process.env.HANDOFF_GO_CLIENT ?? join(root,"runtime/bin/bridge-client");
  await new Promise((resolve,reject)=>{
    const child=spawn(executable,["call",method,"--out",out],{cwd:root,env:{...process.env,BRIDGE_URL:new URL(context.url).origin,BRIDGE_TOKEN:context.tokens[who],BRIDGE_AGENT:["A","B"].includes(who)?"agent_"+who:"",BRIDGE_JOURNAL:join(context.dir,"go-journal-"+who)},stdio:["pipe","ignore","pipe"]});
    let error="";child.stderr.on("data",x=>error+=x);child.on("error",reject);child.on("exit",code=>code===0?resolve():reject(Error("Go call "+method+" failed: "+error)));child.stdin.end(JSON.stringify(params));
  });
  return (await readJSON(out)).result.data;
}
export const getRecord=(result,type)=>result.records.find(r=>r.record_type===type)?.record;
