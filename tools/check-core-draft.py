#!/usr/bin/env python3
"""Check draft schemas and scenario traces against an offline behavior model.

This does not run the released daemon, authenticate anyone, connect real devices,
exercise durable recovery, or prove interoperability of independent services.
"""
from __future__ import annotations

import argparse
from copy import deepcopy
from pathlib import Path
import re

from jsonschema import Draft202012Validator

from core_draft_model import CoreError, Model, ROOT, SCHEMA, json_equal, parse_json, validate

SYMBOL = re.compile(r"^\$\{([^}]+)\}$")


def lookup(value, path):
    for part in path.split("."):
        value = value[int(part)] if isinstance(value, list) else value[part]
    return value


def resolve(value, results):
    if isinstance(value, str) and (match := SYMBOL.fullmatch(value)):
        return deepcopy(lookup(results, match.group(1)))
    if isinstance(value, list):
        return [resolve(item, results) for item in value]
    if isinstance(value, dict):
        return {key: resolve(item, results) for key, item in value.items()}
    return value


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def visible_states(model, references):
    result = []
    for reference in references:
        try:
            result.append(model.snapshot(reference))
        except CoreError as error:
            if error.kind != "TASK_NOT_FOUND":
                raise
            result.append(None)
    return result


def check_trace(path):
    trace = parse_json(path.read_bytes())
    require(isinstance(trace, dict) and set(trace) == {"id", "description", "application_context", "steps"}, "unexpected trace envelope")
    require(isinstance(trace["id"], str) and trace["id"], "missing trace ID")
    require(isinstance(trace["description"], str) and trace["description"], "missing description")
    require(isinstance(trace["application_context"], dict), "application context must be an object")
    require(isinstance(trace["steps"], list) and trace["steps"], "missing steps")
    model, results, seen, references = Model(), {}, set(), []
    counts = {"commands": 0, "rejections": 0, "replays": 0}
    for step in trace["steps"]:
        require(isinstance(step, dict) and {"id", "command", "context", "expect"} <= step.keys()
                and not (step.keys() - {"id", "command", "context", "expect", "note"}), "unexpected step envelope")
        sid = step["id"]
        require(isinstance(sid, str) and sid and sid not in seen, "duplicate/missing step ID")
        seen.add(sid)
        try:
            command, context, expected = (resolve(step[name], results) for name in ("command", "context", "expect"))
            require(isinstance(expected, dict) and not (expected.keys() - {"state", "revision", "error", "replayed", "event_count", "snapshots", "checks"}), "unexpected expectation field")
            require(expected, "step must assert an outcome")
            reference = command.get("task") if isinstance(command, dict) else None
            if isinstance(reference, dict) and reference not in references:
                validate("TaskRef", reference)
                references.append(reference)
            before = visible_states(model, references)
            events_before = model.events
            try:
                result = model.apply(command, context)
            except CoreError as error:
                require(expected.get("error") == error.kind, f"unexpected error {error.kind}; wanted {expected.get('error')}")
                validate("Error", error.as_dict())
                require(json_equal(before, visible_states(model, references)), "rejected command changed task facts")
                require(json_equal(events_before, model.events), "rejected command emitted an event")
                require(not (expected.keys() & {"state", "revision", "replayed", "checks"}), "error step contains success assertions")
                counts["rejections"] += 1
            else:
                require("error" not in expected, "expected rejection but command committed")
                results[sid] = result
                validate("Result", result)
                for name in ("state", "revision"):
                    if name in expected:
                        require(json_equal(result["task"][name], expected[name]), f"unexpected task {name}")
                if "replayed" in expected:
                    require(json_equal(result["replayed"], expected["replayed"]), "unexpected replay flag")
                for check in expected.get("checks", []):
                    require(set(check) == {"path", "equals"}, "unexpected field assertion")
                    require(json_equal(lookup(result, check["path"]), check["equals"]), f"field mismatch at {check['path']}")
                if result["replayed"]:
                    require(json_equal(before, visible_states(model, references)), "replay changed current task facts")
                    require(json_equal(events_before, model.events), "replay emitted a second event")
                    counts["replays"] += 1
            if "event_count" in expected:
                require(json_equal(len(model.events), expected["event_count"]), "unexpected event count")
            for check in expected.get("snapshots", []):
                require("task" in check and not (check.keys() - {"task", "state", "revision"}), "unexpected snapshot assertion")
                snapshot = model.snapshot(check["task"])
                validate("Snapshot", snapshot)
                for name in ("state", "revision"):
                    if name in check:
                        require(json_equal(snapshot["task"][name], check[name]), f"snapshot {name} mismatch")
            counts["commands"] += 1
        except (CoreError, AssertionError, KeyError, IndexError, TypeError, ValueError) as error:
            raise AssertionError(f"{path.name}:{sid}: {error}") from error
    return trace["id"], counts


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("traces", type=Path, nargs="*", help="optional individual JSON traces")
    args = parser.parse_args()
    Draft202012Validator.check_schema(SCHEMA)
    paths = args.traces or [ROOT / "examples/protocol-scenarios" / (name + ".json")
                           for name in ("marketplace", "enterprise", "devices", "personal")]
    total = {"commands": 0, "rejections": 0, "replays": 0}
    for path in paths:
        name, counts = check_trace(path)
        for key in total:
            total[key] += counts[key]
        print(f"PASS {name}: {counts['commands']} commands, {counts['rejections']} expected rejections, {counts['replays']} historical replays")
    print(f"PASS draft schema + {len(paths)} traces: {total['commands']} commands, {total['rejections']} rejections, {total['replays']} replays")
    print("OFFLINE MODEL ONLY: no deployed new service, authentication, durable recovery, real devices or independent interoperability is implied.")


if __name__ == "__main__":
    main()
