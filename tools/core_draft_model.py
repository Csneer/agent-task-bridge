"""Offline reference semantics for task-core-draft-1, not a network service.

State and receipts live only in this Model instance. No authentication, durable
storage, transport, leases, physical execution, or business policy is provided.
"""
from __future__ import annotations

from copy import deepcopy
from decimal import Decimal, DecimalException
import json
import math
from pathlib import Path

from jsonschema import Draft202012Validator, validators

VERSION = "task-core-draft-1"
MAX_INTEGER = 9007199254740991
MAX_NUMBER = Decimal("1.7976931348623157e308")
ROOT = Path(__file__).resolve().parents[1]
SCHEMA = json.loads((ROOT / "protocol/core-draft/schema.json").read_text())
OPERATIONS = frozenset(SCHEMA["$defs"]["Command"]["properties"]["operation"]["enum"])
EXPECTATION_FIELDS = frozenset(SCHEMA["$defs"]["Expectation"]["properties"])
TERMINAL = frozenset({"completed", "cancelled"})
ID_FIELDS = {name: name.lower() + "_id" for name in ("Assignment", "Run", "Report", "Submission", "Decision")}


class CoreError(Exception):
    def __init__(self, kind: str):
        self.kind = kind
        super().__init__(kind)

    def as_dict(self):
        return {"protocol_version": VERSION, "kind": self.kind}


def _reject(kind: str):
    raise CoreError(kind)


def _pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON object key")
        result[key] = value
    return result


def _constant(_):
    raise ValueError("nonfinite JSON number")


def _json_value(value):
    """Copy a JSON tree while preserving exact parsed decimal values."""
    if value is None or isinstance(value, bool):
        return value
    if isinstance(value, str):
        value.encode("utf-8", errors="strict")
        return value
    if isinstance(value, (int, float, Decimal)):
        if isinstance(value, float):
            if not math.isfinite(value):
                raise ValueError("nonfinite number")
            # An already-rounded caller float cannot recover its source digits.
            value = Decimal(str(value))
        number = value if isinstance(value, Decimal) else Decimal(value)
        if not number.is_finite() or number.copy_abs() > MAX_NUMBER:
            raise ValueError("number exceeds finite binary64 magnitude")
        return value
    if isinstance(value, list):
        return [_json_value(item) for item in value]
    if isinstance(value, dict):
        if not all(isinstance(key, str) for key in value):
            raise ValueError("JSON object keys must be strings")
        return {_json_value(key): _json_value(item) for key, item in value.items()}
    raise ValueError("not a JSON value")


def parse_json(raw: str | bytes):
    """Parse strict UTF-8 JSON with exact decimal numbers and duplicate rejection."""
    try:
        if isinstance(raw, bytes):
            raw = raw.decode("utf-8", errors="strict")
        if not isinstance(raw, str):
            raise ValueError("expected JSON bytes or text")
        return _json_value(json.loads(raw, object_pairs_hook=_pairs,
                                      parse_float=Decimal, parse_constant=_constant))
    except (ValueError, UnicodeError, TypeError, OverflowError, RecursionError, DecimalException):
        _reject("INVALID_MESSAGE")


def json_equal(left, right):
    """JSON equality: booleans are not numbers; objects are unordered."""
    if isinstance(left, bool) or isinstance(right, bool):
        return isinstance(left, bool) and isinstance(right, bool) and left is right
    if isinstance(left, (int, float, Decimal)) and isinstance(right, (int, float, Decimal)):
        return Decimal(str(left)) == Decimal(str(right))
    if type(left) is not type(right):
        return False
    if isinstance(left, dict):
        return left.keys() == right.keys() and all(json_equal(left[k], right[k]) for k in left)
    if isinstance(left, list):
        return len(left) == len(right) and all(json_equal(a, b) for a, b in zip(left, right))
    return left == right


def _integer(_checker, value):
    return not isinstance(value, bool) and isinstance(value, (int, Decimal)) and value == int(value)


Validator = validators.extend(Draft202012Validator, type_checker=Draft202012Validator.TYPE_CHECKER.redefine("integer", _integer))
_VALIDATORS = {name: Validator({"$schema": SCHEMA["$schema"], "$defs": SCHEMA["$defs"], "$ref": "#/$defs/" + name})
               for name in SCHEMA["$defs"]}


def validate(definition: str, value):
    if not _VALIDATORS[definition].is_valid(value):
        _reject("INVALID_MESSAGE")


def _key(task):
    return task["authority_id"], task["task_id"]


def _expectation(body, version, actor, reason):
    return {**{k: deepcopy(v) for k, v in body.items() if k in EXPECTATION_FIELDS},
            "version": version, "changed_by": deepcopy(actor), "reason": reason}


def _record(state, kind, identity):
    for item in state["records"]:
        if item["record_type"] == kind and item["record"][ID_FIELDS[kind]] == identity:
            return item["record"]
    _reject("INVALID_STATE")


