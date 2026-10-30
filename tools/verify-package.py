#!/usr/bin/env python3
"""Read-only baseline integrity and static conformance verification."""
from __future__ import annotations

import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PROTOCOL = ROOT / "protocol/m1"


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    failures = []
    manifest = json.loads((PROTOCOL / "manifest.json").read_text())
    body = {key: value for key, value in manifest.items() if key != "content_digest"}
    actual_digest = digest(json.dumps(body, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode())
    if actual_digest != manifest["content_digest"]:
        failures.append("manifest body digest mismatch")
    seen = set()
    paths = [entry["path"] for entry in manifest["files"]]
    if paths != sorted(paths):
        failures.append("manifest paths must be sorted")
    for entry in manifest["files"]:
        path = entry["path"]
        target = ROOT / path
        if path in seen or path.startswith("/") or ".." in Path(path).parts:
            failures.append("unsafe/duplicate manifest path: " + path)
            continue
        seen.add(path)
        if not target.is_file() or target.is_symlink():
            failures.append("missing/non-regular frozen file: " + path)
            continue
        data = target.read_bytes()
        if digest(data) != entry["sha256"] or len(data) != entry["bytes"]:
            failures.append("frozen content changed: " + path)
    essential = {"protocol/m1/README.md", "protocol/m1/state.md", "protocol/m1/auth.md",
                 "protocol/m1/wire.md", "protocol/m1/schema.json", "protocol/m1/methods.json",
                 "protocol/m1/golden-vectors.json", "protocol/m1/check-contract.py",
                 "protocol/m1/check-hashes.mjs", "docs/architecture/core.md",
                 "docs/architecture/blueprint.md", "docs/validation/acceptance.md",
                 "docs/validation/vectors.md", "docs/versioning.md", "requirements-contract.txt", "tools/verify-package.py"}
    if set(paths) != essential:
        failures.append("manifest frozen file set mismatch")
    schema = json.loads((PROTOCOL / "schema.json").read_text())
    catalog = json.loads((PROTOCOL / "methods.json").read_text())
    definitions = schema["$defs"]
    protocol = manifest["protocol_version"]
    if protocol != catalog["protocol_version"] or protocol != definitions["Capabilities"]["properties"]["protocol_version"]["const"]:
        failures.append("protocol version mismatch")
    if schema["$id"] != "urn:agent-task-bridge:" + protocol:
        failures.append("schema identity mismatch")
    requests = {item["properties"]["method"]["const"]: item["properties"]["params"]["$ref"]
                for item in definitions["Request"]["oneOf"]}
    methods = {item["method"]: item for item in catalog["methods"]}
    if len(methods) != len(catalog["methods"]) or methods.keys() != requests.keys():
        failures.append("method catalog/request coverage mismatch")
    wire = (PROTOCOL / "wire.md").read_text()
    for method, entry in methods.items():
        if entry["params"] != requests.get(method) or entry["result_data"].split("/")[-1] not in definitions:
            failures.append("method schema mapping mismatch: " + method)
            continue
        required = definitions[entry["params"].split("/")[-1]]["required"]
        for flag, name in [("idempotency_required", "idempotency_key"),
                           ("task_cas_required", "expected_revision"), ("lease_required", "lease")]:
            expected = name in required
            if flag == "idempotency_required":
                expected = expected and entry["kind"] == "write"
            if entry[flag] != expected:
                failures.append("method flag mismatch: " + method + ":" + flag)
        if method not in wire:
            failures.append("method missing from wire: " + method)
    documents = [ROOT / "README.md", ROOT / "AGENTS.md", *ROOT.glob("docs/**/*.md"), *ROOT.glob("protocol/**/*.md")]
    links = 0
    for path in documents:
        for url in re.findall(r"\[[^\]]+\]\(([^)]+)\)", path.read_text()):
            if re.match(r"^[a-z][a-z0-9+.-]*:", url, re.I) or url.startswith("#"):
                continue
            destination = (path.parent / url.split("#", 1)[0]).resolve()
            if not destination.is_relative_to(ROOT) or not destination.exists():
                failures.append("missing/nonportable link: " + str(path.relative_to(ROOT)) + " → " + url)
            links += 1
    if failures:
        raise SystemExit("\n".join(failures))
    print(f"PASS: {len(paths)} frozen files; {len(methods)} method mappings; {links} local links; {actual_digest}", flush=True)
    subprocess.run([sys.executable, str(PROTOCOL / "check-contract.py")], check=True, cwd=ROOT)
    subprocess.run(["node", str(PROTOCOL / "check-hashes.mjs")], check=True, cwd=ROOT)
    print("BASELINE VERIFIED. No daemon/runtime or H01/V01–V25 acceptance is implied.")


if __name__ == "__main__":
    main()
