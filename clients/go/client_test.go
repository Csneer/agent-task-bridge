package bridgeclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func golden(t *testing.T) map[string]any {
	t.Helper()
	raw, e := os.ReadFile("../../protocol/m1/golden-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		t.Fatal(e)
	}
	return v
}
func sample(t *testing.T, id string) map[string]any {
	t.Helper()
	for _, v := range golden(t)["schema_cases"].([]any) {
		c := v.(map[string]any)
		if c["id"] == id {
			return c["value"].(map[string]any)
		}
	}
	t.Fatal("missing fixture", id)
	return nil
}
func TestFrozenConformance(t *testing.T) {
	raw, e := os.ReadFile("../../protocol/m1/schema.json")
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(raw, schemaJSON) {
		t.Fatal("embedded schema drift")
	}
	g := golden(t)
	for _, v := range g["schema_cases"].([]any) {
		c := v.(map[string]any)
		t.Run(c["id"].(string), func(t *testing.T) {
			e := Validate(c["definition"].(string), c["value"])
			if (e == nil) != c["valid"].(bool) {
				t.Fatalf("validation=%v", e)
			}
		})
	}
	for _, v := range g["hash_cases"].([]any) {
		c := v.(map[string]any)
		t.Run(c["id"].(string), func(t *testing.T) {
			r := c["request"].(map[string]any)
			h, e := RequestHash(r["method"].(string), r["params"].(map[string]any))
			if e != nil || h != c["sha256"] {
				t.Fatalf("hash %s: %v", h, e)
			}
		})
	}
	for _, v := range g["parser_cases"].([]any) {
		c := v.(map[string]any)
		t.Run(c["id"].(string), func(t *testing.T) {
			_, e := Parse([]byte(c["json"].(string)))
			if (e == nil) != c["valid"].(bool) {
				t.Fatalf("parse=%v", e)
			}
		})
	}
}
func TestIndependentExactJSON(t *testing.T) {
	for _, s := range []string{`1.0000000000000001`, `9007199254740990.5`, `1e-400`, `9007199254740992`, `1e999999999999999999999`, `1e-999999999999999999999`, `{"x":1,"\u0078":2}`, `"\ud800"`, `"\udc00"`, `"\ud800\u0041"`, string([]byte{34, 255, 34})} {
		if _, e := Parse([]byte(s)); e == nil {
			t.Errorf("accepted %q", s)
		}
	}
	for in, want := range map[string]string{`1e0`: "1", `1.00e2`: "100", `-0`: "0", `0e999999999999999999999`: "0", `9007199254740991`: "9007199254740991", `1.000000000000000000000000`: "1"} {
		v, e := Parse([]byte(in))
		if e != nil {
			t.Fatal(e)
		}
		raw, e := Canonical(v)
		if e != nil || string(raw) != want {
			t.Errorf("%s => %s %v", in, raw, e)
		}
	}
	v, e := Parse([]byte(`{"\ue000":1,"😀":2,"x":"<>&\u2028\n"}`))
	if e != nil {
		t.Fatal(e)
	}
	raw, e := Canonical(v)
	if e != nil || string(raw) != "{\"x\":\"<>&\u2028\\n\",\"😀\":2,\"\ue000\":1}" {
		t.Fatalf("JCS: %s %v", raw, e)
	}
}
func TestUnknownRetryJournalAndRefusal(t *testing.T) {
	var calls atomic.Int32
	var sent [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("missing bearer")
		}
		body, _ := io.ReadAll(r.Body)
		v, _ := Parse(body)
		req := v.(map[string]any)
		if req["method"] == "bridge.capabilities" {
			response := sample(t, "G01_Result_Capabilities")
			response["id"] = req["id"]
			json.NewEncoder(w).Encode(response)
			return
		}
		sent = append(sent, body)
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(503)
			return
		}
		fixture := "G01_Error_STALE_REVISION"
		if n == 3 {
			fixture = "G01_Result_CommandResult"
		}
		response := sample(t, fixture)
		response["id"] = req["id"]
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	c, e := New(server.URL, "private-token", "", dir)
	if e != nil {
		t.Fatal(e)
	}
	p := sample(t, "G01_Request_task.publish")["params"].(map[string]any)
	out, e := c.Call(context.Background(), "task.publish", p)
	if !errors.Is(e, ErrUnknown) {
		t.Fatal(e)
	}
	key := out.Key
	// New client simulates process restart, without changing the original intent.
	c, e = New(server.URL, "private-token", "", dir)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Retry(context.Background(), key)
	var rejection *RPCError
	if !errors.As(e, &rejection) {
		t.Fatalf("wanted business refusal: %v", e)
	}
	if _, e = os.Stat(filepath.Join(dir, key+".unknown")); e != nil {
		t.Fatal("refusal cleared unknown")
	}
	altered := map[string]any{}
	for k, v := range p {
		altered[k] = v
	}
	altered["expected_revision"] = json.Number("99")
	if _, e = c.Call(context.Background(), "task.publish", altered); !errors.Is(e, ErrConflict) {
		t.Fatalf("allowed mutated intent %v", e)
	}
	out, e = c.Retry(context.Background(), key)
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Response) == 0 {
		t.Fatal("no receipt")
	}
	if len(sent) != 3 || !bytes.Equal(sent[0], sent[1]) || !bytes.Equal(sent[1], sent[2]) {
		t.Fatal("retry changed request")
	}
	for _, suffix := range []string{".request.json", ".unknown", ".success.json"} {
		info, e := os.Stat(filepath.Join(dir, key+suffix))
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private journal %s: %v", suffix, e)
		}
	}
}
func TestResourceDigestAndSupervision(t *testing.T) {
	raw := []byte("review input\n")
	sum := sha256.Sum256(raw)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credential forwarded")
		}
		w.Write(raw)
	}))
	defer server.Close()
	ref := map[string]any{"kind": "immutable", "uri": server.URL, "media_type": "text/plain", "version": "1", "sha256": hex.EncodeToString(sum[:])}
	got, e := ReadImmutable(context.Background(), ref, 1024)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatalf("resource %v", e)
	}
	ref["sha256"] = string(bytes.Repeat([]byte("0"), 64))
	if _, e = ReadImmutable(context.Background(), ref, 1024); e == nil {
		t.Fatal("digest mismatch accepted")
	}
	var fences atomic.Int32
	stopped := make(chan struct{})
	e = Supervise(context.Background(), time.Millisecond, func(context.Context) error {
		if fences.Add(1) > 1 {
			return ErrUnknown
		}
		return nil
	}, func(ctx context.Context) error { <-ctx.Done(); close(stopped); return ctx.Err() })
	if !errors.Is(e, ErrUnknown) {
		t.Fatal(e)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("work not cancelled")
	}
	var ran bool
	e = Supervise(context.Background(), time.Second, func(context.Context) error { return ErrUnknown }, func(context.Context) error { ran = true; return nil })
	if e == nil || ran {
		t.Fatal("work started without fence")
	}
}

