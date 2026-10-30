package blackbox

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"agent-task-bridge/tests/fixtures"
)

func TestEscapedLargeContractLeavesMaintenanceCapacity(t *testing.T) {
	h := newHarness(t)
	create := func(n int) Response {
		p := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
		parts := []any{}
		for n > 0 {
			count := min(n, 8192)
			parts = append(parts, strings.Repeat("<", count))
			n -= count
		}
		object(p["contract"])["extensions"] = map[string]any{"test.payload": parts}
		client := h.client(fixtures.Owner)
		prepared, err := client.Prepare("task.create", p)
		if err != nil {
			t.Fatal(err)
		}
		// Legal JSON need not escape '<'. Storage/output JSON does, so request
		// capacity alone cannot guarantee space for future mandatory events.
		prepared.body = bytes.ReplaceAll(prepared.body, []byte(`\u003c`), []byte("<"))
		if len(prepared.body) > 262144 {
			t.Fatal("invalid capacity fixture")
		}
		r, err := client.Do(context.Background(), prepared)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	h.kind(create(174000), "RESOURCE_LIMIT")
	if len(h.allSnapshot(fixtures.Owner)) != 0 {
		t.Fatal("rejected capacity create left facts")
	}
	r := create(170000)
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	f := h.fromCreate(r.Data)
	f.publish()
	f.claim(fixtures.PrincipalB, 5)
	f.start()
	h.clock.Advance(5 * time.Second)
	h.maintain()
	h.state(f.id, "offered")
	if h.countEvent(f.id, "assignment.expired") != 1 {
		t.Fatal("large Task could not materialize mandatory expiry")
	}
}
