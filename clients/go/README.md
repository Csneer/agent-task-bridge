# Independent Go M1 client

This client implements `bridge-m1-1` using the public frozen JSON Schema only.
It imports no daemon packages. Parser, canonicalizer, HTTP and retry journal are
independent implementations. The embedded schema is byte-for-byte frozen JSON;
fixture tests detect drift.

Build from the repository root:

```sh
go build -o /tmp/bridge-client ./clients/go/cmd/bridge-client
export BRIDGE_URL=http://127.0.0.1:8080
export BRIDGE_TOKEN=YOUR_PRIVATE_BEARER
export BRIDGE_AGENT=YOUR_AGENT_ID
export BRIDGE_JOURNAL=/private/path/agent-a-journal
mkdir -m 700 "$BRIDGE_JOURNAL"
echo '{}' | /tmp/bridge-client call bridge.capabilities --out /private/path/capabilities.json
echo '{"limit":10}' | /tmp/bridge-client call task.discover --out /private/path/discovery.json
/tmp/bridge-client call task.claim --out /private/path/claim.json < /private/path/claim-params.json
/tmp/bridge-client retry ORIGINAL_UUID --out /private/path/recovered.json
```

Omit BRIDGE_AGENT for Principal-only calls. `call METHOD --out FILE` consumes
JSON params on stdin. Capabilities discovers authority before ordinary calls;
explicitly different authority fails. The client fills absent authority/Agent
and write UUID fields, but never guesses business fields, revisions or leases.
Explicit as_agent_id is preserved. Output is the original JSON-RPC response
(success data at `.result.data`). Nothing is printed to stdout. Stderr contains
only key/status. The new output file is created atomically with mode 0600;
existing files are never overwritten. Errors exit nonzero; defined RPC errors
are also saved in the private output file.

For H01 A can call task.claim, run.start, task.createChild, task.publish,
run.report and run.submit with frozen method params. Obtain the lease token
from the private claim result, and maintain the parent lease while B works.
`ReadImmutable` fetches fixed inputs/artifacts and verifies SHA-256 before
returning bytes. It does not forward bridge credentials. Explicit local file,
loopback HTTP and HTTPS resources are supported. Resource fetching is not a
sandbox: supply only operator-approved references.

## Durable recovery and privacy

Use a separate private journal for each authority and authenticated Principal.
Do not share it across identities; same-Principal credential rotation may retain
it. Lease tokens are journal secrets, including in backups; bearer tokens are
never journaled. Local hostile administrators and storage rollback are outside
this reference client. The filesystem must support atomic hard links and fsync.
Group/other access to the journal directory is rejected.

Before sending, the client fsyncs an immutable `<key>.request.json` and directory.
Atomic no-overwrite linking handles concurrent same-key initialization. A changed
intent under an existing key is rejected locally. Each send durably creates an
`<key>.unknown` marker first. Timeout, malformed response, 503 and internal errors
preserve it. **A later refusal never removes unknown history or permits changed
parameters.** Receipt absence cannot prove an in-flight copy will not commit.
This client conservatively keeps even synchronously refused intents immutable.
New intent selection is a caller decision, never automatic retry behavior.

`retry` sends the exact original request: RPC ID, key, revision and lease.
Validated success is saved privately as `<key>.success.json` and resolves the
intent; the unknown marker remains historical evidence. Retry still contacts
the bridge for current authorization instead of serving cached sensitive output.
There is no automatic compensation, journal GC, or external exactly-once claim.

## Library API and lease boundary

- `New`, `Discover`, `Call`, `Retry`: schema-checked HTTP. Concurrent Discover/Call/Retry synchronize authority discovery. Keep exported
  configuration fields fixed while operations run; do not copy a used Client.
  Endpoint accepts either a base URL or an existing /rpc path.
- `Parse`, `Canonical`, `RequestHash`, `Validate`: exact integer JSON, duplicate
  keys/Unicode checks, UTF-16-key JCS ordering and schema. Canonical accepts parsed
  JSON values, int/int64; unsupported types fail. Parse bytes before float conversion.
- `RenewLease`: reads revision, issues fenced renewal, returns the confirmed
  deadline. CAS failures and unknown results are not hidden.
- `SuperviseLease(ctx, interval, margin, fence, work)`: initial successful fence,
  periodic renewal, cancellation on error or last confirmed deadline, even during
  stalled renewal. Fence returns the deadline from a current successful command.
  Set interval and clock/network margin inside the actual remaining lease.
- `Supervise`: simpler periodic fencing for callers enforcing deadlines separately.
- `ReadImmutable(ctx, ref, maxBytes)`: bounded retrieval and digest validation.
- `PrivateWrite`: private atomic no-overwrite output.

Callbacks must honor cancellation. Supervision cannot terminate arbitrary code
or fence external side effects. A historical claim token never alone authorizes
resuming work: first complete a current run.start, run.report or strictly extending
assignment.renew. Task pointers alone do not prove current authority. Local clocks
must satisfy M1 managed-clock assumptions.

## Checks

```sh
go test -race ./clients/go/...
go vet ./clients/go/...
go build ./clients/go/...
```

Responses are bounded at 101 MiB, allowing 100 legal 1 MiB commit batches
plus envelope overhead. A public-object HTTP test verifies a page over 40 MiB.

Tests run all 92 schema, 7 hash and 9 parser fixtures plus exact-number/JCS
counterexamples, restart retry, refusal after unknown, private files, resource
digest and cancellation during stalled renewal. Actual daemon interoperability
and H01 are separate integration evidence.
