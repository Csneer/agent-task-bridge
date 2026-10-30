package blackbox

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Csneer/agent-task-bridge/bridge"
	"github.com/Csneer/agent-task-bridge/tests/fixtures"
)

type harness struct {
	t          *testing.T
	service    *bridge.Service
	server     *httptest.Server
	path       string
	key        []byte
	cfg        bridge.Config
	tokens     map[string]string
	clock      *Clock
	mu         sync.RWMutex
	hook       func(bridge.HookEvent) error
	evidenceMu sync.Mutex
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg, tokens := fixtures.Bootstrap()
	h := &harness{t: t, cfg: cfg, tokens: tokens, path: filepath.Join(t.TempDir(), "bridge.sqlite"), key: make([]byte, 32), clock: NewClock(fixtures.StartTime)}
	if _, err := rand.Read(h.key); err != nil {
		t.Fatal(err)
	}
	h.open(&h.cfg)
	t.Cleanup(func() {
		if h.server != nil {
			h.server.Close()
		}
		if h.service != nil {
			if err := h.service.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	return h
}

func (h *harness) open(cfg *bridge.Config) {
	h.t.Helper()
	s, err := bridge.Open(h.path, cfg, bridge.Options{Key: h.key, Clock: h.clock.Now, DisableMaintenance: true, Hook: func(e bridge.HookEvent) error {
		h.evidence(map[string]any{"kind": "schedule", "point": e.Point, "method": e.Method, "key": e.Key})
		h.mu.RLock()
		hook := h.hook
		h.mu.RUnlock()
		if hook != nil {
			return hook(e)
		}
		return nil
	}})
	if err != nil {
		h.t.Fatal(err)
	}
	h.service = s
	h.server = httptest.NewServer(s.Handler())
}
func (h *harness) restart() {
	h.t.Helper()
	h.server.Close()
	if err := h.service.Close(); err != nil {
		h.t.Fatal(err)
	}
	h.open(nil)
}
func (h *harness) setHook(hook func(bridge.HookEvent) error) {
	h.mu.Lock()
	h.hook = hook
	h.mu.Unlock()
}
func (h *harness) importConfig() {
	h.t.Helper()
	if err := h.service.ImportConfig(context.Background(), h.cfg); err != nil {
		h.t.Fatal(err)
	}
}
func (h *harness) maintain() {
	h.t.Helper()
	if err := h.service.Maintain(context.Background()); err != nil {
		h.t.Fatal(err)
	}
}
func (h *harness) client(principal string) *Client {
	c := &Client{URL: h.server.URL, Authority: fixtures.Authority, Token: h.tokens[principal]}
	c.Observe = func(p Prepared, r Response, err error) {
		var request any
		_ = json.Unmarshal(p.body, &request)
		entry := map[string]any{"kind": "rpc", "principal": principal, "request": request, "response": r}
		if err != nil {
			entry["error"] = err.Error()
		}
		h.evidence(entry)
	}
	if principal == fixtures.PrincipalA {
		c.Agent = fixtures.AgentA
	}
	if principal == fixtures.PrincipalB {
		c.Agent = fixtures.AgentB
	}
	return c
}
func (h *harness) call(principal, method string, p map[string]any) Response {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := h.client(principal).Call(ctx, method, p)
	if err != nil {
		h.t.Fatalf("%s transport: %v", method, err)
	}
	return r
}
func (h *harness) ok(principal, method string, p map[string]any) map[string]any {
	h.t.Helper()
	r := h.call(principal, method, p)
	if r.Status != 200 || r.Error != nil || r.TransportCode != "" {
		h.t.Fatalf("%s failed: HTTP=%d rpc=%v transport=%s", method, r.Status, r.Error, r.TransportCode)
	}
	return r.Data
}
func (h *harness) kind(r Response, want string) {
	h.t.Helper()
	if r.Status != 200 || r.Error == nil || r.Error.Data.Kind != want {
		h.t.Fatalf("want %s; HTTP=%d error=%v transport=%s", want, r.Status, r.Error, r.TransportCode)
	}
}
func (h *harness) read(id string) map[string]any {
	return h.ok(fixtures.Owner, "task.get", map[string]any{"task_id": id})
}
func (h *harness) record(taskID, kind, id string) map[string]any {
	return object(h.ok(fixtures.Owner, "record.get", map[string]any{"task_id": taskID, "record_type": kind, "record_id": id})["record"])
}
func object(v any) map[string]any {
	if v == nil {
		return nil
	}
	return v.(map[string]any)
}
func list(v any) []any {
	if v == nil {
		return nil
	}
	return v.([]any)
}
func str(v any) string {
	if v == nil {
		return ""
	}
	return v.(string)
}
func number(v any) int64 {
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return i
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		panic(fmt.Sprintf("not a number: %T", v))
	}
}
func (h *harness) state(id, want string) {
	h.t.Helper()
	if got := str(h.read(id)["state"]); got != want {
		h.t.Fatalf("task state=%s want %s", got, want)
	}
}

type taskFixture struct {
	h                           *harness
	id                          string
	grants                      map[string]string
	assignment, run, submission string
	lease                       map[string]any
	actor                       string
}

func (h *harness) create(eligible []string, recovery string, rework bool) *taskFixture {
	h.t.Helper()
	return h.fromCreate(h.ok(fixtures.Owner, "task.create", fixtures.Create(eligible, recovery, rework)))
}
func (h *harness) fromCreate(result map[string]any) *taskFixture {
	task := object(result["task"])
	f := &taskFixture{h: h, id: str(task["task_id"]), grants: map[string]string{}}
	for _, raw := range list(result["records"]) {
		r := object(raw)
		if r["record_type"] != "Grant" {
			continue
		}
		g := object(r["record"])
		if str(g["agent_id"]) != "" {
			f.grants[str(g["subject_principal_id"])] = str(g["grant_id"])
		}
	}
	return f
}
func (f *taskFixture) params() map[string]any {
	return map[string]any{"task_id": f.id, "expected_revision": f.h.read(f.id)["revision"], "idempotency_key": fixtures.Key()}
}
func (f *taskFixture) publish() { f.h.ok(fixtures.Owner, "task.publish", f.params()) }
func (f *taskFixture) claim(actor string, seconds int) {
	f.actor = actor
	p := f.params()
	p["claim_grant_id"] = f.grants[actor]
	p["lease_seconds"] = seconds
	d := f.h.ok(actor, "task.claim", p)
	for _, raw := range list(d["records"]) {
		r := object(raw)
		if r["record_type"] == "Assignment" {
			a := object(r["record"])
			f.assignment = str(a["assignment_id"])
			f.lease = map[string]any{"assignment_id": f.assignment, "generation": a["generation"], "token": d["lease_token"]}
		}
	}
	if f.assignment == "" {
		f.h.t.Fatal("claim returned no Assignment")
	}
}
func (f *taskFixture) execution() map[string]any { p := f.params(); p["lease"] = f.lease; return p }
func (f *taskFixture) start() {
	d := f.h.ok(f.actor, "run.start", f.execution())
	for _, raw := range list(d["records"]) {
		r := object(raw)
		if r["record_type"] == "Run" {
			f.run = str(object(r["record"])["run_id"])
		}
	}
	if f.run == "" {
		f.h.t.Fatal("start returned no Run")
	}
}
func (f *taskFixture) reportParams() map[string]any {
	p := f.execution()
	p["run_id"] = f.run
	p["message"] = "Reviewed the fixed input"
	p["evidence_refs"] = []any{}
	p["extensions"] = map[string]any{}
	return p
}
func (f *taskFixture) submitParams() map[string]any {
	p := f.execution()
	p["run_id"] = f.run
	p["content_version"] = 1
	p["summary"] = "Candidate review delivered"
	p["artifact_refs"] = []any{fixtures.ImmutableRef("https://example.invalid/review-v1.txt", "text/plain", []byte("candidate review\n"))}
	p["evidence_refs"] = []any{}
	p["extensions"] = map[string]any{}
	return p
}
func (f *taskFixture) submit() {
	d := f.h.ok(f.actor, "run.submit", f.submitParams())
	f.submission = str(object(d["task"])["pending_submission_id"])
	if f.submission == "" {
		f.h.t.Fatal("submit returned no Submission")
	}
}
func (f *taskFixture) decide(outcome string) map[string]any {
	p := f.params()
	p["submission_id"] = f.submission
	p["reason"] = "Verified fixed candidate against acceptance"
	p["evidence_refs"] = []any{}
	return f.h.ok(fixtures.Acceptance, "task."+outcome, p)
}
func (f *taskFixture) control(method string) map[string]any {
	p := f.params()
	p["reason"] = "Explicit owner decision"
	return f.h.ok(fixtures.Owner, method, p)
}
func (h *harness) active(recovery string, rework bool) *taskFixture {
	f := h.create([]string{fixtures.AgentA, fixtures.AgentB}, recovery, rework)
	f.publish()
	f.claim(fixtures.PrincipalB, 30)
	f.start()
	return f
}

func (h *harness) allEvents() []map[string]any {
	h.t.Helper()
	d := h.ok(fixtures.Owner, "events.open", map[string]any{"scope": map[string]any{}})
	cursor := d["cursor"]
	var events []map[string]any
	for i := 0; i < 100; i++ {
		page := h.ok(fixtures.Owner, "events.read", map[string]any{"cursor": cursor, "limit": 100})
		for _, b := range list(page["batches"]) {
			for _, raw := range list(object(b)["events"]) {
				events = append(events, object(raw))
			}
		}
		if page["at_head"] == true {
			return events
		}
		cursor = page["next_cursor"]
	}
	h.t.Fatal("event replay did not reach head")
	return nil
}
func (h *harness) countEvent(taskID, eventType string) int {
	n := 0
	for _, e := range h.allEvents() {
		if e["task_id"] == taskID && e["type"] == eventType {
			n++
		}
	}
	return n
}
func (h *harness) allSnapshot(principal string) []map[string]any {
	h.t.Helper()
	open := h.ok(principal, "snapshot.open", map[string]any{"scope": map[string]any{}})
	cursor := open["page_cursor"]
	var records []map[string]any
	for i := 0; i < 1000; i++ {
		p := h.ok(principal, "snapshot.page", map[string]any{"snapshot_id": open["snapshot_id"], "page_cursor": cursor, "limit": 100})
		for _, r := range list(p["records"]) {
			records = append(records, object(r))
		}
		if p["complete"] == true {
			return records
		}
		cursor = p["next_page_cursor"]
	}
	h.t.Fatal("snapshot did not complete")
	return nil
}
