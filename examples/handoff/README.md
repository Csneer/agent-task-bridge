# H01 staged cross-client handoff

This scenario uses the independent Go client for Owner/A, the TypeScript client
for B/X, and the independent read consumer for R. It supplies a fixed candidate
retry-policy proposal, measured attempt counts, and acceptance criteria. It
contains **no prewritten B review or X decision**. An independent Agent must
produce the review from the public task/resources; another reviewer checks it.

Prepare dependencies and executables from repository root:

```sh
npm --prefix clients/typescript ci
npm --prefix clients/typescript run build
npm --prefix examples/read-consumer ci
go build -o runtime/bin/bridge ./cmd/bridge
go build -o runtime/bin/bridge-client ./clients/go/cmd/bridge-client
node --test examples/handoff/test/integration.test.mjs
node examples/handoff/run.mjs init runtime/handoff
```

`init` exclusively creates a new private runtime. It generates disposable
credentials and 24-hour bindings/grants; do not commit this directory. Default
ports are 18781/18782; set HANDOFF_BRIDGE_PORT and HANDOFF_RESOURCE_PORT before
init if necessary. HANDOFF_GO_CLIENT may point to the compiled Go executable.

Run the daemon and immutable resource fixture in separate terminals:

```sh
runtime/bin/bridge --db runtime/handoff/bridge.sqlite \
  --key runtime/handoff/bridge.key --init-key \
  --config runtime/handoff/config.json --listen 127.0.0.1:18781
node examples/handoff/resources.mjs runtime/handoff
```

Use `--init-key` only once; normal restarts omit both --init-key and --config.
The controller never reads the bridge database. Resource GETs are recorded
without credentials in resource-access.jsonl. Publication copies immutable
input and output files into this fixture, separately from bridge storage.

Run phases in order:

```sh
node examples/handoff/run.mjs a-delegate runtime/handoff
node examples/handoff/run.mjs supervise runtime/handoff  # separate terminal
node examples/handoff/run.mjs authorization-check runtime/handoff
node examples/handoff/run.mjs b-open runtime/handoff
node examples/handoff/run.mjs r-sync runtime/handoff
```

A creates/publishes P, claims/starts P, and uses explicit Owner capability to
create/publish child C. B cannot claim draft C. B then discovers C, reads its
own claim grant via public snapshots, claims/starts it and fetches all immutable
inputs over HTTP. `supervise` renews A and active B leases every ten seconds;
keep it running through final verification. It stops on errors rather than
claiming external execution remains authorized. Run only one supervisor.

**Pause here.** Give a fresh B Agent only `runtime/handoff/b-public-work/`:
`task.json` plus three public input files. Ask it to read the task and acceptance
criteria and write a real `review.json`. It must not read runtime context,
state, private credentials, daemon files, or implementation source. Keeping
this separation is the operator responsibility; this example is not a sandbox.

```sh
node examples/handoff/run.mjs b-submit runtime/handoff runtime/handoff/b-public-work/review.json
node examples/handoff/run.mjs x-inspect runtime/handoff
```

B checks current fenced authority, publishes its candidate artifact through the
external resource fixture and submits its digest reference. X reads the public
Submission, fetches the artifact and all inputs, verifies digests, recomputes
metrics and writes x-inspection.json. Quantitative checks cannot replace the
narrative review.

**Pause again.** Give an independent X reviewer the public input files and
x-inspection.json. Require it to assess the evidence, unsupported claims,
recommendation and proposed validation, then write a decision JSON:

```json
{"outcome":"accepted", "artifact_sha256":"digest of reviewed artifact", "reason":"Concrete evidence-based review explanation, at least 40 characters"}
```

The reviewer may reject; H01 positive acceptance requires an actual justified
accepted outcome. Never generate an acceptance decision merely to pass a test.

```sh
node examples/handoff/run.mjs x-decide runtime/handoff /private/x-decision.json
node examples/handoff/run.mjs verify runtime/handoff
```

R resumes its pre-submission checkpoint in a new instance, restores all stable
IDs and states, re-reads a previously processed cursor to exercise deduplication,
and independently obtains retained audit batches. Verification asserts accepted
C, completed child Assignment, submitted child Run, one Submission/Decision,
correct Relation and an active parent. Separate authorization-check proves an
Owner can create a child without any parent lease, while a read-only B with the
same parent ID cannot; the actor name differs from H01 step 7 but the missing
Owner/Issuer authority is identical.

Stop supervisor, resource server and daemon after collecting evidence. Parent
P intentionally remains active at the verification point; stopping its owner
later allows its normal lease expiration and does not cancel the child.

Private state/claim response/journals can contain lease tokens. Publish only
sanitized stage result files, public resources, inspection, audit and verified
summary after reviewing them. A failed controller command is not a durable
workflow engine: keep its private journals and recover the exact original
request where needed; do not blindly rerun a stage after an unknown response.
For a new independent trial choose a fresh runtime directory.

The automated integration test uses a transport artifact, clearly labelled as
such; it validates real daemon/TS/R HTTP lifecycle and crash/replay behavior
but **does not by itself satisfy the authored-work H01 criterion**.
