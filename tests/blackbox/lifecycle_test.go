package blackbox

import (
	"context"
	"reflect"
	"testing"
	"time"

	"agent-task-bridge/tests/fixtures"
)

func TestV01RejectedSubmissionCanBeReworked(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	f.submit()
	s1 := f.submission
	p := f.params()
	p["submission_id"] = s1
	p["reason"] = "Needs correction"
	p["evidence_refs"] = []any{}
	x := h.client(fixtures.Acceptance)
	request, err := x.Prepare("task.reject", p)
	if err != nil {
		t.Fatal(err)
	}
	d1, err := x.Do(context.Background(), request)
	if err != nil || d1.Error != nil {
		t.Fatalf("reject: %v %v", err, d1.Error)
	}
	f.control("task.reopen")
	a1, r1 := f.assignment, f.run
	f.claim(fixtures.PrincipalB, 30)
	f.start()
	f.submit()
	f.decide("accept")
	if f.assignment == a1 || f.run == r1 || f.submission == s1 {
		t.Fatal("rework reused historical object")
	}
	h.state(f.id, "accepted")
	if number(h.read(f.id)["content_version"]) != 1 {
		t.Fatal("rework changed frozen content version")
	}
	replay, err := x.Do(context.Background(), request)
	if err != nil || replay.Error != nil || !replay.Replayed || !reflect.DeepEqual(replay.Data, d1.Data) {
		t.Fatal("old decision receipt did not replay historical result")
	}
	h.state(f.id, "accepted")
	p = f.params()
	p["submission_id"] = s1
	p["reason"] = "Late decision"
	p["evidence_refs"] = []any{}
	h.kind(h.call(fixtures.Acceptance, "task.accept", p), "INVALID_STATE")
	if h.countEvent(f.id, "decision.created") != 2 || h.countEvent(f.id, "submission.created") != 2 {
		t.Fatal("wrong immutable history counts")
	}
}

func TestV02RecoveryCannotBypassLifecycle(t *testing.T) {
	for _, state := range []string{"rejected", "accepted", "closed"} {
		t.Run(state, func(t *testing.T) {
			h := newHarness(t)
			f := h.active("safe_reopen", false)
			f.submit()
			if state == "rejected" {
				f.decide("reject")
			} else {
				f.decide("accept")
			}
			if state == "closed" {
				f.control("task.close")
			}
			p := f.params()
			p["reason"] = "Try reopen"
			want := "INVALID_STATE"
			if state == "rejected" {
				want = "POLICY_DENIED"
			}
			h.kind(h.call(fixtures.Owner, "task.reopen", p), want)
			p = f.params()
			p["claim_grant_id"] = f.grants[fixtures.PrincipalB]
			p["lease_seconds"] = 30
			h.kind(h.call(fixtures.PrincipalB, "task.claim", p), "INVALID_STATE")
			h.state(f.id, state)
		})
	}
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	f.submit()
	f.decide("reject")
	p := f.params()
	p["reason"] = "Acceptance alone cannot reopen"
	h.kind(h.call(fixtures.Acceptance, "task.reopen", p), "FORBIDDEN")
}

func TestV03SubmitReceiptSurvivesEndedLease(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	client := h.client(f.actor)
	request, err := client.Prepare("run.submit", f.submitParams())
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Do(context.Background(), request)
	if err != nil || first.Error != nil {
		t.Fatalf("submit: %v %v", err, first.Error)
	}
	f.submission = str(object(first.Data["task"])["pending_submission_id"])
	revision := h.read(f.id)["revision"]
	h.clock.Advance(time.Minute)
	h.maintain()
	replay, err := client.Do(context.Background(), request)
	if err != nil || replay.Error != nil || !replay.Replayed || !reflect.DeepEqual(first.Data, replay.Data) {
		t.Fatal("committed submission receipt not recovered")
	}
	if !reflect.DeepEqual(h.read(f.id)["revision"], revision) {
		t.Fatal("receipt replay changed revision")
	}
	h.kind(h.call(f.actor, "run.report", f.reportParams()), "LEASE_FENCED")
	f.decide("accept")
	if h.countEvent(f.id, "submission.created") != 1 || h.countEvent(f.id, "assignment.expired") != 0 {
		t.Fatal("submit duplicated or old timer expired completed assignment")
	}
}

