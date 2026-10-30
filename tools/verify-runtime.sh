#!/usr/bin/env bash
# Execute the full local M1 verification and retain reproducible raw evidence.
set -euo pipefail
cd "$(dirname "$0")/.."
bridge_go=${BRIDGE_GO:-go}
bridge_python=${BRIDGE_PYTHON:-.venv/bin/python}
report_dir=${1:-runtime/validation}
mkdir -p "$report_dir" runtime/bin
report_dir=$(cd "$report_dir" && pwd)
git rev-parse HEAD > "$report_dir/implementation-commit.txt"
git status --porcelain > "$report_dir/worktree-status.txt"
export BRIDGE_TEST_REVISION
BRIDGE_TEST_REVISION=$(cat "$report_dir/implementation-commit.txt")
export BRIDGE_TEST_EVIDENCE="$report_dir/vectors"
if [ -d "$BRIDGE_TEST_EVIDENCE" ]; then
  printf '%s\n' 'Use a new evidence directory; prior vector evidence is never overwritten.' >&2
  exit 1
fi
printf '%s\n' 'Running Go tests with race detection and public protocol evidence...'
"$bridge_go" test -json -race ./... -count=1 > "$report_dir/go-test.jsonl"
"$bridge_go" vet ./... > "$report_dir/go-vet.txt" 2>&1
"$bridge_go" build ./... > "$report_dir/go-build.txt" 2>&1
"$bridge_go" build -o runtime/bin/bridge ./cmd/bridge
"$bridge_go" build -o runtime/bin/bridge-client ./clients/go/cmd/bridge-client
printf '%s\n' 'Running independent TypeScript and consumer checks...'
npm --prefix clients/typescript run typecheck > "$report_dir/typescript-typecheck.txt" 2>&1
npm --prefix clients/typescript test > "$report_dir/typescript-test.txt" 2>&1
npm --prefix examples/read-consumer run check > "$report_dir/consumer-check.txt" 2>&1
npm --prefix examples/read-consumer test > "$report_dir/consumer-test.txt" 2>&1
node --test examples/handoff/test/integration.test.mjs > "$report_dir/real-http-integration.txt" 2>&1
"$bridge_python" tools/verify-package.py > "$report_dir/frozen-package.txt" 2>&1
git diff --check > "$report_dir/diff-check.txt" 2>&1
printf '%s\n' 'PASS: runtime suite, build, static analysis, independent clients, real HTTP integration and frozen package.'
