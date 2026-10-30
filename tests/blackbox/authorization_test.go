package blackbox

import (
	"context"
	"testing"
	"time"

	"github.com/Csneer/agent-task-bridge/tests/fixtures"
)

func TestV04ReceiptRequiresCurrentReadAndAuthentication(t *testing.T) {
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
	submission := str(object(first.Data["task"])["pending_submission_id"])
	var ids []string
	for _, record := range h.allSnapshot(fixtures.Owner) {
		if record["record_type"] == "Grant" {
			g := object(record["record"])
			if g["task_id"] == f.id && g["subject_principal_id"] == fixtures.PrincipalB {
				ids = append(ids, str(g["grant_id"]))
			}
		}
	}
	for _, id := range ids {
		p := f.params()
		p["grant_id"] = id
		p["reason"] = "Remove B visibility"
		h.ok(fixtures.Owner, "grant.revoke", p)
	}
	r, err := client.Do(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	h.kind(r, "NOT_FOUND")
	if h.record(f.id, "Submission", submission)["submission_id"] != submission {
		t.Fatal("read revocation deleted submission")
	}
	p := f.params()
	for k, v := range fixtures.Grant(fixtures.PrincipalB, nil, []string{"task.read"}, fixtures.StartTime.Add(24*time.Hour)) {
		p[k] = v
	}
	h.ok(fixtures.Owner, "grant.issue", p)
	r, err = client.Do(context.Background(), request)
	if err != nil || r.Error != nil || !r.Replayed {
		t.Fatal("restored read could not recover old receipt")
	}
	now := fixtures.Timestamp(h.clock.Now())
	for i := range h.cfg.Credentials {
		if h.cfg.Credentials[i].PrincipalID == fixtures.PrincipalB {
			h.cfg.Credentials[i].RevokedAt = &now
		}
	}
	h.importConfig()
	r, err = client.Do(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != 401 || r.TransportCode != "UNAUTHENTICATED" {
		t.Fatal("revoked credential could read receipt")
	}
}

func TestV07ChildCreationChecksAllPermissionsAtomically(t *testing.T) {
	for _, missing := range []string{"grant_scope", "task.create", "task.link"} {
		t.Run(missing, func(t *testing.T) {
			h := newHarness(t)
			parent := h.create([]string{fixtures.AgentA}, "safe_reopen", true)
			parent.publish()
			before := number(h.read(parent.id)["revision"])
			request := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
			request["parent_task_id"] = parent.id
			if missing == "grant_scope" {
				h.cfg.OwnerCapabilities[0].GrantActions = []string{"task.read", "task.claim", "submission.decide", "task.publish", "task.link"}
				g := fixtures.Grant(fixtures.PrincipalB, nil, []string{"task.cancel"}, fixtures.StartTime.Add(24*time.Hour))
				request["initial_grants"] = append(request["initial_grants"].([]any), g)
			} else {
				var keep []string
				for _, a := range h.cfg.OwnerCapabilities[0].Actions {
					if a != missing {
						keep = append(keep, a)
					}
				}
				h.cfg.OwnerCapabilities[0].Actions = keep
				if missing == "task.link" {
					for _, record := range h.allSnapshot(fixtures.Owner) {
						if record["record_type"] == "Grant" {
							g := object(record["record"])
							if g["task_id"] == parent.id && g["subject_principal_id"] == fixtures.PrincipalA && g["agent_id"] == nil {
								p := parent.params()
								p["grant_id"] = g["grant_id"]
								p["reason"] = "Remove parent link"
								h.ok(fixtures.Owner, "grant.revoke", p)
							}
						}
					}
					before = number(h.read(parent.id)["revision"])
				}
			}
			h.importConfig()
			r := h.call(fixtures.PrincipalA, "task.createChild", request)
			if r.Error == nil || (r.Error.Data.Kind != "FORBIDDEN" && r.Error.Data.Kind != "POLICY_DENIED" && r.Error.Data.Kind != "NOT_FOUND") {
				t.Fatalf("invalid child authorization accepted: %v", r.Error)
			}
			count := 0
			for _, record := range h.allSnapshot(fixtures.Owner) {
				if record["record_type"] == "Task" {
					count++
				}
				if record["record_type"] == "Relation" {
					t.Fatal("failed child left Relation")
				}
			}
			if count != 1 || number(h.read(parent.id)["revision"]) != before {
				t.Fatal("failed child left partial facts or changed parent")
			}
		})
	}
}

func (h *harness) child(parent *taskFixture) *taskFixture {
	p := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
	p["parent_task_id"] = parent.id
	f := h.fromCreate(h.ok(fixtures.PrincipalA, "task.createChild", p))
	f.publish()
	return f
}

func TestV11ChildExpiryOrRejectionDoesNotChangeParent(t *testing.T) {
	for _, outcome := range []string{"expiry", "rejected"} {
		t.Run(outcome, func(t *testing.T) {
			h := newHarness(t)
			p := h.create([]string{fixtures.AgentA}, "safe_reopen", true)
			p.publish()
			p.claim(fixtures.PrincipalA, 120)
			p.start()
			c := h.child(p)
			c.claim(fixtures.PrincipalB, 30)
			c.start()
			revision := number(h.read(p.id)["revision"])
			if outcome == "expiry" {
				h.clock.Advance(30 * time.Second)
				h.maintain()
				h.state(c.id, "offered")
				req := c.params()
				req["claim_grant_id"] = c.grants[fixtures.PrincipalB]
				req["lease_seconds"] = 30
				h.kind(h.call(fixtures.PrincipalA, "task.claim", req), "FORBIDDEN")
			} else {
				c.submit()
				c.decide("reject")
				h.state(c.id, "rejected")
			}
			h.state(p.id, "active")
			if number(h.read(p.id)["revision"]) != revision {
				t.Fatal("child mutated parent revision")
			}
		})
	}
}

func TestV12ParentExpiryPreservesIndependentChildAuthority(t *testing.T) {
	h := newHarness(t)
	p := h.create([]string{fixtures.AgentA}, "safe_reopen", true)
	p.publish()
	p.claim(fixtures.PrincipalA, 30)
	p.start()
	c := h.child(p)
	c.claim(fixtures.PrincipalB, 120)
	c.start()
	h.clock.Advance(30 * time.Second)
	h.maintain()
	h.state(p.id, "offered")
	h.kind(h.call(p.actor, "run.report", p.reportParams()), "LEASE_FENCED")
	h.ok(c.actor, "run.report", c.reportParams())
	c.submit()
	c.decide("accept")
	h.state(c.id, "accepted")
	// A still has independent OwnerCapability; no current parent lease is needed.
	other := h.child(p)
	h.state(other.id, "offered")
}

func TestV20IdentityCannotBeReplacedByToken(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	p := f.reportParams()
	p["as_agent_id"] = fixtures.AgentA
	h.kind(h.call(fixtures.PrincipalB, "run.report", p), "FORBIDDEN")
	p = f.reportParams()
	r := h.call(fixtures.PrincipalA, "run.report", p)
	h.kind(r, "FORBIDDEN")
}

func TestV23NaturalBindingExpiryUsesLeaseExpiryReason(t *testing.T) {
	h := newHarness(t)
	for i := range h.cfg.Bindings {
		if h.cfg.Bindings[i].AgentID == fixtures.AgentB {
			h.cfg.Bindings[i].BindingID = "binding_B_short"
			h.cfg.Bindings[i].ExpiresAt = fixtures.Timestamp(fixtures.StartTime.Add(15 * time.Second))
		}
	}
	h.importConfig()
	f := h.active("safe_reopen", true)
	a := h.record(f.id, "Assignment", f.assignment)
	if a["lease_expires_at"] != fixtures.Timestamp(fixtures.StartTime.Add(15*time.Second)) {
		t.Fatal("lease not capped by binding expiry")
	}
	h.clock.Advance(15 * time.Second)
	h.maintain()
	a = h.record(f.id, "Assignment", f.assignment)
	if a["state"] != "expired" || a["end_reason"] != "lease_expired" {
		t.Fatal("natural binding expiry did not use lease-expiry precedence")
	}
	for i := range h.cfg.Bindings {
		if h.cfg.Bindings[i].AgentID == fixtures.AgentB {
			h.cfg.Bindings[i].BindingID = "binding_B_after_expiry"
			h.cfg.Bindings[i].NotBefore = fixtures.Timestamp(h.clock.Now())
			h.cfg.Bindings[i].ExpiresAt = fixtures.Timestamp(fixtures.StartTime.Add(time.Hour))
		}
	}
	h.importConfig()
	h.kind(h.call(f.actor, "run.report", f.reportParams()), "FORBIDDEN")
	old := f.assignment
	f.claim(fixtures.PrincipalB, 30)
	if f.assignment == old {
		t.Fatal("new binding resumed expired assignment")
	}
}

func TestV23RevokedBindingNeverResurrects(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	oldBinding := str(h.record(f.id, "Assignment", f.assignment)["binding_id"])
	for i := range h.cfg.Agents {
		if h.cfg.Agents[i].AgentID == fixtures.AgentB {
			h.cfg.Agents[i].Enabled = false
		}
	}
	h.importConfig()
	for i := range h.cfg.Agents {
		if h.cfg.Agents[i].AgentID == fixtures.AgentB {
			h.cfg.Agents[i].Enabled = true
		}
	}
	for i := range h.cfg.Bindings {
		if h.cfg.Bindings[i].BindingID == oldBinding {
			h.cfg.Bindings[i].BindingID = "binding_B_reenabled"
		}
	}
	h.importConfig()
	r := h.call(f.actor, "run.report", f.reportParams())
	if r.Error == nil {
		t.Fatal("old assignment resurrected before maintenance")
	}
	h.maintain()
	a := h.record(f.id, "Assignment", f.assignment)
	if a["state"] != "revoked" || a["end_reason"] != "authorization_revoked" {
		t.Fatal("wrong binding revocation termination")
	}
	h.state(f.id, "offered")
	old := f.assignment
	f.claim(fixtures.PrincipalB, 30)
	if f.assignment == old || h.record(f.id, "Assignment", f.assignment)["binding_id"] == oldBinding {
		t.Fatal("new claim reused revoked binding")
	}
	if h.countEvent(f.id, "assignment.revoked") != 1 {
		t.Fatal("binding revocation duplicated")
	}
}

func TestV24NaturalAuthorizationBoundaryAndReceiptIsolation(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	// A future authorization boundary must expire the context even without an epoch write.
	p := f.params()
	for k, v := range fixtures.Grant(fixtures.Reader, nil, []string{"task.read"}, fixtures.StartTime.Add(time.Hour)) {
		p[k] = v
	}
	p["not_before"] = fixtures.Timestamp(fixtures.StartTime.Add(10 * time.Second))
	h.ok(fixtures.Owner, "grant.issue", p)
	open := h.ok(fixtures.Reader, "snapshot.open", map[string]any{"scope": map[string]any{}})
	events := h.ok(fixtures.Reader, "events.open", map[string]any{"scope": map[string]any{}})
	h.clock.Advance(10 * time.Second)
	h.kind(h.call(fixtures.Reader, "snapshot.page", map[string]any{"snapshot_id": open["snapshot_id"], "page_cursor": open["page_cursor"], "limit": 100}), "REINITIALIZE_REQUIRED")
	h.kind(h.call(fixtures.Reader, "events.read", map[string]any{"cursor": events["cursor"], "limit": 100}), "REINITIALIZE_REQUIRED")
	h.kind(h.call(fixtures.Owner, "receipt.get", map[string]any{"idempotency_key": fixtures.Key()}), "RECEIPT_NOT_FOUND")
	p = f.params()
	p["grant_id"] = f.grants[fixtures.PrincipalB]
	p["reason"] = "Revoke execution grant"
	h.ok(fixtures.Owner, "grant.revoke", p)
	h.kind(h.call(f.actor, "run.report", f.reportParams()), "FORBIDDEN")
}
