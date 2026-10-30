// A dependency-free HTTP walkthrough, not an SDK or interoperability certification.
import {open, stat, writeFile, readFile} from "node:fs/promises";
import {constants} from "node:fs";
import {resolve, join} from "node:path";
import {pathToFileURL} from "node:url";
import {createHash, randomUUID} from "node:crypto";
import assert from "node:assert/strict";

async function privateJSON(path) {
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const info = await file.stat();
    if (!info.isFile() || (info.mode & 0o077) !== 0 || info.uid !== process.getuid())
      throw Error("bootstrap and credentials must be owner-only regular files");
    return JSON.parse(await file.readFile("utf8"));
  } finally { await file.close(); }
}

async function main() {
  const args = process.argv.slice(2);
  if (args.length !== 2 && args.length !== 4 || args[0] !== "--dir" || args.length === 4 && args[2] !== "--url")
    throw Error("usage: node run.mjs --dir PRIVATE_DIR [--url http://127.0.0.1:8080]");
  const dir = resolve(args[1]);
  const mode = await stat(dir);
  if (!mode.isDirectory() || mode.mode & 0o077 || mode.uid !== process.getuid()) throw Error("directory must be private (0700)");
  const url = new URL(args[3] ?? "http://127.0.0.1:8080");
  if (url.protocol !== "http:" || !["127.0.0.1", "[::1]"].includes(url.hostname) || url.username || url.password || url.pathname !== "/" || url.search || url.hash)
    throw Error("this local example requires an HTTP numeric loopback origin");
  const info = await privateJSON(join(dir, "bootstrap.json"));
  const tokens = await privateJSON(join(dir, "credentials.json"));
  if (Date.parse(info.expires_at) <= Date.now() + 60000) throw Error("credentials expire soon; rotate before running the example");
  const rpc = async (role, method, params = {}, write = false) => {
    const id = randomUUID();
    const request = {jsonrpc:"2.0", id, method, params:{authority_id:info.authority_id, ...params}};
    if (role === "agent") request.params.as_agent_id = info.agent_id;
    if (write) request.params.idempotency_key = randomUUID();
    // Deliberately no automatic retry: never print bearer or lease-bearing requests.
    const response = await fetch(new URL("/rpc",url), {method:"POST", headers:{"Content-Type":"application/json",Authorization:`Bearer ${tokens[role]}`},body:JSON.stringify(request),signal:AbortSignal.timeout(10000),redirect:"error"});
    if (!response.ok) throw Error(`${method}: HTTP ${response.status}`);
    const result = await response.json();
    if (result.error) throw Error(`${method}: ${result.error.data?.kind ?? result.error.code}`);
    assert.equal(result.id,id);
    assert.equal(result.result.meta.authority_id,info.authority_id);
    return result.result.data;
  };
  const text = "Agent Task Bridge: a small, reproducible task.\n";
  const contract = {profile:"delivery-acceptance-v1",owner_principal_id:info.owner_principal_id,goal:"Compute the SHA-256 digest and UTF-8 byte count of the exact string in extensions",constraints:["Compute locally; do not execute commands or fetch resources"],input_refs:[],expected_output:"Submission extensions org.example.minimal with sha256 and bytes, independently checked by Owner",acceptance_refs:[],context_refs:[],eligible_agent_ids:[info.agent_id],acceptance_principal_id:info.owner_principal_id,recovery_policy:"safe_reopen",allow_rework:true,lease_policy:{min_seconds:5,max_seconds:300},extensions:{"org.example.minimal":{text}}};
  const grant = (principal, agent, actions) => ({subject_principal_id:principal,agent_id:agent,actions,not_before:info.not_before,expires_at:info.expires_at});
  const created = await rpc("owner","task.create",{contract,initial_grants:[grant(info.agent_principal_id,info.agent_id,["task.read","task.claim"]),grant(info.reader_principal_id,null,["task.read"])]},true);
  const taskID = created.task.task_id;
  let current = await rpc("owner","task.publish",{task_id:taskID,expected_revision:created.task.revision},true);
  let after; let discovered;
  do {
    const page = await rpc("agent","task.discover",{limit:100,...(after ? {after_task_id:after} : {})});
    discovered = page.tasks.find(task => task.task_id === taskID);
    after = page.next_after_task_id;
  } while (!discovered && after);
  assert.ok(discovered,"published task must be discoverable");
  // The Owner supplies the grant ID from its public creation result.
  const claimGrant = created.records.find(r => r.record_type === "Grant" && r.record.agent_id === info.agent_id).record.grant_id;
  current = await rpc("agent","task.claim",{task_id:taskID,expected_revision:discovered.revision,claim_grant_id:claimGrant,lease_seconds:300},true);
  const assignment = current.records.find(r => r.record_type === "Assignment").record;
  const lease = {assignment_id:assignment.assignment_id,generation:assignment.generation,token:current.lease_token};
  current = await rpc("agent","run.start",{task_id:taskID,expected_revision:current.task.revision,lease},true);
  const runID = current.records.find(r => r.record_type === "Run").record.run_id;
  const bytes = Buffer.from(discovered.contract.extensions["org.example.minimal"].text,"utf8");
  const output = {sha256:createHash("sha256").update(bytes).digest("hex"),bytes:bytes.length};
  const artifactPath = join(dir, `result-${taskID}.json`);
  const artifactBytes = Buffer.from(JSON.stringify(output) + "\n");
  await writeFile(artifactPath, artifactBytes, {mode:0o600,flag:"wx"});
  const artifact = {kind:"immutable",uri:pathToFileURL(artifactPath).href,media_type:"application/json",version:"1",sha256:createHash("sha256").update(artifactBytes).digest("hex")};
  current = await rpc("agent","run.submit",{task_id:taskID,expected_revision:current.task.revision,lease,run_id:runID,content_version:current.task.content_version,summary:"Computed the string digest and byte count",artifact_refs:[artifact],evidence_refs:[],extensions:{"org.example.minimal":output}},true);
  const submissionID = current.task.pending_submission_id;
  const submission = await rpc("owner","record.get",{task_id:taskID,record_type:"Submission",record_id:submissionID});
  assert.equal(submission.record.artifact_refs[0].uri, artifact.uri);
  const delivered = await readFile(artifactPath);
  assert.equal(createHash("sha256").update(delivered).digest("hex"),submission.record.artifact_refs[0].sha256);
  assert.deepEqual(JSON.parse(delivered), submission.record.extensions["org.example.minimal"]);
  const ownerTask = await rpc("owner","task.get",{task_id:taskID});
  const ownerInput = ownerTask.contract.extensions["org.example.minimal"].text;
  assert.deepEqual(submission.record.extensions["org.example.minimal"],{sha256:createHash("sha256").update(ownerInput,"utf8").digest("hex"),bytes:Buffer.byteLength(ownerInput,"utf8")});
  current = await rpc("owner","task.accept",{task_id:taskID,expected_revision:ownerTask.revision,submission_id:submissionID,reason:"Owner independently recomputed the SHA-256 digest and UTF-8 byte count",evidence_refs:[]},true);
  assert.equal(current.task.state,"accepted");
  const opened = await rpc("reader","events.open",{scope:{owner_principal_id:info.owner_principal_id}});
  let cursor = opened.cursor, atHead = false; const actions = [];
  while (!atHead) {
    const page = await rpc("reader","events.read",{cursor,limit:100});
    actions.push(...page.batches.filter(batch => batch.events.some(event => event.task_id === taskID)).map(batch => batch.audit.action));
    cursor = page.next_cursor; atHead = page.at_head;
  }
  assert.deepEqual(actions,["task.create","task.publish","task.claim","run.start","run.submit","task.accept"]);
  console.log(JSON.stringify({task_id:taskID,state:current.task.state,output,history_actions:actions}));
}
main().catch(error => { console.error(`Minimal example failed: ${error.message}`); process.exitCode = 1; });
