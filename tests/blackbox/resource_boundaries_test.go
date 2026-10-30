package blackbox

import (
	"agent-task-bridge/internal/config"
	"agent-task-bridge/tests/fixtures"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPagingLimits(t *testing.T) {
	h := newHarness(t)
	h.active("safe_reopen", true)
	for _, limit := range []int{0, 1, 100, 101} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			snapshot := h.ok(fixtures.Owner, "snapshot.open", map[string]any{"scope": map[string]any{}})
			events := h.ok(fixtures.Owner, "events.open", map[string]any{"scope": map[string]any{}})
			inputs := map[string]map[string]any{
				"task.discover": {"limit": limit},
				"snapshot.page": {"snapshot_id": snapshot["snapshot_id"], "page_cursor": snapshot["page_cursor"], "limit": limit},
				"events.read":   {"cursor": events["cursor"], "limit": limit},
			}
			for method, p := range inputs {
				actor := fixtures.Owner
				if method == "task.discover" {
					actor = fixtures.PrincipalB
				}
				if limit == 0 || limit == 101 {
					h.kind(h.call(actor, method, p), "INVALID_PARAMS")
				} else {
					h.ok(actor, method, p)
				}
			}
			// Finish any incomplete context before the next boundary case.
			h.ok(fixtures.Owner, "snapshot.page", map[string]any{"snapshot_id": snapshot["snapshot_id"], "page_cursor": snapshot["page_cursor"], "limit": 100})
		})
	}
}

func TestInitialGrantAndLeaseLimits(t *testing.T) {
	h := newHarness(t)
	for _, count := range []int{16, 17} {
		p := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
		grants := []any{}
		for i := 0; i < count; i++ {
			grants = append(grants, fixtures.Grant(fixtures.Reader, nil, []string{"task.read"}, fixtures.StartTime.Add(time.Hour)))
		}
		p["initial_grants"] = grants
		if count == 16 {
			h.ok(fixtures.Owner, "task.create", p)
		} else {
			h.kind(h.call(fixtures.Owner, "task.create", p), "INVALID_PARAMS")
		}
	}
	for _, seconds := range []int{4, 5, 300, 301} {
		f := h.create([]string{fixtures.AgentB}, "safe_reopen", true)
		f.publish()
		p := f.params()
		p["claim_grant_id"] = f.grants[fixtures.PrincipalB]
		p["lease_seconds"] = seconds
		if seconds == 4 || seconds == 301 {
			h.kind(h.call(fixtures.PrincipalB, "task.claim", p), "INVALID_PARAMS")
		} else {
			h.ok(fixtures.PrincipalB, "task.claim", p)
		}
	}
	f := h.create([]string{fixtures.AgentB}, "safe_reopen", true)
	contract := fixtures.Contract([]string{fixtures.AgentB}, "safe_reopen", true)
	contract["lease_policy"] = map[string]any{"min_seconds": 10, "max_seconds": 20}
	p := f.params()
	p["contract"] = contract
	h.ok(fixtures.Owner, "task.updateDraft", p)
	f.publish()
	for _, seconds := range []int{9, 21} {
		p = f.params()
		p["claim_grant_id"] = f.grants[fixtures.PrincipalB]
		p["lease_seconds"] = seconds
		h.kind(h.call(fixtures.PrincipalB, "task.claim", p), "POLICY_DENIED")
	}
}

func TestSnapshotExactExpiryBoundary(t *testing.T) {
	h := newHarness(t)
	h.active("safe_reopen", true)
	s := h.ok(fixtures.Owner, "snapshot.open", map[string]any{"scope": map[string]any{}})
	p := map[string]any{"snapshot_id": s["snapshot_id"], "page_cursor": s["page_cursor"], "limit": 1}
	h.clock.Advance(299 * time.Second)
	h.ok(fixtures.Owner, "snapshot.page", p)
	h.clock.Advance(time.Second)
	h.kind(h.call(fixtures.Owner, "snapshot.page", p), "REINITIALIZE_REQUIRED")
}

