"""Behavioral counterexamples for the offline draft, not deployed-service tests."""

from copy import deepcopy
from decimal import Decimal
import json
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "tools"))
from core_draft_model import CoreError, Model, parse_json


VERSION = "task-core-draft-1"
ISSUER = {"kind": "human", "id": "customer"}
WORKER = {"kind": "agent", "id": "worker"}
HUMAN_WORKER = {"kind": "human", "id": "reviewer"}
OTHER = {"kind": "agent", "id": "other"}
TASK = {"authority_id": "platform-a", "task_id": "task-1"}


def context(actor, *, execute=True, history=True):
    return {"actor": deepcopy(actor), "can_execute": execute, "can_read_history": history}


class CoreDraftBehaviorTests(unittest.TestCase):
    def setUp(self):
        self.model = Model()
        self.sequence = 0

    def command(self, operation, body=None, *, task=TASK, revision=None, key=None):
        self.sequence += 1
        if revision is None:
            revision = 0 if operation == "task.create" else self.model.snapshot(task)["task"]["revision"]
        return {"protocol_version": VERSION, "command_id": key or f"command-{self.sequence}",
                "task": deepcopy(task), "expected_revision": revision,
                "operation": operation, "body": deepcopy(body if body is not None else {})}

    def call(self, operation, body=None, *, actor=ISSUER, task=TASK, revision=None):
        return self.model.apply(self.command(operation, body, task=task, revision=revision), context(actor))

    def create(self, *, task=TASK, actor=ISSUER, **extra):
        body = {"goal": "Prepare a useful result", "expected_result": "A result for the issuer to evaluate"}
        body.update(extra)
        return self.call("task.create", body, task=task, actor=actor)

    def claim(self, *, task=TASK, actor=WORKER):
        result = self.call("task.claim", actor=actor, task=task)
        return {"assignment_id": result["task"]["current_assignment_id"],
                "generation": result["task"]["generation"]}

    def start(self, assignment, *, task=TASK, actor=WORKER):
        basis = self.model.snapshot(task)["task"]["expectation_version"]
        result = self.call("run.start", {**assignment, "basis_version": basis}, actor=actor, task=task)
        return {**assignment, "run_id": result["task"]["current_run_id"], "basis_version": basis}

    def active(self, *, task=TASK, issuer=ISSUER, actor=WORKER, **extra):
        self.create(task=task, actor=issuer, **extra)
        return self.start(self.claim(task=task, actor=actor), task=task, actor=actor)

    def submit_command(self, execution, *, task=TASK):
        return self.command("run.submit", {**execution, "summary": "Actual delivered result"}, task=task)

    def submit(self, execution, *, task=TASK, actor=WORKER):
        return self.model.apply(self.submit_command(execution, task=task), context(actor))

    def decide(self, *, task=TASK, actor=ISSUER, outcome="accepted"):
        submission = self.model.snapshot(task)["task"]["pending_submission_id"]
        return self.call("task.decide", {"submission_id": submission, "outcome": outcome,
                                        "reason": "Issuer inspected the actual delivery"}, actor=actor, task=task)

    def records(self, record_type, *, task=TASK):
        return [item["record"] for item in self.model.snapshot(task)["records"]
                if item["record_type"] == record_type]

    def assert_rejected(self, command, actor=WORKER, kind="INVALID_STATE", *, ctx=None):
        """A rejected command must not change either facts or published events."""
        task = command["task"]
        try:
            before = self.model.snapshot(task)
        except CoreError as error:
            self.assertEqual(error.kind, "TASK_NOT_FOUND")
            before = None
        events = self.model.events
        with self.assertRaises(CoreError) as raised:
            self.model.apply(command, ctx if ctx is not None else context(actor))
        self.assertEqual(raised.exception.kind, kind)
        self.assertEqual(self.model.events, events)
        if before is not None:
            self.assertEqual(self.model.snapshot(task), before)
        else:
            with self.assertRaises(CoreError) as missing:
                self.model.snapshot(task)
            self.assertEqual(missing.exception.kind, "TASK_NOT_FOUND")

    def revise(self, *, actor=ISSUER, task=TASK):
        return self.call("task.revise", {"goal": "Prepare a useful result",
                                        "expected_result": "An updated result, evaluated after delivery",
                                        "reason": "Issuer clarified expectations after discussion"},
                         actor=actor, task=task)

    def test_create_requires_expected_result(self):
        self.assert_rejected(self.command("task.create", {"goal": "Undefined expected result"}),
                             actor=ISSUER, kind="INVALID_MESSAGE")

    def test_human_issuer_is_derived_from_trusted_context(self):
        result = self.create()
        self.assertEqual(result["task"]["issuer"], ISSUER)
        self.assertEqual(result["task"]["expectation_version"], 1)
        self.assertEqual(result["task"]["state"], "offered")

    def test_human_can_execute_and_agent_issuer_can_accept(self):
        execution = self.active(issuer=OTHER, actor=HUMAN_WORKER)
        self.submit(execution, actor=HUMAN_WORKER)
        self.assertEqual(self.decide(actor=OTHER)["task"]["state"], "completed")

    def test_progress_is_not_delivery_or_completion(self):
        execution = self.active()
        body = {key: value for key, value in execution.items() if key != "basis_version"}
        result = self.call("run.report", {**body, "message": "All calculations are done"}, actor=WORKER)
        self.assertEqual(result["task"]["state"], "active")
        self.assertEqual(len(self.records("Report")), 1)
        self.assertEqual(self.records("Submission"), [])
        self.assertEqual(self.records("Decision"), [])

    def test_submission_waits_for_issuer_and_ends_execution_pointers(self):
        result = self.submit(self.active())
        self.assertEqual(result["task"]["state"], "submitted")
        self.assertIsNone(result["task"]["current_assignment_id"])
        self.assertIsNone(result["task"]["current_run_id"])
        self.assertIsNotNone(result["task"]["pending_submission_id"])
        self.assertEqual(self.records("Assignment")[0]["state"], "completed")
        self.assertEqual(self.records("Run")[0]["state"], "submitted")
        self.assertEqual(self.records("Decision"), [])

    def test_admitted_assignee_cannot_accept_its_own_delivery(self):
        result = self.submit(self.active())
        command = self.command("task.decide", {"submission_id": result["task"]["pending_submission_id"],
                                              "outcome": "accepted", "reason": "Self assessment"})
        self.assert_rejected(command, actor=WORKER, kind="ROLE_MISMATCH")

    def test_same_actor_id_with_other_kind_does_not_become_issuer(self):
        result = self.submit(self.active())
        command = self.command("task.decide", {"submission_id": result["task"]["pending_submission_id"],
                                              "outcome": "accepted", "reason": "Same id is not same identity"})
        self.assert_rejected(command, actor={"kind": "agent", "id": ISSUER["id"]}, kind="ROLE_MISMATCH")

    def test_completed_review_task_does_not_complete_parent_or_grant_decision_role(self):
        self.submit(self.active())
        parent_before = self.model.snapshot(TASK)
        review = {**TASK, "task_id": "review"}
        execution = self.active(task=review, issuer=WORKER, actor=HUMAN_WORKER,
                                relations=[{"kind": "parent", "target": TASK}])
        self.submit(execution, task=review, actor=HUMAN_WORKER)
        self.decide(task=review, actor=WORKER)
        self.assertEqual(self.model.snapshot(TASK), parent_before)
        command = self.command("task.decide", {"submission_id": parent_before["task"]["pending_submission_id"],
                                              "outcome": "accepted", "reason": "Review task completed"})
        self.assert_rejected(command, actor=HUMAN_WORKER, kind="ROLE_MISMATCH")

    def test_active_revision_preserves_original_basis_and_issuer_can_accept_old_basis(self):
        execution = self.active()
        old_expectation = self.model.snapshot(TASK)["task"]["expectations"][0]
        self.revise()
        snapshot = self.model.snapshot(TASK)
        self.assertEqual(snapshot["task"]["expectations"][0], old_expectation)
        self.assertEqual(len(snapshot["task"]["expectations"]), 2)
        self.assertEqual(self.records("Run")[0]["basis_version"], 1)
        self.submit(execution)
        self.assertEqual(self.records("Submission")[0]["basis_version"], 1)
        result = self.decide()
        self.assertEqual(result["task"]["state"], "completed")
        decision = self.records("Decision")[0]
        self.assertEqual((decision["basis_version"], decision["evaluated_version"]), (1, 2))
        self.assertEqual(self.records("Run")[0]["state"], "submitted")

    def test_post_delivery_revision_does_not_rewrite_submission(self):
        self.submit(self.active())
        delivered = self.records("Submission")
        self.revise()
        self.assertEqual(self.records("Submission"), delivered)
        self.decide()
        self.assertEqual(self.records("Decision")[0]["evaluated_version"], 2)

    def test_new_run_cannot_choose_stale_expectation_version(self):
        self.create()
        assignment = self.claim()
        self.revise()
        self.assert_rejected(self.command("run.start", {**assignment, "basis_version": 1}), kind="INVALID_BASIS")
        execution = self.start(assignment)
        self.assertEqual(execution["basis_version"], 2)

    def test_submission_cannot_silently_rebase_existing_run(self):
        execution = self.active()
        self.revise()
        self.assert_rejected(self.submit_command({**execution, "basis_version": 2}), kind="INVALID_BASIS")

    def test_assignee_cannot_revise_issuer_expectations(self):
        self.active()
        self.assert_rejected(self.command("task.revise", {"goal": "Different goal", "expected_result": "Less work",
                                                          "reason": "Worker prefers it"}), kind="ROLE_MISMATCH")

    def test_terminal_tasks_cannot_be_revised(self):
        for terminal in ("completed", "cancelled"):
            with self.subTest(state=terminal):
                task = {**TASK, "task_id": terminal}
                if terminal == "completed":
                    self.submit(self.active(task=task), task=task)
                    self.decide(task=task)
                else:
                    self.create(task=task)
                    self.call("task.cancel", {"reason": "No longer required"}, task=task)
                command = self.command("task.revise", {"goal": "New goal", "expected_result": "New result",
                                                       "reason": "Late update"}, task=task)
                self.assert_rejected(command, actor=ISSUER, kind="INVALID_STATE")

    def test_reopen_retains_history_and_old_submission_cannot_be_decided_again(self):
        self.submit(self.active())
        old_submission = self.records("Submission")[0]
        self.decide(outcome="rejected")
        old_decision = self.records("Decision")[0]
        opened = self.call("task.reopen", {"reason": "Please rework"})
        for pointer in ("current_assignment_id", "current_run_id", "pending_submission_id"):
            self.assertIsNone(opened["task"][pointer])
        execution = self.start(self.claim())
        self.assertEqual(execution["generation"], 2)
        self.submit(execution)
        self.assertEqual(self.records("Submission")[0], old_submission)
        self.assertEqual(self.records("Decision")[0], old_decision)
        self.assert_rejected(self.command("task.decide", {"submission_id": old_submission["submission_id"],
                                                         "outcome": "accepted", "reason": "Wrong attempt"}),
                             actor=ISSUER, kind="INVALID_STATE")

    def test_two_claimants_with_same_revision_have_one_winner(self):
        self.create()
        first = self.command("task.claim", revision=1)
        competing = self.command("task.claim", revision=1)
        self.model.apply(first, context(WORKER))
        self.assert_rejected(competing, actor=OTHER, kind="STALE_REVISION")
        self.assertEqual(len(self.records("Assignment")), 1)

    def test_current_revision_still_does_not_allow_second_holder(self):
        self.create()
        self.claim()
        self.assert_rejected(self.command("task.claim"), actor=OTHER, kind="INVALID_STATE")
        self.assertEqual(len(self.records("Assignment")), 1)

    def test_old_assignment_cannot_submit_after_replacement_even_with_fresh_revision(self):
        old = self.active()
        self.call("assignment.release", {"assignment_id": old["assignment_id"], "generation": old["generation"],
                                         "reason": "Issuer reassigned work"})
        current = self.start(self.claim())
        self.assertNotEqual(old["assignment_id"], current["assignment_id"])
        self.assert_rejected(self.submit_command(old), kind="STALE_ASSIGNMENT")

    def test_host_admission_does_not_override_current_assignee(self):
        execution = self.active()
        self.assert_rejected(self.submit_command(execution), actor=OTHER, kind="ROLE_MISMATCH")

    def test_stale_revision_rejection_keeps_facts_and_events_unchanged(self):
        execution = self.active()
        command = self.submit_command(execution)
        self.revise()
        self.assert_rejected(command, kind="STALE_REVISION")

    def test_release_before_start_creates_no_run(self):
        self.create()
        assignment = self.claim()
        result = self.call("assignment.release", {**assignment, "reason": "Cannot start"}, actor=WORKER)
        self.assertEqual(result["task"]["state"], "offered")
        self.assertIsNone(result["task"]["current_assignment_id"])
        self.assertIsNone(result["task"]["current_run_id"])
        self.assertEqual(self.records("Run"), [])
        self.assertEqual(self.records("Assignment")[0]["state"], "released")

    def test_cancel_active_ends_current_attempt_without_submission(self):
        self.active()
        result = self.call("task.cancel", {"reason": "No longer required"})
        self.assertEqual(result["task"]["state"], "cancelled")
        self.assertIsNone(result["task"]["current_assignment_id"])
        self.assertIsNone(result["task"]["current_run_id"])
        self.assertEqual(self.records("Assignment")[0]["state"], "cancelled")
        self.assertEqual(self.records("Run")[0]["state"], "cancelled")
        self.assertEqual(self.records("Submission"), [])

    def test_cancel_submitted_preserves_completed_execution_and_delivery(self):
        self.submit(self.active())
        before = {kind: self.records(kind) for kind in ("Assignment", "Run", "Submission")}
        result = self.call("task.cancel", {"reason": "Issuer stopped the task after delivery"})
        self.assertEqual(result["task"]["state"], "cancelled")
        self.assertIsNone(result["task"]["pending_submission_id"])
        for kind, records in before.items():
            self.assertEqual(self.records(kind), records)

    def test_failure_is_not_rejection_and_retry_requires_explicit_reopen(self):
        execution = self.active()
        body = {key: value for key, value in execution.items() if key != "basis_version"}
        result = self.call("run.fail", {**body, "reason": "Input unavailable"}, actor=WORKER)
        self.assertEqual(result["task"]["state"], "failed")
        self.assertEqual(self.records("Run")[0]["state"], "failed")
        self.assertEqual(self.records("Decision"), [])
        self.assert_rejected(self.command("task.claim"), kind="INVALID_STATE")
        self.assertEqual(self.call("task.reopen", {"reason": "Input restored"})["task"]["state"], "offered")

    def test_reopen_reason_is_preserved_in_public_event(self):
        self.submit(self.active())
        self.decide(outcome="rejected")
        command = self.command("task.reopen", {"reason": "Issuer requested a second candidate"})
        result = self.model.apply(command, context(ISSUER))
        self.assertEqual(result["event"]["reason"], command["body"]["reason"])
        self.assertEqual(result["event"]["command_id"], command["command_id"])

    def test_cancellation_after_delivery_keeps_public_reason_without_rewriting_result(self):
        self.submit(self.active())
        submission = self.records("Submission")
        command = self.command("task.cancel", {"reason": "Business requirement was withdrawn"})
        result = self.model.apply(command, context(ISSUER))
        self.assertEqual(result["event"]["reason"], command["body"]["reason"])
        self.assertEqual(result["event"]["command_id"], command["command_id"])
        self.assertEqual(self.records("Submission"), submission)

    def test_same_local_task_id_is_distinct_between_authorities(self):
        other_task = {**TASK, "authority_id": "platform-b"}
        self.create()
        untouched = self.model.snapshot(TASK)
        second = self.command("task.create", {"goal": "Other platform", "expected_result": "Independent result"},
                              task=other_task, key="command-1")
        self.model.apply(second, context(ISSUER))
        self.claim(task=other_task)
        self.assertEqual(self.model.snapshot(TASK), untouched)
        self.assertEqual(self.model.snapshot(other_task)["task"]["state"], "claimed")

    def test_external_reference_does_not_require_or_create_remote_task(self):
        remote = {"authority_id": "external-platform", "task_id": "external-task"}
        self.create(relations=[{"kind": "external", "target": remote}])
        self.assertEqual(self.model.snapshot(TASK)["task"]["relations"][0]["target"], remote)
        with self.assertRaises(CoreError) as missing:
            self.model.snapshot(remote)
        self.assertEqual(missing.exception.kind, "TASK_NOT_FOUND")

    def test_history_only_retry_after_completion_returns_original_submission_result(self):
        execution = self.active()
        command = self.submit_command(execution)
        original = self.model.apply(command, context(WORKER))
        self.decide()
        before = self.model.snapshot(TASK)
        events = self.model.events
        replay = self.model.apply(command, context(WORKER, execute=False, history=True))
        self.assertTrue(replay["replayed"])
        self.assertEqual(replay["task"], original["task"])
        self.assertEqual(replay["records"], original["records"])
        self.assertEqual(replay["event"], original["event"])
        self.assertEqual(replay["task"]["state"], "submitted")
        self.assertEqual(self.model.snapshot(TASK), before)
        self.assertEqual(self.model.events, events)

    def test_execute_permission_does_not_disclose_existing_receipt(self):
        command = self.command("task.create", {"goal": "Private result", "expected_result": "Restricted receipt"})
        self.model.apply(command, context(ISSUER))
        self.assert_rejected(command, actor=ISSUER, kind="NOT_ADMITTED",
                             ctx=context(ISSUER, execute=True, history=False))

    def test_history_permission_does_not_execute_new_commands(self):
        command = self.command("task.create", {"goal": "No execution", "expected_result": "None"})
        self.assert_rejected(command, actor=ISSUER, kind="NOT_ADMITTED",
                             ctx=context(ISSUER, execute=False, history=True))
        self.model.apply(command, context(ISSUER))
        self.assertEqual(self.model.snapshot(TASK)["task"]["revision"], 1)

    def test_receipt_identity_includes_actor_kind_and_id(self):
        first = self.command("task.create", {"goal": "First", "expected_result": "One"}, key="shared-key")
        self.model.apply(first, context(ISSUER))
        second = self.command("task.create", {"goal": "Second", "expected_result": "Two"},
                              task={**TASK, "task_id": "second"}, key="shared-key")
        result = self.model.apply(second, context({"kind": "agent", "id": ISSUER["id"]}))
        self.assertFalse(result["replayed"])
        self.assertEqual(result["task"]["issuer"]["kind"], "agent")

    def test_boolean_number_and_missing_null_are_distinct_for_same_key(self):
        for original, replacement in (({"value": True}, {"value": 1}), ({}, {"value": None}),
                                      ({"values": [1, 2]}, {"values": [2, 1]})):
            with self.subTest(original=original, replacement=replacement):
                task = {**TASK, "task_id": f"equality-{self.sequence}"}
                command = self.command("task.create", {"goal": "Compare payload", "expected_result": "Exact retry",
                                                       "extensions": {"org.example.value": original}}, task=task)
                self.model.apply(command, context(ISSUER))
                changed = deepcopy(command)
                changed["body"]["extensions"]["org.example.value"] = replacement
                self.assert_rejected(changed, actor=ISSUER, kind="IDEMPOTENCY_CONFLICT")

    def test_object_order_and_exact_integral_number_spelling_are_equivalent(self):
        command = self.command("task.create", {"goal": "Compare payload", "expected_result": "Exact retry",
                                               "extensions": {"org.example.value": {"value": 1, "other": "x"}}})
        original = self.model.apply(parse_json(json.dumps(command)), context(ISSUER))
        reordered = dict(reversed(list(command.items())))
        reordered["body"] = dict(reversed(list(command["body"].items())))
        raw = json.dumps(reordered).replace('"value": 1', '"value": 1.0')
        result = self.model.apply(parse_json(raw), context(ISSUER))
        self.assertTrue(result["replayed"])
        self.assertEqual(result["event"], original["event"])
        self.assertEqual(len(self.model.events), 1)

    def test_exact_fraction_does_not_round_into_a_receipt_match(self):
        command = self.command("task.create", {"goal": "Compare exact decimals", "expected_result": "No rounding",
                                               "extensions": {"org.example.value": {"value": 1}}})
        self.model.apply(parse_json(json.dumps(command)), context(ISSUER))
        changed = parse_json(json.dumps(command).replace('"value": 1', '"value": 1.0000000000000001'))
        self.assert_rejected(changed, actor=ISSUER, kind="IDEMPOTENCY_CONFLICT")

    def test_fractional_business_extension_is_accepted(self):
        result = self.create(extensions={"org.example.market": {"rating": Decimal("4.8")}})
        value = result["task"]["expectations"][0]["extensions"]["org.example.market"]["rating"]
        self.assertEqual(value, Decimal("4.8"))

    def test_result_snapshot_and_events_do_not_expose_mutable_model_state(self):
        command = self.command("task.create", {"goal": "Original goal", "expected_result": "Original result"})
        original = self.model.apply(command, context(ISSUER))
        expected = deepcopy(original)
        original["task"]["expectations"][0]["goal"] = "Mutated response"
        snapshot = self.model.snapshot(TASK)
        snapshot["task"]["state"] = "completed"
        events = self.model.events
        events.clear()
        replay = self.model.apply(command, context(ISSUER))
        self.assertEqual(replay["task"], expected["task"])
        self.assertEqual(replay["event"], expected["event"])
        self.assertEqual(self.model.snapshot(TASK)["task"]["state"], "offered")
        self.assertEqual(len(self.model.events), 1)

    def test_unsupported_version_and_operation_are_explicit(self):
        for field, value, error in (("protocol_version", "unknown-version", "UNSUPPORTED_VERSION"),
                                    ("operation", "task.set_status", "UNSUPPORTED_OPERATION")):
            with self.subTest(field=field):
                command = self.command("task.create", {"goal": "Unsupported", "expected_result": "Rejected"})
                command[field] = value
                self.assert_rejected(command, actor=ISSUER, kind=error)

    def test_core_integer_overflow_is_invalid(self):
        command = self.command("task.create", {"goal": "Overflow", "expected_result": "Rejected"}, revision=2**53)
        self.assert_rejected(command, actor=ISSUER, kind="INVALID_MESSAGE")

    def test_unknown_core_fields_do_not_become_authority(self):
        command = self.command("task.create", {"goal": "Not self authenticating", "expected_result": "Rejected"})
        command["actor"] = ISSUER
        self.assert_rejected(command, actor=ISSUER, kind="INVALID_MESSAGE")


