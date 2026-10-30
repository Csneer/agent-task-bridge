#!/usr/bin/env bash
# Verify checksums, execute host binaries and install the SDK outside the checkout.
set -euo pipefail
cd "$(dirname "$0")/.."
output=$(cd "${1:-dist/release}" && pwd)
version=${2:-v0.1.0}
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'Release execution smoke requires Linux amd64' >&2; exit 1; }
(cd "$output" && sha256sum --check SHA256SUMS)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
tar -xzf "$output/agent-task-bridge_${version}_linux_amd64.tar.gz" -C "$scratch"
bin="$scratch/agent-task-bridge_${version}_linux_amd64/bin"
for target in bridge bridge-client; do
  "$bin/$target" --version | grep -F "$version"
  "$bin/$target" --help
done
node --input-type=module - "$scratch/agent-task-bridge_${version}_linux_amd64" "$scratch/private" <<'JS'
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';
import path from 'node:path';
const [release, dir] = process.argv.slice(2);
const binary = path.join(release, 'bin/bridge');
execFileSync(binary, ['init', '--dir', dir], { cwd: release, stdio: 'pipe' });
const daemon = spawn(binary, ['--db', path.join(dir, 'bridge.sqlite'), '--key', path.join(dir, 'bridge.key'),
  '--listen', '127.0.0.1:0', '--log-format', 'json'], { cwd: release, stdio: ['ignore', 'ignore', 'pipe'] });
let logs = '';
daemon.stderr.setEncoding('utf8').on('data', chunk => { logs += chunk; });
const ended = new Promise(resolve => {
  daemon.on('error', error => resolve({ error }));
  daemon.on('exit', (code, signal) => resolve({ code, signal }));
});
try {
  let address;
  for (let i = 0; i < 300; i++) {
    for (const line of logs.split('\n')) {
      try { const entry = JSON.parse(line); if (entry.msg === 'bridge ready') address = entry.address; } catch {}
    }
    if (address) break;
    if (daemon.exitCode !== null || daemon.signalCode !== null) throw Error('extracted daemon exited before readiness');
    await delay(50);
  }
  assert.ok(address, 'extracted daemon readiness timeout');
  const url = `http://${address}`;
  assert.equal((await fetch(`${url}/ready`)).status, 204);
  const result = JSON.parse(execFileSync(process.execPath, [path.join(release, 'examples/minimal/run.mjs'),
    '--dir', dir, '--url', url], { cwd: release, encoding: 'utf8', timeout: 30000 }));
  assert.equal(result.state, 'accepted');
  assert.equal(result.history_actions.length, 6);
  console.log('PASS: extracted release init → daemon → real artifact → accept → event history');
} finally {
  daemon.kill('SIGTERM');
  const killTimer = setTimeout(() => daemon.kill('SIGKILL'), 12000);
  const result = await ended;
  clearTimeout(killTimer);
  assert.equal(result.code, 0, 'extracted daemon must shut down cleanly');
}
JS
mkdir "$scratch/consumer"
cd "$scratch/consumer"
printf '{"private":true,"type":"module"}\n' > package.json
npm install --ignore-scripts --no-audit --no-fund "$output/csneer-agent-task-bridge-${version#v}.tgz"
node --input-type=module <<'JS'
import assert from 'node:assert/strict';
import { BridgeClient, ProtocolValidator, METHODS, canonical } from '@csneer/agent-task-bridge';
assert.equal(typeof BridgeClient, 'function');
assert.equal(METHODS.size, 27);
new ProtocolValidator().validate('Params_task_get', {authority_id: 'authority_smoke', task_id: 'task_smoke'});
assert.equal(canonical({b: 2, a: 1}), '{"a":1,"b":2}');
JS
./node_modules/.bin/bridge-client --version | grep -F "${version#v}"
./node_modules/.bin/bridge-client --help
printf 'PASS: Linux amd64 extracted binaries and independently installed npm tarball. arm64 was cross-compiled only.\n'
