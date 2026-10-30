package blackbox

import (
	"context"
	"reflect"
	"testing"
	"time"

	"agent-task-bridge/bridge"
	"agent-task-bridge/tests/fixtures"
)

type callResult struct {
	response Response
	err      error
}

func asyncCall(client *Client, p Prepared) <-chan callResult {
	out := make(chan callResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, e := client.Do(ctx, p)
		out <- callResult{r, e}
	}()
	return out
}

func TestV05FinalTransactionRechecksAfterRevoke(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	p := f.submitParams()
	gate := NewGate("before_command_tx", "run.submit", str(p["idempotency_key"]))
	defer gate.Release()
	h.setHook(func(e bridge.HookEvent) error { gate.Hook(e.Point, e.Method, e.Key); return nil })
	client := h.client(f.actor)
	request, err := client.Prepare("run.submit", p)
	if err != nil {
		t.Fatal(err)
	}
	pending := asyncCall(client, request)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gate.Await(ctx); err != nil {
		t.Fatal(err)
	}
	revoke := f.params()
	revoke["assignment_id"] = f.assignment
	revoke["reason"] = "Revoke after external precheck"
	h.ok(fixtures.Owner, "assignment.revoke", revoke)
	gate.Release()
	result := <-pending
	if result.err != nil {
		t.Fatal(result.err)
	}
	h.kind(result.response, "STALE_REVISION")
	h.kind(h.call(f.actor, "run.submit", f.submitParams()), "LEASE_FENCED")
	if h.countEvent(f.id, "submission.created") != 0 {
		t.Fatal("revoked submit committed")
	}
}

func TestV15ConcurrentClaimAndIdenticalReplay(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{true: "same_key", false: "different_agents"}[same], func(t *testing.T) {
			h := newHarness(t)
			f := h.create([]string{fixtures.AgentA, fixtures.AgentB}, "safe_reopen", true)
			f.publish()
			p := f.params()
			p["claim_grant_id"] = f.grants[fixtures.PrincipalB]
			p["lease_seconds"] = 30
			cb := h.client(fixtures.PrincipalB)
			pb, err := cb.Prepare("task.claim", p)
			if err != nil {
				t.Fatal(err)
			}
			other, otherRequest := cb, pb
			if !same {
				other = h.client(fixtures.PrincipalA)
				p["claim_grant_id"] = f.grants[fixtures.PrincipalA]
				p["idempotency_key"] = fixtures.Key()
				otherRequest, err = other.Prepare("task.claim", p)
				if err != nil {
					t.Fatal(err)
				}
			}
			one, two := asyncCall(cb, pb), asyncCall(other, otherRequest)
			a, b := <-one, <-two
			if a.err != nil || b.err != nil {
				t.Fatalf("claim transport: %v %v", a.err, b.err)
			}
			if same {
				if a.response.Error != nil || b.response.Error != nil || !reflect.DeepEqual(a.response.Data, b.response.Data) || a.response.Replayed == b.response.Replayed {
					t.Fatal("same-key claims did not converge")
				}
			} else {
				if (a.response.Error == nil) == (b.response.Error == nil) {
					t.Fatal("concurrent claims lack unique winner")
				}
				loser := a.response
				if loser.Error == nil {
					loser = b.response
				}
				h.kind(loser, "STALE_REVISION")
			}
			if h.countEvent(f.id, "assignment.claimed") != 1 {
				t.Fatal("duplicate current assignment created")
			}
		})
	}
}

func TestV16RenewCannotWinAfterCommittedRevocation(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	p := f.execution()
	p["lease_seconds"] = 60
	gate := NewGate("before_command_tx", "assignment.renew", str(p["idempotency_key"]))
	defer gate.Release()
	h.setHook(func(e bridge.HookEvent) error { gate.Hook(e.Point, e.Method, e.Key); return nil })
	client := h.client(f.actor)
	request, err := client.Prepare("assignment.renew", p)
	if err != nil {
		t.Fatal(err)
	}
	pending := asyncCall(client, request)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gate.Await(ctx); err != nil {
		t.Fatal(err)
	}
	revoke := f.params()
	revoke["assignment_id"] = f.assignment
	revoke["reason"] = "Revoke while renew is pending"
	h.ok(fixtures.Owner, "assignment.revoke", revoke)
	before := h.read(f.id)
	gate.Release()
	result := <-pending
	if result.err != nil {
		t.Fatal(result.err)
	}
	h.kind(result.response, "STALE_REVISION")
	p = f.execution()
	p["lease_seconds"] = 60
	h.kind(h.call(f.actor, "assignment.renew", p), "LEASE_FENCED")
	if !reflect.DeepEqual(before, h.read(f.id)) || h.countEvent(f.id, "assignment.renewed") != 0 {
		t.Fatal("pending renew resurrected revoked assignment")
	}
}

