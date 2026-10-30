import {open,readFile,mkdir,rename,unlink} from "node:fs/promises";
import {dirname,resolve} from "node:path";
import {randomUUID,createHash} from "node:crypto";
import {decode} from "./wire.mjs";

const ids = {Task:"task_id",Assignment:"assignment_id",Run:"run_id",Report:"report_id",Submission:"submission_id",Decision:"decision_id",Relation:"relation_id",Grant:"grant_id"};
function put(records, item) {
  const id = item.record[ids[item.record_type]];
  if (!id) throw Error("Record identity missing");
  const key = JSON.stringify([item.record.task_id,item.record_type,id]);
  records[key] = item;
}
export class Consumer {
  constructor({reader,file,scope={},beforeCommit=async()=>{}}) {
    this.reader = reader; this.file = resolve(file); this.scope = scope; this.beforeCommit = beforeCommit;
    this.busy = false;
    // Prevent accidental reuse of a checkpoint with a different credential or filter.
    this.identity = createHash("sha256").update(JSON.stringify([String(reader.url),reader.token,reader.agent ?? null,scope])).digest("hex");
  }
  async save(state) {
    const dir = dirname(this.file);
    await mkdir(dir,{recursive:true,mode:0o700});
    const temp = this.file + "." + randomUUID() + ".tmp";
    let handle;
    try {
      handle = await open(temp,"wx",0o600);
      await handle.writeFile(JSON.stringify(state)); await handle.sync(); await handle.close(); handle = null;
      await this.beforeCommit(state);
      await rename(temp,this.file);
      const directory = await open(dir,"r");
      try { await directory.sync(); } finally { await directory.close(); }
    } finally { await handle?.close(); await unlink(temp).catch(e => { if(e.code !== "ENOENT") throw e; }); }
  }
  async load() {
    try { return decode(await readFile(this.file)); } catch(e) { if(e.code === "ENOENT") return null; throw e; }
  }
  async invalidate() { await this.save({version:1,identity:this.identity,valid:false}); }
  async initialize() {
    const start = await this.reader.call("snapshot.open",{scope:this.scope});
    const state = {version:1,identity:this.identity,authority:this.reader.authority,valid:true,auth_valid_until:start.auth_valid_until,cursor:start.replay_cursor,watermark:start.watermark,records:{},seen:[]};
    let cursor = start.page_cursor;
    const visited = new Set();
    while (true) {
      if (visited.has(cursor)) throw Error("Repeated snapshot cursor");
      visited.add(cursor);
      const page = await this.reader.call("snapshot.page",{snapshot_id:start.snapshot_id,page_cursor:cursor,limit:100});
      for (const record of page.records) put(state.records,record);
      if (page.complete) { if(page.next_page_cursor !== null) throw Error("Invalid completed page"); break; }
      if (!page.next_page_cursor) throw Error("Missing snapshot cursor");
      cursor = page.next_page_cursor;
    }
    this.checkHorizon(state);
    await this.save(state);
    return state;
  }
  checkHorizon(state) {
    if (state.auth_valid_until !== null && Date.now() >= Date.parse(state.auth_valid_until)) {
      const error = Error("Authorization horizon reached"); error.kind = "REINITIALIZE_REQUIRED"; throw error;
    }
  }
  async once() {
    if (this.busy) throw Error("Consumer operation already running");
    this.busy = true;
    try {
      let state = await this.load();
      if (state?.valid) this.checkHorizon(state);
      await this.reader.connect();
      if (!state?.valid || state.identity !== this.identity || state.authority !== this.reader.authority) {
        await this.invalidate(); state = await this.initialize();
      }
      this.checkHorizon(state);
      const page = await this.reader.call("events.read",{cursor:state.cursor,limit:100});
      const next = structuredClone(state);
      const seen = new Set(next.seen);
      for (const batch of page.batches) {
        const duplicates = batch.events.filter(event => seen.has(event.event_id)).length;
        if (duplicates && duplicates !== batch.events.length) throw Error("Partial duplicate transaction");
        for (const event of batch.events) {
        if (seen.has(event.event_id)) continue;
        put(next.records,{record_type:event.record_type,record:event.record});
        seen.add(event.event_id);
        }
      }
      next.seen = [...seen]; next.cursor = page.next_cursor;
      this.checkHorizon(next);
      await this.save(next);
      return {at_head:page.at_head,record_count:Object.keys(next.records).length,cursor:next.cursor};
    } catch(e) {
      if (["REINITIALIZE_REQUIRED","UNAUTHORIZED","FORBIDDEN"].includes(e.kind)) await this.invalidate();
      throw e;
    } finally { this.busy = false; }
  }
  async sync({maxPages=1000}={}) {
    let rebuilds = 0;
    for (let i=0;i<maxPages;i++) {
      try { const result = await this.once(); if(result.at_head) return result; }
      catch(e) { if(e.kind !== "REINITIALIZE_REQUIRED" || ++rebuilds > 3) throw e; }
    }
    throw Error("Catch-up page budget exceeded; checkpoint preserved");
  }
  // Historical history is intentionally separate from the materialized-view checkpoint.
  async audit({maxPages=1000}={}) {
    await this.reader.connect();
    const start = await this.reader.call("events.open",{scope:this.scope});
    let cursor = start.cursor;
    const batches = [], seen = new Set();
    for(let i=0;i<maxPages;i++) {
      this.checkHorizon(start);
      const page = await this.reader.call("events.read",{cursor,limit:100});
      for(const batch of page.batches) {
        const events = batch.events.filter(e => !seen.has(e.event_id));
        if(events.length && events.length !== batch.events.length) throw Error("Partial duplicate transaction");
        if(events.length) { batches.push(batch); for(const e of events) seen.add(e.event_id); }
      }
      cursor = page.next_cursor;
      if(page.at_head) return {batches,cursor};
    }
    throw Error("Audit page budget exceeded");
  }
}
