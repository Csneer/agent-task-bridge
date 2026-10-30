package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Csneer/agent-task-bridge/bridge"
	"github.com/Csneer/agent-task-bridge/tests/fixtures"
)

func TestHTTPObservationsBoundedAndPanicIsolated(t *testing.T) {
	cfg, tokens := fixtures.Bootstrap()
	observations := make(chan bridge.HTTPObservation, 100)
	s, err := bridge.Open(filepath.Join(t.TempDir(), "bridge.sqlite"), &cfg, bridge.Options{
		Key: make([]byte, 32), DisableMaintenance: true, Clock: func() time.Time { return fixtures.StartTime },
		ObserveHTTP: func(o bridge.HTTPObservation) { observations <- o; panic("observer-private-secret") },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	secret := "private-request-secret"
	cases := []struct {
		path, method, body, token, observedMethod, kind string
		status                                          int
	}{
		{"/rpc", "POST", `{"jsonrpc":"2.0","id":"` + secret + `","method":"bridge.capabilities","params":{}}`, tokens[fixtures.Owner], "bridge.capabilities", "", 200},
		{"/rpc", "POST", `{"jsonrpc":"2.0","id":"` + secret + `","method":"` + secret + `","params":{"reason":"` + secret + `"}}`, tokens[fixtures.Owner], "unknown", "METHOD_NOT_FOUND", 200},
		{"/rpc", "POST", `{"jsonrpc":"2.0","id":"` + secret + `","method":"task.get","params":{"token":"` + secret + `"}}`, tokens[fixtures.Owner], "task.get", "INVALID_PARAMS", 200},
		{"/" + secret, "POST", secret, tokens[fixtures.Owner], "unknown", "NOT_FOUND", 404},
		{"/rpc", "POST", secret, secret, "unknown", "UNAUTHENTICATED", 401},
		{"/rpc", "POST", secret, tokens[fixtures.Owner], "unknown", "PARSE_ERROR", 200},
		{"/rpc", "PRIVATEVERB", secret, tokens[fixtures.Owner], "unknown", "TRANSPORT_REJECTED", 405},
	}
	for _, tc := range cases {
		req, err := http.NewRequest(tc.method, server.URL+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tc.token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("status %d body %s", response.StatusCode, body)
		}
		if tc.kind == "" && !strings.Contains(string(body), `"result"`) {
			t.Fatalf("successful response changed: %s", body)
		}
		if tc.status == 200 && tc.kind != "" && !strings.Contains(string(body), `"kind":"`+tc.kind+`"`) {
			t.Fatalf("RPC error response changed: %s", body)
		}
		select {
		case o := <-observations:
			if o.Method != tc.observedMethod || o.Status != tc.status || o.ErrorKind != tc.kind || o.Duration <= 0 {
				t.Fatalf("observation %+v", o)
			}
			encoded, _ := json.Marshal(o)
			if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), tokens[fixtures.Owner]) {
				t.Fatalf("leaked observation %s", encoded)
			}
		case <-time.After(time.Second):
			t.Fatal("missing observation")
		}
	}
	response, err := client.Get(server.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	select {
	case o := <-observations:
		t.Fatalf("readiness observed %+v", o)
	default:
	}
	stats := s.Stats()
	if stats.TotalRequests != uint64(len(cases)) || stats.Errors != uint64(len(cases)-1) {
		t.Fatal(stats)
	}
}

func TestMaintenanceObservationsAfterUnlockAndPanicIsolated(t *testing.T) {
	cfg, _ := fixtures.Bootstrap()
	observations := make(chan bridge.MaintenanceObservation, 10)
	var service *bridge.Service
	service, err := bridge.Open(filepath.Join(t.TempDir(), "bridge.sqlite"), &cfg, bridge.Options{
		Key: make([]byte, 32), DisableMaintenance: true, Clock: func() time.Time { return fixtures.StartTime },
		ObserveMaintenance: func(o bridge.MaintenanceObservation) {
			// A fresh write transaction must succeed from a completed call's observer.
			if service != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := service.ImportConfig(ctx, cfg); err != nil {
					o.ErrorKind = "LOCK_NOT_RELEASED"
				}
			}
			observations <- o
			panic("observer failure")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if o := <-observations; o.ErrorKind != "" || o.Duration <= 0 {
		t.Fatal(o)
	}
	if err := service.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if o := <-observations; o.ErrorKind != "" || o.Duration <= 0 {
		t.Fatal(o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Maintain(ctx); err == nil {
		t.Fatal("canceled maintenance succeeded")
	}
	if o := <-observations; o.ErrorKind != "UNAVAILABLE" || o.Duration <= 0 {
		t.Fatal(o)
	}
	if stats := service.Stats(); stats.MaintenanceRuns != 3 || stats.MaintenanceFailures != 1 {
		t.Fatal(stats)
	}
}

func TestConcurrentObservationCounters(t *testing.T) {
	cfg, tokens := fixtures.Bootstrap()
	var service *bridge.Service
	service, err := bridge.Open(filepath.Join(t.TempDir(), "bridge.sqlite"), &cfg, bridge.Options{
		Key: make([]byte, 32), DisableMaintenance: true, Clock: func() time.Time { return fixtures.StartTime },
		ObserveHTTP: func(o bridge.HTTPObservation) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := service.ImportConfig(ctx, cfg); err != nil {
				t.Errorf("HTTP observer reentry: %v", err)
			}
			_ = service.Stats()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, _ := http.NewRequest("POST", server.URL+"/rpc", strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":"%d","method":"bridge.capabilities","params":{}}`, i)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+tokens[fixtures.Owner])
			response, err := client.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Error(response.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	if stats := service.Stats(); stats.TotalRequests != 12 || stats.Errors != 0 {
		t.Fatal(stats)
	}
}