def _put(state, changed, kind, record):
    envelope = {"record_type": kind, "record": deepcopy(record)}
    for index, old in enumerate(state["records"]):
        if old["record_type"] == kind and old["record"][ID_FIELDS[kind]] == record[ID_FIELDS[kind]]:
            state["records"][index] = envelope
            break
    else:
        state["records"].append(envelope)
    changed.append(deepcopy(envelope))


def _issuer(task, actor):
    if not json_equal(task["issuer"], actor):
        _reject("ROLE_MISMATCH")


def _assignment(state, body, actor, allow_issuer=False):
    task = state["task"]
    if task["current_assignment_id"] != body["assignment_id"] or task["generation"] != body["generation"]:
        _reject("STALE_ASSIGNMENT")
    assignment = _record(state, "Assignment", body["assignment_id"])
    if assignment["state"] not in ("claimed", "active"):
        _reject("STALE_ASSIGNMENT")
    if not json_equal(assignment["assignee"], actor) and not (allow_issuer and json_equal(task["issuer"], actor)):
        _reject("ROLE_MISMATCH")
    return assignment


def _run(state, body, actor):
    assignment = _assignment(state, body, actor)
    task = state["task"]
    if task["current_run_id"] != body["run_id"]:
        _reject("STALE_ASSIGNMENT")
    run = _record(state, "Run", body["run_id"])
    if task["state"] != "active" or run["state"] != "running" or assignment["state"] != "active":
        _reject("INVALID_STATE")
    return assignment, run


def _end_current(state, changed, reason, assignment_state, run_state):
    task = state["task"]
    if task["current_assignment_id"] is not None:
        assignment = _record(state, "Assignment", task["current_assignment_id"])
        assignment.update(state=assignment_state, reason=reason)
        _put(state, changed, "Assignment", assignment)
    if task["current_run_id"] is not None:
        run = _record(state, "Run", task["current_run_id"])
        run.update(state=run_state, reason=reason)
        _put(state, changed, "Run", run)
    task.update(current_assignment_id=None, current_run_id=None)


