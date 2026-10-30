package blackbox

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Csneer/agent-task-bridge/tests/fixtures"
)

func TestV08ReadOnlySnapshotIncludesEveryLifecycle(t *testing.T) {
	h := newHarness(t)
	expected := map[string]string{}
	for _, state := range []string{"offered", "submitted", "accepted", "closed"} {
		f := h.create([]string{fixtures.AgentB}, "safe_reopen", true)
		f.publish()
		if state != "offered" {
			f.claim(fixtures.PrincipalB, 30)
			f.start()
			f.submit()
		}
		if state == "accepted" || state == "closed" {
			f.decide("accept")
		}
		if state == "closed" {
			f.control("task.close")
		}
		expected[f.id] = state
	}
	private := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
	var grants []any
	for _, raw := range private["initial_grants"].([]any) {
		if object(raw)["subject_principal_id"] != fixtures.Reader {
			grants = append(grants, raw)
		}
	}
	private["initial_grants"] = grants
	hidden := h.fromCreate(h.ok(fixtures.Owner, "task.create", private))
	hidden.publish()
	seen := map[string]string{}
	for _, r := range h.allSnapshot(fixtures.Reader) {
		if r["record_type"] == "Task" {
			task := object(r["record"])
			seen[str(task["task_id"])] = str(task["state"])
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("read-only snapshot tasks=%d want %d", len(seen), len(expected))
	}
	for id, state := range expected {
		if seen[id] != state {
			t.Fatal("read-only snapshot omitted lifecycle")
		}
	}
	h.kind(h.call(fixtures.Reader, "task.get", map[string]any{"task_id": hidden.id}), "NOT_FOUND")
	h.kind(h.call(fixtures.Reader, "task.discover", map[string]any{"as_agent_id": fixtures.AgentB, "limit": 100}), "FORBIDDEN")
}

func TestV09SnapshotAndReplayDoNotLoseConcurrentSubmit(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	open := h.ok(fixtures.Reader, "snapshot.open", map[string]any{"scope": map[string]any{}})
	page := h.ok(fixtures.Reader, "snapshot.page", map[string]any{"snapshot_id": open["snapshot_id"], "page_cursor": open["page_cursor"], "limit": 1})
	if page["complete"] == true {
		t.Fatal("fixture did not force pagination")
	}
	var records []map[string]any
	for _, r := range list(page["records"]) {
		records = append(records, object(r))
	}
	f.submit()
	for i := 0; i < 100; i++ {
		page = h.ok(fixtures.Reader, "snapshot.page", map[string]any{"snapshot_id": open["snapshot_id"], "page_cursor": page["next_page_cursor"], "limit": 1})
		for _, r := range list(page["records"]) {
			records = append(records, object(r))
		}
		if page["complete"] == true {
			break
		}
		if i == 99 {
			t.Fatal("snapshot never completed")
		}
	}
	found := false
	for _, r := range records {
		v := object(r["record"])
		if r["record_type"] == "Task" && v["task_id"] == f.id {
			found = true
			if v["state"] != "active" {
				t.Fatal("snapshot mixed concurrent state")
			}
		}
		if r["record_type"] == "Submission" {
			t.Fatal("snapshot included post-watermark submission")
		}
	}
	if !found {
		t.Fatal("snapshot omitted task")
	}
	replay := h.ok(fixtures.Reader, "events.read", map[string]any{"cursor": open["replay_cursor"], "limit": 100})
	found = false
	for _, raw := range list(replay["batches"]) {
		batch := object(raw)
		events := list(batch["events"])
		if object(batch["audit"])["action"] == "run.submit" {
			if len(events) != 4 {
				t.Fatal("submit batch was split")
			}
			for _, e := range events {
				event := object(e)
				if event["record_type"] == "Task" {
					if object(event["record"])["state"] != "submitted" {
						t.Fatal("wrong post-watermark task")
					}
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("post-watermark submission lost")
	}
	again := h.ok(fixtures.Reader, "events.read", map[string]any{"cursor": open["replay_cursor"], "limit": 100})
	b1, b2 := list(replay["batches"]), list(again["batches"])
	if len(b1) != len(b2) || object(b1[0])["commit_id"] != object(b2[0])["commit_id"] {
		t.Fatal("cursor replay changed stable event identity")
	}
}

func TestV10CursorContinuationAndInvalidation(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	open := h.ok(fixtures.Reader, "events.open", map[string]any{"scope": map[string]any{}})
	head := h.ok(fixtures.Reader, "events.read", map[string]any{"cursor": open["cursor"], "limit": 100})
	if head["at_head"] != true {
		t.Fatal("fixture replay did not reach head")
	}
	h.ok(f.actor, "run.report", f.reportParams())
	next := h.ok(fixtures.Reader, "events.read", map[string]any{"cursor": head["next_cursor"], "limit": 100})
	if len(list(next["batches"])) != 1 {
		t.Fatal("head cursor could not read later event")
	}
	h.kind(h.call(fixtures.Owner, "events.read", map[string]any{"cursor": next["next_cursor"], "limit": 100}), "INVALID_CURSOR")
	h.kind(h.call(fixtures.Reader, "events.read", map[string]any{"cursor": "clearly_invalid_cursor_123456789", "limit": 100}), "INVALID_CURSOR")
	snapshot := h.ok(fixtures.Reader, "snapshot.open", map[string]any{"scope": map[string]any{}})
	p := f.params()
	for k, v := range fixtures.Grant(fixtures.Reader, nil, []string{"task.read"}, fixtures.StartTime.Add(12*time.Hour)) {
		p[k] = v
	}
	h.ok(fixtures.Owner, "grant.issue", p)
	h.kind(h.call(fixtures.Reader, "events.read", map[string]any{"cursor": next["next_cursor"], "limit": 100}), "REINITIALIZE_REQUIRED")
	h.kind(h.call(fixtures.Reader, "snapshot.page", map[string]any{"snapshot_id": snapshot["snapshot_id"], "page_cursor": snapshot["page_cursor"], "limit": 100}), "REINITIALIZE_REQUIRED")
	fresh := h.ok(fixtures.Reader, "snapshot.open", map[string]any{"scope": map[string]any{}})
	h.clock.Advance(301 * time.Second)
	h.kind(h.call(fixtures.Reader, "snapshot.page", map[string]any{"snapshot_id": fresh["snapshot_id"], "page_cursor": fresh["page_cursor"], "limit": 100}), "REINITIALIZE_REQUIRED")
	// Expired contexts must not exhaust the principal's active snapshot quota.
	h.allSnapshot(fixtures.Reader)
}

func TestV25HistoricalAuditAndStaleReadAreDistinct(t *testing.T) {
	h := newHarness(t)
	f := h.active("manual_reopen", true)
	p := f.execution()
	p["reason"] = "Release candidate work"
	h.ok(f.actor, "assignment.release", p)
	f.control("task.reopen")
	p = f.params()
	p["reason"] = "Owner cancels after review"
	client := h.client(fixtures.Owner)
	first, err := client.Prepare("task.cancel", p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := client.Do(context.Background(), first)
	if err != nil || r.Error != nil {
		t.Fatal("cancel failed")
	}
	second, err := client.Prepare("task.cancel", p)
	if err != nil {
		t.Fatal(err)
	}
	r, err = client.Do(context.Background(), second)
	if err != nil || r.Error != nil || !r.Replayed {
		t.Fatal("control receipt did not replay")
	}
	h.allSnapshot(fixtures.Reader)
	history := h.ok(fixtures.Reader, "events.open", map[string]any{"scope": map[string]any{}})
	page := h.ok(fixtures.Reader, "events.read", map[string]any{"cursor": history["cursor"], "limit": 100})
	found := false
	for _, raw := range list(page["batches"]) {
		b := object(raw)
		audit := object(b["audit"])
		if audit["action"] == "task.cancel" {
			found = true
			if audit["request_id"] != first.ID || audit["reason"] != "Owner cancels after review" {
				t.Fatal("control audit omitted original identity/reason")
			}
		}
	}
	if !found || h.countEvent(f.id, "task.cancelled") != 1 {
		t.Fatal("historical control event missing/duplicated")
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(str(f.lease["token"]))) {
		t.Fatal("event history leaked lease token")
	}
	for _, token := range h.tokens {
		if bytes.Contains(encoded, []byte(token)) {
			t.Fatal("event history leaked credential")
		}
	}
	other := h.active("safe_reopen", true)
	h.clock.Advance(30 * time.Second)
	h.state(other.id, "active")
	h.kind(h.call(other.actor, "run.report", other.reportParams()), "STALE_REVISION")
	h.kind(h.call(other.actor, "run.report", other.reportParams()), "LEASE_FENCED")
}