func TestV17IdempotencyRejectsChangedIntent(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	p := f.reportParams()
	first := h.ok(f.actor, "run.report", p)
	again := h.call(f.actor, "run.report", p)
	if again.Error != nil || !again.Replayed || !reflect.DeepEqual(first, again.Data) {
		t.Fatal("exact report replay failed")
	}
	p["expected_revision"] = h.read(f.id)["revision"]
	h.kind(h.call(f.actor, "run.report", p), "IDEMPOTENCY_CONFLICT")
	if h.countEvent(f.id, "run.reported") != 1 {
		t.Fatal("same key changed business facts")
	}
}

func TestV17SingleRejectionDoesNotResolveAnotherPendingCopy(t *testing.T) {
	h := newHarness(t)
	originalActions := append([]string(nil), h.cfg.OwnerCapabilities[0].Actions...)
	h.cfg.OwnerCapabilities[0].Actions = []string{"task.link", "grant.issue", "grant.revoke"}
	h.importConfig()
	p := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
	gate := NewGate("before_command_tx", "task.create", str(p["idempotency_key"]))
	defer gate.Release()
	h.setHook(func(e bridge.HookEvent) error { gate.Hook(e.Point, e.Method, e.Key); return nil })
	client := h.client(fixtures.PrincipalA)
	request, err := client.Prepare("task.create", p)
	if err != nil {
		t.Fatal(err)
	}
	pending := asyncCall(client, request)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gate.Await(ctx); err != nil {
		t.Fatal(err)
	}
	// One duplicate is rejected while another copy remains demonstrably in flight.
	rejected, err := client.Do(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	h.kind(rejected, "FORBIDDEN")
	h.cfg.OwnerCapabilities[0].Actions = originalActions
	h.importConfig()
	gate.Release()
	committed := <-pending
	if committed.err != nil || committed.response.Error != nil {
		t.Fatalf("pending copy: %v %v", committed.err, committed.response.Error)
	}
	replayed, err := client.Do(ctx, request)
	if err != nil || replayed.Error != nil || !replayed.Replayed {
		t.Fatal("original key did not converge to committed copy")
	}
	tasks := 0
	for _, r := range h.allSnapshot(fixtures.Owner) {
		if r["record_type"] == "Task" {
			tasks++
		}
	}
	if tasks != 1 {
		t.Fatalf("same intent produced %d tasks", tasks)
	}
}

func TestV21AcceptanceAuthorityAndConcurrentDecision(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	f.submit()
	p := f.params()
	p["submission_id"] = f.submission
	p["reason"] = "Review complete"
	p["evidence_refs"] = []any{}
	h.kind(h.call(fixtures.Owner, "task.accept", p), "FORBIDDEN")
	client := h.client(fixtures.Acceptance)
	accept, err := client.Prepare("task.accept", p)
	if err != nil {
		t.Fatal(err)
	}
	p["idempotency_key"] = fixtures.Key()
	reject, err := client.Prepare("task.reject", p)
	if err != nil {
		t.Fatal(err)
	}
	one, two := asyncCall(client, accept), asyncCall(client, reject)
	a, b := <-one, <-two
	if a.err != nil || b.err != nil {
		t.Fatalf("decision transport: %v %v", a.err, b.err)
	}
	if (a.response.Error == nil) == (b.response.Error == nil) {
		t.Fatal("decisions lack unique winner")
	}
	loser := a.response
	if loser.Error == nil {
		loser = b.response
	}
	h.kind(loser, "STALE_REVISION")
	if h.countEvent(f.id, "decision.created") != 1 {
		t.Fatal("second final Decision created")
	}
	if h.record(f.id, "Run", f.run)["state"] != "submitted" {
		t.Fatal("Decision rewrote Run")
	}
	p = f.params()
	p["submission_id"] = f.submission
	p["reason"] = "Try another final decision"
	p["evidence_refs"] = []any{}
	h.kind(h.call(fixtures.Acceptance, "task.accept", p), "INVALID_STATE")
}
