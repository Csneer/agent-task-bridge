#!/usr/bin/env node
import {Reader} from "./wire.mjs";
import {Consumer} from "./consumer.mjs";
const [command="sync"] = process.argv.slice(2);
try {
  if(!["sync","once","audit"].includes(command)) throw Error("Use sync, once, or audit");
  if(!process.env.BRIDGE_URL || !process.env.BRIDGE_TOKEN || !process.env.BRIDGE_VIEW) throw Error("Set BRIDGE_URL, BRIDGE_TOKEN, and BRIDGE_VIEW");
  const reader = new Reader({url:process.env.BRIDGE_URL,token:process.env.BRIDGE_TOKEN,agent:process.env.BRIDGE_AGENT});
  const scope = process.env.BRIDGE_OWNER ? {owner_principal_id:process.env.BRIDGE_OWNER} : {};
  const consumer = new Consumer({reader,file:process.env.BRIDGE_VIEW,scope});
  console.log(JSON.stringify(await consumer[command]()));
} catch(error) { console.error(error.kind ?? "Consumer operation failed: " + error.message); process.exitCode = 1; }
