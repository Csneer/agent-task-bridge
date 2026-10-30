package blackbox

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"agent-task-bridge/tests/fixtures"
)

func TestDraftCannotEscapeCreationCapabilityEnvelope(t *testing.T) {
	for _, field := range []string{"eligible_agent_ids", "acceptance_principal_id"} {
		t.Run(field, func(t *testing.T) {
			h := newHarness(t)
			// Capability IDs are opaque, including strings resembling owner audit IDs.
			h.cfg.OwnerCapabilities[0].ID = "owner:" + fixtures.Owner
			h.cfg.OwnerCapabilities[0].EligibleAgentIDs = []string{fixtures.AgentB}
			h.importConfig()
			p := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
			p["initial_grants"] = append(p["initial_grants"].([]any), fixtures.Grant(fixtures.PrincipalA, nil, []string{"task.updateDraft"}, fixtures.StartTime.Add(24*time.Hour)))
			f := h.fromCreate(h.ok(fixtures.PrincipalA, "task.create", p))
			// Expanding the issuer's current capability must not expand an existing draft.
			h.cfg.OwnerCapabilities[0].EligibleAgentIDs = []string{fixtures.AgentA, fixtures.AgentB}
			h.cfg.OwnerCapabilities[0].AcceptancePrincipalIDs = []string{fixtures.Acceptance, fixtures.Reader}
			h.importConfig()
			h.restart()
			contract := fixtures.Contract([]string{fixtures.AgentB}, "safe_reopen", true)
			contract["goal"] = "A permitted draft edit inside the original envelope"
			p = f.params()
			p["contract"] = contract
			h.ok(fixtures.PrincipalA, "task.updateDraft", p)
			before := h.read(f.id)
			if field == "eligible_agent_ids" {
				contract[field] = []string{fixtures.AgentA, fixtures.AgentB}
			} else {
				contract[field] = fixtures.Reader
			}
			p = f.params()
			p["contract"] = contract
			h.kind(h.call(fixtures.PrincipalA, "task.updateDraft", p), "POLICY_DENIED")
			if !reflect.DeepEqual(before, h.read(f.id)) {
				t.Fatal("out-of-envelope edit changed the Task")
			}
		})
	}
}

func TestAuditUsesActionAuthorizationRatherThanReadGrant(t *testing.T) {
	h := newHarness(t)
	p := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
	for _, raw := range p["initial_grants"].([]any) {
		g := object(raw)
		if g["subject_principal_id"] == fixtures.PrincipalA {
			g["actions"] = []string{"task.read"}
		}
	}
	p["initial_grants"] = append(p["initial_grants"].([]any), fixtures.Grant(fixtures.PrincipalA, nil, []string{"task.publish"}, fixtures.StartTime.Add(24*time.Hour)))
	created := h.ok(fixtures.Owner, "task.create", p)
	f := h.fromCreate(created)
	var publishGrant string
	for _, raw := range list(created["records"]) {
		r := object(raw)
		if r["record_type"] == "Grant" {
			g := object(r["record"])
			if reflect.DeepEqual(g["actions"], []any{"task.publish"}) {
				publishGrant = str(g["grant_id"])
			}
		}
	}
	if publishGrant == "" {
		t.Fatal("fixture has no separate publish authority")
	}
	h.ok(fixtures.PrincipalA, "task.publish", f.params())
	f.claim(fixtures.PrincipalB, 30)
	f.start()
	h.ok(fixtures.PrincipalB, "run.report", f.reportParams())
	open := h.ok(fixtures.Owner, "events.open", map[string]any{"scope": map[string]any{}})
	page := h.ok(fixtures.Owner, "events.read", map[string]any{"cursor": open["cursor"], "limit": 100})
	wanted := map[string][]any{
		"task.publish": {publishGrant},
		"task.claim":   {f.grants[fixtures.PrincipalB], "binding_" + fixtures.AgentB + "_1"},
		"run.start":    {f.grants[fixtures.PrincipalB], "binding_" + fixtures.AgentB + "_1"},
		"run.report":   {f.grants[fixtures.PrincipalB], "binding_" + fixtures.AgentB + "_1"},
	}
	for _, raw := range list(page["batches"]) {
		b := object(raw)
		action := str(object(b["audit"])["action"])
		if ids, ok := wanted[action]; ok {
			actual := list(object(b["actor"])["authorization_ids"])
			sort.Slice(actual, func(i, j int) bool { return str(actual[i]) < str(actual[j]) })
			sort.Slice(ids, func(i, j int) bool { return str(ids[i]) < str(ids[j]) })
			if !reflect.DeepEqual(actual, ids) {
				t.Fatalf("%s audit authority = %v; want %v", action, object(b["actor"])["authorization_ids"], ids)
			}
			delete(wanted, action)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("missing audited actions: %v", wanted)
	}
}