class CoreDraftParsingTests(unittest.TestCase):
    def test_invalid_json_inputs_are_rejected(self):
        for raw in ('{"key":1,"key":2}', '{"nested":{"key":1,"key":2}}',
                    '{"value":NaN}', '{"value":Infinity}', '{"value":-Infinity}',
                    '{"value":1e309}', '"\\ud800"', '"\\udfff"', b'"\xff"'):
            with self.subTest(raw=raw):
                with self.assertRaises((CoreError, ValueError, UnicodeError)):
                    parse_json(raw)

    def test_parser_preserves_exact_fraction_and_valid_unicode(self):
        parsed = parse_json('{"value":1.0000000000000001,"text":"人和 Agent \\ud83d\\ude80"}')
        self.assertEqual(parsed["value"], Decimal("1.0000000000000001"))
        self.assertNotEqual(parsed["value"], 1)
        self.assertEqual(parsed["text"], "人和 Agent 🚀")

    def test_numeric_upper_bound_is_checked_without_decimal_context_rounding(self):
        maximum = "1.7976931348623157e308"
        parsed = parse_json('{"value":' + maximum + '}')
        self.assertEqual(parsed["value"], Decimal(maximum))
        for raw in ('{"value":1.7976931348623157000000000000000001e308}',
                    '{"value":-1.7976931348623157000000000000000001e308}',
                    '{"value":1e1000000}', '{"value":1e9999999999999999999}'):
            with self.subTest(raw=raw):
                with self.assertRaises(CoreError) as rejected:
                    parse_json(raw)
                self.assertEqual(rejected.exception.kind, "INVALID_MESSAGE")


if __name__ == "__main__":
    unittest.main()
