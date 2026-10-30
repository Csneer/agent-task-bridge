#!/usr/bin/env python3
"""Package redacted public runtime evidence; never reads credentials or a DB."""
import gzip
import hashlib
import json
from pathlib import Path
import re
import shutil
import sys

source, target = map(Path, sys.argv[1:3])
target.mkdir(parents=True, exist_ok=False)
revision = (source / 'implementation-commit.txt').read_text().strip()
events = [json.loads(line) for line in (source / 'go-test.jsonl').read_text().splitlines()]
failures = [e for e in events if e['Action'] == 'fail']
if failures:
    raise SystemExit('Refusing to export a failing runtime suite')
passed = [e for e in events if e['Action'] == 'pass' and 'Test' in e]
summaries = []
for number in range(1, 26):
    vector = f'V{number:02}'
    tests = [e for e in passed if e['Test'].startswith('Test' + vector)]
    if not tests:
        raise SystemExit(f'Missing passing runtime vector: {vector}')
    logs = sorted((source / 'vectors').glob(f'Test{vector}*.jsonl'))
    if not logs:
        raise SystemExit(f'Missing public evidence: {vector}')
    raw = b''.join(p.read_bytes() for p in logs)
    for line in raw.splitlines():
        record = json.loads(line)
        if record['implementation_commit'] != revision:
            raise SystemExit('Source revision changed within evidence')
        # Every sensitive protocol key must already have been redacted by the
        # observer. Fail export instead of attempting post-hoc secret removal.
        def check(v):
            if isinstance(v, dict):
                for key, value in v.items():
                    if key.lower() in {'token', 'lease_token', 'authorization', 'token_hash'} and value != '[redacted]':
                        raise SystemExit('Unredacted protocol secret in evidence')
                    check(value)
            elif isinstance(v, list):
                for value in v:
                    check(value)
        check(record)
    filename = vector + '.jsonl.gz'
    (target / filename).write_bytes(gzip.compress(raw, mtime=0))
    summaries.append({'vector': vector, 'status': 'pass', 'tests': [e['Test'] for e in tests],
                      'evidence': filename, 'entries': len(raw.splitlines())})
for name in ('go-test.jsonl', 'go-vet.txt', 'go-build.txt', 'typescript-typecheck.txt',
             'typescript-test.txt', 'consumer-check.txt', 'consumer-test.txt',
             'real-http-integration.txt', 'frozen-package.txt', 'diff-check.txt',
             'implementation-commit.txt', 'worktree-status.txt'):
    shutil.copyfile(source / name, target / name)
summary = {'implementation_commit': revision, 'protocol_version': 'bridge-m1-1',
           'protocol_checksum': '558dc29110b0138c31a36b0f19dbb9f34677d23d5508e5c9751afc4c7cd20a13',
           'status': 'pass', 'go_test_cases_including_subtests': len(passed), 'vectors': summaries,
           'evidence_format': 'gzip-compressed UTF-8 JSONL; request/response and scheduling records; generated fixture secrets removed before writing'}
(target / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
manifest = [{'file': p.name, 'bytes': p.stat().st_size,
             'sha256': hashlib.sha256(p.read_bytes()).hexdigest()} for p in sorted(target.iterdir())]
(target / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print(f'PASS: 25 vectors exported from {revision}; {len(passed)} Go test cases including subtests')