class Model:
    """One process-local oracle; each TaskRef remains a separate authority key."""
    def __init__(self):
        self._tasks = {}
        self._receipts = {}
        self._events = []

    @property
    def events(self):
        return deepcopy(self._events)

    def snapshot(self, task):
        """Trusted test inspection, not an authenticated public query binding."""
        try:
            task = _json_value(task)
        except (ValueError, UnicodeError, TypeError, OverflowError, DecimalException):
            _reject("INVALID_MESSAGE")
        validate("TaskRef", task)
        if _key(task) not in self._tasks:
            _reject("TASK_NOT_FOUND")
        return deepcopy(self._tasks[_key(task)])

    def apply(self, command, context):
        try:
            command, context = _json_value(command), _json_value(context)
        except (ValueError, UnicodeError, TypeError, OverflowError, RecursionError, DecimalException):
            _reject("INVALID_MESSAGE")
        validate("HostContext", context)
        if not isinstance(command, dict):
            _reject("INVALID_MESSAGE")
        if command.get("protocol_version") != VERSION:
            _reject("UNSUPPORTED_VERSION")
        if not isinstance(command.get("operation"), str) or command["operation"] not in OPERATIONS:
            _reject("UNSUPPORTED_OPERATION")
        validate("Command", command)
        actor = context["actor"]
        task_ref = command["task"]
        receipt_key = (task_ref["authority_id"], actor["kind"], actor["id"], command["command_id"])
        if receipt_key in self._receipts:
            if not context["can_read_history"]:
                _reject("NOT_ADMITTED")
            original, saved = self._receipts[receipt_key]
            if not json_equal(original, command):
                _reject("IDEMPOTENCY_CONFLICT")
            result = deepcopy(saved)
            result["replayed"] = True
            return result
        if not context["can_execute"]:
            _reject("NOT_ADMITTED")
        key = _key(task_ref)
        op, body = command["operation"], command["body"]
        if op == "task.create":
            if key in self._tasks:
                _reject("TASK_EXISTS")
            if command["expected_revision"] != 0:
                _reject("STALE_REVISION")
            state = {"task": {**task_ref, "issuer": actor, "state": "offered", "revision": 0,
                     "expectation_version": 1, "expectations": [_expectation(body, 1, actor, "created")],
                     "generation": 0, "current_assignment_id": None, "current_run_id": None,
                     "pending_submission_id": None, "relations": deepcopy(body.get("relations", []))}, "records": []}
        else:
            if key not in self._tasks:
                _reject("TASK_NOT_FOUND")
            state = deepcopy(self._tasks[key])
            if command["expected_revision"] != state["task"]["revision"]:
                _reject("STALE_REVISION")
        task, changed = state["task"], []
        if task["revision"] >= MAX_INTEGER:
            _reject("INVALID_STATE")
        new_revision = task["revision"] + 1
        identity = lambda kind: f"{kind}-{new_revision}"
        if op == "task.revise":
            _issuer(task, actor)
            if task["state"] in TERMINAL or task["expectation_version"] >= MAX_INTEGER:
                _reject("INVALID_STATE")
            task["expectation_version"] += 1
            task["expectations"].append(_expectation(body, task["expectation_version"], actor, body["reason"]))
        elif op == "task.claim":
            if task["state"] != "offered" or task["current_assignment_id"] is not None:
                _reject("INVALID_STATE")
            if task["generation"] >= MAX_INTEGER:
                _reject("INVALID_STATE")
            task["generation"] += 1
            aid = identity("assignment")
            task.update(state="claimed", current_assignment_id=aid)
            _put(state, changed, "Assignment", {"task": task_ref, "assignment_id": aid, "assignee": actor,
                 "generation": task["generation"], "state": "claimed"})
        elif op == "run.start":
            assignment = _assignment(state, body, actor)
            if task["state"] != "claimed":
                _reject("INVALID_STATE")
            if body["basis_version"] != task["expectation_version"]:
                _reject("INVALID_BASIS")
            rid = identity("run")
            assignment["state"] = "active"
            _put(state, changed, "Assignment", assignment)
            _put(state, changed, "Run", {"task": task_ref, "run_id": rid,
                 "assignment_id": assignment["assignment_id"], "assignee": actor,
                 "basis_version": int(body["basis_version"]), "state": "running"})
            task.update(state="active", current_run_id=rid)
        elif op in ("run.report", "run.submit", "run.fail"):
            assignment, run = _run(state, body, actor)
            if op == "run.report":
                _put(state, changed, "Report", {"task": task_ref, "report_id": identity("report"),
                     "run_id": run["run_id"], "reported_by": actor, "message": body["message"],
                     "extensions": deepcopy(body.get("extensions", {}))})
            elif op == "run.submit":
                if body["basis_version"] != run["basis_version"]:
                    _reject("INVALID_BASIS")
                assignment["state"], run["state"] = "completed", "submitted"
                _put(state, changed, "Assignment", assignment)
                _put(state, changed, "Run", run)
                sid = identity("submission")
                _put(state, changed, "Submission", {"task": task_ref, "submission_id": sid,
                     "assignment_id": assignment["assignment_id"], "run_id": run["run_id"],
                     "basis_version": run["basis_version"], "submitted_by": actor,
                     "summary": body["summary"], "artifacts": deepcopy(body.get("artifacts", [])),
                     "extensions": deepcopy(body.get("extensions", {}))})
                task.update(state="submitted", current_assignment_id=None, current_run_id=None, pending_submission_id=sid)
            else:
                _end_current(state, changed, body["reason"], "failed", "failed")
                task.update(state="failed", pending_submission_id=None)
        elif op == "task.decide":
            _issuer(task, actor)
            if task["state"] != "submitted" or task["pending_submission_id"] != body["submission_id"]:
                _reject("INVALID_STATE")
            submission = _record(state, "Submission", body["submission_id"])
            _put(state, changed, "Decision", {"task": task_ref, "decision_id": identity("decision"),
                 "submission_id": submission["submission_id"], "decided_by": actor,
                 "outcome": body["outcome"], "reason": body["reason"],
                 "basis_version": submission["basis_version"], "evaluated_version": task["expectation_version"]})
            task.update(state="completed" if body["outcome"] == "accepted" else "rejected", pending_submission_id=None)
        elif op == "assignment.release":
            _assignment(state, body, actor, allow_issuer=True)
            if task["state"] not in ("claimed", "active"):
                _reject("INVALID_STATE")
            _end_current(state, changed, body["reason"], "released", "released")
            task.update(state="offered", pending_submission_id=None)
        elif op == "task.reopen":
            _issuer(task, actor)
            if task["state"] not in ("failed", "rejected"):
                _reject("INVALID_STATE")
            task.update(state="offered", current_assignment_id=None, current_run_id=None, pending_submission_id=None)
        elif op == "task.cancel":
            _issuer(task, actor)
            if task["state"] in TERMINAL:
                _reject("INVALID_STATE")
            _end_current(state, changed, body["reason"], "cancelled", "cancelled")
            task.update(state="cancelled", pending_submission_id=None)
        task["revision"] = new_revision
        event = {"protocol_version": VERSION, "event_id": f"event-{len(self._events) + 1}",
                 "task": deepcopy(task_ref), "revision": new_revision, "actor": deepcopy(actor),
                 "operation": op, "command_id": command["command_id"],
                 "task_snapshot": deepcopy(task), "records": deepcopy(changed)}
        if "reason" in body:
            event["reason"] = body["reason"]
        result = {"protocol_version": VERSION, "command_id": command["command_id"], "replayed": False,
                  "task": deepcopy(task), "records": deepcopy(changed), "event": event}
        validate("Snapshot", state)
        validate("Result", result)
        # Only after every guard/schema check succeeds do all facts become visible.
        self._tasks[key] = state
        self._events.append(deepcopy(event))
        self._receipts[receipt_key] = deepcopy((command, result))
        return deepcopy(result)
