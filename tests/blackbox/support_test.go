package blackbox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"agent-task-bridge/tests/fixtures"
)

func TestPreparedReplayPreservesRequest(t *testing.T) {
	var bodies [][]byte
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer disposable" {
			t.Error("missing authentication")
		}
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"data": map[string]any{"task_id": "task_T"}, "meta": map[string]any{"replayed": len(bodies) > 1}}})
	}))
	defer server.Close()
	c := &Client{URL: server.URL, Token: "disposable", Authority: fixtures.Authority, Agent: fixtures.AgentB}
	params := fixtures.Create([]string{fixtures.AgentB}, "safe_reopen", true)
	p, err := c.Prepare("task.create", params)
	if err != nil {
		t.Fatal(err)
	}
	params["idempotency_key"] = fixtures.Key()
	params["contract"].(map[string]any)["goal"] = "changed after Prepare"
	first, err := c.Do(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Do(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || !second.Replayed || len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("prepared retries did not preserve request")
	}
	if bytes.Contains(bodies[0], []byte("changed after Prepare")) {
		t.Fatal("caller mutation changed prepared request")
	}
}

func TestGateOnlyPausesSelectedRequest(t *testing.T) {
	g := NewGate("before_command_tx", "run.submit", "key_A")
	g.Hook("before_command_tx", "run.submit", "key_other")
	finished := make(chan struct{})
	go func() { g.Hook("before_command_tx", "run.submit", "key_A"); close(finished) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := g.Await(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
		t.Fatal("gate did not pause request")
	default:
	}
	g.Release()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The same key can be replayed after the single controlled pause.
	g.Hook("before_command_tx", "run.submit", "key_A")
}
