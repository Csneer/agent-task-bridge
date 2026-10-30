import {parse} from "lossless-json";
import {readFileSync} from "node:fs";
import {randomUUID} from "node:crypto";
import Ajv from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

// The parser receives decimal source text before any IEEE-754 conversion.
function integer(raw) {
  const [, sign, whole, fraction = "", exp = "0"] = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(raw);
  const digits = (whole + fraction).replace(/^0+/, "");
  if (!digits) return 0;
  const scale = BigInt(exp) - BigInt(fraction.length);
  if (scale > 16n || scale < -BigInt(digits.length)) throw Error("Unsafe number");
  let value;
  if (scale < 0n) {
    const count = Number(-scale);
    if (!/^0+$/.test(digits.slice(-count))) throw Error("Fractional number");
    value = digits.slice(0, -count);
  } else value = digits + "0".repeat(Number(scale));
  if (value.length > 16) throw Error("Unsafe number");
  const n = Number(sign + value);
  if (!Number.isSafeInteger(n)) throw Error("Unsafe number");
  return n;
}
export function decode(bytes) {
  const value = parse(typeof bytes === "string" ? bytes : new TextDecoder("utf-8", {fatal:true}).decode(bytes), undefined, {parseNumber:integer});
  function visit(v) {
    if (typeof v === "string" && !v.isWellFormed()) throw Error("Invalid Unicode");
    if (v && typeof v === "object") for (const [k,x] of Object.entries(v)) { visit(k); visit(x); }
  }
  visit(value);
  return value;
}
const schema = JSON.parse(readFileSync(new URL("../../../protocol/m1/schema.json", import.meta.url)));
const methods = JSON.parse(readFileSync(new URL("../../../protocol/m1/methods.json", import.meta.url))).methods;
const ajv = new Ajv({strict:false});
addFormats(ajv);
ajv.addSchema(schema, "bridge");
const validators = new Map();
function validate(ref, value) {
  if (!validators.has(ref)) validators.set(ref, ajv.compile({$ref:"bridge" + ref}));
  if (!validators.get(ref)(value)) throw Error("Invalid protocol shape: " + ref);
}
export class ReadError extends Error {
  constructor(kind) { super(kind); this.kind = kind; }
}
const allowed = new Set(["bridge.capabilities", "snapshot.open", "snapshot.page", "events.open", "events.read"]);
export class Reader {
  constructor({url,token,agent}) {
    this.url = new URL(url);
    if (this.url.protocol !== "https:" && !(this.url.protocol === "http:" && ["localhost","127.0.0.1","[::1]"].includes(this.url.hostname))) throw Error("HTTPS or loopback HTTP required");
    if (this.url.username || this.url.password) throw Error("URL credentials forbidden");
    this.token = token; this.agent = agent;
  }
  async connect() {
    const caps = await this.call("bridge.capabilities", {});
    if (caps.protocol_version !== "bridge-m1-1") throw Error("Unsupported protocol");
    for (const name of allowed) if (!caps.methods.includes(name)) throw Error("Missing read method");
    this.authority = caps.authority_id;
    return this.authority;
  }
  async call(method, fields) {
    if (!allowed.has(method)) throw Error("Read method required");
    const params = method === "bridge.capabilities" ? fields : {...fields,authority_id:this.authority,...(this.agent ? {as_agent_id:this.agent}: {})};
    const def = methods.find(m => m.method === method);
    validate(def.params, params);
    const id = randomUUID();
    const response = await fetch(this.url,{method:"POST",redirect:"error",signal:AbortSignal.timeout(30000),headers:{"content-type":"application/json",authorization:"Bearer " + this.token},body:JSON.stringify({jsonrpc:"2.0",id,method,params})});
    if (response.status === 401 || response.status === 403) throw new ReadError("UNAUTHORIZED");
    if (!response.ok) throw Error("HTTP " + response.status);
    const result = decode(new Uint8Array(await response.arrayBuffer()));
    if (result.jsonrpc !== "2.0" || result.id !== id) throw Error("Response correlation mismatch");
    if (result.error) { validate("#/$defs/ErrorResponse",result); throw new ReadError(result.error.data.kind); }
    if (Object.keys(result).sort().join() !== "id,jsonrpc,result" || Object.keys(result.result ?? {}).sort().join() !== "data,meta" || Object.keys(result.result?.meta ?? {}).sort().join() !== "authority_id,replayed" || typeof result.result?.meta?.replayed !== "boolean") throw Error("Invalid result envelope");
    if (!result.result?.meta || result.result.meta.authority_id !== (this.authority ?? result.result.data?.authority_id)) throw Error("Authority mismatch");
    validate(def.result_data,result.result.data);
    return result.result.data;
  }
}