func TestLeaseDeadlineCancelsWorkDuringStalledRenew(t *testing.T) {
	var count atomic.Int32
	stopped := make(chan struct{})
	started := time.Now()
	e := SuperviseLease(context.Background(), time.Millisecond, 0, func(ctx context.Context) (time.Time, error) {
		if count.Add(1) == 1 {
			return time.Now().Add(40 * time.Millisecond), nil
		}
		<-ctx.Done()
		return time.Time{}, ctx.Err()
	}, func(ctx context.Context) error { <-ctx.Done(); close(stopped); return ctx.Err() })
	if e == nil || time.Since(started) > time.Second {
		t.Fatalf("watchdog failed: %v", e)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("work survived deadline")
	}
}

func TestLargeLegalReplayResponse(t *testing.T) {
	response := sample(t, "G01_Result_ReplayResult")
	data := response["result"].(map[string]any)["data"].(map[string]any)
	chunks := make([]any, 28)
	for i := range chunks {
		chunks[i] = strings.Repeat("e", 8192)
	}
	batches := make([]any, 100)
	base := sample(t, "G01_CommitBatch")
	encoded, _ := Canonical(base)
	for i := range batches {
		v, e := Parse(encoded)
		if e != nil {
			t.Fatal(e)
		}
		batch := v.(map[string]any)
		batch["commit_id"] = fmt.Sprintf("commit_%d", i)
		for j, event := range batch["events"].([]any) {
			ev := event.(map[string]any)
			ev["event_id"] = fmt.Sprintf("event_%d_%d", i, j)
			ev["task_id"] = fmt.Sprintf("task_%d", i)
			record := ev["record"].(map[string]any)
			record["task_id"] = ev["task_id"]
			for _, field := range []string{"assignment_id", "run_id", "submission_id", "pending_submission_id"} {
				if value, ok := record[field]; ok && value != nil {
					record[field] = fmt.Sprintf("%s_%d", field, i)
				}
			}
			if ev["record_type"] == "Submission" {
				record["extensions"] = map[string]any{"org.example.analysis": chunks}
			}
			if ev["record_type"] == "Task" {
				record["contract"].(map[string]any)["extensions"] = map[string]any{"org.example.context": chunks}
				contractBytes, err := Canonical(record["contract"])
				if err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(contractBytes)
				record["contract_digest"] = hex.EncodeToString(digest[:])
			}
		}
		if e := Validate("CommitBatch", batch); e != nil {
			t.Fatal(e)
		}
		raw, e := Canonical(batch)
		if e != nil || len(raw) > 1048576 {
			t.Fatalf("batch limit %d %v", len(raw), e)
		}
		batches[i] = batch
	}
	data["batches"] = batches
	if e := Validate("SuccessResponse", response); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(response)
	if e != nil || len(raw) <= 40*1024*1024 {
		t.Fatalf("not a large valid response: %d %v", len(raw), e)
	}
	// Request ID is fixed here to avoid serializing the 44 MiB page per request.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	defer server.Close()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	c, e := New(server.URL, "token", "", dir)
	if e != nil {
		t.Fatal(e)
	}
	c.Authority = "authority_demo"
	request := map[string]any{"jsonrpc": "2.0", "id": response["id"], "method": "events.read", "params": map[string]any{"authority_id": "authority_demo", "cursor": "cursor_opaque", "limit": 100}}
	body, e := Canonical(request)
	if e != nil {
		t.Fatal(e)
	}
	out, e := c.send(context.Background(), "", body)
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Response) != len(raw) {
		t.Fatal("response truncated")
	}
}