func TestAuthoritySnapshotQuota(t *testing.T) {
	h := newHarness(t)
	ids := []string{}
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("principal_quota_%d", i)
		ids = append(ids, id)
		token := fixtures.Token()
		h.tokens[id] = token
		h.cfg.Principals = append(h.cfg.Principals, config.Principal{PrincipalID: id, Enabled: true})
		h.cfg.Credentials = append(h.cfg.Credentials, config.Credential{CredentialID: fmt.Sprintf("credential_quota_%d", i), PrincipalID: id, Token: token, NotBefore: fixtures.Timestamp(fixtures.StartTime.Add(-time.Hour)), ExpiresAt: fixtures.Timestamp(fixtures.StartTime.Add(time.Hour))})
	}
	h.importConfig()
	var first map[string]any
	for i := 0; i < 8; i++ {
		for j := 0; j < 2; j++ {
			s := h.ok(ids[i], "snapshot.open", map[string]any{"scope": map[string]any{}})
			if i == 0 && j == 0 {
				first = s
			}
		}
	}
	h.kind(h.call(ids[8], "snapshot.open", map[string]any{"scope": map[string]any{}}), "RESOURCE_LIMIT")
	h.ok(ids[0], "snapshot.page", map[string]any{"snapshot_id": first["snapshot_id"], "page_cursor": first["page_cursor"], "limit": 100})
	h.ok(ids[8], "snapshot.open", map[string]any{"scope": map[string]any{}})
}

func TestSnapshotTenThousandRecords(t *testing.T) {
	h := newHarness(t)
	// 588 Tasks with 16 grants each = 9996 records, then four grant-free Tasks.
	for i := 0; i < 592; i++ {
		p := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
		gs := []any{}
		if i < 588 {
			for j := 0; j < 16; j++ {
				gs = append(gs, fixtures.Grant(fixtures.Reader, nil, []string{"task.read"}, fixtures.StartTime.Add(time.Hour)))
			}
		}
		p["initial_grants"] = gs
		h.ok(fixtures.Owner, "task.create", p)
	}
	records := h.allSnapshot(fixtures.Owner)
	if len(records) != 10000 {
		t.Fatalf("records=%d", len(records))
	}
	p := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
	p["initial_grants"] = []any{}
	h.ok(fixtures.Owner, "task.create", p)
	h.kind(h.call(fixtures.Owner, "snapshot.open", map[string]any{"scope": map[string]any{}}), "RESOURCE_LIMIT")
}

func TestSnapshotThirtyTwoMiB(t *testing.T) {
	h := newHarness(t)
	payload := []any{}
	for i := 0; i < 30; i++ {
		payload = append(payload, strings.Repeat("x", 8192))
	}
	total := 0
	for i := 0; i < 160; i++ {
		p := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
		p["initial_grants"] = []any{}
		object(p["contract"])["extensions"] = map[string]any{"test.payload": payload}
		result := h.ok(fixtures.Owner, "task.create", p)
		raw, err := json.Marshal(map[string]any{"record_type": "Task", "record": result["task"]})
		if err != nil {
			t.Fatal(err)
		}
		previous := total
		total += len(raw)
		if total > 33554432 {
			if previous > 33554432 {
				t.Fatal("did not check first boundary crossing")
			}
			h.kind(h.call(fixtures.Owner, "snapshot.open", map[string]any{"scope": map[string]any{}}), "RESOURCE_LIMIT")
			// Trim only string bytes, preserving JSON structure, to exercise equality.
			excess := total - 33554432
			for j := range payload {
				value := str(payload[j])
				cut := min(excess, len(value))
				payload[j] = value[:len(value)-cut]
				excess -= cut
			}
			task := object(result["task"])
			update := map[string]any{"task_id": task["task_id"], "expected_revision": task["revision"], "idempotency_key": fixtures.Key(), "contract": p["contract"]}
			result = h.ok(fixtures.Owner, "task.updateDraft", update)
			raw, err = json.Marshal(map[string]any{"record_type": "Task", "record": result["task"]})
			if err != nil || previous+len(raw) != 33554432 {
				t.Fatalf("exact byte fixture=%d, error=%v", previous+len(raw), err)
			}
			if records := h.allSnapshot(fixtures.Owner); len(records) != i+1 {
				t.Fatal("exact 32 MiB snapshot incomplete")
			}
			payload[0] = str(payload[0]) + "x"
			update["expected_revision"] = object(result["task"])["revision"]
			update["idempotency_key"] = fixtures.Key()
			h.ok(fixtures.Owner, "task.updateDraft", update)
			h.kind(h.call(fixtures.Owner, "snapshot.open", map[string]any{"scope": map[string]any{}}), "RESOURCE_LIMIT")
			return
		}
		// Check the final sub-limit snapshot without materializing it on every insertion.
		if total+len(raw) > 33554432 {
			records := h.allSnapshot(fixtures.Owner)
			if len(records) != i+1 {
				t.Fatal("sub-limit snapshot incomplete")
			}
		}
	}
	t.Fatal("fixture failed to reach 32 MiB")
}
