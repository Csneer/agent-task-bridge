package blackbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Evidence is opt-in and contains only generated fixture data. Secrets are
// removed recursively before bytes are written; headers are never collected.
func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, value := range x {
			lower := strings.ToLower(k)
			if lower == "token" || lower == "lease_token" || lower == "authorization" || lower == "token_hash" {
				out[k] = "[redacted]"
			} else {
				out[k] = redact(value)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = redact(value)
		}
		return out
	default:
		return v
	}
}

func (h *harness) evidence(entry map[string]any) {
	dir := os.Getenv("BRIDGE_TEST_EVIDENCE")
	if dir == "" {
		return
	}
	// Large capacity fixtures have their assertions in the test report; the
	// per-request archive is for the 25 normative behavioral vectors.
	if !strings.HasPrefix(h.t.Name(), "TestV") {
		return
	}
	entry["test"] = h.t.Name()
	entry["protocol_version"] = "bridge-m1-1"
	entry["protocol_checksum"] = "558dc29110b0138c31a36b0f19dbb9f34677d23d5508e5c9751afc4c7cd20a13"
	entry["implementation_commit"] = os.Getenv("BRIDGE_TEST_REVISION")
	entry["fixture_time"] = h.clock.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	// Round-trip typed Response so nested record fields are also redacted.
	raw, err := json.Marshal(entry)
	if err != nil {
		h.t.Error(err)
		return
	}
	var v any
	if err = json.Unmarshal(raw, &v); err != nil {
		h.t.Error(err)
		return
	}
	raw, err = json.Marshal(redact(v))
	if err != nil {
		h.t.Error(err)
		return
	}
	h.evidenceMu.Lock()
	defer h.evidenceMu.Unlock()
	if err = os.MkdirAll(dir, 0700); err != nil {
		h.t.Error(err)
		return
	}
	name := strings.NewReplacer("/", "__", "\\", "__").Replace(h.t.Name()) + ".jsonl"
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		h.t.Error(err)
		return
	}
	_, err = f.Write(append(raw, '\n'))
	f.Close()
	if err != nil {
		h.t.Error(err)
	}
}