func TestConcurrentDiscoveryAndCalls(t *testing.T) {
	capability := sample(t, "G01_Result_Capabilities")
	task := sample(t, "G01_Result_Task")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rpc" {
			t.Errorf("incorrect endpoint %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		v, e := Parse(raw)
		if e != nil {
			t.Error(e)
			return
		}
		request := v.(map[string]any)
		source := task
		if request["method"] == "bridge.capabilities" {
			source = capability
		}
		response := map[string]any{}
		for k, v := range source {
			response[k] = v
		}
		response["id"] = request["id"]
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	for _, suffix := range []string{"", "/rpc", "/rpc/"} {
		dir := t.TempDir()
		os.Chmod(dir, 0700)
		c, e := New(server.URL+suffix, "token", "", dir)
		if e != nil {
			t.Fatal(e)
		}
		var wg sync.WaitGroup
		for i := 0; i < 24; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if i%2 == 0 {
					if e := c.Discover(context.Background()); e != nil {
						t.Error(e)
					}
				} else {
					if _, e := c.Call(context.Background(), "task.get", map[string]any{"task_id": "task_T"}); e != nil {
						t.Error(e)
					}
				}
			}(i)
		}
		wg.Wait()
		if c.authorityID() != "authority_demo" {
			t.Fatal("authority not pinned")
		}
	}
}
