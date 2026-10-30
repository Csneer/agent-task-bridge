package blackbox

import (
	"context"
	"crypto/rand"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"agent-task-bridge/bridge"
	"agent-task-bridge/tests/fixtures"
)

func TestPeriodicMaintenanceExpiresWithoutClientWrites(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	h.server.Close()
	h.server = nil
	if err := h.service.Close(); err != nil {
		t.Fatal(err)
	}
	h.service = nil
	s, err := bridge.Open(h.path, nil, bridge.Options{Key: h.key, Clock: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	h.service = s
	h.server = httptest.NewServer(s.Handler())
	h.clock.Advance(time.Minute)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.read(f.id)["state"] == "offered" {
			if h.countEvent(f.id, "assignment.expired") != 1 {
				t.Fatal("periodic maintenance did not create exactly one expiry")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("periodic maintenance did not expire the lease within 5 seconds")
}

func TestBackwardClockFailsClosedWithHTTP503(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	before := h.read(f.id)
	p := f.reportParams()
	h.clock.Advance(-2 * time.Second)
	r := h.call(f.actor, "run.report", p)
	if r.Status != 503 || r.TransportCode != "UNAVAILABLE" {
		t.Fatalf("backward clock: HTTP=%d transport=%s rpc=%v", r.Status, r.TransportCode, r.Error)
	}
	h.clock.Advance(2 * time.Second)
	if !reflect.DeepEqual(before, h.read(f.id)) || h.countEvent(f.id, "run.reported") != 0 {
		t.Fatal("clock failure committed a report")
	}
	h.ok(f.actor, "run.report", p)
}

func TestSnapshotQuotaReclaimsCompletedAndInvalidContexts(t *testing.T) {
	h := newHarness(t)
	f := h.active("safe_reopen", true)
	first := h.ok(fixtures.Reader, "snapshot.open", map[string]any{"scope": map[string]any{}})
	h.ok(fixtures.Reader, "snapshot.open", map[string]any{"scope": map[string]any{}})
	h.kind(h.call(fixtures.Reader, "snapshot.open", map[string]any{"scope": map[string]any{}}), "RESOURCE_LIMIT")
	page := h.ok(fixtures.Reader, "snapshot.page", map[string]any{"snapshot_id": first["snapshot_id"], "page_cursor": first["page_cursor"], "limit": 100})
	if page["complete"] != true {
		t.Fatal("quota fixture should complete in one page")
	}
	h.ok(fixtures.Reader, "snapshot.open", map[string]any{"scope": map[string]any{}})
	// Configuration import invalidates prior contexts. New opens must reclaim them.
	h.cfg.OwnerCapabilities[0].ParentTaskIDs = []string{f.id}
	h.importConfig()
	h.ok(fixtures.Reader, "snapshot.open", map[string]any{"scope": map[string]any{}})
}

func TestLostReceiptKeyCannotReexecuteCommittedClaim(t *testing.T) {
	h := newHarness(t)
	f := h.create([]string{fixtures.AgentB}, "safe_reopen", true)
	f.publish()
	p := f.params()
	p["claim_grant_id"] = f.grants[fixtures.PrincipalB]
	p["lease_seconds"] = 30
	client := h.client(fixtures.PrincipalB)
	request, err := client.Prepare("task.claim", p)
	if err != nil {
		t.Fatal(err)
	}
	original, err := client.Do(context.Background(), request)
	if err != nil || original.Error != nil {
		t.Fatalf("claim failed: %v %v", err, original.Error)
	}
	before := h.read(f.id)
	h.server.Close()
	h.server = nil
	if err := h.service.Close(); err != nil {
		t.Fatal(err)
	}
	h.service = nil
	if _, err := rand.Read(h.key); err != nil {
		t.Fatal(err)
	}
	h.open(nil)
	client.URL = h.server.URL
	replayed, err := client.Do(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	h.kind(replayed, "RECEIPT_UNAVAILABLE")
	if !reflect.DeepEqual(before, h.read(f.id)) || h.countEvent(f.id, "assignment.claimed") != 1 {
		t.Fatal("receipt decryption failure reexecuted claim")
	}
}
