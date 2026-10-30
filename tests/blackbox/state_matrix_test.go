package blackbox

import (
	"reflect"
	"testing"
	"time"

	"github.com/Csneer/agent-task-bridge/tests/fixtures"
)

func fixtureAtState(h *harness, state string) *taskFixture {
	f := h.create([]string{fixtures.AgentB}, "safe_reopen", true)
	f.actor = fixtures.PrincipalB
	if state == "draft" {
		return f
	}
	f.publish()
	if state == "offered" {
		return f
	}
	f.claim(fixtures.PrincipalB, 30)
	if state == "claimed" {
		return f
	}
	f.start()
	if state == "active" {
		return f
	}
	if state == "cancelled" {
		f.control("task.cancel")
		return f
	}
	f.submit()
	if state == "submitted" {
		return f
	}
	if state == "rejected" {
		f.decide("reject")
		return f
	}
	f.decide("accept")
	if state == "accepted" {
		return f
	}
	f.control("task.close")
	return f
}

func TestV19ControlMethodsStateMatrix(t *testing.T) {
	states := []string{"draft", "offered", "claimed", "active", "submitted", "accepted", "rejected", "closed", "cancelled"}
	allowed := map[string]map[string]bool{
		"task.updateDraft": {"draft": true}, "task.publish": {"draft": true},
		"assignment.release": {"claimed": true, "active": true}, "assignment.revoke": {"claimed": true, "active": true},
		"assignment.renew": {"claimed": true, "active": true},
		"task.reopen":      {"rejected": true},
		"task.cancel":      {"draft": true, "offered": true, "claimed": true, "active": true, "submitted": true},
		"task.close":       {"accepted": true, "rejected": true},
	}
	for method, validStates := range allowed {
		for _, state := range states {
			t.Run(method+"/"+state, func(t *testing.T) {
				h := newHarness(t)
				f := fixtureAtState(h, state)
				before := h.read(f.id)
				p := f.params()
				actor := fixtures.Owner
				switch method {
				case "task.updateDraft":
					p["contract"] = fixtures.Contract([]string{fixtures.AgentB}, "safe_reopen", true)
				case "task.publish":
				case "assignment.release", "assignment.renew":
					actor = f.actor
					p["lease"] = f.lease
					if f.lease == nil {
						p["lease"] = map[string]any{"assignment_id": "assignment_nonexistent", "generation": 1, "token": fixtures.Token()}
					}
					if method == "assignment.release" {
						p["reason"] = "Matrix release"
					} else {
						p["lease_seconds"] = 60
					}
				case "assignment.revoke":
					p["assignment_id"] = f.assignment
					if f.assignment == "" {
						p["assignment_id"] = "assignment_nonexistent"
					}
					p["reason"] = "Matrix revoke"
				default:
					p["reason"] = "Matrix control"
				}
				r := h.call(actor, method, p)
				if validStates[state] {
					if r.Status != 200 || r.Error != nil {
						t.Fatalf("allowed %s in %s rejected: HTTP %d error=%v", method, state, r.Status, r.Error)
					}
					if number(h.read(f.id)["revision"]) != number(before["revision"])+1 {
						t.Fatal("allowed transition did not advance revision once")
					}
				} else {
					if r.Status != 200 || r.Error == nil {
						t.Fatalf("forbidden %s in %s accepted", method, state)
					}
					if r.Error.Code == -32602 {
						t.Fatal("matrix fixture invalid instead of exercising state guard")
					}
					if !reflect.DeepEqual(before, h.read(f.id)) {
						t.Fatal("rejected transition mutated task")
					}
				}
			})
		}
	}
}

func TestGrantManagementDoesNotChangeTerminalLifecycle(t *testing.T) {
	for _, state := range []string{"draft", "offered", "claimed", "active", "submitted", "accepted", "rejected", "closed", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			h := newHarness(t)
			f := fixtureAtState(h, state)
			p := f.params()
			for k, v := range fixtures.Grant(fixtures.Reader, nil, []string{"task.read"}, fixtures.StartTime.Add(20*time.Hour)) {
				p[k] = v
			}
			data := h.ok(fixtures.Owner, "grant.issue", p)
			grantID := ""
			for _, raw := range list(data["records"]) {
				r := object(raw)
				if r["record_type"] == "Grant" {
					grantID = str(object(r["record"])["grant_id"])
				}
			}
			if grantID == "" {
				t.Fatal("grant.issue returned no Grant")
			}
			h.state(f.id, state)
			p = f.params()
			p["grant_id"] = grantID
			p["reason"] = "Revoke read grant only"
			h.ok(fixtures.Owner, "grant.revoke", p)
			h.state(f.id, state)
		})
	}
}
