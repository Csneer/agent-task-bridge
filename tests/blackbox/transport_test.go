package blackbox

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"agent-task-bridge/tests/fixtures"
)

func TestHTTPBindingRejectsUnsupportedShapesWithoutMutation(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name, method, contentType, body, token string
		status                                 int
	}{
		{"batch", http.MethodPost, "application/json", `[{"jsonrpc":"2.0","id":"x","method":"task.create","params":{}}]`, h.tokens[fixtures.Owner], 400},
		{"notification", http.MethodPost, "application/json", `{"jsonrpc":"2.0","method":"task.create","params":{}}`, h.tokens[fixtures.Owner], 204},
		{"wrong_method", http.MethodGet, "application/json", `{}`, h.tokens[fixtures.Owner], 405},
		{"wrong_type", http.MethodPost, "text/plain", `{}`, h.tokens[fixtures.Owner], 415},
		{"too_large", http.MethodPost, "application/json", strings.Repeat(" ", 262145), h.tokens[fixtures.Owner], 413},
		{"authentication_first", http.MethodPost, "application/json", `not json`, "", 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, h.server.URL+"/rpc", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", tc.contentType)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.status {
				t.Fatalf("HTTP status=%d want %d", res.StatusCode, tc.status)
			}
			if tc.status == 204 && len(body) != 0 {
				t.Fatal("notification received response body")
			}
		})
	}
	if len(h.allSnapshot(fixtures.Owner)) != 0 {
		t.Fatal("rejected transport request changed public facts")
	}
}

func TestHTTPRawJSONProfileAndErrorShapes(t *testing.T) {
	h := newHarness(t)
	for _, raw := range []string{
		`{"jsonrpc":"2.0","id":"x","method":"bridge.capabilities","params":{},"params":{}}`,
		`{"jsonrpc":"2.0","id":"\ud800","method":"bridge.capabilities","params":{}}`,
		`{"jsonrpc":"2.0","id":"x","method":"task.discover","params":{"authority_id":"authority_test","as_agent_id":"agent_B","limit":1.0000000000000001}}`,
		`{"jsonrpc":"2.0","id":"x","method":"task.discover","params":{"authority_id":"authority_test","as_agent_id":"agent_B","limit":9007199254740990.5}}`,
		`{"jsonrpc":"2.0","id":"x","method":"task.discover","params":{"authority_id":"authority_test","as_agent_id":"agent_B","limit":1e-400}}`,
	} {
		req, err := http.NewRequest(http.MethodPost, h.server.URL+"/rpc", strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+h.tokens[fixtures.PrincipalB])
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var envelope map[string]any
		err = json.NewDecoder(res.Body).Decode(&envelope)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 || envelope["error"] == nil || envelope["result"] != nil {
			t.Fatal("invalid raw JSON profile did not return RPC error")
		}
	}
	capability := h.ok(fixtures.Owner, "bridge.capabilities", map[string]any{})
	if capability["protocol_version"] != "bridge-m1-1" || len(list(capability["methods"])) != 27 {
		t.Fatal("capabilities differ from frozen profile")
	}
	p := map[string]any{"task_id": "task_missing", "authority_id": "other_authority"}
	h.kind(h.call(fixtures.Owner, "task.get", p), "AUTHORITY_MISMATCH")
	r := h.call(fixtures.Owner, "unknown.method", map[string]any{})
	if r.Error == nil || r.Error.Code != -32601 {
		t.Fatal("unknown method missing standard error")
	}
	// No credentials or lease token may appear in capabilities or ordinary query data.
	encoded, _ := json.Marshal(capability)
	for _, token := range h.tokens {
		if bytes.Contains(encoded, []byte(token)) {
			t.Fatal("capabilities leaked authentication credential")
		}
	}
}
