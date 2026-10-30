# Agent↔Task Bridge TypeScript SDK

This client implements the public `bridge-m1-1` HTTP/JSON-RPC binding. It does not import daemon code or inspect its database. Node.js 22+ is required. Build copies the frozen public Schema and method catalog into `dist/protocol`, so the built client does not depend on the caller's working directory.

```sh
npm ci
npm run typecheck
npm test
```

The tests run all 92 structural fixtures, 7 request hashes and 9 exact JSON parser fixtures, plus local HTTP fault/recovery tests. They do not establish daemon conformance or a real H01 handoff.

## Install the developer preview

Version 0.1.0 is a developer preview for the frozen `bridge-m1-1` protocol. Download
`csneer-agent-task-bridge-0.1.0.tgz` from the
[GitHub release](https://github.com/Csneer/agent-task-bridge/releases), then install:

```sh
gh release download v0.1.0 --repo Csneer/agent-task-bridge --pattern 'csneer-agent-task-bridge-0.1.0.tgz'
npm install ./csneer-agent-task-bridge-0.1.0.tgz
npx bridge-client --version
npx bridge-client --help
```

The tarball contains the compiled ESM library, TypeScript declarations, CLI,
frozen Schema/method catalog and MIT license. Installation needs no source checkout
or build tools. Registry publication is separate from GitHub release availability.

To build from source, use the complete repository checkout: `protocol/m1` is a
required build input, and packing also requires the root `LICENSE`. A detached
`clients/typescript` source copy is not a standalone build distribution; the build
reports this requirement explicitly. From this directory:

```sh
npm ci
npm run typecheck
npm test
npm run test:package
npm pack
```

`npm pack` builds and verifies the frozen resource digests before producing the
tarball. `test:package` packs, installs into a temporary directory outside the
checkout, checks the exact package contents and resource digests, imports the
library, runs CLI help/version, and compiles a TypeScript consumer. It removes its
temporary installation afterwards and does not publish anything.

## Library

```ts
import { BridgeClient, FileJournal, LeaseSession, fetchImmutable } from '@csneer/agent-task-bridge';

const bridge = new BridgeClient({
  url: process.env.BRIDGE_URL!,
  token: process.env.BRIDGE_TOKEN!,
  agentId: 'agent_B',
  journal: new FileJournal('/private/client-B/recovery'),
});
await bridge.connect(); // exact version and authority verification
const found = await bridge.call('task.discover', { limit: 20 });
```

`call(method, params)` supports all 27 methods and validates request, envelope, and method-specific result. It adds the connected authority and optional Agent context. Write calls preserve a generated UUIDv4 key when one is absent. `prepare` exposes the key before transmission; `execute` sends that immutable request; `retry(key)` restores it from the private journal.

The default library client keeps recovery records in memory; pass a `FileJournal` for process-restart recovery. Journal files are mode 0600, the directory mode 0700, and writes use file/directory synchronization. The original request is immutable and published without overwrite; each send has an immutable uncertainty marker. Only a validated success creates a confirmation marker. Concurrent client instances cannot replace the same key with different parameters or clear another instance’s uncertain outcome by receiving a rejection. Durable uncertainty is deliberately conservative: after restart even a rejected send requires original-key recovery unless success was confirmed. Markers are retained with the original request. Prototype journals from the older mutable-file layout are rejected rather than silently dropping their uncertainty; retain those files for explicit original-request recovery. They contain original execution credentials when needed, but never the bridge bearer. Use a distinct journal per authenticated Principal and authority; do not reuse one under another identity. Keep the directory outside Git and on a private filesystem. Credential rotation for the same Principal requires intentionally retaining its journal.

An unknown result remains unknown after a later rejection. No method automatically changes a preserved key or its parameters. `requestState(key)` reports uncertainty and attempts. A temporarily absent receipt cannot prove that no request is still running. Resolving a successful historical claim does not authorize external execution; use a current fenced operation first.

`LeaseSession.fromClaim(bridge, result)` or `new LeaseSession(bridge, identity)` supplies serialized `start`, `renew`, `report`, and `submit`. Each fresh mutation rereads the current Task revision. Unknown requests block subsequent mutations until `retryPending()` succeeds. With a FileJournal, any unsuccessful send remains conservatively unknown in memory and on disk, including a definite rejection received by this instance; another instance may still have committed the same request. Without a journal, definite CAS conflicts are returned for caller handling. `resume(runId)` rereads current IDs and executes `run.start` or a `run.report` before work resumes. `supervise` periodically renews and independently watches the last confirmed Assignment expiry. Its default `safetyMarginMs` is 1000; it stops and calls `onError` before that local deadline even if renewal is blocked. A late success cannot restart that supervisor. Start supervision from a claim result or after a confirmed fenced operation that supplies an Assignment; missing/stale expiry fails closed. Choose a margin for local/server clock skew and scheduling delay. A frozen process or blocked event loop cannot deliver callbacks; the caller must revalidate with `resume` after such a pause. Supervision also stops on the first renewal failure, delivering it to `onError`; the caller must stop starting new external work when supervision fails. No client action proves external process exclusion.

`fetchImmutable(ref)` reads HTTP(S) resources, verifies the exact SHA-256 bytes, and never forwards the bridge bearer. Use its separate `fetch` option if a resource needs its own authorization. It rejects redirects and unsupported URI schemes explicitly. Resource access and produced work are separate from bridge commands.

`parseExact` rejects duplicate keys, invalid UTF-8 or Unicode, and noninteger/unsafe mathematical numeric values before conversion to JS numbers. Use it for raw data rather than `JSON.parse`; rounding already performed by a caller cannot be reversed. `canonical` implements the protocol's safe-integer JCS subset and preserves namespaced extension values. Local parser limits are 32 MiB and depth 128 by default; requests are capped at the profile's 256 KiB limit.

## CLI

```sh
export BRIDGE_URL=http://127.0.0.1:8080
export BRIDGE_TOKEN='<private credential>'
export BRIDGE_AGENT=agent_B
export BRIDGE_JOURNAL=/private/client-B/recovery

npx bridge-client call task.discover <<'JSON'
{"limit":20}
JSON
```

The CLI takes JSON parameters on stdin for `call METHOD`, `claim`, `start`, `report`, and `submit`. The aliases map exactly to the public RPC names; they do not generate work or acceptance decisions. `retry KEY` uses the original preserved request. For every write, stderr first prints the idempotency key, allowing recovery after a lost response.

Stdout always redacts `token` and `lease_token`. Use `--out NEW_FILE` for the private full response, written exclusively at mode 0600; existing files are not overwritten. Subsequent requests can take the Assignment ID/generation/token from that protected response. No credential appears in ordinary error messages. Avoid shell tracing when providing credentials.

```sh
npx bridge-client claim --out /private/client-B/claim.json < claim-params.json
npx bridge-client retry '<original UUIDv4 key>' --out /private/client-B/recovered.json
npx bridge-client fetch-ref --out /private/client-B/input.txt < immutable-reference.json
```

Without `BRIDGE_JOURNAL`, the CLI uses a private state directory under XDG_STATE_HOME (or `~/.local/state`) keyed by endpoint and a credential fingerprint. Switching credentials changes that default directory. Runtime credentials, private results and actual resources belong outside the source tree; no test fixture stands in for an Agent's actual H01 work.
