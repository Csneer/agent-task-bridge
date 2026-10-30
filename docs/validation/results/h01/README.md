# H01 observed acceptance — 2026-10-30

Result: **PASS** for the recorded local run on protocol `bridge-m1-1`.
The Go A client, TypeScript B client, and independent Node read consumer accessed
the daemon through HTTP. A fresh B Agent received only the public work package,
performed the review, and produced review.json. An independent X Agent inspected
the public inputs and actual artifact and accepted the review. Its recommendation
to hold product rollout is distinct from accepting this review deliverable.

The evidence includes original immutable inputs, B artifact, reviewer decision,
public stable IDs, resource GET/digest records, public snapshot objects, and
retained audit batches. No bearer, lease token, private controller state, or
bridge database is included. manifest.json binds the exported evidence bytes.

- verified.json: final public projection assertions and duplicate-cursor replay.
- provenance.json: client sources, normal restart, parent Assignment continuity,
  and the limits of these observations.
- public-records.json / public-audit.json: parent/child facts and full relevant
  audit history at verification time.
- b-opened.json / b-submitted.json / x-inspection.json / x-decided.json:
  actual input and output retrieval, computed metrics, submission and acceptance.
- resource-access.json: resource bytes/digests and absence of bridge credentials.
- authorization-checked.json: child creation with no parent lease; denial for a
  reader/claimant lacking independent Owner/Issuer capability.

A host process-session interruption occurred between Agent turns. The daemon
was normally restarted with the same database/key, without reimport. Parent P
still had its original active Assignment; a fenced renewal using that original
lease succeeded. Audit contains one parent claim, no parent expiry, and no
replacement Assignment. This is evidence of continuity for this particular run,
not an assumption that local background sessions always survive turn boundaries.

R first synchronized before submission, then restored through a new consumer
instance, replayed a previous cursor with event_id deduplication, and enumerated
history separately. Final C is accepted; its Assignment is completed, Run is
submitted, and its unique Decision references its unique Submission. P remained
active at verification and is not automatically completed by child acceptance.

Reproduce with the staged commands in examples/handoff/README.md, using a new
private runtime and fresh independent B/X reviews. The separate automated real
HTTP test uses a clearly labelled transport artifact and does not substitute for
the authored work represented here.