func TestV06SubmitAndExpiryHaveSingleWinner(t *testing.T) {
	t.Run("submit_first", func(t *testing.T) {
		h := newHarness(t)
		f := h.active("safe_reopen", true)
		h.clock.Advance(29 * time.Second)
		f.submit()
		h.clock.Advance(time.Second)
		h.maintain()
		h.state(f.id, "submitted")
		if h.countEvent(f.id, "assignment.expired") != 0 {
			t.Fatal("completed assignment expired")
		}
	})
	for _, maintain := range []bool{false, true} {
		t.Run(map[bool]string{true: "expiry_first", false: "deadline_without_scanner"}[maintain], func(t *testing.T) {
			h := newHarness(t)
			f := h.active("safe_reopen", true)
			h.clock.Advance(30 * time.Second)
			if maintain {
				h.maintain()
			}
			r := h.call(f.actor, "run.submit", f.submitParams())
			if !maintain {
				h.kind(r, "STALE_REVISION")
				r = h.call(f.actor, "run.submit", f.submitParams())
			}
			h.kind(r, "LEASE_FENCED")
			h.state(f.id, "offered")
			run := h.record(f.id, "Run", f.run)
			if run["state"] != "ended" || run["end_reason"] != "lease_expired" {
				t.Fatal("expiry lost run termination fact")
			}
			h.maintain()
			if h.countEvent(f.id, "assignment.expired") != 1 || h.countEvent(f.id, "submission.created") != 0 {
				t.Fatal("expiry duplicated or stale submission accepted")
			}
		})
	}
}

func TestV13RecoveryGateAndExpiryCAS(t *testing.T) {
	for _, policy := range []string{"safe_reopen", "manual_reopen"} {
		t.Run(policy, func(t *testing.T) {
			h := newHarness(t)
			f := h.active(policy, true)
			p := f.params()
			p["claim_grant_id"] = f.grants[fixtures.PrincipalA]
			p["lease_seconds"] = 30
			h.clock.Advance(30 * time.Second)
			h.kind(h.call(fixtures.PrincipalA, "task.claim", p), "STALE_REVISION")
			if policy == "manual_reopen" {
				p = f.params()
				p["claim_grant_id"] = f.grants[fixtures.PrincipalA]
				p["lease_seconds"] = 30
				h.kind(h.call(fixtures.PrincipalA, "task.claim", p), "GATE_CLOSED")
				f.control("task.reopen")
			}
			old := f.assignment
			f.claim(fixtures.PrincipalA, 30)
			if old == f.assignment {
				t.Fatal("recovery reused assignment")
			}
			h.state(f.id, "claimed")
			if h.countEvent(f.id, "assignment.expired") != 1 || h.countEvent(f.id, "run.started") != 1 {
				t.Fatal("reclaim repeated expiry or started run implicitly")
			}
		})
	}
}

func TestV14StartupMaterializesExpiredLeaseOnce(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	h.clock.Advance(time.Minute)
	h.restart()
	h.state(f.id, "offered")
	revision := h.read(f.id)["revision"]
	h.maintain()
	h.restart()
	if !reflect.DeepEqual(revision, h.read(f.id)["revision"]) || h.countEvent(f.id, "assignment.expired") != 1 {
		t.Fatal("restart/scan repeated expiry")
	}
	h.kind(h.call(f.actor, "run.report", f.reportParams()), "LEASE_FENCED")
}

