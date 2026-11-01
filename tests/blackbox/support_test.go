package blackbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Csneer/agent-task-bridge/tests/fixtures"
)

type responseTransport struct{ body io.Reader }

func (r responseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(r.body)}, nil
}

func TestResponseTermination(t *testing.T) {
	const envelope = `{"jsonrpc":"2.0","id":"request_1","result":{"data":{},"meta":{"replayed":false}}}`
	for _, tc := range []struct {
		name     string
		tail     io.Reader
		want     error
		trailing bool
	}{
		{name: "complete", tail: strings.NewReader("\n")},
		{name: "second value", tail: strings.NewReader("\n{}"), trailing: true},
		{name: "deadline after envelope", tail: iotest.ErrReader(context.DeadlineExceeded), want: context.DeadlineExceeded},
		{name: "truncated transport", tail: iotest.ErrReader(io.ErrUnexpectedEOF), want: io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{URL: "http://example.test", HTTP: &http.Client{Transport: responseTransport{body: io.MultiReader(strings.NewReader(envelope), tc.tail)}}}
			_, err := c.Call(context.Background(), "bridge.capabilities", map[string]any{})
			if tc.trailing {
				if err == nil || err.Error() != "response contains trailing JSON" {
					t.Fatalf("want trailing JSON rejection, got %v", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

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
