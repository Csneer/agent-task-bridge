# Independent read consumer

Node.js 22+ read-only HTTP client for `bridge-m1-1`. Imports frozen JSON Schema only, with no Agent-client source, daemon internals, or SQLite. The independent lossless-json parser rejects unsafe/fractional raw numbers, duplicate keys, malformed UTF-8 and Unicode.

```sh
cd examples/read-consumer
npm ci
export BRIDGE_URL=http://127.0.0.1:8080/rpc
export BRIDGE_TOKEN=...              # pre-registered reader credential
export BRIDGE_VIEW=/private/reader/view.json
# Optional: BRIDGE_AGENT, BRIDGE_OWNER (owner_principal_id filter)
node src/cli.mjs sync
node src/cli.mjs once
node src/cli.mjs audit
npm run check
npm test
```

`sync` initializes and catches up to the current head, bounded by 1,000 pages and three authorization rebuild retries. `once` processes one replay page after initialization. `audit` returns retained historical commit batches through events.open/read without changing the live checkpoint. These invocations are finite; schedule sync for continued updates. Network failure exits nonzero; the next invocation resumes the committed checkpoint. An interrupted audit restarts from history origin and emits no partial output.

Snapshot pages stage in memory until complete, then atomically replace the view. Replay starts at the snapshot watermark, covering concurrent writes during pagination. Each complete event response, event_id deduplication set and opaque cursor commit together using a 0600 temporary file, file fsync, atomic rename and directory fsync. Newly created directories are 0700. Interrupted temporary files are never loaded. REINITIALIZE_REQUIRED durably clears the view before rebuilding, removing revoked records. HTTP 401/403 invalidates it. Credential, endpoint, agent or scope changes force rebuilding; only a credential fingerprint is persisted.

State stores valid, auth_valid_until, records keyed by [task_id,record_type,record_id], seen event IDs and cursor. This is a last-observed cache, never a live authorization or lease proof. Offline/stopped consumers cannot observe later revocations. External applications must not use this cache as a permanent authorization decision; display observations only after fresh sync. valid:true identifies the last successful local commit. Known authorization horizons are checked before networking and before commit.

Use one process/writer per state file. Overlapping operations on one instance are rejected; cross-process locking is outside this example. Use a private filesystem supporting atomic rename and directory fsync. Existing parent directories retain their permissions. Dedup history is retained without compaction; audit accumulates history in memory. This example requires the repository protocol files at their relative location.

Tests use a local HTTP fixture: snapshot paging with concurrent writes, crash before persistence, restart, disconnect, duplicate batches, opaque cursors, revoked-read rebuild, historical audit and exact JSON parsing. Actual daemon interoperability/H01 is a separate integration check.
