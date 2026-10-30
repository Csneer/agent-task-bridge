import {createServer} from "node:http";
import {readFile,appendFile} from "node:fs/promises";
import {join,resolve} from "node:path";
import {readJSON,hash} from "./lib.mjs";
const dir=resolve(process.argv[2]);const context=await readJSON(join(dir,"context.json"));
const server=createServer(async(req,res)=>{
  const name=new URL(req.url,"http://localhost").pathname.slice(1);
  if(req.method!=="GET" || !/^[a-z0-9][a-z0-9._-]*$/.test(name)){res.writeHead(404).end();return;}
  try {
    const bytes=await readFile(join(dir,"public",name));
    await appendFile(join(dir,"resource-access.jsonl"),JSON.stringify({at:new Date().toISOString(),name,sha256:hash(bytes),bytes:bytes.length,bridge_credential_received:Boolean(req.headers.authorization)})+"\n",{mode:0o600});
    res.setHeader("content-type",name.endsWith("json")?"application/json":"text/plain");res.end(bytes);
  } catch {res.writeHead(404).end();}
});
server.listen(Number(new URL(context.resourceURL).port),"127.0.0.1",()=>console.log("immutable resource fixture ready"));
for(const signal of ["SIGTERM","SIGINT"])process.on(signal,()=>server.close());