func TestV16RenewOnlyExtendsCurrentLiveAssignment(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	h.clock.Advance(10 * time.Second)
	p := f.execution()
	p["lease_seconds"] = 30
	h.ok(f.actor, "assignment.renew", p)
	assignment := h.record(f.id, "Assignment", f.assignment)
	if assignment["lease_expires_at"] != fixtures.Timestamp(fixtures.StartTime.Add(40*time.Second)) {
		t.Fatal("renew deadline wrong")
	}
	h.clock.Advance(20 * time.Second)
	h.maintain()
	h.state(f.id, "active")
	h.clock.Advance(10 * time.Second)
	h.maintain()
	p = f.execution()
	p["lease_seconds"] = 30
	h.kind(h.call(f.actor, "assignment.renew", p), "LEASE_FENCED")
	if h.countEvent(f.id, "assignment.renewed") != 1 {
		t.Fatal("renew events not exactly once")
	}
}

func TestV16CompletedAssignmentCannotRenew(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	f.submit()
	before := h.read(f.id)
	p := f.execution()
	p["lease_seconds"] = 30
	h.kind(h.call(f.actor, "assignment.renew", p), "LEASE_FENCED")
	if !reflect.DeepEqual(before, h.read(f.id)) || h.countEvent(f.id, "assignment.renewed") != 0 {
		t.Fatal("renew changed completed assignment")
	}
}

func TestV18PublishedContractIsFrozen(t *testing.T) {
	h := newHarness(t)
	f := h.create([]string{fixtures.AgentB}, "safe_reopen", true)
	contract := fixtures.Contract([]string{fixtures.AgentB}, "safe_reopen", true)
	contract["goal"] = "Explicit updated draft goal"
	p := f.params()
	p["contract"] = contract
	h.ok(fixtures.Owner, "task.updateDraft", p)
	f.publish()
	before := h.read(f.id)
	contract["goal"] = "Cannot mutate published goal"
	p = f.params()
	p["contract"] = contract
	h.kind(h.call(fixtures.Owner, "task.updateDraft", p), "INVALID_STATE")
	after := h.read(f.id)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed update changed frozen contract")
	}
}

func TestV19ControlTerminationAndTerminalGuards(t *testing.T) {
	for _, started := range []bool{false, true} {
		for _, method := range []string{"assignment.release", "assignment.revoke", "task.cancel"} {
			t.Run(method+map[bool]string{false: "_claimed", true: "_active"}[started], func(t *testing.T) {
				h := newHarness(t)
				f := h.create([]string{fixtures.AgentB}, "safe_reopen", true)
				f.publish()
				f.claim(fixtures.PrincipalB, 30)
				if started {
					f.start()
				}
				p := f.params()
				p["reason"] = "Explicit test control"
				actor := fixtures.Owner
				if method == "assignment.release" {
					actor = f.actor
					p["lease"] = f.lease
				}
				if method == "assignment.revoke" {
					p["assignment_id"] = f.assignment
				}
				h.ok(actor, method, p)
				wantTask, wantAssignment, wantReason := "offered", "released", "released"
				if method == "assignment.revoke" {
					wantAssignment, wantReason = "revoked", "revoked"
				}
				if method == "task.cancel" {
					wantTask, wantAssignment, wantReason = "cancelled", "cancelled", "task_cancelled"
				}
				h.state(f.id, wantTask)
				a := h.record(f.id, "Assignment", f.assignment)
				if a["state"] != wantAssignment || a["end_reason"] != wantReason {
					t.Fatal("wrong termination fact")
				}
				if started {
					r := h.record(f.id, "Run", f.run)
					if r["state"] != "ended" || r["end_reason"] != wantReason {
						t.Fatal("wrong run termination")
					}
				}
				p = f.execution()
				p["lease_seconds"] = 30
				h.kind(h.call(f.actor, "assignment.renew", p), "LEASE_FENCED")
			})
		}
	}
	for _, state := range []string{"submitted", "accepted", "closed"} {
		t.Run("old_execution_"+state, func(t *testing.T) {
			h := newHarness(t)
			f := h.active("safe_reopen", true)
			f.submit()
			if state != "submitted" {
				f.decide("accept")
			}
			if state == "closed" {
				f.control("task.close")
			}
			h.kind(h.call(f.actor, "run.report", f.reportParams()), "LEASE_FENCED")
			h.state(f.id, state)
		})
	}
}
