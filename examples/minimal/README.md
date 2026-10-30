# Minimal local HTTP walkthrough

This example creates a task, publishes it, discovers and claims it as a preregistered Agent, starts a Run, computes a real SHA-256 digest and UTF-8 byte count, submits a JSON artifact, independently checks it as Owner, accepts it, and reads the six committed actions as Reader. Each execution creates a fresh Task. It uses only the public HTTP protocol; it does not access SQLite or daemon internals.

Requirements: Linux, an installed `bridge` daemon, and Node.js 22 or newer. No npm install, TypeScript build, Go client, H01 fixture, resource server, Python, p-drive, or external service is needed. You can copy this directory outside the repository and run it with the installed daemon. This native Node HTTP script is a walkthrough, not an independent SDK interoperability certification.

## Run

Choose a **new** directory under an existing private parent. Do not initialize into a Git checkout. In terminal one:

```sh
bridge version
bridge init --dir "$HOME/bridge-demo"
bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key"
```

In terminal two, from this example directory:

```sh
node run.mjs --dir "$HOME/bridge-demo"
```

The final JSON contains a new `task_id`, `state: "accepted"`, the calculated digest and byte count, and the six history actions. It never prints bearer credentials, lease tokens, raw command receipts, or the private bootstrap configuration. Run the same command again to create another task. A JSON artifact named `result-TASK_ID.json` is retained in the private directory. The artifact uses an immutable `file:` reference, so this demo intentionally runs Agent and Owner on the same machine with access to that directory; the Owner rereads its exact bytes and verifies its SHA-256 before acceptance.

To choose a different local port:

```sh
bridge --db "$HOME/bridge-demo/bridge.sqlite" --key "$HOME/bridge-demo/bridge.key" --listen 127.0.0.1:8081
node run.mjs --dir "$HOME/bridge-demo" --url http://127.0.0.1:8081
```

The script only accepts numeric loopback HTTP origins and rejects redirects. It computes a fixed string from the published contract; it does not run commands from tasks or fetch arbitrary remote resources. Stop the daemon with Ctrl-C. Restart with the same database/key command, without reimporting configuration.

## Private initialization and expiry

`bridge init` atomically installs a new 0700 directory with Linux `renameat2(RENAME_NOREPLACE)`. Existing directories (including empty ones), files and symlinks are never overwritten. The parent must already exist. Initialization uses a private sibling staging directory; failure leaves its explicit path for inspection and manual removal, without installing a partial target or recursively deleting user files. Concurrent initializers cannot both install the same target.

The directory contains:

- `bridge.sqlite`: initialized authority and preregistered Owner, Agent Principal, Agent binding, and Reader Principal. It is ready for normal daemon startup.
- `bridge.key`: 32 random bytes used for encrypted receipts and cursors; preserve it with the database.
- `admin.json`: complete administrative configuration with only SHA-256 `token_hash` values.
- `credentials.json`: separate random bearer tokens indexed by `owner`, `agent`, and `reader`. Keep this file private; distribute only the relevant credential to each real participant.
- `bootstrap.json`: authority/identity IDs, validity timestamps, and relative configuration/credential filenames; no bearer values.

All generated files are 0600. Each authority ID, bearer token and encryption key is generated using cryptographic randomness. Reader gets no blanket read permission: the example explicitly grants it `task.read` on each new task. Agent receives an explicit task grant and is the only eligible Agent. Owner is the acceptance Principal.

Credentials and binding expire **30 days** after initialization by default. `bridge init --dir NEW_DIR --valid-for 24h` chooses another lifetime, greater than zero and at most `8760h`. The init output states the absolute expiry; the example refuses to begin with less than one minute remaining.

Before expiry, stop the daemon and prepare a complete updated administrative configuration. Create new credential IDs with fresh random tokens/hashes, and a new binding ID with new validity timestamps; do not mutate the identity or lifetime of existing instances. Import with `bridge --db DIR/bridge.sqlite --key DIR/bridge.key --config UPDATED_ADMIN_JSON --import-only`, then restart normally. Update the private credential/bootstrap files used by this walkthrough to match. Omitted credentials/bindings are permanently revoked by the full declarative import. Never reimport an old bootstrap to restore revoked access. Initialization itself is not a rotation command.

## Limits and verification

This short walkthrough holds requests and lease state in memory and does not implement a durable recovery journal or automatic retries. If interrupted or a response is lost, the task may have committed; inspect public state with a full client before recovery. A later invocation creates a **different** task and is not recovery of the previous intent. Existing unfinished tasks remain in the authority; their lease can expire. Production integrations should use the repository clients and their recovery rules.

From a source checkout, `go test ./cmd/bridge` verifies private permissions, token separation, rejection of existing targets, concurrent initialization, version/help, normal startup without reimport, and two complete runs over real loopback HTTP. The HTTP walkthrough test requires Node; it explicitly skips that portion if Node is absent. `go test -race ./cmd/bridge` adds Go race checking. These checks supplement, and do not replace, the independent-client and H01 acceptance evidence.
